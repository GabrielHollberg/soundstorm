package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GabrielHollberg/soundstorm/internal/auth"
	"github.com/GabrielHollberg/soundstorm/internal/state"
)

// The password it prints has to be the one that works, and the old one has to
// stop working - checked through auth rather than by reading the hash.
func TestResetReplacesThePassword(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	store, err := state.Open(path)
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	if _, err := auth.New(store).Signup("gabe", "originalpassword"); err != nil {
		t.Fatalf("signup: %v", err)
	}
	t.Setenv("SOUNDSTORM_STATE_DIR", dir)

	printed := capture(t, func() {
		if code := resetPassword(nil); code != 0 {
			t.Fatalf("reset-password exited %d", code)
		}
	})

	password := ""
	for _, line := range strings.Split(printed, "\n") {
		if fields := strings.Fields(line); len(fields) == 2 && fields[0] == "password" {
			password = fields[1]
		}
	}
	if password == "" {
		t.Fatalf("no password in the output:\n%s", printed)
	}

	// Re-read from disk: the point is that the change was persisted, not that
	// an in-memory store was updated.
	reopened, err := state.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	manager := auth.New(reopened)
	if _, _, _, err := manager.Login("gabe", password); err != nil {
		t.Errorf("the printed password does not work: %v", err)
	}
	if _, _, _, err := manager.Login("gabe", "originalpassword"); err == nil {
		t.Error("the old password still works")
	}
}

// With no accounts there is nothing to reset, and that is not an error - it is
// somebody running recovery on an install they have not set up yet.
func TestResetOnAnEmptyServerExplainsItself(t *testing.T) {
	dir := t.TempDir()
	if _, err := state.Open(filepath.Join(dir, "state.json")); err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Setenv("SOUNDSTORM_STATE_DIR", dir)

	out := capture(t, func() {
		if code := resetPassword(nil); code != 0 {
			t.Errorf("exited %d, want 0 - an empty server is not a failure", code)
		}
	})
	if !strings.Contains(out, "no accounts") {
		t.Errorf("output did not say there are no accounts:\n%s", out)
	}
}

// The generated password has to survive being read off a screen and typed
// into a phone.
func TestGeneratedPasswordIsReadable(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		pw, err := readablePassword()
		if err != nil {
			t.Fatalf("readablePassword: %v", err)
		}
		if seen[pw] {
			t.Fatal("generated the same password twice")
		}
		seen[pw] = true

		if strings.ContainsAny(pw, "l1O0") {
			t.Errorf("%q contains a character that is misread when typed", pw)
		}
		// Long enough that the server's own minimum accepts it.
		if len(pw) < auth.MinPasswordLength {
			t.Errorf("%q is shorter than the server's minimum of %d", pw, auth.MinPasswordLength)
		}
	}
}

// capture collects stdout while fn runs.
func capture(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	old := os.Stdout
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			b.Write(buf[:n])
			if err != nil {
				break
			}
		}
		done <- b.String()
	}()
	fn()
	w.Close()
	os.Stdout = old
	return <-done
}

// A switch has to mean what it says. This was `!= "false"`, so
// SOUNDSTORM_STARTER_LIBRARY=off enabled the starter library - the opposite of
// what was written, with nothing on screen to say so. Found by writing "off"
// while testing something else, because SOUNDSTORM_TLS uses "off" for exactly
// this idea.
func TestEnabledReadsTheObviousSpellings(t *testing.T) {
	for _, off := range []string{"false", "off", "no", "0", "", "  OFF  ", "False"} {
		if enabled(off) {
			t.Errorf("enabled(%q) = true, want false", off)
		}
	}
	for _, on := range []string{"true", "yes", "1", "on", "anything"} {
		if !enabled(on) {
			t.Errorf("enabled(%q) = false, want true", on)
		}
	}
}
