package caretaker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Starting over and erasing (the owner's design, 2026-10-03). SoundStorm
// decides who may ask - the owner, with their password and the word typed -
// and the caretaker, which can stop the stack and owns the data drive, does
// the wiping. Both are for good: there is no bin and no snapshot left behind,
// which is the point when a box is sold.
//
//	start-over  every account, sign-in, list and setting, and everything the
//	            backends learnt about the media, goes; the media stays, and the
//	            box sets itself up again and re-reads it.
//	erase       that, and every file in the library: the box as it came.
//
// Either way the setup code in .env stays - it is the one on the sticker.

// ResetMode is what a reset takes away.
type ResetMode string

const (
	ResetStartOver ResetMode = "start-over"
	ResetErase     ResetMode = "erase"
)

// ErrBadMode is a reset that is neither.
var ErrBadMode = errors.New("a reset is start-over or erase")

// Reset stops SoundStorm, empties what the mode takes away and starts it
// again. One at a time, and never during an update.
func (u *Updater) Reset(ctx context.Context, mode ResetMode) error {
	if mode != ResetStartOver && mode != ResetErase {
		return ErrBadMode
	}
	if !u.busy.TryLock() {
		return ErrBusy
	}
	defer u.busy.Unlock()
	u.log.Warn("resetting the box", "mode", mode)
	u.set(func(s *Status) { s.State = "resetting"; s.Message = "Starting over" })

	if err := u.run(ctx, u.cfg.Up, "stop"); err != nil {
		u.set(func(s *Status) {
			s.State = "failed"
			s.Message = "Could not stop SoundStorm to start over. Nothing was changed."
		})
		return err
	}
	// Snapshots taken before updates hold the accounts too: they go first,
	// so nothing of the old box is left to restore by hand.
	if snaps, _ := filepath.Glob(u.cfg.Volumes + "-before-*"); len(snaps) > 0 {
		for _, p := range snaps {
			if err := u.run(ctx, "btrfs", "subvolume", "delete", p); err != nil {
				_ = os.RemoveAll(p)
			}
		}
	}
	emptied := []string{u.cfg.Volumes, u.cfg.Cache}
	if mode == ResetErase {
		emptied = append(emptied, u.cfg.Library)
	}
	var failed error
	for _, dir := range emptied {
		if err := emptyKeepingFolders(dir); err != nil {
			u.log.Error("could not empty", "dir", dir, "err", err)
			failed = err
		}
	}
	// The caretaker's own choices go back to their defaults; the version
	// running is kept, being a fact about the box.
	_ = os.Remove(filepath.Join(u.cfg.StateDir, "settings.json"))

	if err := u.run(ctx, u.cfg.Up); err != nil {
		u.set(func(s *Status) {
			s.State = "failed"
			s.Message = "Started over, but SoundStorm did not start again. Turning the box off and on may help."
		})
		return err
	}
	u.set(func(s *Status) {
		s.State = "idle"
		s.Message = ""
	})
	u.log.Warn("the box was reset", "mode", mode)
	return failed
}

// emptyKeepingFolders removes everything inside dir but keeps its first level
// of folders, empty: each is a volume compose binds, or a shelf a backend
// mounts, and must still be there for them to start.
func emptyKeepingFolders(dir string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var first error
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if e.IsDir() {
			inner, err := os.ReadDir(p)
			if err != nil {
				first = errors.Join(first, err)
				continue
			}
			for _, x := range inner {
				if err := os.RemoveAll(filepath.Join(p, x.Name())); err != nil {
					first = errors.Join(first, err)
				}
			}
			continue
		}
		if err := os.Remove(p); err != nil {
			first = errors.Join(first, err)
		}
	}
	return first
}

// The power button: pressed once, the box shuts down properly; pressed five
// times quickly, somebody is at the box, and for fifteen minutes the owner's
// password can be set again from any device on the home network - the way
// back in for a forgotten password or a lost sticker, which needs nothing
// erased. SoundStorm asks (GET /button) and says when it was used.

// ButtonWindow is how long five presses leave the owner's password open.
const ButtonWindow = 15 * time.Minute

// presses counts the button: a run of presses ends after a quiet moment.
type presses struct {
	mu    sync.Mutex
	until time.Time
}

func (u *Updater) buttonOpen() (bool, time.Time) {
	u.button.mu.Lock()
	defer u.button.mu.Unlock()
	return time.Now().Before(u.button.until), u.button.until
}

func (u *Updater) closeButton() {
	u.button.mu.Lock()
	u.button.until = time.Time{}
	u.button.mu.Unlock()
}

// Pressed is a run of presses of the power button, counted once it ends.
func (u *Updater) Pressed(ctx context.Context, n int) {
	switch {
	case n >= 5:
		u.button.mu.Lock()
		u.button.until = time.Now().Add(ButtonWindow)
		u.button.mu.Unlock()
		u.log.Warn("the power button was pressed five times: the owner's password can be set for 15 minutes")
	case n == 1:
		u.log.Info("the power button was pressed: shutting down")
		_ = u.run(ctx, "systemctl", "poweroff")
	}
}

// countRuns turns presses into runs: a run ends after quiet with no press,
// and is handed to done with how many presses it had.
func countRuns(ctx context.Context, press <-chan struct{}, quiet time.Duration, done func(int)) {
	n := 0
	var timer <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-press:
			n++
			timer = time.After(quiet)
		case <-timer:
			done(n)
			n, timer = 0, nil
		}
	}
}

// powerButton finds the power button's input device on Linux
// (/proc/bus/input/devices), or "".
func powerButton(devices string) string {
	for _, block := range strings.Split(devices, "\n\n") {
		if !strings.Contains(block, `Name="Power Button"`) {
			continue
		}
		for _, line := range strings.Split(block, "\n") {
			if !strings.HasPrefix(line, "H: Handlers=") {
				continue
			}
			for _, h := range strings.Fields(strings.TrimPrefix(line, "H: Handlers=")) {
				if strings.HasPrefix(h, "event") {
					return "/dev/input/" + h
				}
			}
		}
	}
	return ""
}
