package localbooks

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/GabrielHollberg/soundstorm/internal/media"
)

// One source type serves two shelves, told only which kind it holds. A
// documents folder must neither show an epub nor label a PDF as a book, or the
// shelves blur back into one.
func TestAFolderServesOnlyItsOwnKind(t *testing.T) {
	root := t.TempDir()
	// Only the extension matters to the scan's filter; an unreadable PDF is
	// still listed, titled from its file name.
	os.WriteFile(filepath.Join(root, "Manual - Acme (2020).pdf"), []byte("%PDF-1.4\n%%EOF\n"), 0o644)
	os.WriteFile(filepath.Join(root, "novel.epub"), []byte("not a real epub"), 0o644)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	for _, c := range []struct {
		kind  media.Kind
		count int
	}{
		{media.KindDocument, 1}, // the PDF only
		{media.KindEbook, 1},    // the epub fails to parse; the PDF is a book here
	} {
		s, err := New(Config{ID: string(c.kind), Root: root, Kind: c.kind, Log: quiet})
		if err != nil {
			t.Fatalf("New(%s): %v", c.kind, err)
		}
		if s.Kind() != c.kind {
			t.Errorf("Kind() = %q, want %q", s.Kind(), c.kind)
		}
		if err := s.scan(context.Background()); err != nil {
			t.Fatalf("scan: %v", err)
		}
		items, _ := s.Search(context.Background(), media.Query{Limit: 50})
		if len(items) != c.count {
			t.Errorf("%s folder listed %d items, want %d", c.kind, len(items), c.count)
		}
		for _, it := range items {
			if it.Kind != c.kind {
				t.Errorf("%s folder labelled %q as %q", c.kind, it.Title, it.Kind)
			}
		}
	}
}

func TestAFolderCannotServeAKindItCannotRead(t *testing.T) {
	if _, err := New(Config{ID: "x", Root: t.TempDir(), Kind: media.KindVideo}); err == nil {
		t.Error("a book folder agreed to serve video")
	}
}
