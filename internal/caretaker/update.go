package caretaker

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// Config says where things are on the box. The zero values are filled in by
// Defaults; tests point them at a temporary folder.
type Config struct {
	// ComposeDir holds compose.yml, compose.images.yml and up.sh's files.
	ComposeDir string
	// Up starts the stack (no arguments) and stops it ("stop").
	Up string
	// Prepare readies the data drive (box/rootfs/.../storage.sh): run again
	// after a reset, so the models built into the box are laid back into the
	// caches it emptied.
	Prepare string
	// Forget is run on a reset, with its mode, once the stack is down: what
	// the box keeps beside the data drive (box/rootfs/.../forget.sh).
	Forget string
	// StateDir is the caretaker's own: the running manifest, the last one.
	StateDir string
	// Volumes is the data drive's subvolume holding every data volume -
	// what is snapshotted before an update and put back after a failed one.
	Volumes string
	// Cache holds what can be made again; Library the media (reset.go).
	Cache   string
	Library string
	// USB opens a drive (usb.sh mount PART); DrivesWaiting holds what it
	// noticed of each drive plugged in, DrivesOpen where each opened one is
	// (drives.go).
	USB           string
	DrivesWaiting string
	DrivesOpen    string
	// ManifestURL is where releases are published; ".sig" beside it.
	ManifestURL string
	// Key is the release key built into the box, and Backup the keys also
	// believed: a backup kept safe, for when the release key is lost.
	Key    ed25519.PublicKey
	Backup []ed25519.PublicKey
	// HealthURL is EmberStorm's /healthz, as reached from the box.
	HealthURL string
	// HealthWait is how long a new version has to come up healthy.
	HealthWait time.Duration
	// MinSerial is the release the box was built with: anything not newer
	// is never installed, though nothing has been installed yet (a fresh box
	// would otherwise take the oldest release still signed - the twelfth
	// security pass).
	MinSerial int64
}

// Defaults fills in the box's own paths for anything left empty.
func (c Config) Defaults() Config {
	set := func(s *string, v string) {
		if *s == "" {
			*s = v
		}
	}
	set(&c.ComposeDir, "/opt/soundstorm")
	set(&c.Up, "/usr/local/lib/soundstorm/up.sh")
	set(&c.Prepare, "/usr/local/lib/soundstorm/storage.sh")
	set(&c.Forget, "/usr/local/lib/soundstorm/forget.sh")
	set(&c.StateDir, "/var/lib/soundstorm-caretaker")
	set(&c.Volumes, "/srv/soundstorm/volumes")
	set(&c.Cache, "/srv/soundstorm/cache")
	set(&c.Library, "/srv/soundstorm/library")
	set(&c.USB, "/usr/local/lib/soundstorm/usb.sh")
	set(&c.DrivesWaiting, "/run/soundstorm/usb-waiting")
	set(&c.DrivesOpen, "/run/soundstorm/usb")
	set(&c.ManifestURL, "https://github.com/GabrielHollberg/soundstorm/releases/download/box-channel/manifest.json")
	set(&c.HealthURL, "http://localhost:8099/healthz")
	if c.HealthWait == 0 {
		c.HealthWait = 10 * time.Minute
	}
	return c
}

// Status is what the app is told.
type Status struct {
	Current   *Manifest `json:"current,omitempty"`
	Available *Manifest `json:"available,omitempty"`
	// State is idle, checking, updating, updated, rolled-back or failed.
	State     string     `json:"state"`
	Message   string     `json:"message,omitempty"`
	LastCheck *time.Time `json:"lastCheck,omitempty"`
}

// Runner runs a command on the box. Tests replace it.
type Runner func(ctx context.Context, name string, args ...string) error

func execRunner(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %w: %s", name, args, err, tail(out))
	}
	return nil
}

func tail(b []byte) string {
	if len(b) > 400 {
		b = b[len(b)-400:]
	}
	return string(b)
}

// Updater does updates, one at a time.
type Updater struct {
	cfg  Config
	run  Runner
	http *http.Client
	log  *slog.Logger
	// output runs a command and answers what it printed (images.go).
	output Output
	// snapshots says whether the volumes can be snapshotted (btrfs).
	snapshots func(string) bool

	mu     sync.Mutex
	busy   sync.Mutex
	status Status
	button presses
}

