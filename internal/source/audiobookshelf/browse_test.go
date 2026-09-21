package audiobookshelf

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/GabrielHollberg/soundstorm/internal/media"
)

// Audiobookshelf's search does not answer "what is on this shelf" - asked with
// an empty q it matches nothing at all. /items is its listing call, and returns
// the same library items one wrapper shallower, so the two need different
// paths and different decoding.
func TestBrowseUsesTheItemsEndpoint(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/items") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results": []any{map[string]any{
					"id":    "li_1",
					"media": map[string]any{"metadata": map[string]any{"title": "Listed Book"}},
				}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"book": []any{}})
	}))
	defer srv.Close()

	s, err := New(Config{ID: "abs", BaseURL: srv.URL, Token: "t", LibraryID: "lib"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	items, err := s.Search(context.Background(), media.Query{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !strings.HasSuffix(path, "/items") {
		t.Errorf("browse called %q, want the /items endpoint", path)
	}
	// The listing response nests one level less than search does, so decoding
	// it with the search shape would yield an empty list and look like an
	// empty library rather than a bug.
	if len(items) != 1 || items[0].Title != "Listed Book" {
		t.Fatalf("items = %+v, want the one listed book decoded", items)
	}
	if items[0].Kind != media.KindAudiobook {
		t.Errorf("kind = %q", items[0].Kind)
	}
}

// A real search still goes to /search.
func TestSearchStillUsesTheSearchEndpoint(t *testing.T) {
	var path, query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, query = r.URL.Path, r.URL.Query().Get("q")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"book": []any{}})
	}))
	defer srv.Close()

	s, err := New(Config{ID: "abs", BaseURL: srv.URL, Token: "t", LibraryID: "lib"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := s.Search(context.Background(), media.Query{Text: "earthsea"}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !strings.HasSuffix(path, "/search") || query != "earthsea" {
		t.Errorf("search hit %q with q=%q", path, query)
	}
}
