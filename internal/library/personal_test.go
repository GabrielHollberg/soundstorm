package library

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/GabrielHollberg/soundstorm/internal/media"
)

// A person's photos go to their own folder, whatever their name or the path
// they drop, and never outside it.
func TestPersonalPathsStayInTheFolder(t *testing.T) {
	cases := map[[2]string]string{
		{"alice", "Holiday/beach.jpg"}:    "Personal/alice/Holiday/beach.jpg",
		{"AC/DC", "2025/07/IMG_0001.JPG"}: "Personal/AC-DC/2025/07/IMG_0001.JPG",
	}
	for in, want := range cases {
		got, err := PersonalPath(in[0], in[1])
		if err != nil || got != want {
			t.Errorf("PersonalPath(%q, %q) = %q, %v; want %q", in[0], in[1], got, err, want)
		}
	}
	if got, err := PersonalPath("alice", "../../bob/x.jpg"); err == nil {
		t.Errorf("a path out of the folder was taken: %q", got)
	}
	if !IsPersonalPath("Personal/alice/x.jpg") || IsPersonalPath("Holiday/x.jpg") {
		t.Error("IsPersonalPath")
	}
}

// What a phone asks about: there, of that size, in the person's folder.
func TestPersonalHasAndUsage(t *testing.T) {
	root := t.TempDir()
	l, err := Open(root, root, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	rel, err := l.EnsurePersonalFolder("alice")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(l.PathFor(media.KindPicture), filepath.FromSlash(rel), "2025", "07")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "IMG_0001.JPG"), make([]byte, 1234), 0o644); err != nil {
		t.Fatal(err)
	}
	if !l.PersonalHas("alice", "2025/07/IMG_0001.JPG", 1234) || l.PersonalHas("alice", "2025/07/IMG_0001.JPG", 99) || l.PersonalHas("bob", "2025/07/IMG_0001.JPG", 1234) {
		t.Error("PersonalHas")
	}
	if n := l.PersonalUsage("alice"); n != 1234 {
		t.Errorf("usage = %d, want 1234", n)
	}
}

// A video named as a camera or phone names it is a clip for the photos, even
// dropped alone; a film's name is never like that.
func TestCameraClipsGoToPictures(t *testing.T) {
	cases := map[string]media.Kind{
		"MVI_0002.MOV":               media.KindPicture,
		"VID_20190705_100000.mp4":    media.KindPicture,
		"PXL_20230315_101500123.mp4": media.KindPicture,
		"20180101_120000.mp4":        media.KindPicture,
		"VID-20190705-WA0001.mp4":    media.KindPicture,
		"Arrival (2016).mkv":         media.KindVideo,
		"The Matrix.mp4":             media.KindVideo,
	}
	for name, want := range cases {
		got, _ := decideGroup([]string{name}, []int{0})
		if got != want {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
}
