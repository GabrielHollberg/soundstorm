package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/GabrielHollberg/soundstorm/internal/auth"
	"github.com/GabrielHollberg/soundstorm/internal/state"
)

// withStdin runs fn with standard input reading data, as a shell redirect would
// feed it.
func withStdin(t *testing.T, data []byte, fn func()) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	go func() {
		w.Write(data)
		w.Close()
	}()
	old := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = old; r.Close() }()
	fn()
}

func newState(t *testing.T) (dir string, raw []byte) {
	t.Helper()
	dir = t.TempDir()
	path := filepath.Join(dir, "state.json")
	store, err := state.Open(path)
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	if _, err := auth.New(store).Signup("gabe", "originalpassword"); err != nil {
		t.Fatalf("signup: %v", err)
	}
	raw, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return dir, raw
}

// "backup -" writes the state itself to standard output and nothing else, so a
// shell redirect makes the file - owned by whoever ran it, not by the
// container's user, which on Linux only root could then read. The summary goes
// to standard error, or it would corrupt the backup.
func TestBackupToStandardOutputIsTheStateItself(t *testing.T) {
	dir, raw := newState(t)
	t.Setenv("SOUNDSTORM_STATE_DIR", dir)

	var code int
	out := capture(t, func() { code = backupState([]string{"-"}) })
	if code != 0 {
		t.Fatalf("backup - exited %d", code)
	}
	if out != string(raw) {
		t.Errorf("standard output is not exactly the state file:\n%q", out)
	}
}

// "restore -" reads the backup from standard input, and restores it.
func TestRestoreFromStandardInput(t *testing.T) {
	_, raw := newState(t)
	target := t.TempDir()
	t.Setenv("SOUNDSTORM_STATE_DIR", target)

	var code int
	capture(t, func() {
		withStdin(t, raw, func() { code = restoreState([]string{"-"}) })
	})
	if code != 0 {
		t.Fatalf("restore - exited %d", code)
	}
	summary, err := state.Inspect(filepath.Join(target, "state.json"))
	if err != nil {
		t.Fatalf("restored state does not read: %v", err)
	}
	if summary.Users != 1 {
		t.Errorf("restored %d accounts, want 1", summary.Users)
	}
}

// Whatever arrives on standard input is checked like a file would be: not a
// SoundStorm backup, nothing is replaced.
func TestRestoreFromStandardInputRefusesAnythingElse(t *testing.T) {
	target := t.TempDir()
	t.Setenv("SOUNDSTORM_STATE_DIR", target)

	var code int
	capture(t, func() {
		withStdin(t, []byte("  Backed up to /backup/soundstorm.json (1234 bytes)\n"), func() {
			code = restoreState([]string{"-"})
		})
	})
	if code == 0 {
		t.Error("a summary line was accepted as a backup")
	}
	if _, err := os.Stat(filepath.Join(target, "state.json")); err == nil {
		t.Error("a refused restore still wrote a state file")
	}
}
