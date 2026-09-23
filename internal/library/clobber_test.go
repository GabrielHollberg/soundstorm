package library

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/GabrielHollberg/soundstorm/internal/media"
)

// Two uploads of one name at once both find the destination free, and a
// rename onto an existing name replaces it without a word. Placing a file
// must refuse instead - on a filesystem that can link, and one that cannot.
func TestPlacingAFileNeverReplacesOne(t *testing.T) {
	for _, canLink := range []bool{true, false} {
		dir := t.TempDir()
		dest := filepath.Join(dir, "01 Track.mp3")
		if err := os.WriteFile(dest, []byte("the first upload"), 0o666); err != nil {
			t.Fatal(err)
		}
		staged := filepath.Join(dir, "part-1")
		if err := os.WriteFile(staged, []byte("the second upload"), 0o666); err != nil {
			t.Fatal(err)
		}
		if !canLink {
			link = func(string, string) error { return &os.LinkError{Op: "link", Err: syscall.EPERM} }
		}
		err := placeFile(staged, dest)
		link = os.Link
		if !errors.Is(err, ErrAlreadyThere) {
			t.Errorf("canLink=%v: err = %v, want ErrAlreadyThere", canLink, err)
		}
		if got, _ := os.ReadFile(dest); string(got) != "the first upload" {
			t.Errorf("canLink=%v: the file already there became %q", canLink, got)
		}
	}
}

// And an ordinary placement still works both ways, leaving nothing staged.
func TestPlacingAFileMovesIt(t *testing.T) {
	for _, canLink := range []bool{true, false} {
		dir := t.TempDir()
		staged, dest := filepath.Join(dir, "part-1"), filepath.Join(dir, "a.mp3")
		os.WriteFile(staged, []byte("x"), 0o666)
		if !canLink {
			link = func(string, string) error { return &os.LinkError{Op: "link", Err: syscall.EPERM} }
		}
		err := placeFile(staged, dest)
		link = os.Link
		if err != nil {
			t.Fatalf("canLink=%v: %v", canLink, err)
		}
		if _, err := os.Stat(staged); !os.IsNotExist(err) {
			t.Errorf("canLink=%v: the staged file was left behind", canLink)
		}
	}
}

// The upload endpoint can be called without asking for a plan, so Save
// applies the plan's test itself: a page with a script in it is not media.
func TestSaveRefusesFilesNoShelfKeeps(t *testing.T) {
	l := newLibrary(t)
	for _, name := range []string{"Album/evil.html", "Album/x.svg", "Album/run.exe", "Album/noext"} {
		if _, err := l.Save(media.KindMusic, name, bytes.NewReader([]byte("<script>"))); err == nil {
			t.Errorf("%s was saved", name)
		}
	}
}

// Room refuses a size the disk cannot take while keeping its reserve.
func TestRoomKeepsTheReserve(t *testing.T) {
	l := newLibrary(t)
	if _, known := freeSpace(l.root); !known {
		t.Skip("free space is not known on this platform")
	}
	if l.Room(1 << 62) {
		t.Error("an upload bigger than any disk was allowed")
	}
	if !l.Room(1) || !l.Room(-1) {
		t.Error("a small or unknown size was refused")
	}
}
