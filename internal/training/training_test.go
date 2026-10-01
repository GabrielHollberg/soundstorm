package training

import (
	"context"
	"math"
	"math/rand"
	"strings"
	"testing"

	"github.com/GabrielHollberg/soundstorm/internal/beats"
)

// click is a song at 120 bpm, a click on every beat, the first of each bar
// louder, as heard by the server's analysis.
func click(seconds int, seed int64) *Heard {
	const rate = 44100
	rng := rand.New(rand.NewSource(seed))
	pcm := make([]float64, rate*seconds)
	for i := range pcm {
		pcm[i] = 0.02 * (rng.Float64()*2 - 1)
	}
	for k := 0; ; k++ {
		at := 0.23 + float64(k)*0.5
		start := int(at * rate)
		if start >= len(pcm) {
			break
		}
		gain := 0.5
		if k%4 == 0 {
			gain = 1
		}
		for i := 0; i < rate/10 && start+i < len(pcm); i++ {
			t := float64(i) / rate
			pcm[start+i] += gain * math.Exp(-t*40) * (0.7*math.Sin(2*math.Pi*60*t) + 0.3*(rng.Float64()*2-1)*math.Exp(-t*200))
		}
	}
	a := beats.New(rate)
	for _, x := range pcm {
		a.Add(x)
	}
	r, err := a.Hear(120)
	if err != nil {
		panic(err)
	}
	loud, low, high, hats := a.Frames()
	return &Heard{FPS: float64(rate) / float64(a.Hop()), Loud: loud, Low: low, High: high, Hats: hats, Result: r}
}

// Taps only on each bar's first beat, 150ms late with a hand's wobble: the
// model should learn that, and do better than today's rule, which strikes
// on every click that rises sharply enough.
func TestLearnsTheFirstBeatOfEachBar(t *testing.T) {
	var recs []Recording
	heard := map[string]*Heard{}
	for i, id := range []string{"a", "b", "c"} {
		h := click(40, int64(i+1))
		rng := rand.New(rand.NewSource(int64(10 + i)))
		var taps []float64
		for k, b := range h.Result.Beats {
			if (k-h.Result.Down)%4 == 0 && b > 2 && b < 38 {
				taps = append(taps, b+0.15+(rng.Float64()-0.5)*0.04)
			}
		}
		recs = append(recs, Recording{Source: "s", ID: id, Title: "Song " + id, Taps: taps})
		heard[id] = h
	}
	var log strings.Builder
	dir := t.TempDir()
	err := Run(context.Background(), dir, recs, func(r Recording) (*Heard, error) { return heard[r.ID], nil }, func(s string) { log.WriteString(s + "\n") })
	if err != nil {
		t.Fatal(err)
	}
	report := log.String()
	t.Log(report)
	if !strings.Contains(report, "The model did better than today's rule") {
		t.Errorf("the model did not beat the rule on bar-start taps")
	}
	m, _ := LoadModel(dir)
	if m == nil {
		t.Fatal("no model written")
	}
	beat1 := m.Weights[9]
	for i := 10; i <= 12; i++ {
		if m.Weights[i] >= beat1 {
			t.Errorf("beat %d weighs %.2f, not under beat 1's %.2f", i-8, m.Weights[i], beat1)
		}
	}
}
