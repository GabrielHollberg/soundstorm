package library

import (
	"fmt"
	"testing"

	"github.com/GabrielHollberg/soundstorm/internal/media"
)

func kindOf(t *testing.T, paths ...string) media.Kind {
	t.Helper()
	l := newLibrary(t)
	placements, questions := l.Plan(paths, nil)
	if len(questions) != 0 {
		t.Fatalf("%v: asked %+v", paths, questions)
	}
	return placements[0].Kind
}

// A folder of nothing but photos used to be a folder of nothing but
// companions, and was skipped: there was nowhere for it to go.
func TestAFolderOfPhotosIsPictures(t *testing.T) {
	if got := kindOf(t, "Holiday/IMG_4031.heic", "Holiday/IMG_4032.jpg"); got != media.KindPicture {
		t.Errorf("kind = %q", got)
	}
	if got := kindOf(t, "IMG_4031.jpg"); got != media.KindPicture {
		t.Errorf("a loose photo: kind = %q", got)
	}
}

// A camera roll holds clips as well as photos. When the photos plainly lead,
// the clips go with them rather than into the film library.
func TestACameraRollWithClipsIsPictures(t *testing.T) {
	var paths []string
	for i := 0; i < 8; i++ {
		paths = append(paths, fmt.Sprintf("Phone/IMG_%04d.jpg", i))
	}
	paths = append(paths, "Phone/IMG_0100.mov")
	if got := kindOf(t, paths...); got != media.KindPicture {
		t.Errorf("kind = %q", got)
	}
	// And a DCIM folder says so outright, whatever the counts.
	if got := kindOf(t, "DCIM/100APPLE/IMG_0001.mov", "DCIM/100APPLE/IMG_0002.jpg"); got != media.KindPicture {
		t.Errorf("DCIM: kind = %q", got)
	}
}

// Artwork is not photos. A film with its poster and a folder of fan art is a
// film; an album with its cover and booklet scans is an album.
func TestArtworkBesideMediaStaysArtwork(t *testing.T) {
	film := []string{"Arrival (2016)/Arrival (2016).mkv", "Arrival (2016)/poster.jpg", "Arrival (2016)/fanart.jpg"}
	for i := 0; i < 10; i++ {
		film = append(film, fmt.Sprintf("Arrival (2016)/extrafanart/fanart%d.jpg", i))
	}
	if got := kindOf(t, film...); got != media.KindVideo {
		t.Errorf("a film with fan art: kind = %q", got)
	}

	album := []string{"Laughing Stock/01 Myrrhman.flac", "Laughing Stock/cover.jpg"}
	for i := 0; i < 12; i++ {
		album = append(album, fmt.Sprintf("Laughing Stock/booklet-%02d.jpg", i))
	}
	if got := kindOf(t, album...); got != media.KindMusic {
		t.Errorf("an album with a scanned booklet: kind = %q", got)
	}

	// One or two stills next to a video are its extras, not a photo album.
	if got := kindOf(t, "Clip/clip.mp4", "Clip/still.jpg"); got != media.KindVideo {
		t.Errorf("a video and a still: kind = %q", got)
	}
}

// Photo albums are folders somebody made, and a picture has no author: the
// shelf keeps exactly the folders it was dropped in.
func TestPicturesKeepTheirFolders(t *testing.T) {
	l := newLibrary(t)
	placements, _ := l.Plan([]string{"2024 Holiday/Day 1/IMG_4031.heic"}, nil)
	if placements[0].Dest != "pictures/2024 Holiday/Day 1/IMG_4031.heic" {
		t.Errorf("dest = %q", placements[0].Dest)
	}
}

func TestArtworkNamesAreRecognised(t *testing.T) {
	for _, rel := range []string{"x/poster.jpg", "x/Arrival-fanart.jpg", "x/extrafanart/1.jpg", "x/folder.png"} {
		if !looksLikeArtwork(rel) {
			t.Errorf("%s not taken for artwork", rel)
		}
	}
	for _, rel := range []string{"x/IMG_4031.jpg", "x/beach.jpg", "x/postcard.jpg"} {
		if looksLikeArtwork(rel) {
			t.Errorf("%s taken for artwork", rel)
		}
	}
}
