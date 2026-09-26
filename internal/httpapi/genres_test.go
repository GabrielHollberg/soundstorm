package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/GabrielHollberg/soundstorm/internal/media"
)

// Genres are one per name across a tab's shelves, joined lists split and
// case set aside, most common first; a genre lists what is in it.
func TestGenresAcrossShelves(t *testing.T) {
	audiobooks := stub{id: "audiobookshelf", kind: media.KindAudiobook, items: []media.Item{
		{ID: "a1", SourceID: "audiobookshelf", Kind: media.KindAudiobook, Title: "Dune", Extra: map[string]string{"genre": "Science Fiction, Classics"}},
		{ID: "a2", SourceID: "audiobookshelf", Kind: media.KindAudiobook, Title: "Emma", Extra: map[string]string{"genre": "classics"}},
	}}
	ebooks := stub{id: "ebooks", kind: media.KindEbook, items: []media.Item{
		{ID: "e1", SourceID: "ebooks", Kind: media.KindEbook, Title: "Foundation", Extra: map[string]string{"tags": "Science Fiction"}},
	}}
	h := newHarness(t, audiobooks, ebooks)
	h.signUp(t)
	var list struct {
		Genres []struct {
			Name  string
			Count int
		}
	}
	_, body := h.do(t, http.MethodGet, "/api/genres?kinds=audiobook,ebook", "")
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("%v (%s)", err, body)
	}
	if len(list.Genres) != 2 || list.Genres[0].Count != 2 || list.Genres[1].Count != 2 {
		t.Fatalf("genres = %+v, want Classics 2 and Science Fiction 2", list.Genres)
	}
	var in struct{ Items []struct{ ID string } }
	_, body = h.do(t, http.MethodGet, "/api/genres?kinds=audiobook,ebook&name=science%20fiction", "")
	if err := json.Unmarshal(body, &in); err != nil || len(in.Items) != 2 {
		t.Errorf("science fiction = %s", body)
	}
	if resp, _ := h.do(t, http.MethodGet, "/api/genres", ""); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("no kinds answered %d", resp.StatusCode)
	}
}
