package training

import (
	"math"
	"sort"
)

// What a model sees of one candidate moment - a frame where something rises
// - in measurements every server makes. Nothing here is a threshold from
// today's rule; the model weighs them itself.
var FeatureNames = []string{
	"sharp-highs rise",  // above 7kHz, dB from the quietest of the 3 frames before, /16
	"sharp-highs level", // dB against the song's loud highs (95th percentile), /10
	"bass rise",         // under 150Hz, dB from the frame before, /10
	"bass level",        // dB against the song's loud bass, /10
	"highs rise",        // above 2500Hz, dB from the frame before, /16
	"whole rise",        // the whole sound, dB from the frame before, /8
	"loudness",          // how loud this part of the song is, 0-1 within it
	"from the beat",     // distance to the nearest beat, as a share of a beat (0-0.5)
	"on a beat",         // within 70ms of one
	"beat 1",            // which beat of the bar, when on one
	"beat 2",            //
	"beat 3",            //
	"beat 4",            //
	"between beats",     // within 70ms of halfway between two
	"stands out",        // how strong against the song's typical moment (log)
}

// Song is one recorded song, heard, with its candidate moments.
type Song struct {
	Name    string
	Rec     Recording
	H       *Heard
	Cands   []Cand
	Latency float64   // the hand's lateness, seconds, this song
	Targets []float64 // the moments meant, on the song's clock
	Span    [2]float64
}

// Cand is a candidate moment.
type Cand struct {
	Frame    int
	At       float64
	Strength float64
	X        []float64 // FeatureNames, raw
	Label    bool
	Near     bool // close to a target without being it: kept out of training
}

const onBeatWithin = 0.07

func percentile(v []float32, q float64) float64 {
	s := make([]float64, len(v))
	for i, x := range v {
		s[i] = float64(x)
	}
	sort.Float64s(s)
	if len(s) == 0 {
		return 0
	}
	return s[int(math.Floor(float64(len(s)-1)*q))]
}

// beatAt is the nearest beat to t: its index, the signed distance, and the
// beat's length there.
func beatAt(bs []float64, t float64) (int, float64, float64) {
	if len(bs) == 0 {
		return -1, math.Inf(1), 0.5
	}
	i := sort.SearchFloat64s(bs, t)
	best := -1
	for _, j := range []int{i - 1, i} {
		if j >= 0 && j < len(bs) && (best < 0 || math.Abs(bs[j]-t) < math.Abs(bs[best]-t)) {
			best = j
		}
	}
	period := 0.5
	if best+1 < len(bs) {
		period = bs[best+1] - bs[best]
	} else if best > 0 {
		period = bs[best] - bs[best-1]
	}
	return best, t - bs[best], period
}

// NewSong finds a song's candidates, their measurements, and what the taps
// meant.
func NewSong(name string, r Recording, h *Heard) *Song {
	s := &Song{Name: name, Rec: r, H: h}
	n := len(h.Loud)
	if n < 8 {
		return s
	}
	p95hats, p95low := percentile(h.Hats, 0.95), percentile(h.Low, 0.95)
	rise := func(v []float32, f, back int) float64 {
		lo := float64(v[f-1])
		for k := max(0, f-back); k < f-1; k++ {
			lo = math.Min(lo, float64(v[k]))
		}
		return float64(v[f]) - lo
	}
	strength := make([]float64, n)
	for f := 3; f < n; f++ {
		strength[f] = math.Max(math.Max(rise(h.Hats, f, 3)/16, rise(h.Low, f, 1)/10), math.Max(rise(h.High, f, 1)/16, rise(h.Loud, f, 1)/8))
	}
	var strengths []float64
	for f := 3; f < n; f++ {
		if strength[f] < 0.15 {
			continue
		}
		peak := true
		for k := max(3, f-2); k <= min(n-1, f+2); k++ {
			if strength[k] > strength[f] || (strength[k] == strength[f] && k < f) {
				peak = false
				break
			}
		}
		if !peak {
			continue
		}
		at := float64(f) / h.FPS
		k, dist, period := beatAt(h.Result.Beats, at)
		on := math.Abs(dist) <= onBeatWithin
		pos := 0
		if on && k >= 0 {
			pos = ((k-h.Result.Down)%4+4)%4 + 1
		}
		half := math.Abs(math.Abs(dist)-period/2) <= onBeatWithin
		x := []float64{
			rise(h.Hats, f, 3) / 16,
			(float64(h.Hats[f]) - p95hats) / 10,
			rise(h.Low, f, 1) / 10,
			(float64(h.Low[f]) - p95low) / 10,
			rise(h.High, f, 1) / 16,
			rise(h.Loud, f, 1) / 8,
			float64(h.Result.Loud[min(f, len(h.Result.Loud)-1)]),
			math.Min(0.5, math.Abs(dist)/math.Max(period, 0.2)),
			b2f(on),
			b2f(pos == 1), b2f(pos == 2), b2f(pos == 3), b2f(pos == 4),
			b2f(half && !on),
			strength[f],
		}
		s.Cands = append(s.Cands, Cand{Frame: f, At: at, Strength: strength[f], X: x})
		strengths = append(strengths, strength[f])
	}
	// "Stands out": strength against the song's typical candidate.
	sort.Float64s(strengths)
	typical := 0.3
	if len(strengths) > 0 {
		typical = strengths[len(strengths)/2]
	}
	for i := range s.Cands {
		s.Cands[i].X[14] = math.Log(s.Cands[i].Strength / typical)
	}
	s.label()
	return s
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// label lines the taps up with the moments that set them off. A tap lands a
// little after what it answers - a hand's lateness, and the device's sound
// being behind its clock - so the song's typical lateness is found first:
// for each tap, the strongest candidate in the 400ms before it (or 100ms
// after); the median of those gaps. Then each tap is matched to the
// strongest candidate within 80ms of where that lateness puts it; a tap with
// nothing there is still a moment meant, at that place.
func (s *Song) label() {
	taps := s.Rec.Taps
	if len(taps) == 0 {
		return
	}
	strongestIn := func(lo, hi float64) int {
		best := -1
		for i, c := range s.Cands {
			if c.At >= lo && c.At <= hi && (best < 0 || c.Strength > s.Cands[best].Strength) {
				best = i
			}
		}
		return best
	}
	var gaps []float64
	for _, t := range taps {
		if i := strongestIn(t-0.4, t+0.1); i >= 0 {
			gaps = append(gaps, t-s.Cands[i].At)
		}
	}
	sort.Float64s(gaps)
	if len(gaps) > 0 {
		s.Latency = math.Max(-0.1, math.Min(0.35, gaps[len(gaps)/2]))
	}
	for _, t := range taps {
		at := t - s.Latency
		if i := strongestIn(at-0.08, at+0.08); i >= 0 {
			if !s.Cands[i].Label {
				s.Cands[i].Label = true
				s.Targets = append(s.Targets, s.Cands[i].At)
			}
			continue
		}
		s.Targets = append(s.Targets, at)
	}
	sort.Float64s(s.Targets)
	// Only where the taps were: from a little before the first to a little
	// after the last. A stretch never tapped says nothing either way.
	s.Span = [2]float64{taps[0] - s.Latency - 5, taps[len(taps)-1] - s.Latency + 5}
	for i, c := range s.Cands {
		if c.Label {
			continue
		}
		for _, t := range s.Targets {
			if math.Abs(c.At-t) < 0.15 {
				s.Cands[i].Near = true
				break
			}
		}
	}
}

// inSpan reports whether t is where the taps were.
func (s *Song) inSpan(t float64) bool { return t >= s.Span[0] && t <= s.Span[1] }
