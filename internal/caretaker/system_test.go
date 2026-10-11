package caretaker

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type bundleFile struct {
	name string
	data string
	mode int64
	kind byte // tar.TypeReg unless said
}

func makeBundle(t *testing.T, files ...bundleFile) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range files {
		kind := f.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		mode := f.mode
		if mode == 0 {
			mode = 0o644
		}
		hdr := &tar.Header{Name: f.name, Mode: mode, Size: int64(len(f.data)), Typeflag: kind, ModTime: time.Unix(0, 0)}
		if kind == tar.TypeSymlink {
			hdr.Size, hdr.Linkname = 0, "/etc/shadow"
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if kind == tar.TypeReg {
			_, _ = tw.Write([]byte(f.data))
		}
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

// withSystem publishes release serial carrying bundle as its system files.
func (b *box) withSystem(serial int64, bundle []byte) *Manifest {
	m := release(serial)
	sum := sha256.Sum256(bundle)
	name := "system-" + string(rune('0'+serial)) + ".tar.gz"
	b.files.Store("/"+name, bundle)
	m.System = &System{File: name, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(bundle)),
		Restart: []string{"soundstorm-screen.service"}}
	return m
}

func (b *box) rootFile(p string) string {
	data, err := os.ReadFile(filepath.Join(b.dir, "root", filepath.FromSlash(p)))
	if err != nil {
		return "<none>"
	}
	return string(data)
}

func (b *box) putRoot(p, data string) {
	full := filepath.Join(b.dir, "root", filepath.FromSlash(p))
	_ = os.MkdirAll(filepath.Dir(full), 0o755)
	_ = os.WriteFile(full, []byte(data), 0o755)
}

func (b *box) commands() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.Join(b.ran, "\n")
}

// The box's own files ride on a release: replaced and added when it comes up
// healthy, the caretaker started again as the new one; and put back exactly,
// files added taken away, when the next release does not.
func TestTheBoxsOwnFilesAreUpdatedAndUndone(t *testing.T) {
	b := newBox(t)
	b.putRoot("usr/local/lib/soundstorm/up.sh", "old up")
	b.putRoot(caretakerPath, "old caretaker")

	m3 := b.withSystem(3, makeBundle(t,
		bundleFile{name: "usr/local/lib/soundstorm/up.sh", data: "up 3", mode: 0o755},
		bundleFile{name: "etc/udev/rules.d/80-emberstorm-eee.rules", data: "rule 3"},
		bundleFile{name: caretakerPath, data: "caretaker 3", mode: 0o755},
	))
	if err := b.u.Update(context.Background(), m3); err != nil {
		t.Fatal(err)
	}
	if got := b.rootFile("usr/local/lib/soundstorm/up.sh"); got != "up 3" {
		t.Fatalf("up.sh after the release: %q", got)
	}
	if got := b.rootFile("etc/udev/rules.d/80-emberstorm-eee.rules"); got != "rule 3" {
		t.Fatalf("the new rule: %q", got)
	}
	if got := b.rootFile(caretakerPath + ".previous"); got != "old caretaker" {
		t.Fatalf("the caretaker before was not kept for the restore unit: %q", got)
	}
	info, _ := os.Stat(filepath.Join(b.dir, "root", "usr/local/lib/soundstorm/up.sh"))
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("a script lost its mode: %v", info.Mode())
	}
	ran := b.commands()
	for _, want := range []string{"systemctl daemon-reload", "systemctl try-restart soundstorm-screen.service", "systemctl --no-block restart soundstorm-caretaker"} {
		if !strings.Contains(ran, want) {
			t.Fatalf("%q was not run:\n%s", want, ran)
		}
	}
	if exists(b.u.systemBackup(3)) {
		t.Fatal("the backup of a release that stayed was left behind")
	}

	// The next one does not come up healthy: every file goes back.
	m4 := b.withSystem(4, makeBundle(t,
		bundleFile{name: "usr/local/lib/soundstorm/up.sh", data: "up 4", mode: 0o755},
		bundleFile{name: "usr/local/lib/soundstorm/new.sh", data: "new 4", mode: 0o755},
	))
	// Unhealthy once started, as a broken release would be.
	run := b.u.run
	b.u.run = func(ctx context.Context, name string, args ...string) error {
		if name == "up" && len(args) == 0 {
			b.healthy.Store(false)
		}
		return run(ctx, name, args...)
	}
	if err := b.u.Update(context.Background(), m4); err == nil {
		t.Fatal("an unhealthy release was kept")
	}
	if got := b.rootFile("usr/local/lib/soundstorm/up.sh"); got != "up 3" {
		t.Fatalf("up.sh was not put back: %q", got)
	}
	if got := b.rootFile("usr/local/lib/soundstorm/new.sh"); got != "<none>" {
		t.Fatalf("a file the failed release added was left: %q", got)
	}
	if got := b.rootFile("etc/udev/rules.d/80-emberstorm-eee.rules"); got != "rule 3" {
		t.Fatalf("a file the failed release did not touch changed: %q", got)
	}
}

