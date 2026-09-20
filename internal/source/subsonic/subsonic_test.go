package subsonic

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gabehollberg/atrium/internal/media"
)

// A real search3.view response, trimmed to the fields the adapter reads.
const searchBody = `{"subsonic-response":{"status":"ok","version":"1.16.1",
  "searchResult3":{"song":[
    {"id":"300","title":"Sleep Walk","album":"Santo & Johnny","artist":"Santo & Johnny",
     "year":1959,"duration":141,"coverArt":"al-42","suffix":"flac"}
  ]}}}`

func newTestSource(t *testing.T, handler http.HandlerFunc) (*Source, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	s, err := New(Config{ID: "navidrome", BaseURL: srv.URL, Username: "u", Password: "p"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s, srv
}

func TestSearchMapsSongFields(t *testing.T) {
	s, _ := newTestSource(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/search3.view" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		// The salted-token scheme must send u, t and s - never the password.
		q := r.URL.Query()
		for _, key := range []string{"u", "t", "s", "v", "c"} {
			if q.Get(key) == "" {
				t.Errorf("missing auth parameter %q", key)
			}
		}
		if q.Get("p") != "" {
			t.Error("the plaintext password must not be sent")
		}
		if got := q.Get("query"); got != "sleep walk" {
			t.Errorf("query = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(searchBody))
	})

	items, err := s.Search(context.Background(), media.Query{Text: "sleep walk"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}

	got := items[0]
	if got.Title != "Sleep Walk" {
		t.Errorf("title = %q", got.Title)
	}
	if got.Kind != media.KindMusic {
		t.Errorf("kind = %q", got.Kind)
	}
	if got.SourceID != "navidrome" {
		t.Errorf("sourceId = %q", got.SourceID)
	}
	if len(got.Creators) != 1 || got.Creators[0] != "Santo & Johnny" {
		t.Errorf("creators = %v", got.Creators)
	}
	if got.Year != 1959 {
		t.Errorf("year = %d", got.Year)
	}
	if got.DurationSeconds != 141 {
		t.Errorf("duration = %v", got.DurationSeconds)
	}
	if got.OpenURL == "" {
		t.Error("want a stream URL")
	}
	if got.CoverURL == "" {
		t.Error("want a cover URL when coverArt is present")
	}
	if got.Extra["format"] != "flac" {
		t.Errorf("format = %q", got.Extra["format"])
	}
}

// A Subsonic error arrives as HTTP 200 with an error object, so the adapter
// has to read the envelope rather than trusting the status code.
func TestSearchSurfacesSubsonicErrorEnvelope(t *testing.T) {
	s, _ := newTestSource(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"subsonic-response":{"status":"failed",
		  "error":{"code":40,"message":"Wrong username or password"}}}`))
	})

	if _, err := s.Search(context.Background(), media.Query{Text: "x"}); err == nil {
		t.Fatal("want an error from a failed subsonic envelope")
	}
}

func TestSearchSurfacesHTTPFailure(t *testing.T) {
	s, _ := newTestSource(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusBadGateway)
	})
	if _, err := s.Search(context.Background(), media.Query{Text: "x"}); err == nil {
		t.Fatal("want an error on a 502")
	}
}

func TestNewRejectsMissingCredentials(t *testing.T) {
	if _, err := New(Config{ID: "x", BaseURL: "https://example.com"}); err == nil {
		t.Error("want an error when credentials are missing")
	}
}

func TestNewRejectsNonHTTPURL(t *testing.T) {
	if _, err := New(Config{ID: "x", BaseURL: "ftp://example.com", Username: "u", Password: "p"}); err == nil {
		t.Error("want an error for a non-http base url")
	}
}
