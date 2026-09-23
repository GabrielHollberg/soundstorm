package subsonic

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/federate"
	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/source"
)

// The reported bug: scrolling the music shelf showed songs again. Navidrome's
// search3 lists songs in an order of its own - of the first 50 by title in a
// real 4,413-song library, its first 50 held none - and the merge cuts pages
// after sorting by title, so every page drew from a different set.
//
// This fake answers the same way, in a fixed but scrambled order and honouring
// songCount and songOffset, and the test scrolls the whole shelf through the
// real merge. Every song must appear exactly once, in title order.
func TestScrollingMusicShowsEverySongOnceInOrder(t *testing.T) {
	const total = 237
	var songs []song
	for i := 0; i < total; i++ {
		// Duplicate titles on purpose: "Intro" twice is the case where only
		// the id can keep two songs from trading places between pages.
		title := fmt.Sprintf("Song %03d", i)
		if i%40 == 0 {
			title = "Intro"
		}
		songs = append(songs, song{ID: fmt.Sprintf("id-%03d", i), Title: title})
	}
	rand.New(rand.NewSource(7)).Shuffle(len(songs), func(i, j int) { songs[i], songs[j] = songs[j], songs[i] })

	s := newTestSource(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		count, _ := strconv.Atoi(q.Get("songCount"))
		offset, _ := strconv.Atoi(q.Get("songOffset"))
		from, to := min(offset, len(songs)), min(offset+count, len(songs))
		var body struct {
			Response struct {
				Status        string `json:"status"`
				SearchResult3 struct {
					Song []song `json:"song"`
				} `json:"searchResult3"`
			} `json:"subsonic-response"`
		}
		body.Response.Status = "ok"
		body.Response.SearchResult3.Song = songs[from:to]
		json.NewEncoder(w).Encode(body)
	})

	reg := source.NewRegistry(s)
	seen := map[string]bool{}
	var titles []string
	for offset := 0; offset < 1000; offset += 20 {
		res := federate.Search(context.Background(), reg, media.Query{Limit: 20, Offset: offset}, 5*time.Second)
		for _, it := range res.Items {
			if seen[it.ID] {
				t.Fatalf("%s (%s) appeared twice while scrolling", it.Title, it.ID)
			}
			seen[it.ID] = true
			titles = append(titles, it.Title)
		}
		if !res.HasMore {
			break
		}
	}
	if len(seen) != total {
		t.Errorf("scrolling showed %d songs of %d", len(seen), total)
	}
	for i := 1; i < len(titles); i++ {
		if titles[i-1] > titles[i] {
			t.Fatalf("out of order at %d: %q before %q", i, titles[i-1], titles[i])
		}
	}
}
