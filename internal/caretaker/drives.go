package caretaker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// USB drives wait to be opened (the box's blind security review). Plugged
// in, a drive is only noticed (usb.sh seen, from its udev rule): its label,
// filesystem and size written under DrivesWaiting, nothing mounted. Opening
// it - and so the kernel's filesystem code reading whatever a stranger's
// stick holds - happens only once the owner has said "bring in what is on
// it" in the app, which asks through this socket (POST /drives/open), and
// the caretaker runs usb.sh mount, read-only as ever.

// Drive is a USB drive plugged in: its partition, what it calls itself, its
// filesystem, its size, and where it is open, if it is.
type Drive struct {
	Part  string `json:"part"`
	Label string `json:"label"`
	Type  string `json:"type"`
	Size  int64  `json:"size,omitempty"`
	Open  string `json:"open,omitempty"`
}

// drivePart is a USB partition's name as the kernel gives it.
var drivePart = regexp.MustCompile(`^sd[a-z]{1,3}[0-9]{0,3}$`)

// waitingDrives are the drives noticed, opened or not.
func (u *Updater) waitingDrives() []Drive {
	out := []Drive{}
	entries, _ := os.ReadDir(u.cfg.DrivesWaiting)
	for _, e := range entries {
		if !drivePart.MatchString(e.Name()) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(u.cfg.DrivesWaiting, e.Name()))
		if err != nil || len(data) > 4096 {
			continue
		}
		d := Drive{Part: e.Name()}
		for _, line := range strings.Split(string(data), "\n") {
			k, v, _ := strings.Cut(line, "=")
			switch k {
			case "label":
				d.Label = v
			case "type":
				d.Type = v
			case "size":
				d.Size, _ = strconv.ParseInt(v, 10, 64)
			}
		}
		if open, err := os.ReadFile(filepath.Join(u.cfg.DrivesOpen, e.Name())); err == nil {
			d.Open = filepath.Base(strings.TrimSpace(string(open)))
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Part < out[j].Part })
	return out
}

// driveOpening lets one drive be opened at a time: two asks at once mounted
// a drive twice (the box's blind review).
var driveOpening sync.Mutex

// errNoDrive is a drive asked for that is not plugged in.
var errNoDrive = errors.New("no such drive is plugged in")

// openDrive opens a noticed drive read-only (usb.sh mount) and says the
// folder it is open as, which EmberStorm sees under /drives.
func (u *Updater) openDrive(ctx context.Context, part string) (string, error) {
	if !drivePart.MatchString(part) {
		return "", errNoDrive
	}
	if _, err := os.Stat(filepath.Join(u.cfg.DrivesWaiting, part)); err != nil {
		return "", errNoDrive
	}
	driveOpening.Lock()
	defer driveOpening.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := u.run(ctx, u.cfg.USB, "mount", part); err != nil {
		u.log.Warn("could not open a USB drive", "part", part, "err", err)
		return "", errors.New("the drive could not be opened")
	}
	open, err := os.ReadFile(filepath.Join(u.cfg.DrivesOpen, part))
	if err != nil {
		return "", errors.New("the drive could not be opened")
	}
	u.log.Info("a USB drive was opened, as the owner asked", "part", part)
	return filepath.Base(strings.TrimSpace(string(open))), nil
}
