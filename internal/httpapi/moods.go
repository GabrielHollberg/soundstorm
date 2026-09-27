package httpapi

import (
	"context"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/source/audiomuse"
)

// Moods, from what the sound analysis heard (internal/source/audiomuse).
//
// Its six classifiers answer in a narrow band - a solo piano piece scored
// 0.58 to 0.63 on all of them, checked on 3.6.3 - so their raw numbers mean
// little. What they do carry is order: this song is more relaxed than that
// one. So every feature is turned into its rank in this library (0 the least
// relaxed song here, 1 the most), and a mood is an average of ranks. "Chill"
// is then the most relaxed, least energetic, least aggressive songs *you*
// have, whatever the absolute scores came out as.

type moodDef struct {
	ID       string
	Title    string
	Subtitle string
	score    func(r ranks) float64
}

// ranks is one song's features as ranks in the library, 0 to 1.
type ranks map[string]float64

func avg(v ...float64) float64 {
	t := 0.0
	for _, x := range v {
		t += x
	}
	return t / float64(len(v))
}

var moodDefs = []moodDef{
	{"chill", "Chill", "Relaxed and easy", func(r ranks) float64 {
		return avg(r["relaxed"], 1-r["energy"], 1-r["aggressive"])
	}},
	{"happy", "Feel good", "Bright and upbeat", func(r ranks) float64 {
		return avg(r["happy"], 1-r["sad"], r["energy"])
	}},
	{"melancholy", "Melancholy", "Sad and reflective", func(r ranks) float64 {
		return avg(r["sad"], 1-r["happy"], 1-r["energy"])
	}},
	{"intense", "Intense", "Loud, fast and fierce", func(r ranks) float64 {
		return avg(r["aggressive"], r["energy"], r["tempo"])
	}},
	{"party", "Party", "Made to dance to", func(r ranks) float64 {
		return avg(r["party"], r["danceable"], r["tempo"])
	}},
	{"focus", "Focus", "Calm, few or no words", func(r ranks) float64 {
		return avg(r["instrumental"], r["relaxed"], 1-r["danceable"], 1-r["aggressive"])
	}},
}

func moodByID(id string) (moodDef, bool) {
	for _, m := range moodDefs {
		if m.ID == id {
			return m, true
		}
	}
	return moodDef{}, false
}

// songMoods is every analyzed song's score for every mood, 0 to 1.
type songMoods map[string]map[string]float64

// rankFeatures turns features into ranks within the library.
func rankFeatures(features map[string]audiomuse.Features) map[string]ranks {
	value := func(f audiomuse.Features, name string) float64 {
		switch name {
		case "energy":
			return f.Energy
		case "tempo":
			return f.Tempo
		case "instrumental":
			return f.Tags["instrumental"]
		}
		return f.Moods[name]
	}
	names := []string{"danceable", "aggressive", "happy", "party", "relaxed", "sad", "energy", "tempo", "instrumental"}
	out := make(map[string]ranks, len(features))
	for id := range features {
		out[id] = ranks{}
	}
	ids := make([]string, 0, len(features))
	for id := range features {
		ids = append(ids, id)
	}
	for _, name := range names {
		sort.Slice(ids, func(i, j int) bool {
			a, b := value(features[ids[i]], name), value(features[ids[j]], name)
			return a < b || (a == b && ids[i] < ids[j])
		})
		n := len(ids)
		for i := 0; i < n; {
			// Ties share a rank, so a feature most songs lack (instrumental)
			// ranks them all equally low rather than in id order.
			j := i
			v := value(features[ids[i]], name)
			for j < n && value(features[ids[j]], name) == v {
				j++
			}
			r := 0.5
			if n > 1 {
				r = float64(i+j-1) / 2 / float64(n-1)
			}
			for k := i; k < j; k++ {
				out[ids[k]][name] = r
			}
			i = j
		}
	}
	return out
}

func scoreMoods(features map[string]audiomuse.Features) songMoods {
	out := songMoods{}
	for id, r := range rankFeatures(features) {
		m := make(map[string]float64, len(moodDefs))
		for _, d := range moodDefs {
			m[d.ID] = d.score(r)
		}
		m["energy"] = r["energy"]
		out[id] = m
	}
	return out
}

// moodCache keeps the scores for the analysis they were made from, which
// the client re-reads every half hour.
type moodCache struct {
	mu    sync.Mutex
	from  uintptr
	moods songMoods
}

// sonic finds the sound analysis through the registry, so an account that
// may not hear music never reaches it.
func (s *Server) sonic(ctx context.Context) (*audiomuse.Source, bool) {
	for _, src := range s.reg.All(ctx) {
		if am, ok := src.(*audiomuse.Source); ok {
			return am, true
		}
	}
	return nil, false
}

// moods is every analyzed song's mood scores, or nil before any analysis.
func (s *Server) moods(ctx context.Context) songMoods {
	am, ok := s.sonic(ctx)
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	features, err := am.Features(ctx)
	if err != nil || len(features) == 0 {
		return nil
	}
	key := reflect.ValueOf(features).Pointer()
	s.moodCache.mu.Lock()
	defer s.moodCache.mu.Unlock()
	if s.moodCache.from != key {
		s.moodCache.moods, s.moodCache.from = scoreMoods(features), key
	}
	return s.moodCache.moods
}
