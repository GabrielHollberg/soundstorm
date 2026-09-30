package beats

import (
	"encoding/binary"
	"math"
	"os"
	"testing"
)

// clickTrack is a minute at 120 bpm, the first beat at 0.23s, every fourth
// beat accented, and a quiet stretch from 20s to 35s: the track app.js's
// hearing was measured against.
func clickTrack(rate int) []float64 {
	n := rate * 60
	out := make([]float64, n)
	seed := uint32(1)
	noise := func() float64 {
		seed = seed*1664525 + 1013904223
		return float64(seed)/float64(1<<32)*2 - 1
	}
	for i := range out {
		out[i] = 0.02 * noise() // a bed, so quiet is not silence
	}
	for k := 0; ; k++ {
		at := 0.23 + float64(k)*0.5
		start := int(at * float64(rate))
		if start >= n {
			break
		}
		gain := 0.8
		if k%4 == 0 {
			gain = 1
		}
		if at >= 20 && at < 35 {
			gain *= 0.05
		}
		for i := 0; i < rate/10 && start+i < n; i++ {
			t := float64(i) / float64(rate)
			env := math.Exp(-t * 40)
			kick := math.Sin(2 * math.Pi * 60 * t)
			click := noise() * math.Exp(-t*200)
			out[start+i] += gain * env * (0.7*kick + 0.3*click)
		}
	}
	return out
}

func hear(t *testing.T, rate int, tempo float64) (*Result, []float64) {
	t.Helper()
	pcm := clickTrack(rate)
	a := New(rate)
	for _, x := range pcm {
		a.Add(x)
	}
	r, err := a.Hear(tempo)
	if err != nil {
		t.Fatal(err)
	}
	return r, pcm
}

func TestFindsTheBeatsOfAClickTrack(t *testing.T) {
	for _, rate := range []int{44100, 48000} {
		for _, tempo := range []float64{0, 120} {
			r, _ := hear(t, rate, tempo)
			// Every true beat has a found one within a frame and a half.
			worst, total, matched := 0.0, 0.0, 0
			for k := 0; ; k++ {
				at := 0.23 + float64(k)*0.5
				if at > 59.5 {
					break
				}
				near := math.Inf(1)
				for _, b := range r.Beats {
					near = math.Min(near, math.Abs(b-at))
				}
				if near < 0.035 {
					matched++
					total += near
					worst = math.Max(worst, near)
				}
			}
			if matched < 117 || len(r.Beats) > 122 {
				t.Errorf("rate %d tempo %v: %d of 119 beats found, %d in all", rate, tempo, matched, len(r.Beats))
			}
			if mean := total / float64(max(1, matched)); mean > 0.02 {
				t.Errorf("rate %d tempo %v: mean error %.0fms", rate, tempo, mean*1000)
			}
			// The quiet stretch is quiet, the rest loud.
			quiet := r.Loud[int(28*r.FPS)]
			loud := r.Loud[int(48*r.FPS)]
			if quiet > 0.3 || loud < 0.7 {
				t.Errorf("rate %d: loudness %.2f quiet, %.2f loud", rate, quiet, loud)
			}
		}
	}
}

func TestTooShort(t *testing.T) {
	a := New(44100)
	for i := 0; i < 44100*3; i++ {
		a.Add(0)
	}
	if _, err := a.Hear(0); err != ErrTooShort {
		t.Fatalf("got %v", err)
	}
}

// TestWritesTheClickTrackForTheAppCheck leaves the samples and what was heard
// where scripts/beats-parity.js can compare them with app.js's own hearing,
// when BEATS_PARITY_DIR is set.
func TestWritesTheClickTrackForTheAppCheck(t *testing.T) {
	dir := os.Getenv("BEATS_PARITY_DIR")
	if dir == "" {
		t.Skip("BEATS_PARITY_DIR not set")
	}
	r, pcm := hear(t, 44100, 120)
	raw := make([]byte, 4*len(pcm))
	for i, x := range pcm {
		binary.LittleEndian.PutUint32(raw[4*i:], math.Float32bits(float32(x)))
	}
	if err := os.WriteFile(dir+"/click.f32", raw, 0o644); err != nil {
		t.Fatal(err)
	}
	beats := make([]byte, 8*len(r.Beats))
	for i, b := range r.Beats {
		binary.LittleEndian.PutUint64(beats[8*i:], math.Float64bits(b))
	}
	if err := os.WriteFile(dir+"/click.beats", beats, 0o644); err != nil {
		t.Fatal(err)
	}
}
