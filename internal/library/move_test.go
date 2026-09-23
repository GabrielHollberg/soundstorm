package library

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GabrielHollberg/soundstorm/internal/media"
)

// Found testing the pictures shelf: a shelf mounted from a separate drive
// failed every upload, because the staging directory is on the library's drive
// and a rename cannot cross between filesystems. A test cannot mount a second
// drive, so it makes the first rename fail the way the kernel does.
func TestAnUploadToAShelfOnAnotherDriveLandsWhole(t *testing.T) {
	l := newLibrary(t)
	real := rename
	t.Cleanup(func() { rename = real })
	failures := 0
	rename = func(from, to string) error {
		if strings.Contains(from, ".uploads") {
			failures++
			return &os.LinkError{Op: "rename", Old: from, New: to, Err: crossDeviceErr()}
		}
		return real(from, to)
	}

	const body = "the whole photo"
	dest := saved(t, l, media.KindPicture, "Holiday/IMG_0001.jpg", []byte(body))
	if failures != 1 {
		t.Fatalf("the cross-drive rename was attempted %d times, want once", failures)
	}
	onDisk := filepath.Join(l.Root(), filepath.FromSlash(dest))
	got, err := os.ReadFile(onDisk)
	if err != nil || string(got) != body {
		t.Fatalf("at %s: %q, %v; want the whole file", dest, got, err)
	}

	// Nothing left behind: no temporary beside the photo, nothing in staging.
	entries, _ := os.ReadDir(filepath.Dir(onDisk))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".soundstorm-part") {
			t.Errorf("a temporary file was left beside the photo: %s", e.Name())
		}
	}
	if staged, _ := os.ReadDir(filepath.Join(l.Root(), ".uploads")); len(staged) != 0 {
		t.Errorf("staging still holds %d files", len(staged))
	}
}

// Any other failure is still a failure: copying is only for the one error
// that a copy can fix.
func TestOnlyACrossDriveFailureIsCopied(t *testing.T) {
	l := newLibrary(t)
	real := rename
	t.Cleanup(func() { rename = real })
	rename = func(from, to string) error {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: errors.New("permission denied")}
	}
	if _, err := l.Save(media.KindPicture, "IMG_0002.jpg", strings.NewReader("x")); err == nil {
		t.Error("a permission failure was papered over by copying")
	}
}

// crossDeviceErr is what this platform's rename returns across drives.
func crossDeviceErr() error { return errorNotSameDeviceForTest }
