package media

import (
	"container/list"
	"sort"
	"sync"
	"time"
)

// Ordering. A merged list is ordered by relevance, then by OrderKey, and pages
// of it are cut *after* the merge - so every source must return its own first
// N in exactly this order, or page two holds items that sort before page one's
// and the list repeats and skips as it scrolls.
//
// No backend's own sort can be trusted to agree. Measured on a real library:
// Navidrome's search3 lists songs in an order of its own (of the true first 50
// by title, its first 50 held none, across 4,413 songs); Audiobookshelf's title
// sort ignores case, so "How to Fast" and "How To Overcome" swap; Jellyfin's
// SortName drops a leading "The". So adapters that browse fetch the whole
// shelf and order it with Less - this comparator, and nothing else.

// OrderKey is what orders items of equal relevance: the adapter's SortKey
// when it set one, the title otherwise.
func (i Item) OrderKey() string {
	if i.SortKey != "" {
		return i.SortKey
	}
	return i.Title
}

// Less orders two items from one source the way the merged list will. Equal
// keys fall back to the id, so two songs called "Intro" cannot trade places
// between one page and the next.
func Less(a, b Item) bool {
	if ka, kb := a.OrderKey(), b.OrderKey(); ka != kb {
		return ka < kb
	}
	return a.ID < b.ID
}

// SortForPaging orders items in place with Less.
func SortForPaging(items []Item) {
	sort.SliceStable(items, func(i, j int) bool { return Less(items[i], items[j]) })
}

// FirstN returns the first n of items ordered by Less - what a browsing
// source must hand back for a page asking for n.
func FirstN(items []Item, n int) []Item {
	sorted := append([]Item(nil), items...)
	SortForPaging(sorted)
	if n > 0 && len(sorted) > n {
		sorted = sorted[:n]
	}
	return sorted
}

// ShelfCache keeps whole listings for a short while, so that scrolling
// through a shelf - one request per page, each needing the whole shelf -
// fetches it from the backend once rather than once a page. Keyed by the
// query text; cleared by a rescan, which is when a listing goes stale.
//
// Two problems a first version of this had, both found by asking what a
// naive cache breaks rather than by using one:
//
//   - Concurrent misses. Opening the app fires more than one page-1 request
//     at once - a browser's own retry, more than one tab, more than one
//     device in a household - and against a cold cache every one of them
//     missed and fetched the whole shelf independently: N times the backend
//     calls and N times the bytes for one page load. GetOrFetch coalesces
//     concurrent calls for one key into a single fetch; every caller gets
//     its result.
//   - Eviction that wipes everything. The key is the query text, which for a
//     browsing member is whatever they choose to type, and each one caches a
//     whole shelf - thousands of items for a real library. Wiping the whole
//     cache once it filled meant a burst of one-off searches could evict the
//     listing an open scroll was still paging through, mid-scroll. Eviction
//     is LRU now, so the entry actually in use survives a burst elsewhere.
type ShelfCache struct {
	TTL time.Duration

	mu      sync.Mutex
	order   *list.List               // front is most recently used
	entries map[string]*list.Element // key -> element holding *shelfEntry
	pending map[string]*shelfFetch   // fetches in flight, for coalescing
}

type shelfEntry struct {
	key   string
	items []Item
	at    time.Time
}

// shelfFetch is one fetch in progress, shared by every caller waiting on it.
type shelfFetch struct {
	done  chan struct{}
	items []Item
	err   error
}

// maxShelves bounds the cache to the few listings actually being scrolled at
// once.
const maxShelves = 8

// GetOrFetch returns a cached listing for key when a fresh one exists, and
// otherwise calls fetch and caches what it returns. A fetch already running
// for this key, started by another caller, is waited on and shared rather
// than repeated - see the type doc for why that matters.
func (c *ShelfCache) GetOrFetch(key string, fetch func() ([]Item, error)) ([]Item, error) {
	c.mu.Lock()
	if items, ok := c.getLocked(key); ok {
		c.mu.Unlock()
		return items, nil
	}
	if f, ok := c.pending[key]; ok {
		c.mu.Unlock()
		<-f.done
		return f.items, f.err
	}
	f := &shelfFetch{done: make(chan struct{})}
	if c.pending == nil {
		c.pending = map[string]*shelfFetch{}
	}
	c.pending[key] = f
	c.mu.Unlock()

	// Deliberately outside the lock: this is the network call, and holding
	// the lock across it would serialise every shelf's fetches behind
	// whichever one is slowest.
	items, err := fetch()

	c.mu.Lock()
	delete(c.pending, key)
	if err == nil {
		c.putLocked(key, items)
	}
	c.mu.Unlock()

	f.items, f.err = items, err
	close(f.done)
	return items, err
}

func (c *ShelfCache) getLocked(key string) ([]Item, bool) {
	el, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	e := el.Value.(*shelfEntry)
	if time.Since(e.at) > c.ttl() {
		c.removeLocked(el)
		return nil, false
	}
	c.order.MoveToFront(el)
	return e.items, true
}

func (c *ShelfCache) putLocked(key string, items []Item) {
	if el, ok := c.entries[key]; ok {
		el.Value.(*shelfEntry).items = items
		el.Value.(*shelfEntry).at = time.Now()
		c.order.MoveToFront(el)
		return
	}
	if c.order == nil {
		c.order = list.New()
		c.entries = map[string]*list.Element{}
	}
	el := c.order.PushFront(&shelfEntry{key: key, items: items, at: time.Now()})
	c.entries[key] = el
	for len(c.entries) > maxShelves {
		c.removeLocked(c.order.Back())
	}
}

func (c *ShelfCache) removeLocked(el *list.Element) {
	e := el.Value.(*shelfEntry)
	delete(c.entries, e.key)
	c.order.Remove(el)
}

// Clear forgets everything, for when the backend has been told to look again.
func (c *ShelfCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.order = nil
	c.entries = nil
}

func (c *ShelfCache) ttl() time.Duration {
	if c.TTL > 0 {
		return c.TTL
	}
	return 30 * time.Second
}
