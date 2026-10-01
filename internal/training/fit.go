package training

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Model is a fitted big-moments model, as written out to be built in.
type Model struct {
	Version   int       `json:"version"`
	Kind      string    `json:"kind"`
	Features  []string  `json:"features"`
	Mean      []float64 `json:"mean"`
	Std       []float64 `json:"std"`
	Weights   []float64 `json:"weights"`
	Bias      float64   `json:"bias"`
	Threshold float64   `json:"threshold"`
	Songs     int       `json:"songs"`
	Taps      int       `json:"taps"`
	Trained   time.Time `json:"trained"`
}

// prob is the model's belief that raw measurements x are a big moment.
func (m *Model) prob(x []float64) float64 {
	z := m.Bias
	for i, v := range x {
		z += m.Weights[i] * (v - m.Mean[i]) / m.Std[i]
	}
	return 1 / (1 + math.Exp(-z))
}

// examples are the candidates a model learns from: within the tapped span,
// and not merely near a moment meant.
func examples(songs []*Song) (xs [][]float64, ys []bool) {
	for _, s := range songs {
		for _, c := range s.Cands {
			if !s.inSpan(c.At) || c.Near {
				continue
			}
			xs = append(xs, c.X)
			ys = append(ys, c.Label)
		}
	}
	return xs, ys
}

// fit is a logistic regression by gradient descent, the features scaled to
// mean 0 and spread 1, the rarer big moments weighted up to count as much as
// everything else, and a little weight decay so a few songs cannot push any
// one measurement to an extreme. The threshold is the one that does best on
// the songs it was fitted to.
func fit(songs []*Song) *Model {
	xs, ys := examples(songs)
	pos := 0
	for _, y := range ys {
		if y {
			pos++
		}
	}
	if pos == 0 || pos == len(ys) {
		return nil
	}
	d := len(FeatureNames)
	m := &Model{Version: 1, Kind: "big-moments", Features: FeatureNames, Mean: make([]float64, d), Std: make([]float64, d), Weights: make([]float64, d)}
	for _, x := range xs {
		for i, v := range x {
			m.Mean[i] += v
		}
	}
	for i := range m.Mean {
		m.Mean[i] /= float64(len(xs))
	}
	for _, x := range xs {
		for i, v := range x {
			m.Std[i] += (v - m.Mean[i]) * (v - m.Mean[i])
		}
	}
	for i := range m.Std {
		m.Std[i] = math.Sqrt(m.Std[i] / float64(len(xs)))
		if m.Std[i] < 1e-6 {
			m.Std[i] = 1
		}
	}
	wPos := float64(len(ys)-pos) / float64(pos)
	const rate, decay, rounds = 0.5, 1e-3, 1500
	grad := make([]float64, d)
	for r := 0; r < rounds; r++ {
		for i := range grad {
			grad[i] = 0
		}
		gb, total := 0.0, 0.0
		for k, x := range xs {
			p := m.prob(x)
			y, w := 0.0, 1.0
			if ys[k] {
				y, w = 1, wPos
			}
			e := (p - y) * w
			for i, v := range x {
				grad[i] += e * (v - m.Mean[i]) / m.Std[i]
			}
			gb += e
			total += w
		}
		for i := range m.Weights {
			m.Weights[i] -= rate * (grad[i]/total + decay*m.Weights[i])
		}
		m.Bias -= rate * gb / total
	}
	// The threshold: the best F1 over the candidates fitted to.
	type scored struct {
		p float64
		y bool
	}
	var all []scored
	for k, x := range xs {
		all = append(all, scored{m.prob(x), ys[k]})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].p > all[j].p })
	tp, bestF, bestT := 0, -1.0, 0.5
	for i, s := range all {
		if s.y {
			tp++
		}
		fp := i + 1 - tp
		fn := pos - tp
		f := 2 * float64(tp) / float64(2*tp+fp+fn)
		if f > bestF {
			bestF, bestT = f, s.p
		}
	}
	m.Threshold = bestT
	for _, s := range songs {
		m.Taps += len(s.Rec.Taps)
	}
	m.Songs = len(songs)
	m.Trained = time.Now().UTC()
	return m
}

// score counts how predicted moments match the moments meant, within 70ms,
// each used once: found (true positives), extra (false positives) and
// missed.
type score struct{ found, extra, missed int }

func (a *score) add(b score) { a.found += b.found; a.extra += b.extra; a.missed += b.missed }

func (a score) String() string {
	p := ratio(a.found, a.found+a.extra)
	r := ratio(a.found, a.found+a.missed)
	f := ratio(2*a.found, 2*a.found+a.extra+a.missed)
	return fmt.Sprintf("caught %3.0f%% of your moments, %3.0f%% of its strikes wanted, overall %3.0f%%  (%d found, %d extra, %d missed)", 100*r, 100*p, 100*f, a.found, a.extra, a.missed)
}

