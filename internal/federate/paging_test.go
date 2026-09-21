package federate

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/source"
)

// limited is a source that honours the Limit it is handed, the way a real
// backend does. The plain stub ignores it, which would hide the whole reason
// paging works the way it does.
type limited struct {
	id    string
	kind  media.Kind
	items []media.Item
	asked []int // every Limit it was called with
}

func (l *limited) ID() string       { return l.id }
func (l *limited) Kind() media.Kind { return l.kind }
func (l *limited) Health(context.Context) error {
	return nil
}

func (l *limited) Search(_ context.Context, q media.Query) ([]media.Item, error) {
	l.asked = append(l.asked, q.Limit)
	n := q.LimitOr(25)
	if n > len(l.items) {
		n = len(l.items)
	}
	return l.items[:n], nil
}

// shelf builds n items whose titles sort in the order given.
func shelf(id string, kind media.Kind, titles ...string) *limited {
	out := &limited{id: id, kind: kind}
	for _, t := range titles {
		out.items = append(out.items, media.Item{ID: t, SourceID: id, Kind: kind, Title: t})
	}
	return out
}

func titles(items []media.Item) []string {
	out := make([]string, 0, len(items))
	for _, i := range items {
		out = append(out, i.Title)
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The property that decides the whole design.
//
// Paging a merged list cannot be done by paging each source: source A's second
// page starts over at the top of A's own order, so those items sort in behind
// things already shown. Walking every page in turn has to reproduce the single
// sorted list exactly, with nothing repeated and nothing skipped.
func TestPagesWalkTheMergedOrderExactly(t *testing.T) {
	a := shelf("a", media.KindMusic, "Anna", "Clara", "Edith", "Greta", "Iris")
	b := shelf("b", media.KindEbook, "Bella", "Dora", "Freya", "Hilda", "Juno")
	reg := source.NewRegistry(a, b)

	want := []string{
		"Anna", "Bella", "Clara", "Dora", "Edith",
		"Freya", "Greta", "Hilda", "Iris", "Juno",
	}

	var got []string
	for offset := 0; ; {
		res := Search(context.Background(), reg,
			media.Query{Limit: 3, Offset: offset}, time.Second)
		got = append(got, titles(res.Items)...)
		if res.Offset != offset {
			t.Fatalf("page echoed offset %d, asked for %d", res.Offset, offset)
		}
		if !res.HasMore {
			break
		}
		offset += 3
		if offset > 100 {
			t.Fatal("paging never reported the end")
		}
	}

	if !equal(got, want) {
		t.Errorf("walking the pages gave\n  %v\nwant\n  %v", got, want)
	}
}

// Each source is asked for the whole run up to the end of the window, because
// the merge has to happen before the slice.
func TestEachPageRefetchesFromTheStart(t *testing.T) {
	a := shelf("a", media.KindMusic, "Anna", "Clara", "Edith")
	reg := source.NewRegistry(a)

	Search(context.Background(), reg, media.Query{Limit: 2, Offset: 0}, time.Second)
	Search(context.Background(), reg, media.Query{Limit: 2, Offset: 2}, time.Second)

	if len(a.asked) != 2 || a.asked[0] != 2 || a.asked[1] != 4 {
		t.Errorf("source was asked for limits %v, want [2 4] - offset+window each time", a.asked)
	}
}

// A source cut off at exactly the limit means there is more behind it, even
// when the merged page came back short of full. Without this, one source
// holding a long shelf answers one page and looks exhausted.
func TestATruncatedSourceMeansThereIsMore(t *testing.T) {
	long := shelf("a", media.KindEbook)
	for i := 0; i < 50; i++ {
		long.items = append(long.items, media.Item{
			ID: fmt.Sprint(i), SourceID: "a", Kind: media.KindEbook,
			Title: fmt.Sprintf("Book %02d", i),
		})
	}
	reg := source.NewRegistry(long)

	res := Search(context.Background(), reg, media.Query{Limit: 10}, time.Second)
	if len(res.Items) != 10 {
		t.Fatalf("page had %d items, want 10", len(res.Items))
	}
	if !res.HasMore {
		t.Error("a full page from a source with 40 more behind it reported no more")
	}
}

// And the end of a shelf really does end, or the UI scrolls forever.
func TestTheLastPageSaysSo(t *testing.T) {
	a := shelf("a", media.KindMusic, "One", "Two", "Three")
	reg := source.NewRegistry(a)

	res := Search(context.Background(), reg, media.Query{Limit: 10}, time.Second)
	if len(res.Items) != 3 {
		t.Fatalf("got %d items, want 3", len(res.Items))
	}
	if res.HasMore {
		t.Error("a short page reported more to come")
	}
}

// Past the end is empty and final, not an error and not a loop.
func TestAnOffsetPastTheEndIsEmpty(t *testing.T) {
	a := shelf("a", media.KindMusic, "One", "Two")
	reg := source.NewRegistry(a)

	res := Search(context.Background(), reg, media.Query{Limit: 10, Offset: 500}, time.Second)
	if len(res.Items) != 0 {
		t.Errorf("got %v, want nothing", titles(res.Items))
	}
	if res.HasMore {
		t.Error("past the end still reported more")
	}
}

// Paging stops at MaxDepth rather than serving pages that never arrive.
func TestPagingStopsAtTheDepthCap(t *testing.T) {
	long := shelf("a", media.KindEbook)
	for i := 0; i < MaxDepth+500; i++ {
		long.items = append(long.items, media.Item{
			ID: fmt.Sprint(i), SourceID: "a", Kind: media.KindEbook,
			Title: fmt.Sprintf("Book %05d", i),
		})
	}
	reg := source.NewRegistry(long)

	res := Search(context.Background(), reg,
		media.Query{Limit: 100, Offset: MaxDepth - 100}, time.Second)
	if res.HasMore {
		t.Error("the last page inside the cap still asked for another")
	}
}
