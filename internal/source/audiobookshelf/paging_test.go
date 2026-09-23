package audiobookshelf

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

// Audiobookshelf's title sort ignores case; the merge's does not. On a real
// library "How to Fast" and "How To Overcome" came back in opposite orders,
// so a book at a page boundary repeated or vanished. The fake sorts the way
// Audiobookshelf does and pages by limit and page; scrolling must still show
// every book once, in the merge's order.
func TestScrollingAudiobooksShowsEveryBookOnce(t *testing.T) {
	var titles []string
	for i := 0; i < 130; i++ {
		// Alternating case, so a case-blind sort and a byte sort disagree
		// all the way down the shelf.
		p := "book"
		if i%2 == 0 {
			p = "Book"
		}
		titles = append(titles, fmt.Sprintf("%s %03d", p, i))
	}
	sort.Slice(titles, func(i, j int) bool { return strings.ToLower(titles[i]) < strings.ToLower(titles[j]) })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		from, to := min(page*limit, len(titles)), min(page*limit+limit, len(titles))
		var results []map[string]any
		for i, t := range titles[from:to] {
			results = append(results, map[string]any{
				"id":    fmt.Sprintf("li_%03d", from+i),
				"media": map[string]any{"metadata": map[string]any{"title": t}},
			})
		}
		json.NewEncoder(w).Encode(map[string]any{"results": results})
	}))
	defer srv.Close()
	s, err := New(Config{ID: "abs", BaseURL: srv.URL, Token: "t", LibraryID: "lib"})
	if err != nil {
		t.Fatal(err)
	}

	seen := map[string]bool{}
	var got []string
	reg := source.NewRegistry(s)
	for offset := 0; offset < 1000; offset += 25 {
		res := federate.Search(context.Background(), reg, media.Query{Limit: 25, Offset: offset}, 5*time.Second)
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
	if len(seen) != len(titles) {
		t.Errorf("scrolling showed %d books of %d", len(seen), len(titles))
	}
	if !sort.StringsAreSorted(got) {
		t.Error("the shelf did not come out in the merge's order")
	}
}
