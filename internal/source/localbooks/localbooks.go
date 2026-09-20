// Package localbooks serves ebooks straight off the disk, with no backend.
//
// Every other source in SoundStorm wraps a media server. This one does not, and
// the reason is that an ebook library does not need one: an EPUB carries its
// own title, author and cover (see internal/epub), and a book needs no
// transcoding. Everything Calibre-Web was doing for us - find the books, read
// their metadata, answer a search, hand over the file - is a directory walk and
// an XML parse.
//
// What that buys: "drop files into a folder" becomes true for ebooks the way it
// already was for music, film and audiobooks. Calibre-Web was the one backend
// that needed a *database* rather than a folder, and the one whose credentials
// SoundStorm could not rotate.
//
// Existing Calibre libraries still work, without a SQLite reader: Calibre
// writes a metadata.opf sidecar next to every book in exactly the format an
// EPUB carries internally, so the same parser reads both, and the sidecar wins
// because it is richer (series, tags, corrected authors).
package localbooks

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gabehollberg/soundstorm/internal/epub"
	"github.com/gabehollberg/soundstorm/internal/media"
	"github.com/gabehollberg/soundstorm/internal/source"
)

// DefaultRescanInterval is how often the library is re-walked. Cheap, because
// unchanged files are served from the cache by size and modification time.
const DefaultRescanInterval = 2 * time.Minute

// Config configures a local book library.
type Config struct {
	ID   string
	Root string // directory SoundStorm scans
	Log  *slog.Logger

	RescanInterval time.Duration
}

// book is one indexed title.
type book struct {
	ID    string // stable: the path relative to Root
	Path  string // absolute path to the book file
	Meta  epub.Metadata
	Cover coverSource

	size    int64
	modTime time.Time
}

// coverSource says where a book's artwork comes from. A sidecar file is
// preferred: it is already an image on disk, so serving it needs no unzip.
type coverSource struct {
	SidecarPath string // cover.jpg next to the book, if present
	Href        string // path inside the epub, if not
}

// Source is a directory of ebooks.
type Source struct {
	id   string
	root string
	log  *slog.Logger

	rescanInterval time.Duration

	mu       sync.RWMutex
	books    []book
	byID     map[string]book
	scanned  time.Time
	scanning bool
}

// New builds a local book source. It does no I/O; call Start.
func New(cfg Config) (*Source, error) {
	if cfg.Root == "" {
		return nil, fmt.Errorf("localbooks %q: root is required", cfg.ID)
	}
	info, err := os.Stat(cfg.Root)
	if err != nil {
		return nil, fmt.Errorf("localbooks %q: %w", cfg.ID, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("localbooks %q: %s is not a directory", cfg.ID, cfg.Root)
	}
	interval := cfg.RescanInterval
	if interval <= 0 {
		interval = DefaultRescanInterval
	}
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	return &Source{
		id:             cfg.ID,
		root:           cfg.Root,
		log:            log.With("source", cfg.ID),
		rescanInterval: interval,
		byID:           map[string]book{},
	}, nil
}

func (s *Source) ID() string       { return s.id }
func (s *Source) Kind() media.Kind { return media.KindEbook }

// Start performs the first scan, then rescans on a ticker.
//
// The first scan is synchronous so that "ready" in the setup UI means
// "searchable". On a large library that can take a while, which is exactly why
// provisioning already runs in the background and reports progress.
func (s *Source) Start(ctx context.Context) error {
	if err := s.scan(ctx); err != nil {
		return err
	}
	go s.rescanLoop(ctx)
	return nil
}

func (s *Source) rescanLoop(ctx context.Context) {
	ticker := time.NewTicker(s.rescanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.scan(ctx); err != nil && ctx.Err() == nil {
				s.log.Warn("rescan failed", "err", err)
			}
		}
	}
}

