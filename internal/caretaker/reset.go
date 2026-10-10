package caretaker

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Starting over and erasing (the owner's design, 2026-10-03). EmberStorm
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

// Reset stops EmberStorm, empties what the mode takes away and starts it
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
	// Written down first: cut short by a power cut, it is done again at the
	// next start (pending.go) rather than leaving a box half emptied.
	if err := u.writePending(pending{Kind: "reset", Mode: mode}); err != nil {
		u.log.Warn("could not note the reset", "err", err)
	}

	// Down, not only stopped: the containers go, and with them what each
	// kept of its own - its logs above all (the twelfth security pass).
	if err := u.runFor(ctx, stopTimeout, u.cfg.Up, "down"); err != nil {
		u.clearPending()
		// Part of it may have stopped: started again, as it was.
		_ = u.runFor(ctx, upTimeout, u.cfg.Up)
		u.set(func(s *Status) {
			s.State = "failed"
			s.Message = "Could not stop EmberStorm to start over. Nothing was changed."
		})
		return err
	}
	// Snapshots taken before updates hold the accounts too: they go first,
	// so nothing of the old box is left to restore by hand.
	snaps, _ := filepath.Glob(u.cfg.Volumes + "-before-*")
	// And updated volumes a rollback set aside: a copy of every account.
	failedCopies, _ := filepath.Glob(u.cfg.Volumes + "-failed-*")
	if snaps = append(snaps, failedCopies...); len(snaps) > 0 {
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
	// And what the box keeps beside the data drive.
	if err := u.run(ctx, u.cfg.Forget, string(mode)); err != nil {
		u.log.Error("could not clear what the box keeps beside the drive", "err", err)
	}
	// The drive is readied again as at boot: the models built into the box go
	// back into the emptied caches (photo search, Make an ebook and read-along
	// work with no internet), and the shelves and folders are as they should
	// be. Without it they came back only at the next boot.
	if err := u.run(ctx, u.cfg.Prepare); err != nil {
		u.log.Error("could not ready the drive again", "err", err)
	}

	// Everything taken away is gone now: starting again is all that is
	// left, which a restart does by itself.
	u.clearPending()
	if err := u.runFor(ctx, upTimeout, u.cfg.Up); err != nil {
		u.set(func(s *Status) {
			s.State = "failed"
			s.Message = "Started over, but EmberStorm did not start again. Turning the box off and on may help."
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

// The power button (the owner's choices, 2026-10-10): pressed twice quickly,
// the box shuts down properly - once does nothing, so a press to "wake" it
// switches nothing off, and a PC's button says only that it went down, never
// how long it was held; pressed five times quickly, somebody is at the box,
// and for fifteen minutes the owner's password can be set again from any
// device on the home network with the code on the box's screen or the setup
// code on the sticker - the way back in for a forgotten password, which needs
// nothing erased. Ten presses are what lets an erase or a start over asked
// for in the app go ahead (ArmReset): nobody empties the box without being
// at it, and the five a forgotten password takes never do it.
// EmberStorm asks (GET /button) and says when it was used.

// ButtonWindow is how long five presses leave the owner's password open.
const ButtonWindow = 15 * time.Minute

// presses counts the button: a run of presses ends after a quiet moment.
type presses struct {
	mu    sync.Mutex
	until time.Time
	// code is shown on the box's own screen while the window is open, and a
	// new password needs it: being on the home network is not being at the
	// box (a guest's laptop could have set it first), and the box's screen is
	// somewhere only somebody there can read (the blind security review).
	code        string
	wrong       int
	pausedUntil time.Time
	// resetUntil: an erase or start over asked for in the app (resetMode)
	// waits until then for ResetPresses.
	resetUntil time.Time
	resetMode  ResetMode
}

// EraseWait is how long an erase or start over asked for in the app waits
// for the button.
const EraseWait = 10 * time.Minute

// ResetPresses confirm an erase or start over at the box: ten, so the five a
// forgotten password takes can never empty it (the box's blind security
// review: the two shared one gesture, and the box mostly has no screen to
// say which was waiting).
const ResetPresses = 10

// ArmReset has ResetPresses within EraseWait start mode - an erase, or
// starting over. It is said on the box's own screen (EraseFile), and
// CancelReset takes it back. Asked for through the socket alone - which the
// app's container shares - nothing that cannot be undone happens.
func (u *Updater) ArmReset(mode ResetMode) {
	u.button.mu.Lock()
	u.button.resetUntil = time.Now().Add(EraseWait)
	u.button.resetMode = mode
	u.showReset(u.button.resetUntil, mode)
	u.button.mu.Unlock()
	u.log.Warn("resetting the box was asked for: waiting for the power button", "mode", mode, "presses", ResetPresses)
}

// CancelReset takes back an erase or start over still waiting for the button.
func (u *Updater) CancelReset() {
	u.button.mu.Lock()
	armed := time.Now().Before(u.button.resetUntil)
	u.button.resetUntil = time.Time{}
	u.showReset(time.Time{}, "")
	u.button.mu.Unlock()
	if armed {
		u.log.Warn("resetting the box was cancelled")
	}
}

// EraseFile tells the box's screen (screen.sh, root) an erase or start over
// is waiting: "<until, unix seconds> <mode>". Gone once it is cancelled,
// done or past.
const EraseFile = "erase-waiting"

func (u *Updater) showReset(until time.Time, mode ResetMode) {
	path := filepath.Join(u.cfg.StateDir, EraseFile)
	if until.IsZero() {
		_ = os.Remove(path)
		return
	}
	if err := os.MkdirAll(u.cfg.StateDir, 0o700); err != nil {
		return
	}
	_ = os.WriteFile(path, []byte(fmt.Sprintf("%d %s\n", until.Unix(), mode)), 0o600)
}

// resetArmed is what waits for the button, if anything, and until when.
func (u *Updater) resetArmed() (ResetMode, time.Time) {
	u.button.mu.Lock()
	defer u.button.mu.Unlock()
	if !time.Now().Before(u.button.resetUntil) {
		return "", time.Time{}
	}
	return u.button.resetMode, u.button.resetUntil
}

// maxWrongCodes closes the window: a code of six digits is not guessed
// before it shuts.
const maxWrongCodes = 5

func (u *Updater) buttonOpen() (bool, time.Time) {
	u.button.mu.Lock()
	defer u.button.mu.Unlock()
	return time.Now().Before(u.button.until), u.button.until
}

// ButtonCodeFile is where the code is put for the box's screen (screen.sh,
// root): "<code> <until, unix seconds>", in the caretaker's own folder,
// which nothing else can read. Gone when the window closes.
const ButtonCodeFile = "button-code"

func (u *Updater) showButtonCode(code string, until time.Time) {
	path := filepath.Join(u.cfg.StateDir, ButtonCodeFile)
	if code == "" {
		_ = os.Remove(path)
		return
	}
	if err := os.MkdirAll(u.cfg.StateDir, 0o700); err != nil {
		return
	}
	_ = os.WriteFile(path, []byte(fmt.Sprintf("%s %d\n", code, until.Unix())), 0o600)
}

// buttonCode is the code to show on the box's screen, while the window is open.
func (u *Updater) buttonCode() string {
	u.button.mu.Lock()
	defer u.button.mu.Unlock()
	if !time.Now().Before(u.button.until) {
		return ""
	}
	return u.button.code
}

// claimButton closes the window if code is the one on the screen - or the
// setup code on the sticker, which only the owner has, for a box with no
// screen plugged in (the owner's choice, 2026-10-10) - in one step: two asks
// at once cannot both have it. A wrong code counts, and the fifth closes the
// window too.
func (u *Updater) claimButton(code string) bool {
	u.button.mu.Lock()
	defer u.button.mu.Unlock()
	if !time.Now().Before(u.button.until) || u.button.code == "" || time.Now().Before(u.button.pausedUntil) {
		return false
	}
	screen := subtle.ConstantTimeCompare([]byte(digitsOf(code)), []byte(u.button.code)) == 1
	sticker := false
	if want := u.setupCode(); want != "" {
		sticker = subtle.ConstantTimeCompare([]byte(plainCode(code)), []byte(want)) == 1
	}
	if !screen && !sticker {
		// Every maxWrongCodes wrong codes, a minute's pause; the window
		// closes only after many. Five closing it let anybody at home keep
		// the owner out by sending five on every press (the box's blind
		// review); 50 guesses at a code of six digits stay hopeless.
		u.button.wrong++
		if u.button.wrong%maxWrongCodes == 0 {
			u.button.pausedUntil = time.Now().Add(time.Minute)
		}
		if u.button.wrong >= 10*maxWrongCodes {
			u.button.until, u.button.code = time.Time{}, ""
			u.showButtonCode("", time.Time{})
		}
		return false
	}
	u.button.until, u.button.code = time.Time{}, ""
	u.showButtonCode("", time.Time{})
	return true
}

func (u *Updater) closeButton() {
	u.button.mu.Lock()
	u.button.until, u.button.code = time.Time{}, ""
	u.showButtonCode("", time.Time{})
	u.button.mu.Unlock()
}

// setupCode is the box's setup code, as on its sticker (prepare.sh keeps it
// in the stack's .env), plain; "" when it cannot be read.
func (u *Updater) setupCode() string {
	data, err := os.ReadFile(filepath.Join(u.cfg.ComposeDir, ".env"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "SOUNDSTORM_SETUP_CODE="); ok {
			return plainCode(v)
		}
	}
	return ""
}

// plainCode is a code as typed with case, spaces and dashes set aside.
func plainCode(s string) string {
	return strings.Map(func(c rune) rune {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'z':
			return c
		case c >= 'A' && c <= 'Z':
			return c - 'A' + 'a'
		}
		return -1
	}, s)
}

// digitsOf keeps a typed code's digits, as the screen's code is.
func digitsOf(s string) string {
	return strings.Map(func(c rune) rune {
		if c >= '0' && c <= '9' {
			return c
		}
		return -1
	}, s)
}

// sixDigits is a code to read off a screen and type.
func sixDigits() string {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%06d", n.Int64())
}

// Pressed is a run of presses of the power button, counted once it ends.
func (u *Updater) Pressed(ctx context.Context, n int) {
	switch {
	case n >= 5:
		u.button.mu.Lock()
		if n >= ResetPresses && time.Now().Before(u.button.resetUntil) {
			mode := u.button.resetMode
			u.button.resetUntil = time.Time{}
			u.showReset(time.Time{}, "")
			u.button.mu.Unlock()
			u.log.Warn("the power button was pressed ten times: resetting the box, as asked in the app", "mode", mode)
			// A job under way finishes first: the presses are not lost to
			// it (the box's blind review), for up to two hours.
			go func() {
				for deadline := time.Now().Add(2 * time.Hour); ; {
					err := u.Reset(context.Background(), mode)
					if err == nil {
						return
					}
					if !errors.Is(err, ErrBusy) || time.Now().After(deadline) {
						u.log.Error("reset", "err", err)
						u.set(func(s *Status) {
							s.State = "failed"
							s.Message = "The box could not be reset just now. Ask for it again in the app."
						})
						return
					}
					time.Sleep(10 * time.Second)
				}
			}()
			return
		}
		u.button.until = time.Now().Add(ButtonWindow)
		u.button.code, u.button.wrong = sixDigits(), 0
		u.showButtonCode(u.button.code, u.button.until)
		u.button.mu.Unlock()
		u.log.Warn("the power button was pressed five times: the owner's password can be set for 15 minutes")
	case n == 2:
		u.log.Info("the power button was pressed twice: shutting down")
		_ = u.run(ctx, "systemctl", "poweroff")
	case n == 1:
		u.log.Info("the power button was pressed once: press it twice to shut down")
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

// powerButtons finds every input device that sends the power key on Linux
// (/proc/bus/input/devices): a real PC has more than one - the ACPI button
// (PNP0C0C), the fixed button (LNXPWRBN), sometimes "Intel HID events" - and
// its firmware reports a press on only one of them, which a VM's single
// device never showed (the blind reviews, 2026-10-10). A full keyboard (one
// with an A key) is left out: a power key on a keyboard is not the box's.
// Without a key bitmap, a device named "Power Button" counts.
func powerButtons(devices string) []string {
	var out []string
	for _, block := range strings.Split(devices, "\n\n") {
		var keys, handlers string
		named := strings.Contains(block, `Name="Power Button"`)
		for _, line := range strings.Split(block, "\n") {
			if v, ok := strings.CutPrefix(line, "B: KEY="); ok {
				keys = v
			}
			if v, ok := strings.CutPrefix(line, "H: Handlers="); ok {
				handlers = v
			}
		}
		power := named
		if keys != "" {
			power = keyBit(keys, keyPower) && !keyBit(keys, keyA)
		}
		if !power {
			continue
		}
		for _, h := range strings.Fields(handlers) {
			if strings.HasPrefix(h, "event") {
				out = append(out, "/dev/input/"+h)
				break
			}
		}
	}
	return out
}

// Linux key codes (input-event-codes.h).
const (
	keyA     = 30
	keyPower = 116
)

// keyBit reads one bit of a key bitmap as /proc/bus/input/devices writes it:
// hex words, the most significant first, each as wide as the kernel's long.
func keyBit(bitmap string, bit int) bool {
	words := strings.Fields(bitmap)
	const width = 64
	i := len(words) - 1 - bit/width
	if i < 0 || i >= len(words) {
		return false
	}
	v, err := strconv.ParseUint(words[i], 16, 64)
	if err != nil {
		return false
	}
	return v&(1<<uint(bit%width)) != 0
}
