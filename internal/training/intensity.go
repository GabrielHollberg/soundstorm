package training

import (
	"fmt"
	"math"
	"strings"
)

// Intensity: how big and fast the music should feel, slid along to songs.
// Today the looks take it from loudness alone; this asks how well a few more
// measurements, averaged around each moment, predict what was slid, against
// loudness alone - each scored on songs left out, as the big moments are.

var intensityNames = []string{"loudness", "bass level", "sharp-highs level", "hits a second", "tempo"}

type sample struct {
	x []float64
	y float64
}

func intensitySamples(s *Song) []sample {
	h := s.H
	n := len(h.Loud)
	if n == 0 || len(s.Rec.Intensity) == 0 {
		return nil
	}
	p95low, p95hats := percentile(h.Low, 0.95), percentile(h.Hats, 0.95)
	var out []sample
	for _, p := range s.Rec.Intensity {
		t := p[0] // a level, not a moment: no lateness taken off
		f := int(t * h.FPS)
		if f < 0 || f >= n {
			continue
		}
		w := int(h.FPS) // a second either side
		lo, hi := max(0, f-w), min(n-1, f+w)
		var loud, low, hats float64
		for k := lo; k <= hi; k++ {
			loud += float64(h.Result.Loud[min(k, len(h.Result.Loud)-1)])
			low += float64(h.Low[k]) - p95low
			hats += float64(h.Hats[k]) - p95hats
		}
		cnt := float64(hi - lo + 1)
		hits := 0
		for _, c := range s.Cands {
			if math.Abs(c.At-t) <= 1 && c.Strength >= 0.3 {
				hits++
			}
		}
		tempo := 0.0
		if _, _, period := beatAt(h.Result.Beats, t); period > 0 {
			tempo = 60 / period / 180
		}
		out = append(out, sample{x: []float64{loud / cnt, low / cnt / 20, hats / cnt / 20, float64(hits) / 2 / 8, tempo}, y: p[1]})
	}
	return out
}

// ridge fits y from the chosen columns of x, least squares with a little
// shrinkage, by gradient descent on scaled features.
func ridge(train []sample, cols []int) func([]float64) float64 {
	d := len(cols)
	mean, std := make([]float64, d), make([]float64, d)
	ym := 0.0
	for _, s := range train {
		for j, c := range cols {
			mean[j] += s.x[c]
		}
		ym += s.y
	}
	for j := range mean {
		mean[j] /= float64(len(train))
	}
	ym /= float64(len(train))
	for _, s := range train {
		for j, c := range cols {
			std[j] += (s.x[c] - mean[j]) * (s.x[c] - mean[j])
		}
	}
	for j := range std {
		std[j] = math.Sqrt(std[j] / float64(len(train)))
		if std[j] < 1e-9 {
			std[j] = 1
		}
	}
	w := make([]float64, d)
	g := make([]float64, d)
	for r := 0; r < 800; r++ {
		for j := range g {
			g[j] = 0
		}
		for _, s := range train {
			pred := ym
			for j, c := range cols {
				pred += w[j] * (s.x[c] - mean[j]) / std[j]
			}
			e := pred - s.y
			for j, c := range cols {
				g[j] += e * (s.x[c] - mean[j]) / std[j]
			}
		}
		for j := range w {
			w[j] -= 0.1 * (g[j]/float64(len(train)) + 1e-3*w[j])
		}
	}
	return func(x []float64) float64 {
		p := ym
		for j, c := range cols {
			p += w[j] * (x[c] - mean[j]) / std[j]
		}
		return math.Max(0, math.Min(1, p))
	}
}

// explained is how much of the slid intensity's variation a prediction
// accounts for, 1 being all of it, 0 no better than its average.
func explained(test []sample, pred func([]float64) float64) float64 {
	m := 0.0
	for _, s := range test {
		m += s.y
	}
	m /= float64(len(test))
	var res, tot float64
	for _, s := range test {
		res += (s.y - pred(s.x)) * (s.y - pred(s.x))
		tot += (s.y - m) * (s.y - m)
	}
	if tot == 0 {
		return 0
	}
	return 1 - res/tot
}

func intensityReport(songs []*Song) string {
	var withData []*Song
	per := map[*Song][]sample{}
	for _, s := range songs {
		if smp := intensitySamples(s); len(smp) > 0 {
			withData = append(withData, s)
			per[s] = smp
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\nINTENSITY - %d songs slid\n", len(withData))
	if len(withData) < 2 {
		b.WriteString("  (needs at least two songs slid: each is scored by a fit to the others)\n")
		return b.String()
	}
	all := []int{0, 1, 2, 3, 4}
	var lossLoud, lossAll float64
	for i, s := range withData {
		var train []sample
		for j, o := range withData {
			if j != i {
				train = append(train, per[o]...)
			}
		}
		loudOnly := explained(per[s], ridge(train, []int{0}))
		full := explained(per[s], ridge(train, all))
		lossLoud += loudOnly
		lossAll += full
		fmt.Fprintf(&b, "  %s: loudness alone explains %3.0f%%, with the rest %3.0f%%\n", s.Name, 100*loudOnly, 100*full)
	}
	n := float64(len(withData))
	fmt.Fprintf(&b, "  ALL: loudness alone (today) %3.0f%%, with %s %3.0f%%\n", 100*lossLoud/n, strings.Join(intensityNames[1:], ", "), 100*lossAll/n)
	return b.String()
}
