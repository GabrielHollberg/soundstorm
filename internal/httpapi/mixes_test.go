package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/source"
)

// mixShelf can list albums and draw songs at random.
type mixShelf struct{ albumShelf }

func (mixShelf) RandomSongs(_ context.Context, n int, genre string, from, to int) ([]media.Item, error) {
	var out []media.Item
	for _, id := range []string{"r1", "r2", "r3", "r4", "r5", "r6"} {
		out = append(out, media.Item{ID: id, SourceID: "navidrome", Kind: media.KindMusic, Title: id, ArtID: "al-" + id})
	}
	return out, nil
}
func (mixShelf) Genres(context.Context) ([]source.Genre, error) {
	return []source.Genre{{Name: "Test", SongCount: 9}, {Name: "Tiny", SongCount: 2}}, nil
}

func TestMixesFromListeningAndTheLibrary(t *testing.T) {
	h := newHarness(t, mixShelf{}, bookShelf{})
	h.signUp(t)
	for _, id := range []string{"a", "b", "b", "b", "c", "c"} {
		if resp, body := h.do(t, http.MethodPost, "/api/history", `{"source":"navidrome","id":"`+id+`"}`); resp.StatusCode != http.StatusOK {
			t.Fatalf("record %s: %d %s", id, resp.StatusCode, body)
		}
	}
	if resp, _ := h.do(t, http.MethodPost, "/api/history", `{"source":"ebooks","id":"b1"}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a book recorded as a play: %d", resp.StatusCode)
	}

	_, body := h.do(t, http.MethodGet, "/api/music/mixes", "")
	var list struct{ Mixes []struct{ ID string } }
	_ = json.Unmarshal(body, &list)
	ids := map[string]bool{}
	for _, m := range list.Mixes {
		ids[m.ID] = true
	}
	for _, want := range []string{"most-played", "recently-played", "shuffle", "recently-added", "genre:Test"} {
		if !ids[want] {
			t.Errorf("no %q mix in %v", want, ids)
		}
	}
	if ids["genre:Tiny"] {
		t.Error("a genre with two songs got a mix")
	}

	_, body = h.do(t, http.MethodGet, "/api/music/mixes/most-played", "")
	var mix struct {
		Songs []struct{ ID string } `json:"songs"`
	}
	_ = json.Unmarshal(body, &mix)
	if len(mix.Songs) != 3 || mix.Songs[0].ID != "b" || mix.Songs[1].ID != "c" {
		t.Errorf("most played = %+v, want b, c, a", mix.Songs)
	}

	// Somebody else's listening is not in your mixes.
	h.addMember(t, "sam", "sam-password-1")
	sam := h.asUser(t, "sam", "sam-password-1")
	_, body = sam.do(t, http.MethodGet, "/api/music/mixes/most-played", "")
	_ = json.Unmarshal(body, &mix)
	if len(mix.Songs) != 0 {
		t.Errorf("sam's most played has the owner's %d songs", len(mix.Songs))
	}
}