// A release whose files leave EmberStorm healthy at home but the box cut off
// from the internet - from every later update - is undone; one of images
// alone is not held to the internet, so an outage cannot undo it.
func TestAReleaseThatCutsTheBoxOffIsUndone(t *testing.T) {
	b := newBox(t)
	b.putRoot("etc/systemd/network/10-emberstorm.network", "good network")
	m3 := b.withSystem(3, makeBundle(t,
		bundleFile{name: "etc/systemd/network/10-emberstorm.network", data: "broken network"},
	))
	run := b.u.run
	b.u.run = func(ctx context.Context, name string, args ...string) error {
		if name == "up" && len(args) == 0 {
			// Started on the new files, the box no longer reaches the channel
			// - until the old ones are back.
			b.offline.Store(b.rootFile("etc/systemd/network/10-emberstorm.network") == "broken network")
		}
		return run(ctx, name, args...)
	}
	if err := b.u.Update(context.Background(), m3); err == nil {
		t.Fatal("a release that cut the box off was kept")
	}
	if got := b.rootFile("etc/systemd/network/10-emberstorm.network"); got != "good network" {
		t.Fatalf("the network settings were not put back: %q", got)
	}

	// Images alone, with the internet down after they start: kept.
	b.u.run = func(ctx context.Context, name string, args ...string) error {
		if name == "up" && len(args) == 0 {
			b.offline.Store(true)
		}
		return run(ctx, name, args...)
	}
	b.offline.Store(false)
	if err := b.u.Update(context.Background(), release(4)); err != nil {
		t.Fatalf("an update of images alone was undone by the internet being down: %v", err)
	}
}

// A bundle with anything a release may not write - outside the box's own
// places, climbing out, a link - or not matching its checksum changes
// nothing at all: not a file, not the stack.
func TestABadSystemBundleChangesNothing(t *testing.T) {
	bad := map[string][]byte{
		"outside":    makeBundle(t, bundleFile{name: "etc/passwd", data: "root::0:0"}),
		"climbing":   makeBundle(t, bundleFile{name: "usr/local/lib/soundstorm/../../../../etc/shadow", data: "x"}),
		"a link":     makeBundle(t, bundleFile{name: "usr/local/lib/soundstorm/up.sh", kind: tar.TypeSymlink}),
		"the loader": makeBundle(t, bundleFile{name: "boot/grub/grub.cfg", data: "x"}),
		"not ours":   makeBundle(t, bundleFile{name: "etc/systemd/system/sshd.service", data: "x"}),
		"keys":       makeBundle(t, bundleFile{name: "etc/soundstorm/release.pub", data: "x"}),
	}
	for name, bundle := range bad {
		t.Run(name, func(t *testing.T) {
			b := newBox(t)
			b.putRoot("usr/local/lib/soundstorm/up.sh", "old up")
			m := b.withSystem(3, bundle)
			if err := b.u.Update(context.Background(), m); err == nil {
				t.Fatal("taken")
			}
			if got := b.rootFile("usr/local/lib/soundstorm/up.sh"); got != "old up" {
				t.Fatalf("a file changed: %q", got)
			}
			if strings.Contains(b.commands(), "up stop") {
				t.Fatal("the stack was stopped for a bundle that was refused")
			}
		})
	}

	b := newBox(t)
	b.putRoot("usr/local/lib/soundstorm/up.sh", "old up")
	m := b.withSystem(3, makeBundle(t, bundleFile{name: "usr/local/lib/soundstorm/up.sh", data: "up 3"}))
	b.files.Store("/"+m.System.File, makeBundle(t, bundleFile{name: "usr/local/lib/soundstorm/up.sh", data: "evil"}))
	if err := b.u.Update(context.Background(), m); err == nil || b.rootFile("usr/local/lib/soundstorm/up.sh") != "old up" {
		t.Fatalf("a bundle not matching its checksum: %v", err)
	}
}

