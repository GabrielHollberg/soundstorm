package httpapi

import (
	"encoding/json"
	"net"
	"net/http"
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
	mux.HandleFunc("POST /button/used", func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		c.open = false
		c.asked = append(c.asked, "used")
		c.mu.Unlock()
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

	if resp, _ := stranger.do(t, http.MethodPost, "/api/reset/owner-password", `{"password":"violet tractor glacier"}`); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("set without the button: %d", resp.StatusCode)
	}
	c.mu.Lock()
	c.open = true
	c.mu.Unlock()
	if _, body := stranger.do(t, http.MethodGet, "/api/reset/button", ""); !strings.Contains(string(body), `"owner": "gabe"`) {
		t.Fatalf("the button's window: %s", body)
	}
	if resp, _ := stranger.do(t, http.MethodPost, "/api/reset/owner-password", `{"password":"short"}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a weak password was taken: %d", resp.StatusCode)
	}
	if resp, body := stranger.do(t, http.MethodPost, "/api/reset/owner-password", `{"password":"violet tractor glacier"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("setting it: %d %s", resp.StatusCode, body)
	}
	if resp, _ := h.do(t, http.MethodGet, "/api/favorites", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("the owner's old session lived on: %d", resp.StatusCode)
	}
	if status, _ := signInAs(t, h.another(t), "gabe", "violet tractor glacier"); status != http.StatusOK {
		t.Fatalf("the new password: %d", status)
	}
	if resp, _ := stranger.do(t, http.MethodPost, "/api/reset/owner-password", `{"password":"another long passphrase"}`); resp.StatusCode != http.StatusForbidden {
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
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.resets) != 1 || c.resets[0] != "start-over" {
		t.Fatalf("the caretaker was asked %v", c.resets)
	}
}
