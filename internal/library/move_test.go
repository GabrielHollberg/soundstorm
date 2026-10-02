package library

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/GabrielHollberg/soundstorm/internal/media"
)

func TestMovingAnItemToAnotherShelf(t *testing.T) {
	root := t.TempDir()
	l, err := Open(root, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	write := func(rel string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(rel), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("movies/The Office S01/The Office S01E01.mkv")
	write("movies/The Office S01/The Office S01E01.en.srt")

	// A film that was a show: to TV, folder and subtitle with it.
	res, err := l.MoveItems([]BinItem{{Title: "The Office", Kind: media.KindVideo, Paths: []string{"movies/The Office S01"}}}, media.KindTV, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if r.Err != nil || r.To == "" {
			t.Fatalf("not moved: %+v", r)
		}
	}
	for _, rel := range []string{"tv/The Office S01/The Office S01E01.mkv", "tv/The Office S01/The Office S01E01.en.srt"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s is not there: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "movies", "The Office S01")); !os.IsNotExist(err) {
		t.Errorf("the emptied film folder was left behind")
	}

	// A shelf that does not keep the file: nothing moves.
	write("movies/Dune/Dune.mkv")
	_, err = l.MoveItems([]BinItem{{Title: "Dune", Kind: media.KindVideo, Paths: []string{"movies/Dune"}}}, media.KindMusic, nil)
	if !errors.Is(err, ErrShelfRefuses) {
		t.Fatalf("a film to music: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "movies", "Dune", "Dune.mkv")); err != nil {
		t.Errorf("a refused move moved something: %v", err)
	}
}
