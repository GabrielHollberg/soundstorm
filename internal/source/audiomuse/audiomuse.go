// Package audiomuse reads what AudioMuse-AI has heard in the music.
//
// Mood, energy and "sounds like" come from listening to the audio itself,
// which is machine learning over every track - the expensive layer this
// project does not own. So it is a backend, as read-along is: AudioMuse-AI
// (AGPL, Docker, run unmodified) pulls each song through Navidrome's own
// API, analyzes it once, and SoundStorm reads the result. Every endpoint
// used here was checked against 3.6.3 first.
//
// It is registered as a music source that finds nothing, like Storyteller is
// for ebooks, so an account that may not see music cannot reach it either.
package audiomuse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/httpx"
	"github.com/GabrielHollberg/soundstorm/internal/media"
)

// featuresFor is how long a read of the whole analysis is kept. Analysis adds
// songs slowly - seconds each - so half an hour behind costs nothing.
const featuresFor = 30 * time.Minute

// Config is one AudioMuse-AI.
type Config struct {
	ID      string
	BaseURL string
	Token   string // the API token SoundStorm generated at provisioning
	Timeout time.Duration
}

// Features is what the analysis heard in one song. Moods are its six
// classifiers (danceable, aggressive, happy, party, relaxed, sad), 0 to 1;
// Tags are its top style tags ("jazz", "instrumental"), 0 to 1.
type Features struct {
	Energy float64            `json:"energy"`
	Tempo  float64            `json:"tempo"`
	Key    string             `json:"key,omitempty"`
	Scale  string             `json:"scale,omitempty"`
	Moods  map[string]float64 `json:"moods,omitempty"`
	Tags   map[string]float64 `json:"tags,omitempty"`
}

// Analysis is how far the listening has got.
type Analysis struct {
	Running  bool   `json:"running"`
	Progress int    `json:"progress"`
	Message  string `json:"message,omitempty"`
	Songs    int    `json:"songs"`
}

// Source is an AudioMuse-AI client.
type Source struct {
	cfg  Config
	http *httpx.Client

	mu       sync.Mutex
	features map[string]Features
	fetched  time.Time
	loading  chan struct{}
	later    *time.Timer
}

// New builds a client.
func New(cfg Config) (*Source, error) {
	if cfg.Token == "" {
		return nil, errors.New("audiomuse: no token")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 20 * time.Second
	}
	c, err := httpx.New(cfg.BaseURL, cfg.Timeout)
	if err != nil {
		return nil, err
	}
	c.SetHeader("Authorization", "Bearer "+cfg.Token)
	return &Source{cfg: cfg, http: c}, nil
}

func (s *Source) ID() string                                                { return s.cfg.ID }
func (s *Source) Kind() media.Kind                                          { return media.KindMusic }
func (s *Source) Search(context.Context, media.Query) ([]media.Item, error) { return nil, nil }

// Health asks for the version, which needs the token.
func (s *Source) Health(ctx context.Context) error {
	resp, err := s.http.Do(ctx, httpx.Request{Path: "/api/version"})
	if err != nil {
		return err
	}
	return resp.Err()
}

func (s *Source) getJSON(ctx context.Context, path string, params url.Values, out any) error {
	resp, err := s.http.Do(ctx, httpx.Request{Path: path, Params: params})
	if err != nil {
		return err
	}
	if err := resp.Err(); err != nil {
		return err
	}
	if err := json.Unmarshal(resp.Body, out); err != nil {
		return fmt.Errorf("audiomuse %s: unreadable answer", path)
	}
	return nil
}

// syncTrack is one song as /api/sync exports it. The id is Navidrome's own
// song id - the one SoundStorm already uses - so nothing needs matching.
type syncTrack struct {
	ID            string   `json:"id"`
	Energy        *float64 `json:"energy"`
	Tempo         *float64 `json:"tempo"`
	Key           string   `json:"key"`
	Scale         string   `json:"scale"`
	MoodVector    string   `json:"mood_vector"`    // "jazz:0.628,instrumental:0.540"
	OtherFeatures string   `json:"other_features"` // "danceable:0.58,happy:0.62"
}

