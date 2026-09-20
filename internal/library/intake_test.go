package library

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gabehollberg/soundstorm/internal/media"
)

func newLibrary(t *testing.T) *Library {
	t.Helper()
	l, err := Open(filepath.Join(t.TempDir(), "library"), "./library",
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return l
}

// plan is the common case: nothing forced, work it out.
func plan(l *Library, paths ...string) []Placement {
	return l.Plan(paths, "")
}

func TestFilesGoToTheShelfTheirNameImplies(t *testing.T) {
	l := newLibrary(t)

	cases := []struct {
		path string
		dest string
	}{
		{"Myrrhman.flac", "music/Myrrhman.flac"},
		{"Arrival (2016).mkv", "movies/Arrival (2016).mkv"},
		{"A Wizard of Earthsea.epub", "ebooks/A Wizard of Earthsea.epub"},
		{"Attention Is All You Need.pdf", "ebooks/Attention Is All You Need.pdf"},
		{"book.m4b", "audiobooks/book.m4b"},
		// Television, in both of the forms people actually name it.
		{"Severance - S01E01.mkv", "tv/Severance - S01E01.mkv"},
		{"The Wire 1x02.avi", "tv/The Wire 1x02.avi"},
	}
	for _, c := range cases {
		got := plan(l, c.path)[0]
		if got.Skipped {
			t.Errorf("%s was skipped: %s", c.path, got.Reason)
			continue
		}
		if got.Dest != c.dest {
			t.Errorf("%s -> %s, want %s", c.path, got.Dest, c.dest)
		}
	}
}

// A season folder says television even when no file name does.
func TestASeasonFolderMakesItTelevision(t *testing.T) {
	l := newLibrary(t)
	got := plan(l, "Severance/Season 01/pilot.mkv")[0]
	if got.Kind != media.KindTV {
		t.Errorf("kind = %q, want tv", got.Kind)
	}
	if got.Dest != "tv/Severance/Season 01/pilot.mkv" {
		t.Errorf("dest = %q: the folder structure was not kept", got.Dest)
	}
}

// The reason a placement is decided per dropped item rather than per file. A
// subtitle in the ebook shelf is useless, and anywhere but beside its film it
// is invisible.
func TestSidecarsFollowTheirMedia(t *testing.T) {
	l := newLibrary(t)
	got := l.Plan([]string{
		"Arrival (2016)/Arrival (2016).mkv",
		"Arrival (2016)/Arrival (2016).en.srt",
		"Arrival (2016)/poster.jpg",
	}, "")

	for _, p := range got {
		if p.Skipped {
			t.Errorf("%s was skipped: %s", p.Path, p.Reason)
			continue
		}
		if !strings.HasPrefix(p.Dest, "movies/Arrival (2016)/") {
			t.Errorf("%s went to %s", p.Path, p.Dest)
		}
	}
}

// A companion with nothing to accompany belongs nowhere, and saying so is
// better than inventing a shelf for it.
func TestALoneSubtitleIsSkipped(t *testing.T) {
	l := newLibrary(t)
	got := plan(l, "orphan.srt")[0]
	if !got.Skipped {
		t.Errorf("a lone subtitle was placed in %s", got.Dest)
	}
}

// Each top-level item gets its own answer, or dropping an album and a film
// together would put one of them in the wrong place.
func TestEachDroppedItemIsDecidedSeparately(t *testing.T) {
	l := newLibrary(t)
	got := l.Plan([]string{
		"Laughing Stock/01 Myrrhman.flac",
		"Laughing Stock/cover.jpg",
		"Arrival (2016)/Arrival (2016).mkv",
		"Arrival (2016)/Arrival (2016).srt",
	}, "")

	want := []string{
		"music/Laughing Stock/01 Myrrhman.flac",
		"music/Laughing Stock/cover.jpg",
		"movies/Arrival (2016)/Arrival (2016).mkv",
		"movies/Arrival (2016)/Arrival (2016).srt",
	}
	for i, w := range want {
		if got[i].Dest != w {
			t.Errorf("%s -> %s, want %s", got[i].Path, got[i].Dest, w)
		}
	}
}

// An mp3 is a song or a chapter of a book and nothing in the file says which.
// Music is the default; a folder that says otherwise is believed; and dropping
// onto a shelf settles it outright.
func TestAmbiguousAudioDefaultsToMusicAndCanBeOverridden(t *testing.T) {
	l := newLibrary(t)

	if got := plan(l, "track.mp3")[0]; got.Kind != media.KindMusic {
		t.Errorf("a loose mp3 went to %q", got.Kind)
	}
	if got := plan(l, "Audiobooks/James Allen/chapter01.mp3")[0]; got.Kind != media.KindAudiobook {
		t.Errorf("an mp3 under a folder saying audiobooks went to %q", got.Kind)
	}

	forced := l.Plan([]string{"chapter01.mp3"}, media.KindAudiobook)[0]
	if forced.Dest != "audiobooks/chapter01.mp3" {
		t.Errorf("dropping onto a shelf gave %s", forced.Dest)
	}
}

// Choosing a shelf does not mean anything at all is accepted into it.
func TestAChosenShelfStillRefusesRubbish(t *testing.T) {
	l := newLibrary(t)
	got := l.Plan([]string{"installer.exe"}, media.KindMusic)[0]
	if !got.Skipped {
		t.Errorf("an .exe was accepted into %s", got.Dest)
	}
}

// Every one of these is a browser-supplied string, which is to say
// attacker-supplied.
func TestPathsCannotEscapeTheLibrary(t *testing.T) {
	l := newLibrary(t)
	for _, nasty := range []string{
		"../../../etc/passwd.mp3",
		"..\\..\\windows\\system32\\evil.mp3",
		"/etc/cron.d/evil.mp3",
		"C:\\Windows\\evil.mp3",
		"music/../../escape.mp3",
		"\x00/etc/passwd.mp3",
		"....//....//escape.mp3",
	} {
		got := plan(l, nasty)[0]
		if !got.Skipped && strings.Contains(got.Dest, "..") {
			t.Errorf("%q produced %q", nasty, got.Dest)
		}
		// And Save must refuse it too, whatever Plan decided.
		if _, err := l.Save(media.KindMusic, nasty, strings.NewReader("x")); err == nil {
			t.Errorf("Save accepted %q", nasty)
		}
	}

	// Nothing was written outside the library, which is the claim that matters.
	root := l.Root()
	var strays []string
	_ = filepath.Walk(filepath.Dir(root), func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasPrefix(p, root) {
			strays = append(strays, p)
		}
		return nil
	})
	if len(strays) > 0 {
		t.Errorf("files appeared outside the library: %v", strays)
	}
}

