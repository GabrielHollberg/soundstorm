package httpapi

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/collections"
	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/source"
	"net/url"
)

// Mixes: ready-made queues, the first thing under Music. Some come from the
// library (shuffle everything, recently added, a genre, a decade), some from
// what this person has listened to (most played, recently played,
// rediscover). The listening history is SoundStorm's, per person - see
// internal/collections.
//
// No "sounds like": that needs the audio of every track analyzed, which is
// the expensive layer this project does not own. Genre, era and a person's own
// history get a long way without it.

const (
	mixSize = 100
	// A song counts as listened to once half of it has played or four minutes,
	// whichever is sooner - the rule most music services use. The browser
	// decides when; this only records it.
	rediscoverAfter = 60 * 24 * time.Hour
	// A genre or decade needs this many songs before it is worth a mix.
	minMixSongs = 5
)

type mixCard struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Subtitle string   `json:"subtitle"`
	Covers   []string `json:"covers"` // art ids, up to four, for a collage
	SourceID string   `json:"sourceId"`
}

// libraryMixes caches the library's own mix list - genres and decades - which
// takes a handful of backend calls to work out and changes only when music
// is added.
type libraryMixes struct {
	mu    sync.Mutex
	at    time.Time
	cards []mixCard
}

const libraryMixesFor = 5 * time.Minute

