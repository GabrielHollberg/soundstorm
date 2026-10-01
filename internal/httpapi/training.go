package httpapi

// Training data for the looks, recorded by SoundStorm's developer and no one
// else: on the one install with SOUNDSTORM_TRAINING on, Now Playing has a
// training mode - tap where a big moment (lightning) should be, or hold and
// slide for how intense the music should feel - and what is recorded is kept
// here, a file per song under the state dir's training/. A training script
// learns from it, and what it learns ships in SoundStorm itself, for every
// install. Without the setting none of this exists: the routes answer 404
// and the session never mentions it.
//
//	GET /api/training/song?source=&id=   what was recorded for a song
//	PUT /api/training/song?source=&id=   replace it

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/source"
)

const (
	trainingMaxTaps      = 4000
	trainingMaxIntensity = 40000
	trainingMaxSeconds   = 6 * 60 * 60
	trainingMaxBody      = 2 << 20
)

// trainingSong is one song's recording.
type trainingSong struct {
	Source   string  `json:"source"`
	ID       string  `json:"id"`
	Title    string  `json:"title,omitempty"`
	Artist   string  `json:"artist,omitempty"`
	Album    string  `json:"album,omitempty"`
	Duration float64 `json:"duration,omitempty"`
	// Taps are the moments tapped, in seconds on the song's clock as the
	// recording device heard it (the player's time plus its Timing).
	Taps []float64 `json:"taps"`
	// Intensity is [seconds, 0-1] while a finger was held; a gap is a stretch
	// nothing was said about, not a zero.
	Intensity [][2]float64 `json:"intensity"`
	// Lead is the recording device's Timing setting, kept to tell later how
	// much of a tap's lateness was the device rather than the hand.
	Lead    float64   `json:"lead"`
	Device  string    `json:"device,omitempty"`
	Updated time.Time `json:"updated"`
}

func (s *Server) trainingPath(sourceID, id string) string {
	sum := sha256.Sum256([]byte(sourceID + "\x00" + id))
	return filepath.Join(s.trainingDir, hex.EncodeToString(sum[:16])+".json")
}

// trainingTarget is the song a training request names, or an answer already
// written: 404 everywhere training is off.
func (s *Server) trainingTarget(w http.ResponseWriter, r *http.Request) (source.Source, media.Item, bool) {
	if s.trainingDir == "" {
		http.NotFound(w, r)
		return nil, media.Item{}, false
	}
	user, ok := s.requireUser(w, r)
	if !ok {
		return nil, media.Item{}, false
	}
	// What is recorded shapes what ships to every install: the owner's
	// alone, not anybody with an account here (a security review).
	if !user.IsOwner() {
		http.NotFound(w, r)
		return nil, media.Item{}, false
	}
	q := r.URL.Query()
	id := q.Get("id")
	src, ok := s.reg.ByID(r.Context(), q.Get("source"))
	ig, getter := src.(source.ItemGetter)
	if !ok || !getter || id == "" || len(id) > 128 {
		writeError(w, http.StatusNotFound, "no such song")
		return nil, media.Item{}, false
	}
	item, found := ig.ItemByID(r.Context(), id)
	if !found || item.Kind != media.KindMusic {
		writeError(w, http.StatusNotFound, "no such song")
		return nil, media.Item{}, false
	}
	return src, item, true
}

func (s *Server) handleTrainingSong(w http.ResponseWriter, r *http.Request) {
	src, item, ok := s.trainingTarget(w, r)
	if !ok {
		return
	}
	raw, err := os.ReadFile(s.trainingPath(src.ID(), item.ID))
	if errors.Is(err, os.ErrNotExist) {
		writeJSON(w, http.StatusOK, trainingSong{Source: src.ID(), ID: item.ID, Taps: []float64{}, Intensity: [][2]float64{}})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read the recording")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(raw)
}

func (s *Server) handleSetTrainingSong(w http.ResponseWriter, r *http.Request) {
	src, item, ok := s.trainingTarget(w, r)
	if !ok {
		return
	}
	var body trainingSong
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, trainingMaxBody)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "expected the recording as JSON")
		return
	}
	moment := func(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= trainingMaxSeconds }
	if len(body.Taps) > trainingMaxTaps || len(body.Intensity) > trainingMaxIntensity || math.IsNaN(body.Lead) || math.Abs(body.Lead) > 5 {
		writeError(w, http.StatusBadRequest, "too much in one recording")
		return
	}
	for _, t := range body.Taps {
		if !moment(t) {
			writeError(w, http.StatusBadRequest, "a tap out of range")
			return
		}
	}
	for _, p := range body.Intensity {
		if !moment(p[0]) || math.IsNaN(p[1]) || p[1] < 0 || p[1] > 1 {
			writeError(w, http.StatusBadRequest, "an intensity out of range")
			return
		}
	}
	if len(body.Device) > 200 {
		body.Device = body.Device[:200]
	}
	// What the song is comes from the library, never the request.
	body.Source, body.ID = src.ID(), item.ID
	body.Title, body.Artist, body.Album = item.Title, strings.Join(item.Creators, ", "), item.Extra["album"]
	if d := item.DurationSeconds; d > 0 {
		body.Duration = d
	}
	if body.Taps == nil {
		body.Taps = []float64{}
	}
	if body.Intensity == nil {
		body.Intensity = [][2]float64{}
	}
	body.Updated = time.Now().UTC()
	p := s.trainingPath(src.ID(), item.ID)
	if len(body.Taps) == 0 && len(body.Intensity) == 0 {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			writeError(w, http.StatusInternalServerError, "could not clear the recording")
			return
		}
		writeJSON(w, http.StatusOK, body)
		return
	}
	if err := writeFileAtomic(p, body); err != nil {
		s.log.Warn("could not keep a training recording", "err", err)
		writeError(w, http.StatusInternalServerError, "could not keep the recording")
		return
	}
	writeJSON(w, http.StatusOK, body)
}

// writeFileAtomic writes v as JSON to a temporary name beside p and renames
// it over p.
func writeFileAtomic(p string, v any) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}
