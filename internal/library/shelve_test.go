package library

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GabrielHollberg/soundstorm/internal/media"
)

// id3 builds a minimal but real ID3v2.3 tag followed by a byte of "audio",
// so the reader is exercised on the same shape a tagger writes.
func id3(frames map[string]string) []byte {
	var body []byte
	for id, value := range frames {
		payload := append([]byte{3}, []byte(value)...) // 3 = UTF-8
		size := len(payload)
		body = append(body, []byte(id)...)
		body = append(body, byte(size>>24), byte(size>>16), byte(size>>8), byte(size))
		body = append(body, 0, 0)
		body = append(body, payload...)
	}
	n := len(body)
	head := []byte{'I', 'D', '3', 3, 0, 0,
		byte(n >> 21 & 0x7F), byte(n >> 14 & 0x7F), byte(n >> 7 & 0x7F), byte(n & 0x7F)}
	return append(append(head, body...), 0xFF, 0xFB, 0x90, 0x00)
}

func saved(t *testing.T, l *Library, kind media.Kind, rel string, content []byte) string {
	t.Helper()
	dest, err := l.Save(kind, rel, bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Save(%s): %v", rel, err)
	}
	return dest
}

// A loose track must not sit at the top of the music shelf. The folders are
// the part of this a person navigates by hand, and one file at a time is
// exactly how that turns into a mess.
func TestALooseTrackIsFiledByItsTags(t *testing.T) {
	l := newLibrary(t)
	dest := saved(t, l, media.KindMusic, "01 15 Step.mp3",
		id3(map[string]string{"TPE1": "Radiohead", "TALB": "In Rainbows"}))

	if dest != "music/Radiohead/In Rainbows/01 15 Step.mp3" {
		t.Errorf("filed at %q", dest)
	}
	if _, err := os.Stat(filepath.Join(l.PathFor(media.KindMusic),
		"Radiohead", "In Rainbows", "01 15 Step.mp3")); err != nil {
		t.Errorf("the file is not where the answer said: %v", err)
	}
}

// The album artist wins, or a compilation scatters across a folder per track.
func TestACompilationFilesUnderItsAlbumArtist(t *testing.T) {
	l := newLibrary(t)
	dest := saved(t, l, media.KindMusic, "04 Song.mp3", id3(map[string]string{
		"TPE1": "Some Guest Singer",
		"TPE2": "Various Artists",
		"TALB": "Now That's What I Call Music",
	}))
	if !strings.HasPrefix(dest, "music/Various Artists/") {
		t.Errorf("filed at %q, want it under the album artist", dest)
	}
}

// Somebody who dropped a structured folder has said where it goes more
// reliably than a tag will. Do not second-guess them.
func TestAnAlreadyStructuredDropIsLeftAlone(t *testing.T) {
	l := newLibrary(t)
	dest := saved(t, l, media.KindMusic, "Radiohead/In Rainbows/01 15 Step.mp3",
		id3(map[string]string{"TPE1": "Somebody Else", "TALB": "A Different Album"}))

	if dest != "music/Radiohead/In Rainbows/01 15 Step.mp3" {
		t.Errorf("a structured drop was rewritten to %q", dest)
	}
}

// A file that says nothing about itself still has to land somewhere findable.
func TestAnUntaggedTrackGetsPlaceholders(t *testing.T) {
	l := newLibrary(t)
	dest := saved(t, l, media.KindMusic, "mystery.mp3", []byte("not audio at all"))
	if dest != "music/Unknown Artist/Unknown Album/mystery.mp3" {
		t.Errorf("filed at %q", dest)
	}
}

// Real albums are called things like "AC/DC Live". A tag is somebody else's
// text, and it must not become a directory separator or fail the upload.
func TestTagsThatLookLikePathsAreMadeSafe(t *testing.T) {
	l := newLibrary(t)
	dest := saved(t, l, media.KindMusic, "01.mp3", id3(map[string]string{
		"TPE1": "AC/DC",
		"TALB": "../../etc/passwd",
	}))
	// What matters is that no segment *is* "..", not that the text contains
	// two dots - "..-..-etc-passwd" is one harmless folder name, and an album
	// really could be called "...And Justice For All".
	for _, segment := range strings.Split(dest, "/") {
		if strings.Trim(segment, ".") == "" {
			t.Fatalf("a tag became a traversal segment: %q", dest)
		}
	}
	if !strings.HasPrefix(dest, "music/AC-DC/") {
		t.Errorf("filed at %q, want the slash replaced rather than refused", dest)
	}
	// And it really is inside the shelf, not merely named as though it were.
	abs := filepath.Join(l.PathFor(media.KindMusic), filepath.FromSlash(strings.TrimPrefix(dest, "music/")))
	if !within(l.PathFor(media.KindMusic), abs) {
		t.Errorf("%s is outside the music folder", abs)
	}
}

