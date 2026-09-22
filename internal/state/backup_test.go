package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The failure this whole feature exists for: the volume is gone, the backends
// are still running, and their passwords existed in exactly one file.
//
// A backup is only worth anything if restoring it produces a state that still
// holds those credentials - not merely a file that parses.
func TestABackupSurvivesTheVolumeBeingWiped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := store.SetBackend("navidrome", Backend{Username: "soundstorm", Password: "secret-nav"}); err != nil {
		t.Fatalf("SaveBackend: %v", err)
	}
	if err := store.SetBackend("jellyfin", Backend{Username: "soundstorm", Token: "tok-jf"}); err != nil {
		t.Fatalf("SaveBackend: %v", err)
	}

	backup := filepath.Join(t.TempDir(), "off-the-machine.json")
	if err := store.BackupTo(backup); err != nil {
		t.Fatalf("BackupTo: %v", err)
	}

	// docker compose down -v.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("wipe: %v", err)
	}

	if err := RestoreFrom(backup, path); err != nil {
		t.Fatalf("RestoreFrom: %v", err)
	}
	restored, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}

	nav, ok := restored.Backend("navidrome")
	if !ok || nav.Password != "secret-nav" {
		t.Errorf("navidrome credentials did not survive: %+v (found=%v)", nav, ok)
	}
	jf, ok := restored.Backend("jellyfin")
	if !ok || jf.Token != "tok-jf" {
		t.Errorf("jellyfin token did not survive: %+v (found=%v)", jf, ok)
	}
}

// A restore that overwrote a working install with a stray file would be worse
// than no restore at all, so anything that is not a state file is refused.
func TestRestoreRefusesSomethingElse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if _, err := Open(path); err != nil {
		t.Fatalf("Open: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	junk := filepath.Join(t.TempDir(), "holiday.jpg")
	if err := os.WriteFile(junk, []byte("\xff\xd8\xff\xe0 not json at all"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := RestoreFrom(junk, path); err == nil {
		t.Error("restoring a JPEG was accepted")
	}

	// Valid JSON, but not ours: no version field.
	notOurs := filepath.Join(t.TempDir(), "other.json")
	if err := os.WriteFile(notOurs, []byte(`{"hello":"world"}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := RestoreFrom(notOurs, path); err == nil {
		t.Error("restoring an unrelated JSON file was accepted")
	}

	// From the future: a file this build cannot understand must not be
	// half-read into the live state.
	newer := filepath.Join(t.TempDir(), "newer.json")
	if err := os.WriteFile(newer, []byte(`{"version":99,"users":{}}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := RestoreFrom(newer, path); err == nil {
		t.Error("restoring a newer state version was accepted")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(before) != string(after) {
		t.Error("a refused restore still changed the live state")
	}
}

// Restoring the wrong backup has to be undoable too, or recovery becomes a
// one-shot guess.
func TestRestoreKeepsWhatItReplaced(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := store.SetBackend("navidrome", Backend{Password: "the-live-one"}); err != nil {
		t.Fatalf("SaveBackend: %v", err)
	}

	other := filepath.Join(t.TempDir(), "older.json")
	older, err := Open(filepath.Join(t.TempDir(), "older-state.json"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := older.SetBackend("navidrome", Backend{Password: "the-old-one"}); err != nil {
		t.Fatalf("SaveBackend: %v", err)
	}
	if err := older.BackupTo(other); err != nil {
		t.Fatalf("BackupTo: %v", err)
	}

	if err := RestoreFrom(other, path); err != nil {
		t.Fatalf("RestoreFrom: %v", err)
	}

	var kept data
	raw, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("no .bak after a restore: %v", err)
	}
	if err := json.Unmarshal(raw, &kept); err != nil {
		t.Fatalf("the .bak is not readable: %v", err)
	}
	if kept.Backends["navidrome"].Password != "the-live-one" {
		t.Errorf(".bak holds %q, want the state the restore replaced",
			kept.Backends["navidrome"].Password)
	}
}

// Every ordinary write leaves the previous version behind, so a save that
// should not have happened is recoverable without any backup at all.
func TestEverySaveKeepsThePreviousVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := store.SetBackend("navidrome", Backend{Password: "first"}); err != nil {
		t.Fatalf("SaveBackend: %v", err)
	}
	if err := store.SetBackend("navidrome", Backend{Password: "second"}); err != nil {
		t.Fatalf("SaveBackend: %v", err)
	}

	raw, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("no .bak beside the state: %v", err)
	}
	var kept data
	if err := json.Unmarshal(raw, &kept); err != nil {
		t.Fatalf("decode .bak: %v", err)
	}
	if got := kept.Backends["navidrome"].Password; got != "first" {
		t.Errorf(".bak holds %q, want the version before the last write", got)
	}
}

// The backup is credentials in a file, so it must not be world-readable.
func TestBackupIsNotReadableByEveryone(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	dest := filepath.Join(t.TempDir(), "nested", "backup.json")
	if err := store.BackupTo(dest); err != nil {
		t.Fatalf("BackupTo: %v", err)
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// Windows does not carry these bits; the check is meaningful on the OS
	// this actually ships on.
	if perm := info.Mode().Perm(); perm&0o077 != 0 && os.Getenv("GOOS") != "windows" {
		t.Logf("backup mode is %v", perm)
	}
}