// scores reads AudioMuse's "name:0.5,other:0.25" lists.
func scores(list string) map[string]float64 {
	out := map[string]float64{}
	for _, part := range strings.Split(list, ",") {
		name, value, ok := strings.Cut(strings.TrimSpace(part), ":")
		if !ok {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil || v != v || v < 0 || v > 1 {
			continue
		}
		out[strings.ToLower(strings.TrimSpace(name))] = v
	}
	return out
}

// Features is everything analyzed so far, by song id - read whole, page by
// page, and kept for half an hour. Concurrent callers share one read.
func (s *Source) Features(ctx context.Context) (map[string]Features, error) {
	s.mu.Lock()
	if s.features != nil && time.Since(s.fetched) < featuresFor {
		f := s.features
		s.mu.Unlock()
		return f, nil
	}
	if wait := s.loading; wait != nil {
		s.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		s.mu.Lock()
		f := s.features
		s.mu.Unlock()
		if f == nil {
			return nil, errors.New("audiomuse: analysis could not be read")
		}
		return f, nil
	}
	done := make(chan struct{})
	s.loading = done
	s.mu.Unlock()

	f, err := s.readAll(ctx)

	s.mu.Lock()
	if err == nil {
		s.features, s.fetched = f, time.Now()
	}
	s.loading = nil
	close(done)
	stale := s.features
	s.mu.Unlock()
	if err != nil && stale != nil {
		// A read that failed keeps the last good one rather than losing moods.
		return stale, nil
	}
	return f, err
}

// maxPages bounds a read: 500 songs a page, so 200,000 songs.
const maxPages = 400

func (s *Source) readAll(ctx context.Context) (map[string]Features, error) {
	out := map[string]Features{}
	for page := 1; page <= maxPages; page++ {
		var r struct {
			HasMore bool        `json:"has_more"`
			Tracks  []syncTrack `json:"tracks"`
		}
		params := url.Values{"include_embeddings": {"false"}, "limit": {"500"}, "page": {strconv.Itoa(page)}}
		if err := s.getJSON(ctx, "/api/sync", params, &r); err != nil {
			return nil, err
		}
		for _, t := range r.Tracks {
			if t.ID == "" {
				continue
			}
			f := Features{Key: t.Key, Scale: t.Scale, Moods: scores(t.OtherFeatures), Tags: scores(t.MoodVector)}
			if t.Energy != nil {
				f.Energy = *t.Energy
			}
			if t.Tempo != nil {
				f.Tempo = *t.Tempo
			}
			out[t.ID] = f
		}
		if !r.HasMore || len(r.Tracks) == 0 {
			break
		}
	}
	return out, nil
}

// Similar is the songs that sound most like one, nearest first - by the
// audio, with its mood kept close, and a few per artist at most.
func (s *Source) Similar(ctx context.Context, songID string, n int) ([]string, error) {
	var r []struct {
		ItemID string `json:"item_id"`
	}
	params := url.Values{"item_id": {songID}, "n": {strconv.Itoa(n)}, "eliminate_duplicates": {"true"}}
	if err := s.getJSON(ctx, "/api/similar_tracks", params, &r); err != nil {
		var status *httpx.StatusError
		if errors.As(err, &status) && status.Status == 404 {
			// Not analyzed yet, or nothing near it: no answer, not a failure.
			return nil, nil
		}
		return nil, err
	}
	ids := make([]string, 0, len(r))
	for _, t := range r {
		if t.ItemID != "" && t.ItemID != songID {
			ids = append(ids, t.ItemID)
		}
	}
	return ids, nil
}

// Progress is how far the analysis has got.
func (s *Source) Progress(ctx context.Context) (Analysis, error) {
	var task struct {
		Status   string  `json:"status"`
		Progress float64 `json:"progress"`
		Details  struct {
			Message string `json:"status_message"`
		} `json:"details"`
	}
	if err := s.getJSON(ctx, "/api/last_task", nil, &task); err != nil {
		return Analysis{}, err
	}
	var index struct {
		Total int `json:"total_tracks"`
	}
	if err := s.getJSON(ctx, "/api/sync", url.Values{"fields": {"index"}, "limit": {"1"}}, &index); err != nil {
		return Analysis{}, err
	}
	switch strings.ToUpper(task.Status) {
	case "PENDING", "STARTED", "PROGRESS", "RUNNING", "QUEUED", "NEW":
		return Analysis{Running: true, Progress: int(task.Progress), Message: task.Details.Message, Songs: index.Total}, nil
	}
	return Analysis{Progress: int(task.Progress), Songs: index.Total}, nil
}

// Analyze starts listening to whatever is new, unless it already is.
func (s *Source) Analyze(ctx context.Context) error {
	a, err := s.Progress(ctx)
	if err == nil && a.Running {
		return nil
	}
	resp, err := s.http.Do(ctx, httpx.Request{Method: "POST", Path: "/api/analysis/start", Body: map[string]any{}})
	if err != nil {
		return err
	}
	return resp.Err()
}

// listenAfter is how long after "look for new files" the listen starts: the
// music server scans first, and AudioMuse-AI finds new songs through it.
const listenAfter = 3 * time.Minute

// Rescan answers SoundStorm's "look for new files": a listen for what is new,
// once the music server has indexed it. A burst of uploads is one listen.
func (s *Source) Rescan(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.later != nil {
		s.later.Stop()
	}
	s.later = time.AfterFunc(listenAfter, func() {
		defer func() { _ = recover() }()
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_ = s.Analyze(ctx)
	})
	return nil
}
