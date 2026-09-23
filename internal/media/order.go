package media

import (
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
type ShelfCache struct {
	TTL time.Duration

	mu      sync.Mutex
	entries map[string]shelfEntry
}

type shelfEntry struct {
	items []Item
	at    time.Time
}

// maxShelves bounds the cache to the few listings being scrolled at once.
const maxShelves = 8

// Get returns a cached listing, if a fresh one exists.
func (c *ShelfCache) Get(key string) ([]Item, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || time.Since(e.at) > c.ttl() {
		return nil, false
	}
	return e.items, true
}

// Put stores a listing.
func (c *ShelfCache) Put(key string, items []Item) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil || len(c.entries) >= maxShelves {
		c.entries = map[string]shelfEntry{}
	}
	c.entries[key] = shelfEntry{items: items, at: time.Now()}
}

// Clear forgets everything, for when the backend has been told to look again.
func (c *ShelfCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = nil
}

func (c *ShelfCache) ttl() time.Duration {
	if c.TTL > 0 {
		return c.TTL
	}
	return 30 * time.Second
}
