package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"

	"github.com/GabrielHollberg/soundstorm/internal/media"
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
	other := &harness{srv: h.srv, client: &http.Client{Jar: jar}, root: h.root}
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
		`{"setupCode":"`+testSetupCode+`","username":"intruder","password":"`+samPassword+`"}`)
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
		`{"current":"`+samPassword+`","password":"a different long one"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	h.asUser(t, "sam", "a different long one")

	// And a short one is still refused, or the rule only applied at signup.
	resp, _ = sam.do(t, http.MethodPost, "/api/account/password",
		`{"current":"a different long one","password":"short"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a five character password was accepted: %d", resp.StatusCode)
	}
}

// A session alone must not be enough to take an account over: somebody who
// finds a signed-in browser could otherwise change the password and keep it.
func TestChangingYourPasswordNeedsTheCurrentOne(t *testing.T) {
	h := newHarness(t)
	h.signUp(t)
	h.addMember(t, "sam", samPassword)
	sam := h.asUser(t, "sam", samPassword)

	for _, body := range []string{
		`{"password":"taken over by someone"}`,
		`{"current":"a wrong guess","password":"taken over by someone"}`,
	} {
		resp, out := sam.do(t, http.MethodPost, "/api/account/password", body)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403: %s", body, resp.StatusCode, out)
		}
	}
	// The old password must still be the one that works.
	h.asUser(t, "sam", samPassword)
}