// New makes an Updater for the box.
func New(cfg Config, log *slog.Logger) *Updater {
	u := &Updater{
		cfg:       cfg.Defaults(),
		run:       execRunner,
		output:    execOutput,
		http:      &http.Client{Timeout: 30 * time.Second},
		log:       log,
		snapshots: isBtrfs,
	}
	u.status.State = "idle"
	u.status.Current, _ = u.load("current.json")
	return u
}

// Status returns a copy of the current status.
func (u *Updater) Status() Status {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.status
}

func (u *Updater) set(f func(*Status)) {
	u.mu.Lock()
	defer u.mu.Unlock()
	f(&u.status)
}

func (u *Updater) load(name string) (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(u.cfg.StateDir, name))
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func (u *Updater) save(name string, m *Manifest) error {
	if err := os.MkdirAll(u.cfg.StateDir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(filepath.Join(u.cfg.StateDir, name), data)
}

// writeFile replaces a file whole: written beside it, then renamed over.
func writeFile(path string, data []byte) error {
	tmp := path + ".new"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (u *Updater) fetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := u.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

// Check asks for the newest release. It answers the manifest when it is newer
// than the one running, and nil when there is nothing to do.
func (u *Updater) Check(ctx context.Context) (*Manifest, error) {
	u.set(func(s *Status) { s.State = "checking"; s.Message = "" })
	m, err := u.check(ctx)
	u.set(func(s *Status) {
		now := time.Now()
		s.LastCheck = &now
		s.State = "idle"
		if err != nil {
			s.Message = "Could not check for updates: " + err.Error()
			return
		}
		s.Available = m
	})
	return m, err
}

func (u *Updater) check(ctx context.Context) (*Manifest, error) {
	data, err := u.fetch(ctx, u.cfg.ManifestURL)
	if err != nil {
		return nil, err
	}
	sig, err := u.fetch(ctx, u.cfg.ManifestURL+".sig")
	if err != nil {
		return nil, err
	}
	m, err := VerifyAny(data, sig, append([]ed25519.PublicKey{u.cfg.Key}, u.cfg.Backup...)...)
	if err != nil {
		return nil, err
	}
	if time.Now().After(m.Expires) {
		return nil, errors.New("the published release has expired; it will be signed again")
	}
	if m.Serial <= u.cfg.MinSerial {
		return nil, nil
	}
	cur := u.Status().Current
	if cur != nil && m.Serial <= cur.Serial {
		return nil, nil
	}
	// A release names every service the running one does: one left out
	// would carry on as it is, unnoticed.
	if cur != nil {
		for svc := range cur.Images {
			if _, ok := m.Images[svc]; !ok {
				return nil, fmt.Errorf("the published release leaves out %s", svc)
			}
		}
	}
	return m, nil
}

// Health is what EmberStorm's /healthz says.
type Health struct {
	Status  string `json:"status"`
	Sources int    `json:"sources"`
}

func (u *Updater) health(ctx context.Context) (Health, error) {
	var h Health
	data, err := u.fetch(ctx, u.cfg.HealthURL)
	if err != nil {
		return h, err
	}
	return h, json.Unmarshal(data, &h)
}

// ErrBusy is an update asked for while one is under way.
var ErrBusy = errors.New("an update is already under way")

// Update moves the box to m: the new images are downloaded first (nothing
// changes if that fails), then the stack is stopped, the volumes snapshotted,
// the new images started, and EmberStorm given HealthWait to answer as healthy
// with at least as many sources as before. If it does not, everything is put
// back as it was - images and volumes both, since a new version may have
// changed its database on the way up - and the update is reported as undone.
func (u *Updater) Update(ctx context.Context, m *Manifest) error {
	if !u.busy.TryLock() {
		return ErrBusy
	}
	defer u.busy.Unlock()
	if err := m.Validate(); err != nil {
		return err
	}
	// Checked again here: a release found before its expiry and installed
	// after it is past (the box's blind security review).
	if time.Now().After(m.Expires) {
		return u.fail(errors.New("this release has expired"), "That update is too old to install. The box will look for a newer one.")
	}
	u.set(func(s *Status) { s.State = "updating"; s.Message = "Downloading EmberStorm " + m.Version })

	before, _ := u.health(ctx)
	// What the running version says of itself is not believed past what
	// the box last saw installed healthy: a compromised version could
	// claim more sources than any new one has, and so have every update -
	// the one that fixes it included - rolled back for ever (the thirteenth
	// security pass).
	before.Sources = min(before.Sources, u.baseline())

	for svc, ref := range m.Images {
		if err := u.runFor(ctx, pullTimeout, "docker", "pull", "-q", ref); err != nil {
			return u.fail(fmt.Errorf("downloading %s: %w", svc, err), "The update could not be downloaded. Nothing was changed.")
		}
	}

	imagesPath := filepath.Join(u.cfg.ComposeDir, "compose.images.yml")
	previous, err := os.ReadFile(imagesPath)
	hadPrevious := err == nil

	// Written down before anything is stopped, so a power cut from here on
	// is finished or undone at the next start (pending.go).
	_ = os.MkdirAll(u.cfg.StateDir, 0o755)
	if hadPrevious {
		if err := writeFile(filepath.Join(u.cfg.StateDir, previousImages), previous); err != nil {
			return u.fail(err, "The update could not be prepared. Nothing was changed.")
		}
	}
	p := pending{Kind: "update", Manifest: m, HadPrevious: hadPrevious}
	if err := u.writePending(p); err != nil {
		return u.fail(err, "The update could not be prepared. Nothing was changed.")
	}

	u.set(func(s *Status) { s.Message = "Installing EmberStorm " + m.Version })
	if err := u.runFor(ctx, stopTimeout, u.cfg.Up, "stop"); err != nil {
		_ = u.runFor(ctx, upTimeout, u.cfg.Up)
		u.clearPending()
		return u.fail(err, "The update could not stop EmberStorm. Nothing was changed.")
	}
	snap := ""
	if u.snapshots(u.cfg.Volumes) {
		snap = u.snapshotPath(m.Serial)
		if err := u.run(ctx, "btrfs", "subvolume", "snapshot", u.cfg.Volumes, snap); err != nil {
			_ = u.runFor(ctx, upTimeout, u.cfg.Up)
			u.clearPending()
			return u.fail(err, "The update could not save a copy of your settings first, so it was not installed.")
		}
		p.Snapshot = snap
		if err := u.writePending(p); err != nil {
			u.log.Warn("could not note the snapshot", "err", err)
		}
	}
	if err := writeFile(imagesPath, ImagesFile(m)); err != nil {
		u.rollback(ctx, imagesPath, previous, snap)
		u.clearPending()
		return u.fail(err, "The update could not be installed. EmberStorm is back as it was.")
	}
	if err := u.runFor(ctx, upTimeout, u.cfg.Up); err != nil || !u.healthy(ctx, before) {
		back := u.rollback(ctx, imagesPath, previous, snap)
		u.clearPending()
		if err == nil {
			err = errors.New("did not come up healthy")
		}
		u.log.Warn("update undone", "version", m.Version, "err", err, "back", back)
		u.set(func(s *Status) {
			if !back {
				// Said as it is: the box may be on the old version with the
				// new one's data, or not running.
				s.State = "failed"
				s.Message = "EmberStorm " + m.Version + " did not start properly, and going back to the version you had did not work either. Turn the box off and on; if it still does not start, contact support."
				return
			}
			s.State = "rolled-back"
			s.Message = "EmberStorm " + m.Version + " did not start properly, so the box went back to the version you had. Nothing was lost."
		})
		return err
	}
	u.record(ctx, m, snap, previous)
	u.clearPending()
	u.log.Info("updated", "version", m.Version, "serial", m.Serial)
	return nil
}

// record makes m the box's version once it is up and healthy, and tidies
// what the update before it left.
func (u *Updater) record(ctx context.Context, m *Manifest, snap string, previous []byte) {
	if h, err := u.health(ctx); err == nil {
		u.saveBaseline(h.Sources)
	}
	cur := u.Status().Current
	if cur != nil {
		_ = u.save("previous.json", cur)
	}
	if err := u.save("current.json", m); err != nil {
		u.log.Warn("could not record the version", "err", err)
	}
	u.pruneSnapshots(ctx, snap)
	if n := u.pruneImages(ctx, m, cur, previous); n > 0 {
		u.log.Info("removed old versions", "images", n)
	}
	u.set(func(s *Status) {
		s.Current = m
		s.Available = nil
		s.State = "updated"
		s.Message = "Updated to EmberStorm " + m.Version + "."
	})
}

// How long each step may take before it counts as failed: a download that
// hangs, or a start that never ends, used to hold the box "updating" for
// ever, with nothing else - a reset, the next update - able to start.
const (
	pullTimeout = 30 * time.Minute
	stopTimeout = 5 * time.Minute
	upTimeout   = 15 * time.Minute
)

// runFor runs a command, given at most d.
func (u *Updater) runFor(ctx context.Context, d time.Duration, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	return u.run(ctx, name, args...)
}

func (u *Updater) fail(err error, message string) error {
	u.log.Warn("update failed", "err", err)
	u.set(func(s *Status) { s.State = "failed"; s.Message = message })
	return err
}

// healthy waits for EmberStorm to say ok with at least the sources it had.
func (u *Updater) healthy(ctx context.Context, before Health) bool {
	deadline := time.Now().Add(u.cfg.HealthWait)
	for time.Now().Before(deadline) {
		h, err := u.health(ctx)
		if err == nil && h.Status == "ok" && h.Sources >= before.Sources {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(pollEvery):
		}
	}
	return false
}

var pollEvery = 5 * time.Second

// rollback puts the previous images and, when there is one, the volumes'
// snapshot back, and starts the stack again. It says whether all of that
// worked: a rollback that could not put the volumes back was reported as
// nothing lost (the thirteenth security pass).
func (u *Updater) rollback(ctx context.Context, imagesPath string, previous []byte, snap string) bool {
	ok := true
	_ = u.runFor(ctx, stopTimeout, u.cfg.Up, "stop")
	if snap != "" && !exists(u.cfg.Volumes) && exists(snap) {
		// A rollback cut short after setting the volumes aside: the copy
		// goes straight back.
		if err := os.Rename(snap, u.cfg.Volumes); err != nil {
			u.log.Error("could not put the volumes back", "err", err)
			ok = false
		}
	} else if snap != "" && exists(snap) {
		// A name of its own each time: one left from an earlier failure
		// that could not be deleted stopped the volumes being set aside.
		failed := u.cfg.Volumes + "-failed-" + strconv.FormatInt(time.Now().UnixNano(), 36)
		if err := os.Rename(u.cfg.Volumes, failed); err != nil {
			u.log.Error("could not set the updated volumes aside", "err", err)
			ok = false
		} else if err := os.Rename(snap, u.cfg.Volumes); err != nil {
			u.log.Error("could not put the volumes back", "err", err)
			_ = os.Rename(failed, u.cfg.Volumes)
			ok = false
		} else {
			_ = u.run(ctx, "btrfs", "subvolume", "delete", failed)
		}
	}
	if previous != nil {
		if err := writeFile(imagesPath, previous); err != nil {
			ok = false
		}
	} else {
		_ = os.Remove(imagesPath)
	}
	if err := u.runFor(ctx, upTimeout, u.cfg.Up); err != nil {
		u.log.Error("could not start the previous version again", "err", err)
		ok = false
	}
	return ok
}

// maxBaseline is the most sources a health check is ever held to.
const maxBaseline = 32

// baseline is how many sources the box last saw a version come up healthy
// with, or maxBaseline before it has seen one.
func (u *Updater) baseline() int {
	var b struct {
		Sources int `json:"sources"`
	}
	data, err := os.ReadFile(filepath.Join(u.cfg.StateDir, "baseline.json"))
	if err != nil || json.Unmarshal(data, &b) != nil || b.Sources <= 0 {
		return maxBaseline
	}
	return min(b.Sources, maxBaseline)
}

func (u *Updater) saveBaseline(n int) {
	if n <= 0 {
		return
	}
	data, _ := json.Marshal(map[string]int{"sources": min(n, maxBaseline)})
	_ = os.MkdirAll(u.cfg.StateDir, 0o755)
	if err := writeFile(filepath.Join(u.cfg.StateDir, "baseline.json"), data); err != nil {
		u.log.Warn("could not record the sources seen", "err", err)
	}
}

// pruneSnapshots keeps the newest snapshot, for going back by hand, and
// deletes the rest.
func (u *Updater) pruneSnapshots(ctx context.Context, keep string) {
	matches, _ := filepath.Glob(u.cfg.Volumes + "-before-*")
	for _, p := range matches {
		if p != keep {
			_ = u.run(ctx, "btrfs", "subvolume", "delete", p)
		}
	}
}
