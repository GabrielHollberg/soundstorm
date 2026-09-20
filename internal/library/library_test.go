package library

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/gabehollberg/atrium/internal/media"
)

func testLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// The whole point of this package: installing atrium should leave you with
// folders to put media in, without reading anything first.
func TestOpenCreatesTheFoldersFromNothing(t *testing.T) {
	root := filepath.Join(t.TempDir(), "library")

	lib, err := Open(root, "./library", testLog())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	for _, name := range []string{"music", "movies", "audiobooks", "ebooks"} {
		info, err := os.Stat(filepath.Join(root, name))
		if err != nil {
			t.Errorf("%s was not created: %v", name, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%s is not a directory", name)
		}
	}

	if !lib.IsEmpty() {
		t.Error("a freshly created library should report itself empty")
	}
}

func TestOpenIsIdempotentAndKeepsContent(t *testing.T) {
	root := filepath.Join(t.TempDir(), "library")
	if _, err := Open(root, "", testLog()); err != nil {
		t.Fatalf("first Open: %v", err)
	}

	keep := filepath.Join(root, "music", "keep.mp3")
	if err := os.WriteFile(keep, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Restarting atrium must not disturb a library someone has filled in.
	if _, err := Open(root, "", testLog()); err != nil {
		t.Fatalf("second Open: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("reopening the library removed content: %v", err)
	}
}

func TestPathForEveryKind(t *testing.T) {
	root := filepath.Join(t.TempDir(), "library")
	lib, err := Open(root, "", testLog())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	for _, kind := range media.AllKinds() {
		if got := lib.PathFor(kind); got == "" {
			t.Errorf("no folder for kind %q", kind)
		}
	}
	if got := lib.PathFor(media.Kind("nonsense")); got != "" {
		t.Errorf("PathFor(nonsense) = %q, want empty", got)
	}
}

// The counts drive the UI's "empty" vs "still scanning" distinction, so they
// have to count the right things and ignore the rest.
func TestFoldersCountOnlyMediaFiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), "library")
	lib, err := Open(root, "./library", testLog())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	write := func(parts ...string) {
		t.Helper()
		path := filepath.Join(append([]string{root}, parts...)...)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write("music", "Artist", "Album", "01.flac")
	write("music", "Artist", "Album", "02.MP3")    // extension match is case-insensitive
	write("music", "Artist", "Album", "cover.jpg") // not music
	write("music", "README.txt")                   // the placeholder we ship
	write("movies", "Arrival (2016)", "film.mkv")
	write("ebooks", "book.epub")
	write("ebooks", ".hidden", "ignored.epub") // dot-directories are skipped

	counts := map[string]int{}
	for _, f := range lib.Folders() {
		counts[f.Name] = f.Files
		if want := "./library/" + f.Name; f.Hint != want {
			t.Errorf("hint for %s = %q, want %q", f.Name, f.Hint, want)
		}
	}

	for name, want := range map[string]int{
		"music": 2, "movies": 1, "audiobooks": 0, "ebooks": 1,
	} {
		if counts[name] != want {
			t.Errorf("%s counted %d files, want %d", name, counts[name], want)
		}
	}

	if lib.IsEmpty() {
		t.Error("a library with files in it should not report empty")
	}
}

func TestEveryFolderIsDescribed(t *testing.T) {
	root := filepath.Join(t.TempDir(), "library")
	lib, err := Open(root, "", testLog())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// These strings are the product: they are what a new user reads instead of
	// a README. An empty one is a bug, not a cosmetic gap.
	for _, f := range lib.Folders() {
		if f.Description == "" || f.Example == "" || f.Name == "" {
			t.Errorf("folder %+v is missing user-facing text", f)
		}
	}
}
