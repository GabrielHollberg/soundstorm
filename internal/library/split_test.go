package library

import (
	"io"
	"log/slog"
	"testing"

	"github.com/GabrielHollberg/soundstorm/internal/media"
)

func planKinds(t *testing.T, paths ...string) (map[string]media.Kind, int) {
	t.Helper()
	l, err := Open(t.TempDir(), "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	files, questions := l.Plan(paths, nil)
	out := map[string]media.Kind{}
	for _, f := range files {
		out[f.Path] = f.Kind
	}
	return out, len(questions)
}

// A folder of everything is split where its subfolders disagree. It used to
// go wholly to Audiobooks, because one subfolder was called that.
func TestAFolderOfEverythingIsSplitByShelf(t *testing.T) {
	got, _ := planKinds(t,
		"Everything/Music/Adele/25/01 Hello.flac",
		"Everything/Movies/Dune (2021)/Dune (2021).mkv",
		"Everything/Shows/The Office/Season 01/S01E01.mkv",
		"Everything/Audiobooks/Dune/Dune.m4b",
		"Everything/Books/Herbert, Frank/Dune.epub",
		"Everything/Phone/IMG_0001.HEIC",
		"Everything/Phone/IMG_0002.MOV",
	)
	want := map[string]media.Kind{
		"Everything/Music/Adele/25/01 Hello.flac":          media.KindMusic,
		"Everything/Movies/Dune (2021)/Dune (2021).mkv":    media.KindVideo,
		"Everything/Shows/The Office/Season 01/S01E01.mkv": media.KindTV,
		"Everything/Audiobooks/Dune/Dune.m4b":              media.KindAudiobook,
		"Everything/Books/Herbert, Frank/Dune.epub":        media.KindEbook,
		"Everything/Phone/IMG_0001.HEIC":                   media.KindPicture,
		"Everything/Phone/IMG_0002.MOV":                    media.KindPicture,
	}
	for p, k := range want {
		if got[p] != k {
			t.Errorf("%s went to %q, want %q", p, got[p], k)
		}
	}
	// An ebook and an audiobook of one book, side by side, each to its own.
	got, _ = planKinds(t, "Dune/Dune.epub", "Dune/Dune.m4b")
	if got["Dune/Dune.epub"] != media.KindEbook || got["Dune/Dune.m4b"] != media.KindAudiobook {
		t.Errorf("an ebook beside its audiobook: %v", got)
	}
}

// What belongs together stays together: an album with its covers and scans,
// a film with its subtitles and poster, an audiobook with its booklet.
func TestWhatBelongsTogetherIsNotSplit(t *testing.T) {
	got, _ := planKinds(t,
		"Abbey Road/CD1/01 Come Together.flac", "Abbey Road/CD2/01 Something.flac",
		"Abbey Road/cover.jpg", "Abbey Road/Scans/booklet-01.jpg", "Abbey Road/Scans/booklet-02.jpg")
	for p, k := range got {
		if k != media.KindMusic {
			t.Errorf("%s went to %q, want music", p, k)
		}
	}
	got, _ = planKinds(t, "Dune (2021)/Dune (2021).mkv", "Dune (2021)/Subs/Dune.en.srt", "Dune (2021)/poster.jpg")
	for p, k := range got {
		if k != media.KindVideo {
			t.Errorf("%s went to %q, want a film", p, k)
		}
	}
	got, _ = planKinds(t, "Atomic Habits/Atomic Habits.m4b", "Atomic Habits/Extras/Atomic Habits.pdf")
	for p, k := range got {
		if k != media.KindAudiobook {
			t.Errorf("%s went to %q, want an audiobook", p, k)
		}
	}
}