// scan walks the root and rebuilds the index.
func (s *Source) scan(ctx context.Context) error {
	s.mu.Lock()
	if s.scanning {
		s.mu.Unlock()
		return nil
	}
	s.scanning = true
	previous := s.byID
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.scanning = false
		s.mu.Unlock()
	}()

	started := time.Now()
	var found []book
	var parsed, reused, failed int

	err := filepath.WalkDir(s.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable subdirectory should cost us that subdirectory, not
			// the whole library.
			s.log.Debug("skipping unreadable path", "path", p, "err", err)
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && p != s.root {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(d.Name()), ".epub") {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(s.root, p)
		if err != nil {
			return nil
		}
		id := filepath.ToSlash(rel)

		// Unchanged since last scan? Reuse it and skip the parse. This is what
		// makes rescanning a large library cheap.
		if old, ok := previous[id]; ok && old.size == info.Size() && old.modTime.Equal(info.ModTime()) {
			found = append(found, old)
			reused++
			return nil
		}

		b, err := s.read(p, id, info.Size(), info.ModTime())
		if err != nil {
			s.log.Warn("could not read book", "path", rel, "err", err)
			failed++
			return nil
		}
		found = append(found, b)
		parsed++
		return nil
	})
	if err != nil {
		return err
	}

	sort.Slice(found, func(i, j int) bool { return found[i].Meta.Title < found[j].Meta.Title })
	byID := make(map[string]book, len(found))
	for _, b := range found {
		byID[b.ID] = b
	}

	s.mu.Lock()
	s.books = found
	s.byID = byID
	s.scanned = time.Now()
	s.mu.Unlock()

	s.log.Info("library scanned",
		"books", len(found), "parsed", parsed, "reused", reused, "failed", failed,
		"tookMs", time.Since(started).Milliseconds())
	return nil
}

// read builds one index entry, preferring Calibre's sidecar metadata.
func (s *Source) read(bookPath, id string, size int64, modTime time.Time) (book, error) {
	b := book{ID: id, Path: bookPath, size: size, modTime: modTime}
	dir := filepath.Dir(bookPath)

	// A Calibre library puts metadata.opf beside the book. It is the same
	// format as the one inside the epub and it is better maintained, because
	// it is what the user edited in Calibre.
	if raw, err := os.ReadFile(filepath.Join(dir, "metadata.opf")); err == nil {
		if meta, _, err := epub.ParseOPF(raw, ""); err == nil {
			b.Meta = meta
		}
	}

	if b.Meta.Title == "" {
		opened, err := epub.Open(bookPath)
		if err != nil {
			return book{}, err
		}
		b.Meta = opened.Meta
		b.Cover.Href = opened.Meta.CoverHref
		opened.Close()
	} else if inner, err := epub.Open(bookPath); err == nil {
		// Sidecar metadata won, but the cover still has to come from somewhere.
		b.Cover.Href = inner.Meta.CoverHref
		inner.Close()
	}

	// Calibre also extracts the cover next to the book, which saves an unzip
	// on every artwork request.
	for _, name := range []string{"cover.jpg", "cover.jpeg", "cover.png"} {
		candidate := filepath.Join(dir, name)
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			b.Cover.SidecarPath = candidate
			break
		}
	}

	if b.Meta.Title == "" {
		// Fall back to the filename rather than showing an untitled row.
		b.Meta.Title = strings.TrimSuffix(filepath.Base(bookPath), filepath.Ext(bookPath))
	}
	return b, nil
}

// Search matches the in-memory index. Ranking is left to internal/federate,
// which scores every source's hits on the same scale.
func (s *Source) Search(_ context.Context, q media.Query) ([]media.Item, error) {
	needle := normalize(q.Text)
	if needle == "" {
		return nil, nil
	}
	terms := strings.Fields(needle)

	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.scanned.IsZero() {
		return nil, fmt.Errorf("library has not finished scanning yet")
	}

	limit := q.LimitOr(25)
	items := make([]media.Item, 0, limit)
	for _, b := range s.books {
		if len(items) >= limit {
			break
		}
		if !matches(b, terms) {
			continue
		}
		items = append(items, b.item(s.id))
	}
	return items, nil
}

// matches reports whether every query term appears somewhere useful. Requiring
// all terms keeps "wizard earthsea" from returning every book with "the" in it.
func matches(b book, terms []string) bool {
	haystack := normalize(strings.Join(append([]string{
		b.Meta.Title, b.Meta.Series, strings.Join(b.Meta.Subjects, " "),
	}, b.Meta.Creators...), " "))
	for _, t := range terms {
		if !strings.Contains(haystack, t) {
			return false
		}
	}
	return true
}

