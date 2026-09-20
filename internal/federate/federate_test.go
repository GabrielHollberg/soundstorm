package federate

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gabehollberg/atrium/internal/media"
	"github.com/gabehollberg/atrium/internal/source"
)

// stub is a Source that returns canned results, an error, or hangs.
type stub struct {
	id    string
	kind  media.Kind
	items []media.Item
	err   error
	delay time.Duration
}

func (s stub) ID() string       { return s.id }
func (s stub) Kind() media.Kind { return s.kind }

func (s stub) Search(ctx context.Context, _ media.Query) ([]media.Item, error) {
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if s.err != nil {
		return nil, s.err
	}
	return s.items, nil
}

func (s stub) Health(ctx context.Context) error { return s.err }

func item(sourceID, title string, kind media.Kind) media.Item {
	return media.Item{ID: title, SourceID: sourceID, Kind: kind, Title: title}
}

// The core promise: one dead source must not take the search down.
func TestSearchToleratesAFailedSource(t *testing.T) {
	reg := source.NewRegistry(
		stub{id: "books", kind: media.KindEbook, items: []media.Item{item("books", "The Hobbit", media.KindEbook)}},
		stub{id: "music", kind: media.KindMusic, err: errors.New("connection refused")},
	)

	res := Search(context.Background(), reg, media.Query{Text: "hobbit"}, time.Second)

	if len(res.Items) != 1 {
		t.Fatalf("want 1 item from the healthy source, got %d", len(res.Items))
	}
	if !res.Degraded {
		t.Error("want Degraded=true when a source fails")
	}
	if len(res.Sources) != 2 {
		t.Fatalf("want a status for each source, got %d", len(res.Sources))
	}

	byID := map[string]SourceStatus{}
	for _, s := range res.Sources {
		byID[s.SourceID] = s
	}
	if !byID["books"].OK {
		t.Error("books should be OK")
	}
	if byID["music"].OK || byID["music"].Error == "" {
		t.Error("music should report its failure")
	}
}

// A hung source must be cut off at the per-source deadline, not waited on.
func TestSearchEnforcesPerSourceTimeout(t *testing.T) {
	reg := source.NewRegistry(
		stub{id: "fast", kind: media.KindEbook, items: []media.Item{item("fast", "Dune", media.KindEbook)}},
		stub{id: "slow", kind: media.KindMusic, delay: 30 * time.Second},
	)

	start := time.Now()
	res := Search(context.Background(), reg, media.Query{Text: "dune"}, 100*time.Millisecond)
	elapsed := time.Since(start)

	if elapsed > 5*time.Second {
		t.Fatalf("search waited %v; the per-source timeout was not enforced", elapsed)
	}
	if len(res.Items) != 1 {
		t.Errorf("want the fast source's result, got %d items", len(res.Items))
	}
	if !res.Degraded {
		t.Error("want Degraded=true when a source times out")
	}
}

// Kind filtering should keep irrelevant backends out of the fan-out entirely.
func TestSearchFiltersByKind(t *testing.T) {
	reg := source.NewRegistry(
		stub{id: "books", kind: media.KindEbook, items: []media.Item{item("books", "Dune", media.KindEbook)}},
		stub{id: "music", kind: media.KindMusic, items: []media.Item{item("music", "Dune", media.KindMusic)}},
	)

	res := Search(context.Background(), reg, media.Query{Text: "dune", Kinds: []media.Kind{media.KindEbook}}, time.Second)

	if len(res.Sources) != 1 || res.Sources[0].SourceID != "books" {
		t.Fatalf("want only the ebook source queried, got %+v", res.Sources)
	}
	if res.Degraded {
		t.Error("filtering out a source is not degradation")
	}
}

func TestRelevanceOrdering(t *testing.T) {
	q := "the hobbit"
	exact := Relevance(q, media.Item{Title: "The Hobbit"})
	prefix := Relevance(q, media.Item{Title: "The Hobbit: An Unexpected Journey"})
	contains := Relevance(q, media.Item{Title: "Reading The Hobbit Aloud"})
	byAuthor := Relevance(q, media.Item{Title: "Unrelated", Creators: []string{"The Hobbit"}})
	weak := Relevance(q, media.Item{Title: "The Silmarillion"})

	if !(exact > prefix && prefix > contains && contains > byAuthor && byAuthor > weak) {
		t.Errorf("relevance is misordered: exact=%v prefix=%v contains=%v author=%v weak=%v",
			exact, prefix, contains, byAuthor, weak)
	}
	if exact != 1.0 {
		t.Errorf("an exact title match should score 1.0, got %v", exact)
	}
}

func TestNormalizeIgnoresPunctuationAndCase(t *testing.T) {
	if got, want := normalize("The Hobbit!"), "the hobbit"; got != want {
		t.Errorf("normalize(%q) = %q, want %q", "The Hobbit!", got, want)
	}
	if got, want := normalize("  Dune   Messiah  "), "dune messiah"; got != want {
		t.Errorf("normalize collapsed whitespace wrong: %q", got)
	}
}

func TestSearchWithNoSources(t *testing.T) {
	res := Search(context.Background(), source.NewRegistry(), media.Query{Text: "anything"}, time.Second)
	if len(res.Items) != 0 || res.Degraded {
		t.Errorf("an empty registry should yield an empty, non-degraded result, got %+v", res)
	}
}
