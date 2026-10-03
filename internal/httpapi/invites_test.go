package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// An invitation: the owner makes it for a name, the person opens its link,
// chooses their own password and is signed in; it works once, can be
// cancelled, and is the owner's alone to make.
func TestAnInvitationMakesAnAccount(t *testing.T) {
	h := newHarness(t)
	h.signUp(t)
	h.do(t, http.MethodPut, "/api/settings/new-devices", `{"enabled":true}`)

	resp, body := h.do(t, http.MethodPost, "/api/invites", `{"name":"Mom"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("inviting: %d %s", resp.StatusCode, body)
	}
	var inv struct{ ID, URL, QR string }
	json.Unmarshal(body, &inv)
	i := strings.Index(inv.URL, "/invite/")
	if i < 0 || !strings.HasPrefix(inv.QR, "data:image/png;base64,") {
		t.Fatalf("the invitation: %s", body)
	}
	token := inv.URL[i+len("/invite/"):]

	mom := h.another(t)
	if resp, got := mom.do(t, http.MethodGet, "/invite/"+token, ""); resp.StatusCode != 200 || resp.Request.URL.RequestURI() != "/?invite="+token {
		t.Fatalf("the code's address: %d %s %s", resp.StatusCode, resp.Request.URL, got[:0])
	}
	if _, body := mom.do(t, http.MethodGet, "/api/invite/"+token, ""); !strings.Contains(string(body), `"name": "Mom"`) || !strings.Contains(string(body), `"by": "gabe"`) {
		t.Fatalf("looking it up: %s", body)
	}
	if resp, _ := mom.do(t, http.MethodPost, "/api/invite/"+token, `{"username":"Mom","password":"short"}`); resp.StatusCode == http.StatusOK {
		t.Fatal("a weak password was taken")
	}
	if resp, body := mom.do(t, http.MethodPost, "/api/invite/"+token, `{"username":"Mom","password":"violet tractor glacier"}`); resp.StatusCode != http.StatusOK || !signedIn(body) {
		t.Fatalf("accepting: %d %s", resp.StatusCode, body)
	}
	// Signed in at once, approval of new devices or not: the owner vouched.
	if resp, _ := mom.do(t, http.MethodGet, "/api/favorites", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("not signed in after accepting: %d", resp.StatusCode)
	}
	if resp, _ := h.another(t).do(t, http.MethodPost, "/api/invite/"+token, `{"username":"Mom2","password":"violet tractor glacier"}`); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("used twice: %d", resp.StatusCode)
	}
	if _, body := h.do(t, http.MethodGet, "/api/users", ""); !strings.Contains(string(body), `"name": "Mom"`) {
		t.Fatalf("Mom is not among the people: %s", body)
	}
	// A member cannot invite.
	if resp, _ := mom.do(t, http.MethodPost, "/api/invites", `{"name":"Stranger"}`); resp.StatusCode == http.StatusOK {
		t.Fatal("a member made an invitation")
	}
	// Cancelled, it is gone.
	_, body = h.do(t, http.MethodPost, "/api/invites", `{"name":"Dad"}`)
	json.Unmarshal(body, &inv)
	dad := inv.URL[strings.Index(inv.URL, "/invite/")+len("/invite/"):]
	h.do(t, http.MethodDelete, "/api/invites/"+inv.ID, "")
	if resp, _ := h.another(t).do(t, http.MethodGet, "/api/invite/"+dad, ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a cancelled invitation: %d", resp.StatusCode)
	}
}
