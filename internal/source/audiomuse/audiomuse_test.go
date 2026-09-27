package audiomuse

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fake answers the way 3.6.3 did, two pages of the analysis.
func fake(t *testing.T) *Source {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/sync":
			if r.URL.Query().Get("include_embeddings") != "false" {
				t.Error("asked for the embeddings, which are most of the payload")
			}
			page := r.URL.Query().Get("page")
			more := page == "1"
			fmt.Fprintf(w, `{"has_more":%v,"tracks":[{"id":"song%s","energy":0.64,"tempo":81.5,"key":"B","scale":"minor",`+
				`"mood_vector":"jazz:0.628,instrumental:0.540","other_features":"danceable:0.58,aggressive:0.61,bad,sad:9"}]}`, more, page)
		case "/api/similar_tracks":
			if r.URL.Query().Get("item_id") == "unheard" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			fmt.Fprint(w, `[{"item_id":"a","distance":0.1},{"item_id":"seed"},{"item_id":"b"}]`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	s, err := New(Config{ID: "audiomuse", BaseURL: srv.URL, Token: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestFeaturesReadsEveryPage(t *testing.T) {
	f, err := fake(t).Features(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(f) != 2 {
		t.Fatalf("%d songs, want both pages", len(f))
	}
	s := f["song1"]
	if s.Energy != 0.64 || s.Tempo != 81.5 || s.Tags["instrumental"] != 0.54 || s.Moods["aggressive"] != 0.61 {
		t.Errorf("read as %+v", s)
	}
	if _, ok := s.Moods["sad"]; ok {
		t.Error("kept a score outside 0 to 1")
	}
}

func TestSimilarLeavesOutTheSeedAndTreatsUnheardAsNone(t *testing.T) {
	s := fake(t)
	ids, err := s.Similar(context.Background(), "seed", 10)
	if err != nil || len(ids) != 2 || ids[0] != "a" || ids[1] != "b" {
		t.Errorf("similar = %v, %v", ids, err)
	}
	ids, err = s.Similar(context.Background(), "unheard", 10)
	if err != nil || ids != nil {
		t.Errorf("a song not yet analyzed = %v, %v; want nothing and no error", ids, err)
	}
}
