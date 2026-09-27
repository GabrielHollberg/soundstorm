package httpapi

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/collections"
	"github.com/GabrielHollberg/soundstorm/internal/media"
)

// The year in music: what somebody played, when, and how it sounded, from
// their own listens (internal/collections/listens.go) - a year recap
// available any day of the year, private to them. "All time" works from the
// per-song counts History has always kept, so it has something to say from
// the first day, before a year of listens exists; it cannot say when.

const recapTop = 5

// recapSongs is how many top songs are listed: five are shown, and all of
// them play as "your top songs".
const recapSongs = 25

type recapCount struct {
	Name     string `json:"name"`
	Sub      string `json:"sub,omitempty"`
	Plays    int    `json:"plays"`
	Minutes  int    `json:"minutes"`
	SourceID string `json:"sourceId,omitempty"`
	ID       string `json:"id,omitempty"`
	ArtID    string `json:"artId,omitempty"`
	// Item is the song as its backend described it, for a top song the app
	// can play; from History's snapshot.
	Item    *media.Item `json:"item,omitempty"`
	seconds float64     // summed before rounding
}

type tally struct {
	by map[string]*recapCount
}

func newTally() *tally { return &tally{by: map[string]*recapCount{}} }

func (t *tally) add(key string, plays int, seconds float64, fill func(*recapCount)) {
	if strings.TrimSpace(key) == "" {
		return
	}
	c, ok := t.by[key]
	if !ok {
		c = &recapCount{}
		fill(c)
		t.by[key] = c
	}
	c.Plays += plays
	c.seconds += seconds
	if c.ArtID == "" {
		fill(c)
	}
}

func (t *tally) top(n int) []recapCount {
	all := make([]recapCount, 0, len(t.by))
	for _, c := range t.by {
		c.Minutes = int(c.seconds/60 + 0.5)
		all = append(all, *c)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Plays != all[j].Plays {
			return all[i].Plays > all[j].Plays
		}
		return all[i].Name < all[j].Name
	})
	return all[:min(n, len(all))]
}

// recap is the summary of a set of plays: each listen, or each song with a
// count (all time).
type recap struct {
	Period    string       `json:"period"`
	Year      int          `json:"year,omitempty"`
	Years     []int        `json:"years"`
	Plays     int          `json:"plays"`
	Minutes   int          `json:"minutes"`
	Songs     int          `json:"songs"`
	Artists   int          `json:"artists"`
	Albums    int          `json:"albums"`
	TopSongs  []recapCount `json:"topSongs"`
	TopArtist []recapCount `json:"topArtists"`
	TopAlbums []recapCount `json:"topAlbums"`
	TopGenres []recapCount `json:"topGenres"`
	// When: only from listens.
	Months     []int        `json:"months,omitempty"`   // 12, plays per month
	Weekdays   []int        `json:"weekdays,omitempty"` // 7, Sunday first
	Hours      []int        `json:"hours,omitempty"`    // 24
	Streak     *recapStreak `json:"streak,omitempty"`
	First      *recapCount  `json:"first,omitempty"` // the first song of the year
	FirstAt    *time.Time   `json:"firstAt,omitempty"`
	NewArtists int          `json:"newArtists,omitempty"` // first played this year
	NewTop     *recapCount  `json:"newTop,omitempty"`     // the most played of them
	Since      *time.Time   `json:"since,omitempty"`      // the earliest listen counted
	// How it sounded: the share of analyzed plays whose strongest mood was
	// each, when the sound analysis has heard them.
	Moods []recapMood `json:"moods,omitempty"`
}

type recapStreak struct {
	Days int    `json:"days"`
	From string `json:"from"`
	To   string `json:"to"`
}

type recapMood struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Share int    `json:"share"` // percent
}