// Windows swallows trailing dots and spaces, so a name has to be normalised
// before it is checked rather than after.
func TestAwkwardNamesAreNormalisedOrRefused(t *testing.T) {
	l := newLibrary(t)

	got := plan(l, "  spaced out .mp3  ")[0]
	if got.Skipped {
		t.Fatalf("a harmlessly spaced name was refused: %s", got.Reason)
	}
	if got.Dest != "music/spaced out .mp3" {
		t.Errorf("dest = %q", got.Dest)
	}

	if got := plan(l, "CON.mp3")[0]; !got.Skipped {
		t.Errorf("a reserved Windows name was accepted as %q", got.Dest)
	}
	if got := plan(l, strings.Repeat("a", 300)+".mp3")[0]; !got.Skipped {
		t.Error("an absurdly long name was accepted")
	}
	if got := plan(l, "a/b/c/d/e/f/g/h/i/j/deep.mp3")[0]; !got.Skipped {
		t.Error("an absurdly deep path was accepted")
	}
}

func TestSaveWritesTheFileWhereThePlanSaid(t *testing.T) {
	l := newLibrary(t)
	const body = "not really a flac"

	dest, err := l.Save(media.KindMusic, "Laughing Stock/01 Myrrhman.flac", strings.NewReader(body))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if dest != "music/Laughing Stock/01 Myrrhman.flac" {
		t.Errorf("dest = %q", dest)
	}

	onDisk := filepath.Join(l.Root(), "music", "Laughing Stock", "01 Myrrhman.flac")
	got, err := os.ReadFile(onDisk)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != body {
		t.Errorf("content = %q", got)
	}

	// The count the UI shows has to move, or a file appears to have vanished.
	for _, f := range l.Folders() {
		if f.Kind == media.KindMusic && f.Files != 1 {
			t.Errorf("music reports %d files after an upload", f.Files)
		}
	}
}

// Dropping the same album twice is a mistake far more often than it is a
// request for a second copy.
func TestSaveRefusesToOverwrite(t *testing.T) {
	l := newLibrary(t)
	if _, err := l.Save(media.KindMusic, "track.mp3", strings.NewReader("first")); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Save(media.KindMusic, "track.mp3", strings.NewReader("second")); err == nil {
		t.Fatal("the second upload overwrote the first")
	}

	got, _ := os.ReadFile(filepath.Join(l.Root(), "music", "track.mp3"))
	if string(got) != "first" {
		t.Errorf("the original was changed to %q", got)
	}
}

// Navidrome and Audiobookshelf watch these folders. A half-written file is
// exactly what a scanner indexes as a corrupt track, so nothing may appear at
// its destination until all of it is there.
func TestAFailedUploadLeavesNothingBehind(t *testing.T) {
	l := newLibrary(t)

	_, err := l.Save(media.KindMusic, "broken.mp3", &failingReader{after: 4})
	if err == nil {
		t.Fatal("a broken upload reported success")
	}
	if _, err := os.Stat(filepath.Join(l.Root(), "music", "broken.mp3")); !os.IsNotExist(err) {
		t.Error("a partial file was left at the destination")
	}

	staging, _ := os.ReadDir(filepath.Join(l.Root(), ".uploads"))
	for _, e := range staging {
		if strings.HasPrefix(e.Name(), "part-") {
			t.Errorf("a staging file was left behind: %s", e.Name())
		}
	}
}

// Staging is inside the library root so the rename is atomic, and hidden at
// the top level so no backend has it mounted.
func TestStagingIsSweptOnStartup(t *testing.T) {
	l := newLibrary(t)
	staging := filepath.Join(l.Root(), ".uploads")
	if err := os.MkdirAll(staging, 0o777); err != nil {
		t.Fatal(err)
	}
	leftover := filepath.Join(staging, "part-fromacrash")
	if err := os.WriteFile(leftover, []byte("half a film"), 0o666); err != nil {
		t.Fatal(err)
	}

	l.ClearStaging()
	if _, err := os.Stat(leftover); !os.IsNotExist(err) {
		t.Error("an interrupted upload was still taking up space")
	}
}

type failingReader struct {
	after int
	read  int
}

func (r *failingReader) Read(p []byte) (int, error) {
	if r.read >= r.after {
		return 0, io.ErrUnexpectedEOF
	}
	n := copy(p, "abcd")
	r.read += n
	return n, nil
}
