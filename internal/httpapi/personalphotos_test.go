package httpapi

import (
	"path/filepath"
	"testing"
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