// Changing a password is what somebody does when they think it is known, so
// every other device goes - and the one they did it on stays.
func TestChangingYourPasswordSignsOutEverywhereElse(t *testing.T) {
	h := newHarness(t)
	h.signUp(t)
	h.addMember(t, "sam", samPassword)
	phone := h.asUser(t, "sam", samPassword)
	laptop := h.asUser(t, "sam", samPassword)

	resp, body := phone.do(t, http.MethodPost, "/api/account/password",
		`{"current":"`+samPassword+`","password":"a different long one"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("change: %d %s", resp.StatusCode, body)
	}
	if resp, _ := phone.do(t, http.MethodGet, "/api/library", ""); resp.StatusCode != http.StatusOK {
		t.Errorf("the device that changed it was signed out: %d", resp.StatusCode)
	}
	if resp, _ := laptop.do(t, http.MethodGet, "/api/library", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("another device is still signed in: %d", resp.StatusCode)
	}
	// Nobody else is touched.
	if resp, _ := h.do(t, http.MethodGet, "/api/library", ""); resp.StatusCode != http.StatusOK {
		t.Errorf("the owner was signed out by somebody else's change: %d", resp.StatusCode)
	}
}

// An owner resetting somebody's password signs that person out everywhere,
// since the usual reason is that the old one got out.
func TestAResetSignsThatAccountOut(t *testing.T) {
	h := newHarness(t)
	h.signUp(t)
	memberID := h.addMember(t, "sam", samPassword)
	sam := h.asUser(t, "sam", samPassword)

	resp, body := h.do(t, http.MethodPost, "/api/users/"+memberID+"/password",
		`{"password":"the one the owner chose"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reset: %d %s", resp.StatusCode, body)
	}
	if resp, _ := sam.do(t, http.MethodGet, "/api/library", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("sam is still signed in on the old password: %d", resp.StatusCode)
	}

	// The admin reset path refuses the owner's own account: it takes no
	// current password, so allowing it would be a current-password-free way
	// to change the owner password from a stolen session - exactly what
	// ChangeOwnPassword's check exists to stop. Self-service goes through
	// /api/account/password instead.
	resp, body = h.do(t, http.MethodPost, "/api/users/"+h.ownID(t)+"/password",
		`{"password":"the owner's new one"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("owner self-reset via the admin path should be refused, got %d %s", resp.StatusCode, body)
	}
	// The owner is still signed in and still on the original password.
	if resp, _ := h.do(t, http.MethodGet, "/api/library", ""); resp.StatusCode != http.StatusOK {
		t.Errorf("the owner was signed out by a refused reset: %d", resp.StatusCode)
	}
}

// Guessing has to get slower, and the refusal has to cost nothing and say
// when to come back.
func TestRepeatedWrongPasswordsAreThrottled(t *testing.T) {
	h := newHarness(t)
	h.signUp(t)

	wrong := `{"username":"gabe","password":"not the password"}`
	for i := 0; i < 5; i++ {
		if resp, _ := h.do(t, http.MethodPost, "/api/login", wrong); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want 401", i+1, resp.StatusCode)
		}
	}
	resp, body := h.do(t, http.MethodPost, "/api/login", wrong)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("sixth attempt: status = %d, want 429: %s", resp.StatusCode, body)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Error("a throttled sign-in does not say when to retry")
	}
	// Even the right password waits: otherwise the throttle is only a
	// throttle on wrong answers, which a guesser can tell apart for free.
	resp, _ = h.do(t, http.MethodPost, "/api/login", `{"username":"gabe","password":"correct horse"}`)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("the right password skipped the wait: %d", resp.StatusCode)
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
	h := newHarness(t, bookStub{stub: stub{id: "ebooks", kind: media.KindEbook}, books: []string{"dune.epub"}})
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
		`{"setupCode":"`+testSetupCode+`","username":"gabe","password":"`+samPassword+`"}`)
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

// A fraction is a JSON number like any other, so nothing stopped one outside
// [0, 1] before - including the exact trap noted for Audiobookshelf's own
// API: a literal too large for float64 decodes to +Inf without an error.
func TestReadingProgressRejectsABadFraction(t *testing.T) {
	h := newHarness(t, bookStub{stub: stub{id: "ebooks", kind: media.KindEbook}, books: []string{"dune.epub"}})
	h.signUp(t)
	const where = "?source=ebooks&id=dune.epub"

	for _, body := range []string{
		`{"location":"epubcfi(/6/4!/2)","fraction":1e400}`,  // +Inf
		`{"location":"epubcfi(/6/4!/2)","fraction":-1e400}`, // -Inf
		`{"location":"epubcfi(/6/4!/2)","fraction":-0.5}`,
		`{"location":"epubcfi(/6/4!/2)","fraction":1.5}`,
	} {
		resp, respBody := h.do(t, http.MethodPut, "/api/book/progress"+where, body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400 (body %s)", body, resp.StatusCode, respBody)
		}
	}
	// The boundaries themselves are fine.
	for _, f := range []string{"0", "1", "0.5"} {
		resp, respBody := h.do(t, http.MethodPut, "/api/book/progress"+where,
			`{"location":"epubcfi(/6/4!/2)","fraction":`+f+`}`)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("fraction %s: status = %d, want 200 (body %s)", f, resp.StatusCode, respBody)
		}
	}
}

// An EPUB CFI is a few dozen characters. Nothing stopped a client sending
// something enormous, which would sit in state.json - the file every login
// and session change reads and rewrites whole - forever.
// A reading position is only kept for a book that is on the shelf. The id was
// a free string once, and every invented one was another entry in the file each
// request's lock guards - so one member could slow the server for everybody.
func TestReadingProgressOnlyForRealBooks(t *testing.T) {
	h := newHarness(t, bookStub{stub: stub{id: "ebooks", kind: media.KindEbook}, books: []string{"dune.epub"}})
	h.signUp(t)

	resp, _ := h.do(t, http.MethodPut, "/api/book/progress?source=ebooks&id=invented-1.epub",
		`{"location":"epubcfi(/6/4!/2)","fraction":0.1}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("progress for a book that does not exist = %d, want 404", resp.StatusCode)
	}
	resp, _ = h.do(t, http.MethodPut, "/api/book/progress?source=ebooks&id=dune.epub",
		`{"location":"epubcfi(/6/4!/2)","fraction":0.1}`)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("progress for a real book = %d, want 200", resp.StatusCode)
	}
}

