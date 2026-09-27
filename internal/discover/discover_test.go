package discover

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// fake answers the four services in the shapes they were seen to answer.
func fake(t *testing.T, calls *int32) *Finder {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(calls, 1)
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "SoundStorm") {
			http.Error(w, "say who you are", http.StatusForbidden)
			return
		}
		switch {
		case r.URL.Path == "/ws/2/artist/" && strings.Contains(r.URL.Query().Get("query"), "Radiohead"):
			w.Write([]byte(`{"artists":[{"id":"mb-rh","name":"Radiohead","score":100}]}`))
		case r.URL.Path == "/ws/2/artist/":
			w.Write([]byte(`{"artists":[{"id":"mb-x","name":"Someone Else","score":72}]}`))
		case r.URL.Path == "/ws/2/artist/mb-rh":
			w.Write([]byte(`{"relations":[{"type":"wikidata","url":{"resource":"https://www.wikidata.org/wiki/Q44190"}}]}`))
		case r.URL.Path == "/similar-artists/json":
			w.Write([]byte(`[{"artist_mbid":"mb-n","name":"Nirvana"},{"artist_mbid":"mb-rh","name":"Radiohead"},{"artist_mbid":"mb-b","name":"Beck"}]`))
		case r.URL.Path == "/wiki/Special:EntityData/Q44190.json":
			w.Write([]byte(`{"entities":{"Q44190":{"sitelinks":{"enwiki":{"title":"Radiohead"}}}}}`))
		case r.URL.Path == "/api/rest_v1/page/summary/Radiohead":
			w.Write([]byte(`{"extract":"Radiohead are an English rock band.","content_urls":{"desktop":{"page":"https://en.wikipedia.org/wiki/Radiohead"}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	f, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f.MusicBrainz, f.ListenBrainz, f.Wikidata, f.Wikipedia = srv.URL, srv.URL, srv.URL, srv.URL
	f.MBGap = 0
	return f
}

func TestAnArtistIsFoundSimilarAndDescribedThenKept(t *testing.T) {
	var calls int32
	f := fake(t, &calls)
	info, found, err := f.Artist(context.Background(), "Radiohead")
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if info.MBID != "mb-rh" || len(info.Similar) != 2 || info.Similar[0].Name != "Nirvana" || info.Similar[1].Name != "Beck" {
		t.Errorf("similar = %+v, want Nirvana and Beck without Radiohead itself", info.Similar)
	}
	if info.Bio != "Radiohead are an English rock band." || info.BioURL != "https://en.wikipedia.org/wiki/Radiohead" {
		t.Errorf("bio = %q %q", info.Bio, info.BioURL)
	}
	before := atomic.LoadInt32(&calls)
	if again, ok, _ := f.Artist(context.Background(), "radiohead"); !ok || again.MBID != "mb-rh" || atomic.LoadInt32(&calls) != before {
		t.Errorf("asked again (%d calls) rather than kept", atomic.LoadInt32(&calls)-before)
	}
	if cached, ok := f.Cached("Radiohead"); !ok || cached.Bio == "" {
		t.Error("Cached did not have it")
	}
}

// A weak match is nobody: another artist's bio is worse than none. And
// "nobody" is kept too, so it is not asked again at once.
func TestAWeakMatchIsNobodyAndIsKept(t *testing.T) {
	var calls int32
	f := fake(t, &calls)
	if _, found, err := f.Artist(context.Background(), "Aurora Lane"); err != nil || found {
		t.Fatalf("found=%v err=%v, want not found", found, err)
	}
	before := atomic.LoadInt32(&calls)
	f.Artist(context.Background(), "Aurora Lane")
	if atomic.LoadInt32(&calls) != before {
		t.Error("asked again about an artist already found to be nobody")
	}
}
