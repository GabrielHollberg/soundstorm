package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/collections"
	"github.com/GabrielHollberg/soundstorm/internal/federate"
	"github.com/GabrielHollberg/soundstorm/internal/media"
)

// Radio: stations that never end, built from the whole music shelf, this
// person's own listening, and - with music discovery on - which artists
// people play together. Plexamp's radio rests on analysing every track's
// audio, the expensive layer this project does not own; these rest on what
// is already here, and on things Plexamp does not do: a station tuned by
// hand (how familiar, which years, which genres), and every station
// sequenced rather than shuffled - never the same artist twice within a few
// songs, never two from one album in a row.
//
// A station is asked for in batches. The app sends what it has already
// queued, and asks for more before the end, so nothing repeats and nothing
// stops.

const (
	radioBatch      = 25
	radioMaxBatch   = 100
	radioMaxExclude = 2000
	radioBody       = 64 << 10
	artistGap       = 3 // songs between two by the same artist
)

type radioParams struct {
	Mode     string   `json:"mode"`
	Seed     string   `json:"seed"`
	Familiar *float64 `json:"familiar"` // 0 never played ... 1 favorites
	From     int      `json:"from"`
	Until    int      `json:"until"`
	Genres   []string `json:"genres"`
	Exclude  []string `json:"exclude"`
	Size     int      `json:"size"`
}

type station struct {
	Title    string       `json:"title"`
	Subtitle string       `json:"subtitle,omitempty"`
	Songs    []media.Item `json:"songs"`
	// Next is what to send as From for the batch after, for a station that
	// moves through the years.
	Next int `json:"next,omitempty"`
}

// listening is one person's history and favorites, by song.
type radioListening struct {
	plays map[string]collections.Play
	favs  map[string]bool
	now   time.Time
}

func songKey(it media.Item) string { return it.SourceID + "/" + it.ID }

func artistOf(it media.Item) string {
	if len(it.Creators) > 0 {
		return it.Creators[0]
	}
	return ""
}

// familiarity is how well this person knows a song: 1 for a favorite or one
// played often, 0 for one never played.
func (l radioListening) familiarity(it media.Item) float64 {
	if l.favs[songKey(it)] {
		return 1
	}
	p, ok := l.plays[songKey(it)]
	if !ok || p.Count == 0 {
		return 0
	}
	return math.Min(1, 0.35+float64(p.Count)/10)
}

// fresh is lower for a song played in the last few hours: a station does
// not come straight back to what was just heard.
func (l radioListening) fresh(it media.Item) float64 {
	p, ok := l.plays[songKey(it)]
	if !ok || p.Last.IsZero() {
		return 1
	}
	if since := l.now.Sub(p.Last); since < 6*time.Hour {
		return 0.1
	}
	return 1
}

// artistPlays is how often each artist has been played.
func (l radioListening) artistPlays(pool []media.Item) map[string]int {
	out := map[string]int{}
	for _, it := range pool {
		if p, ok := l.plays[songKey(it)]; ok {
			out[nameKey(artistOf(it))] += p.Count
		}
	}
	return out
}

func topArtists(counts map[string]int, n int) []string {
	type kv struct {
		k string
		v int
	}
	var all []kv
	for k, v := range counts {
		if v > 0 {
			all = append(all, kv{k, v})
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].v > all[j].v || (all[i].v == all[j].v && all[i].k < all[j].k) })
	var out []string
	for i := 0; i < len(all) && i < n; i++ {
		out = append(out, all[i].k)
	}
	return out
}

// pick draws n songs, each with probability by its weight, none twice.
func pick(rng *rand.Rand, songs []media.Item, weight func(media.Item) float64, n int) []media.Item {
	type w struct {
		it  media.Item
		key float64
	}
	var ws []w
	for _, it := range songs {
		if x := weight(it); x > 0 {
			// Efraimidis-Spirakis: the n largest u^(1/w) are a weighted
			// sample without replacement.
			ws = append(ws, w{it, math.Pow(rng.Float64(), 1/x)})
		}
	}
	sort.Slice(ws, func(i, j int) bool { return ws[i].key > ws[j].key })
	out := make([]media.Item, 0, n)
	for i := 0; i < len(ws) && i < n; i++ {
		out = append(out, ws[i].it)
	}
	return out
}

