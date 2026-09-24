package subsonic

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The shape Navidrome 0.64.1 answers for a song with a .lrc beside it:
// structuredLyrics, synced, each line's start in milliseconds.
func TestLyricsPreferTheSyncedVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"subsonic-response":{"status":"ok","version":"1.16.1","lyricsList":{"structuredLyrics":[
			{"synced":false,"line":[{"value":"plain words"}]},
			{"synced":true,"line":[{"start":500,"value":"The sky begins to open"},{"start":4000,"value":"A line of gold"}]}
		]}}}`))
	}))
	defer srv.Close()
	s, err := New(Config{ID: "navidrome", BaseURL: srv.URL, Username: "u", Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	l, err := s.Lyrics(context.Background(), "song1")
	if err != nil {
		t.Fatal(err)
	}
	if !l.Synced || len(l.Lines) != 2 || l.Lines[0].Start != 500 || l.Lines[1].Text != "A line of gold" {
		t.Errorf("lyrics = %+v", l)
	}
}
