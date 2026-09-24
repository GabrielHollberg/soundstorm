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
)

// Mixes: ready-made queues, the first thing under Music. Some come from the
// library (shuffle everything, recently added, a genre, a decade), some from
// what this person has listened to (most played, recently played,
// rediscover). The listening history is SoundStorm's, per person - see
// internal/collections.
//
// No "sounds like": that needs the audio of every track analysed, which is
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
	if err := s.collections.RecordPlay(user.ID, item, time.Now().UTC()); err != nil {
		s.collectionsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recorded": true})
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
				Subtitle: "Favourites you haven't played lately", Covers: covers(playItems(old)), SourceID: src.ID()})
		}
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
// the nearest honest thing to "radio" without analysing the audio.
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
	for g := range genres {
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
