package httpapi

import (
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/collections"
	"github.com/GabrielHollberg/soundstorm/internal/media"
)

func radioPool() []media.Item {
	var pool []media.Item
	for a, artist := range []string{"Radiohead", "Nirvana", "Beck", "Muse"} {
		for n := 0; n < 10; n++ {
			pool = append(pool, media.Item{
				ID: fmt.Sprintf("%s-%d", artist, n), SourceID: "nd", Kind: media.KindMusic, Title: fmt.Sprintf("%s %d", artist, n),
				Creators: []string{artist}, Year: 1990 + a*5 + n%3,
				Extra: map[string]string{"album": fmt.Sprintf("%s album %d", artist, n%2), "genre": "Rock"},
			})
		}
	}
	return pool
}

func seeded() *rand.Rand { return rand.New(rand.NewPCG(1, 2)) }

func TestAStationNeverRepeatsAnArtistWithinAFewSongs(t *testing.T) {
	st, err := buildStation(seeded(), radioParams{Mode: "library", Size: 20}, radioPool(), radioListening{now: time.Now()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, it := range st.Songs {
		for j := max(0, i-artistGap); j < i; j++ {
			if artistOf(st.Songs[j]) == artistOf(it) {
				t.Fatalf("%s at %d and %d", artistOf(it), j, i)
			}
		}
	}
}

func TestDeepCutsAreUnplayedSongsByArtistsYouPlay(t *testing.T) {
	pool := radioPool()
	l := radioListening{plays: map[string]collections.Play{}, favs: map[string]bool{}, now: time.Now().Add(24 * time.Hour)}
	for _, it := range pool[:3] { // three Radiohead songs, played a lot
		l.plays[songKey(it)] = collections.Play{Item: it, Count: 9, Last: time.Now()}
	}
	st, err := buildStation(seeded(), radioParams{Mode: "deep", Size: 50}, pool, l, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Songs) != 7 {
		t.Fatalf("%d songs, want Radiohead's 7 unplayed", len(st.Songs))
	}
	for _, it := range st.Songs {
		if artistOf(it) != "Radiohead" || l.plays[songKey(it)].Count > 0 {
			t.Errorf("deep cut %s (played %d)", it.Title, l.plays[songKey(it)].Count)
		}
	}
}

func TestTimeTravelGoesForwardAndCarriesOn(t *testing.T) {
	st, err := buildStation(seeded(), radioParams{Mode: "time", Size: 8}, radioPool(), radioListening{now: time.Now()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(st.Songs); i++ {
		if st.Songs[i].Year < st.Songs[i-1].Year {
			t.Fatalf("went back from %d to %d", st.Songs[i-1].Year, st.Songs[i].Year)
		}
	}
	next, _ := buildStation(seeded(), radioParams{Mode: "time", Size: 8, From: st.Next}, radioPool(), radioListening{now: time.Now()}, nil)
	if next.Songs[0].Year < st.Next {
		t.Errorf("the next batch started at %d, before %d", next.Songs[0].Year, st.Next)
	}
}

func TestTheTunerKeepsToItsYearsAndFamiliarity(t *testing.T) {
	pool := radioPool()
	l := radioListening{plays: map[string]collections.Play{}, favs: map[string]bool{}, now: time.Now().Add(24 * time.Hour)}
	for _, it := range pool {
		if it.Creators[0] == "Beck" {
			l.favs[songKey(it)] = true
		}
	}
	one := 1.0
	st, err := buildStation(seeded(), radioParams{Mode: "custom", Familiar: &one, From: 2000, Until: 2012, Size: 10}, pool, l, nil)
	if err != nil {
		t.Fatal(err)
	}
	beck := 0
	for _, it := range st.Songs {
		if it.Year < 2000 || it.Year > 2012 {
			t.Errorf("%s is from %d", it.Title, it.Year)
		}
		if artistOf(it) == "Beck" {
			beck++
		}
	}
	if beck < len(st.Songs)/2 {
		t.Errorf("favorites turned all the way up gave %d of %d favorites", beck, len(st.Songs))
	}
}

func TestSongRadioStartsWithTheSongAndSkipsWhatIsQueued(t *testing.T) {
	pool := radioPool()
	st, err := buildStation(seeded(), radioParams{Mode: "song", Seed: "Muse-4", Exclude: []string{"nd/Muse-5"}, Size: 10}, pool, radioListening{now: time.Now()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if st.Songs[0].ID != "Muse-4" {
		t.Errorf("starts with %s", st.Songs[0].ID)
	}
	for _, it := range st.Songs {
		if it.ID == "Muse-5" {
			t.Error("played a song already queued")
		}
	}
}
