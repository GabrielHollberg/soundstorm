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
	// StateDir is the caretaker's own: the running manifest, the last one.
	StateDir string
	// Volumes is the data drive's subvolume holding every data volume -
	// what is snapshotted before an update and put back after a failed one.
	Volumes string
	// ManifestURL is where releases are published; ".sig" beside it.
	ManifestURL string
	// Key is the release key built into the box.
	Key ed25519.PublicKey
	// HealthURL is SoundStorm's /healthz, as reached from the box.
	HealthURL string
	// HealthWait is how long a new version has to come up healthy.
	HealthWait time.Duration
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
	set(&c.StateDir, "/var/lib/soundstorm-caretaker")
	set(&c.Volumes, "/srv/soundstorm/volumes")
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
	// snapshots says whether the volumes can be snapshotted (btrfs).
	snapshots func(string) bool

	mu     sync.Mutex
	busy   sync.Mutex
	status Status
}

// New makes an Updater for the box.
func New(cfg Config, log *slog.Logger) *Updater {
	u := &Updater{
		cfg:       cfg.Defaults(),
		run:       execRunner,
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
	m, err := Verify(data, sig, u.cfg.Key)
	if err != nil {
		return nil, err
	}
	if cur := u.Status().Current; cur != nil && m.Serial <= cur.Serial {
		return nil, nil
	}
	return m, nil
}

// Health is what SoundStorm's /healthz says.
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
// the new images started, and SoundStorm given HealthWait to answer as healthy
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
	u.set(func(s *Status) { s.State = "updating"; s.Message = "Downloading SoundStorm " + m.Version })

	before, _ := u.health(ctx)

	for svc, ref := range m.Images {
		if err := u.run(ctx, "docker", "pull", "-q", ref); err != nil {
			return u.fail(fmt.Errorf("downloading %s: %w", svc, err), "The update could not be downloaded. Nothing was changed.")
		}
	}

	imagesPath := filepath.Join(u.cfg.ComposeDir, "compose.images.yml")
	previous, _ := os.ReadFile(imagesPath)

	u.set(func(s *Status) { s.Message = "Installing SoundStorm " + m.Version })
	if err := u.run(ctx, u.cfg.Up, "stop"); err != nil {
		return u.fail(err, "The update could not stop SoundStorm. Nothing was changed.")
	}
	snap := ""
	if u.snapshots(u.cfg.Volumes) {
		snap = u.cfg.Volumes + "-before-" + strconv.FormatInt(m.Serial, 10)
		if err := u.run(ctx, "btrfs", "subvolume", "snapshot", u.cfg.Volumes, snap); err != nil {
			_ = u.run(ctx, u.cfg.Up)
			return u.fail(err, "The update could not save a copy of your settings first, so it was not installed.")
		}
	}
	if err := writeFile(imagesPath, ImagesFile(m)); err != nil {
		u.rollback(ctx, imagesPath, previous, snap)
		return u.fail(err, "The update could not be installed. SoundStorm is back as it was.")
	}
	if err := u.run(ctx, u.cfg.Up); err != nil || !u.healthy(ctx, before) {
		u.rollback(ctx, imagesPath, previous, snap)
		if err == nil {
			err = errors.New("did not come up healthy")
		}
		u.log.Warn("update undone", "version", m.Version, "err", err)
		u.set(func(s *Status) {
			s.State = "rolled-back"
			s.Message = "SoundStorm " + m.Version + " did not start properly, so the box went back to the version you had. Nothing was lost."
		})
		return err
	}

	if cur := u.Status().Current; cur != nil {
		_ = u.save("previous.json", cur)
	}
	if err := u.save("current.json", m); err != nil {
		u.log.Warn("could not record the version", "err", err)
	}
	u.pruneSnapshots(ctx, snap)
	u.set(func(s *Status) {
		s.Current = m
		s.Available = nil
		s.State = "updated"
		s.Message = "Updated to SoundStorm " + m.Version + "."
	})
	u.log.Info("updated", "version", m.Version, "serial", m.Serial)
	return nil
}

func (u *Updater) fail(err error, message string) error {
	u.log.Warn("update failed", "err", err)
	u.set(func(s *Status) { s.State = "failed"; s.Message = message })
	return err
}

// healthy waits for SoundStorm to say ok with at least the sources it had.
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
// snapshot back, and starts the stack again.
func (u *Updater) rollback(ctx context.Context, imagesPath string, previous []byte, snap string) {
	_ = u.run(ctx, u.cfg.Up, "stop")
	if snap != "" {
		failed := u.cfg.Volumes + "-failed"
		_ = u.run(ctx, "btrfs", "subvolume", "delete", failed)
		if err := os.Rename(u.cfg.Volumes, failed); err != nil {
			u.log.Error("could not set the updated volumes aside", "err", err)
		} else if err := os.Rename(snap, u.cfg.Volumes); err != nil {
			u.log.Error("could not put the volumes back", "err", err)
			_ = os.Rename(failed, u.cfg.Volumes)
		} else {
			_ = u.run(ctx, "btrfs", "subvolume", "delete", failed)
		}
	}
	if previous != nil {
		_ = writeFile(imagesPath, previous)
	} else {
		_ = os.Remove(imagesPath)
	}
	if err := u.run(ctx, u.cfg.Up); err != nil {
		u.log.Error("could not start the previous version again", "err", err)
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