func (s *Server) handleRecap(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	// The browser's offset, in minutes east of UTC, so a play at 11pm counts
	// on the day it was, not on the server's.
	offset, _ := strconv.Atoi(r.URL.Query().Get("tz"))
	if offset < -14*60 || offset > 14*60 {
		offset = 0
	}
	zone := time.FixedZone("local", offset*60)
	years := s.collections.ListenYears(user.ID)
	if years == nil {
		years = []int{}
	}
	visible := s.visibleSources(r)

	if r.URL.Query().Get("period") == "all" {
		plays, err := s.collections.History(user.ID)
		if err != nil {
			s.collectionsError(w, err)
			return
		}
		out := allTime(plays, visible)
		out.Years = years
		withItems(out.TopSongs, plays)
		out.Moods = s.recapMoods(r, func(yield func(id string, n int)) {
			for _, p := range plays {
				yield(p.Item.ID, p.Count)
			}
		})
		writeJSON(w, http.StatusOK, out)
		return
	}

	year := time.Now().In(zone).Year()
	if y, err := strconv.Atoi(r.URL.Query().Get("year")); err == nil && y > 1900 && y <= year {
		year = y
	}
	// A year in somebody's own time zone reaches into the files either side.
	var listens []collections.Listen
	for _, y := range []int{year - 1, year, year + 1} {
		ls, err := s.collections.Listens(user.ID, y)
		if err != nil {
			s.collectionsError(w, err)
			return
		}
		for _, l := range ls {
			if l.At.In(zone).Year() == year && visible[l.SourceID] {
				listens = append(listens, l)
			}
		}
	}
	// Artists played before this year, for "new to you": from earlier years'
	// listens, and from History's first plays, which reach back further.
	before := map[string]bool{}
	start := time.Date(year, 1, 1, 0, 0, 0, 0, zone)
	if hist, err := s.collections.History(user.ID); err == nil {
		for _, p := range hist {
			if p.First.Before(start) && len(p.Item.Creators) > 0 {
				before[nameKey(p.Item.Creators[0])] = true
			}
		}
	}
	for _, y := range years {
		if y < year-1 {
			if ls, err := s.collections.Listens(user.ID, y); err == nil {
				for _, l := range ls {
					before[nameKey(l.Artist)] = true
				}
			}
		}
	}
	out := yearRecap(listens, zone, before)
	out.Year, out.Years = year, years
	if hist, err := s.collections.History(user.ID); err == nil {
		withItems(out.TopSongs, hist)
	}
	out.Moods = s.recapMoods(r, func(yield func(id string, n int)) {
		for _, l := range listens {
			yield(l.ID, 1)
		}
	})
	writeJSON(w, http.StatusOK, out)
}

// visibleSources is the shelves this account may see, so a recap never names
// what a restricted account cannot open.
func (s *Server) visibleSources(r *http.Request) map[string]bool {
	out := map[string]bool{}
	for _, src := range s.reg.All(r.Context()) {
		out[src.ID()] = true
	}
	return out
}

func yearRecap(listens []collections.Listen, zone *time.Location, before map[string]bool) recap {
	out := recap{Period: "year", Months: make([]int, 12), Weekdays: make([]int, 7), Hours: make([]int, 24)}
	songs, artists, albums, genres := newTally(), newTally(), newTally(), newTally()
	days := map[string]bool{}
	var seconds float64
	sort.Slice(listens, func(i, j int) bool { return listens[i].At.Before(listens[j].At) })
	for _, l := range listens {
		at := l.At.In(zone)
		out.Plays++
		seconds += l.Seconds
		out.Months[at.Month()-1]++
		out.Weekdays[at.Weekday()]++
		out.Hours[at.Hour()]++
		days[at.Format("2006-01-02")] = true
		songs.add(l.SourceID+"/"+l.ID, 1, l.Seconds, func(c *recapCount) {
			c.Name, c.Sub, c.SourceID, c.ID, c.ArtID = l.Title, l.Artist, l.SourceID, l.ID, l.ArtID
		})
		artists.add(nameKey(l.Artist), 1, l.Seconds, func(c *recapCount) { c.Name, c.SourceID, c.ArtID = l.Artist, l.SourceID, l.ArtID })
		if l.Album != "" {
			albums.add(nameKey(l.Artist)+"/"+l.Album, 1, l.Seconds, func(c *recapCount) {
				c.Name, c.Sub, c.SourceID, c.ArtID = l.Album, l.Artist, l.SourceID, l.ArtID
			})
		}
		for _, g := range genresOf(l.Genre) {
			genres.add(strings.ToLower(g), 1, l.Seconds, func(c *recapCount) { c.Name = g })
		}
	}
	out.Minutes = int(seconds/60 + 0.5)
	out.Songs, out.Artists, out.Albums = len(songs.by), len(artists.by), len(albums.by)
	out.TopSongs, out.TopArtist = songs.top(recapSongs), artists.top(recapTop)
	out.TopAlbums, out.TopGenres = albums.top(recapTop), genres.top(3)
	if len(listens) > 0 {
		l := listens[0]
		out.First = &recapCount{Name: l.Title, Sub: l.Artist, SourceID: l.SourceID, ID: l.ID, ArtID: l.ArtID}
		at := l.At.In(zone)
		out.FirstAt, out.Since = &at, &at
		out.Streak = longestStreak(days)
	}
	fresh := newTally()
	for key, c := range artists.by {
		if !before[key] {
			fresh.by[key] = c
		}
	}
	out.NewArtists = len(fresh.by)
	if top := fresh.top(1); len(top) == 1 {
		out.NewTop = &top[0]
	}
	return out
}

