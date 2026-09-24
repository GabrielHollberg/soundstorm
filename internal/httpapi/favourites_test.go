package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/GabrielHollberg/soundstorm/internal/collections"
	"github.com/GabrielHollberg/soundstorm/internal/media"
)

func testCollections(t *testing.T) *collections.Store {
	t.Helper()
	c, err := collections.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// songShelf is a music source that can describe its songs.
type songShelf struct{}

func (songShelf) ID() string                                                { return "navidrome" }
func (songShelf) Kind() media.Kind                                          { return media.KindMusic }
func (songShelf) Health(context.Context) error                              { return nil }
func (songShelf) Search(context.Context, media.Query) ([]media.Item, error) { return nil, nil }
func (songShelf) ItemByID(_ context.Context, id string) (media.Item, bool) {
	if id == "missing" {
		return media.Item{}, false
	}
	return media.Item{ID: id, SourceID: "navidrome", Kind: media.KindMusic, Title: "Song " + id}, true
}

func TestFavouritesAreServerCheckedAndPerPerson(t *testing.T) {
	h := newHarness(t, songShelf{}, bookShelf{})
	h.signUp(t)

	if resp, body := h.do(t, http.MethodPut, "/api/favourites?source=navidrome&id=s1", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("favourite: %d %s", resp.StatusCode, body)
	}
	if resp, _ := h.do(t, http.MethodPut, "/api/favourites?source=navidrome&id=missing", ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a song that does not exist = %d, want 404", resp.StatusCode)
	}
	h.do(t, http.MethodPut, "/api/favourites?source=ebooks&id=b1", "")

	_, body := h.do(t, http.MethodGet, "/api/favourites", "")
	var out struct {
		Items []struct{ ID, Title string } `json:"items"`
	}
	_ = json.Unmarshal(body, &out)
	if len(out.Items) != 2 || out.Items[0].ID != "b1" || out.Items[1].Title != "Song s1" {
		t.Fatalf("favourites = %+v, want the book then the song, titled by the server", out.Items)
	}

	h.addMember(t, "sam", "sam-password-1")
	sam := h.asUser(t, "sam", "sam-password-1")
	_, body = sam.do(t, http.MethodGet, "/api/favourites", "")
	var theirs struct{ Items []json.RawMessage }
	_ = json.Unmarshal(body, &theirs)
	if len(theirs.Items) != 0 {
		t.Errorf("sam sees %d of the owner's favourites", len(theirs.Items))
	}

	h.do(t, http.MethodDelete, "/api/favourites?source=navidrome&id=s1", "")
	_, body = h.do(t, http.MethodGet, "/api/favourites", "")
	_ = json.Unmarshal(body, &out)
	if len(out.Items) != 1 {
		t.Errorf("after removing one, %d left", len(out.Items))
	}
}

// A favourite kept before the owner took a shelf away is not a way back in.
func TestFavouritesOnAShelfYouCannotSeeAreHidden(t *testing.T) {
	h := newHarness(t, songShelf{}, bookShelf{})
	h.signUp(t)
	id := h.addMember(t, "sam", "sam-password-1")
	sam := h.asUser(t, "sam", "sam-password-1")
	sam.do(t, http.MethodPut, "/api/favourites?source=ebooks&id=b1", "")
	sam.do(t, http.MethodPut, "/api/favourites?source=navidrome&id=s1", "")

	h.setLibraries(t, id, `{"libraries":["music"]}`)
	_, body := sam.do(t, http.MethodGet, "/api/favourites", "")
	var out struct {
		Items []struct{ ID string } `json:"items"`
	}
	_ = json.Unmarshal(body, &out)
	if len(out.Items) != 1 || out.Items[0].ID != "s1" {
		t.Errorf("favourites = %+v, want only the song", out.Items)
	}
}

func TestPlaylistsHoldSongsInOrder(t *testing.T) {
	h := newHarness(t, songShelf{}, bookShelf{})
	h.signUp(t)

	resp, body := h.do(t, http.MethodPost, "/api/playlists", `{"name":"Road trip"}`)
	var p struct{ ID string }
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &p) != nil || p.ID == "" {
		t.Fatalf("create: %d %s", resp.StatusCode, body)
	}
	for _, id := range []string{"a", "b", "c"} {
		if resp, body := h.do(t, http.MethodPost, "/api/playlists/"+p.ID+"/items", `{"source":"navidrome","id":"`+id+`"}`); resp.StatusCode != http.StatusOK {
			t.Fatalf("add %s: %d %s", id, resp.StatusCode, body)
		}
	}
	if resp, _ := h.do(t, http.MethodPost, "/api/playlists/"+p.ID+"/items", `{"source":"ebooks","id":"b1"}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a book in a playlist = %d, want 400", resp.StatusCode)
	}
	h.do(t, http.MethodPost, "/api/playlists/"+p.ID+"/move", `{"from":2,"to":0}`)
	h.do(t, http.MethodDelete, "/api/playlists/"+p.ID+"/items/1", "")

	_, body = h.do(t, http.MethodGet, "/api/playlists/"+p.ID, "")
	var got struct {
		Name  string
		Items []struct{ ID string } `json:"items"`
	}
	_ = json.Unmarshal(body, &got)
	order := ""
	for _, it := range got.Items {
		order += it.ID
	}
	if got.Name != "Road trip" || order != "cb" {
		t.Errorf("playlist = %q %q, want Road trip with c then b", got.Name, order)
	}

	h.addMember(t, "sam", "sam-password-1")
	sam := h.asUser(t, "sam", "sam-password-1")
	if resp, _ := sam.do(t, http.MethodGet, "/api/playlists/"+p.ID, ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("sam opening the owner's playlist = %d, want 404", resp.StatusCode)
	}
}
