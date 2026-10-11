package caretaker

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// A release can carry the box's own files (Manifest.System): its scripts,
// services and settings and the caretaker itself, which only images used to
// reach - so a fix to any of them meant the USB stick at the box. They go in
// with the images, in the same transaction: every file replaced is kept
// first, and if the release does not come up healthy the old files go back
// with the old images and volumes, a power cut part way included
// (pending.go). A caretaker that will not start is put back by systemd
// (soundstorm-caretaker-restore.service).

// caretakerPath is the caretaker itself, relative to the system's root; the
// one before an update is kept beside it for the restore unit.
const caretakerPath = "usr/local/bin/soundstorm-caretaker"

// systemAllowed says whether a release may write a file: only the box's own,
// named for it - never the start-up loader, the disk layout, accounts, the
// release keys or anything of the people's. A path is relative to the root,
// already cleaned.
func systemAllowed(p string) bool {
	ours := func(name string) bool {
		return strings.Contains(name, "soundstorm") || strings.Contains(name, "emberstorm")
	}
	base := path.Base(p)
	switch {
	case p == caretakerPath,
		p == "opt/soundstorm/compose.yml",
		p == "opt/soundstorm/compose.box.yml",
		p == "etc/docker/daemon.json",
		p == "etc/systemd/zram-generator.conf",
		p == "etc/systemd/system/ssh-hostkeys.service":
		return true
	case strings.HasPrefix(p, "usr/local/lib/soundstorm/") && !strings.Contains(strings.TrimPrefix(p, "usr/local/lib/soundstorm/"), "/"):
		return true
	case strings.HasPrefix(p, "etc/systemd/system/"):
		// A unit of ours, or a drop-in of ours for another unit.
		rest := strings.TrimPrefix(p, "etc/systemd/system/")
		if !strings.Contains(rest, "/") {
			return ours(base)
		}
		dir := path.Dir(rest)
		return strings.HasSuffix(dir, ".d") && !strings.Contains(dir, "/") && ours(base)
	}
	// Settings folders of one level, each file of ours named so.
	for _, dir := range []string{
		"etc/systemd/journald.conf.d/", "etc/systemd/logind.conf.d/", "etc/systemd/network/",
		"etc/udev/rules.d/", "etc/tmpfiles.d/", "etc/apt/apt.conf.d/",
	} {
		if rest, ok := strings.CutPrefix(p, dir); ok {
			return rest != "" && !strings.Contains(rest, "/") && ours(base)
		}
	}
	return false
}

// systemFile is a file of a bundle, staged.
type stagedFile struct {
	path string // relative to the root
	mode os.FileMode
}

const (
	maxSystemFiles = 2000
	maxSystemBytes = 400 << 20
)

// fetchSystem downloads the release's bundle, from beside its manifest, and
// checks it against the signed checksum. It answers where it put it.
func (u *Updater) fetchSystem(ctx context.Context, s *System) (string, error) {
	base, err := url.Parse(u.cfg.ManifestURL)
	if err != nil {
		return "", err
	}
	ref, _ := url.Parse(s.File) // checked by validate: a plain file name
	where := base.ResolveReference(ref).String()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, where, nil)
	if err != nil {
		return "", err
	}
	client := *u.http
	client.Timeout = pullTimeout
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: %s", where, resp.Status)
	}
	if err := os.MkdirAll(u.cfg.StateDir, 0o700); err != nil {
		return "", err
	}
	out := filepath.Join(u.cfg.StateDir, "system.tar.gz")
	f, err := os.OpenFile(out, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, s.Size+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", err
	}
	if n != s.Size || hex.EncodeToString(h.Sum(nil)) != s.SHA256 {
		_ = os.Remove(out)
		return "", errors.New("the system files do not match the release's checksum")
	}
	return out, nil
}

// stageSystem unpacks a bundle into StateDir/system-new: regular files only,
// each where systemAllowed says a release may write. Anything else in it -
// a link, a device, a path climbing out, a file elsewhere - refuses the
// whole bundle: it was not made by release.sh.
func (u *Updater) stageSystem(bundle string) ([]stagedFile, error) {
	staged := filepath.Join(u.cfg.StateDir, "system-new")
	if err := os.RemoveAll(staged); err != nil {
		return nil, err
	}
	f, err := os.Open(bundle)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(bufio.NewReader(f))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var files []stagedFile
	var total int64
	seen := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		name := strings.TrimPrefix(hdr.Name, "./")
		if hdr.Typeflag == tar.TypeDir {
			continue
		}
		if hdr.Typeflag != tar.TypeReg {
			return nil, fmt.Errorf("the system files hold %s, which is not a plain file", hdr.Name)
		}
		clean := path.Clean(name)
		if clean != name || path.IsAbs(clean) || strings.HasPrefix(clean, "../") || clean == ".." {
			return nil, fmt.Errorf("the system files hold a bad path: %s", hdr.Name)
		}
		if !systemAllowed(clean) {
			return nil, fmt.Errorf("the system files hold %s, which a release may not change", clean)
		}
		if seen[clean] {
			return nil, fmt.Errorf("the system files hold %s twice", clean)
		}
		seen[clean] = true
		if len(files) >= maxSystemFiles {
			return nil, errors.New("the system files hold too many files")
		}
		total += hdr.Size
		if hdr.Size < 0 || total > maxSystemBytes {
			return nil, errors.New("the system files are too big")
		}
		mode := os.FileMode(0o644)
		if hdr.Mode&0o111 != 0 {
			mode = 0o755
		}
		dst := filepath.Join(staged, filepath.FromSlash(clean))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return nil, err
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, err
		}
		_, err = io.Copy(out, io.LimitReader(tr, hdr.Size))
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return nil, err
		}
		files = append(files, stagedFile{path: clean, mode: mode})
	}
	if len(files) == 0 {
		return nil, errors.New("the system files are empty")
	}
	return files, nil
}

