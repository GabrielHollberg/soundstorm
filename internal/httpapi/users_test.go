package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"

	"github.com/gabehollberg/soundstorm/internal/media"
)

const samPassword = "a long enough password"

// asUser returns a second client with its own cookie jar, signed in as
// somebody else - which is the only way to test that two people are actually
// kept apart rather than merely appearing to be.
func (h *harness) asUser(t *testing.T, name, password string) *harness {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	other := &harness{srv: h.srv, client: &http.Client{Jar: jar}}
	resp, body := other.do(t, http.MethodPost, "/api/login",
		`{"username":"`+name+`","password":"`+password+`"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sign in as %s: %d %s", name, resp.StatusCode, body)
	}
	return other
}

func (h *harness) addMember(t *testing.T, name, password string) string {
	t.Helper()
	resp, body := h.do(t, http.MethodPost, "/api/users",
		`{"username":"`+name+`","password":"`+password+`"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create %s: %d %s", name, resp.StatusCode, body)
	}
	var out struct {
		User struct {
			ID    string `json:"id"`
			Owner bool   `json:"owner"`
		} `json:"user"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.User.Owner {
		t.Errorf("%s was created as an owner", name)
	}
	return out.User.ID
}

func (h *harness) ownID(t *testing.T) string {
	t.Helper()
	_, body := h.do(t, http.MethodGet, "/api/session", "")
	var session struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(body, &session); err != nil {
		t.Fatal(err)
	}
	return session.User.ID
}

func TestTheFirstAccountOwnsTheServerAndCanAddOthers(t *testing.T) {
	h := newHarness(t)
	h.signUp(t)

	_, body := h.do(t, http.MethodGet, "/api/session", "")
	if !strings.Contains(string(body), `"owner": true`) {
		t.Errorf("the account that set the server up is not the owner: %s", body)
	}

	h.addMember(t, "sam", samPassword)
	_, body = h.do(t, http.MethodGet, "/api/users", "")
	var list struct {
		Users []struct {
			Name  string `json:"name"`
			Owner bool   `json:"owner"`
		} `json:"users"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list.Users) != 2 {
		t.Fatalf("want 2 accounts, got %d: %s", len(list.Users), body)
	}
	// Oldest first, so the list does not reshuffle between page loads.
	if list.Users[0].Name != "gabe" || !list.Users[0].Owner {
		t.Errorf("first entry = %+v", list.Users[0])
	}
	if list.Users[1].Owner {
		t.Error("the added account is an owner too")
	}
}

// Signup is a first-boot action. A second one would be a stranger who found the
// port claiming a server somebody else set up.
func TestSignupIsClosedOnceThereIsAnAccount(t *testing.T) {
	h := newHarness(t)
	h.signUp(t)

	resp, _ := h.do(t, http.MethodPost, "/api/signup",
		`{"username":"intruder","password":"`+samPassword+`"}`)
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want 409", resp.StatusCode)
	}
}

// The role distinction exists for exactly one thing, and this is it.
func TestAMemberCannotManageAccounts(t *testing.T) {
	h := newHarness(t)
	h.signUp(t)
	memberID := h.addMember(t, "sam", samPassword)
	sam := h.asUser(t, "sam", samPassword)

	for _, call := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/users", ""},
		{http.MethodPost, "/api/users", `{"username":"x","password":"` + samPassword + `"}`},
		{http.MethodDelete, "/api/users/" + memberID, ""},
		{http.MethodPost, "/api/users/" + memberID + "/password", `{"password":"` + samPassword + `"}`},
	} {
		resp, body := sam.do(t, call.method, call.path, call.body)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s = %d, want 403: %s", call.method, call.path, resp.StatusCode, body)
		}
	}
}