func (s *Server) handleRecordPlay(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var body struct {
		Source string `json:"source"`
		ID     string `json:"id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxCollectionBody)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "expected a JSON body with source and id")
		return
	}
	item, ok := s.resolveItem(w, r, body.Source, body.ID)
	if !ok {
		return
	}
	if item.Kind != media.KindMusic {
		writeError(w, http.StatusBadRequest, "only music is remembered")
		return
	}
	// Plays come no faster than songs can be half heard; a flood of them is
	// a script growing the listen log, and is dropped quietly.
	if !s.plays.allow(user.ID, time.Now(), playBurst, playEvery) {
		writeJSON(w, http.StatusOK, map[string]any{"recorded": false})
		return
	}
	if err := s.collections.RecordPlay(user.ID, item, time.Now().UTC()); err != nil {
		s.collectionsError(w, err)
		return
	}
	go s.sendScrobbles(user.ID)
	writeJSON(w, http.StatusOK, map[string]any{"recorded": true})
}

// allowance lets each person make a burst of some write and then one every
// so often: plays (skipping through a queue is quick, but never this quick
// for long) and reading positions (each rewrites state.json, under the lock
// every request takes).
type allowance struct {
	mu   sync.Mutex
	left map[string]playBucket
}

type playBucket struct {
	tokens float64
	at     time.Time
}

const (
	playBurst     = 20
	playEvery     = 10 * time.Second
	positionBurst = 30
	positionEvery = 2 * time.Second
)

func (p *allowance) allow(userID string, now time.Time, burst float64, every time.Duration) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.left == nil {
		p.left = map[string]playBucket{}
	}
	b, ok := p.left[userID]
	if !ok {
		b = playBucket{tokens: burst, at: now}
	}
	b.tokens = min(burst, b.tokens+now.Sub(b.at).Seconds()/every.Seconds())
	b.at = now
	if b.tokens < 1 {
		p.left[userID] = b
		return false
	}
	b.tokens--
	p.left[userID] = b
	return true
}

// mixSource finds the music shelf that can draw random songs.
func (s *Server) mixSource(r *http.Request) (source.Source, source.MixSource, bool) {
	for _, src := range s.reg.All(r.Context()) {
		if src.Kind() != media.KindMusic {
			continue
		}
		if m, ok := src.(source.MixSource); ok {
			return src, m, true
		}
	}
	return nil, nil, false
}

// history is this person's plays, on shelves they can still see.
func (s *Server) history(r *http.Request, userID string) []collections.Play {
	plays, err := s.collections.History(userID)
	if err != nil {
		return nil
	}
	out := plays[:0]
	for _, p := range plays {
		if _, ok := s.reg.ByID(r.Context(), p.Item.SourceID); ok {
			out = append(out, p)
		}
	}
	return out
}

func covers(items []media.Item) []string {
	var out []string
	seen := map[string]bool{}
	for _, it := range items {
		if it.ArtID != "" && !seen[it.ArtID] {
			seen[it.ArtID] = true
			out = append(out, it.ArtID)
			if len(out) == 4 {
				break
			}
		}
	}
	return out
}

func (s *Server) handleMixes(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	src, mixer, ok := s.mixSource(r)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"mixes": []mixCard{}})
		return
	}
	var cards []mixCard

	// The songs this person has hearted, first: the mix most their own.
	if favs := s.favoriteSongs(r, user.ID); len(favs) > 0 {
		cards = append(cards, mixCard{ID: "favorites", Title: "Your favorites",
			Subtitle: "Every song you've hearted, shuffled", Covers: covers(shuffleItems(favs)), SourceID: src.ID()})
	}

	// From what this person listens to.
	plays := s.history(r, user.ID)
	if len(plays) > 0 {
		byCount := sortedPlays(plays, func(a, b collections.Play) bool { return a.Count > b.Count })
		cards = append(cards, mixCard{ID: "most-played", Title: "Your most played",
			Subtitle: "What you come back to", Covers: covers(playItems(byCount)), SourceID: src.ID()})
		byLast := sortedPlays(plays, func(a, b collections.Play) bool { return a.Last.After(b.Last) })
		cards = append(cards, mixCard{ID: "recently-played", Title: "Recently played",
			Subtitle: "Picking up where you were", Covers: covers(playItems(byLast)), SourceID: src.ID()})
		if old := rediscover(plays, time.Now()); len(old) >= minMixSongs {
			cards = append(cards, mixCard{ID: "rediscover", Title: "Rediscover",
				Subtitle: "Favorites you haven't played lately", Covers: covers(playItems(old)), SourceID: src.ID()})
		}
		cards = append(cards, s.moreLikeCards(r, src, byCount)...)
	}

	// From the library itself.
	shuffle, _ := mixer.RandomSongs(r.Context(), 12, "", 0, 0)
	cards = append(cards, mixCard{ID: "shuffle", Title: "Shuffle everything",
		Subtitle: "Your whole library, mixed up", Covers: covers(shuffle), SourceID: src.ID()})
	cards = append(cards, s.libraryMixCards(r, src, mixer)...)

	writeJSON(w, http.StatusOK, map[string]any{"mixes": cards})
}

// libraryMixCards are the recently-added, genre and decade mixes: the same for
// everybody, so worked out once every few minutes rather than per request.
func (s *Server) libraryMixCards(r *http.Request, src source.Source, mixer source.MixSource) []mixCard {
	s.libMixes.mu.Lock()
	defer s.libMixes.mu.Unlock()
	if time.Since(s.libMixes.at) < libraryMixesFor && s.libMixes.cards != nil {
		return s.libMixes.cards
	}
	var cards []mixCard
	if browser, ok := src.(source.MusicBrowser); ok {
		if newest, err := browser.Albums(r.Context(), source.AlbumsNewest, 0, 4); err == nil && len(newest) > 0 {
			var art []string
			for _, a := range newest {
				if a.ArtID != "" {
					art = append(art, a.ArtID)
				}
			}
			cards = append(cards, mixCard{ID: "recently-added", Title: "Recently added",
				Subtitle: "The newest in your library", Covers: art, SourceID: src.ID()})
		}
	}
	if genres, err := mixer.Genres(r.Context()); err == nil {
		sort.Slice(genres, func(i, j int) bool { return genres[i].SongCount > genres[j].SongCount })
		for i, g := range genres {
			if i == 8 || g.SongCount < minMixSongs {
				break
			}
			sample, _ := mixer.RandomSongs(r.Context(), 8, g.Name, 0, 0)
			cards = append(cards, mixCard{ID: "genre:" + g.Name, Title: g.Name + " mix",
				Subtitle: fmt.Sprintf("%d songs", g.SongCount), Covers: covers(sample), SourceID: src.ID()})
		}
	}
	for decade := time.Now().Year() / 10 * 10; decade >= 1950; decade -= 10 {
		sample, err := mixer.RandomSongs(r.Context(), 20, "", decade, decade+9)
		if err != nil || len(sample) < minMixSongs {
			continue
		}
		cards = append(cards, mixCard{ID: "decade:" + strconv.Itoa(decade), Title: fmt.Sprintf("The %ds", decade),
			Subtitle: "From that decade", Covers: covers(sample), SourceID: src.ID()})
	}
	s.libMixes.cards, s.libMixes.at = cards, time.Now()
	return cards
}

func (s *Server) handleMix(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	src, mixer, ok := s.mixSource(r)
	if !ok {
		writeError(w, http.StatusNotFound, "no music library")
		return
	}
	id := r.PathValue("id")
	var songs []media.Item
	var err error
	switch {
	case id == "favorites":
		// All of them, not a mix's hundred: a favorites mix that left some out
		// would not be the favorites.
		writeJSON(w, http.StatusOK, map[string]any{"songs": nonNil(shuffleItems(s.favoriteSongs(r, user.ID)))})
		return
	case id == "shuffle":
		songs, err = mixer.RandomSongs(r.Context(), mixSize, "", 0, 0)
	case id == "most-played":
		songs = playItems(sortedPlays(s.history(r, user.ID), func(a, b collections.Play) bool { return a.Count > b.Count }))
	case id == "recently-played":
		songs = playItems(sortedPlays(s.history(r, user.ID), func(a, b collections.Play) bool { return a.Last.After(b.Last) }))
	case id == "rediscover":
		songs = shuffleItems(playItems(rediscover(s.history(r, user.ID), time.Now())))
	case id == "recently-added":
		songs, err = s.recentlyAdded(r, src)
	case strings.HasPrefix(id, "genre:"):
		songs, err = mixer.RandomSongs(r.Context(), mixSize, strings.TrimPrefix(id, "genre:"), 0, 0)
	case strings.HasPrefix(id, "decade:"):
		decade, convErr := strconv.Atoi(strings.TrimPrefix(id, "decade:"))
		if convErr != nil {
			writeError(w, http.StatusBadRequest, "no such mix")
			return
		}
		songs, err = mixer.RandomSongs(r.Context(), mixSize, "", decade, decade+9)
	case strings.HasPrefix(id, "artist:"):
		songs, err = s.artistMix(r, src, mixer, strings.TrimPrefix(id, "artist:"))
	case strings.HasPrefix(id, "like:"):
		songs, err = s.moreLike(r, src, strings.TrimPrefix(id, "like:"))
	default:
		writeError(w, http.StatusNotFound, "no such mix")
		return
	}
	if err != nil {
		s.musicError(w, err)
		return
	}
	if len(songs) > mixSize {
		songs = songs[:mixSize]
	}
	writeJSON(w, http.StatusOK, map[string]any{"songs": nonNil(songs)})
}

// recentlyAdded is the newest albums' songs, album by album.
func (s *Server) recentlyAdded(r *http.Request, src source.Source) ([]media.Item, error) {
	browser, ok := src.(source.MusicBrowser)
	if !ok {
		return nil, nil
	}
	newest, err := browser.Albums(r.Context(), source.AlbumsNewest, 0, 10)
	if err != nil {
		return nil, err
	}
	var songs []media.Item
	for _, a := range newest {
		_, tracks, err := browser.Album(r.Context(), a.ID)
		if err != nil {
			continue
		}
		songs = append(songs, tracks...)
		if len(songs) >= mixSize {
			break
		}
	}
	return songs, nil
}

// artistMix is an artist's own songs with others from their genres woven in -
// the nearest honest thing to "radio" without analyzing the audio.
func (s *Server) artistMix(r *http.Request, src source.Source, mixer source.MixSource, artistID string) ([]media.Item, error) {
	browser, ok := src.(source.MusicBrowser)
	if !ok {
		return nil, nil
	}
	_, albums, err := browser.Artist(r.Context(), artistID)
	if err != nil {
		return nil, err
	}
	var own []media.Item
	genres := map[string]int{}
	for _, a := range albums {
		_, tracks, err := browser.Album(r.Context(), a.ID)
		if err != nil {
			continue
		}
		for _, t := range tracks {
			own = append(own, t)
			if g := t.Extra["genre"]; g != "" {
				genres[g]++
			}
		}
	}
	own = shuffleItems(own)
	var others []media.Item
	seen := map[string]bool{}
	for _, t := range own {
		seen[t.ID] = true
	}
	// Artists like them, from what the owner's music discovery found out -
	// the thing an artist's radio should be. Genres are what is left without
	// it: the nearest thing without anybody to ask.
	if artist, _, err := browser.Artist(r.Context(), artistID); err == nil {
		for _, t := range s.similarSongs(r, browser, artist.Name, 6, 4) {
			if !seen[t.ID] {
				seen[t.ID] = true
				others = append(others, t)
			}
		}
	}
	for g := range genres {
		if len(others) > 0 {
			break
		}
		drawn, err := mixer.RandomSongs(r.Context(), 40, g, 0, 0)
		if err != nil {
			continue
		}
		for _, t := range drawn {
			if !seen[t.ID] {
				seen[t.ID] = true
				others = append(others, t)
			}
		}
	}
	others = shuffleItems(others)
	// Two of theirs, then one from their genres, and so on.
	var out []media.Item
	for i, j := 0, 0; i < len(own) || j < len(others); {
		for k := 0; k < 2 && i < len(own); k++ {
			out = append(out, own[i])
			i++
		}
		if j < len(others) {
			out = append(out, others[j])
			j++
		}
	}
	return out, nil
}

func sortedPlays(plays []collections.Play, less func(a, b collections.Play) bool) []collections.Play {
	out := append([]collections.Play(nil), plays...)
	sort.SliceStable(out, func(i, j int) bool { return less(out[i], out[j]) })
	return out
}

func playItems(plays []collections.Play) []media.Item {
	out := make([]media.Item, 0, len(plays))
	for _, p := range plays {
		out = append(out, p.Item)
	}
	return out
}

// rediscover is what somebody played at least twice and not in the last two
// months, most played first.
func rediscover(plays []collections.Play, now time.Time) []collections.Play {
	var out []collections.Play
	for _, p := range plays {
		if p.Count >= 2 && now.Sub(p.Last) > rediscoverAfter {
			out = append(out, p)
		}
	}
	return sortedPlays(out, func(a, b collections.Play) bool { return a.Count > b.Count })
}

func shuffleItems(items []media.Item) []media.Item {
	out := append([]media.Item(nil), items...)
	rand.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

// favoriteSongs is this person's favorite songs, on shelves they can see.
func (s *Server) favoriteSongs(r *http.Request, userID string) []media.Item {
	entries, err := s.collections.Favorites(userID)
	if err != nil {
		return nil
	}
	var songs []media.Item
	for _, it := range s.visible(r, entries) {
		if it.Kind == media.KindMusic {
			songs = append(songs, it)
		}
	}
	return songs
}

// limited puts an allowance in front of a handler: a burst, then one every
// so often, per person; past it, 429.
func (s *Server) limited(a *allowance, burst float64, every time.Duration, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		if !a.allow(user.ID, time.Now(), burst, every) {
			writeError(w, http.StatusTooManyRequests, "too many changes at once; wait a moment")
			return
		}
		h(w, r)
	}
}

// hlsSessions counts the video conversions each person has going: a play
// session is live while asked for in the last two minutes.
type hlsSessions struct {
	mu   sync.Mutex
	seen map[string]map[string]time.Time
	// source of each live session, so one left idle can be stopped
	sources map[string]string
	once    sync.Once
}

// hlsIdle is how long a play session may ask for nothing before its
// conversion is stopped: a closed film, or one paused for a long while
// (which carries on - the backend converts again from where it is asked).
const hlsIdle = 3 * time.Minute

// watch stops the conversions of sessions gone quiet, once a minute.
func (h *hlsSessions) watch(stop func(sourceID, session string)) {
	h.once.Do(func() {
		go func() {
			defer func() { _ = recover() }()
			t := time.NewTicker(time.Minute)
			defer t.Stop()
			for now := range t.C {
				for _, gone := range h.idle(now) {
					stop(gone[0], gone[1])
				}
			}
		}()
	})
}

func (h *hlsSessions) idle(now time.Time) [][2]string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out [][2]string
	for _, mine := range h.seen {
		for session, at := range mine {
			if session != "" && now.Sub(at) > hlsIdle {
				if src, ok := h.sources[session]; ok {
					out = append(out, [2]string{src, session})
					delete(h.sources, session)
				}
			}
		}
	}
	return out
}

func (h *hlsSessions) note(sourceID, session string) {
	if session == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sources == nil {
		h.sources = map[string]string{}
	}
	h.sources[session] = sourceID
}

const maxHLSSessions = 4

func (h *hlsSessions) allow(userID, session string, now time.Time) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.seen == nil {
		h.seen = map[string]map[string]time.Time{}
	}
	mine := h.seen[userID]
	if mine == nil {
		mine = map[string]time.Time{}
		h.seen[userID] = mine
	}
	for k, t := range mine {
		if now.Sub(t) > hlsIdle+2*time.Minute {
			delete(mine, k)
		}
	}
	// Live for the cap: asked for in the last two minutes.
	live := 0
	for _, t := range mine {
		if now.Sub(t) <= 2*time.Minute {
			live++
		}
	}
	if t, known := mine[session]; (!known || now.Sub(t) > 2*time.Minute) && live >= maxHLSSessions {
		return false
	}
	mine[session] = now
	return true
}

// queryValue is a parameter read case-blind, as Jellyfin reads them.
func queryValue(q url.Values, name string) string {
	for k, v := range q {
		if strings.EqualFold(k, name) && len(v) > 0 {
			return v[0]
		}
	}
	return ""
}
