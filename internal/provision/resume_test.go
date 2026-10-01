package provision

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"github.com/GabrielHollberg/soundstorm/internal/httpx"
	"github.com/GabrielHollberg/soundstorm/internal/state"
)

// A setup that fails after the admin account exists must finish on the next
// attempt. The password was saved only at the end, so a failure in between -
// here, signing in - left an account whose password nobody held, every retry
// was refused as "already set up", and only resetting the backend's volume
// by hand recovered it.
func TestASetupThatFailsHalfWayFinishesOnTheNextAttempt(t *testing.T) {
	var mu sync.Mutex
	initialized := false
	var rootPassword string
	logins := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch r.URL.Path {
		case "/status":
			_ = json.NewEncoder(w).Encode(map[string]any{"app": "audiobookshelf", "isInit": initialized})
		case "/init":
			if initialized {
				http.Error(w, "already initialized", http.StatusInternalServerError)
				return
			}
			root := body["newRoot"].(map[string]any)
			rootPassword = root["password"].(string)
			initialized = true
		case "/login":
			logins++
			if logins == 1 {
				// The step after the account fails: a restart, a timeout.
				http.Error(w, "busy", http.StatusServiceUnavailable)
				return
			}
			if body["password"] != rootPassword {
				http.Error(w, "wrong password", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"user": map[string]any{"id": "u1", "token": "tok"}})
		case "/api/libraries":
			if r.Method == http.MethodGet {
				_ = json.NewEncoder(w).Encode(map[string]any{"libraries": []any{}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "lib1"})
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	m := &Manager{store: store}
	c, err := httpx.New(srv.URL, 0)
	if err != nil {
		t.Fatal(err)
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	target := Target{ID: "audiobookshelf", MediaPath: "/audiobooks"}

	if _, err := provisionAudiobookshelf(context.Background(), c, target, m.secretsFor(target.ID), quiet); err == nil {
		t.Fatal("the first attempt should fail at signing in")
	}
	if _, kept := store.SetupSecret(target.ID, "password"); !kept {
		t.Fatal("the password should be kept the moment the account was made")
	}

	creds, err := provisionAudiobookshelf(context.Background(), c, target, m.secretsFor(target.ID), quiet)
	if err != nil {
		t.Fatalf("the second attempt should carry on with the kept password, got %v", err)
	}
	if creds.Token != "tok" || creds.LibraryID != "lib1" {
		t.Fatalf("got %+v", creds)
	}

	// Saved for good, the setup's own copy goes.
	if err := store.SetBackend(target.ID, creds); err != nil {
		t.Fatal(err)
	}
	if _, kept := store.SetupSecret(target.ID, "password"); kept {
		t.Fatal("the kept password should be cleared once the backend is saved")
	}
}

// Without a kept password, an account somebody else made is still refused:
// carrying on is only for SoundStorm's own unfinished setup.
func TestAnAccountSoundStormDidNotMakeIsStillRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			_ = json.NewEncoder(w).Encode(map[string]any{"isInit": true})
		}
	}))
	defer srv.Close()
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	m := &Manager{store: store}
	c, _ := httpx.New(srv.URL, 0)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := provisionAudiobookshelf(context.Background(), c, Target{ID: "audiobookshelf"}, m.secretsFor("audiobookshelf"), quiet); err == nil {
		t.Fatal("an initialized backend with nothing kept should be refused")
	}
}

// A person's own account made on a try whose answer was lost: the next try
// signs in with the kept password rather than being refused as a name taken.
func TestAPersonsAccountWhoseAnswerWasLostIsFinished(t *testing.T) {
	var mu sync.Mutex
	made := map[string]string{} // username -> password
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch r.URL.Path {
		case "/api/users":
			name := body["username"].(string)
			if _, taken := made[name]; taken {
				http.Error(w, "username taken", http.StatusBadRequest)
				return
			}
			made[name] = body["password"].(string)
			// Made, but the answer never arrives.
			http.Error(w, "gateway timeout", http.StatusGatewayTimeout)
		case "/login":
			if made[body["username"].(string)] != body["password"] {
				http.Error(w, "no", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"user": map[string]any{"id": "m1", "token": "member-token"}})
		}
	}))
	defer srv.Close()
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	m := &Manager{store: store}
	c, _ := httpx.New(srv.URL, 0)
	key := memberSecrets("audiobookshelf", "u1")
	if _, err := createAudiobookshelfUser(context.Background(), c, m.secretsFor(key), "admin", "member-u1"); err == nil {
		t.Fatal("the first try lost its answer and should fail")
	}
	id, err := createAudiobookshelfUser(context.Background(), c, m.secretsFor(key), "admin", "member-u1")
	if err != nil {
		t.Fatalf("the second try should sign in with the kept password, got %v", err)
	}
	if id.Token != "member-token" || id.RemoteID != "m1" {
		t.Fatalf("got %+v", id)
	}
}
