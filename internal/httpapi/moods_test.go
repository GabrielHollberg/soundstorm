package httpapi

import (
	"fmt"
	"testing"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/source/audiomuse"
)

// The analysis answers in a narrow band - these are as close together as
// real ones - so moods must come from order, not from the raw numbers.
func narrowFeatures() map[string]audiomuse.Features {
	f := map[string]audiomuse.Features{}
	for i := 0; i < 20; i++ {
		d := float64(i) / 1000 // 0.000 ... 0.019
		f[fmt.Sprintf("s%02d", i)] = audiomuse.Features{
			Energy: 0.5 + d, Tempo: 90 + float64(i),
			Moods: map[string]float64{"relaxed": 0.62 - d, "aggressive": 0.58 + d, "happy": 0.6, "sad": 0.6,
				"party": 0.58 + d, "danceable": 0.58 + d},
		}
	}
	return f
}

func TestMoodsComeFromRankNotRawScores(t *testing.T) {
	m := scoreMoods(narrowFeatures())
	if m["s00"]["chill"] < 0.9 || m["s19"]["chill"] > 0.1 {
		t.Errorf("chill: calmest %.2f, loudest %.2f", m["s00"]["chill"], m["s19"]["chill"])
	}
	if m["s19"]["intense"] < 0.9 || m["s19"]["party"] < 0.9 {
		t.Errorf("the loudest, fastest song: intense %.2f party %.2f", m["s19"]["intense"], m["s19"]["party"])
	}
	// Every song scored the same for happy and sad: a tie is the middle, not an order.
	if r := rankFeatures(narrowFeatures()); r["s00"]["happy"] != r["s19"]["happy"] {
		t.Errorf("tied scores ranked apart: %.2f and %.2f", r["s00"]["happy"], r["s19"]["happy"])
	}
}

func TestAMoodStationPlaysThatMood(t *testing.T) {
	var pool []media.Item
	for i := 0; i < 20; i++ {
		pool = append(pool, media.Item{ID: fmt.Sprintf("s%02d", i), SourceID: "nd", Kind: media.KindMusic,
			Creators: []string{fmt.Sprintf("Artist %d", i%10)}, Extra: map[string]string{"album": fmt.Sprintf("A%d", i)}})
	}
	h := heard{moods: scoreMoods(narrowFeatures())}
	st, err := buildStation(seeded(), radioParams{Mode: "mood", Seed: "chill", Size: 10}, pool, radioListening{now: time.Now()}, nil, h)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range st.Songs {
		if h.moods[it.ID]["chill"] <= 0.6 {
			t.Errorf("%s on Chill scores %.2f", it.ID, h.moods[it.ID]["chill"])
		}
	}
	if _, err := buildStation(seeded(), radioParams{Mode: "mood", Seed: "chill"}, pool, radioListening{now: time.Now()}, nil, heard{}); err == nil {
		t.Error("a mood station with nothing analyzed should say so")
	}
}

func TestSongRadioFollowsWhatSoundsLikeIt(t *testing.T) {
	pool := radioPool()
	h := heard{like: []string{"Beck-3", "Beck-4", "Nirvana-1"}}
	st, err := buildStation(seeded(), radioParams{Mode: "song", Seed: "Muse-2", Size: 10}, pool, radioListening{now: time.Now()}, nil, h)
	if err != nil {
		t.Fatal(err)
	}
	if st.Songs[0].ID != "Muse-2" {
		t.Errorf("starts with %s", st.Songs[0].ID)
	}
	for _, it := range st.Songs[1:] {
		if it.ID != "Beck-3" && it.ID != "Beck-4" && it.ID != "Nirvana-1" && artistOf(it) != "Muse" {
			t.Errorf("%s is neither like it nor by Muse", it.ID)
		}
	}
}
