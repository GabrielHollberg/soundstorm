// Package beats hears a song the way the app's visualizer wants it heard:
// where every beat falls, which one starts each bar, and how loud and how
// punchy each moment is.
//
// It is hearSong in app.js, step for step - the same frames, filters,
// scaling, tempo search and beat tracker, with the same number types where
// they change a result (Float32Array there is float32 here) - so a song heard
// here and one heard on a phone come out the same. The server hears every
// song once, ahead of time, and a phone fetches the result instead of
// downloading the song a second time to hear it. Change one and the other
// must follow, with Version raised.
package beats

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"math"
	"sort"
)

// Version is the shape and method of a result, as the app's kept copies
// count it: a result of another version is heard again.
const Version = 8

// Analyzer takes a song's samples one at a time and hears it at the end.
type Analyzer struct {
	sr, hop     int
	kLow, kHigh float64
	lp, hp      float64
	sx, sl, sh  float64
	n           int
	loud, low   []float32
	high        []float32
	// The sharp highs: above 7kHz through four one-pole high-passes, where
	// hats and the crack of a snare are and a voice's body is not (though
	// its "s" is). Heard for the looks, not for finding the beats.
	kHat float64
	hat  [4]float64
	sHat float64
	hats []float32
}

// New is an analyzer for samples at rate a second: 256 samples a frame at
// 11025, and as many more at higher rates - about 23ms a frame whatever the
// rate, as on a phone.
func New(rate int) *Analyzer {
	hop := 256 * int(jsRound(float64(rate)/11025))
	if hop < 256 {
		hop = 256
	}
	return &Analyzer{
		sr: rate, hop: hop,
		// Two one-pole filters split the low end (kick, bass) from the top
		// (snare, hats): cheap, and enough to tell them apart.
		kLow:  1 - math.Exp((-2*math.Pi*150)/float64(rate)),
		kHigh: 1 - math.Exp((-2*math.Pi*2500)/float64(rate)),
		kHat:  1 - math.Exp((-2*math.Pi*math.Min(7000, 0.4*float64(rate)))/float64(rate)),
	}
}

// Add is the next sample, the channels averaged, between -1 and 1.
func (a *Analyzer) Add(x float64) {
	a.lp += a.kLow * (x - a.lp)
	a.hp += a.kHigh * (x - a.hp)
	h := x - a.hp
	a.sx += x * x
	a.sl += a.lp * a.lp
	a.sh += h * h
	y := x
	for p := range a.hat {
		a.hat[p] += a.kHat * (y - a.hat[p])
		y -= a.hat[p]
	}
	a.sHat += y * y
	a.n++
	if a.n == a.hop {
		hop := float64(a.hop)
		a.loud = append(a.loud, float32(10*math.Log10(a.sx/hop+1e-10)))
		a.low = append(a.low, float32(10*math.Log10(a.sl/hop+1e-10)))
		a.high = append(a.high, float32(10*math.Log10(a.sh/hop+1e-10)))
		a.hats = append(a.hats, float32(10*math.Log10(a.sHat/hop+1e-10)))
		a.sx, a.sl, a.sh, a.sHat, a.n = 0, 0, 0, 0, 0
	}
}

// Result is what was heard.
type Result struct {
	FPS   float64
	Loud  []float32 // 0 to 1, the song's quietest to its loudest, a frame each
	Low   []float32 // bass hits, 0 to 1, on a fixed scale
	High  []float32 // sharp highs (hats, a snare's crack, "s" sounds), 0 to 1, on a fixed scale
	Beats []float64 // seconds
	Down  int       // which beat of four starts each bar
}

// ErrTooShort is a song under five seconds, which has no beat to find.
var ErrTooShort = errors.New("beats: too short to hear")

