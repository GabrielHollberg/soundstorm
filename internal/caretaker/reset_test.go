package caretaker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Starting over empties every volume and the caches but keeps the media; an
// erase takes the media too. The folders themselves stay - compose binds them
// and backends mount them - and the snapshots taken before updates go, as
// they hold the accounts.
func TestResetEmptiesWhatItSays(t *testing.T) {
	for _, mode := range []ResetMode{ResetStartOver, ResetErase} {
		b := newBox(t)
		srv := filepath.Join(b.dir, "srv")
		b.u.cfg.Volumes = filepath.Join(srv, "volumes")
		b.u.cfg.Cache = filepath.Join(srv, "cache")
		b.u.cfg.Library = filepath.Join(srv, "library")
		write := func(rel string) {
			p := filepath.Join(srv, rel)
			os.MkdirAll(filepath.Dir(p), 0o755)
			os.WriteFile(p, []byte("x"), 0o644)
		}
		write("volumes/soundstorm-state/state.json")
		write("volumes/navidrome-data/navidrome.db")
		write("cache/jellyfin-cache/x/y")
		write("library/music/Artist/Album/01.mp3")
		write("library/README.txt")
		write("volumes-before-7/soundstorm-state/state.json")

		if err := b.u.Reset(context.Background(), mode); err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		gone := func(rel string) bool { _, err := os.Stat(filepath.Join(srv, rel)); return os.IsNotExist(err) }
		there := func(rel string) bool { return !gone(rel) }
		if !gone("volumes/soundstorm-state/state.json") || !gone("volumes/navidrome-data/navidrome.db") || !gone("cache/jellyfin-cache/x") {
			t.Errorf("%s: accounts or caches left", mode)
		}
		if !there("volumes/soundstorm-state") || !there("cache/jellyfin-cache") || !there("library/music") {
			t.Errorf("%s: a volume or shelf folder went", mode)
		}
		if mode == ResetStartOver && !there("library/music/Artist/Album/01.mp3") {
			t.Error("starting over took the media")
		}
		if mode == ResetErase && (there("library/music/Artist") || there("library/README.txt")) {
			t.Error("an erase left media")
		}
		b.mu.Lock()
		ran := strings.Join(b.ran, "; ")
		b.mu.Unlock()
		if !strings.HasPrefix(ran, "up stop") || !strings.HasSuffix(ran, "up") || !strings.Contains(ran, "btrfs subvolume delete "+filepath.Join(srv, "volumes-before-7")) {
			t.Errorf("%s ran: %s", mode, ran)
		}
	}
	if err := newBox(t).u.Reset(context.Background(), "everything"); err != ErrBadMode {
		t.Errorf("an unknown mode: %v", err)
	}
}

// Presses come in runs: once is a shutdown, five quickly open the owner's
// password for a while.
func TestThePowerButtonCountsRuns(t *testing.T) {
	press := make(chan struct{}, 16)
	runs := make(chan int, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go countRuns(ctx, press, 50*time.Millisecond, func(n int) { runs <- n })
	for i := 0; i < 5; i++ {
		press <- struct{}{}
		time.Sleep(10 * time.Millisecond)
	}
	if n := <-runs; n != 5 {
		t.Fatalf("five quick presses counted as %d", n)
	}
	press <- struct{}{}
	if n := <-runs; n != 1 {
		t.Fatalf("one press counted as %d", n)
	}

	b := newBox(t)
	b.u.Pressed(ctx, 5)
	if open, _ := b.u.buttonOpen(); !open {
		t.Fatal("five presses did not open the owner's password")
	}
	b.u.closeButton()
	if open, _ := b.u.buttonOpen(); open {
		t.Fatal("still open after it was used")
	}
	b.u.Pressed(ctx, 1)
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.ran) == 0 || b.ran[len(b.ran)-1] != "systemctl poweroff" {
		t.Fatalf("one press ran %v", b.ran)
	}
}

func TestThePowerButtonIsFound(t *testing.T) {
	devices := "I: Bus=0019 Vendor=0000 Product=0001 Version=0000\nN: Name=\"Power Button\"\nH: Handlers=kbd event2 \n\n" +
		"I: Bus=0003\nN: Name=\"Keyboard\"\nH: Handlers=sysrq kbd event3\n"
	if got := powerButton(devices); got != "/dev/input/event2" {
		t.Fatalf("found %q", got)
	}
}
