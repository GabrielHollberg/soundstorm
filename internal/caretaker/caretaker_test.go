package caretaker

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const digest = "@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func release(serial int64) *Manifest {
	return &Manifest{Serial: serial, Version: "2026.10." + string(rune('0'+serial)),
		Created: time.Now().Add(-time.Hour).UTC().Truncate(time.Second), Expires: time.Now().Add(30 * 24 * time.Hour).UTC().Truncate(time.Second),
		Images: map[string]string{
			"soundstorm": "ghcr.io/gabrielhollberg/soundstorm" + digest,
			"navidrome":  "deluan/navidrome" + digest,
		}}
}

func keys(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func TestOnlyASignedManifestIsTaken(t *testing.T) {
	pub, priv := keys(t)
	_, stranger := keys(t)
	data, _ := json.Marshal(release(3))

	if m, err := Verify(data, Sign(data, priv), pub); err != nil || m.Serial != 3 {
		t.Fatalf("our own manifest: %v %v", m, err)
	}
	if _, err := Verify(data, Sign(data, stranger), pub); err == nil {
		t.Fatal("a manifest signed by another key was taken")
	}
	changed := []byte(strings.Replace(string(data), "navidrome", "evil", 1))
	if _, err := Verify(changed, Sign(data, priv), pub); err == nil {
		t.Fatal("a manifest changed after signing was taken")
	}
}

func TestAManifestMustPinEveryImage(t *testing.T) {
	for _, ref := range []string{
		"deluan/navidrome:latest",                   // a tag moves
		"deluan/navidrome" + digest + "\n  evil: x", // YAML
		"--privileged" + digest,                     // a flag
	} {
		m := release(1)
		m.Images["navidrome"] = ref
		if m.Validate() == nil {
			t.Errorf("%q was accepted", ref)
		}
	}
}

// box is a pretend box: a manifest server, a EmberStorm health answer and a
// record of what the caretaker ran.
type box struct {
	t        *testing.T
	priv     ed25519.PrivateKey
	u        *Updater
	dir      string
	manifest atomic.Pointer[[]byte]
	healthy  atomic.Bool
	mu       sync.Mutex
	ran      []string
}

func newBox(t *testing.T) *box {
	t.Helper()
	pub, priv := keys(t)
	b := &box{t: t, priv: priv, dir: t.TempDir()}
	b.healthy.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/manifest.json":
			_, _ = w.Write(*b.manifest.Load())
		case "/manifest.json.sig":
			_, _ = w.Write(Sign(*b.manifest.Load(), b.priv))
		case "/healthz":
			if b.healthy.Load() {
				_, _ = io.WriteString(w, `{"status":"ok","sources":9}`)
			} else {
				_, _ = io.WriteString(w, `{"status":"ok","sources":4}`)
			}
		}
	}))
	t.Cleanup(srv.Close)
	cfg := Config{
		ComposeDir:  b.dir,
		Up:          "up",
		StateDir:    filepath.Join(b.dir, "state"),
		Volumes:     filepath.Join(b.dir, "volumes"),
		ManifestURL: srv.URL + "/manifest.json",
		HealthURL:   srv.URL + "/healthz",
		HealthWait:  300 * time.Millisecond,
		Key:         pub,
	}
	b.u = New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	b.u.run = func(_ context.Context, name string, args ...string) error {
		b.mu.Lock()
		defer b.mu.Unlock()
		b.ran = append(b.ran, strings.TrimSpace(name+" "+strings.Join(args, " ")))
		return nil
	}
	// No Docker to ask what is in use: the clean-up after an update does
	// nothing (images_test.go tests it).
	b.u.output = func(context.Context, string, ...string) ([]byte, error) { return nil, io.EOF }
	pollEvery = 20 * time.Millisecond
	return b
}

func (b *box) publish(m *Manifest) {
	data, _ := json.Marshal(m)
	b.manifest.Store(&data)
}

func (b *box) images() string {
	data, _ := os.ReadFile(filepath.Join(b.dir, "compose.images.yml"))
	return string(data)
}

func TestAnUpdateIsInstalledAndAnOldOneIsNot(t *testing.T) {
	b := newBox(t)
	ctx := context.Background()
	b.publish(release(2))
	m, err := b.u.Check(ctx)
	if err != nil || m == nil {
		t.Fatalf("check: %v %v", m, err)
	}
	if err := b.u.Update(ctx, m); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.images(), "deluan/navidrome"+digest) {
		t.Fatalf("images file:\n%s", b.images())
	}
	if s := b.u.Status(); s.State != "updated" || s.Current.Serial != 2 {
		t.Fatalf("status %+v", s)
	}
	// A restart remembers what it runs.
	again := New(b.u.cfg, b.u.log)
	if again.Status().Current == nil || again.Status().Current.Serial != 2 {
		t.Fatal("the version running was not kept")
	}
	// The same release, or an older one replayed, is nothing to do.
	for _, serial := range []int64{2, 1} {
		b.publish(release(serial))
		if m, err := b.u.Check(ctx); err != nil || m != nil {
			t.Fatalf("release %d offered again: %v %v", serial, m, err)
		}
	}
}

