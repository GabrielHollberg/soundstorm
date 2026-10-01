package media

import (
	"context"
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
			items, err := c.GetOrFetch(context.Background(), "q", func(context.Context) ([]Item, error) {
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

	if _, err := c.GetOrFetch(context.Background(), "q", func(context.Context) ([]Item, error) { return nil, boom }); err != boom {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	// The next call must try again rather than replaying the failure.
	called := false
	items, err := c.GetOrFetch(context.Background(), "q", func(context.Context) ([]Item, error) {
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
	fetch := func(id string) func(context.Context) ([]Item, error) {
		return func(context.Context) ([]Item, error) { return []Item{{ID: id}}, nil }
	}
	for i := 0; i < maxShelves; i++ {
		key := string(rune('a' + i))
		if _, err := c.GetOrFetch(context.Background(), key, fetch(key)); err != nil {
			t.Fatal(err)
		}
	}
	// Touch "a" so it is the most recently used, not the next to go.
	if _, err := c.GetOrFetch(context.Background(), "a", fetch("a")); err != nil {
		t.Fatal(err)
	}
	// One more key must evict "b" (now the least recently used), not "a".
	if _, err := c.GetOrFetch(context.Background(), "z", fetch("z")); err != nil {
		t.Fatal(err)
	}

	aFetched := false
	if _, err := c.GetOrFetch(context.Background(), "a", func(context.Context) ([]Item, error) { aFetched = true; return nil, nil }); err != nil {
		t.Fatal(err)
	}
	if aFetched {
		t.Error("the recently-touched entry was evicted")
	}

	bFetched := false
	if _, err := c.GetOrFetch(context.Background(), "b", func(context.Context) ([]Item, error) { bFetched = true; return []Item{{ID: "b"}}, nil }); err != nil {
		t.Fatal(err)
	}
	if !bFetched {
		t.Error("the least recently used entry was not evicted")
	}
}

// Clear forgets everything, cached or in flight, for a rescan.
func TestShelfCacheClear(t *testing.T) {
	c := &ShelfCache{}
	c.GetOrFetch(context.Background(), "q", func(context.Context) ([]Item, error) { return []Item{{ID: "1"}}, nil })
	c.Clear()
	fetched := false
	c.GetOrFetch(context.Background(), "q", func(context.Context) ([]Item, error) { fetched = true; return []Item{{ID: "1"}}, nil })
	if !fetched {
		t.Error("Clear did not forget the cached listing")
	}
}

// A fetch that panics must not leave the shelf pending: every later request
// for it would wait for ever.
func TestGetOrFetchSurvivesAPanickingFetch(t *testing.T) {
	var c ShelfCache
	if _, err := c.GetOrFetch(context.Background(), "k", func(context.Context) ([]Item, error) { panic("adapter bug") }); err == nil {
		t.Fatal("a panic came back as no error")
	}
	done := make(chan struct{})
	go func() {
		_, _ = c.GetOrFetch(context.Background(), "k", func(context.Context) ([]Item, error) { return nil, nil })
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the next fetch of the shelf hung")
	}
}

// A shared fetch outlives the caller that started it: one request going away
// (a closed tab, a newer search) used to fail everybody waiting on the fetch.
func TestGetOrFetchSurvivesItsFirstCaller(t *testing.T) {
	c := &ShelfCache{}
	release := make(chan struct{})
	first, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := c.GetOrFetch(first, "q", func(ctx context.Context) ([]Item, error) {
			<-release
			return []Item{{ID: "1"}}, ctx.Err()
		})
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-done; err == nil {
		t.Fatal("the cancelled caller should stop waiting")
	}
	result := make(chan []Item, 1)
	go func() {
		items, _ := c.GetOrFetch(context.Background(), "q", nil)
		result <- items
	}()
	time.Sleep(20 * time.Millisecond)
	close(release)
	if items := <-result; len(items) != 1 {
		t.Fatalf("the second caller got %v, want the shared fetch's item", items)
	}
}

// A listing fetched before a rescan is not cached after it.
func TestClearDropsAFetchInFlight(t *testing.T) {
	c := &ShelfCache{}
	release := make(chan struct{})
	go c.GetOrFetch(context.Background(), "q", func(context.Context) ([]Item, error) {
		<-release
		return []Item{{ID: "old"}}, nil
	})
	time.Sleep(20 * time.Millisecond)
	c.Clear()
	close(release)
	time.Sleep(20 * time.Millisecond)
	items, _ := c.GetOrFetch(context.Background(), "q", func(context.Context) ([]Item, error) {
		return []Item{{ID: "new"}}, nil
	})
	if len(items) != 1 || items[0].ID != "new" {
		t.Fatalf("got %v after the rescan, want the new listing", items)
	}
}
