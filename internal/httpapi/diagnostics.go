package httpapi

// Playback reports, on the developer's own install only (the same switch as
// training, SOUNDSTORM_TRAINING). Asked for after "the sound goes out and the
// song keeps going" in the Android app, which nothing in the code explained:
// the app keeps a log of what its native player saw - volume, audio focus,
// the audio output, errors - and Settings sends it here the moment the sound
// goes, kept as a file under the state dir's diagnostics/ to be read by hand.
//
//	POST /api/diagnostics/playback   {"log": "...", "page": {...}}

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	maxDiagnosticBytes = 512 << 10
	keepDiagnostics    = 30
)

func (s *Server) handlePlaybackReport(w http.ResponseWriter, r *http.Request) {
	if s.trainingDir == "" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var body struct {
		Log  string          `json:"log"`
		Page json.RawMessage `json:"page"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxDiagnosticBytes)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "expected a report")
		return
	}
	dir := filepath.Join(filepath.Dir(s.trainingDir), "diagnostics")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		writeError(w, http.StatusInternalServerError, "could not keep the report")
		return
	}
	var b strings.Builder
	b.WriteString("Playback report from " + user.Name + " at " + time.Now().UTC().Format(time.RFC3339) + "\n")
	b.WriteString("Device: " + r.UserAgent() + "\n\nPage:\n")
	b.Write(body.Page)
	b.WriteString("\n\nPlayer log (newest last):\n")
	b.WriteString(body.Log)
	name := time.Now().UTC().Format("20060102-150405") + "-playback.txt"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(b.String()), 0o600); err != nil {
		writeError(w, http.StatusInternalServerError, "could not keep the report")
		return
	}
	// Only the newest few.
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > keepDiagnostics {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		sort.Strings(names)
		for _, n := range names[:len(names)-keepDiagnostics] {
			_ = os.Remove(filepath.Join(dir, n))
		}
	}
	s.log.Info("playback report kept", "by", user.Name, "file", name)
	writeJSON(w, http.StatusOK, map[string]any{"kept": true})
}