func TestAnUnhealthyUpdateIsUndone(t *testing.T) {
	b := newBox(t)
	ctx := context.Background()
	before := "# what the box ran before\n"
	if err := os.WriteFile(filepath.Join(b.dir, "compose.images.yml"), []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	b.publish(release(5))
	m, _ := b.u.Check(ctx)
	// Healthy before, then only four sources once the new version is up.
	b.u.run = func(_ context.Context, name string, args ...string) error {
		b.mu.Lock()
		defer b.mu.Unlock()
		b.ran = append(b.ran, strings.TrimSpace(name+" "+strings.Join(args, " ")))
		if name == "up" && len(args) == 0 {
			b.healthy.Store(false)
		}
		return nil
	}
	if err := b.u.Update(ctx, m); err == nil {
		t.Fatal("an unhealthy update reported success")
	}
	if b.images() != before {
		t.Fatalf("the previous images were not put back:\n%s", b.images())
	}
	if s := b.u.Status(); s.State != "rolled-back" || s.Current != nil {
		t.Fatalf("status %+v", s)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if last := b.ran[len(b.ran)-1]; last != "up" {
		t.Fatalf("the box was not started again; last ran %q", last)
	}
}

func TestADownloadThatFailsChangesNothing(t *testing.T) {
	b := newBox(t)
	ctx := context.Background()
	b.publish(release(2))
	m, _ := b.u.Check(ctx)
	b.u.run = func(_ context.Context, name string, args ...string) error {
		if name == "docker" {
			return io.ErrUnexpectedEOF
		}
		t.Errorf("ran %s %v after a failed download", name, args)
		return nil
	}
	if err := b.u.Update(ctx, m); err == nil {
		t.Fatal("a failed download reported success")
	}
	if b.images() != "" || b.u.Status().State != "failed" {
		t.Fatalf("state %q, images %q", b.u.Status().State, b.images())
	}
}

// A box takes nothing older than it was built with, nothing past its expiry,
// and no release that leaves out a service it runs.
func TestOldExpiredAndPartialReleasesAreRefused(t *testing.T) {
	b := newBox(t)
	ctx := context.Background()
	b.u.cfg.MinSerial = 4
	b.publish(release(3))
	if m, err := b.u.Check(ctx); err != nil || m != nil {
		t.Fatalf("a release older than the box was offered: %v %v", m, err)
	}
	stale := release(5)
	stale.Created, stale.Expires = time.Now().Add(-100*24*time.Hour), time.Now().Add(-10*24*time.Hour)
	b.publish(stale)
	if m, err := b.u.Check(ctx); err == nil || m != nil {
		t.Fatalf("an expired release was offered: %v %v", m, err)
	}
	b.publish(release(5))
	m, err := b.u.Check(ctx)
	if err != nil || m == nil {
		t.Fatalf("check: %v %v", m, err)
	}
	if err := b.u.Update(ctx, m); err != nil {
		t.Fatal(err)
	}
	partial := release(6)
	delete(partial.Images, "navidrome")
	b.publish(partial)
	if m, err := b.u.Check(ctx); err == nil || m != nil {
		t.Fatalf("a release leaving out a service was offered: %v %v", m, err)
	}
}

// A release signed with the backup key is taken; one signed with neither key
// is not; the box's key file holds the release key first, then the backup.
func TestTheBackupKeySignsToo(t *testing.T) {
	b := newBox(t)
	backupPub, backupPriv := keys(t)
	b.u.cfg.Backup = []ed25519.PublicKey{backupPub}
	data, _ := json.Marshal(release(2))
	if _, err := VerifyAny(data, Sign(data, backupPriv), b.u.cfg.Key, backupPub); err != nil {
		t.Fatalf("the backup key's release was refused: %v", err)
	}
	_, stranger := keys(t)
	if _, err := VerifyAny(data, Sign(data, stranger), b.u.cfg.Key, backupPub); err == nil {
		t.Fatal("a stranger's release was taken")
	}
	text := "# release key, then the backup\n" + base64.StdEncoding.EncodeToString(b.u.cfg.Key) + "\n\n" + base64.StdEncoding.EncodeToString(backupPub) + "\n"
	got, err := ParsePublicKeys([]byte(text))
	if err != nil || len(got) != 2 || !got[1].Equal(backupPub) {
		t.Fatalf("keys read: %v %v", got, err)
	}
}
