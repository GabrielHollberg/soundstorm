// Package federate fans a query out across every source and merges the answers.
//
// The governing design rule: a slow or dead source must never take the search
// down with it. Each source gets its own deadline and its own error slot. If
// the music server is unreachable, you still get your films, the response says
// so, and the UI can show a banner instead of an error page.
package federate

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/source"
)

// DefaultPerSourceTimeout bounds how long any single backend may hold up a
// search.
const DefaultPerSourceTimeout = 5 * time.Second

// MaxResults caps one page of the merged list.
const MaxResults = 100

// MaxDepth is how far into a shelf paging will go.
//
// Every page re-fetches from the start (see Search), so the work grows with
// the offset rather than staying flat - which is fine for a house's worth of
// media and needs a stop somewhere. At the cap Search reports HasMore false
// rather than serving empty pages forever.
const MaxDepth = 2000

// SourceStatus is the per-source outcome of one federated search.
type SourceStatus struct {
	SourceID string     `json:"sourceId"`
	Kind     media.Kind `json:"kind"`
	OK       bool       `json:"ok"`
	Error    string     `json:"error,omitempty"`
	Count    int        `json:"count"`
	TookMS   int64      `json:"tookMs"`
}

// Result is a merged, ranked answer plus a per-source report.
type Result struct {
	Items   []media.Item   `json:"items"`
	Sources []SourceStatus `json:"sources"`

	// Degraded is true when at least one source failed. The items are still
	// valid; they are just incomplete.
	Degraded bool  `json:"degraded"`
	TookMS   int64 `json:"tookMs"`

	// Offset is where this page starts, echoed back so a client appending
	// pages can tell a reply to its own request from a stale one.
	Offset int `json:"offset"`

	// HasMore says another page is worth asking for.
	HasMore bool `json:"hasMore"`
}

// Search queries every source the registry says matches q, concurrently.
//
// It returns once every source has answered, failed, or hit perSourceTimeout.
// It does not return an error: a total failure is expressed as a Result where
// every SourceStatus is not OK.
func Search(ctx context.Context, reg *source.Registry, q media.Query, perSourceTimeout time.Duration) Result {
	if perSourceTimeout <= 0 {
		perSourceTimeout = DefaultPerSourceTimeout
	}
	started := time.Now()
	sources := reg.Matching(ctx, q)

	// Every source is asked for the whole run up to the end of the window,
	// not just the window itself - so page two asks for 200 and throws the
	// first 100 away.
	//
	// That looks wasteful and is the only correct option here. A merged,
	// globally sorted page cannot be built from per-source pages: each
	// source's second page starts over at the top of its own order, so those
	// items would sort in behind ones already on screen. Slicing after the
	// merge is the only way the ordering survives paging.
	window := q.Limit
	if window <= 0 {
		window = MaxResults
	}
	depth := q.Offset + window
	if depth > MaxDepth {
		depth = MaxDepth
	}
	fetch := q
	fetch.Limit = depth

	type outcome struct {
		status SourceStatus
		items  []media.Item
	}
	outcomes := make([]outcome, len(sources))

	var wg sync.WaitGroup
	for i, src := range sources {
		wg.Add(1)
		go func(i int, src source.Source) {
			defer wg.Done()

			// Each source gets its own budget, derived from the caller's
			// context so an aborted request still cancels everything.
			sctx, cancel := context.WithTimeout(ctx, perSourceTimeout)
			defer cancel()

			begin := time.Now()
			items, err := src.Search(sctx, fetch)
			status := SourceStatus{
				SourceID: src.ID(),
				Kind:     src.Kind(),
				TookMS:   time.Since(begin).Milliseconds(),
			}
			if err != nil {
				status.Error = err.Error()
				outcomes[i] = outcome{status: status}
				return
			}
			status.OK = true
			status.Count = len(items)
			outcomes[i] = outcome{status: status, items: items}
		}(i, src)
	}
	wg.Wait()

	// Items starts as an empty slice, not nil, so the JSON is always an array.
	// A browser doing results.items.map() should not have to special-case
	// "nothing matched".
	res := Result{
		Items:   []media.Item{},
		Sources: make([]SourceStatus, 0, len(outcomes)),
	}
	for _, o := range outcomes {
		res.Sources = append(res.Sources, o.status)
		if !o.status.OK {
			res.Degraded = true
			continue
		}
		for _, item := range o.items {
			item.Score = Relevance(q.Text, item)
			res.Items = append(res.Items, item)
		}
	}

	sortItems(res.Items)

	// A source that returned exactly what it was asked for was probably cut
	// off, so there is more behind it even when this page is not full. Without
	// this, one source holding a thousand books answers a first page of a
	// hundred and looks exhausted.
	truncated := false
	for _, o := range outcomes {
		if o.status.OK && len(o.items) >= depth {
			truncated = true
		}
	}

	total := len(res.Items)
	start := min(q.Offset, total)
	end := min(start+window, total)
	res.Offset = q.Offset
	res.HasMore = total > end || truncated
	// At the cap, stop rather than hand out pages that will never arrive.
	if q.Offset+window >= MaxDepth {
		res.HasMore = false
	}
	res.Items = res.Items[start:end]

	res.TookMS = time.Since(started).Milliseconds()
	return res
}

