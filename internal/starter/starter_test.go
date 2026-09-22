package starter

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// always reports every folder empty, as on a fresh install.
func always(string) bool { return true }

func TestInstallPopulatesEmptyFolders(t *testing.T) {
	root := t.TempDir()

	installed, err := Install(root, always, testLog())
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(installed) == 0 {
		t.Fatal("nothing was installed")
	}

	byFolder := map[string]Unpacked{}
	for _, u := range installed {
		byFolder[u.Folder] = u
	}
	// Every shelf the bundle covers. This is the assertion that says what the
	// bundle is for: a first run has to be able to answer "does this kind of
	// media work", and it cannot answer that for a shelf it left empty. Video is
	// deliberately absent - see the package comment - so movies and tv are not
	// in this list, and a credit points at Blender instead.
	for _, want := range []string{"ebooks", "audiobooks", "music"} {
		if byFolder[want].Files == 0 {
			t.Errorf("no files installed into %s", want)
		}
	}

	// One each, not a selection. It was ten ebooks and four music tracks, which
	// demonstrated nothing the first of each did not and made the ebook shelf
	// look like somebody else's taste in books.
	for folder, u := range byFolder {
		if u.Files > 1 {
			t.Errorf("%s has %d files; the bundle is one item per shelf", folder, u.Files)
		}
	}

	// The files have to actually be on disk and non-empty, not merely counted.
	var files int
	var bytes int64
	err = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if info.Size() == 0 {
			t.Errorf("%s was written empty", path)
		}
		files++
		bytes += info.Size()
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if files != len(byFolder) {
		t.Errorf("%d files on disk for %d shelves; expected one each", files, len(byFolder))
	}

	// Worth knowing if this ever balloons: it ships in every binary.
	//
	// 24MB against a bundle of 17MB, and the headroom is deliberately narrow.
	// It was 30MB against 22MB when the bundle held fourteen files; one item per
	// shelf is much smaller, so the budget came down with it rather than being
	// left as slack for something to grow into. The audiobook is 13MB of the 17
	// and the only item big enough to matter.
	const budget = 24 << 20
	if bytes > budget {
		t.Errorf("bundle is %d bytes, over the %d budget", bytes, budget)
	}
	t.Logf("installed %d files, %.1f MB", files, float64(bytes)/(1<<20))
}

// A folder with anything in it is somebody's library, and not ours to add to.
func TestInstallSkipsFoldersThatHaveContent(t *testing.T) {
	root := t.TempDir()

	// Music already has something; everything else is empty.
	isEmpty := func(folder string) bool { return folder != "music" }

	installed, err := Install(root, isEmpty, testLog())
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	for _, u := range installed {
		if u.Folder == "music" {
			t.Error("wrote into a folder that already had content")
		}
	}
	if _, err := os.Stat(filepath.Join(root, "music")); !os.IsNotExist(err) {
		t.Error("music folder should have been left alone entirely")
	}
	if _, err := os.Stat(filepath.Join(root, "ebooks")); err != nil {
		t.Errorf("ebooks should still have been installed: %v", err)
	}
}

// Restarting the server must not duplicate anything, which is what the
// emptiness check buys us instead of a marker file.
func TestInstallIsIdempotentOnceFoldersHaveContent(t *testing.T) {
	root := t.TempDir()

	if _, err := Install(root, always, testLog()); err != nil {
		t.Fatalf("first Install: %v", err)
	}
	before := countFiles(t, root)

	// Second boot: the folders now have content, so nothing is empty.
	installed, err := Install(root, func(string) bool { return false }, testLog())
	if err != nil {
		t.Fatalf("second Install: %v", err)
	}
	if len(installed) != 0 {
		t.Errorf("second run installed %d folders again", len(installed))
	}
	if after := countFiles(t, root); after != before {
		t.Errorf("file count changed from %d to %d on restart", before, after)
	}
}

// Every bundled file must be something a backend will actually index, or it is
// dead weight in every binary we ship.
func TestBundleContainsOnlyPlayableMedia(t *testing.T) {
	root := t.TempDir()
	if _, err := Install(root, always, testLog()); err != nil {
		t.Fatalf("Install: %v", err)
	}

	allowed := map[string]bool{".mp3": true, ".epub": true}
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		ext := strings.ToLower(filepath.Ext(path))
		if !allowed[ext] {
			t.Errorf("unexpected file in the bundle: %s", path)
		}
		return nil
	})
}

// The credits are the half of this that actually teaches somebody something,
// so an entry without a source is worse than useless.
func TestAttributionsAreComplete(t *testing.T) {
	seen := map[string]bool{}
	for _, a := range Attributions() {
		if a.Folder == "" || a.Title == "" || a.Source == "" || a.License == "" {
			t.Errorf("incomplete attribution: %+v", a)
		}
		if !strings.HasPrefix(a.URL, "https://") {
			t.Errorf("attribution for %s has no usable link: %q", a.Folder, a.URL)
		}
		seen[a.Folder] = true
	}
	// Video is not bundled, but it is named - pointing somebody at free films
	// is the point even when we ship none.
	for _, want := range []string{"ebooks", "audiobooks", "music", "movies"} {
		if !seen[want] {
			t.Errorf("nothing credited for %s", want)
		}
	}
}

func countFiles(t *testing.T, root string) int {
	t.Helper()
	var n int
	_ = filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			n++
		}
		return nil
	})
	return n
}
