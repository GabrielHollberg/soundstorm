package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/GabrielHollberg/soundstorm/internal/media"
)

// An empty q is a browse, not a mistake.
//
// It used to be a 400, which made "show me everything on this shelf" a thing
// the UI could not ask for - so picking a filter with nothing typed showed
// nothing at all.
func TestEmptyQueryListsTheShelf(t *testing.T) {
	music := stub{id: "navidrome", kind: media.KindMusic, items: []media.Item{
		{ID: "m1", SourceID: "navidrome", Kind: media.KindMusic, Title: "Zebra"},
		{ID: "m2", SourceID: "navidrome", Kind: media.KindMusic, Title: "Apple"},
	}}
	books := stub{id: "ebooks", kind: media.KindEbook, items: []media.Item{
		{ID: "b1", SourceID: "ebooks", Kind: media.KindEbook, Title: "Middle"},
	}}
	h := newHarness(t, music, books)
	h.signUp(t)

	resp, body := h.do(t, http.MethodGet, "/api/search?q=", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.StatusCode, body)
	}
	var out struct {
		Items []struct {
			Title string `json:"title"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Items) != 3 {
		t.Fatalf("got %d items, want all 3", len(out.Items))
	}
	// Nothing to rank a browse by, so relevance scores every item 0 and the
	// merged list falls through to its title tiebreak. Alphabetical is the
	// right order for a list nobody asked a question of - and it is what the
	// existing sort already did, which is why this needed no new code.
	want := []string{"Apple", "Middle", "Zebra"}
	for i, w := range want {
		if out.Items[i].Title != w {
			t.Errorf("item %d = %q, want %q (browse should be alphabetical)", i, out.Items[i].Title, w)
		}
	}
}

// Missing entirely, not merely empty - the UI sends no q at all on first load.
func TestBrowseWithNoQueryParameterAtAll(t *testing.T) {
	music := stub{id: "navidrome", kind: media.KindMusic, items: []media.Item{
		{ID: "m1", SourceID: "navidrome", Kind: media.KindMusic, Title: "Something"},
	}}
	h := newHarness(t, music)
	h.signUp(t)

	resp, body := h.do(t, http.MethodGet, "/api/search", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.StatusCode, body)
	}
}

// Browsing is still a search as far as permission is concerned. An empty query
// must not become a way around the shelves an account cannot see.
func TestBrowseHonoursLibraryRestrictions(t *testing.T) {
	h := fullHouse(t)
	h.signUp(t)
	id := h.addMember(t, "sam", samPassword)
	h.setLibraries(t, id, `{"libraries":["music"]}`)
	sam := h.asUser(t, "sam", samPassword)

	_, body := sam.do(t, http.MethodGet, "/api/search?q=", "")
	var out struct {
		Items []struct {
			Kind string `json:"kind"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, item := range out.Items {
		if item.Kind != string(media.KindMusic) {
			t.Errorf("a music-only account browsed up a %s", item.Kind)
		}
	}
}

// And an unknown kind is still rejected, browse or not.
func TestBrowseStillRejectsAnUnknownKind(t *testing.T) {
	h := newHarness(t)
	h.signUp(t)
	resp, _ := h.do(t, http.MethodGet, "/api/search?q=&kind=sculpture", "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}
