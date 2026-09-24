package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/GabrielHollberg/soundstorm/internal/collections"
	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/state"
)

func favourite(t *testing.T, stateDir, userID, itemID string) {
	t.Helper()
	c, err := collections.Open(filepath.Join(stateDir, "collections"))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.AddFavourite(userID, media.Item{ID: itemID, SourceID: "navidrome", Kind: media.KindMusic, Title: itemID}); err != nil {
		t.Fatal(err)
	}
}

// Favourites and playlists go into the backup and come back out of it, into
// a fresh volume, alongside the accounts.
func TestBackupCarriesFavouritesAndPlaylists(t *testing.T) {
	from, _ := newState(t)
	favourite(t, from, "u1", "aria")
	t.Setenv("SOUNDSTORM_STATE_DIR", from)
	backupFile := filepath.Join(t.TempDir(), "backup.json")
	var code int
	capture(t, func() { code = backupState([]string{backupFile}) })
	if code != 0 {
		t.Fatalf("backup exited %d", code)
	}

	to := t.TempDir()
	t.Setenv("SOUNDSTORM_STATE_DIR", to)
	capture(t, func() { code = restoreState([]string{backupFile}) })
	if code != 0 {
		t.Fatalf("restore exited %d", code)
	}
	if summary, err := state.Inspect(filepath.Join(to, "state.json")); err != nil || summary.Users != 1 {
		t.Fatalf("restored state = %+v, %v", summary, err)
	}
	c, _ := collections.Open(filepath.Join(to, "collections"))
	favs, _ := c.Favourites("u1")
	if len(favs) != 1 || favs[0].Item.ID != "aria" {
		t.Errorf("favourites after restore = %+v", favs)
	}
	// And the lists are not left in the state file itself.
	raw, _ := os.ReadFile(filepath.Join(to, "state.json"))
	if containsField(raw, "collections") {
		t.Error("the restored state.json still carries the lists")
	}
}

// A new backup is still a state file: what an older SoundStorm's restore does
// with it - check the version field and write it - must still work, and the
// state must read back.
func TestANewBackupStillRestoresOnAnOlderSoundStorm(t *testing.T) {
	from, _ := newState(t)
	favourite(t, from, "u1", "aria")
	t.Setenv("SOUNDSTORM_STATE_DIR", from)
	var out string
	var code int
	out = capture(t, func() { code = backupState([]string{"-"}) })
	if code != 0 {
		t.Fatalf("backup exited %d", code)
	}
	target := filepath.Join(t.TempDir(), "state.json")
	if err := state.RestoreBytes([]byte(out), "backup", target); err != nil {
		t.Fatalf("the older restore path refused it: %v", err)
	}
	if summary, err := state.Inspect(target); err != nil || summary.Users != 1 {
		t.Errorf("state after the older restore = %+v, %v", summary, err)
	}
}

// A backup made before lists existed restores exactly as it always did, and
// leaves anybody's current lists alone.
func TestAnOldBackupLeavesListsAlone(t *testing.T) {
	_, raw := newState(t)
	to := t.TempDir()
	favourite(t, to, "u1", "kept")
	t.Setenv("SOUNDSTORM_STATE_DIR", to)
	var code int
	capture(t, func() {
		withStdin(t, raw, func() { code = restoreState([]string{"-"}) })
	})
	if code != 0 {
		t.Fatalf("restore exited %d", code)
	}
	c, _ := collections.Open(filepath.Join(to, "collections"))
	if favs, _ := c.Favourites("u1"); len(favs) != 1 {
		t.Errorf("an old backup changed the lists: %+v", favs)
	}
}

func containsField(raw []byte, field string) bool {
	for i := 0; i+len(field)+2 <= len(raw); i++ {
		if string(raw[i:i+len(field)+2]) == `"`+field+`"` {
			return true
		}
	}
	return false
}
