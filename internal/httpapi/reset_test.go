package httpapi

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// A stand-in box caretaker on a socket: the button, open or not, and what it
// was asked.
type fakeCaretaker struct {
	mu     sync.Mutex
	open   bool
	asked  []string
	resets []string
}

func startCaretaker(t *testing.T) *fakeCaretaker {
	t.Helper()
	dir, err := os.MkdirTemp("", "ct")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "c.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	c := &fakeCaretaker{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /button", func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		defer c.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]bool{"open": c.open})
	})
	mux.HandleFunc("POST /button/claim", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Code string }
		json.NewDecoder(r.Body).Decode(&body)
		c.mu.Lock()
		defer c.mu.Unlock()
		// As the real caretaker reads a typed code: spaces and dashes aside.
		if !c.open || strings.NewReplacer(" ", "", "-", "").Replace(body.Code) != "123456" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		c.open = false
		c.asked = append(c.asked, "claimed")
	})
	mux.HandleFunc("POST /reset", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Mode string }
		json.NewDecoder(r.Body).Decode(&body)
		c.mu.Lock()
		c.resets = append(c.resets, body.Mode)
		c.mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	testCaretakerSocket = sock
	t.Cleanup(func() { testCaretakerSocket = "" })
	return c
}

// The owner's password set again after five presses of the box's button:
// not before, once only, with today's password rules, and signing the owner
// out everywhere.
func TestTheBoxButtonSetsTheOwnersPassword(t *testing.T) {
	c := startCaretaker(t)
	h := newHarness(t)
	h.signUp(t)
	stranger := h.another(t)

	if resp, _ := stranger.do(t, http.MethodPost, "/api/reset/owner-password", `{"password":"violet tractor glacier","code":"123456"}`); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("set without the button: %d", resp.StatusCode)
	}
	c.mu.Lock()
	c.open = true
	c.mu.Unlock()
	if _, body := stranger.do(t, http.MethodGet, "/api/reset/button", ""); !strings.Contains(string(body), `"owner": "gabe"`) || strings.Contains(string(body), "123456") {
		t.Fatalf("the button's window: %s", body)
	}
	if resp, _ := stranger.do(t, http.MethodPost, "/api/reset/owner-password", `{"password":"short","code":"123456"}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a weak password was taken: %d", resp.StatusCode)
	}
	// Somebody on the home network who is not at the box has no code.
	if resp, _ := stranger.do(t, http.MethodPost, "/api/reset/owner-password", `{"password":"violet tractor glacier","code":"654321"}`); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("set without the code on the box's screen: %d", resp.StatusCode)
	}
	if resp, body := stranger.do(t, http.MethodPost, "/api/reset/owner-password", `{"password":"violet tractor glacier","code":"123 456"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("setting it: %d %s", resp.StatusCode, body)
	}
	if resp, _ := h.do(t, http.MethodGet, "/api/favorites", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("the owner's old session lived on: %d", resp.StatusCode)
	}
	if status, _ := signInAs(t, h.another(t), "gabe", "violet tractor glacier"); status != http.StatusOK {
		t.Fatalf("the new password: %d", status)
	}
	if resp, _ := stranger.do(t, http.MethodPost, "/api/reset/owner-password", `{"password":"another long passphrase","code":"123456"}`); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("the window was used twice: %d", resp.StatusCode)
	}
}

