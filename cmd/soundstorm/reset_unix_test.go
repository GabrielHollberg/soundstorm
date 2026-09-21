//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/GabrielHollberg/soundstorm/internal/auth"
	"github.com/GabrielHollberg/soundstorm/internal/state"
)

// newStateWithOwner writes a state file containing one account and hands it to
// uid/gid, standing in for the volume a running SoundStorm owns.
func newStateWithOwner(t *testing.T, uid, gid int) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	store, err := state.Open(path)
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	if _, err := auth.New(store).Signup("gabe", "originalpassword"); err != nil {
		t.Fatalf("signup: %v", err)
	}
	// The directory too, or the service user cannot reach the file through it.
	if err := os.Chown(dir, uid, gid); err != nil {
		t.Fatalf("chown dir: %v", err)
	}
	if err := os.Chown(path, uid, gid); err != nil {
		t.Fatalf("chown state: %v", err)
	}
	return path
}

func ownerOf(t *testing.T, path string) (int, int) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	sys, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("no ownership information on this platform")
	}
	return int(sys.Uid), int(sys.Gid)
}

// The failure this exists to prevent, reproduced exactly: root resets a
// password on a state file owned by the unprivileged service user, and the
// server afterwards cannot read its own state. It crash-loops at startup on
// "read state: permission denied", which took a live install down for two
// minutes before this was written.
//
// state.Open migrates and saves unconditionally - it rewrites the file every
// time, changed or not - so merely opening the store as another user is
// enough to take ownership. That is why the stat has to happen first.
func TestResetKeepsTheFileItsOwner(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to hand a file to another user; run the suite in a container")
	}

	const uid, gid = 10001, 10001
	path := newStateWithOwner(t, uid, gid)
	t.Setenv("SOUNDSTORM_STATE_DIR", filepath.Dir(path))

	if code := resetPassword(nil); code != 0 {
		t.Fatalf("reset-password exited %d", code)
	}

	gotUID, gotGID := ownerOf(t, path)
	if gotUID != uid || gotGID != gid {
		t.Errorf("state is now owned by %d:%d, want %d:%d - SoundStorm would not start",
			gotUID, gotGID, uid, gid)
	}
}

// Opening the store is itself a write, so an early return - a name that does
// not exist - must not leave the file behind in the wrong hands either.
func TestAFailedResetAlsoKeepsTheOwner(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to hand a file to another user; run the suite in a container")
	}

	const uid, gid = 10001, 10001
	path := newStateWithOwner(t, uid, gid)
	t.Setenv("SOUNDSTORM_STATE_DIR", filepath.Dir(path))

	if code := resetPassword([]string{"nobody"}); code == 0 {
		t.Fatal("resetting an account that does not exist reported success")
	}

	gotUID, gotGID := ownerOf(t, path)
	if gotUID != uid || gotGID != gid {
		t.Errorf("a failed reset left the state owned by %d:%d, want %d:%d", gotUID, gotGID, uid, gid)
	}
}