// Audiobookshelf reads Author/Title from the path, so the same mechanism
// applies with different words for the fields.
func TestALooseAudiobookPartIsFiledByAuthorAndTitle(t *testing.T) {
	l := newLibrary(t)
	dest := saved(t, l, media.KindAudiobook, "chapter01.mp3", id3(map[string]string{
		"TPE1": "Ursula K. Le Guin",
		"TALB": "A Wizard of Earthsea",
	}))
	if dest != "audiobooks/Ursula K. Le Guin/A Wizard of Earthsea/chapter01.mp3" {
		t.Errorf("filed at %q", dest)
	}
}

// Films and television are not restructured: Jellyfin matches on the name,
// not the depth, and a folder dropped there is already the right shape.
func TestVideoIsNotRestructured(t *testing.T) {
	l := newLibrary(t)
	dest := saved(t, l, media.KindVideo, "Arrival (2016).mkv", []byte("not really a film"))
	if dest != "movies/Arrival (2016).mkv" {
		t.Errorf("a film was moved to %q", dest)
	}
}

// An Audible audiobook arrives as an .m4b beside a companion .pdf of the same
// name. The m4b carries tags and the pdf carries none, so reading each file's
// own metadata filed one book under two authors:
//
//	Brandon Sanderson/The Way of Kings [B003ZWFO7E]/....m4b
//	Unknown Author/The Way of Kings [B003ZWFO7E]/....pdf
//
// Reported from a real drop of 94 audiobooks, where every single entry under
// "Unknown Author" was a companion PDF.
func TestACompanionJoinsTheFolderItsGroupAlreadyMade(t *testing.T) {
	root := t.TempDir()
	lib, err := Open(root, "./library", testLog())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	shelf := lib.PathFor(media.KindAudiobook)

	// The audio file went first and named the author, which is what the client
	// now guarantees by sending taggable files before the rest of their group.
	group := "The Way of Kings [B003ZWFO7E]"
	if err := os.MkdirAll(filepath.Join(shelf, "Brandon Sanderson", group), 0o777); err != nil {
		t.Fatal(err)
	}

	got, ok := groupFolder(media.KindAudiobook, group+"/The Way of Kings.pdf", shelf)
	if !ok {
		t.Fatal("the companion found no folder to join")
	}
	want := "Brandon Sanderson/" + group + "/The Way of Kings.pdf"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Every Audible folder is named "Title [ASIN]", and to filepath.Glob a bracketed
// run is a character class - so a Glob-based lookup would match a directory
// called "The Way of Kings B" and never the real one. This is the test that
// would have caught that.
func TestGroupLookupTreatsBracketsAsLiteralText(t *testing.T) {
	root := t.TempDir()
	lib, err := Open(root, "./library", testLog())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	shelf := lib.PathFor(media.KindAudiobook)

	group := "The Way of Kings [B003ZWFO7E]"
	// A decoy that a character class would match and a literal comparison will
	// not. If the lookup ever goes back to globbing, this directory is what it
	// finds instead.
	for _, dir := range []string{
		filepath.Join("Brandon Sanderson", group),
		filepath.Join("Somebody Else", "The Way of Kings B"),
	} {
		if err := os.MkdirAll(filepath.Join(shelf, dir), 0o777); err != nil {
			t.Fatal(err)
		}
	}

	got, ok := groupFolder(media.KindAudiobook, group+"/notes.pdf", shelf)
	if !ok || got != "Brandon Sanderson/"+group+"/notes.pdf" {
		t.Errorf("got %q (ok=%v), want the bracketed folder", got, ok)
	}
}

// Two authors already holding the same book is a mess somebody has to resolve,
// and joining one of them at random would make it permanent.
func TestACompanionWillNotChooseBetweenTwoFolders(t *testing.T) {
	root := t.TempDir()
	lib, err := Open(root, "./library", testLog())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	shelf := lib.PathFor(media.KindAudiobook)

	group := "The Way of Kings [B003ZWFO7E]"
	for _, author := range []string{"Brandon Sanderson", "Unknown Author"} {
		if err := os.MkdirAll(filepath.Join(shelf, author, group), 0o777); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := groupFolder(media.KindAudiobook, group+"/notes.pdf", shelf); ok {
		t.Error("it picked one of two equally good answers")
	}
}

// A taggable file must keep deciding for itself, or the first book to land under
// Unknown Author would drag every later one in beside it.
func TestATaggableFileIgnoresWhatIsAlreadyThere(t *testing.T) {
	for _, name := range []string{"book.m4b", "01 track.mp3", "x.flac", "y.m4a"} {
		if !taggable(name) {
			t.Errorf("%s should be taggable", name)
		}
	}
	for _, name := range []string{"notes.pdf", "cover.jpg", "info.nfo", "book.epub"} {
		if taggable(name) {
			t.Errorf("%s should not be taggable", name)
		}
	}
}
