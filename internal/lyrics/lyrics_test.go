package lyrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestParseLRC(t *testing.T) {
	l := ParseLRC("[ar:Somebody]\n[00:04.00]Second\n[00:00.50]First\n[01:02.5][01:30.25]Chorus\n\n")
	want := []struct {
		start int
		text  string
	}{{500, "First"}, {4000, "Second"}, {62500, "Chorus"}, {90250, "Chorus"}}
	if !l.Synced || len(l.Lines) != len(want) {
		t.Fatalf("got %+v", l)
	}
	for i, w := range want {
		if l.Lines[i].Start != w.start || l.Lines[i].Text != w.text {
			t.Errorf("line %d = %+v, want %+v", i, l.Lines[i], w)
		}
	}
}

// LRCLIB is asked with artist, title, album and duration; the answer is kept,
// so the second play asks nothing; and a song it lacks is remembered as none.
func TestFindAsksOnceAndRemembers(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		q := r.URL.Query()
		if q.Get("track_name") == "Missing" {
			http.NotFound(w, r)
			return
		}
		if q.Get("artist_name") != "Aurora Lane" || q.Get("album_name") != "Tidewater" || q.Get("duration") != "215" {
			t.Errorf("asked with %v", q)
		}
		if r.Header.Get("User-Agent") == "" {
			t.Error("no User-Agent")
		}
		_, _ = w.Write([]byte(`{"syncedLyrics":"[00:01.00]Hello\n[00:03.00]World","plainLyrics":"Hello\nWorld"}`))
	}))
	defer srv.Close()
	f, _ := New(t.TempDir())
	f.BaseURL = srv.URL

	song := Song{Key: "navidrome/1", Artist: "Aurora Lane", Title: "Harbour", Album: "Tidewater", Duration: 214.6}
	for i := 0; i < 2; i++ {
		l, found, err := f.Find(context.Background(), song)
		if err != nil || !found || !l.Synced || len(l.Lines) != 2 || l.Lines[1].Text != "World" {
			t.Fatalf("find %d = %+v %v %v", i, l, found, err)
		}
	}
	for i := 0; i < 2; i++ {
		if _, found, err := f.Find(context.Background(), Song{Key: "navidrome/2", Artist: "A", Title: "Missing"}); found || err != nil {
			t.Fatalf("missing song: found=%v err=%v", found, err)
		}
	}
	if calls != 2 {
		t.Errorf("LRCLIB asked %d times, want 2 (once per song)", calls)
	}
}
