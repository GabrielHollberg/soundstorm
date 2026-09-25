package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/GabrielHollberg/soundstorm/internal/media"
)

// recentStub answers the home page's "what arrived lately".
type recentStub struct {
	stub
	recent []media.Item
	err    error
}

func (r recentStub) Recent(context.Context, int) ([]media.Item, error) { return r.recent, r.err }

// A shelf that fails is left out; the rest of the home page still comes back,
// in the fixed order of shelves.
func TestHomeLeavesOutAShelfThatFails(t *testing.T) {
	books := recentStub{stub: stub{id: "books", kind: media.KindEbook}, recent: []media.Item{{ID: "b1", Title: "Dune", Kind: media.KindEbook}}}
	films := recentStub{stub: stub{id: "films", kind: media.KindVideo}, err: errors.New("jellyfin is down")}
	photos := recentStub{stub: stub{id: "photos", kind: media.KindPicture}, recent: []media.Item{{ID: "p1", Title: "IMG_1", Kind: media.KindPicture}}}
	h := newHarness(t, books, films, photos)
	h.signUp(t)

	resp, body := h.do(t, http.MethodGet, "/api/home", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/home = %d %s", resp.StatusCode, body)
	}
	var out struct {
		Shelves []struct {
			Kind  string       `json:"kind"`
			Items []media.Item `json:"items"`
		} `json:"shelves"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, s := range out.Shelves {
		kinds = append(kinds, s.Kind)
	}
	if len(kinds) != 2 || kinds[0] != "ebook" || kinds[1] != "picture" {
		t.Errorf("shelves = %v, want [ebook picture]: the failing films shelf left out, the rest in order", kinds)
	}
}
