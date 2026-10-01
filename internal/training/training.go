// Package training learns the looks from what SoundStorm's developer
// recorded: where big moments (lightning) should be, tapped along to songs,
// and how intense the music should feel, slid along to them (httpapi's
// training mode, on one install alone). Run as `soundstorm train-looks`
// inside that install's container, it hears every recorded song again with
// the same analysis the server uses (internal/beats), keeps the raw readings
// at every moment, lines the taps up with the hits that set them off, fits a
// model and scores it - and today's rule - on songs it was not trained on.
// What it learns is written out to be built into SoundStorm, for every
// install, only if it does better than the rule.
//
// The model is deliberately simple to begin with: a logistic regression over
// measurements every server already makes, so it runs anywhere. It starts
// from nothing - none of today's thresholds are given to it.
package training

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/beats"
	"github.com/GabrielHollberg/soundstorm/internal/flac"
	"github.com/GabrielHollberg/soundstorm/internal/httpx"
	"github.com/GabrielHollberg/soundstorm/internal/source"
)

// Recording is one song's training recording, as httpapi keeps it.
type Recording struct {
	Source    string       `json:"source"`
	ID        string       `json:"id"`
	Title     string       `json:"title"`
	Artist    string       `json:"artist"`
	Taps      []float64    `json:"taps"`
	Intensity [][2]float64 `json:"intensity"`
	Lead      float64      `json:"lead"`
}

// LoadRecordings reads every recording in dir.
func LoadRecordings(dir string) ([]Recording, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []Recording
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || strings.HasPrefix(e.Name(), "model") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var r Recording
		if json.Unmarshal(raw, &r) != nil || r.ID == "" {
			continue
		}
		sort.Float64s(r.Taps)
		sort.Slice(r.Intensity, func(i, j int) bool { return r.Intensity[i][0] < r.Intensity[j][0] })
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Title < out[j].Title })
	return out, nil
}

// Heard is a song as the analysis hears it: the raw readings per frame and
// what the server's hearing made of them.
type Heard struct {
	FPS                   float64
	Loud, Low, High, Hats []float32 // dB per frame
	Result                *beats.Result
}

// Hear fetches a song as mono FLAC (the transcoding the server adds to
// Navidrome) and hears it, with the tempo the server leaned on (prior) so
// the beats found are the server's own.
func Hear(ctx context.Context, lt source.Listener, id string, prior float64) (*Heard, error) {
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
	_, err = flac.Decode(resp.Body, func(info flac.Info, ch [][]int32) error {
		if a == nil {
			a = beats.New(info.SampleRate)
		}
		scale := 1 / math.Ldexp(1, info.Bits-1)
		for i := range ch[0] {
			if len(ch) > 1 {
				a.Add((float64(ch[0][i])*scale + float64(ch[1][i])*scale) * 0.5)
			} else {
				a.Add(float64(ch[0][i]) * scale)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if a == nil {
		return nil, errors.New("no audio")
	}
	r, err := a.Hear(prior)
	if err != nil {
		return nil, err
	}
	loud, low, high, hats := a.Frames()
	return &Heard{FPS: float64(a.Rate()) / float64(a.Hop()), Loud: loud, Low: low, High: high, Hats: hats, Result: r}, nil
}

// Prior is the tempo the server's hearing of a song leaned on, from its
// cache under beatsDir, or 0.
func Prior(beatsDir, sourceID, id string) float64 {
	raw, err := os.ReadFile(beats.CachePath(beatsDir, sourceID, id))
	if err != nil {
		return 0
	}
	var k struct {
		Prior float64 `json:"prior"`
	}
	if json.Unmarshal(raw, &k) != nil {
		return 0
	}
	return k.Prior
}

// Run hears every recording and reports, writing the model to dir when one
// was fitted and it beat today's rule on songs it had not seen. hear is how a
// song is heard (Hear, in the real run).
func Run(ctx context.Context, dir string, recs []Recording, hear func(Recording) (*Heard, error), out func(string)) error {
	return run(ctx, dir, recs, hear, out, false)
}

// Rules scores versions of today's rule against the recordings instead of
// fitting a model.
func Rules(ctx context.Context, recs []Recording, hear func(Recording) (*Heard, error), out func(string)) error {
	return run(ctx, "", recs, hear, out, true)
}

func run(ctx context.Context, dir string, recs []Recording, hear func(Recording) (*Heard, error), out func(string), rules bool) error {
	var songs []*Song
	for _, r := range recs {
		if len(r.Taps) == 0 && len(r.Intensity) == 0 {
			continue
		}
		name := r.Title
		if r.Artist != "" {
			name += " - " + r.Artist
		}
		started := time.Now()
		h, err := hear(r)
		if err != nil {
			out(fmt.Sprintf("  could not hear %s: %v", name, err))
			continue
		}
		songs = append(songs, NewSong(name, r, h))
		out(fmt.Sprintf("  heard %s (%d taps, %d intensity points) in %s", name, len(r.Taps), len(r.Intensity), time.Since(started).Round(100*time.Millisecond)))
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	if len(songs) == 0 {
		return errors.New("nothing recorded yet: tap or slide along to some songs first (Looks, Training)")
	}
	if rules {
		out(RuleReport(songs))
		return nil
	}
	report, model := Evaluate(songs)
	out(report)
	// Only a model that beat today's rule on songs it had not seen is worth
	// building in; a fitted one that did not would make the looks worse.
	if model != nil && !modelBeatsRule(songs) {
		out("model not written: it did not beat today's rule")
		model = nil
	}
	if model != nil {
		raw, err := json.MarshalIndent(model, "", "  ")
		if err != nil {
			return err
		}
		p := filepath.Join(dir, "model-big-moments.json")
		if err := os.WriteFile(p, raw, 0o644); err != nil {
			return err
		}
		out("model written to " + p)
	}
	return nil
}

// modelBeatsRule scores the model as Evaluate does - each tapped song by a
// model fitted on the others - against today's rule on the same songs, and
// reports whether it did strictly better overall.
func modelBeatsRule(songs []*Song) bool {
	var tapped []*Song
	for _, s := range songs {
		if len(s.Rec.Taps) > 0 {
			tapped = append(tapped, s)
		}
	}
	if len(tapped) < 2 {
		return false
	}
	var ruleAll, modelAll score
	for i, s := range tapped {
		ruleAll.add(match(ruleStrikes(s), s.Targets))
		others := append(append([]*Song{}, tapped[:i]...), tapped[i+1:]...)
		if m := fit(others); m != nil {
			modelAll.add(match(modelStrikes(m, s), s.Targets))
		}
	}
	f := func(a score) float64 { return ratio(2*a.found, 2*a.found+a.extra+a.missed) }
	return f(modelAll) > f(ruleAll)
}
