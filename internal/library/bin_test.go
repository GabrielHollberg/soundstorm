package library

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/media"
)

func newBinLibrary(t *testing.T) *Library {
	t.Helper()
	l, err := Open(t.TempDir(), "./library", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return l
}

// put writes files under the library root, slash-separated.
func put(t *testing.T, l *Library, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		p := filepath.Join(l.Root(), filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o777); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x "+rel), 0o666); err != nil {
			t.Fatal(err)
		}
	}
}

func exists(l *Library, rel string) bool {
	_, err := os.Stat(filepath.Join(l.Root(), filepath.FromSlash(rel)))
	return err == nil
}

func TestResolveTakesWhatBelongsWithAnItem(t *testing.T) {
	l := newBinLibrary(t)
	put(t, l,
		"movies/Dune (2021)/Dune.mkv",
		"movies/Dune (2021)/Dune.en.srt",
		"movies/Dune (2021)/poster.jpg",
		"tv/Show/Season 01/Show S01E01.mkv",
		"tv/Show/Season 01/Show S01E01.en.srt",
		"tv/Show/Season 01/Show S01E02.mkv",
		"tv/Show/Season 01/Show S01E02.en.srt",
		"music/Artist/Album/01 One.mp3",
		"music/Artist/Album/02 Two.mp3",
		"music/Artist/Album/cover.jpg",
		"music/Solo/Single/01 Only.mp3",
		"music/Solo/Single/cover.jpg",
		"movies/Loose.mkv",
		"movies/Loose.srt",
		"movies/Loose Two.mkv",
	)
	cases := []struct {
		name string
		kind media.Kind
		rel  string
		want []string
	}{
		{"a film takes its folder, subtitles and poster included",
			media.KindVideo, "Dune (2021)/Dune.mkv", []string{"movies/Dune (2021)"}},
		{"an episode takes its own subtitle and leaves its season",
			media.KindTV, "Show/Season 01/Show S01E01.mkv",
			[]string{"tv/Show/Season 01/Show S01E01.en.srt", "tv/Show/Season 01/Show S01E01.mkv"}},
		{"a track with others beside it goes alone",
			media.KindMusic, "Artist/Album/01 One.mp3", []string{"music/Artist/Album/01 One.mp3"}},
		{"the last track of an album takes the album's cover with it",
			media.KindMusic, "Solo/Single/01 Only.mp3", []string{"music/Solo/Single"}},
		{"a loose film takes its own subtitle and not a film that starts the same",
			media.KindVideo, "Loose.mkv", []string{"movies/Loose.mkv", "movies/Loose.srt"}},
		{"a folder goes whole",
			media.KindTV, "Show", []string{"tv/Show"}},
	}
	for _, c := range cases {
		got, err := l.Resolve(c.kind, []string{c.rel})
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

// A backend's answer is somebody else's text. None of these may name anything
// outside the shelf, or the shelf itself.
func TestResolveRefusesAnythingOutsideTheShelf(t *testing.T) {
	l := newBinLibrary(t)
	put(t, l, "music/a.mp3", "ebooks/book.epub", "secret.txt")
	for _, rel := range []string{
		"../ebooks/book.epub", "../secret.txt", "/etc/passwd", "", ".", "a/../../secret.txt",
	} {
		if got, err := l.Resolve(media.KindMusic, []string{rel}); err == nil {
			t.Errorf("Resolve(%q) = %q, want a refusal", rel, got)
		}
	}
	if exists(l, "secret.txt") == false {
		t.Fatal("the test file went missing")
	}
}

func TestBinMovesAndUndoPutsBack(t *testing.T) {
	l := newBinLibrary(t)
	put(t, l, "movies/Dune (2021)/Dune.mkv", "movies/Dune (2021)/Dune.en.srt", "movies/Other/Other.mkv")

	paths, err := l.Resolve(media.KindVideo, []string{"Dune (2021)/Dune.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := l.MoveToBin([]BinItem{{Title: "Dune", Kind: media.KindVideo, Paths: paths}}, "gabriel")
	if err != nil {
		t.Fatalf("MoveToBin: %v", err)
	}
	if entry.Files != 2 {
		t.Errorf("counted %d files, want 2", entry.Files)
	}
	if exists(l, "movies/Dune (2021)") {
		t.Error("the film is still on the shelf")
	}
	if !exists(l, "movies/Other/Other.mkv") {
		t.Error("a film nobody deleted went too")
	}
	if !exists(l, ".trash/"+entry.ID+"/files/movies/Dune (2021)/Dune.mkv") {
		t.Error("the film is not in the bin")
	}

	_, restored, blocked, err := l.Restore(entry.ID)
	if err != nil || restored != 1 || len(blocked) != 0 {
		t.Fatalf("Restore = %d, %v, %v", restored, blocked, err)
	}
	if !exists(l, "movies/Dune (2021)/Dune.en.srt") {
		t.Error("undo did not bring the subtitle back")
	}
	if exists(l, ".trash/"+entry.ID) {
		t.Error("an emptied bin entry was left behind")
	}
	if _, _, _, err := l.Restore(entry.ID); err != ErrNotInBin {
		t.Errorf("a second undo = %v, want ErrNotInBin", err)
	}
}

// Somebody uploaded the same film again after deleting it: undo must not
// overwrite the new copy, and must not lose the old one either.
func TestUndoNeverOverwrites(t *testing.T) {
	l := newBinLibrary(t)
	put(t, l, "music/A/B/01.mp3", "music/A/B/02.mp3")
	paths, _ := l.Resolve(media.KindMusic, []string{"A/B/01.mp3"})
	entry, err := l.MoveToBin([]BinItem{{Title: "01", Kind: media.KindMusic, Paths: paths}}, "g")
	if err != nil {
		t.Fatal(err)
	}
	put(t, l, "music/A/B/01.mp3") // arrived again

	_, restored, blocked, err := l.Restore(entry.ID)
	if err != nil || restored != 0 || len(blocked) != 1 {
		t.Fatalf("Restore = %d, %v, %v; want it blocked", restored, blocked, err)
	}
	if !exists(l, ".trash/"+entry.ID+"/files/music/A/B/01.mp3") {
		t.Error("the binned copy was lost")
	}
}

func TestDeletingAnArtistsOnlyAlbumTidiesTheArtistFolder(t *testing.T) {
	l := newBinLibrary(t)
	put(t, l, "music/Solo/Single/01 Only.mp3", "music/Solo/Single/cover.jpg")
	paths, _ := l.Resolve(media.KindMusic, []string{"Solo/Single/01 Only.mp3"})
	if _, err := l.MoveToBin([]BinItem{{Paths: paths}}, "g"); err != nil {
		t.Fatal(err)
	}
	if exists(l, "music/Solo") {
		t.Error("an empty artist folder was left behind")
	}
	if !exists(l, "music") {
		t.Error("the shelf itself was removed")
	}
}

func TestSweepEmptiesOnlyOldEntries(t *testing.T) {
	l := newBinLibrary(t)
	put(t, l, "music/old.mp3", "music/new.mp3")
	oldPaths, _ := l.Resolve(media.KindMusic, []string{"old.mp3"})
	old, _ := l.MoveToBin([]BinItem{{Paths: oldPaths}}, "g")
	newPaths, _ := l.Resolve(media.KindMusic, []string{"new.mp3"})
	fresh, _ := l.MoveToBin([]BinItem{{Paths: newPaths}}, "g")

	later := time.Now().Add(BinKeep + time.Hour)
	// Make the second one a day younger than the first.
	m, _ := readManifest(filepath.Join(l.Root(), ".trash", fresh.ID))
	m.DeletedAt = later.Add(-24 * time.Hour)
	_ = writeManifest(filepath.Join(l.Root(), ".trash", fresh.ID), m)

	if n := l.SweepBin(later, BinKeep); n != 1 {
		t.Errorf("swept %d, want 1", n)
	}
	if exists(l, ".trash/"+old.ID) {
		t.Error("an entry past its time was kept")
	}
	if !exists(l, ".trash/"+fresh.ID) {
		t.Error("an entry inside its time was emptied")
	}
}
