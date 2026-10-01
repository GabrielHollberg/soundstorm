package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func newLinkFor(t *testing.T, tv *harness) (id, code, url string) {
	t.Helper()
	resp, body := tv.do(t, http.MethodPost, "/api/link", "")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("asking for a code: %d %s", resp.StatusCode, body)
	}
	var out struct{ ID, Code, URL string }
	if err := json.Unmarshal(body, &out); err != nil || out.ID == "" || len(out.Code) != 7 {
		t.Fatalf("a code should come back: %s", body)
	}
	return out.ID, out.Code, out.URL
}

// A TV signed in from a phone: it asks for a code, waits, and once a phone
// signed in allows the code it is signed in as the phone's person - with
// approval of new devices on, which a phone's yes stands in for. The code
// works once.
func TestATVIsSignedInFromAPhone(t *testing.T) {
	h := newHarness(t)
	h.signUp(t) // the phone, signed in
	h.do(t, http.MethodPut, "/api/settings/new-devices", `{"enabled":true}`)

	tv := h.another(t)
	id, code, url := newLinkFor(t, tv)
	if !strings.HasSuffix(url, "/?link="+strings.ReplaceAll(code, "-", "")) {
		t.Fatalf("the QR's address should carry the code: %s", url)
	}
	if resp, body := tv.do(t, http.MethodGet, "/api/link/"+id, ""); !strings.Contains(string(body), "waiting") {
		t.Fatalf("should be waiting: %d %s", resp.StatusCode, body)
	}
	if resp, _ := tv.do(t, http.MethodGet, "/api/favorites", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a waiting TV reached a guarded route: %d", resp.StatusCode)
	}
	if resp, _ := tv.do(t, http.MethodGet, "/api/link/"+id+"/qr.png", ""); resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("the QR code: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}

	// The phone looks the code up as typed - lower case, no dash - and
	// allows it.
	typed := strings.ToLower(strings.ReplaceAll(code, "-", " "))
	resp, body := h.do(t, http.MethodGet, "/api/link/code/"+typed, "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"as": "gabe"`) {
		t.Fatalf("looking the code up: %d %s", resp.StatusCode, body)
	}
	if resp, body := h.do(t, http.MethodPost, "/api/link/code/"+typed, `{"approve":true}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("allowing it: %d %s", resp.StatusCode, body)
	}
	if resp, body := tv.do(t, http.MethodGet, "/api/link/"+id, ""); !signedIn(body) {
		t.Fatalf("allowed, the TV should be signed in: %d %s", resp.StatusCode, body)
	}
	if resp, _ := tv.do(t, http.MethodGet, "/api/favorites", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("signed in, the TV should reach guarded routes: %d", resp.StatusCode)
	}
	// Used up.
	if resp, _ := tv.do(t, http.MethodGet, "/api/link/"+id, ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a used code should be gone: %d", resp.StatusCode)
	}
	if resp, _ := h.do(t, http.MethodGet, "/api/link/code/"+typed, ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a used code should not be found: %d", resp.StatusCode)
	}
}

// Not allowed, the TV is told so and is not signed in; and nobody signed out
// can look a code up or answer it.
func TestARefusedOrUnsignedLinkSignsNothingIn(t *testing.T) {
	h := newHarness(t)
	h.signUp(t)
	tv := h.another(t)
	id, code, _ := newLinkFor(t, tv)

	stranger := h.another(t)
	if resp, _ := stranger.do(t, http.MethodGet, "/api/link/code/"+code, ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("looking a code up signed out: %d", resp.StatusCode)
	}
	if resp, _ := stranger.do(t, http.MethodPost, "/api/link/code/"+code, `{"approve":true}`); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("allowing a code signed out: %d", resp.StatusCode)
	}

	h.do(t, http.MethodPost, "/api/link/code/"+code, `{"approve":false}`)
	if resp, _ := tv.do(t, http.MethodGet, "/api/link/"+id, ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a refused TV should be told: %d", resp.StatusCode)
	}
	if resp, _ := tv.do(t, http.MethodGet, "/api/favorites", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a refused TV reached a guarded route: %d", resp.StatusCode)
	}
}

// Codes cannot be tried one after another: looking them up is limited per
// person.
func TestLinkCodesCannotBeGuessedQuickly(t *testing.T) {
	h := newHarness(t)
	h.signUp(t)
	limited := false
	for i := 0; i < 15; i++ {
		resp, _ := h.do(t, http.MethodGet, "/api/link/code/AAAAAA", "")
		if resp.StatusCode == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("fifteen guesses in a row were all answered")
	}
}
