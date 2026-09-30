package httpapi

// The server hears every song's beats once, ahead of time, so the app's
// visualizer follows a song from its first second without downloading it a
// second time to hear it (which, on a phone away from home, arrived minutes
// into the song).
//
//	GET /api/music/beats?source=&id=   what was heard in one song
//
// A song is fetched from Navidrome as mono FLAC (a transcoding SoundStorm
// adds to it, see subsonic/listen.go), decoded
// (internal/flac) and heard (internal/beats, hearSong from app.js step for
// step). The results are kept under the state dir's beats/, never in the
// music folders, a small file per song, and can always be made again: this
// is a cache, not a record of anybody's media. A background pass works
// through the library and picks up new songs after a music scan; a song
// asked for before the pass reaches it is heard there and then.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/beats"
	"github.com/GabrielHollberg/soundstorm/internal/flac"
	"github.com/GabrielHollberg/soundstorm/internal/httpx"
	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/source"
)

const (
	beatsFirstPass   = 3 * time.Minute // after starting, leave the backends to settle
	beatsAfterScan   = 2 * time.Minute // after a music scan, once Navidrome has indexed it
	beatsEvery       = 6 * time.Hour   // and a look for anything missed
	beatsRest        = 300 * time.Millisecond
	beatsSongTimeout = 3 * time.Minute  // fetch, decode and hear one song
	beatsMaxLength   = 30 * time.Minute // longer is a DJ mix or a whole album: not heard
	beatsRetryNone   = 30 * 24 * time.Hour
)

// beatStore keeps what was heard, and makes sure one song is heard once at
// a time however many ask.
type beatStore struct {
	dir  string
	kick chan struct{}
	// slot is how many songs are heard on request at once, beside the
	// background pass: each is a conversion in Navidrome and a decode here.
	slot chan struct{}
	mu   sync.Mutex
	busy map[string]chan struct{}
}

func newBeatStore(dir string) *beatStore {
	if dir == "" {
		return nil
	}
	return &beatStore{dir: dir, kick: make(chan struct{}, 1), slot: make(chan struct{}, 1), busy: map[string]chan struct{}{}}
}

// keptBeats is a file in the store: what was heard, or that nothing could be.
type keptBeats struct {
	beats.Kept
	None  bool      `json:"none,omitempty"`
	Why   string    `json:"why,omitempty"`
	Heard time.Time `json:"heard"`
}

func (b *beatStore) path(sourceID, id string) string {
	sum := sha256.Sum256([]byte(sourceID + "\x00" + id))
	h := hex.EncodeToString(sum[:16])
	return filepath.Join(b.dir, h[:2], h+".json")
}

func (b *beatStore) load(sourceID, id string) (keptBeats, bool) {
	raw, err := os.ReadFile(b.path(sourceID, id))
	if err != nil {
		return keptBeats{}, false
	}
	var k keptBeats
	if json.Unmarshal(raw, &k) != nil || k.V != beats.Version {
		return keptBeats{}, false
	}
	return k, true
}

func (b *beatStore) save(sourceID, id string, k keptBeats) error {
	p := b.path(sourceID, id)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	raw, err := json.Marshal(k)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".beats-*")
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

// kickBeats asks the background pass to look soon, after a music scan.
func (s *Server) kickBeats() {
	if s.beats == nil {
		return
	}
	select {
	case s.beats.kick <- struct{}{}:
	default:
	}
}