// sequence orders songs as a station plays them: no artist twice within
// artistGap songs, no album twice in a row, as far as the songs allow.
func sequence(songs []media.Item) []media.Item {
	left := append([]media.Item(nil), songs...)
	out := make([]media.Item, 0, len(songs))
	remaining := map[string]int{}
	for _, it := range left {
		remaining[nameKey(artistOf(it))]++
	}
	for len(left) > 0 {
		// Of the songs that keep the spread, the one whose artist has most
		// left to place: saving the busiest artist for last is what forces
		// repeats at the end.
		chosen, best := 0, -1
		for i, it := range left {
			if n := remaining[nameKey(artistOf(it))]; n > best && spreadOK(out, it) {
				chosen, best = i, n
			}
		}
		remaining[nameKey(artistOf(left[chosen]))]--
		out = append(out, left[chosen])
		left = append(left[:chosen], left[chosen+1:]...)
	}
	return out
}

// spreadArtists takes size songs from a weighted draw, in its order, with
// no artist taking more than their share of the batch - one busy artist
// would otherwise crowd a station and force repeats. The share gives way
// when there is nobody else to play.
func spreadArtists(drawn []media.Item, size int) []media.Item {
	share := max(2, (size+artistGap)/(artistGap+1))
	taken := map[string]int{}
	used := make([]bool, len(drawn))
	var out []media.Item
	for i, it := range drawn {
		if len(out) == size {
			break
		}
		a := nameKey(artistOf(it))
		if taken[a] < share {
			taken[a]++
			used[i] = true
			out = append(out, it)
		}
	}
	for i, it := range drawn {
		if len(out) == size {
			break
		}
		if !used[i] {
			out = append(out, it)
		}
	}
	return out
}

func spreadOK(before []media.Item, it media.Item) bool {
	artist := nameKey(artistOf(it))
	for i := len(before) - 1; i >= 0 && i >= len(before)-artistGap; i-- {
		if artist != "" && nameKey(artistOf(before[i])) == artist {
			return false
		}
	}
	if n := len(before); n > 0 && it.Extra["album"] != "" && before[n-1].Extra["album"] == it.Extra["album"] {
		return false
	}
	return true
}

func hasGenre(it media.Item, wanted map[string]bool) bool {
	for _, g := range genreNames(it) {
		if wanted[strings.ToLower(g)] {
			return true
		}
	}
	return false
}

// buildStation makes one batch of a station. similar gives the artists (by
// name key) like one, that are in the library; nil without music discovery.
func buildStation(rng *rand.Rand, p radioParams, pool []media.Item, l radioListening, similar func(artist string) []string) (station, error) {
	size := p.Size
	if size <= 0 {
		size = radioBatch
	}
	size = min(size, radioMaxBatch)
	excluded := map[string]bool{}
	for _, k := range p.Exclude {
		excluded[k] = true
	}
	var songs []media.Item
	for _, it := range pool {
		if !excluded[songKey(it)] {
			songs = append(songs, it)
		}
	}
	// Once everything has been played, a station starts over rather than stops.
	if len(songs) == 0 {
		songs = pool
	}
	byKey := map[string]media.Item{}
	for _, it := range pool {
		byKey[it.ID] = it
	}
	weighted := func(title, subtitle string, weight func(media.Item) float64) (station, error) {
		chosen := spreadArtists(pick(rng, songs, func(it media.Item) float64 { return weight(it) * l.fresh(it) }, 3*size), size)
		if len(chosen) == 0 {
			return station{}, fmt.Errorf("nothing in the library fits that station")
		}
		return station{Title: title, Subtitle: subtitle, Songs: sequence(chosen)}, nil
	}

	switch p.Mode {
	case "library":
		return weighted("Library radio", "Everything, leaning toward what you love", func(it media.Item) float64 {
			return 1 + 2*l.familiarity(it)
		})

	case "deep":
		top := map[string]bool{}
		for _, a := range topArtists(l.artistPlays(pool), 15) {
			top[a] = true
		}
		st, err := weighted("Deep cuts", "Songs you haven't played, by artists you have", func(it media.Item) float64 {
			if !top[nameKey(artistOf(it))] || l.favs[songKey(it)] {
				return 0
			}
			if pl, ok := l.plays[songKey(it)]; ok && pl.Count > 1 {
				return 0
			}
			return 1
		})
		if err != nil {
			// Nobody has a history yet: whatever has never been played.
			return weighted("Deep cuts", "Songs you haven't played yet", func(it media.Item) float64 {
				return 1 - l.familiarity(it)
			})
		}
		return st, nil

	case "time":
		return timeTravel(rng, p, songs, size)

	case "discover":
		if similar == nil {
			return station{}, fmt.Errorf("discovery radio needs music discovery on")
		}
		counts := l.artistPlays(pool)
		want := map[string]bool{}
		for _, a := range topArtists(counts, 8) {
			for _, s := range similar(a) {
				if counts[s] <= 2 {
					want[s] = true
				}
			}
		}
		return weighted("Discovery radio", "Artists like your favorites that you rarely play", func(it media.Item) float64 {
			if want[nameKey(artistOf(it))] {
				return 1
			}
			return 0
		})

	case "artist", "song", "album":
		seed, ok := byKey[p.Seed]
		var artist, title string
		switch {
		case p.Mode == "artist":
			artist, title = p.Seed, p.Seed+" radio"
		case ok && p.Mode == "song":
			artist, title = artistOf(seed), seed.Title+" radio"
		case ok:
			artist, title = artistOf(seed), seed.Extra["album"]+" radio"
		default:
			return station{}, fmt.Errorf("no such song")
		}
		near := map[string]bool{}
		if similar != nil {
			for _, s := range similar(artist) {
				near[s] = true
			}
		}
		genres := map[string]bool{}
		if len(near) == 0 {
			// Without anybody to ask, the artist's own genres stand in.
			for _, it := range pool {
				if nameKey(artistOf(it)) == nameKey(artist) {
					for _, g := range genreNames(it) {
						genres[strings.ToLower(g)] = true
					}
				}
			}
		}
		st, err := weighted(title, "", func(it media.Item) float64 {
			a := nameKey(artistOf(it))
			switch {
			case a == nameKey(artist):
				return 3
			case near[a]:
				return 2
			case len(near) == 0 && hasGenre(it, genres):
				return 1
			}
			return 0
		})
		// A song's radio starts with the song.
		if err == nil && ok && p.Mode == "song" && !excluded[songKey(seed)] {
			rest := []media.Item{seed}
			for _, it := range st.Songs {
				if it.ID != seed.ID {
					rest = append(rest, it)
				}
			}
			st.Songs = rest
		}
		return st, err

	case "genre":
		want := map[string]bool{strings.ToLower(p.Seed): true}
		return weighted(p.Seed+" radio", "", func(it media.Item) float64 {
			if hasGenre(it, want) {
				return 1 + l.familiarity(it)
			}
			return 0
		})

	case "custom":
		f := 0.5
		if p.Familiar != nil {
			f = math.Max(0, math.Min(1, *p.Familiar))
		}
		genres := map[string]bool{}
		for _, g := range p.Genres {
			genres[strings.ToLower(g)] = true
		}
		return weighted("Your station", customSubtitle(p, f), func(it media.Item) float64 {
			if p.From > 0 && (it.Year == 0 || it.Year < p.From) || p.Until > 0 && (it.Year == 0 || it.Year > p.Until) {
				return 0
			}
			if len(genres) > 0 && !hasGenre(it, genres) {
				return 0
			}
			fam := l.familiarity(it)
			closeness := 1 - math.Abs(f-fam)
			return 0.02 + closeness*closeness*closeness
		})
	}
	return station{}, fmt.Errorf("no such station")
}

