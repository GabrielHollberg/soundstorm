package httpapi

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/source"
)

// The Continue row: what this person is part way through, newest first, so
// the home screen can say "carry on where you left off" instead of opening on
// the whole library.
//
// Two places know. A book's reading position is SoundStorm's own, in the state
// file; an audiobook's listening position is Audiobookshelf's, per person (see
// "Accounts, and the one thing that is per person" in CLAUDE.md). A film's or
// an episode's is SoundStorm's too, kept like a book's, because the house
// shares one Jellyfin account and a position there would be everybody's.

const (
	continueLimit = 12
	// A position this close to either end is "not started" or "finished",
	// not something to carry on with.
	continueMin      = 0.005
	continueMax      = 0.985
	continueMaxVideo = 0.93
)

type continueItem struct {
	media.Item
	Progress float64 `json:"progress"`
	at       time.Time
}

func (s *Server) handleContinue(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.perSourceTimeout)
	defer cancel()

	var (
		mu  sync.Mutex
		out []continueItem
		wg  sync.WaitGroup
	)
	add := func(item media.Item, fraction float64, at time.Time) {
		limit := continueMax
		if item.Kind == media.KindVideo || item.Kind == media.KindTV {
			// A film is over at the credits, which can be the last few
			// minutes: stopping there is finishing, not stopping part way.
			limit = continueMaxVideo
		}
		if fraction < continueMin || fraction > limit {
			return
		}
		mu.Lock()
		out = append(out, continueItem{Item: item, Progress: fraction, at: at})
		mu.Unlock()
	}

	// Sources that remember for themselves - Audiobookshelf. Each on its own,
	// so a slow backend does not hold the row up, and a failure is just an
	// absence: this is a convenience, never a reason for the page to fail.
	for _, src := range s.reg.All(ctx) {
		lister, ok := src.(source.InProgressLister)
		if !ok {
			continue
		}
		wg.Add(1)
		go func(src source.Source, lister source.InProgressLister) {
			defer wg.Done()
			defer func() {
				if p := recover(); p != nil {
					s.log.Error("continue: a source panicked", "source", src.ID(), "panic", p)
				}
			}()
			started, err := lister.InProgress(ctx, continueLimit)
			if err != nil {
				s.log.Debug("continue: source did not answer", "source", src.ID(), "err", err)
				return
			}
			for _, st := range started {
				add(st.Item, st.Fraction, st.At)
			}
		}(src, lister)
	}

	// Books, from SoundStorm's own reading positions. Looked up through the
	// registry, which applies this account's access - a shelf somebody may no
	// longer see drops out of the row with everything else.
	prefix := user.ID + "/"
	for key, p := range s.store.ProgressWithPrefix(prefix) {
		sourceID, itemID, ok := strings.Cut(strings.TrimPrefix(key, prefix), "/")
		if !ok {
			continue
		}
		src, ok := s.reg.ByID(ctx, sourceID)
		if !ok {
			continue
		}
		getter, ok := src.(source.ItemGetter)
		if !ok {
			continue
		}
		item, ok := getter.ItemByID(ctx, itemID)
		if !ok {
			continue // a book since deleted
		}
		add(item, p.Fraction, p.UpdatedAt)
	}
	wg.Wait()

	sort.SliceStable(out, func(i, j int) bool { return out[i].at.After(out[j].at) })
	if len(out) > continueLimit {
		out = out[:continueLimit]
	}
	if out == nil {
		out = []continueItem{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}