// Cut short with the new files in but nothing started (the power), the next
// start puts them back.
func TestSystemFilesCutShortAreUndoneAtTheNextStart(t *testing.T) {
	b := newBox(t)
	b.putRoot("usr/local/lib/soundstorm/up.sh", "old up")
	m := b.withSystem(3, makeBundle(t, bundleFile{name: "usr/local/lib/soundstorm/up.sh", data: "up 3"}))
	data, _ := json.Marshal(m)
	path, err := b.u.fetchSystem(context.Background(), m.System)
	if err != nil {
		t.Fatal(err)
	}
	files, err := b.u.stageSystem(path)
	if err != nil {
		t.Fatal(err)
	}
	backup := b.u.systemBackup(3)
	if err := b.u.installSystem(files, backup); err != nil {
		t.Fatal(err)
	}
	var mm Manifest
	_ = json.Unmarshal(data, &mm)
	if err := b.u.writePending(pending{Kind: "update", Manifest: &mm, SystemBackup: backup}); err != nil {
		t.Fatal(err)
	}
	if b.rootFile("usr/local/lib/soundstorm/up.sh") != "up 3" {
		t.Fatal("not installed")
	}
	b.u.recoverPending(context.Background())
	if got := b.rootFile("usr/local/lib/soundstorm/up.sh"); got != "old up" {
		t.Fatalf("not put back at the next start: %q", got)
	}
}

func TestOnlyTheBoxsOwnPlacesAreWritable(t *testing.T) {
	for p, want := range map[string]bool{
		"usr/local/lib/soundstorm/up.sh":        true,
		"usr/local/lib/soundstorm/sub/x.sh":     false,
		caretakerPath:                           true,
		"usr/local/bin/docker":                  false,
		"opt/soundstorm/compose.yml":            true,
		"opt/soundstorm/.env":                   false,
		"opt/soundstorm/compose.images.yml":     false,
		"etc/systemd/system/soundstorm.service": true,
		"etc/systemd/system/apt-daily-upgrade.timer.d/soundstorm-night.conf": true,
		"etc/systemd/system/getty@.service":                                  false,
		"etc/systemd/system/multi-user.target.wants/soundstorm.service":      false,
		"etc/udev/rules.d/80-emberstorm-eee.rules":                           true,
		"etc/udev/rules.d/99-evil.rules":                                     false,
		"etc/apt/apt.conf.d/52soundstorm-upgrades":                           true,
		"etc/apt/sources.list.d/soundstorm.sources":                          false,
		"etc/fstab":                                                false,
		"etc/soundstorm/release.pub":                               false,
		"etc/soundstorm/identity.env":                              false,
		"boot/efi/EFI/debian/grub.cfg":                             false,
		"etc/systemd/network/05-emberstorm-no-usb-network.network": true,
	} {
		if got := systemAllowed(p); got != want {
			t.Errorf("%s: %v, want %v", p, got, want)
		}
	}
}