// Starting over and erasing: the owner's, with the password and the word.
func TestStartingOverNeedsThePasswordAndTheWord(t *testing.T) {
	c := startCaretaker(t)
	h := newHarness(t)
	h.signUp(t)
	if _, body := h.do(t, http.MethodGet, "/api/session", ""); !strings.Contains(string(body), `"boxReset": true`) {
		t.Fatalf("the owner's session should offer it: %s", body)
	}
	if resp, _ := h.do(t, http.MethodPost, "/api/reset", `{"mode":"erase","password":"correct horse","confirm":"erase it"}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("the wrong word: %d", resp.StatusCode)
	}
	if resp, _ := h.do(t, http.MethodPost, "/api/reset", `{"mode":"erase","password":"wrong","confirm":"ERASE"}`); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("the wrong password: %d", resp.StatusCode)
	}
	if resp, body := h.do(t, http.MethodPost, "/api/reset", `{"mode":"start-over","password":"correct horse","confirm":"start over"}`); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("starting over: %d %s", resp.StatusCode, body)
	}
	h.do(t, http.MethodPost, "/api/users", `{"username":"sam","password":"violet tractor glacier"}`)
	sam := h.another(t)
	sam.do(t, http.MethodPost, "/api/login", `{"username":"sam","password":"violet tractor glacier"}`)
	if resp, _ := sam.do(t, http.MethodPost, "/api/reset", `{"mode":"erase","password":"violet tractor glacier","confirm":"ERASE"}`); resp.StatusCode == http.StatusAccepted {
		t.Fatal("a member erased the box")
	}
	// The setup code, as on the label under the box, is the owner's to see
	// (lost label, Start over), never a member's.
	if _, body := h.do(t, http.MethodGet, "/api/session", ""); !strings.Contains(string(body), `"boxSetupCode": "`+NormalizeSetupCode(testSetupCode)+`"`) {
		t.Fatalf("the owner should see the setup code: %s", body)
	}
	if _, body := sam.do(t, http.MethodGet, "/api/session", ""); strings.Contains(string(body), "boxSetupCode") {
		t.Fatalf("a member saw the setup code: %s", body)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.resets) != 1 || c.resets[0] != "start-over" {
		t.Fatalf("the caretaker was asked %v", c.resets)
	}
}

// The power button's password reset is the home network's: the connection's
// own address decides, never the name the request asks for.
func TestTheButtonsResetIsOnlyFromHome(t *testing.T) {
	for addr, home := range map[string]bool{
		"192.168.0.20:51000":     true,
		"10.1.2.3:443":           true,
		"127.0.0.1:8099":         true,
		"[fe80::1]:8099":         true,
		"[::ffff:192.168.1.5]:1": true,
		"203.0.113.9:51000":      false,
		"8.8.8.8:443":            false,
		"[2001:db8::1]:443":      false,
	} {
		r := httptest.NewRequest(http.MethodPost, "http://192.168.0.20:8099/api/reset/owner-password", nil)
		r.RemoteAddr = addr
		if got := fromHomeNetwork(r); got != home {
			t.Errorf("%s: from home %v, want %v", addr, got, home)
		}
	}
}

// On a box, a connection from one of the container's own networks was
// relayed (Docker's proxy for IPv6, Tailscale's sidecar) and is not taken
// for the home network; off a box it is, as Docker Desktop gives every
// connection such an address.
func TestARelayedConnectionIsNotFromHomeOnABox(t *testing.T) {
	nets := ownNetworks()
	if len(nets) == 0 {
		t.Skip("no network here")
	}
	relayed := nets[0].Addr().Next()
	if !relayed.IsPrivate() {
		t.Skip("this network is not a private one")
	}
	r := httptest.NewRequest(http.MethodPost, "/api/reset/owner-password", nil)
	r.RemoteAddr = netip.AddrPortFrom(relayed, 40000).String()
	box := &Server{caretakerSocket: "/run/x"}
	if box.homeConnection(r) {
		t.Errorf("%s, relayed, was taken for home on a box", relayed)
	}
	if !(&Server{}).homeConnection(r) {
		t.Errorf("%s was refused off a box", relayed)
	}
	r.RemoteAddr = "127.0.0.1:5000"
	if !box.homeConnection(r) {
		t.Error("the box itself was refused")
	}
}

// The request log never carries a secret that rode in an address.
func TestSecretsInAddressesAreNotLogged(t *testing.T) {
	for in, want := range map[string]string{
		"/invite/AAAAAAAAAAAAAAAAAAAAAA":      "/invite/...",
		"/api/invite/AAAAAAAAAAAAAAAAAAAAAA":  "/api/invite/...",
		"/api/link/0123456789abcdef/qr.png":   "/api/link/.../qr.png",
		"/api/link/code/KXT4PM":               "/api/link/code/...",
		"/api/login/pending/0123456789abcdef": "/api/login/pending/...",
		"/api/devices/pending/0123456789abcd": "/api/devices/pending/...",
		"/link/KXT4PM":                        "/link/...",
		"/api/search":                         "/api/search",
		"/api/players/abcdef0123456789/next":  "/api/players/abcdef0123456789/next",
	} {
		if got := logPath(in); got != want {
			t.Errorf("%s logged as %s, want %s", in, got, want)
		}
	}
}

// Another site's page cannot start work on this server through a visitor's
// browser, reading or writing; the app's own pages, and players that say
// nothing, can.
func TestOtherSitesCannotStartWorkByReading(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := sameOrigin(ok)
	for _, c := range []struct {
		path, site string
		want       int
	}{
		{"/api/hls/jellyfin/x/master.m3u8", "cross-site", http.StatusForbidden},
		{"/api/music/beats", "same-site", http.StatusForbidden},
		{"/api/music/beats", "same-origin", http.StatusOK},
		{"/api/stream/navidrome/1", "", http.StatusOK},
		{"/api/search", "none", http.StatusOK},
		{"/invite/abc", "cross-site", http.StatusOK},
	} {
		r := httptest.NewRequest(http.MethodGet, c.path, nil)
		if c.site != "" {
			r.Header.Set("Sec-Fetch-Site", c.site)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != c.want {
			t.Errorf("%s from %q: %d, want %d", c.path, c.site, w.Code, c.want)
		}
	}
}
