package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/GabrielHollberg/soundstorm/internal/lyrics"
	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/source"
)

// Albums and artists: browsing music the way people do, not as a list of four
// thousand song titles. Every call goes through the registry, so an account
// that may not see music gets nothing here either.

// maxAlbums bounds a whole album listing - far past a household's collection,
// short of a request that never ends.
const maxAlbums = 10000

// musicBrowser finds the music shelf that can list albums and artists.
func (s *Server) musicBrowser(r *http.Request) (source.MusicBrowser, bool) {
	for _, src := range s.reg.All(r.Context()) {
		if src.Kind() != media.KindMusic {
			continue
		}
		if b, ok := src.(source.MusicBrowser); ok {
			return b, true
		}
	}
	return nil, false
}

// browserFor is the named shelf, when it can list albums and artists.
func (s *Server) browserFor(w http.ResponseWriter, r *http.Request) (source.MusicBrowser, bool) {
	src, ok := s.reg.ByID(r.Context(), r.PathValue("source"))
	if !ok {
		writeError(w, http.StatusNotFound, "no such music library")
		return nil, false
	}
	b, ok := src.(source.MusicBrowser)
	if !ok {
		writeError(w, http.StatusNotFound, "no such music library")
		return nil, false
	}
	return b, true
}

func (s *Server) handleAlbums(w http.ResponseWriter, r *http.Request) {
	b, ok := s.musicBrowser(r)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"albums": []source.Album{}})
		return
	}
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		albums, _, err := b.SearchMusic(r.Context(), q)
		if err != nil {
			s.musicError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"albums": nonNil(albums)})
		return
	}
	order := r.URL.Query().Get("order")
	var all []source.Album
	for offset := 0; offset < maxAlbums; offset += 500 {
		page, err := b.Albums(r.Context(), order, offset, 500)
		if err != nil {
			s.musicError(w, err)
			return
		}
		all = append(all, page...)
		// A random order is a sample, not a list to page through: Subsonic
		// answers every page with a fresh draw.
		if len(page) < 500 || order == source.AlbumsRandom {
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"albums": nonNil(all)})
}

func (s *Server) handleAlbum(w http.ResponseWriter, r *http.Request) {
	b, ok := s.browserFor(w, r)
	if !ok {
		return
	}
	album, songs, err := b.Album(r.Context(), r.PathValue("id"))
	if err != nil {
		s.musicError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"album": album, "songs": nonNil(songs)})
}

func (s *Server) handleArtists(w http.ResponseWriter, r *http.Request) {
	b, ok := s.musicBrowser(r)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"artists": []source.Artist{}})
		return
	}
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		_, artists, err := b.SearchMusic(r.Context(), q)
		if err != nil {
			s.musicError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"artists": nonNil(artists)})
		return
	}
	artists, err := b.Artists(r.Context())
	if err != nil {
		s.musicError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"artists": nonNil(artists)})
}

func (s *Server) handleArtist(w http.ResponseWriter, r *http.Request) {
	b, ok := s.browserFor(w, r)
	if !ok {
		return
	}
	artist, albums, err := b.Artist(r.Context(), r.PathValue("id"))
	if err != nil {
		s.musicError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"artist": artist, "albums": nonNil(albums)})
}

func (s *Server) musicError(w http.ResponseWriter, err error) {
	s.log.Warn("music browsing", "err", err)
	writeError(w, http.StatusBadGateway, "the music library did not answer")
}

func nonNil[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}

// handleLyrics is a song's words, synced where the file has timings. Lyrics
// the music library has always win; LRCLIB is asked only for a song with none,
// and only when the owner has allowed it.
func (s *Server) handleLyrics(w http.ResponseWriter, r *http.Request) {
	src, ok := s.reg.ByID(r.Context(), r.PathValue("source"))
	if !ok {
		writeError(w, http.StatusNotFound, "no such music library")
		return
	}
	var found source.Lyrics
	if ls, ok := src.(source.LyricsSource); ok {
		l, err := ls.Lyrics(r.Context(), r.PathValue("id"))
		if err != nil {
			s.musicError(w, err)
			return
		}
		found = l
	}
	answer := map[string]any{"synced": found.Synced, "lines": nonNil(found.Lines)}
	if len(found.Lines) == 0 && s.lyrics != nil && s.store.OnlineLyrics() {
		if l, ok := s.onlineLyrics(r, src, r.PathValue("id")); ok {
			answer = map[string]any{"synced": l.Synced, "lines": nonNil(l.Lines), "from": "lrclib"}
		}
	}
	writeJSON(w, http.StatusOK, answer)
}

// onlineLyrics asks LRCLIB about one song, by what the library says it is.
// Any failure is quietly no lyrics: the song plays either way.
func (s *Server) onlineLyrics(r *http.Request, src source.Source, id string) (source.Lyrics, bool) {
	ig, ok := src.(source.ItemGetter)
	if !ok {
		return source.Lyrics{}, false
	}
	item, ok := ig.ItemByID(r.Context(), id)
	if !ok || item.Kind != media.KindMusic || len(item.Creators) == 0 {
		return source.Lyrics{}, false
	}
	l, found, err := s.lyrics.Find(r.Context(), lyrics.Song{
		Key:      src.ID() + "/" + id,
		Artist:   item.Creators[0],
		Title:    item.Title,
		Album:    item.Extra["album"],
		Duration: item.DurationSeconds,
	})
	if err != nil {
		s.log.Info("online lyrics", "err", err)
	}
	return l, found
}

// handleSetOnlineLyrics turns the LRCLIB lookup on or off. Owner-only: it
// decides whether song names leave the house.
func (s *Server) handleSetOnlineLyrics(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1024)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "expected a JSON body with enabled")
		return
	}
	if s.lyrics == nil {
		writeError(w, http.StatusNotImplemented, "online lyrics are not available")
		return
	}
	if err := s.store.SetOnlineLyrics(body.Enabled); err != nil {
		writeError(w, http.StatusInternalServerError, "could not save the setting")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": body.Enabled})
}
