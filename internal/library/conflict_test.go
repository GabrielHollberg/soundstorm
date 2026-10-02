package library

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	dest, err := save("another film", SaveOptions{Conflict: ConflictKeep})
	if err != nil || dest != "movies/Dune (2021)/Dune (2021) (2).mkv" {
		t.Fatalf("keep both should save it as (2): %q %v", dest, err)
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
