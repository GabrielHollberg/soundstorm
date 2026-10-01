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

func TestFavoritesAreKeptPerPersonAndSurviveARestart(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	if err := s.AddFavorite("u1", song("a")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	_ = s.AddFavorite("u1", song("b"))
	_ = s.AddFavorite("u1", song("a")) // twice is still once
	_ = s.AddFavorite("u2", song("c"))

	reopened, _ := Open(dir)
	got, _ := reopened.Favorites("u1")
	if len(got) != 2 || got[0].Item.ID != "b" || got[1].Item.ID != "a" {
		t.Fatalf("u1 favorites = %+v, want b then a", got)
	}
	other, _ := reopened.Favorites("u2")
	if len(other) != 1 || other[0].Item.ID != "c" {
		t.Errorf("u2 favorites = %+v", other)
	}

	_ = reopened.RemoveFavorite("u1", "navidrome", "b")
	got, _ = reopened.Favorites("u1")
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
	for _, id := range []string{"a", "b", "c", "d"} {
		if _, err := s.AddToPlaylist("u1", p.ID, song(id)); err != nil {
			t.Fatal(err)
		}
	}
	// A playlist holds each song once.
	if _, err := s.AddToPlaylist("u1", p.ID, song("b")); err != ErrAlreadyIn {
		t.Errorf("adding a song twice = %v, want ErrAlreadyIn", err)
	}
	order := func() string {
		got, _ := s.Playlist("u1", p.ID)
		out := ""
		for _, e := range got.Items {
			out += e.Item.ID
		}
		return out
	}
	if order() != "abcd" {
		t.Fatalf("order = %s", order())
	}
	_ = s.MovePlaylistItem("u1", p.ID, 3, 0)
	if order() != "dabc" {
		t.Errorf("after moving the last to the front: %s", order())
	}
	_ = s.RemoveFromPlaylist("u1", p.ID, 1)
	if order() != "dbc" {
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
	if err := s.AddFavorite("../escape", song("a")); err == nil {
		t.Error("an account id with a path in it was accepted")
	}
}

func TestForgetRemovesTheFile(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	_ = s.AddFavorite("u1", song("a"))
	if err := s.Forget("u1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "u1.json")); !os.IsNotExist(err) {
		t.Error("the file is still there")
	}
	if got, _ := s.Favorites("u1"); len(got) != 0 {
		t.Errorf("favorites after forget = %d", len(got))
	}
}

func TestExportAndImportRoundTrip(t *testing.T) {
	from := t.TempDir()
	s, _ := Open(from)
	_ = s.AddFavorite("u1", song("a"))
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
	if favs, _ := back.Favorites("u1"); len(favs) != 1 || favs[0].Item.ID != "a" {
		t.Errorf("favorites after import = %+v", favs)
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

// Putting nothing away is a choice, kept as an empty list - read back as
// missing, it would mean the defaults, which put Genres away again.
func TestNothingPutAwayStaysAChoice(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	if _, err := s.ChangePrefs("u1", PrefsChange{HiddenPills: map[string][]string{"music": {}}}); err != nil {
		t.Fatal(err)
	}
	reopened, _ := Open(dir)
	p, err := reopened.Prefs("u1")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := p.HiddenPills["music"]; !ok || got == nil || len(got) != 0 {
		t.Errorf("hiddenPills[music] = %#v (present %v), want an empty list", got, ok)
	}
}

func TestAPlaylistShowsItsSongsInItsOwnOrder(t *testing.T) {
	s, _ := Open(t.TempDir())
	p, _ := s.CreatePlaylist("u1", "Mix")
	add := func(id, title, artist string) {
		it := song(id)
		it.Title, it.Creators = title, []string{artist}
		if _, err := s.AddToPlaylist("u1", p.ID, it); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond) // distinct added times
	}
	add("1", "Zebra", "Alpha")
	add("2", "apple", "Charlie")
	add("3", "Mango", "Bravo")
	order := func() string {
		got, _ := s.Playlist("u1", p.ID)
		out := ""
		for _, e := range got.Ordered() {
			out += e.Item.ID
		}
		return out
	}
	if order() != "231" {
		t.Errorf("A to Z (the default) = %s, want 231", order())
	}
	for sort, want := range map[string]string{"artist": "132", "added": "321", "custom": "123", "title": "231"} {
		if err := s.SetPlaylistSort("u1", p.ID, sort); err != nil {
			t.Fatal(err)
		}
		if order() != want {
			t.Errorf("%s = %s, want %s", sort, order(), want)
		}
	}
	if err := s.SetPlaylistSort("u1", p.ID, "random"); err != ErrBadSort {
		t.Errorf("a made-up order = %v", err)
	}
}

// Recording a refused token changes only the problem: plays queued after the
// scrobbler was read must survive it.
func TestSetScrobblerProblemKeepsThePlaysQueuedMeanwhile(t *testing.T) {
	s, _ := Open(t.TempDir())
	if err := s.SetScrobbler("u1", &Scrobbler{Service: "listenbrainz", Token: "t"}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordPlay("u1", song("a"), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.SetScrobblerProblem("u1", "connect again"); err != nil {
		t.Fatal(err)
	}
	sc, ok, err := s.ScrobblerFor("u1")
	if err != nil || !ok || sc.Problem != "connect again" || len(sc.Pending) != 1 || sc.Token != "t" {
		t.Errorf("scrobbler = %+v, ok %v, err %v", sc, ok, err)
	}
	// Nobody connected: nothing to record, and nothing made up.
	if err := s.SetScrobblerProblem("u2", "x"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.ScrobblerFor("u2"); ok {
		t.Error("a problem recorded for nobody's service connected one")
	}
}