// Changing your own password is the only account operation a member has.
func TestAnyoneCanChangeTheirOwnPassword(t *testing.T) {
	h := newHarness(t)
	h.signUp(t)
	h.addMember(t, "sam", samPassword)
	sam := h.asUser(t, "sam", samPassword)

	resp, body := sam.do(t, http.MethodPost, "/api/account/password",
		`{"password":"a different long one"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	h.asUser(t, "sam", "a different long one")

	// And a short one is still refused, or the rule only applied at signup.
	resp, _ = sam.do(t, http.MethodPost, "/api/account/password", `{"password":"short"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a five character password was accepted: %d", resp.StatusCode)
	}
}

// An owner has to be able to get somebody back in: there is no email here to
// send a reset link to.
func TestTheOwnerCanResetSomebodyElsesPassword(t *testing.T) {
	h := newHarness(t)
	h.signUp(t)
	memberID := h.addMember(t, "sam", samPassword)

	resp, body := h.do(t, http.MethodPost, "/api/users/"+memberID+"/password",
		`{"password":"the one the owner chose"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	h.asUser(t, "sam", "the one the owner chose")
}

// The whole point of multi-user: two people reading the same book keep their
// own places.
func TestTwoPeopleKeepSeparateReadingPositions(t *testing.T) {
	h := newHarness(t, stub{id: "ebooks", kind: media.KindEbook})
	h.signUp(t)
	h.addMember(t, "sam", samPassword)
	sam := h.asUser(t, "sam", samPassword)

	const where = "?source=ebooks&id=dune.epub"
	resp, body := h.do(t, http.MethodPut, "/api/book/progress"+where,
		`{"location":"epubcfi(/6/4!/2)","fraction":0.1}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("owner save: %d %s", resp.StatusCode, body)
	}
	resp, body = sam.do(t, http.MethodPut, "/api/book/progress"+where,
		`{"location":"epubcfi(/6/40!/9)","fraction":0.9}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("member save: %d %s", resp.StatusCode, body)
	}

	read := func(who *harness) (string, float64) {
		t.Helper()
		_, body := who.do(t, http.MethodGet, "/api/book/progress"+where, "")
		var p struct {
			Location string  `json:"location"`
			Fraction float64 `json:"fraction"`
		}
		if err := json.Unmarshal(body, &p); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return p.Location, p.Fraction
	}

	ownerAt, ownerFraction := read(h)
	memberAt, memberFraction := read(sam)
	if ownerAt == memberAt {
		t.Errorf("both accounts are at %q; one overwrote the other", ownerAt)
	}
	if ownerFraction != 0.1 || memberFraction != 0.9 {
		t.Errorf("owner at %v, member at %v", ownerFraction, memberFraction)
	}
}

// Removing somebody has to take effect at once, not whenever their cookie
// happened to expire.
func TestRemovingSomebodySignsThemOutImmediately(t *testing.T) {
	h := newHarness(t)
	h.signUp(t)
	memberID := h.addMember(t, "sam", samPassword)
	sam := h.asUser(t, "sam", samPassword)

	resp, _ := sam.do(t, http.MethodGet, "/api/setup", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the member could not use the server before being removed: %d", resp.StatusCode)
	}

	resp, body := h.do(t, http.MethodDelete, "/api/users/"+memberID, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete: %d %s", resp.StatusCode, body)
	}

	// Both the JSON API and the media bytes, because the second is what a
	// removed account would otherwise still be able to help themselves to.
	for _, path := range []string{"/api/setup", "/api/search?q=anything", "/api/stream/x/y"} {
		resp, _ = sam.do(t, http.MethodGet, path, "")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("a removed account still reaches %s: %d", path, resp.StatusCode)
		}
	}
}

// An owner who removed themselves would leave a server nobody could administer.
func TestTheOwnerCannotRemoveThemselves(t *testing.T) {
	h := newHarness(t)
	h.signUp(t)

	resp, _ := h.do(t, http.MethodDelete, "/api/users/"+h.ownID(t), "")
	if resp.StatusCode == http.StatusOK {
		t.Error("the owner removed their own account")
	}
}

// A wrong name and a wrong password must be indistinguishable, or the login
// form becomes a way to find out who has an account here.
func TestAnUnknownNameLooksLikeAWrongPassword(t *testing.T) {
	h := newHarness(t)
	h.signUp(t)

	wrongPasswordResp, wrongPassword := h.do(t, http.MethodPost, "/api/login",
		`{"username":"gabe","password":"nope"}`)
	wrongNameResp, wrongName := h.do(t, http.MethodPost, "/api/login",
		`{"username":"nobody","password":"nope"}`)

	if wrongPasswordResp.StatusCode != wrongNameResp.StatusCode {
		t.Errorf("status %d vs %d", wrongPasswordResp.StatusCode, wrongNameResp.StatusCode)
	}
	if string(wrongPassword) != string(wrongName) {
		t.Errorf("responses differ:\n  %s\n  %s", wrongPassword, wrongName)
	}
}

// Nothing may hand back the material a password could be attacked offline with.
func TestAccountsNeverLeakPasswordMaterial(t *testing.T) {
	h := newHarness(t)
	h.signUp(t)
	h.addMember(t, "sam", samPassword)

	for _, path := range []string{"/api/session", "/api/users"} {
		_, body := h.do(t, http.MethodGet, path, "")
		lower := strings.ToLower(string(body))
		for _, leak := range []string{"salt", "hash", "iterations", "password"} {
			if strings.Contains(lower, leak) {
				t.Errorf("%s exposes %q: %s", path, leak, body)
			}
		}
	}
}

// The UI decides whether to show account management from this response, so
// without it the owner would not find out they were the owner until reload.
func TestSignupReturnsTheAccountItJustCreated(t *testing.T) {
	h := newHarness(t)
	resp, body := h.do(t, http.MethodPost, "/api/signup",
		`{"username":"gabe","password":"`+samPassword+`"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	var out struct {
		User struct {
			Name  string `json:"name"`
			Owner bool   `json:"owner"`
		} `json:"user"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.User.Name != "gabe" || !out.User.Owner {
		t.Errorf("signup returned %+v", out.User)
	}
}
