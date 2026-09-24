package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/auth"
	"github.com/GabrielHollberg/soundstorm/internal/library"
	"github.com/GabrielHollberg/soundstorm/internal/provision"
	"github.com/GabrielHollberg/soundstorm/internal/source"
	"github.com/GabrielHollberg/soundstorm/internal/state"
)

// remoteState is a tiny stand-in for what the TLS server and state store give
// the handlers: whether remote access can be offered at all, whether it is on,
// the address it would be reached at, and a place to record a change.
type remoteState struct {
	available bool
	enabled   bool
	name      string
	setErr    error // when non-nil, SetRemoteAccess fails
	sets      int   // how many times the toggle was actually applied
}

// newRemoteHarness builds a server with the remote-access funcs wired to rs.
// It mirrors newHarness but for the one thing that harness leaves nil.
func newRemoteHarness(t *testing.T, rs *remoteState) *harness {
	t.Helper()

	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	reg := source.NewRegistry()

	libRoot := filepath.Join(t.TempDir(), "library")
	lib, err := library.Open(libRoot, "./library", log)
	if err != nil {
		t.Fatalf("library.Open: %v", err)
	}

	api := New(Config{
		Registry:         reg,
		Store:            store,
		Library:          lib,
		Auth:             auth.New(store),
		Setup:            provision.New(store, reg, log, nil),
		PerSourceTimeout: time.Second,
		Log:              log,
		SetupCode:        testSetupCode,
		RemoteStatus: func() (bool, bool, string) {
			return rs.available, rs.enabled, rs.name
		},
		SetRemoteAccess: func(on bool) error {
			if rs.setErr != nil {
				return rs.setErr
			}
			rs.enabled = on
			rs.sets++
			return nil
		},
	})

	srv := httptest.NewServer(api.Routes())
	t.Cleanup(srv.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	return &harness{srv: srv, client: &http.Client{Jar: jar}, root: libRoot, api: api}
}

// The session tells a signed-in owner whether remote access is available, on,
// and where it can be reached - which is what the account panel paints.
func TestSessionCarriesRemoteStatus(t *testing.T) {
	rs := &remoteState{available: true, enabled: true, name: "abc.net.soundstorm.dev"}
	h := newRemoteHarness(t, rs)
	h.signUp(t)

	var out struct {
		Remote *struct {
			Available bool   `json:"available"`
			Enabled   bool   `json:"enabled"`
			Name      string `json:"name"`
		} `json:"remote"`
	}
	_, body := h.do(t, http.MethodGet, "/api/session", "")
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Remote == nil {
		t.Fatal("session carried no remote block for the owner")
	}
	if !out.Remote.Available || !out.Remote.Enabled {
		t.Errorf("remote = %+v, want available and enabled", *out.Remote)
	}
	if out.Remote.Name != rs.name {
		t.Errorf("remote name = %q, want %q", out.Remote.Name, rs.name)
	}
}

// An anonymous request gets no remote block: the status is only for whoever is
// signed in and looking at their own account panel.
func TestSessionHidesRemoteStatusWhenSignedOut(t *testing.T) {
	rs := &remoteState{available: true, enabled: true, name: "abc.net.soundstorm.dev"}
	h := newRemoteHarness(t, rs)
	h.signUp(t)

	// A fresh client with no cookie is signed out.
	jar, _ := cookiejar.New(nil)
	anon := &harness{srv: h.srv, client: &http.Client{Jar: jar}, root: h.root}

	var out struct {
		Remote json.RawMessage `json:"remote"`
	}
	_, body := anon.do(t, http.MethodGet, "/api/session", "")
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Remote) != 0 {
		t.Errorf("signed-out session carried a remote block: %s", out.Remote)
	}
}

// The owner can turn remote access on, and the change is actually applied.
func TestOwnerCanToggleRemoteAccess(t *testing.T) {
	rs := &remoteState{available: true, enabled: false, name: "abc.net.soundstorm.dev"}
	h := newRemoteHarness(t, rs)
	h.signUp(t)

	resp, body := h.do(t, http.MethodPut, "/api/remote", `{"enabled":true}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /api/remote = %d %s", resp.StatusCode, body)
	}
	if rs.sets != 1 || !rs.enabled {
		t.Errorf("toggle not applied: sets=%d enabled=%v", rs.sets, rs.enabled)
	}
	var out struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.Enabled {
		t.Error("response did not report remote access as enabled")
	}
}

// A member cannot put the server on the internet. The route is behind the owner
// guard, so the change must be refused without touching the setting.
func TestAMemberCannotToggleRemoteAccess(t *testing.T) {
	rs := &remoteState{available: true, enabled: false, name: "abc.net.soundstorm.dev"}
	h := newRemoteHarness(t, rs)
	h.signUp(t)
	h.addMember(t, "sam", samPassword)
	member := h.asUser(t, "sam", samPassword)

	resp, _ := member.do(t, http.MethodPut, "/api/remote", `{"enabled":true}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("member toggle = %d, want 403", resp.StatusCode)
	}
	if rs.sets != 0 || rs.enabled {
		t.Errorf("a member changed remote access: sets=%d enabled=%v", rs.sets, rs.enabled)
	}
}

// Turning it on when there is no real certificate to serve is refused, rather
// than half-enabling something that cannot work. The setting is left untouched.
func TestRemoteToggleRefusedWithoutAutoHTTPS(t *testing.T) {
	rs := &remoteState{available: false}
	h := newRemoteHarness(t, rs)
	h.signUp(t)

	resp, _ := h.do(t, http.MethodPut, "/api/remote", `{"enabled":true}`)
	if resp.StatusCode != http.StatusPreconditionFailed {
		t.Errorf("toggle without auto https = %d, want 412", resp.StatusCode)
	}
	if rs.sets != 0 {
		t.Errorf("the setting was changed anyway: sets=%d", rs.sets)
	}
}