// songTempo is what the song's analysis (AudioMuse) says about its tempo and
// energy: the tempo folded into 70-150 bpm as the prior the beat search
// leans on, as the app folds it. Zero and nil when it has not heard the song.
func (s *Server) songTempo(ctx context.Context, id string) (float64, *beats.Sound) {
	am, ok := s.sonic(ctx)
	if !ok {
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	features, err := am.Features(ctx)
	f, heard := features[id]
	if err != nil || !heard || f.Tempo <= 0 {
		return 0, nil
	}
	bpm := f.Tempo
	for bpm < 70 {
		bpm *= 2
	}
	for bpm > 150 {
		bpm /= 2
	}
	return bpm, &beats.Sound{Tempo: f.Tempo, Energy: s.energyRank(features, id)}
}

// hearSong hears one song, or waits for the hearing already under way, and
// keeps the answer. Only one of any song at a time.
func (s *Server) hearSong(ctx context.Context, src source.Source, id string) (keptBeats, error) {
	key := src.ID() + "\x00" + id
	for {
		s.beats.mu.Lock()
		wait, busy := s.beats.busy[key]
		if !busy {
			done := make(chan struct{})
			s.beats.busy[key] = done
			s.beats.mu.Unlock()
			defer func() {
				s.beats.mu.Lock()
				delete(s.beats.busy, key)
				s.beats.mu.Unlock()
				close(done)
			}()
			break
		}
		s.beats.mu.Unlock()
		select {
		case <-wait:
			if k, ok := s.beats.load(src.ID(), id); ok {
				return k, nil
			}
		case <-ctx.Done():
			return keptBeats{}, ctx.Err()
		}
	}

	prior, sound := s.songTempo(ctx, id)
	heard, err := hearFLAC(ctx, src, id, prior)
	k := keptBeats{Heard: time.Now().UTC()}
	switch {
	case err == nil:
		k.Kept = heard.Keep(sound, prior)
	case errors.Is(err, beats.ErrTooShort) || errors.Is(err, errTooLong) || errors.Is(err, errUndecodable):
		// Not a song to hear: kept as none, so it is not fetched again and
		// again. Looked at once more in a month.
		k.Kept = beats.Kept{V: beats.Version}
		k.None, k.Why = true, err.Error()
	default:
		return keptBeats{}, err // the network or Navidrome: tried again later
	}
	if err := s.beats.save(src.ID(), id, k); err != nil {
		s.log.Warn("could not keep a song's beats", "err", err)
	}
	return k, nil
}

var (
	errTooLong     = errors.New("longer than 30 minutes")
	errUndecodable = errors.New("could not be decoded")
)

// hearFLAC fetches a song as FLAC and hears it as it arrives.
func hearFLAC(ctx context.Context, src source.Source, id string, prior float64) (*beats.Result, error) {
	lt, ok := src.(source.Listener)
	if !ok {
		return nil, errors.New("this library cannot hand songs over to be heard")
	}
	ctx, cancel := context.WithTimeout(ctx, beatsSongTimeout)
	defer cancel()
	if err := lt.PrepareListening(ctx); err != nil {
		return nil, err
	}
	t, err := lt.ListenTarget(ctx, id)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.URL, nil)
	if err != nil {
		return nil, httpx.Redact(err)
	}
	for k, v := range t.Headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, httpx.Redact(err) // a Subsonic URL carries its credential
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("navidrome answered %d", resp.StatusCode)
	}
	var a *beats.Analyzer
	var limit int
	var seen int
	_, err = flac.Decode(resp.Body, func(info flac.Info, ch [][]int32) error {
		if a == nil {
			a = beats.New(info.SampleRate)
			limit = info.SampleRate * int(beatsMaxLength/time.Second)
		}
		scale := 1 / math.Ldexp(1, info.Bits-1)
		left := ch[0]
		if len(ch) > 1 {
			right := ch[1]
			for i := range left {
				// The two channels averaged, as the app's decode is: left and
				// right, whatever else there is.
				a.Add((float64(left[i])*scale + float64(right[i])*scale) * 0.5)
			}
		} else {
			for _, v := range left {
				a.Add(float64(v) * scale)
			}
		}
		seen += len(left)
		if seen > limit {
			return errTooLong
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, errTooLong) {
			return nil, errTooLong
		}
		if errors.Is(err, flac.ErrNotFLAC) {
			// Navidrome sent something else (its own format when it does not
			// know ours): a setup problem, not this song's, tried again later.
			return nil, errors.New("navidrome did not convert the song to FLAC")
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var nerr interface{ Timeout() bool }
		if errors.As(err, &nerr) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", errUndecodable, err)
	}
	if a == nil {
		return nil, errUndecodable
	}
	return a.Hear(prior)
}

// handleSongBeats answers what was heard in one song, hearing it now if the
// background pass has not reached it yet.
func (s *Server) handleSongBeats(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	if s.beats == nil {
		writeError(w, http.StatusNotFound, "songs are not heard on this server")
		return
	}
	q := r.URL.Query()
	id := q.Get("id")
	src, ok := s.reg.ByID(r.Context(), q.Get("source"))
	if !ok || id == "" || len(id) > 128 {
		writeError(w, http.StatusNotFound, "no such song")
		return
	}
	lt, ok := src.(source.Listener)
	if !ok {
		writeError(w, http.StatusNotFound, "no such song")
		return
	}
	k, ok := s.beats.load(src.ID(), id)
	if !ok {
		// Without the backend's help, nothing can be heard here: quickly
		// no, and the phone hears the song itself.
		if err := lt.PrepareListening(r.Context()); err != nil {
			writeError(w, http.StatusNotFound, "songs are not heard on this server")
			return
		}
		// Only a song that is on the shelf is heard: every hearing is a
		// conversion in Navidrome.
		ig, isGetter := src.(source.ItemGetter)
		if !isGetter {
			writeError(w, http.StatusNotFound, "no such song")
			return
		}
		if item, found := ig.ItemByID(r.Context(), id); !found || item.Kind != media.KindMusic {
			writeError(w, http.StatusNotFound, "no such song")
			return
		}
		// One heard on request at a time; the rest hear it themselves.
		select {
		case s.beats.slot <- struct{}{}:
		case <-time.After(10 * time.Second):
			writeError(w, http.StatusServiceUnavailable, "busy hearing another song")
			return
		case <-r.Context().Done():
			return
		}
		var err error
		k, err = s.hearSong(r.Context(), src, id)
		<-s.beats.slot
		if err != nil {
			s.log.Info("could not hear a song", "source", src.ID(), "err", err)
			writeError(w, http.StatusServiceUnavailable, "could not hear this song")
			return
		}
	}
	if k.None {
		writeError(w, http.StatusNotFound, "this song has no beats to hear: "+k.Why)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	writeJSON(w, http.StatusOK, k.Kept)
}

// RunBeats is the background pass: every song on every music shelf heard
// once, one at a time, resting between songs; again after a music scan and
// every few hours for anything new.
func (s *Server) RunBeats(ctx context.Context) {
	if s.beats == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("hearing songs panicked; recovered", "panic", r, "stack", string(debug.Stack()))
		}
	}()
	next := time.NewTimer(beatsFirstPass)
	defer next.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.beats.kick:
			next.Reset(beatsAfterScan)
			continue
		case <-next.C:
		}
		s.beatsPass(ctx)
		next.Reset(beatsEvery)
	}
}