func customSubtitle(p radioParams, f float64) string {
	var parts []string
	switch {
	case f < 0.2:
		parts = append(parts, "Never played")
	case f > 0.8:
		parts = append(parts, "Favorites")
	case f < 0.45:
		parts = append(parts, "Mostly new to you")
	case f > 0.55:
		parts = append(parts, "Mostly familiar")
	default:
		parts = append(parts, "A mix of old and new")
	}
	if p.From > 0 || p.Until > 0 {
		from, until := "", ""
		if p.From > 0 {
			from = strconv.Itoa(p.From)
		}
		if p.Until > 0 {
			until = strconv.Itoa(p.Until)
		}
		parts = append(parts, strings.Trim(from+"-"+until, "-"))
	}
	if len(p.Genres) > 0 {
		parts = append(parts, strings.Join(p.Genres, ", "))
	}
	return strings.Join(parts, " · ")
}

// timeTravel walks the library's years in order, a couple of songs from
// each, carrying on where the last batch stopped and starting again from the
// oldest at the end.
func timeTravel(rng *rand.Rand, p radioParams, songs []media.Item, size int) (station, error) {
	byYear := map[int][]media.Item{}
	for _, it := range songs {
		if it.Year > 0 {
			byYear[it.Year] = append(byYear[it.Year], it)
		}
	}
	var years []int
	for y := range byYear {
		years = append(years, y)
	}
	if len(years) == 0 {
		return station{}, fmt.Errorf("no songs here say what year they are from")
	}
	sort.Ints(years)
	start := 0
	for start < len(years) && years[start] < p.From {
		start++
	}
	if start == len(years) {
		start = 0
	}
	per := max(1, min(3, size/max(1, len(years)-start)))
	var out []media.Item
	last := years[start]
	for i := start; i < len(years) && len(out) < size; i++ {
		y := years[i]
		in := byYear[y]
		rng.Shuffle(len(in), func(a, b int) { in[a], in[b] = in[b], in[a] })
		out = append(out, sequence(in[:min(per, len(in))])...)
		last = y
	}
	if len(out) > size {
		out = out[:size]
	}
	return station{
		Title:    "Time travel",
		Subtitle: fmt.Sprintf("%d to %d, in order", years[start], last),
		Songs:    out,
		Next:     last + 1,
	}, nil
}

