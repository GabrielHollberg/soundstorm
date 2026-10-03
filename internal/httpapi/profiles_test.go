package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

type profileList struct {
	People []struct {
		ID, Name, Needs string
		Owner           bool
	}
	Current string
}

func profilesOn(t *testing.T, h *harness) profileList {
	t.Helper()
	resp, body := h.do(t, http.MethodGet, "/api/profiles", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("profiles: %d %s", resp.StatusCode, body)
	}
	var out profileList
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func (p profileList) id(name string) string {
	for _, person := range p.People {
		if person.Name == name {
			return person.ID
		}
	}
	return ""
}

func switchTo(t *testing.T, h *harness, id, extra string) int {
	t.Helper()
	resp, _ := h.do(t, http.MethodPost, "/api/profiles/switch", `{"id":"`+id+`"`+extra+`}`)
	return resp.StatusCode
}

// A shared TV keeps the people who asked to be kept, and switches between
// them: nothing needed for somebody without a PIN, their PIN once they set
// one, and the owner's password while the owner has no PIN.
func TestASharedDeviceSwitchesBetweenItsPeople(t *testing.T) {
	h := newHarness(t)
	h.signUp(t)
	if resp, body := h.do(t, http.MethodPost, "/api/users", `{"username":"sam","password":"violet tractor glacier"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("adding sam: %d %s", resp.StatusCode, body)
	}

	tv := h.another(t)
	tv.do(t, http.MethodPost, "/api/login", `{"username":"gabe","password":"correct horse","keep":true}`)
	tv.do(t, http.MethodPost, "/api/login", `{"username":"sam","password":"violet tractor glacier","keep":true}`)
	list := profilesOn(t, tv)
	if len(list.People) != 2 || list.People[0].Name != "sam" {
		t.Fatalf("the TV should keep both, the latest first: %+v", list)
	}
	gabe, sam := list.id("gabe"), list.id("sam")
	if list.Current != sam {
		t.Fatalf("sam signed in last: %+v", list)
	}
	for _, p := range list.People {
		if want := map[string]string{"gabe": "password", "sam": ""}[p.Name]; p.Needs != want {
			t.Fatalf("%s needs %q, want %q", p.Name, p.Needs, want)
		}
	}

	// The owner, without a PIN, needs the password.
	if code := switchTo(t, tv, gabe, ""); code != http.StatusForbidden {
		t.Fatalf("switching to the owner with nothing: %d", code)
	}
	if code := switchTo(t, tv, gabe, `,"password":"correct horse"`); code != http.StatusOK {
		t.Fatalf("switching to the owner with the password: %d", code)
	}
	if profilesOn(t, tv).Current != gabe {
		t.Fatal("the TV should be the owner now")
	}
	// Sam, without a PIN, needs nothing.
	if code := switchTo(t, tv, sam, ""); code != http.StatusOK || profilesOn(t, tv).Current != sam {
		t.Fatalf("switching to sam: %d", code)
	}

	// Sam sets a PIN; now it is needed.
	if resp, body := tv.do(t, http.MethodPut, "/api/account/pin", `{"password":"violet tractor glacier","pin":"2468"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("setting a PIN: %d %s", resp.StatusCode, body)
	}
	if resp, _ := tv.do(t, http.MethodPut, "/api/account/pin", `{"password":"wrong one","pin":"1111"}`); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a PIN set without the password: %d", resp.StatusCode)
	}
	if code := switchTo(t, tv, sam, `,"pin":"1357"`); code != http.StatusForbidden {
		t.Fatalf("a wrong PIN: %d", code)
	}
	if code := switchTo(t, tv, sam, `,"pin":"2468"`); code != http.StatusOK {
		t.Fatalf("the right PIN: %d", code)
	}

	// Another device, which nobody kept themselves on, switches to nobody.
	stranger := h.another(t)
	if code := switchTo(t, stranger, sam, ""); code != http.StatusNotFound {
		t.Fatalf("a device without sam kept on it: %d", code)
	}
}

// Wrong PINs are limited: a few, then a wait - counted before they are
// checked, so a burst is no way round it.
func TestWrongPINsWait(t *testing.T) {
	h := newHarness(t)
	h.signUp(t)
	h.do(t, http.MethodPut, "/api/account/pin", `{"password":"correct horse","pin":"2468"}`)
	tv := h.another(t)
	tv.do(t, http.MethodPost, "/api/login", `{"username":"gabe","password":"correct horse","keep":true}`)
	gabe := profilesOn(t, tv).id("gabe")
	waited := false
	for i := 0; i < 8; i++ {
		if switchTo(t, tv, gabe, `,"pin":"0000"`) == http.StatusTooManyRequests {
			waited = true
			break
		}
	}
	if !waited {
		t.Fatal("eight wrong PINs in a row were all checked")
	}
	if code := switchTo(t, tv, gabe, `,"pin":"2468"`); code != http.StatusTooManyRequests {
		t.Fatalf("while waiting, even the right PIN waits: %d", code)
	}
}

// Off a device: signing out there, a new password, or taking yourself off.
// Switching keeps everybody.
func TestPeopleLeaveADevice(t *testing.T) {
	h := newHarness(t)
	h.signUp(t)
	h.do(t, http.MethodPost, "/api/users", `{"username":"sam","password":"violet tractor glacier"}`)
	tv := h.another(t)
	tv.do(t, http.MethodPost, "/api/login", `{"username":"gabe","password":"correct horse","keep":true}`)
	tv.do(t, http.MethodPost, "/api/login", `{"username":"sam","password":"violet tractor glacier","keep":true}`)

	// Signing out on the TV takes sam off it.
	tv.do(t, http.MethodPost, "/api/logout", "")
	if list := profilesOn(t, tv); len(list.People) != 1 || list.People[0].Name != "gabe" {
		t.Fatalf("sam signed out, so only gabe is kept: %+v", list)
	}

	// A new password takes somebody off every device.
	tv.do(t, http.MethodPost, "/api/login", `{"username":"sam","password":"violet tractor glacier","keep":true}`)
	if resp, body := tv.do(t, http.MethodPost, "/api/account/password", `{"current":"violet tractor glacier","password":"orange kayak meadow"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("changing sam's password: %d %s", resp.StatusCode, body)
	}
	if list := profilesOn(t, tv); list.id("sam") != "" {
		t.Fatalf("a new password should take sam off: %+v", list)
	}

	// Taking yourself off.
	gabe := profilesOn(t, tv).id("gabe")
	if code := switchTo(t, tv, gabe, `,"password":"correct horse"`); code != http.StatusOK {
		t.Fatalf("switching to gabe: %d", code)
	}
	if resp, _ := tv.do(t, http.MethodDelete, "/api/profiles/"+gabe, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("taking gabe off: %d", resp.StatusCode)
	}
	if list := profilesOn(t, tv); len(list.People) != 0 {
		t.Fatalf("nobody should be kept now: %+v", list)
	}
}

// A person's own picture: set by them, shown on "Who's listening?" of a
// device that keeps them before anybody is signed in there, and to nobody
// else signed out.
func TestAProfilePictureShowsWhereThePersonIsKept(t *testing.T) {
	h := newHarness(t)
	h.signUp(t)
	png := "\x89PNG\r\n\x1a\n" + strings.Repeat("x", 64)
	if resp, body := h.do(t, http.MethodPut, "/api/account/picture", png); resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "/picture?v=") {
		t.Fatalf("setting a picture: %d %s", resp.StatusCode, body)
	}
	if resp, _ := h.do(t, http.MethodPut, "/api/account/picture", "<svg onload=alert(1)>"); resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("an SVG was taken as a picture: %d", resp.StatusCode)
	}

	h.do(t, http.MethodPost, "/api/users", `{"username":"sam","password":"violet tractor glacier"}`)
	tv := h.another(t)
	tv.do(t, http.MethodPost, "/api/login", `{"username":"gabe","password":"correct horse","keep":true}`)
	tv.do(t, http.MethodPost, "/api/login", `{"username":"sam","password":"violet tractor glacier","keep":true}`)
	tv.do(t, http.MethodPost, "/api/logout", "") // sam leaves; gabe is still kept, nobody signed in
	_, body := tv.do(t, http.MethodGet, "/api/profiles", "")
	var list struct {
		People []struct{ ID, Picture string }
	}
	json.Unmarshal(body, &list)
	if len(list.People) != 1 || list.People[0].Picture == "" {
		t.Fatalf("the kept person should carry their picture: %s", body)
	}
	pic := list.People[0].Picture
	if resp, got := tv.do(t, http.MethodGet, pic, ""); resp.StatusCode != http.StatusOK || string(got) != png || resp.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("the device keeping them should get the picture: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if resp, _ := h.another(t).do(t, http.MethodGet, pic, ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a stranger got the picture: %d", resp.StatusCode)
	}

	h.do(t, http.MethodDelete, "/api/account/picture", "")
	if _, body := tv.do(t, http.MethodGet, "/api/profiles", ""); strings.Contains(string(body), "picture") {
		t.Fatalf("taken away, the picture should be gone: %s", body)
	}
}