// createdList is the backup's record of the files an update added, which
// going back deletes.
const createdList = ".created"

// installSystem puts the staged files in place. Each file replaced is first
// copied into backup, and each new one noted there (before it is written:
// a power cut after leaves nothing unknown), so restoreSystem can put the
// box's files back exactly. Each file is written beside its place and
// renamed over it. A caretaker replaced is also kept as .previous, for the
// restore unit.
func (u *Updater) installSystem(files []stagedFile, backup string) error {
	staged := filepath.Join(u.cfg.StateDir, "system-new")
	if err := os.MkdirAll(backup, 0o700); err != nil {
		return err
	}
	created, err := os.OpenFile(filepath.Join(backup, createdList), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer created.Close()
	for _, f := range files {
		src := filepath.Join(staged, filepath.FromSlash(f.path))
		dst := filepath.Join(u.root(), filepath.FromSlash(f.path))
		data, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		old, err := os.ReadFile(dst)
		switch {
		case err == nil:
			if bytes.Equal(old, data) {
				if info, e := os.Stat(dst); e == nil && info.Mode().Perm() == f.mode {
					continue
				}
			}
			info, _ := os.Stat(dst)
			keep := filepath.Join(backup, filepath.FromSlash(f.path))
			if err := os.MkdirAll(filepath.Dir(keep), 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(keep, old, info.Mode().Perm()); err != nil {
				return err
			}
			if f.path == caretakerPath {
				if err := writeMode(dst+".previous", old, 0o755); err != nil {
					return err
				}
			}
		case errors.Is(err, os.ErrNotExist):
			if _, err := fmt.Fprintln(created, f.path); err != nil {
				return err
			}
			if err := created.Sync(); err != nil {
				return err
			}
		default:
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := writeMode(dst, data, f.mode); err != nil {
			return err
		}
	}
	return nil
}

// restoreSystem puts back what installSystem changed: the files it kept,
// and deletes those it added. It answers whether all of it worked.
func (u *Updater) restoreSystem(backup string) bool {
	if backup == "" || !exists(backup) {
		return true
	}
	ok := true
	err := filepath.Walk(backup, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(backup, p)
		if rel == createdList {
			return nil
		}
		data, err := os.ReadFile(p)
		if err == nil {
			err = writeMode(filepath.Join(u.root(), rel), data, info.Mode().Perm())
		}
		if err != nil {
			u.log.Error("could not put a system file back", "file", rel, "err", err)
			ok = false
		}
		return nil
	})
	if err != nil {
		ok = false
	}
	if list, err := os.ReadFile(filepath.Join(backup, createdList)); err == nil {
		for _, line := range strings.Split(string(list), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || !systemAllowed(line) {
				continue
			}
			if err := os.Remove(filepath.Join(u.root(), filepath.FromSlash(line))); err != nil && !errors.Is(err, os.ErrNotExist) {
				ok = false
			}
		}
	}
	return ok
}

// applySystem tells the system its files changed: units and udev rules read
// again, tmpfiles made, the release's units switched on and started again.
// Errors are logged, not fatal: the files are in, and EmberStorm's health
// decides whether the release stays.
func (u *Updater) applySystem(ctx context.Context, s *System) {
	for _, c := range [][]string{
		{"systemctl", "daemon-reload"},
		{"udevadm", "control", "--reload"},
		{"systemd-tmpfiles", "--create"},
	} {
		if err := u.runFor(ctx, stopTimeout, c[0], c[1:]...); err != nil {
			u.log.Warn("after the system files", "command", c[0], "err", err)
		}
	}
	if s == nil {
		return
	}
	if len(s.Enable) > 0 {
		if err := u.runFor(ctx, stopTimeout, "systemctl", append([]string{"enable"}, s.Enable...)...); err != nil {
			u.log.Warn("switching units on", "err", err)
		}
	}
	if len(s.Restart) > 0 {
		if err := u.runFor(ctx, stopTimeout, "systemctl", append([]string{"try-restart"}, s.Restart...)...); err != nil {
			u.log.Warn("starting units again", "err", err)
		}
	}
}

// installPackages installs the release's Debian packages (already installed
// ones are left as they are), before any file changes: a package that cannot
// be had stops the release with nothing changed.
func (u *Updater) installPackages(ctx context.Context, s *System) error {
	if s == nil || len(s.Packages) == 0 {
		return nil
	}
	if err := u.runFor(ctx, pullTimeout, "env", "DEBIAN_FRONTEND=noninteractive", "apt-get", "update", "-q"); err != nil {
		return err
	}
	args := append([]string{"DEBIAN_FRONTEND=noninteractive", "apt-get", "install", "-y", "-q", "--no-install-recommends"}, s.Packages...)
	return u.runFor(ctx, pullTimeout, "env", args...)
}

// root is the system's root: "/" on a box, a folder in the tests.
func (u *Updater) root() string {
	if u.cfg.SystemRoot != "" {
		return u.cfg.SystemRoot
	}
	return "/"
}

// systemBackup is where an update keeps the files it replaces.
func (u *Updater) systemBackup(serial int64) string {
	return filepath.Join(u.cfg.StateDir, fmt.Sprintf("system-before-%d", serial))
}

// caretakerChanged is whether an update replaced the caretaker, which then
// starts again as the new one.
func caretakerChanged(backup string) bool {
	return exists(filepath.Join(backup, filepath.FromSlash(caretakerPath)))
}

// writeMode replaces a file whole with a mode: written beside it, then
// renamed over it.
func writeMode(p string, data []byte, mode os.FileMode) error {
	tmp := p + ".soundstorm-new"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
