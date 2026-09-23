package provision

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/httpx"
)

// fakeImmichSetup answers Immich 3.2's first-run endpoints in the shapes the
// live server returned, and remembers what was asked.
type fakeImmichSetup struct {
	initialised bool
	paths       []string
	savedConfig map[string]any
	libraryBody map[string]any
}

func (f *fakeImmichSetup) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.paths = append(f.paths, r.Method+" "+r.URL.Path)
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	json.Unmarshal(raw, &body)
	reply := func(v any) { json.NewEncoder(w).Encode(v) }

	switch r.Method + " " + r.URL.Path {
	case "GET /api/server/ping":
		reply(map[string]string{"res": "pong"})
	case "GET /api/server/config":
		reply(map[string]bool{"isInitialized": f.initialised})
	case "POST /api/auth/admin-sign-up":
		f.initialised = true
		w.WriteHeader(http.StatusCreated)
		reply(map[string]string{"id": "u-1", "email": body["email"].(string)})
	case "POST /api/auth/login":
		reply(map[string]string{"accessToken": "session-token", "userId": "u-1"})
	case "POST /api/api-keys":
		if r.Header.Get("Authorization") != "Bearer session-token" {
			http.Error(w, "unauthorised", http.StatusUnauthorized)
			return
		}
		reply(map[string]any{"secret": "the-api-key", "permissions": body["permissions"]})
	case "GET /api/libraries":
		reply([]any{})
	case "POST /api/libraries":
		f.libraryBody = body
		reply(map[string]any{"id": "lib-1", "importPaths": body["importPaths"]})
	case "GET /api/system-config":
		reply(map[string]any{
			"library": map[string]any{
				"scan":  map[string]any{"enabled": true, "cronExpression": "0 0 * * *"},
				"watch": map[string]any{"enabled": false},
			},
			"ffmpeg": map[string]any{"crf": 23},
		})
	case "PUT /api/system-config":
		f.savedConfig = body
		reply(body)
	case "POST /api/libraries/lib-1/scan":
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}

func runImmichSetup(t *testing.T, f *fakeImmichSetup) (string, string, string, error) {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	c, err := httpx.New(srv.URL, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	b, err := provisionImmich(context.Background(), c, Target{ID: "immich", MediaPath: "/pictures"}, quiet)
	return b.Token, b.LibraryID, b.UserID, err
}

func TestImmichIsProvisionedWithNobodyLoggingIn(t *testing.T) {
	f := &fakeImmichSetup{}
	token, library, user, err := runImmichSetup(t, f)
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	// The API key, not the session token: a session expires, and SoundStorm
	// keeps no password to log in with again.
	if token != "the-api-key" || library != "lib-1" || user != "u-1" {
		t.Errorf("stored token=%q library=%q user=%q", token, library, user)
	}
	if got := f.libraryBody["importPaths"]; got == nil || got.([]any)[0] != "/pictures" || f.libraryBody["ownerId"] != "u-1" {
		t.Errorf("library created with %v", f.libraryBody)
	}
	want := []string{
		"GET /api/server/ping", "GET /api/server/config", "POST /api/auth/admin-sign-up",
		"POST /api/auth/login", "POST /api/api-keys", "GET /api/libraries", "POST /api/libraries",
		"GET /api/system-config", "PUT /api/system-config", "POST /api/libraries/lib-1/scan",
	}
	if strings.Join(f.paths, "\n") != strings.Join(want, "\n") {
		t.Errorf("calls:\n  %s\nwant:\n  %s", strings.Join(f.paths, "\n  "), strings.Join(want, "\n  "))
	}
}

// The config endpoint takes the whole document back. Watching is switched on
// and everything else - here the ffmpeg setting - must pass through as it was.
func TestImmichWatchingChangesOnlyTheOneSetting(t *testing.T) {
	f := &fakeImmichSetup{}
	if _, _, _, err := runImmichSetup(t, f); err != nil {
		t.Fatal(err)
	}
	lib := f.savedConfig["library"].(map[string]any)
	if lib["watch"].(map[string]any)["enabled"] != true {
		t.Error("watching was not switched on")
	}
	if lib["scan"].(map[string]any)["cronExpression"] != "0 0 * * *" {
		t.Error("the scan schedule did not survive")
	}
	if f.savedConfig["ffmpeg"].(map[string]any)["crf"] != float64(23) {
		t.Error("an unrelated setting was lost on the way back")
	}
}

// An Immich that already has an admin, with credentials SoundStorm does not
// hold, is a human decision - never something to retry into.
func TestAnImmichSetUpByOthersIsRefused(t *testing.T) {
	_, _, _, err := runImmichSetup(t, &fakeImmichSetup{initialised: true})
	if err == nil || !strings.Contains(err.Error(), "no stored credentials") {
		t.Errorf("err = %v", err)
	}
}