// radioCatalog is what the Radio page offers: its stations, each with a
// collage drawn from a first taste of it, and what the tuner can choose from.
func (s *Server) radioCatalog(pool []media.Item, l radioListening, similar func(string) []string, sourceID string) map[string]any {
	modes := []string{"library", "deep", "time"}
	if similar != nil {
		modes = append(modes, "discover")
	}
	rng := rand.New(rand.NewPCG(uint64(l.now.UnixNano()), 7))
	stations := []map[string]any{}
	for _, mode := range modes {
		st, err := buildStation(rng, radioParams{Mode: mode, Size: 16}, pool, l, similar)
		if err != nil {
			continue
		}
		if mode == "time" {
			st.Subtitle = "Your library in order, year by year"
		}
		stations = append(stations, map[string]any{"mode": mode, "title": st.Title, "subtitle": st.Subtitle,
			"covers": stationCovers(st.Songs), "sourceId": sourceID})
	}
	genres := map[string]int{}
	names := map[string]string{}
	minYear, maxYear := 0, 0
	for _, it := range pool {
		for _, g := range genreNames(it) {
			genres[strings.ToLower(g)]++
			names[strings.ToLower(g)] = g
		}
		if it.Year > 0 {
			if minYear == 0 || it.Year < minYear {
				minYear = it.Year
			}
			maxYear = max(maxYear, it.Year)
		}
	}
	top := []string{}
	for _, k := range topArtists(genres, 16) {
		top = append(top, names[k])
	}
	return map[string]any{"stations": stations, "genres": top, "years": map[string]int{"from": minYear, "until": maxYear}}
}

// stationCovers is up to four covers from different albums.
func stationCovers(songs []media.Item) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, it := range songs {
		if it.ArtID != "" && !seen[it.ArtID] && len(out) < 4 {
			seen[it.ArtID] = true
			out = append(out, it.ArtID)
		}
	}
	return out
}

// handleRadio is the Radio page's catalog (GET) or a station's next batch
// (POST).
func (s *Server) handleRadio(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	src, _, ok := s.mixSource(r)
	if !ok {
		writeError(w, http.StatusNotFound, "no music library")
		return
	}
	pool, err := src.Search(r.Context(), media.Query{Kinds: []media.Kind{media.KindMusic}, Limit: federate.MaxDepth})
	if err != nil {
		s.musicError(w, err)
		return
	}
	var p radioParams
	if r.Method == http.MethodPost {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, radioBody)).Decode(&p); err != nil || len(p.Exclude) > radioMaxExclude {
			writeError(w, http.StatusBadRequest, "expected a station")
			return
		}
	}
	l := radioListening{plays: map[string]collections.Play{}, favs: map[string]bool{}, now: time.Now()}
	for _, pl := range s.history(r, user.ID) {
		l.plays[songKey(pl.Item)] = pl
	}
	if entries, err := s.collections.Favorites(user.ID); err == nil {
		for _, e := range entries {
			l.favs[songKey(e.Item)] = true
		}
	}
	var similar func(string) []string
	if s.discovering() {
		similar = s.similarInLibrary(r.Context(), pool, p)
	}
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, s.radioCatalog(pool, l, similar, src.ID()))
		return
	}
	rng := rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), rand.Uint64()))
	st, err := buildStation(rng, p, pool, l, similar)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// similarInLibrary answers, by name key, which artists in the library are
// like one - from what music discovery already knows, asking only about a
// station's own seed, where somebody is waiting for exactly that artist.
func (s *Server) similarInLibrary(ctx context.Context, pool []media.Item, p radioParams) func(string) []string {
	display := map[string]string{}
	for _, it := range pool {
		if a := artistOf(it); a != "" {
			display[nameKey(a)] = a
		}
	}
	seed := ""
	if p.Mode == "artist" {
		seed = nameKey(p.Seed)
	}
	return func(artist string) []string {
		key := nameKey(artist)
		name := display[key]
		if name == "" {
			name = artist
		}
		info, known := s.discover.Cached(name)
		if !known && key == seed {
			ctx, cancel := context.WithTimeout(ctx, discoverDeadline)
			defer cancel()
			info, known, _ = s.discover.Artist(ctx, name)
		}
		if !known {
			go s.lookUpLater(name)
			return nil
		}
		var out []string
		for _, sim := range info.Similar {
			if k := nameKey(sim.Name); display[k] != "" && k != key {
				out = append(out, k)
			}
		}
		return out
	}
}