func (b book) item(sourceID string) media.Item {
	item := media.Item{
		ID:       b.ID,
		SourceID: sourceID,
		Kind:     media.KindEbook,
		Title:    b.Meta.Title,
		Creators: b.Meta.Creators,
		Year:     b.Meta.Year(),
		Extra:    map[string]string{"format": "epub"},
	}
	if b.Cover.SidecarPath != "" || b.Cover.Href != "" {
		item.ArtID = b.ID
	}
	if b.Meta.Series != "" {
		item.Subtitle = b.Meta.Series
		item.Extra["series"] = b.Meta.Series
		if b.Meta.SeriesIndex != "" {
			item.Extra["seriesIndex"] = b.Meta.SeriesIndex
		}
	}
	if b.Meta.Language != "" {
		item.Extra["language"] = b.Meta.Language
	}
	if len(b.Meta.Subjects) > 0 {
		item.Extra["tags"] = strings.Join(b.Meta.Subjects, ", ")
	}
	return item
}

// StreamTarget hands over the book file itself.
func (s *Source) StreamTarget(_ context.Context, itemID string) (source.Target, error) {
	b, ok := s.lookup(itemID)
	if !ok {
		return source.Target{}, fmt.Errorf("localbooks %q: no book %q", s.id, itemID)
	}
	return source.Target{
		FilePath:    b.Path,
		ContentType: "application/epub+zip",
		Name:        filepath.Base(b.Path),
	}, nil
}

// ArtTarget serves the cover, from the sidecar when there is one and from
// inside the book when there is not.
func (s *Source) ArtTarget(_ context.Context, artID string) (source.Target, error) {
	b, ok := s.lookup(artID)
	if !ok {
		return source.Target{}, fmt.Errorf("localbooks %q: no book %q", s.id, artID)
	}

	if b.Cover.SidecarPath != "" {
		return source.Target{FilePath: b.Cover.SidecarPath, Name: filepath.Base(b.Cover.SidecarPath)}, nil
	}
	if b.Cover.Href == "" {
		return source.Target{}, fmt.Errorf("localbooks %q: %q has no cover", s.id, artID)
	}

	opened, err := epub.Open(b.Path)
	if err != nil {
		return source.Target{}, err
	}
	defer opened.Close()

	data, contentType, err := opened.Cover()
	if err != nil {
		return source.Target{}, err
	}
	return source.Target{
		Bytes:       data,
		ContentType: contentType,
		Name:        filepath.Base(b.Cover.Href),
		ModTime:     b.modTime,
	}, nil
}

// OpenBook exposes the inside of a book so the reader can walk it.
//
// The zip is reopened per reading session rather than held: a book is opened
// once and read for an hour, so the cost is nothing and the alternative is a
// cache with a lifetime nobody wants to reason about.
func (s *Source) OpenBook(_ context.Context, itemID string) (source.OpenBook, error) {
	b, ok := s.lookup(itemID)
	if !ok {
		return nil, fmt.Errorf("localbooks %q: no book %q", s.id, itemID)
	}
	opened, err := epub.Open(b.Path)
	if err != nil {
		return nil, err
	}
	return &openBook{Book: opened}, nil
}

// openBook adapts epub.Book to source.OpenBook.
type openBook struct{ *epub.Book }

func (o *openBook) Entries() []source.BookEntry {
	inner := o.Book.Entries()
	out := make([]source.BookEntry, len(inner))
	for i, e := range inner {
		out[i] = source.BookEntry{Name: e.Name, Size: e.Size}
	}
	return out
}

func (s *Source) lookup(id string) (book, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.byID[id]
	return b, ok
}

// Health reports whether the library has been read and is still readable.
func (s *Source) Health(_ context.Context) error {
	s.mu.RLock()
	scanned := s.scanned
	count := len(s.books)
	s.mu.RUnlock()

	if scanned.IsZero() {
		return fmt.Errorf("library has not been scanned yet")
	}
	if _, err := os.Stat(s.root); err != nil {
		return fmt.Errorf("library root unreadable: %w", err)
	}
	// An empty library is healthy. Someone who has not added books yet has a
	// working ebook backend with nothing in it, not a broken one.
	_ = count
	return nil
}

// Count reports how many books are indexed, for setup progress.
func (s *Source) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.books)
}

// normalize lowercases and strips punctuation so "Le Guin's" matches "le
// guins".
func normalize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