// Hear finds the beats. tempo is what the song's analysis (AudioMuse)
// measured, folded into 70-150 bpm, or 0 when it is not known.
func (a *Analyzer) Hear(tempo float64) (*Result, error) {
	fps := float64(a.sr) / float64(a.hop)
	frames := len(a.loud)
	if float64(frames) < fps*5 {
		return nil, ErrTooShort
	}
	loud, low, high := a.loud, a.low, a.high

	// Onsets: how sharply each band got louder.
	lowOn := make([]float32, frames)
	highOn := make([]float32, frames)
	onset := make([]float32, frames)
	for f := 1; f < frames; f++ {
		lowOn[f] = float32(math.Max(0, float64(low[f])-float64(low[f-1])))
		highOn[f] = float32(math.Max(0, float64(high[f])-float64(high[f-1])))
		onset[f] = float32(float64(lowOn[f]) + 0.6*float64(highOn[f]) + 0.4*math.Max(0, float64(loud[f])-float64(loud[f-1])))
	}

	// How loud a part of the song feels is its energy over about half a
	// second, not one 23ms slice.
	win := int(math.Max(1, jsRound(fps*0.25)))
	energy := make([]float64, frames)
	for f, db := range loud {
		energy[f] = math.Pow(10, float64(db)/10)
	}
	felt := make([]float32, frames)
	run := 0.0
	for f := 0; f < frames+win; f++ {
		if f < frames {
			run += energy[f]
		}
		if f-2*win-1 >= 0 {
			run -= energy[f-2*win-1]
		}
		c := f - win
		if c >= 0 && c < frames {
			n := min(frames-1, c+win) - max(0, c-win) + 1
			felt[c] = float32(10 * math.Log10(run/float64(n)+1e-10))
		}
	}
	loudN := scale(felt, 0.05, 0.97)
	// Scaled within the song, for choosing each bar's first beat below.
	lowOnN := scale(lowOn, 0.5, 0.995)
	// What the looks see is on a fixed scale instead: scaled within the song,
	// every song had as many hits as any other, a ballad as many as a drum
	// track (measured: Change My Mind 94 kicks and 50 highs a minute, Thunder
	// 82 and 60). A jump of 4dB in the bass starts to count and 10dB is a
	// full hit; in the highs above 7kHz, 9dB and 21dB (then 42 and 17 a
	// minute against 29 and 35; the click track still 120 of 120).
	kicks := fixed(lowOn, 4, 6)
	// A sharp high's rise is measured from the quietest of the three frames
	// before it, not the one frame before: a hit whose rise fell across a
	// frame boundary read as two half jumps, so hits that sounded alike came
	// out strong or weak by where they landed (reported on Thunder, whose
	// every other beat is a loud hit: measured 11-18dB from the frame
	// before, 13.5-20dB this way). 6dB starts to count and 16dB is a full
	// hit, so lightning (80%) is 14dB: 72% of Thunder's loud hits, against
	// 25% before.
	hatOn := make([]float32, frames)
	for f := 1; f < frames; f++ {
		lo := float64(a.hats[f-1])
		for k := max(0, f-3); k < f-1; k++ {
			lo = math.Min(lo, float64(a.hats[k]))
		}
		hatOn[f] = float32(math.Max(0, float64(a.hats[f])-lo))
	}
	highs := fixed(hatOn, 6, 10)
	// And a hit must be heard: a rise is as big from near silence as in a
	// chorus, so faint ticks struck lightning (reported: "stuff I can hardly
	// hear"). A hit counts in full within 10dB of the song's loud highs (its
	// 95th percentile above 7kHz), fading out over the 4dB below. Measured:
	// Thunder's loud hits every other beat all kept, strikes 74 to 68 a
	// minute; Change My Mind 48 to 34.
	loudHats := append([]float32(nil), a.hats...)
	sort.Slice(loudHats, func(i, j int) bool { return loudHats[i] < loudHats[j] })
	top := float64(loudHats[int(math.Floor(float64(len(loudHats))*0.95))])
	for f := range highs {
		heardAt := math.Max(0, math.Min(1, (float64(a.hats[f])-(top-14))/4))
		highs[f] = float32(float64(highs[f]) * heardAt)
	}

	// The tempo: the lag at which the onsets repeat best, between 70 and 180
	// beats a minute, leaning towards the analysis's own tempo when there is
	// one. Lags in tenths of a frame.
	mean := 0.0
	for _, v := range onset {
		mean += float64(v)
	}
	mean /= float64(frames)
	sd := 0.0
	for _, v := range onset {
		d := float64(v) - mean
		sd += d * d
	}
	sd = math.Sqrt(sd / float64(frames))
	if sd == 0 {
		sd = 1
	}
	on := make([]float32, frames)
	for f, v := range onset {
		on[f] = float32((float64(v) - mean) / sd)
	}
	prior := 120.0
	if tempo > 0 {
		prior = tempo
	}
	width := 0.9
	if tempo > 0 {
		width = 0.25
	}
	bestLag := (60 / prior) * fps
	best := math.Inf(-1)
	for lag := (60.0 / 180) * fps; lag <= (60.0/70)*fps; lag += 0.1 {
		sum := 0.0
		whole := int(math.Floor(lag))
		part := lag - float64(whole)
		for f := whole + 1; f < frames; f++ {
			sum += float64(on[f]) * (float64(on[f-whole])*(1-part) + float64(on[f-whole-1])*part)
		}
		sum /= float64(frames - whole - 1)
		octaves := math.Log2((60 * fps) / lag / prior)
		weighted := sum * math.Exp(-0.5*math.Pow(octaves/width, 2))
		if weighted > best {
			best, bestLag = weighted, lag
		}
	}
	period := bestLag

	// The beats: dynamic programming (Ellis, 2007) - every frame's best chain
	// of beats ending there, rewarding onsets and punishing gaps that stray
	// from the period, so it follows a tempo that wanders.
	score := make([]float32, frames)
	back := make([]int32, frames)
	const tight = 100
	for i := 0; i < frames; i++ {
		bestPrev := math.Inf(-1)
		bj := -1
		from := i - int(jsRound(period*2))
		to := i - int(jsRound(period/2))
		for j := max(0, from); j <= to; j++ {
			gap := math.Log(float64(i-j) / period)
			v := float64(score[j]) - tight*gap*gap
			if v > bestPrev {
				bestPrev, bj = v, j
			}
		}
		s := float64(on[i])
		if bj >= 0 {
			s += bestPrev
		}
		score[i] = float32(s)
		back[i] = int32(bj)
	}
	end := frames - 1
	// app.js starts this look at frames - period, which is almost never a
	// whole frame, so it reads nothing and the chain ends at the last frame.
	// The same here, so both hear the same beats.
	if start := math.Max(0, float64(frames)-period); start == math.Floor(start) {
		for i := int(start); i < frames; i++ {
			if score[i] > score[end] {
				end = i
			}
		}
	}
	var beatFrames []int
	for i := end; i >= 0; i = int(back[i]) {
		beatFrames = append(beatFrames, i)
	}
	for l, r := 0, len(beatFrames)-1; l < r; l, r = l+1, r-1 {
		beatFrames[l], beatFrames[r] = beatFrames[r], beatFrames[l]
	}
	beats := make([]float64, len(beatFrames))
	for k, f := range beatFrames {
		beats[k] = float64(f) / fps
	}
	// Which beat starts each bar: the one of four where the kick lands
	// hardest.
	var hit [4]float64
	for k, f := range beatFrames {
		hit[k%4] += float64(lowOnN[f]) + float64(lowOnN[min(frames-1, f+1)])
	}
	down := 0
	for k := 1; k < 4; k++ {
		if hit[k] > hit[down] {
			down = k
		}
	}
	return &Result{FPS: fps, Loud: loudN, Low: kicks, High: highs, Beats: beats, Down: down}, nil
}

