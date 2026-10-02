package httpapi

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/GabrielHollberg/soundstorm/internal/library"
	"github.com/GabrielHollberg/soundstorm/internal/media"
)

// Only the folders SoundStorm sorts photos into count as already kept: a
// photo in a folder somebody arranged themselves does not stop a copy being
// filed by date.
func TestOnlyDatedFoldersCountAsKept(t *testing.T) {
	personal := filepath.Join("lib", "pictures", "Personal", "bob")
	cases := map[string]bool{
		"2019/07/IMG_1.jpg":        true,
		"Undated/IMG_2.jpg":        true,
		"Vacation/IMG_1.jpg":       false,
		"2019/IMG_1.jpg":           false,
		"Family/2019/07/IMG_1.jpg": false,
		"IMG_3.jpg":                false,
	}
	for rel, want := range cases {
		if got := managedPhoto(personal, filepath.Join(personal, filepath.FromSlash(rel))); got != want {
			t.Errorf("%s: %v, want %v", rel, got, want)
		}
	}
}

// A different photo whose name is taken that month is kept as "Name (2)",
// the next free; and a photo sent again is found under any of those names
// by its size, so a phone's backup does not file it twice.
func TestATakenPhotoNameIsNumbered(t *testing.T) {
	lib, err := library.Open(t.TempDir(), "./library", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{library: lib}
	rel, err := lib.EnsurePersonalFolder("bob")
	if err != nil {
		t.Fatal(err)
	}
	month := filepath.Join(lib.PathFor(media.KindPicture), filepath.FromSlash(rel), "2024", "05")
	if err := os.MkdirAll(month, 0o777); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(month, "IMG_0001.jpg"), []byte("first"), 0o666)
	if free, there := s.freePersonalName("bob", "2024/05/IMG_0001.jpg", 6); there || free != "2024/05/IMG_0001 (2).jpg" {
		t.Fatalf("a different photo: %q %v", free, there)
	}
	os.WriteFile(filepath.Join(month, "IMG_0001 (2).jpg"), []byte("second"), 0o666)
	if _, there := s.freePersonalName("bob", "2024/05/IMG_0001.jpg", 6); !there {
		t.Fatal("the second photo, sent again, should be found as (2)")
	}
	if free, _ := s.freePersonalName("bob", "2024/05/IMG_0001.jpg", 0); free != "2024/05/IMG_0001 (3).jpg" {
		t.Fatalf("the next free name: %q", free)
	}
	if free, there := s.freePersonalName("bob", "2024/05/IMG_0002.jpg", 4); there || free != "2024/05/IMG_0002.jpg" {
		t.Fatalf("a free name stays: %q %v", free, there)
	}
}
