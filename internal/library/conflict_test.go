package library

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/media"
)

// A taken name: an exact copy is refused whatever was decided; a different
// file with the name is refused by default, kept beside as "Name (2)", or
// replaces the one there with the old one handed to the bin.
func TestATakenName(t *testing.T) {
	root := t.TempDir()
	l, err := Open(root, "./library", testLog())
	if err != nil {
		t.Fatal(err)
	}
	film := filepath.Join(root, "movies", "Dune (2021)", "Dune (2021).mkv")
	if err := os.MkdirAll(filepath.Dir(film), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(film, []byte("the first film"), 0o666); err != nil {
		t.Fatal(err)
	}
	rel := "Dune (2021)/Dune (2021).mkv"
	save := func(body string, opts SaveOptions) (string, error) {
		return l.SaveWith(media.KindVideo, rel, strings.NewReader(body), nil, opts)
	}

	// What is there, before anything is sent.
	st, err := l.CheckDest(media.KindVideo, "movies/"+rel, int64(len("the first film")))
	if err != nil || !st.Taken || st.Sample == "" {
		t.Fatalf("the check should find it, with a sample: %+v %v", st, err)
	}
	if mine, _ := Sample(film); mine != st.Sample {
		t.Fatal("the check's sample should be the file's")
	}
	if _, err := l.CheckDest(media.KindVideo, "music/x.mp3", 1); err == nil {
		t.Fatal("a path on another shelf should be refused")
	}

	if _, err := save("the first film", SaveOptions{Conflict: ConflictKeep}); err != ErrAlreadyThere {
		t.Fatalf("an exact copy is already there, even asked to keep both: %v", err)
	}
	if _, err := save("another film", SaveOptions{}); err != ErrAlreadyThere {
		t.Fatalf("a different file is refused unless asked: %v", err)
	}
	// Nothing in these files tells them apart, so the new one is named for
	// the day it came; a name somebody typed wins when it is free.
	dest, err := save("another film", SaveOptions{Conflict: ConflictKeep})
	if want := "movies/Dune (2021)/Dune (2021) - added " + time.Now().Format("2006-01-02") + ".mkv"; err != nil || dest != want {
		t.Fatalf("keep both should name it for the day: %q %v", dest, err)
	}
	dest, err = save("a fourth film", SaveOptions{Conflict: ConflictKeep, Name: "Dune (2021) - extended"})
	if err != nil || dest != "movies/Dune (2021)/Dune (2021) - extended.mkv" {
		t.Fatalf("a typed name should be used, with its extension: %q %v", dest, err)
	}
	var binned string
	dest, err = save("a third film", SaveOptions{Conflict: ConflictReplace, Replace: func(r string) error {
		binned = r
		return os.Rename(film, filepath.Join(root, "binned.mkv"))
	}})
	if err != nil || dest != "movies/"+rel || binned != "movies/"+rel {
		t.Fatalf("replace should bin the old one and take its name: %q %q %v", dest, binned, err)
	}
	if b, _ := os.ReadFile(film); string(b) != "a third film" {
		t.Fatalf("the new one should be in place: %q", b)
	}
}

// A Sample reads the start, middle and end of a big file, and its length.
func TestASampleSeesTheLengthAndTheEnds(t *testing.T) {
	dir := t.TempDir()
	big := make([]byte, 5*sampleChunk)
	a := filepath.Join(dir, "a")
	os.WriteFile(a, big, 0o666)
	big[len(big)-1] = 1
	b := filepath.Join(dir, "b")
	os.WriteFile(b, big, 0o666)
	sa, _ := Sample(a)
	sb, _ := Sample(b)
	if sa == "" || sa == sb {
		t.Fatal("a change in the last megabyte should change the sample")
	}
}

// What tells two files of one name apart, as their names will say.
func TestLabelsSayWhatDiffers(t *testing.T) {
	day := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	taken := time.Date(2024, 5, 12, 14, 3, 22, 0, time.UTC)
	cases := []struct {
		kind      media.Kind
		inc, have Traits
		want      string
	}{
		{media.KindPicture, Traits{Camera: "iPhone 15 Pro", Taken: taken}, Traits{Camera: "Pixel 8"}, "iPhone 15 Pro"},
		{media.KindPicture, Traits{Camera: "Pixel 8", Taken: taken}, Traits{Camera: "Pixel 8"}, "2024-05-12 14.03.22"},
		{media.KindVideo, Traits{Width: 3840, Height: 1600}, Traits{Width: 1920, Height: 800}, "2160p"},
		{media.KindVideo, Traits{Width: 1920, Height: 800, Seconds: 10260}, Traits{Width: 1920, Height: 800, Seconds: 9300}, "2h 51m"},
		{media.KindMusic, Traits{Kbps: 320, Seconds: 225}, Traits{Kbps: 128, Seconds: 225}, "320 kbps"},
		{media.KindMusic, Traits{Kbps: 256, Seconds: 300}, Traits{Kbps: 256, Seconds: 225}, "5m 00s"},
		{media.KindAudiobook, Traits{Narrator: "Jonathan Haidt"}, Traits{Narrator: "Someone Else"}, "read by Jonathan Haidt"},
		{media.KindEbook, Traits{}, Traits{}, "added 2026-10-02"},
	}
	for _, c := range cases {
		if got := Label(c.kind, c.inc, c.have, day); got != c.want {
			t.Errorf("%s: %q, want %q", c.kind, got, c.want)
		}
	}
}