func (s *Server) beatsPass(ctx context.Context) {
	started := time.Now()
	heard, failed, total := 0, 0, 0
	var features map[string]bool // songs whose analysis now has a tempo
	if am, ok := s.sonic(ctx); ok {
		fctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		if all, err := am.Features(fctx); err == nil {
			features = make(map[string]bool, len(all))
			for id, f := range all {
				features[id] = f.Tempo > 0
			}
		}
		cancel()
	}
	for _, src := range s.reg.All(ctx) {
		lister, ok := src.(source.SongFileLister)
		lt, can := src.(source.Listener)
		if !ok || !can {
			continue
		}
		if err := lt.PrepareListening(ctx); err != nil {
			s.log.Warn("songs cannot be heard on the server; phones hear them instead", "source", src.ID(), "err", err)
			continue
		}
		lctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		songs, err := lister.SongFiles(lctx)
		cancel()
		if err != nil {
			s.log.Info("could not list songs to hear", "source", src.ID(), "err", err)
			continue
		}
		for _, song := range songs {
			if ctx.Err() != nil {
				return
			}
			total++
			id := song.Item.ID
			if k, ok := s.beats.load(src.ID(), id); ok {
				stale := k.None && time.Since(k.Heard) > beatsRetryNone
				// Heard before its analysis knew the tempo: heard again with
				// it, since the search leans on it.
				better := !k.None && k.Prior == 0 && features[id]
				if !stale && !better {
					continue
				}
			}
			if _, err := s.hearSong(ctx, src, id); err != nil {
				failed++
				if ctx.Err() == nil && failed%20 == 1 {
					s.log.Info("could not hear a song", "source", src.ID(), "err", err)
				}
			} else {
				heard++
				if heard%200 == 0 {
					s.log.Info("hearing songs", "heard", heard, "of", len(songs))
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(beatsRest):
			}
		}
	}
	if heard > 0 || failed > 0 {
		s.log.Info("songs heard", "new", heard, "failed", failed, "songs", total, "took", time.Since(started).Round(time.Second))
	}
}
