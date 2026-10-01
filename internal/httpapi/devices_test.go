package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
)

// another is a second browser: its own cookies, so its own device.
func (h *harness) another(t *testing.T) *harness {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &harness{srv: h.srv, client: &http.Client{Jar: jar}, root: h.root, api: h.api}
}

func signInAs(t *testing.T, h *harness, name, password string) (int, map[string]any) {
	t.Helper()
	resp, body := h.do(t, http.MethodPost, "/api/login", `{"username":"`+name+`","password":"`+password+`"}`)
	var out map[string]any
	_ = json.Unmarshal(body, &out)
	return resp.StatusCode, out
}

// With approval on, the right password on a new device waits until a device
// already signed in approves it; a refused one never gets in; and a device
// that has signed in before is not asked again.
func TestNewDevicesWaitForApproval(t *testing.T) {
	h := newHarness(t)
	h.signUp(t) // this browser is the owner's, and known
	if resp, body := h.do(t, http.MethodPut, "/api/settings/new-devices", `{"enabled":true}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("turning it on: %d %s", resp.StatusCode, body)
	}

	phone := h.another(t)
	code, out := signInAs(t, phone, "gabe", "correct horse")
	if code != http.StatusAccepted || out["pending"] == nil {
		t.Fatalf("a new device should wait: %d %v", code, out)
	}
	id := out["pending"].(string)
	// Waiting, it is not signed in.
	if resp, _ := phone.do(t, http.MethodGet, "/api/favorites", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a waiting device reached a guarded route: %d", resp.StatusCode)
	}
	if resp, body := phone.do(t, http.MethodGet, "/api/login/pending/"+id, ""); !strings.Contains(string(body), "waiting") {
		t.Fatalf("should still be waiting: %d %s", resp.StatusCode, body)
	}

	// The known device sees it and approves it.
	resp, body := h.do(t, http.MethodGet, "/api/devices/pending", "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), id) {
		t.Fatalf("the signed-in device should see the request: %d %s", resp.StatusCode, body)
	}
	if resp, body := h.do(t, http.MethodPost, "/api/devices/pending/"+id, `{"approve":true}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("approving: %d %s", resp.StatusCode, body)
	}
	if resp, body := phone.do(t, http.MethodGet, "/api/login/pending/"+id, ""); !signedIn(body) {
		t.Fatalf("approved, it should be signed in: %d %s", resp.StatusCode, body)
	}
	if resp, _ := phone.do(t, http.MethodGet, "/api/favorites", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("approved, it should reach guarded routes: %d", resp.StatusCode)
	}
	// Known now: the next sign-in on it is not held.
	phone.do(t, http.MethodPost, "/api/logout", "")
	if code, out := signInAs(t, phone, "gabe", "correct horse"); code != http.StatusOK {
		t.Fatalf("a device approved once should not be asked again: %d %v", code, out)
	}

	// A refused one never gets in.
	stranger := h.another(t)
	_, out = signInAs(t, stranger, "gabe", "correct horse")
	id2 := out["pending"].(string)
	h.do(t, http.MethodPost, "/api/devices/pending/"+id2, `{"approve":false}`)
	if resp, _ := stranger.do(t, http.MethodGet, "/api/login/pending/"+id2, ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a refused sign-in should be refused: %d", resp.StatusCode)
	}
	if resp, _ := stranger.do(t, http.MethodGet, "/api/favorites", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a refused device reached a guarded route: %d", resp.StatusCode)
	}

	// A wrong password is still just a wrong password.
	if code, _ := signInAs(t, h.another(t), "gabe", "wrong password!"); code != http.StatusUnauthorized {
		t.Fatalf("a wrong password: %d", code)
	}
}

// The setup code approves a device when nothing else is signed in - but only
// a configured one (from .env), never one made up at start.
func TestTheSetupCodeApprovesADevice(t *testing.T) {
	h := newHarness(t)
	h.signUp(t)
	h.do(t, http.MethodPut, "/api/settings/new-devices", `{"enabled":true}`)
	h.api.approvalCode = NormalizeSetupCode(testSetupCode)

	laptop := h.another(t)
	_, out := signInAs(t, laptop, "gabe", "correct horse")
	id := out["pending"].(string)
	if resp, _ := laptop.do(t, http.MethodPost, "/api/login/pending/"+id, `{"setupCode":"not-it"}`); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a wrong code should be refused: %d", resp.StatusCode)
	}
	resp, body := laptop.do(t, http.MethodPost, "/api/login/pending/"+id, `{"setupCode":"`+testSetupCode+`"}`)
	if resp.StatusCode != http.StatusOK || !signedIn(body) {
		t.Fatalf("the setup code should approve it: %d %s", resp.StatusCode, body)
	}
}

// Off (the default), nothing changes.
func TestApprovalOffChangesNothing(t *testing.T) {
	h := newHarness(t)
	h.signUp(t)
	if code, out := signInAs(t, h.another(t), "gabe", "correct horse"); code != http.StatusOK {
		t.Fatalf("with approval off a new device signs in: %d %v", code, out)
	}
}

func signedIn(body []byte) bool {
	var out struct {
		SignedIn bool `json:"signedIn"`
	}
	return json.Unmarshal(body, &out) == nil && out.SignedIn
}
