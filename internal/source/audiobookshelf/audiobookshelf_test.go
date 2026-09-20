package audiobookshelf

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gabehollberg/soundstorm/internal/media"
)

// LibriVox catalogue entries carry HTML entities in plain-text fields, and
// Audiobookshelf stores what it is given. Without decoding, a reader sees
// "Las F&aacute;bulas de Esopo" instead of the accent. Seen in real data.
func TestHTMLEntitiesAreDecoded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"book":[{"libraryItem":{"id":"1","media":{"duration":60,
		  "metadata":{"title":"Las F&aacute;bulas de Esopo, Vol. 01",
		              "subtitle":"Cuentos &amp; Fábulas",
		              "authorName":"Esopo &amp; otros"}}}}]}`))
	}))
	defer srv.Close()

	s, err := New(Config{ID: "abs", BaseURL: srv.URL, Token: "t", LibraryID: "lib"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	items, err := s.Search(context.Background(), media.Query{Text: "esopo"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}

	got := items[0]
	if got.Title != "Las Fábulas de Esopo, Vol. 01" {
		t.Errorf("title = %q, entities not decoded", got.Title)
	}
	if got.Subtitle != "Cuentos & Fábulas" {
		t.Errorf("subtitle = %q", got.Subtitle)
	}
	if len(got.Creators) != 1 || got.Creators[0] != "Esopo & otros" {
		t.Errorf("creators = %v", got.Creators)
	}
}
