package jellyfin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/federate"
	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/source"
)

// Jellyfin's SortName drops a leading "The", so "The Matrix" sorts under M
// for Jellyfin and under T for the merge. The fake sorts the way Jellyfin
// does and pages by Limit and StartIndex; scrolling must still show every
// film once, in the merge's order.
func TestScrollingFilmsShowsEveryFilmOnce(t *testing.T) {
	var names []string
	for i := 0; i < 90; i++ {
		n := fmt.Sprintf("Film %02d", i)
		if i%3 == 0 {
			n = "The " + n
		}
		names = append(names, n)
	}
	sortName := func(n string) string { return strings.ToLower(strings.TrimPrefix(n, "The ")) }
	sort.Slice(names, func(i, j int) bool { return sortName(names[i]) < sortName(names[j]) })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("Limit"))
		start, _ := strconv.Atoi(q.Get("StartIndex"))
		from, to := min(start, len(names)), min(start+limit, len(names))
		var items []map[string]any
		for i, n := range names[from:to] {
			items = append(items, map[string]any{"Id": fmt.Sprintf("f%02d", from+i), "Name": n, "Type": "Movie"})
		}
		json.NewEncoder(w).Encode(map[string]any{"Items": items, "TotalRecordCount": len(names)})
	}))
	defer srv.Close()
	s, err := New(Config{ID: "jellyfin", BaseURL: srv.URL, Token: "t", UserID: "u"})
	if err != nil {
		t.Fatal(err)
	}

	seen := map[string]bool{}
	var got []string
	reg := source.NewRegistry(s)
	for offset := 0; offset < 1000; offset += 20 {
		res := federate.Search(context.Background(), reg, media.Query{Limit: 20, Offset: offset}, 5*time.Second)
		for _, it := range res.Items {
			if seen[it.ID] {
				t.Fatalf("%q appeared twice while scrolling", it.Title)
			}
			seen[it.ID] = true
			got = append(got, it.Title)
		}
		if !res.HasMore {
			break
		}
	}
	if len(seen) != len(names) {
		t.Errorf("scrolling showed %d films of %d", len(seen), len(names))
	}
	if !sort.StringsAreSorted(got) {
		t.Error("the shelf did not come out in the merge's order")
	}
}