func ratio(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}

func match(pred, want []float64) score {
	sort.Float64s(pred)
	used := make([]bool, len(want))
	var s score
	for _, p := range pred {
		best := -1
		for i, w := range want {
			if !used[i] && math.Abs(p-w) <= 0.07 && (best < 0 || math.Abs(p-w) < math.Abs(p-want[best])) {
				best = i
			}
		}
		if best >= 0 {
			used[best] = true
			s.found++
		} else {
			s.extra++
		}
	}
	for _, u := range used {
		if !u {
			s.missed++
		}
	}
	return s
}

// ruleStrikes is where today's rule strikes in a song (app.js strikesAt): a
// sharp high rising past 80% on the server's fixed scale, within 70ms of a
// beat.
func ruleStrikes(s *Song) []float64 {
	var out []float64
	hi := s.H.Result.High
	for f := 1; f < len(hi); f++ {
		if hi[f] >= 0.8 && hi[f-1] < 0.8 {
			at := float64(f) / s.H.FPS
			if _, dist, _ := beatAt(s.H.Result.Beats, at); math.Abs(dist) <= onBeatWithin && s.inSpan(at) {
				out = append(out, at)
			}
		}
	}
	return out
}

func modelStrikes(m *Model, s *Song) []float64 {
	var out []float64
	for _, c := range s.Cands {
		if s.inSpan(c.At) && m.prob(c.X) >= m.Threshold {
			out = append(out, c.At)
		}
	}
	return out
}

// Evaluate scores the model, fitted with each song left out in turn and
// tested on it, against today's rule on the same songs; then fits it on
// every song to be written out. It also reports intensity when any was
// recorded.
func Evaluate(songs []*Song) (string, *Model) {
	var b strings.Builder
	var tapped []*Song
	for _, s := range songs {
		if len(s.Rec.Taps) > 0 {
			tapped = append(tapped, s)
		}
	}
	fmt.Fprintf(&b, "\nBIG MOMENTS - %d songs tapped\n", len(tapped))
	var ruleAll, modelAll score
	for i, s := range tapped {
		rs := match(ruleStrikes(s), s.Targets)
		ruleAll.add(rs)
		fmt.Fprintf(&b, "\n%s\n  %d taps, your lateness about %.0fms\n  today's rule: %s\n", s.Name, len(s.Rec.Taps), s.Latency*1000, rs)
		if len(tapped) < 2 {
			continue
		}
		others := append(append([]*Song{}, tapped[:i]...), tapped[i+1:]...)
		if m := fit(others); m != nil {
			ms := match(modelStrikes(m, s), s.Targets)
			modelAll.add(ms)
			fmt.Fprintf(&b, "  the model:    %s\n", ms)
		}
	}
	fmt.Fprintf(&b, "\nALL SONGS\n  today's rule: %s\n", ruleAll)
	var model *Model
	if len(tapped) >= 2 {
		fmt.Fprintf(&b, "  the model:    %s\n  (each song scored by a model trained on the others)\n", modelAll)
		model = fit(tapped)
		if model != nil {
			fmt.Fprintf(&b, "\nWHAT IT LEARNT (on all %d songs; positive means more likely a big moment)\n", len(tapped))
			order := make([]int, len(model.Weights))
			for i := range order {
				order[i] = i
			}
			sort.Slice(order, func(a, c int) bool { return math.Abs(model.Weights[order[a]]) > math.Abs(model.Weights[order[c]]) })
			for _, i := range order {
				fmt.Fprintf(&b, "  %+6.2f  %s\n", model.Weights[i], model.Features[i])
			}
			better := ratio(2*modelAll.found, 2*modelAll.found+modelAll.extra+modelAll.missed) > ratio(2*ruleAll.found, 2*ruleAll.found+ruleAll.extra+ruleAll.missed)
			if better {
				b.WriteString("\nThe model did better than today's rule on songs it had not seen.\n")
			} else {
				b.WriteString("\nThe model did not beat today's rule yet; more songs, or more varied ones, should help.\n")
			}
		}
	} else {
		b.WriteString("  (the model needs at least two tapped songs: it is always tested on one it was not trained on)\n")
	}
	b.WriteString(intensityReport(songs))
	return b.String(), model
}

// LoadModel reads the model Run wrote to dir, or nil.
func LoadModel(dir string) (*Model, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "model-big-moments.json"))
	if err != nil {
		return nil, err
	}
	var m Model
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return &m, nil
}
