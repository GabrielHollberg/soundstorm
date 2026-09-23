package library

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/GabrielHollberg/soundstorm/internal/media"
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

// plan is the common case: work it out, nothing answered.
func plan(l *Library, paths ...string) []Placement {
	placements, _ := l.Plan(paths, nil)
	return placements
}

// ask returns the questions a drop raises.
func ask(l *Library, paths ...string) []Question {
	_, questions := l.Plan(paths, nil)
	return questions
}

func TestFilesGoToTheShelfTheirNameImplies(t *testing.T) {
	l := newLibrary(t)

	cases := []struct {
		path string
		dest string
	}{
		// Music and audiobooks are filed under artist and album even when the
		// drop names neither: the shelf is never a flat pile of tracks.
		{"Myrrhman.flac", "music/Unknown Artist/Unknown Album/Myrrhman.flac"},
		{"Arrival (2016).mkv", "movies/Arrival (2016).mkv"},
		{"A Wizard of Earthsea.epub", "ebooks/A Wizard of Earthsea.epub"},
		{"Attention Is All You Need.pdf", "ebooks/Attention Is All You Need.pdf"},
		{"book.m4b", "audiobooks/Unknown Author/Unknown Title/book.m4b"},
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
	got := plan(l,
		"Arrival (2016)/Arrival (2016).mkv",
		"Arrival (2016)/Arrival (2016).en.srt",
		"Arrival (2016)/poster.jpg",
	)

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
	got := plan(l,
		"Laughing Stock/01 Myrrhman.flac",
		"Laughing Stock/cover.jpg",
		"Arrival (2016)/Arrival (2016).mkv",
		"Arrival (2016)/Arrival (2016).srt",
	)

	want := []string{
		// The album folder the drop gave is kept; only the missing artist
		// level is filled in, and the cover travels with its album.
		"music/Unknown Artist/Laughing Stock/01 Myrrhman.flac",
		"music/Unknown Artist/Laughing Stock/cover.jpg",
		"movies/Arrival (2016)/Arrival (2016).mkv",
		"movies/Arrival (2016)/Arrival (2016).srt",
	}
	for i, w := range want {
		if got[i].Dest != w {
			t.Errorf("%s -> %s, want %s", got[i].Path, got[i].Dest, w)
		}
	}
}

// An mp3 is a song or a chapter of a book and nothing in the file says which,
// so it is asked about rather than guessed at. Being wrong costs somebody
// moving files on disk; asking costs one click.
func TestAnMP3IsAskedAbout(t *testing.T) {
	l := newLibrary(t)

	questions := ask(l, "chapter01.mp3")
	if len(questions) != 1 {
		t.Fatalf("questions = %+v, want one", questions)
	}
	if len(questions[0].Options) != 2 {
		t.Errorf("options = %v", questions[0].Options)
	}

	placements, _ := l.Plan([]string{"chapter01.mp3"}, nil)
	if !placements[0].Waiting {
		t.Errorf("the file was placed at %q instead of waiting", placements[0].Dest)
	}
	if placements[0].Skipped {
		t.Error("an mp3 was skipped rather than asked about")
	}
}

// One question per dropped item, not per file. A thirty chapter audiobook is
// one decision.
func TestAThirtyPartBookIsOneQuestion(t *testing.T) {
	l := newLibrary(t)

	var paths []string
	for i := 1; i <= 30; i++ {
		paths = append(paths, fmt.Sprintf("Fabulas de Esopo/fabula_%02d.mp3", i))
	}
	questions := ask(l, paths...)
	if len(questions) != 1 {
		t.Fatalf("asked %d questions about one folder", len(questions))
	}
	if questions[0].Count != 30 {
		t.Errorf("the question covers %d files, want 30", questions[0].Count)
	}
	if questions[0].Label != "Fabulas de Esopo" {
		t.Errorf("label = %q; it should name what was dragged", questions[0].Label)
	}
}

// Answering settles the whole group.
func TestAnAnswerPlacesTheWholeGroup(t *testing.T) {
	l := newLibrary(t)
	paths := []string{"Esopo/one.mp3", "Esopo/two.mp3", "Esopo/cover.jpg"}

	placements, questions := l.Plan(paths, map[string]media.Kind{"Esopo": media.KindAudiobook})
	if len(questions) != 0 {
		t.Fatalf("still asking after an answer: %+v", questions)
	}
	for i, p := range placements {
		if p.Skipped || p.Waiting {
			t.Errorf("%s was not placed: %+v", paths[i], p)
			continue
		}
		// The folder the drop named is kept as the title; the missing
		// author level is filled in.
		if !strings.HasPrefix(p.Dest, "audiobooks/Unknown Author/Esopo/") {
			t.Errorf("%s -> %s", paths[i], p.Dest)
		}
	}
}

// A client must not be able to send a group somewhere the question never
// offered, or the choice is decoration.
func TestAnAnswerOutsideTheOptionsIsIgnored(t *testing.T) {
	l := newLibrary(t)
	_, questions := l.Plan([]string{"track.mp3"}, map[string]media.Kind{"track.mp3": media.KindVideo})
	if len(questions) != 1 {
		t.Errorf("an answer of \"video\" to a music-or-audiobook question was accepted")
	}
}

// Most audio is not ambiguous at all, and asking about every album drop would
// be worse than the occasional wrong guess.
func TestUnambiguousAudioIsNotAskedAbout(t *testing.T) {
	l := newLibrary(t)
	for _, name := range []string{"song.flac", "song.wav", "song.m4a", "song.aiff"} {
		if got := ask(l, name); len(got) != 0 {
			t.Errorf("%s raised a question", name)
		}
		if got := plan(l, name)[0]; got.Kind != media.KindMusic {
			t.Errorf("%s -> %q", name, got.Kind)
		}
	}
	// And the containers only audiobooks use stay decisive.
	if got := plan(l, "book.m4b")[0]; got.Kind != media.KindAudiobook {
		t.Errorf("m4b -> %q", got.Kind)
	}
	// A path that says audiobooks is believed without asking.
	if got := ask(l, "Audiobooks/James Allen/chapter01.mp3"); len(got) != 0 {
		t.Errorf("a folder saying audiobooks still raised a question: %+v", got)
	}
}

// A single film is a film. A folder of six videos with no episode numbering is
// a series somebody named badly, and six episodes in the film library is worth
// one question.
func TestAPileOfVideosIsAskedAbout(t *testing.T) {
	l := newLibrary(t)

	if got := ask(l, "Arrival (2016)/Arrival (2016).mkv"); len(got) != 0 {
		t.Errorf("one film raised a question: %+v", got)
	}
	if got := ask(l, "Boxset/a.mkv", "Boxset/b.mkv"); len(got) != 0 {
		t.Errorf("two films raised a question: %+v", got)
	}

	got := ask(l, "Show/a.mkv", "Show/b.mkv", "Show/c.mkv", "Show/d.mkv")
	if len(got) != 1 {
		t.Fatalf("four unnumbered videos raised %d questions", len(got))
	}
	if len(got[0].Options) != 2 {
		t.Errorf("options = %v, want films or tv", got[0].Options)
	}

	// Numbered episodes need no question at all.
	if got := ask(l, "Show/S01E01.mkv", "Show/S01E02.mkv", "Show/S01E03.mkv"); len(got) != 0 {
		t.Errorf("numbered episodes raised a question: %+v", got)
	}
}

// Answering a question does not mean anything at all is accepted into it.
func TestAnAnsweredGroupStillRefusesRubbish(t *testing.T) {
	l := newLibrary(t)
	placements, _ := l.Plan([]string{"Esopo/one.mp3", "Esopo/installer.exe"},
		map[string]media.Kind{"Esopo": media.KindAudiobook})
	if placements[1].Skipped != true {
		t.Errorf("an .exe was accepted into %s", placements[1].Dest)
	}
	if placements[0].Skipped {
		t.Error("the mp3 beside it was refused too")
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

	// A flac rather than an mp3: this is about the name, and an mp3 would be
	// waiting on a question rather than placed.
	got := plan(l, "  spaced out .flac  ")[0]
	if got.Skipped {
		t.Fatalf("a harmlessly spaced name was refused: %s", got.Reason)
	}
	if got.Dest != "music/Unknown Artist/Unknown Album/spaced out .flac" {
		t.Errorf("dest = %q", got.Dest)
	}

	if got := plan(l, "CON.flac")[0]; !got.Skipped {
		t.Errorf("a reserved Windows name was accepted as %q", got.Dest)
	}
	// Too long is shortened, not refused: see
	// TestAnAbsurdlyLongNameIsShortenedWithItsExtensionIntact.
	if got := plan(l, strings.Repeat("a", 300)+".flac")[0]; got.Skipped {
		t.Errorf("an over-long name was refused rather than shortened: %s", got.Reason)
	} else if name := path.Base(got.Dest); len(name) > maxSegment || !strings.HasSuffix(name, ".flac") {
		t.Errorf("shortened to %q", name)
	}
	if got := plan(l, "a/b/c/d/e/f/g/h/i/j/deep.flac")[0]; !got.Skipped {
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
	// The plan and the save agree: both apply the layout rule, and with no
	// readable tags in "not really a flac" both reach the same placeholder.
	if dest != "music/Unknown Artist/Laughing Stock/01 Myrrhman.flac" {
		t.Errorf("dest = %q", dest)
	}

	onDisk := filepath.Join(l.Root(), "music", "Unknown Artist", "Laughing Stock", "01 Myrrhman.flac")
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

	got, _ := os.ReadFile(filepath.Join(l.Root(), "music", "Unknown Artist", "Unknown Album", "track.mp3"))
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

// One video among many tracks is a bonus video, not a film.
//
// Any video used to win the decision outright, so a single .mp4 sitting beside
// eleven .flac files sent the whole album to the film library - tracks and all.
// The video travels with the album into music, where Navidrome ignores it and
// it stays beside the record it came with.
func TestAnAlbumWithABonusVideoStaysMusic(t *testing.T) {
	l := newLibrary(t)
	got := plan(l,
		"Radiohead/In Rainbows/01 15 Step.flac",
		"Radiohead/In Rainbows/02 Bodysnatchers.flac",
		"Radiohead/In Rainbows/03 Nude.flac",
		"Radiohead/In Rainbows/bonus-video.mp4",
	)
	if len(got) != 4 {
		t.Fatalf("placed %d of 4 files", len(got))
	}
	for _, p := range got {
		if !strings.HasPrefix(p.Dest, "music/") {
			t.Errorf("%s went to %s, want music/", p.Path, p.Dest)
		}
	}
}

// And the reverse must still hold: a film with more video than audio is a film.
func TestAFilmWithASampleTrackIsStillAFilm(t *testing.T) {
	l := newLibrary(t)
	got := plan(l,
		"Arrival (2016)/Arrival (2016).mkv",
		"Arrival (2016)/theme.mp3",
	)
	for _, p := range got {
		if !strings.HasPrefix(p.Dest, "movies/") {
			t.Errorf("%s went to %s, want movies/", p.Path, p.Dest)
		}
	}
}

// A season folder is still television, even with a stray audio file in it -
// unless the audio actually outnumbers the episodes, at which point it is not
// a season folder in any meaningful sense.
func TestASeasonFolderIsStillTelevision(t *testing.T) {
	l := newLibrary(t)
	got := plan(l,
		"Severance (2022)/Season 01/Severance - S01E01.mkv",
		"Severance (2022)/Season 01/Severance - S01E02.mkv",
		"Severance (2022)/Season 01/theme.mp3",
	)
	for _, p := range got {
		if !strings.HasPrefix(p.Dest, "tv/") {
			t.Errorf("%s went to %s, want tv/", p.Path, p.Dest)
		}
	}
}

// An album of mp3s with a bonus video is still the question it always was:
// mp3 is the one extension that is genuinely both.
func TestMP3AlbumWithAVideoStillAsks(t *testing.T) {
	l := newLibrary(t)
	questions := ask(l,
		"Some Artist/Some Album/01.mp3",
		"Some Artist/Some Album/02.mp3",
		"Some Artist/Some Album/bonus.mp4",
	)
	if len(questions) != 1 {
		t.Fatalf("got %d questions, want 1", len(questions))
	}
	want := map[media.Kind]bool{media.KindMusic: true, media.KindAudiobook: true}
	for _, opt := range questions[0].Options {
		if !want[opt] {
			t.Errorf("offered %s, want only music or audiobook", opt)
		}
	}
}

// The whole path, not just the lookup: Save is what a real upload calls, and it
// is what has to put the pdf beside the m4b.
func TestSaveFilesACompanionBesideItsAudio(t *testing.T) {
	root := t.TempDir()
	lib, err := Open(root, "./library", testLog())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	group := "The Way of Kings [B003ZWFO7E]"
	audio := id3(map[string]string{"TPE1": "Brandon Sanderson", "TALB": "The Way of Kings"})

	first, err := lib.Save(media.KindAudiobook, group+"/book.m4b", bytes.NewReader(audio))
	if err != nil {
		t.Fatalf("Save the audio: %v", err)
	}
	second, err := lib.Save(media.KindAudiobook, group+"/book.pdf", strings.NewReader("%PDF-1.4"))
	if err != nil {
		t.Fatalf("Save the companion: %v", err)
	}

	// The m4b carries an ID3 tag here rather than MP4 atoms, which internal/tags
	// reads happily - what matters is that one file has an author to give and the
	// other has none.
	wantDir := filepath.Dir(first)
	if got := filepath.Dir(second); got != wantDir {
		t.Errorf("the companion landed in %q, the audio in %q", got, wantDir)
	}
	if !strings.Contains(filepath.ToSlash(second), "Brandon Sanderson/") {
		t.Errorf("companion went to %q, expected it under the author", second)
	}
	if strings.Contains(filepath.ToSlash(second), "Unknown Author") {
		t.Errorf("companion went to %q, which is the bug this test exists for", second)
	}
}

// With no audio to follow, a companion still has to land somewhere findable
// rather than being refused.
func TestSaveStillPlacesACompanionWithNoAudioToFollow(t *testing.T) {
	root := t.TempDir()
	lib, err := Open(root, "./library", testLog())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	dest, err := lib.Save(media.KindAudiobook,
		"The Way of Kings [B003ZWFO7E]/book.pdf", strings.NewReader("%PDF-1.4"))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !strings.Contains(filepath.ToSlash(dest), "Unknown Author/") {
		t.Errorf("dest = %q, want the placeholder author", dest)
	}
}

// The error somebody actually saw was "write /library/.uploads/part-753272949:
// input/output error", ninety-one times, with the host drive at 0 bytes free.
// EIO is what Docker Desktop reports for a bind mount with no room left, and it
// reads like corruption - so the message has to carry the number that explains
// it. Measured rather than inferred from the errno, which is why this asserts
// the figure is present rather than what it says.
func TestAFailedWriteReportsTheRoomLeft(t *testing.T) {
	dir := t.TempDir()
	if _, ok := freeSpace(dir); !ok {
		t.Skip("free space is not measurable on this platform, and the message says less")
	}

	err := receiveError(dir, fmt.Errorf("input/output error"))
	if err == nil {
		t.Fatal("no error")
	}
	if !strings.Contains(err.Error(), "free") {
		t.Errorf("error does not mention free space: %v", err)
	}
	// The original has to survive, or the cause is lost.
	if !strings.Contains(err.Error(), "input/output error") {
		t.Errorf("error dropped the underlying cause: %v", err)
	}
}

func TestHumanBytesReadsLikeASentence(t *testing.T) {
	for _, c := range []struct {
		in   uint64
		want string
	}{
		{0, "0 bytes"},
		{512, "512 bytes"},
		{2 << 10, "2 KB"},
		{5 << 20, "5 MB"},
		{3 << 30, "3.0 GB"},
	} {
		if got := humanBytes(c.in); got != c.want {
			t.Errorf("humanBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A real Audible filename is title, subtitle and ASIN in one string, and the
// 120-character cap refused the long half of a 94-book drop. The evidence was
// the library itself: the longest name that had ever made it in was 118
// characters and nothing sat above it, which is a distribution with its tail
// cut off rather than a coincidence.
func TestALongAudiobookNameIsAcceptedNotRefused(t *testing.T) {
	long := "How to Fast_ Rediscover the Ancient Practice for Unlocking Physical, " +
		"Emotional, Spiritual and Communal Renewal in a World That Never Stops " +
		"Eating, Unabridged [B0DCZY2XYL].m4b"
	if len(long) <= 120 {
		t.Fatalf("the sample is only %d characters; it has to exceed the old cap", len(long))
	}

	got, err := cleanRelPath("Jentezen Franklin/Fasting [B0DCZY2XYL]/" + long)
	if err != nil {
		t.Fatalf("refused a real audiobook name: %v", err)
	}
	if !strings.HasSuffix(got, ".m4b") {
		t.Errorf("the extension did not survive: %q", got)
	}
	for _, seg := range strings.Split(got, "/") {
		if len(seg) > maxSegment {
			t.Errorf("segment is %d characters, over the %d cap: %q", len(seg), maxSegment, seg)
		}
	}
}

// Beyond the cap it is shortened rather than refused, and the extension is what
// survives: it decides the shelf and it is what every player dispatches on.
func TestAnAbsurdlyLongNameIsShortenedWithItsExtensionIntact(t *testing.T) {
	name := strings.Repeat("The Complete and Unabridged Edition ", 20) + ".m4b"
	got, err := cleanRelPath(name)
	if err != nil {
		t.Fatalf("cleanRelPath: %v", err)
	}
	if len(got) > maxSegment {
		t.Errorf("result is %d characters, over the %d cap", len(got), maxSegment)
	}
	if !strings.HasSuffix(got, ".m4b") {
		t.Errorf("lost the extension: %q", got)
	}
}

// Cutting mid-character would leave half a UTF-8 sequence, and these names are
// full of typographic quotes and accents - the library already holds one with a
// curly quote in it.
func TestShorteningCutsOnARuneBoundary(t *testing.T) {
	name := strings.Repeat("Alcoholics Anonymous \u201cBig Book\u201d ", 12) + ".m4b"
	got := shortenSegment(name)
	if !utf8.ValidString(got) {
		t.Errorf("shortened to invalid UTF-8: %q", got)
	}
	if len(got) > maxSegment {
		t.Errorf("result is %d characters, over the %d cap", len(got), maxSegment)
	}
}

// The reason has to be the actual reason. Every refusal used to read "that does
// not look like a file name", so somebody missing half their library could not
// tell a too-long name from an absolute path.
func TestASkippedFileSaysWhyItWasSkipped(t *testing.T) {
	root := t.TempDir()
	lib, err := Open(root, "./library", testLog())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	places, _ := lib.Plan([]string{
		"C:/Users/gabe/Music/track.mp3",
		"book/../../etc/passwd.m4b",
		"book/no-extension",
	}, nil)

	for _, want := range []struct{ path, contains string }{
		{"C:/Users/gabe/Music/track.mp3", "absolute"},
		{"book/../../etc/passwd.m4b", "climb"},
		{"book/no-extension", "extension"},
	} {
		var found *Placement
		for i := range places {
			if places[i].Path == want.path {
				found = &places[i]
			}
		}
		if found == nil {
			t.Errorf("no placement for %q", want.path)
			continue
		}
		if !found.Skipped {
			t.Errorf("%q was not skipped", want.path)
		}
		if !strings.Contains(found.Reason, want.contains) {
			t.Errorf("reason for %q is %q, expected it to mention %q",
				want.path, found.Reason, want.contains)
		}
	}
}
