package jellyfin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/GabrielHollberg/soundstorm/internal/media"
)

// recordItems captures the query string /Items was called with.
func recordItems(t *testing.T) (*Source, *url.Values) {
	t.Helper()
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"Items": []any{}})
	}))
	t.Cleanup(srv.Close)

	s, err := New(Config{ID: "jellyfin", BaseURL: srv.URL, Token: "t", UserID: "u", ItemTypes: "Movie"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s, &got
}

// An empty query must not be sent as searchTerm=.
//
// /Items with no searchTerm is Jellyfin's own browse and returns the library.
// Asking it to match the empty string is a different question, and not one it
// answers usefully - so the parameter is omitted rather than blanked.
func TestBrowseOmitsSearchTerm(t *testing.T) {
	s, got := recordItems(t)
	if _, err := s.Search(context.Background(), media.Query{}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if _, present := (*got)["searchTerm"]; present {
		t.Errorf("searchTerm was sent on a browse: %v", (*got)["searchTerm"])
	}
	// Nothing to rank a browse by, so name order - and SortName is the field
	// Jellyfin itself sorts on, which ignores a leading "The".
	if (*got).Get("SortBy") != "SortName" {
		t.Errorf("SortBy = %q, want SortName", (*got).Get("SortBy"))
	}
}

// And a real search still sends it, without a sort that would override
// Jellyfin's own relevance ordering.
func TestSearchStillSendsSearchTerm(t *testing.T) {
	s, got := recordItems(t)
	if _, err := s.Search(context.Background(), media.Query{Text: "dune"}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if (*got).Get("searchTerm") != "dune" {
		t.Errorf("searchTerm = %q, want dune", (*got).Get("searchTerm"))
	}
	if _, present := (*got)["SortBy"]; present {
		t.Error("a search should keep Jellyfin's own ordering, not force SortName")
	}
}
