package caretaker

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// pending is an update or a reset under way, written down before anything is
// stopped and taken away once it is over, so a power cut part way is finished
// or undone at the next start (recoverPending) - where it used to leave a box
// with half its new images, or its volumes set aside and nothing put back,
// or half erased (the blind reviews, 2026-10-10).
type pending struct {
	Kind string `json:"kind"` // "update" or "reset"
	// An update: the release being installed, the snapshot of the volumes
	// once made, and whether there was an images file before (its contents
	// are kept beside, previousImages).
	Manifest    *Manifest `json:"manifest,omitempty"`
	Snapshot    string    `json:"snapshot,omitempty"`
	HadPrevious bool      `json:"hadPrevious,omitempty"`
	// A reset: what it takes away.
	Mode ResetMode `json:"mode,omitempty"`
}

const (
	pendingFile    = "pending.json"
	previousImages = "images.previous.yml"
)

func (u *Updater) writePending(p pending) error {
	if err := os.MkdirAll(u.cfg.StateDir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return writeFile(filepath.Join(u.cfg.StateDir, pendingFile), data)
}

func (u *Updater) readPending() (pending, bool) {
	var p pending
	data, err := os.ReadFile(filepath.Join(u.cfg.StateDir, pendingFile))
	if err != nil || json.Unmarshal(data, &p) != nil {
		return p, false
	}
	return p, p.Kind == "update" || p.Kind == "reset"
}

func (u *Updater) clearPending() {
	_ = os.Remove(filepath.Join(u.cfg.StateDir, pendingFile))
	_ = os.Remove(filepath.Join(u.cfg.StateDir, previousImages))
}

// snapshotPath is where an update's snapshot of the volumes goes: a name of
// its own when one is already there (an earlier attempt at the same release
// cut short), since btrfs snapshots onto an existing folder by putting the
// copy inside it - and the rollback would then have put back a folder
// holding the volumes, not the volumes.
func (u *Updater) snapshotPath(serial int64) string {
	p := u.cfg.Volumes + "-before-" + strconv.FormatInt(serial, 10)
	if _, err := os.Lstat(p); err == nil {
		p += "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return p
}

// recoverPending finishes or undoes what a power cut interrupted. An update
// is kept if EmberStorm comes up healthy on it, else put back as it was; a
// reset is done again from the start - it takes away the same things, and
// somebody asked for it.
func (u *Updater) recoverPending(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			u.log.Error("finishing what was under way stopped", "panic", r)
		}
	}()
	p, ok := u.readPending()
	if !ok {
		return
	}
	switch p.Kind {
	case "reset":
		u.log.Warn("a reset was cut short; doing it again", "mode", p.Mode)
		if err := u.Reset(ctx, p.Mode); err != nil {
			u.log.Error("finishing the reset", "err", err)
		}
	case "update":
		u.finishUpdate(ctx, p)
	}
}

func (u *Updater) finishUpdate(ctx context.Context, p pending) {
	if !u.busy.TryLock() {
		return
	}
	defer u.busy.Unlock()
	m := p.Manifest
	if m == nil {
		u.clearPending()
		return
	}
	// Already recorded: it was cut short only in the tidying after.
	if cur := u.Status().Current; cur != nil && cur.Serial == m.Serial {
		u.clearPending()
		return
	}
	imagesPath := filepath.Join(u.cfg.ComposeDir, "compose.images.yml")
	var previous []byte
	if p.HadPrevious {
		data, err := os.ReadFile(filepath.Join(u.cfg.StateDir, previousImages))
		if err != nil {
			u.log.Error("the version from before the update is not known; leaving the box as it is", "err", err)
			u.clearPending()
			return
		}
		previous = data
	}
	u.set(func(s *Status) { s.State = "updating"; s.Message = "Finishing the update to EmberStorm " + m.Version })
	installed, _ := os.ReadFile(imagesPath)
	// Kept only when the update got as far as starting: its images named,
	// the volumes in place, and the snapshot still beside them (gone, a
	// rollback was under way and the volumes are already the old ones).
	if string(installed) == string(ImagesFile(m)) && exists(u.cfg.Volumes) && (p.Snapshot == "" || exists(p.Snapshot)) {
		// Its images were written: started (or started again) and healthy,
		// it is kept.
		_ = u.runFor(ctx, upTimeout, u.cfg.Up)
		// Held to the sources last seen healthy; with none recorded yet,
		// answering ok with any is enough.
		want := u.baseline()
		if want == maxBaseline {
			want = 1
		}
		if u.healthy(ctx, Health{Sources: want}) {
			u.log.Warn("an update cut short came up healthy; keeping it", "version", m.Version)
			u.record(ctx, m, p.Snapshot, previous)
			u.clearPending()
			return
		}
	}
	back := u.rollback(ctx, imagesPath, previous, p.Snapshot)
	u.markRolledBack(m.Serial)
	u.clearPending()
	u.log.Warn("an update cut short was undone", "version", m.Version, "back", back)
	u.set(func(s *Status) {
		if !back {
			s.State = "failed"
			s.Message = "The update to EmberStorm " + m.Version + " was cut short, and going back to the version you had did not work. Turn the box off and on; if it still does not start, contact support."
			return
		}
		s.State = "rolled-back"
		s.Message = "The update to EmberStorm " + m.Version + " was cut short, so the box went back to the version you had. Nothing was lost."
	})
}

// exists says whether a path is there.
func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil || !errors.Is(err, os.ErrNotExist)
}
