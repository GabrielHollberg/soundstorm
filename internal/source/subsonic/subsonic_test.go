package subsonic

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gabehollberg/atrium/internal/media"
)

// A real search3.view response, trimmed to the fields the adapter reads.
const searchBody = `{"subsonic-response":{"status":"ok","version":"1.16.1",
  "searchResult3":{"song":[
    {"id":"300","title":"Sleep Walk","album":"Santo & Johnny","artist":"Santo & Johnny",
     "year":1959,"duration":141,"coverArt":"al-42","suffix":"flac"}
  ]}}}`

func newTestSource(t *testing.T, handler http.HandlerFunc) *Source {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	s, err := New(Config{ID: "navidrome", BaseURL: srv.URL, Username: "u", Password: "p"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func TestSearchMapsSongFields(t *testing.T) {
	s := newTestSource(t, func(w http.ResponseWriter, r *http.Request) {
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
	if got.Extra["format"] != "flac" {
		t.Errorf("format = %q", got.Extra["format"])
	}
	// Cover art has its own id space, so it must be carried separately from the
	// song id rather than assumed equal to it.
	if got.ArtID != "al-42" {
		t.Errorf("artId = %q, want the coverArt id al-42", got.ArtID)
	}
}

// An Item must never carry a URL the browser could use to reach Navidrome
// directly: the backends have no published port, and a leaked upstream URL
// would also leak the credentials Subsonic puts in the query string.
func TestItemsCarryNoUpstreamURLs(t *testing.T) {
	s := newTestSource(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(searchBody))
	})

	items, err := s.Search(context.Background(), media.Query{Text: "x"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, field := range items[0].Extra {
		if strings.Contains(field, "http://") || strings.Contains(field, "https://") {
			t.Errorf("Extra leaked an upstream URL: %q", field)
		}
	}
}

func TestStreamAndArtTargetsAreAuthenticated(t *testing.T) {
	s := newTestSource(t, func(http.ResponseWriter, *http.Request) {})

	streamTarget, err := s.StreamTarget("300")
	if err != nil {
		t.Fatalf("StreamTarget: %v", err)
	}
	u, err := url.Parse(streamTarget.URL)
	if err != nil {
		t.Fatalf("parse stream url: %v", err)
	}
	if u.Path != "/rest/stream.view" {
		t.Errorf("stream path = %q", u.Path)
	}
	q := u.Query()
	if q.Get("id") != "300" {
		t.Errorf("stream id = %q", q.Get("id"))
	}
	for _, key := range []string{"u", "t", "s"} {
		if q.Get(key) == "" {
			t.Errorf("stream url is missing auth parameter %q", key)
		}
	}
	if q.Get("p") != "" {
		t.Error("stream url must not carry a plaintext password")
	}

	artTarget, err := s.ArtTarget("al-42")
	if err != nil {
		t.Fatalf("ArtTarget: %v", err)
	}
	if !strings.Contains(artTarget.URL, "/rest/getCoverArt.view") || !strings.Contains(artTarget.URL, "id=al-42") {
		t.Errorf("art url = %q", artTarget.URL)
	}

	// Each call re-salts, so two stream URLs for the same track must differ.
	// A fixed token would be a replayable credential.
	again, err := s.StreamTarget("300")
	if err != nil {
		t.Fatalf("StreamTarget: %v", err)
	}
	if again.URL == streamTarget.URL {
		t.Error("stream urls should use a fresh salt each time")
	}
}

func TestStreamURLRejectsEmptyID(t *testing.T) {
	s := newTestSource(t, func(http.ResponseWriter, *http.Request) {})
	if _, err := s.StreamTarget(""); err == nil {
		t.Error("want an error for an empty item id")
	}
	if _, err := s.ArtTarget(""); err == nil {
		t.Error("want an error for an empty art id")
	}
}

// A Subsonic error arrives as HTTP 200 with an error object, so the adapter has
// to read the envelope rather than trusting the status code.
func TestSearchSurfacesSubsonicErrorEnvelope(t *testing.T) {
	s := newTestSource(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"subsonic-response":{"status":"failed",
		  "error":{"code":40,"message":"Wrong username or password"}}}`))
	})

	_, err := s.Search(context.Background(), media.Query{Text: "x"})
	if err == nil {
		t.Fatal("want an error from a failed subsonic envelope")
	}
	if !strings.Contains(err.Error(), "Wrong username or password") {
		t.Errorf("error should quote the upstream message, got %v", err)
	}
}

func TestSearchSurfacesHTTPFailure(t *testing.T) {
	s := newTestSource(t, func(w http.ResponseWriter, _ *http.Request) {
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