func allTime(plays []collections.Play, visible map[string]bool) recap {
	out := recap{Period: "all"}
	songs, artists, albums, genres := newTally(), newTally(), newTally(), newTally()
	var seconds float64
	var since time.Time
	for _, p := range plays {
		it := p.Item
		if !visible[it.SourceID] {
			continue
		}
		artist := artistOf(it)
		secs := it.DurationSeconds * float64(p.Count)
		out.Plays += p.Count
		seconds += secs
		if since.IsZero() || p.First.Before(since) {
			since = p.First
		}
		songs.add(it.SourceID+"/"+it.ID, p.Count, secs, func(c *recapCount) {
			c.Name, c.Sub, c.SourceID, c.ID, c.ArtID = it.Title, artist, it.SourceID, it.ID, it.ArtID
		})
		artists.add(nameKey(artist), p.Count, secs, func(c *recapCount) { c.Name, c.SourceID, c.ArtID = artist, it.SourceID, it.ArtID })
		if album := it.Extra["album"]; album != "" {
			albums.add(nameKey(artist)+"/"+album, p.Count, secs, func(c *recapCount) {
				c.Name, c.Sub, c.SourceID, c.ArtID = album, artist, it.SourceID, it.ArtID
			})
		}
		for _, g := range genresOf(it.Extra["genre"]) {
			genres.add(strings.ToLower(g), p.Count, secs, func(c *recapCount) { c.Name = g })
		}
	}
	out.Minutes = int(seconds/60 + 0.5)
	out.Songs, out.Artists, out.Albums = len(songs.by), len(artists.by), len(albums.by)
	out.TopSongs, out.TopArtist = songs.top(recapSongs), artists.top(recapTop)
	out.TopAlbums, out.TopGenres = albums.top(recapTop), genres.top(3)
	if !since.IsZero() {
		out.Since = &since
	}
	return out
}

// withItems fills in each top song from History's snapshot of it.
func withItems(top []recapCount, plays []collections.Play) {
	by := map[string]media.Item{}
	for _, p := range plays {
		by[p.Item.SourceID+"/"+p.Item.ID] = p.Item
	}
	for i := range top {
		if it, ok := by[top[i].SourceID+"/"+top[i].ID]; ok {
			top[i].Item = &it
		}
	}
}

// genresOf splits a song's genre the way the Genres pages do.
func genresOf(g string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(g, func(r rune) bool { return r == ',' || r == ';' }) {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// longestStreak is the most days in a row with at least one play.
func longestStreak(days map[string]bool) *recapStreak {
	var dates []time.Time
	for d := range days {
		if t, err := time.Parse("2006-01-02", d); err == nil {
			dates = append(dates, t)
		}
	}
	if len(dates) == 0 {
		return nil
	}
	sort.Slice(dates, func(i, j int) bool { return dates[i].Before(dates[j]) })
	best := &recapStreak{Days: 1, From: dates[0].Format("2006-01-02"), To: dates[0].Format("2006-01-02")}
	run, runFrom := 1, dates[0]
	for i := 1; i < len(dates); i++ {
		if dates[i].Sub(dates[i-1]) == 24*time.Hour {
			run++
		} else {
			run, runFrom = 1, dates[i]
		}
		if run > best.Days {
			best = &recapStreak{Days: run, From: runFrom.Format("2006-01-02"), To: dates[i].Format("2006-01-02")}
		}
	}
	return best
}

// recapMoods is the share of plays whose strongest mood was each, over the
// songs the sound analysis has heard. Nil without it.
func (s *Server) recapMoods(r *http.Request, each func(yield func(id string, n int))) []recapMood {
	moods := s.moods(r.Context())
	if len(moods) == 0 {
		return nil
	}
	counts := map[string]int{}
	total := 0
	each(func(id string, n int) {
		m, ok := moods[id]
		if !ok {
			return
		}
		best, bestScore := "", -1.0
		for _, d := range moodDefs {
			if m[d.ID] > bestScore {
				best, bestScore = d.ID, m[d.ID]
			}
		}
		counts[best] += n
		total += n
	})
	if total == 0 {
		return nil
	}
	var out []recapMood
	for _, d := range moodDefs {
		if counts[d.ID] > 0 {
			out = append(out, recapMood{ID: d.ID, Title: d.Title, Share: int(float64(counts[d.ID])*100/float64(total) + 0.5)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Share > out[j].Share })
	return out
}
