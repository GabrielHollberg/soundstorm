package collections

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/media"
)

func song(id string) media.Item {
	return media.Item{ID: id, SourceID: "navidrome", Kind: media.KindMusic, Title: "Song " + id}
}

func TestFavouritesAreKeptPerPersonAndSurviveARestart(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	if err := s.AddFavourite("u1", song("a")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	_ = s.AddFavourite("u1", song("b"))
	_ = s.AddFavourite("u1", song("a")) // twice is still once
	_ = s.AddFavourite("u2", song("c"))

	reopened, _ := Open(dir)
	got, _ := reopened.Favourites("u1")
	if len(got) != 2 || got[0].Item.ID != "b" || got[1].Item.ID != "a" {
		t.Fatalf("u1 favourites = %+v, want b then a", got)
	}
	other, _ := reopened.Favourites("u2")
	if len(other) != 1 || other[0].Item.ID != "c" {
		t.Errorf("u2 favourites = %+v", other)
	}

	_ = reopened.RemoveFavourite("u1", "navidrome", "b")
	got, _ = reopened.Favourites("u1")
	if len(got) != 1 || got[0].Item.ID != "a" {
		t.Errorf("after removing b: %+v", got)
	}
}

func TestPlaylistsKeepTheirOrder(t *testing.T) {
	s, _ := Open(t.TempDir())
	p, err := s.CreatePlaylist("u1", "  Road trip  ")
	if err != nil || p.Name != "Road trip" {
		t.Fatalf("CreatePlaylist = %+v, %v", p, err)
	}
	for _, id := range []string{"a", "b", "c", "a"} {
		if _, err := s.AddToPlaylist("u1", p.ID, song(id)); err != nil {
			t.Fatal(err)
		}
	}
	order := func() string {
		got, _ := s.Playlist("u1", p.ID)
		out := ""
		for _, e := range got.Items {
			out += e.Item.ID
		}
		return out
	}
	if order() != "abca" {
		t.Fatalf("order = %s", order())
	}
	_ = s.MovePlaylistItem("u1", p.ID, 3, 0)
	if order() != "aabc" {
		t.Errorf("after moving the last to the front: %s", order())
	}
	_ = s.RemoveFromPlaylist("u1", p.ID, 1)
	if order() != "abc" {
		t.Errorf("after removing the second: %s", order())
	}
	if _, err := s.Playlist("u2", p.ID); err != ErrNotFound {
		t.Errorf("somebody else's playlist = %v, want ErrNotFound", err)
	}
	if err := s.DeletePlaylist("u1", p.ID); err != nil {
		t.Fatal(err)
	}
	if lists, _ := s.Playlists("u1"); len(lists) != 0 {
		t.Errorf("playlists after delete = %d", len(lists))
	}
}

func TestNamesAndIdsAreChecked(t *testing.T) {
	s, _ := Open(t.TempDir())
	if _, err := s.CreatePlaylist("u1", "   "); err == nil {
		t.Error("a blank name was accepted")
	}
	long := make([]rune, MaxNameLength+1)
	for i := range long {
		long[i] = 'x'
	}
	if _, err := s.CreatePlaylist("u1", string(long)); err == nil {
		t.Error("an overlong name was accepted")
	}
	if err := s.AddFavourite("../escape", song("a")); err == nil {
		t.Error("an account id with a path in it was accepted")
	}
}

func TestForgetRemovesTheFile(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	_ = s.AddFavourite("u1", song("a"))
	if err := s.Forget("u1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "u1.json")); !os.IsNotExist(err) {
		t.Error("the file is still there")
	}
	if got, _ := s.Favourites("u1"); len(got) != 0 {
		t.Errorf("favourites after forget = %d", len(got))
	}
}

func TestExportAndImportRoundTrip(t *testing.T) {
	from := t.TempDir()
	s, _ := Open(from)
	_ = s.AddFavourite("u1", song("a"))
	p, _ := s.CreatePlaylist("u2", "Mix")
	_, _ = s.AddToPlaylist("u2", p.ID, song("b"))

	files, err := Export(from)
	if err != nil || len(files) != 2 {
		t.Fatalf("Export = %d files, %v", len(files), err)
	}

	to := t.TempDir()
	if _, err := Import(to, files); err != nil {
		t.Fatal(err)
	}
	back, _ := Open(to)
	if favs, _ := back.Favourites("u1"); len(favs) != 1 || favs[0].Item.ID != "a" {
		t.Errorf("favourites after import = %+v", favs)
	}
	if got, err := back.Playlist("u2", p.ID); err != nil || len(got.Items) != 1 {
		t.Errorf("playlist after import = %+v, %v", got, err)
	}
}

func TestImportRefusesADamagedBackupBeforeWritingAnything(t *testing.T) {
	to := t.TempDir()
	_, err := Import(to, map[string]json.RawMessage{
		"u1":         json.RawMessage(`{"favourites":[]}`),
		"../outside": json.RawMessage(`{}`),
	})
	if err == nil {
		t.Fatal("a backup naming a path was accepted")
	}
	if entries, _ := os.ReadDir(to); len(entries) != 0 {
		t.Errorf("%d files written before the refusal", len(entries))
	}
}

func TestHistoryCountsAndForgetsTheOldestFirst(t *testing.T) {
	s, _ := Open(t.TempDir())
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_ = s.RecordPlay("u1", song("a"), t0)
	_ = s.RecordPlay("u1", song("a"), t0.Add(time.Hour))
	_ = s.RecordPlay("u1", song("b"), t0.Add(2*time.Hour))
	plays, _ := s.History("u1")
	counts := map[string]int{}
	for _, p := range plays {
		counts[p.Item.ID] = p.Count
	}
	if counts["a"] != 2 || counts["b"] != 1 {
		t.Fatalf("counts = %v", counts)
	}
	if other, _ := s.History("u2"); len(other) != 0 {
		t.Errorf("u2 has %d plays of u1's", len(other))
	}
}
