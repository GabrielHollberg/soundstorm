package httpapi

import (
	"testing"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/collections"
)

func play(day string, hour int, id, artist string) collections.Listen {
	d, _ := time.Parse("2006-01-02", day)
	return collections.Listen{At: d.Add(time.Duration(hour) * time.Hour), SourceID: "nd", ID: id, Title: id, Artist: artist,
		Album: artist + " LP", Seconds: 180, Genre: "Rock; Indie"}
}

func TestAYearRecapCountsWhatAndWhen(t *testing.T) {
	listens := []collections.Listen{
		play("2026-03-01", 22, "a", "Radiohead"), play("2026-03-02", 22, "a", "Radiohead"),
		play("2026-03-03", 23, "b", "Radiohead"), play("2026-03-10", 9, "c", "Beck"),
		play("2026-07-04", 21, "d", "Muse"),
	}
	r := yearRecap(listens, time.UTC, map[string]bool{"beck": true})
	if r.Plays != 5 || r.Minutes != 15 || r.Songs != 4 || r.Artists != 3 {
		t.Errorf("totals: %+v", r)
	}
	if r.TopArtist[0].Name != "Radiohead" || r.TopArtist[0].Plays != 3 || r.TopSongs[0].Name != "a" {
		t.Errorf("top: %+v %+v", r.TopArtist, r.TopSongs)
	}
	if r.Months[2] != 4 || r.Hours[22] != 2 || r.Streak.Days != 3 || r.Streak.From != "2026-03-01" {
		t.Errorf("when: months %v hours22 %d streak %+v", r.Months, r.Hours[22], r.Streak)
	}
	if r.NewArtists != 2 || r.NewTop.Name != "Radiohead" {
		t.Errorf("new to you: %d, %+v", r.NewArtists, r.NewTop)
	}
	if len(r.TopGenres) != 2 || r.First.Name != "a" {
		t.Errorf("genres %+v first %+v", r.TopGenres, r.First)
	}
}

func TestAPlayCountsOnTheDayItWasWhereTheListenerIs(t *testing.T) {
	// 03:00 UTC on 1 March is 22:00 on Saturday 28 February in New York.
	l := collections.Listen{At: time.Date(2026, 3, 1, 3, 0, 0, 0, time.UTC), SourceID: "nd", ID: "a", Title: "a", Artist: "X"}
	r := yearRecap([]collections.Listen{l}, time.FixedZone("ny", -5*3600), nil)
	if r.Months[1] != 1 || r.Hours[22] != 1 || r.Weekdays[time.Saturday] != 1 {
		t.Errorf("months %v hour22 %d weekdays %v", r.Months, r.Hours[22], r.Weekdays)
	}
}