func TestReadingProgressRejectsAnOverlongLocation(t *testing.T) {
	h := newHarness(t, bookStub{stub: stub{id: "ebooks", kind: media.KindEbook}, books: []string{"dune.epub"}})
	h.signUp(t)
	const where = "?source=ebooks&id=dune.epub"

	huge := strings.Repeat("a", maxLocationLength+1)
	resp, body := h.do(t, http.MethodPut, "/api/book/progress"+where,
		`{"location":"`+huge+`","fraction":0.1}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (body %s)", resp.StatusCode, body)
	}

	ok := strings.Repeat("a", maxLocationLength)
	resp, body = h.do(t, http.MethodPut, "/api/book/progress"+where,
		`{"location":"`+ok+`","fraction":0.1}`)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("a location right at the limit was refused: %d %s", resp.StatusCode, body)
	}
}

// Reading position took any source/id string and wrote it straight to
// state.json - a member with no library at all could fill the file with
// made-up ids. It now goes through the same source check as everything else,
// and caps how many positions one account may hold.
func TestReadingProgressChecksTheSourceAndItsCaps(t *testing.T) {
	h := newHarness(t, stub{id: "ebooks", kind: media.KindEbook})
	h.signUp(t)

	// A source that does not exist is refused.
	resp, _ := h.do(t, http.MethodPut, "/api/book/progress?source=made-up&id=x",
		`{"location":"epubcfi(/6/4!/2)","fraction":0.1}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown source: status %d, want 404", resp.StatusCode)
	}

	// A member who cannot see ebooks cannot store a position against it.
	id := h.addMember(t, "sam", samPassword)
	h.setLibraries(t, id, `{"libraries":["music"]}`)
	sam := h.asUser(t, "sam", samPassword)
	resp, _ = sam.do(t, http.MethodPut, "/api/book/progress?source=ebooks&id=x",
		`{"location":"epubcfi(/6/4!/2)","fraction":0.1}`)
	if resp.StatusCode == http.StatusOK {
		t.Errorf("a restricted member stored progress against a forbidden source: %d", resp.StatusCode)
	}

	// An over-long id is refused before it can bloat the file.
	huge := "?source=ebooks&id=" + strings.Repeat("a", 2000)
	resp, _ = h.do(t, http.MethodPut, "/api/book/progress"+huge,
		`{"location":"epubcfi(/6/4!/2)","fraction":0.1}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("over-long id: status %d, want 400", resp.StatusCode)
	}
}

// End to end, and the Docker Desktop case exactly: every client here arrives
// from 127.0.0.1. A stranger guessing at the owner's name puts both the address
// and the account into backoff, which used to refuse the owner too. The owner's
// browser, which has signed in before and carries the device cookie, still gets
// in; a browser that never has is still held back.
func TestAStrangerGuessingDoesNotLockTheOwnerOut(t *testing.T) {
	h := newHarness(t)
	h.signUp(t) // the owner's browser: signing up marks it as a device the account uses

	jar, _ := cookiejar.New(nil)
	attacker := &harness{srv: h.srv, client: &http.Client{Jar: jar}, root: h.root}
	throttled := false
	for i := 0; i < 20 && !throttled; i++ {
		resp, _ := attacker.do(t, http.MethodPost, "/api/login", `{"username":"gabe","password":"not it"}`)
		throttled = resp.StatusCode == http.StatusTooManyRequests
	}
	if !throttled {
		t.Fatal("the stranger was never throttled; the test proves nothing")
	}

	jar2, _ := cookiejar.New(nil)
	newDevice := &harness{srv: h.srv, client: &http.Client{Jar: jar2}, root: h.root}
	if resp, _ := newDevice.do(t, http.MethodPost, "/api/login", `{"username":"gabe","password":"correct horse"}`); resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("a never-seen device = %d, want 429 while an attack runs", resp.StatusCode)
	}

	h.do(t, http.MethodPost, "/api/logout", "")
	resp, body := h.do(t, http.MethodPost, "/api/login", `{"username":"gabe","password":"correct horse"}`)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("the owner's own browser = %d, want 200 despite the stranger: %s", resp.StatusCode, body)
	}
}
