package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/collections"
	"github.com/GabrielHollberg/soundstorm/internal/source/audiomuse"
)

// Covers of one's own (collections/art.go), and the sound of a song for Now
// Playing's moving cover.

// handleMyArt lists this person's own covers: what each replaces, and where
// the picture is.
func (s *Server) handleMyArt(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	art, err := s.collections.Art(user.ID)
	if err != nil {
		s.collectionsError(w, err)
		return
	}
	out := make(map[string]string, len(art))
	for k, f := range art {
		out[k] = "/api/myart/" + f
	}
	writeJSON(w, http.StatusOK, map[string]any{"art": out})
}

// artKeysFor checks the ?key= values name shelves this account can see, or
// playlists of its own.
func (s *Server) artKeysFor(r *http.Request, userID string) ([]string, bool) {
	keys := r.URL.Query()["key"]
	if len(keys) == 0 {
		return nil, false
	}
	for _, k := range keys {
		if id, ok := strings.CutPrefix(k, "playlist:"); ok {
			if _, err := s.collections.Playlist(userID, id); err != nil {
				return nil, false
			}
			continue
		}
		_, rest, ok := strings.Cut(k, ":")
		src, _, ok2 := strings.Cut(rest, "/")
		if !ok || !ok2 {
			return nil, false
		}
		if _, ok := s.reg.ByID(r.Context(), src); !ok {
			return nil, false
		}
	}
	return keys, true
}

// handleSetMyArt stores the request body, an image, as this person's cover
// for every ?key=.
func (s *Server) handleSetMyArt(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	keys, ok := s.artKeysFor(r, user.ID)
	if !ok {
		writeError(w, http.StatusBadRequest, "say which covers to replace")
		return
	}
	if !s.artUploads.allow(user.ID, time.Now(), 20, 3*time.Second) {
		writeError(w, http.StatusTooManyRequests, "Too many at once; wait a moment.")
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, collections.MaxArtBytes+1))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "That picture is too big.")
		return
	}
	switch err := s.collections.SetArt(user.ID, keys, data); {
	case errors.Is(err, collections.ErrBadArt):
		writeError(w, http.StatusUnsupportedMediaType, "Choose a JPEG, PNG or WebP picture.")
		return
	case err != nil:
		s.collectionsError(w, err)
		return
	}
	s.handleMyArt(w, r)
}

// handleRemoveMyArt puts the original covers back.
func (s *Server) handleRemoveMyArt(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	keys := r.URL.Query()["key"]
	if len(keys) == 0 {
		writeError(w, http.StatusBadRequest, "say which covers to put back")
		return
	}
	if err := s.collections.RemoveArt(user.ID, keys); err != nil {
		s.collectionsError(w, err)
		return
	}
	s.handleMyArt(w, r)
}

// handleMyArtFile serves one of this person's own pictures - never anybody
// else's. Named by content, so it can be cached for good.
func (s *Server) handleMyArtFile(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	path, ok := s.collections.ArtPath(user.ID, name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	types := map[string]string{"jpg": "image/jpeg", "png": "image/png", "webp": "image/webp"}
	w.Header().Set("Content-Type", types[name[strings.LastIndex(name, ".")+1:]])
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox")
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	http.ServeFile(w, r, path)
}

// soundCache is every analyzed song's energy as a rank within the library,
// for the analysis it came from.
type soundCache struct {
	mu     sync.Mutex
	from   uintptr
	energy map[string]float64
}

// handleSongSound is a song's tempo and how energetic it is next to the rest
// of the library, from the sound analysis - what Now Playing's moving cover
// keeps time with. Unknown until the analysis has heard the song.
func (s *Server) handleSongSound(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	id := r.URL.Query().Get("id")
	am, ok := s.sonic(r.Context())
	if !ok || id == "" {
		writeJSON(w, http.StatusOK, map[string]any{"known": false})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	features, err := am.Features(ctx)
	f, heard := features[id]
	if err != nil || !heard || f.Tempo <= 0 {
		writeJSON(w, http.StatusOK, map[string]any{"known": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"known": true, "tempo": f.Tempo, "energy": s.energyRank(features, id)})
}

// energyRank is id's energy as a fraction of the library quieter than it.
func (s *Server) energyRank(features map[string]audiomuse.Features, id string) float64 {
	c := &s.sounds
	c.mu.Lock()
	defer c.mu.Unlock()
	key := reflect.ValueOf(features).Pointer()
	if c.energy == nil || c.from != key {
		ids := make([]string, 0, len(features))
		for k := range features {
			ids = append(ids, k)
		}
		sort.Slice(ids, func(i, j int) bool { return features[ids[i]].Energy < features[ids[j]].Energy })
		c.energy = make(map[string]float64, len(ids))
		for i, k := range ids {
			c.energy[k] = float64(i) / float64(max(1, len(ids)-1))
		}
		c.from = key
	}
	return c.energy[id]
}
