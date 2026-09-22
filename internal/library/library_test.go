package library

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GabrielHollberg/soundstorm/internal/media"
)

func testLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// The whole point of this package: installing SoundStorm should leave you with
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

	// Restarting SoundStorm must not disturb a library someone has filled in.
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

// A fresh compose install crash-looped on this: Docker creates library/music
// and library/movies as bind-mount points for the backends before SoundStorm
// ever runs, so the folders already exist. Treating that as a failure to
// create them took the whole server down, repeatedly, on the one path that
// matters most - somebody installing it for the first time.
func TestOpenToleratesFoldersSomebodyElseCreated(t *testing.T) {
	root := filepath.Join(t.TempDir(), "library")

	// Exactly what Docker does for a bind mount.
	for _, name := range []string{"music", "movies"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o777); err != nil {
			t.Fatal(err)
		}
	}

	lib, err := Open(root, "./library", testLog())
	if err != nil {
		t.Fatalf("Open failed on folders that already existed: %v", err)
	}

	// Every folder should be present, whoever made it.
	for _, f := range lib.Folders() {
		info, err := os.Stat(filepath.Join(root, f.Name))
		if err != nil {
			t.Errorf("%s missing: %v", f.Name, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%s is not a directory", f.Name)
		}
	}
}

// One unusable folder is a degraded library, not a dead server.
func TestOpenSurvivesAFolderItCannotCreate(t *testing.T) {
	root := filepath.Join(t.TempDir(), "library")
	if err := os.MkdirAll(root, 0o777); err != nil {
		t.Fatal(err)
	}
	// A plain file where a folder should go: mkdir cannot win here.
	if err := os.WriteFile(filepath.Join(root, "music"), []byte("not a folder"), 0o666); err != nil {
		t.Fatal(err)
	}

	lib, err := Open(root, "./library", testLog())
	if err != nil {
		t.Fatalf("Open should degrade, not fail: %v", err)
	}
	if got := lib.PathFor(media.KindEbook); got == "" {
		t.Error("the other folders should still be usable")
	}
	if _, err := os.Stat(filepath.Join(root, "ebooks")); err != nil {
		t.Errorf("ebooks should still have been created: %v", err)
	}
}

// The placeholder is the thing that stops a library folder ever being empty,
// and an empty folder is what makes a backend refuse to notice a deletion. So
// these read as tests about a README and are really tests about whether
// deleting a film makes it disappear.

func TestOpenWritesAPlaceholderInEveryFolder(t *testing.T) {
	root := filepath.Join(t.TempDir(), "library")
	if _, err := Open(root, "./library", testLog()); err != nil {
		t.Fatalf("Open: %v", err)
	}

	for _, f := range layout {
		path := filepath.Join(root, f.Name, readmeName)
		body, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s has no placeholder: %v", f.Name, err)
			continue
		}
		if len(body) == 0 {
			t.Errorf("%s placeholder is empty", f.Name)
		}
		// It has to say what the folder is for, or it is just a marker and
		// somebody will reasonably delete it.
		if !strings.Contains(string(body), f.Example) {
			t.Errorf("%s placeholder does not show the example layout", f.Name)
		}
	}
}

func TestEnsurePlaceholdersPutsADeletedOneBack(t *testing.T) {
	root := filepath.Join(t.TempDir(), "library")
	lib, err := Open(root, "./library", testLog())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Somebody selects everything in the folder and deletes it, placeholder
	// included, while the server is running. This is exactly how it happened.
	gone := filepath.Join(root, "movies", readmeName)
	if err := os.Remove(gone); err != nil {
		t.Fatalf("remove: %v", err)
	}

	lib.EnsurePlaceholders()

	if _, err := os.Stat(gone); err != nil {
		t.Errorf("the placeholder was not restored: %v", err)
	}
}

func TestEnsurePlaceholdersLeavesAnEditedOneAlone(t *testing.T) {
	root := filepath.Join(t.TempDir(), "library")
	lib, err := Open(root, "./library", testLog())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	path := filepath.Join(root, "music", readmeName)
	mine := "my own notes about where the b-sides went\n"
	if err := os.WriteFile(path, []byte(mine), 0o666); err != nil {
		t.Fatalf("write: %v", err)
	}

	lib.EnsurePlaceholders()

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(body) != mine {
		t.Error("a placeholder somebody had written in was overwritten")
	}
}

// A folder holding nothing but its placeholder is a folder nobody has put
// anything in, and the UI must say so. Getting this wrong would replace "drag
// your music here" with a library that claims to have one file in it.
func TestAPlaceholderDoesNotCountAsMedia(t *testing.T) {
	root := filepath.Join(t.TempDir(), "library")
	lib, err := Open(root, "./library", testLog())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if !lib.IsEmpty() {
		t.Error("a library containing only placeholders should report itself empty")
	}
	for _, f := range lib.Folders() {
		if f.Files != 0 {
			t.Errorf("%s counts %d files with only a placeholder in it", f.Name, f.Files)
		}
	}
}

// SoundStorm's central claim is that a user never finds out which servers are
// behind it. A file dropped into their media folder is a poor place to break
// that, however good the explanation would be.
func TestThePlaceholderNamesNoBackend(t *testing.T) {
	for _, f := range layout {
		body := readme(f)
		for _, name := range []string{"Jellyfin", "Navidrome", "Audiobookshelf", "Docker"} {
			if strings.Contains(body, name) {
				t.Errorf("%s placeholder mentions %s", f.Name, name)
			}
		}
	}
}
