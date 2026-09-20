package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gabehollberg/soundstorm/internal/media"
	"github.com/gabehollberg/soundstorm/internal/source"
)

// fullHouse is one source per media kind, each with something findable in it
// and real bytes behind it, so a restriction can be shown to apply to search
// and to the media itself rather than only to one of them.
func fullHouse(t *testing.T) *harness {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("media bytes"))
	}))
	t.Cleanup(upstream.Close)

	var sources []sourceForTest
	for _, k := range media.AllKinds() {
		sources = append(sources, sourceForTest{kind: k, url: upstream.URL})
	}
	return newHarness(t, asSources(sources)...)
}

type sourceForTest struct {
	kind media.Kind
	url  string
}

func asSources(in []sourceForTest) []source.Source {
	out := make([]source.Source, 0, len(in))
	for _, s := range in {
		out = append(out, stub{
			id:        string(s.kind),
			kind:      s.kind,
			streamURL: s.url,
			items: []media.Item{{
				ID: "item-1", SourceID: string(s.kind), Kind: s.kind, Title: "findable",
			}},
		})
	}
	return out
}

func (h *harness) setLibraries(t *testing.T, id, body string) (*http.Response, []byte) {
	t.Helper()
	return h.do(t, http.MethodPut, "/api/users/"+id+"/libraries", body)
}

