package media

import (
	"sync"
	"testing"
	"time"
)

// Concurrent requests for a shelf still cold must share one fetch, not each
// start their own. Without this, opening the app from two devices at once -
// or a browser's own retry - would ask the backend for the whole shelf twice
// for one page load.
func TestGetOrFetchCoalescesConcurrentMisses(t *testing.T) {
	c := &ShelfCache{}
	var calls int32
	var mu sync.Mutex
	start := make(chan struct{})

	var wg sync.WaitGroup
	results := make([][]Item, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			items, err := c.GetOrFetch("q", func() ([]Item, error) {
				mu.Lock()
				calls++
				mu.Unlock()
				time.Sleep(20 * time.Millisecond)
				return []Item{{ID: "1", Title: "one"}}, nil
			})
			if err != nil {
				t.Errorf("GetOrFetch: %v", err)
			}
			results[i] = items
		}(i)
	}
	close(start)
	wg.Wait()

	if calls != 1 {
		t.Errorf("fetch ran %d times, want 1", calls)
	}
	for i, r := range results {
		if len(r) != 1 || r[0].ID != "1" {
			t.Errorf("caller %d got %v", i, r)
		}
	}
}

// A fetch that fails is not cached, and every waiter sees the same error - a
// backend that is briefly down must not poison the cache for the TTL.
func TestGetOrFetchDoesNotCacheAFailure(t *testing.T) {
	c := &ShelfCache{}
	boom := errFake("upstream exploded")

	if _, err := c.GetOrFetch("q", func() ([]Item, error) { return nil, boom }); err != boom {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	// The next call must try again rather than replaying the failure.
	called := false
	items, err := c.GetOrFetch("q", func() ([]Item, error) {
		called = true
		return []Item{{ID: "1"}}, nil
	})
	if !called {
		t.Error("a failed fetch was cached")
	}
	if err != nil || len(items) != 1 {
		t.Errorf("items=%v err=%v", items, err)
	}
}

type errFake string

func (e errFake) Error() string { return string(e) }

// A fresh key beyond maxShelves evicts the least recently used entry, not an
// entry a caller is still actively re-reading.
func TestShelfCacheEvictsLeastRecentlyUsed(t *testing.T) {
	c := &ShelfCache{}
	fetch := func(id string) func() ([]Item, error) {
		return func() ([]Item, error) { return []Item{{ID: id}}, nil }
	}
	for i := 0; i < maxShelves; i++ {
		key := string(rune('a' + i))
		if _, err := c.GetOrFetch(key, fetch(key)); err != nil {
			t.Fatal(err)
		}
	}
	// Touch "a" so it is the most recently used, not the next to go.
	if _, err := c.GetOrFetch("a", fetch("a")); err != nil {
		t.Fatal(err)
	}
	// One more key must evict "b" (now the least recently used), not "a".
	if _, err := c.GetOrFetch("z", fetch("z")); err != nil {
		t.Fatal(err)
	}

	aFetched := false
	if _, err := c.GetOrFetch("a", func() ([]Item, error) { aFetched = true; return nil, nil }); err != nil {
		t.Fatal(err)
	}
	if aFetched {
		t.Error("the recently-touched entry was evicted")
	}

	bFetched := false
	if _, err := c.GetOrFetch("b", func() ([]Item, error) { bFetched = true; return []Item{{ID: "b"}}, nil }); err != nil {
		t.Fatal(err)
	}
	if !bFetched {
		t.Error("the least recently used entry was not evicted")
	}
}

// Clear forgets everything, cached or in flight, for a rescan.
func TestShelfCacheClear(t *testing.T) {
	c := &ShelfCache{}
	c.GetOrFetch("q", func() ([]Item, error) { return []Item{{ID: "1"}}, nil })
	c.Clear()
	fetched := false
	c.GetOrFetch("q", func() ([]Item, error) { fetched = true; return []Item{{ID: "1"}}, nil })
	if !fetched {
		t.Error("Clear did not forget the cached listing")
	}
}
