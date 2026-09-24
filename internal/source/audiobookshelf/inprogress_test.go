package audiobookshelf

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Shapes from Audiobookshelf 2.36.1: /api/me/items-in-progress lists library
// items with progressLastUpdate, and /api/me carries mediaProgress. A book
// finished, one hidden from continue listening, one missing from disk and a
// podcast episode must all stay out of the row; the one really in progress
// comes back with its fraction and when it was last touched.
func TestInProgressKeepsOnlyBooksBeingListenedTo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/me/items-in-progress":
			_, _ = w.Write([]byte(`{"libraryItems":[
				{"id":"reading","progressLastUpdate":1790144306309,"media":{"metadata":{"title":"As a Man Thinketh","authorName":"James Allen"}}},
				{"id":"done","progressLastUpdate":1790144000000,"media":{"metadata":{"title":"Finished"}}},
				{"id":"hidden","progressLastUpdate":1790143000000,"media":{"metadata":{"title":"Hidden"}}},
				{"id":"gone","isMissing":true,"progressLastUpdate":1790142000000,"media":{"metadata":{"title":"Gone"}}}
			]}`))
		case "/api/me":
			_, _ = w.Write([]byte(`{"mediaProgress":[
				{"libraryItemId":"reading","progress":0.42,"isFinished":false,"hideFromContinueListening":false},
				{"libraryItemId":"done","progress":1,"isFinished":true},
				{"libraryItemId":"hidden","progress":0.3,"hideFromContinueListening":true},
				{"libraryItemId":"gone","progress":0.5},
				{"libraryItemId":"podcast","episodeId":"ep1","progress":0.5}
			]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	started, err := newTestSource(t, srv.URL).InProgress(context.Background(), 12)
	if err != nil {
		t.Fatalf("InProgress: %v", err)
	}
	if len(started) != 1 {
		t.Fatalf("got %d items, want only the book in progress: %+v", len(started), started)
	}
	got := started[0]
	if got.Item.ID != "reading" || got.Item.Title != "As a Man Thinketh" {
		t.Errorf("item = %+v", got.Item)
	}
	if got.Fraction != 0.42 {
		t.Errorf("fraction = %v, want 0.42", got.Fraction)
	}
	if !got.At.Equal(time.UnixMilli(1790144306309)) {
		t.Errorf("at = %v", got.At)
	}
}
