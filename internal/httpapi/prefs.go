package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/GabrielHollberg/soundstorm/internal/collections"
)

// A person's small choices - the order of a tab's pills, read-along's
// highlight, audiobook speed - kept with their favourites and playlists so
// they follow them from device to device.

const maxPrefsBody = 8 << 10

func (s *Server) handleGetPrefs(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	if s.collections == nil {
		writeJSON(w, http.StatusOK, collections.Prefs{})
		return
	}
	p, err := s.collections.Prefs(user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read your preferences")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handlePatchPrefs(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var ch collections.PrefsChange
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPrefsBody)).Decode(&ch); err != nil {
		writeError(w, http.StatusBadRequest, "expected preferences")
		return
	}
	if s.collections == nil {
		writeError(w, http.StatusServiceUnavailable, "preferences are not available")
		return
	}
	p, err := s.collections.ChangePrefs(user.ID, ch)
	if errors.Is(err, collections.ErrBadPrefs) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not save your preferences")
		return
	}
	writeJSON(w, http.StatusOK, p)
}
