package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/source"
)

// albumShelf is a music source that knows albums and artists.
type albumShelf struct{ songShelf }

func (albumShelf) Albums(_ context.Context, order string, offset, _ int) ([]source.Album, error) {
	if offset > 0 {
		return nil, nil
	}
	return []source.Album{{ID: "al1", SourceID: "navidrome", Title: "Northern Lights", Artist: "Aurora Lane", SongCount: 3}}, nil
}
func (albumShelf) Album(_ context.Context, id string) (source.Album, []media.Item, error) {
	return source.Album{ID: id, SourceID: "navidrome", Title: "Northern Lights"},
		[]media.Item{{ID: "s1", SourceID: "navidrome", Kind: media.KindMusic, Title: "First Light"}}, nil
}
func (albumShelf) Artists(context.Context) ([]source.Artist, error) {
	return []source.Artist{{ID: "ar1", SourceID: "navidrome", Name: "Aurora Lane", AlbumCount: 2}}, nil
}
func (albumShelf) Artist(_ context.Context, id string) (source.Artist, []source.Album, error) {
	return source.Artist{ID: id, Name: "Aurora Lane"}, []source.Album{{ID: "al1", Title: "Northern Lights"}}, nil
}
func (albumShelf) SearchMusic(context.Context, string) ([]source.Album, []source.Artist, error) {
	return nil, nil, nil
}

func TestAlbumsAndArtists(t *testing.T) {
	h := newHarness(t, albumShelf{}, bookShelf{})
	h.signUp(t)
	for path, want := range map[string]string{
		"/api/music/albums":                "Northern Lights",
		"/api/music/albums/navidrome/al1":  "First Light",
		"/api/music/artists":               "Aurora Lane",
		"/api/music/artists/navidrome/ar1": "Northern Lights",
		"/api/music/albums?q=nothing":      "[]",
	} {
		resp, body := h.do(t, http.MethodGet, path, "")
		if resp.StatusCode != http.StatusOK || !json.Valid(body) || !contains(body, want) {
			t.Errorf("%s = %d %s, want it to mention %q", path, resp.StatusCode, body, want)
		}
	}
}

// An account that may not see music gets no albums either.
func TestAlbumsFollowTheAccountsShelves(t *testing.T) {
	h := newHarness(t, albumShelf{}, bookShelf{})
	h.signUp(t)
	id := h.addMember(t, "sam", "sam-password-1")
	h.setLibraries(t, id, `{"libraries":["ebook"]}`)
	sam := h.asUser(t, "sam", "sam-password-1")
	_, body := sam.do(t, http.MethodGet, "/api/music/albums", "")
	if contains(body, "Northern Lights") {
		t.Errorf("an account without music sees albums: %s", body)
	}
	if resp, _ := sam.do(t, http.MethodGet, "/api/music/albums/navidrome/al1", ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("an account without music opened an album: %d", resp.StatusCode)
	}
}

func contains(body []byte, s string) bool {
	return len(s) == 0 || (len(body) >= len(s) && indexOf(string(body), s) >= 0)
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}
