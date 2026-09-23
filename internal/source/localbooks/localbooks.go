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

	"github.com/GabrielHollberg/soundstorm/internal/epub"
	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/pdf"
	"github.com/GabrielHollberg/soundstorm/internal/source"
)

// DefaultRescanInterval is how often the library is re-walked. Cheap, because
// unchanged files are served from the cache by size and modification time.
const DefaultRescanInterval = 2 * time.Minute

// Config configures a local book library.
type Config struct {
	ID   string
	Root string // directory SoundStorm scans
	Log  *slog.Logger

	// Kind is what the folder holds: ebooks (EPUB and PDF), or documents
	// (PDF only). Empty means ebooks. One source type serves both because a
	// document is read exactly the way a PDF book is - the difference is the
	// shelf it is browsed on, not how it is opened.
	Kind media.Kind

	RescanInterval time.Duration
}

// book is one indexed title, in whatever format it arrived.
//
// Deliberately not epub.Metadata any more: a PDF describes itself far less
// well, and pretending both formats have the same shape pushed format-specific
// guessing into every caller.
type book struct {
	ID     string // stable: the path relative to Root
	Path   string // absolute path to the book file
	Format string // "epub" or "pdf"

	Title       string
	Creators    []string
	Series      string
	SeriesIndex string
	Language    string
	Subjects    []string
	Year        int

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
	id      string
	root    string
	log     *slog.Logger
	kind    media.Kind
	formats map[string]bool // formatOf results this folder serves

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
	kind := cfg.Kind
	if kind == "" {
		kind = media.KindEbook
	}
	formats := map[string]bool{"epub": true, "pdf": true}
	switch kind {
	case media.KindEbook:
	case media.KindDocument:
		formats = map[string]bool{"pdf": true}
	default:
		return nil, fmt.Errorf("localbooks %q: cannot serve %s", cfg.ID, kind)
	}
	return &Source{
		id:             cfg.ID,
		root:           cfg.Root,
		log:            log.With("source", cfg.ID),
		kind:           kind,
		formats:        formats,
		rescanInterval: interval,
		byID:           map[string]book{},
	}, nil
}

func (s *Source) ID() string       { return s.id }
func (s *Source) Kind() media.Kind { return s.kind }

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
		if !s.formats[formatOf(d.Name())] {
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

	// Title then path, which is exactly media.Less over the items these become:
	// a plain title sort left two books of the same name free to swap between
	// one page and the next.
	sort.Slice(found, func(i, j int) bool {
		if found[i].Title != found[j].Title {
			return found[i].Title < found[j].Title
		}
		return found[i].ID < found[j].ID
	})
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

// formatOf reports the book format a filename implies, or "" if it is not one.
func formatOf(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".epub":
		return "epub"
	case ".pdf":
		return "pdf"
	default:
		return ""
	}
}

// read builds one index entry.
func (s *Source) read(bookPath, id string, size int64, modTime time.Time) (book, error) {
	b := book{ID: id, Path: bookPath, Format: formatOf(bookPath), size: size, modTime: modTime}

	var err error
	switch b.Format {
	case "pdf":
		err = s.readPDF(&b)
	default:
		err = s.readEPUB(&b)
	}
	if err != nil {
		return book{}, err
	}

	if b.Title == "" {
		// Better a filename than an untitled row.
		b.Title = strings.TrimSuffix(filepath.Base(bookPath), filepath.Ext(bookPath))
	}
	return b, nil
}

// readEPUB fills in a book from an EPUB, preferring Calibre's sidecar.
func (s *Source) readEPUB(b *book) error {
	dir := filepath.Dir(b.Path)

	var meta epub.Metadata
	// A Calibre library puts metadata.opf beside the book. It is the same
	// format as the one inside the epub and it is better maintained, because
	// it is what the user edited in Calibre.
	if raw, err := os.ReadFile(filepath.Join(dir, "metadata.opf")); err == nil {
		if sidecar, _, err := epub.ParseOPF(raw, ""); err == nil {
			meta = sidecar
		}
	}

	if meta.Title == "" {
		opened, err := epub.Open(b.Path)
		if err != nil {
			return err
		}
		meta = opened.Meta
		b.Cover.Href = opened.Meta.CoverHref
		opened.Close()
	} else if inner, err := epub.Open(b.Path); err == nil {
		// Sidecar metadata won, but the cover still has to come from somewhere.
		b.Cover.Href = inner.Meta.CoverHref
		inner.Close()
	}

	b.Title = meta.Title
	b.Creators = meta.Creators
	b.Series = meta.Series
	b.SeriesIndex = meta.SeriesIndex
	b.Language = meta.Language
	b.Subjects = meta.Subjects
	b.Year = meta.Year()
	s.findSidecarCover(b)
	return nil
}

// readPDF fills in a book from a PDF.
//
// The filename is merged in rather than used only as a last resort, because a
// PDF that knows its title often does not know its author, and whoever saved
// the file usually put both in its name.
func (s *Source) readPDF(b *book) error {
	meta, err := pdf.Open(b.Path)
	if err != nil {
		return err
	}
	meta = meta.Merge(pdf.FromFilename(filepath.Base(b.Path)))

	b.Title = meta.Title
	b.Creators = meta.Authors
	b.Subjects = meta.Keywords
	b.Year = meta.Year()

	// No cover: extracting one means rendering page one, which needs a PDF
	// renderer this project is not going to carry. A sidecar image beside the
	// file is still honoured, because that is cheap and some people make them.
	s.findSidecarCover(b)
	return nil
}