// scale maps arr to 0-1 between its lo and hi quantiles.
func scale(arr []float32, lo, hi float64) []float32 {
	sorted := append([]float32(nil), arr...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	a := float64(sorted[int(math.Floor(float64(len(sorted))*lo))])
	b := float64(sorted[int(math.Floor(float64(len(sorted))*hi))])
	span := b - a
	if span == 0 {
		span = 1
	}
	out := make([]float32, len(arr))
	for i, v := range arr {
		out[i] = float32(math.Max(0, math.Min(1, (float64(v)-a)/span)))
	}
	return out
}

// fixed maps jumps in dB to 0-1: from lo, full at lo+span.
func fixed(arr []float32, lo, span float64) []float32 {
	out := make([]float32, len(arr))
	for i, v := range arr {
		out[i] = float32(math.Max(0, math.Min(1, (float64(v)-lo)/span)))
	}
	return out
}

// jsRound is JavaScript's Math.round: halves go up.
func jsRound(x float64) float64 { return math.Floor(x + 0.5) }

// Sound is the song's tempo and energy from its analysis, kept beside what
// was heard so a phone offline has it too.
type Sound struct {
	Tempo  float64 `json:"tempo"`
	Energy float64 `json:"energy"`
}

// Kept is a result as the app keeps one (saveHeard in app.js): loudness and
// the two bands as bytes 0-255, the beats as float32, in base64.
type Kept struct {
	V     int     `json:"v"`
	FPS   float64 `json:"fps"`
	Down  int     `json:"down"`
	Loud  string  `json:"loud"`
	Low   string  `json:"low"`
	High  string  `json:"high"`
	Beats string  `json:"beats"`
	Sound *Sound  `json:"sound"`
	// Prior is the tempo the search leaned on, 0 for none: heard before the
	// song's analysis had a tempo, it is heard again once it has.
	Prior float64 `json:"prior"`
}

// Keep is r in the app's shape.
func (r *Result) Keep(sound *Sound, prior float64) Kept {
	beats := make([]byte, 4*len(r.Beats))
	for i, b := range r.Beats {
		binary.LittleEndian.PutUint32(beats[4*i:], math.Float32bits(float32(b)))
	}
	return Kept{
		V: Version, FPS: r.FPS, Down: r.Down,
		Loud: toB64(r.Loud), Low: toB64(r.Low), High: toB64(r.High),
		Beats: base64.StdEncoding.EncodeToString(beats),
		Sound: sound, Prior: prior,
	}
}

func toB64(v []float32) string {
	b := make([]byte, len(v))
	for i, x := range v {
		b[i] = byte(jsRound(math.Max(0, math.Min(1, float64(x))) * 255))
	}
	return base64.StdEncoding.EncodeToString(b)
}
