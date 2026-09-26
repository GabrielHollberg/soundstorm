package httpapi

import (
	"net/http"

	"github.com/GabrielHollberg/soundstorm/internal/source"
)

// A television series' page - its episodes, season by season, and how far
// this person is through each - and the episode after one that has ended.
// The positions are SoundStorm's own (a film's or an episode's place is kept
// per person, see "The Continue row"), so one person's half-watched episode
// is nobody else's.

func (s *Server) showSource(w http.ResponseWriter, r *http.Request) (source.ShowBrowser, string, bool) {
	sourceID := r.URL.Query().Get("source")
	src, ok := s.reg.ByID(r.Context(), sourceID)
	if !ok {
		writeError(w, http.StatusNotFound, "no such shelf")
		return nil, "", false
	}
	shows, ok := src.(source.ShowBrowser)
	if !ok {
		writeError(w, http.StatusNotFound, "that shelf has no shows")
		return nil, "", false
	}
	return shows, sourceID, true
}

// handleShow is a series' episodes, and this person's place in each.
func (s *Server) handleShow(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	shows, sourceID, ok := s.showSource(w, r)
	if !ok {
		return
	}
	episodes, err := shows.Episodes(r.Context(), r.URL.Query().Get("id"))
	if err != nil {
		s.log.Warn("show", "err", err)
		writeError(w, http.StatusNotFound, "could not load that show")
		return
	}
	progress := map[string]float64{}
	for _, e := range episodes {
		if p, ok := s.store.Progress(progressKey(user.ID, sourceID, e.ID)); ok {
			progress[e.ID] = p.Fraction
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"episodes": nonNil(episodes), "progress": progress})
}

// handleNextEpisode is the episode after the one given, for playing on.
func (s *Server) handleNextEpisode(w http.ResponseWriter, r *http.Request) {
	shows, _, ok := s.showSource(w, r)
	if !ok {
		return
	}
	next, found, err := shows.NextEpisode(r.Context(), r.URL.Query().Get("id"))
	if err != nil {
		s.log.Warn("next episode", "err", err)
	}
	if err != nil || !found {
		writeJSON(w, http.StatusOK, map[string]any{"found": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"found": true, "episode": next})
}
