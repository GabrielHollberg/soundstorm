package caretaker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// What a power cut part way through an update leaves: the new images file
// written, the release noted as under way, and the snapshot made.
func cutShort(t *testing.T, b *box, m *Manifest, before string) string {
	t.Helper()
	write := func(p, s string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	snap := b.u.cfg.Volumes + "-before-5"
	write(filepath.Join(snap, "accounts"), "the household's accounts")
	write(filepath.Join(b.u.cfg.Volumes, "accounts"), "changed by the new version")
	write(filepath.Join(b.dir, "compose.images.yml"), string(ImagesFile(m)))
	write(filepath.Join(b.u.cfg.StateDir, previousImages), before)
	if err := b.u.writePending(pending{Kind: "update", Manifest: m, Snapshot: snap, HadPrevious: true}); err != nil {
		t.Fatal(err)
	}
	return snap
}

func TestAnUpdateCutShortIsKeptWhenItComesUpHealthy(t *testing.T) {
	b := newBox(t)
	m := release(5)
	cutShort(t, b, m, "# before\n")
	u := New(b.u.cfg, b.u.log)
	u.run, u.output = b.u.run, b.u.output
	u.recoverPending(context.Background())
	if s := u.Status(); s.Current == nil || s.Current.Serial != 5 {
		t.Fatalf("status %+v", s)
	}
	if _, ok := u.readPending(); ok {
		t.Fatal("the update was still noted as under way")
	}
}

func TestAnUpdateCutShortIsUndoneWhenItIsNotHealthy(t *testing.T) {
	b := newBox(t)
	m := release(5)
	snap := cutShort(t, b, m, "# before\n")
	b.u.saveBaseline(9) // the box last came up with nine
	b.healthy.Store(false)
	// Cut short again, in the rollback: the volumes set aside, the copy not
	// yet put back.
	if err := os.Rename(b.u.cfg.Volumes, b.u.cfg.Volumes+"-failed-x"); err != nil {
		t.Fatal(err)
	}
	u := New(b.u.cfg, b.u.log)
	u.run, u.output = b.u.run, b.u.output
	u.recoverPending(context.Background())
	if b.images() != "# before\n" {
		t.Fatalf("the images from before were not put back:\n%s", b.images())
	}
	got, err := os.ReadFile(filepath.Join(b.u.cfg.Volumes, "accounts"))
	if err != nil || string(got) != "the household's accounts" {
		t.Fatalf("the volumes were not put back: %q %v", got, err)
	}
	if exists(snap) {
		t.Fatal("the snapshot was left where it was")
	}
	if s := u.Status(); s.State != "rolled-back" || s.Current != nil {
		t.Fatalf("status %+v", s)
	}
	if _, ok := u.readPending(); ok {
		t.Fatal("the update was still noted as under way")
	}
}

func TestAResetCutShortIsDoneAgain(t *testing.T) {
	b := newBox(t)
	b.u.cfg.Cache = filepath.Join(b.dir, "cache")
	b.u.cfg.Library = filepath.Join(b.dir, "library")
	for _, d := range []string{b.u.cfg.Volumes, b.u.cfg.Cache, b.u.cfg.Library} {
		if err := os.MkdirAll(filepath.Join(d, "kept"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	left := filepath.Join(b.u.cfg.Volumes, "kept", "accounts.json")
	if err := os.WriteFile(left, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := b.u.writePending(pending{Kind: "reset", Mode: ResetStartOver}); err != nil {
		t.Fatal(err)
	}
	b.u.recoverPending(context.Background())
	if exists(left) {
		t.Fatal("the reset was not done again")
	}
	if _, ok := b.u.readPending(); ok {
		t.Fatal("the reset was still noted as under way")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !strings.Contains(strings.Join(b.ran, "\n"), "up down") || b.ran[len(b.ran)-1] != "up" {
		t.Fatalf("ran %q", b.ran)
	}
}

// A snapshot never goes onto one already there: btrfs would put it inside.
func TestASnapshotHasANameOfItsOwn(t *testing.T) {
	b := newBox(t)
	first := b.u.snapshotPath(5)
	if err := os.MkdirAll(first, 0o755); err != nil {
		t.Fatal(err)
	}
	if again := b.u.snapshotPath(5); again == first || !strings.HasPrefix(again, first+"-") {
		t.Fatalf("%q then %q", first, again)
	}
}

// The caretaker stopping while an update waits for health is not the update
// failing: nothing is rolled back, and the update stays written down for
// the next start.
func TestAnUpdateCutByShutdownIsLeftForTheNextStart(t *testing.T) {
	b := newBox(t)
	b.healthy.Store(false)
	b.u.saveBaseline(9)
	b.publish(release(5))
	m, _ := b.u.Check(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	b.u.run = func(_ context.Context, name string, args ...string) error {
		if name == "up" && len(args) == 0 {
			cancel() // switched off as the new version starts
		}
		return nil
	}
	if err := b.u.Update(ctx, m); err == nil {
		t.Fatal("reported as updated")
	}
	if _, ok := b.u.readPending(); !ok {
		t.Fatal("the update was not left for the next start")
	}
	if b.u.rolledBack(5) {
		t.Fatal("taken for a failed release")
	}
}

// A release undone here is not offered again.
func TestARolledBackReleaseIsNotOfferedAgain(t *testing.T) {
	b := newBox(t)
	b.u.markRolledBack(5)
	b.publish(release(5))
	if m, err := b.u.Check(context.Background()); err != nil || m != nil {
		t.Fatalf("offered again: %v %v", m, err)
	}
	b.publish(release(6))
	if m, _ := b.u.Check(context.Background()); m == nil || m.Serial != 6 {
		t.Fatal("a newer release was not offered")
	}
}

// Ten presses while an update holds the box are not lost: the reset runs
// once it is free. And an erase leaves no copy of the accounts behind.
func TestAResetWaitsForABusyBox(t *testing.T) {
	b := newBox(t)
	b.u.cfg.Volumes = filepath.Join(b.dir, "volumes")
	b.u.cfg.Cache = filepath.Join(b.dir, "cache")
	b.u.cfg.Library = filepath.Join(b.dir, "library")
	for _, d := range []string{b.u.cfg.Volumes, b.u.cfg.Cache, b.u.cfg.Library, b.u.cfg.Volumes + "-failed-x"} {
		os.MkdirAll(filepath.Join(d, "kept"), 0o755)
	}
	account := filepath.Join(b.u.cfg.Volumes, "kept", "state.json")
	os.WriteFile(account, []byte("{}"), 0o644)
	var deleted []string
	b.u.run = func(_ context.Context, name string, args ...string) error {
		if name == "btrfs" && len(args) == 3 && args[1] == "delete" {
			deleted = append(deleted, args[2])
		}
		return nil
	}
	b.u.busy.Lock() // an update under way
	b.u.ArmReset(ResetErase)
	b.u.Pressed(context.Background(), ResetPresses)
	time.Sleep(300 * time.Millisecond)
	if _, err := os.Stat(account); err != nil {
		t.Fatal("reset while busy")
	}
	b.u.busy.Unlock()
	for i := 0; i < 150; i++ {
		if _, err := os.Stat(account); os.IsNotExist(err) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if _, err := os.Stat(account); !os.IsNotExist(err) {
		t.Fatal("the reset never ran once the box was free")
	}
	if !strings.Contains(strings.Join(deleted, " "), "-failed-x") {
		t.Fatalf("a copy of the accounts was left: deleted %v", deleted)
	}
}