func kindsIn(t *testing.T, body []byte) []string {
	t.Helper()
	var result struct {
		Items []struct {
			Kind string `json:"kind"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("decode search: %v", err)
	}
	seen := map[string]bool{}
	var out []string
	for _, i := range result.Items {
		if !seen[i.Kind] {
			seen[i.Kind] = true
			out = append(out, i.Kind)
		}
	}
	return out
}

// The thing a household with children actually asks for.
func TestARestrictedAccountOnlySearchesItsOwnLibraries(t *testing.T) {
	h := fullHouse(t)
	h.signUp(t)
	id := h.addMember(t, "sam", samPassword)

	resp, body := h.setLibraries(t, id, `{"libraries":["music","ebook"]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("set libraries: %d %s", resp.StatusCode, body)
	}
	sam := h.asUser(t, "sam", samPassword)

	_, body = sam.do(t, http.MethodGet, "/api/search?q=findable", "")
	got := kindsIn(t, body)
	if len(got) != 2 {
		t.Fatalf("member searched %v, want music and ebook only: %s", got, body)
	}
	for _, kind := range got {
		if kind != "music" && kind != "ebook" {
			t.Errorf("a restricted account found a %s", kind)
		}
	}

	// The owner is unaffected.
	_, body = h.do(t, http.MethodGet, "/api/search?q=findable", "")
	if n := len(kindsIn(t, body)); n != len(media.AllKinds()) {
		t.Errorf("the owner now sees %d kinds, want %d", n, len(media.AllKinds()))
	}
}

// Asking for a forbidden kind by name must not be a way around it.
func TestAskingForAForbiddenLibraryByNameFindsNothing(t *testing.T) {
	h := fullHouse(t)
	h.signUp(t)
	id := h.addMember(t, "sam", samPassword)
	h.setLibraries(t, id, `{"libraries":["music"]}`)
	sam := h.asUser(t, "sam", samPassword)

	resp, body := sam.do(t, http.MethodGet, "/api/search?q=findable&kind=video", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	if got := kindsIn(t, body); len(got) != 0 {
		t.Errorf("asking for video directly returned %v", got)
	}
}

// Hiding results is not a permission. The bytes are what matter, and a stream
// url is guessable - source id and item id both appear in an ordinary result.
func TestARestrictedAccountCannotReachTheMediaItself(t *testing.T) {
	h := fullHouse(t)
	h.signUp(t)
	id := h.addMember(t, "sam", samPassword)
	h.setLibraries(t, id, `{"libraries":["music"]}`)
	sam := h.asUser(t, "sam", samPassword)

	// Everything that can hand over bytes, metadata or a playable url.
	for _, path := range []string{
		"/api/stream/video/item-1",
		"/api/art/video/item-1",
		"/api/playback/video/item-1",
		"/api/hls/video/main.m3u8",
		"/api/subtitle/video/0",
		"/api/book/manifest?source=ebook&id=item-1",
		"/api/book/resource?source=ebook&id=item-1&path=x",
	} {
		resp, body := sam.do(t, http.MethodGet, path, "")
		if resp.StatusCode == http.StatusOK {
			t.Errorf("%s served a forbidden library: %d %s", path, resp.StatusCode, body)
		}
		if strings.Contains(string(body), "media bytes") {
			t.Errorf("%s handed over the actual media", path)
		}
	}

	// And the library it does have still works, or the test proves nothing.
	resp, _ := sam.do(t, http.MethodGet, "/api/stream/music/item-1", "")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("the allowed library stopped working: %d", resp.StatusCode)
	}
}

// Listing a shelf somebody cannot open, with a count of what is on it, is a
// strange thing to show a child account.
func TestTheFolderGuideHidesForbiddenLibraries(t *testing.T) {
	h := fullHouse(t)
	h.signUp(t)
	id := h.addMember(t, "sam", samPassword)
	h.setLibraries(t, id, `{"libraries":["music"]}`)
	sam := h.asUser(t, "sam", samPassword)

	_, body := sam.do(t, http.MethodGet, "/api/library", "")
	var out struct {
		Folders []struct {
			Kind string `json:"kind"`
		} `json:"folders"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Folders) != 1 || out.Folders[0].Kind != "music" {
		t.Errorf("folders = %+v, want music alone", out.Folders)
	}
}

// The one mistake this feature must not make. An empty list means nothing,
// and if it were ever read back as nil it would mean everything.
func TestAnAccountCanBeAllowedNothingAndThatSticks(t *testing.T) {
	h := fullHouse(t)
	h.signUp(t)
	id := h.addMember(t, "sam", samPassword)

	resp, body := h.setLibraries(t, id, `{"libraries":[]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	sam := h.asUser(t, "sam", samPassword)

	_, body = sam.do(t, http.MethodGet, "/api/search?q=findable", "")
	if got := kindsIn(t, body); len(got) != 0 {
		t.Errorf("an account allowed nothing found %v", got)
	}

	// Read back through the accounts list, which is the round trip that would
	// turn an empty list into a nil one.
	_, body = h.do(t, http.MethodGet, "/api/users", "")
	var list struct {
		Users []struct {
			Name         string   `json:"name"`
			Libraries    []string `json:"libraries"`
			AllLibraries bool     `json:"allLibraries"`
		} `json:"users"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, u := range list.Users {
		if u.Name != "sam" {
			continue
		}
		if u.AllLibraries || len(u.Libraries) != 0 {
			t.Errorf("sam reads back as %+v; the restriction was lost", u)
		}
	}
}

// Sending null puts an account back to seeing everything.
func TestNullGivesEverythingBack(t *testing.T) {
	h := fullHouse(t)
	h.signUp(t)
	id := h.addMember(t, "sam", samPassword)
	h.setLibraries(t, id, `{"libraries":["music"]}`)

	resp, body := h.setLibraries(t, id, `{"libraries":null}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	sam := h.asUser(t, "sam", samPassword)

	_, body = sam.do(t, http.MethodGet, "/api/search?q=findable", "")
	if n := len(kindsIn(t, body)); n != len(media.AllKinds()) {
		t.Errorf("after null the account sees %d kinds, want all %d", n, len(media.AllKinds()))
	}
}

// An owner who locked themselves out of a library would have no way back in,
// since they are the only account that can change these.
func TestTheOwnerCannotBeRestricted(t *testing.T) {
	h := fullHouse(t)
	h.signUp(t)

	resp, _ := h.setLibraries(t, h.ownID(t), `{"libraries":["music"]}`)
	if resp.StatusCode == http.StatusOK {
		t.Error("the owner was restricted and could not undo it")
	}

	_, body := h.do(t, http.MethodGet, "/api/search?q=findable", "")
	if n := len(kindsIn(t, body)); n != len(media.AllKinds()) {
		t.Errorf("the owner now sees %d kinds", n)
	}
}

// A typo should be an error now rather than a library that silently never
// appears.
func TestAnUnknownLibraryNameIsRefused(t *testing.T) {
	h := fullHouse(t)
	h.signUp(t)
	id := h.addMember(t, "sam", samPassword)

	resp, body := h.setLibraries(t, id, `{"libraries":["musik"]}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "musik") {
		t.Errorf("the error does not name the typo: %s", body)
	}
}

// Otherwise the restriction is a suggestion.
func TestAMemberCannotChangeTheirOwnLibraries(t *testing.T) {
	h := fullHouse(t)
	h.signUp(t)
	id := h.addMember(t, "sam", samPassword)
	h.setLibraries(t, id, `{"libraries":["music"]}`)
	sam := h.asUser(t, "sam", samPassword)

	resp, _ := sam.do(t, http.MethodPut, "/api/users/"+id+"/libraries", `{"libraries":null}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}

	_, body := sam.do(t, http.MethodGet, "/api/search?q=findable", "")
	if got := kindsIn(t, body); len(got) != 1 || got[0] != "music" {
		t.Errorf("the member widened their own access: %v", got)
	}
}

// The UI decides which tabs to show from this, so it has to be concrete rather
// than a null that a client has to know means "everything".
func TestSessionSaysWhichLibrariesYouHave(t *testing.T) {
	h := fullHouse(t)
	h.signUp(t)
	id := h.addMember(t, "sam", samPassword)
	h.setLibraries(t, id, `{"libraries":["ebook","music"]}`)
	sam := h.asUser(t, "sam", samPassword)

	_, body := sam.do(t, http.MethodGet, "/api/session", "")
	var out struct {
		User struct {
			Libraries    []string `json:"libraries"`
			AllLibraries bool     `json:"allLibraries"`
		} `json:"user"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.User.AllLibraries {
		t.Error("a restricted account reports having everything")
	}
	// SoundStorm's own order, not the order they were sent in, so a UI built
	// from this does not reshuffle its tabs.
	if strings.Join(out.User.Libraries, ",") != "music,ebook" {
		t.Errorf("libraries = %v", out.User.Libraries)
	}
}
