package localbooks

import (
	"archive/zip"
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GabrielHollberg/soundstorm/internal/media"
)

// writeValidEPUB builds a minimal but structurally valid EPUB, so the scan
// indexes it - this test is about what happens when *opening* it later
// fails, not about the scan's own tolerance for a bad file.
func writeValidEPUB(t *testing.T, path string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name, body string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	add("mimetype", "application/epub+zip")
	add("META-INF/container.xml", `<?xml version="1.0"?>
	<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
	  <rootfiles><rootfile full-path="content.opf" media-type="application/oebps-package+xml"/></rootfiles>
	</container>`)
	add("content.opf", `<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
	  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
	    <dc:title>A Book</dc:title>
	    <dc:creator>Someone</dc:creator>
	  </metadata>
	  <manifest><item id="ch1" href="ch1.xhtml" media-type="application/xhtml+xml"/></manifest>
	  <spine><itemref idref="ch1"/></spine>
	</package>`)
	add("ch1.xhtml", "<html><body>Chapter one</body></html>")
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write epub: %v", err)
	}
}

// A book that fails to open - corrupted, or deleted, between the scan and the
// request - must not hand back the container's own absolute path. zip.Open
// and the filesystem both quote it in full on failure, and that reached a
// browser as the body of a 404. The real path still goes to the log.
func TestOpenBookDoesNotLeakTheFilesystemPath(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "book.epub")
	writeValidEPUB(t, path)

	var logged bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logged, nil))
	s, err := New(Config{ID: "ebooks", Root: root, Kind: media.KindEbook, Log: log})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.scan(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}
	items, _ := s.Search(context.Background(), media.Query{Limit: 10})
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}

	// Corrupted after the scan indexed it, but before anyone opens it - the
	// scan's own tolerance for a bad file is a separate concern (kind_test.go).
	if err := os.WriteFile(path, []byte("not a zip file at all"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = s.OpenBook(context.Background(), items[0].ID)
	if err == nil {
		t.Fatal("opening a corrupted epub was not refused")
	}
	// The item id may legitimately echo the file's own name - that is
	// already known to whoever searched for it. What must not appear is the
	// filesystem prefix in front of it: the container's own root.
	if strings.Contains(err.Error(), root) {
		t.Errorf("the error handed to the caller names the filesystem path: %v", err)
	}

	// The path is still findable by whoever runs the server.
	if !strings.Contains(logged.String(), "book.epub") {
		t.Error("the real path was not logged for the owner to find")
	}
}