// findSidecarCover looks for artwork sitting next to the book.
//
// Calibre extracts one there, which saves an unzip on every artwork request.
func (s *Source) findSidecarCover(b *book) {
	if b.Cover.SidecarPath != "" {
		return
	}
	dir := filepath.Dir(b.Path)
	stem := strings.TrimSuffix(filepath.Base(b.Path), filepath.Ext(b.Path))
	for _, name := range []string{
		"cover.jpg", "cover.jpeg", "cover.png",
		stem + ".jpg", stem + ".jpeg", stem + ".png",
	} {
		candidate := filepath.Join(dir, name)
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			b.Cover.SidecarPath = candidate
			return
		}
	}
}

// Search matches the in-memory index. Ranking is left to internal/federate,
// which scores every source's hits on the same scale.
// Rescan re-walks the ebook folder now rather than at the next sweep.
//
// There is no backend to ask here - SoundStorm is the one that indexes this
// folder - so this is simply the scan the ticker would have run in up to two
// minutes' time.
func (s *Source) Rescan(ctx context.Context) error {
	return s.scan(ctx)
}

func (s *Source) Search(_ context.Context, q media.Query) ([]media.Item, error) {
	// An empty query lists the shelf rather than matching nothing. terms is
	// then empty, and matches() with no terms accepts every book - so the
	// loop below needs no second path.
	terms := strings.Fields(normalize(q.Text))

	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.scanned.IsZero() {
		return nil, fmt.Errorf("library has not finished scanning yet")
	}

	// Matched first, then sorted, then cut - in that order, and the order is
	// what makes paging work.
	//
	// federate asks every source for the first N and merges, which only
	// yields the globally first N if each source really did return *its* first
	// N by the same key the merge sorts on. Cutting at the limit while still
	// in scan order would hand back an arbitrary subset, so page two would
	// repeat some books and skip others. See federate.Search.
	matched := make([]media.Item, 0, len(s.books))
	for _, b := range s.books {
		if matches(b, terms) {
			matched = append(matched, b.item(s.id, s.kind))
		}
	}
	sort.SliceStable(matched, func(a, b int) bool {
		if matched[a].Title != matched[b].Title {
			return matched[a].Title < matched[b].Title
		}
		return matched[a].ID < matched[b].ID
	})

	if limit := q.LimitOr(25); len(matched) > limit {
		matched = matched[:limit]
	}
	return matched, nil
}

// matches reports whether every query term appears somewhere useful. Requiring
// all terms keeps "wizard earthsea" from returning every book with "the" in it.
func matches(b book, terms []string) bool {
	haystack := normalize(strings.Join(append([]string{
		b.Title, b.Series, strings.Join(b.Subjects, " "),
	}, b.Creators...), " "))
	for _, t := range terms {
		if !strings.Contains(haystack, t) {
			return false
		}
	}
	return true
}

func (b book) item(sourceID string, kind media.Kind) media.Item {
	format := b.Format
	if format == "" {
		format = "epub"
	}
	item := media.Item{
		ID:       b.ID,
		SourceID: sourceID,
		Kind:     kind,
		Title:    b.Title,
		Creators: b.Creators,
		Year:     b.Year,
		Extra:    map[string]string{"format": format},
	}
	if b.Cover.SidecarPath != "" || b.Cover.Href != "" {
		item.ArtID = b.ID
	}
	if b.Series != "" {
		item.Subtitle = b.Series
		item.Extra["series"] = b.Series
		if b.SeriesIndex != "" {
			item.Extra["seriesIndex"] = b.SeriesIndex
		}
	}
	if b.Language != "" {
		item.Extra["language"] = b.Language
	}
	if len(b.Subjects) > 0 {
		item.Extra["tags"] = strings.Join(b.Subjects, ", ")
	}
	return item
}

// StreamTarget hands over the book file itself.
func (s *Source) StreamTarget(_ context.Context, itemID string) (source.Target, error) {
	b, ok := s.lookup(itemID)
	if !ok {
		return source.Target{}, fmt.Errorf("localbooks %q: no book %q", s.id, itemID)
	}
	contentType := "application/epub+zip"
	if b.Format == "pdf" {
		// Browsers decide whether to render a PDF inline from this header, so
		// getting it wrong turns "read the book" into "download the book".
		contentType = "application/pdf"
	}
	return source.Target{
		FilePath:    b.Path,
		ContentType: contentType,
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
	if b.Format == "pdf" {
		// A PDF is one file with no parts worth serving separately, and the
		// browser has its own viewer for it. Saying so plainly beats returning
		// an empty manifest the reader would then fail to make sense of.
		return nil, fmt.Errorf("localbooks %q: %q is a pdf and is read whole", s.id, itemID)
	}
	opened, err := epub.Open(b.Path)
	if err != nil {
		// epub.Open's error wraps whatever the zip package or the filesystem
		// said, and both quote the path in full - the container's own
		// absolute path, not the one anybody outside was ever shown. That
		// reached the browser as a 404 body; the detail belongs in the log,
		// not on someone's screen.
		s.log.Warn("could not open book", "id", itemID, "path", b.Path, "err", err)
		return nil, fmt.Errorf("localbooks %q: could not open %q", s.id, itemID)
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