// sortItems orders by score descending, then title, then source, so that
// equal-scoring results come back in a stable order run to run.
func sortItems(items []media.Item) {
	sort.SliceStable(items, func(a, b int) bool {
		if items[a].Score != items[b].Score {
			return items[a].Score > items[b].Score
		}
		if ka, kb := items[a].OrderKey(), items[b].OrderKey(); ka != kb {
			return ka < kb
		}
		if items[a].SourceID != items[b].SourceID {
			return items[a].SourceID < items[b].SourceID
		}
		// The same tiebreak a source orders itself by (media.Less), so equal
		// keys cannot trade places between one page and the next.
		return items[a].ID < items[b].ID
	})
}

// Relevance scores an item against the raw query text, 0..1.
//
// This is deliberately simple and readable rather than clever. Every backend
// has already done its own matching; our job is only to decide whose hits
// deserve to be near the top of a merged list. Upgrade to BM25 over a local
// index if and when the naive version visibly misranks something.
func Relevance(queryText string, item media.Item) float64 {
	q := normalize(queryText)
	if q == "" {
		return 0
	}
	title := normalize(item.Title)

	switch {
	case title == q:
		return 1.0
	case strings.HasPrefix(title, q):
		return 0.85
	case strings.Contains(title, q):
		return 0.7
	}

	for _, c := range item.Creators {
		if normalize(c) == q {
			return 0.65
		}
		if strings.Contains(normalize(c), q) {
			return 0.5
		}
	}

	// Subtitle carries the album for music, the series for a book or a show.
	// A backend that matched on one of those returns results we would
	// otherwise score at the 0.05 floor and bury: searching a concert's venue
	// finds every track on it, each of which looks irrelevant judged on its
	// title alone. Found by pointing this at real live recordings, where
	// almost nothing matches on the track title.
	if item.Subtitle != "" && strings.Contains(normalize(item.Subtitle), q) {
		return 0.45
	}

	// Fall back to how much of the query's vocabulary the title covers.
	if overlap := tokenOverlap(q, title); overlap > 0 {
		return 0.1 + 0.35*overlap
	}
	return 0.05
}

// tokenOverlap is the fraction of query tokens present in the text.
func tokenOverlap(query, text string) float64 {
	qt := strings.Fields(query)
	if len(qt) == 0 {
		return 0
	}
	have := make(map[string]struct{})
	for _, t := range strings.Fields(text) {
		have[t] = struct{}{}
	}
	var hits int
	for _, t := range qt {
		if _, ok := have[t]; ok {
			hits++
		}
	}
	return float64(hits) / float64(len(qt))
}

// normalize lowercases, collapses whitespace and drops punctuation so that
// "The Hobbit!" and "the hobbit" compare equal.
func normalize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			// Keep word boundaries where punctuation used to be.
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// HealthAll checks every source concurrently and reports per-source status.
func HealthAll(ctx context.Context, reg *source.Registry, timeout time.Duration) []SourceStatus {
	if timeout <= 0 {
		timeout = DefaultPerSourceTimeout
	}
	sources := reg.All(ctx)
	statuses := make([]SourceStatus, len(sources))

	var wg sync.WaitGroup
	for i, src := range sources {
		wg.Add(1)
		go func(i int, src source.Source) {
			defer wg.Done()
			sctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			begin := time.Now()
			err := src.Health(sctx)
			st := SourceStatus{
				SourceID: src.ID(),
				Kind:     src.Kind(),
				TookMS:   time.Since(begin).Milliseconds(),
				OK:       err == nil,
			}
			if err != nil {
				st.Error = err.Error()
			}
			statuses[i] = st
		}(i, src)
	}
	wg.Wait()
	return statuses
}
