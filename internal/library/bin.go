package library

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/media"
)

// The bin.
//
// Deleting from SoundStorm never deletes anything at once. An item's files are
// moved into library/.trash and kept there for BinKeep, and an Undo puts them
// back. A wrong tap on a film collection should be a mistake, not a disaster.
//
// It is a top-level dot-directory for the same reason .uploads is: not one of
// the shelf folders the backends have mounted, so the moment something is in
// the bin it is gone from every backend's view, and a rescan drops it from
// search. It is also on the library's own disk, so moving there is a rename.

// BinKeep is how long a deleted item waits before it is really gone.
const BinKeep = 30 * 24 * time.Hour

const (
	binDir       = ".trash"
	binManifest  = "entry.json"
	binFilesDir  = "files"
	maxBinPaths  = 2000
	binIDPattern = "20060102-150405"
)

// ErrNotInBin is an undo for an entry that is not there - already undone,
// already emptied, or never existed.
var ErrNotInBin = errors.New("that is no longer in the bin")

// BinItem is one thing somebody deleted, as they saw it.
type BinItem struct {
	Title string     `json:"title"`
	Kind  media.Kind `json:"kind"`
	// Paths are relative to the library root, slash-separated: what was
	// moved, and where it goes back to.
	Paths []string `json:"paths"`
}

// BinEntry is one delete: everything removed together, undone together.
type BinEntry struct {
	ID        string    `json:"id"`
	DeletedAt time.Time `json:"deletedAt"`
	By        string    `json:"by"`
	Items     []BinItem `json:"items"`
	Files     int       `json:"files"`
	Bytes     int64     `json:"bytes"`
}

// Resolve turns the files a backend says an item is made of into everything
// that should go with it, relative to the library root.
//
// A backend knows the film's file, not that its subtitles, poster and .nfo sit
// beside it and mean nothing on their own. So:
//
//   - a folder goes whole (an audiobook, a series);
//   - a file takes the siblings that share its name - "Dune.mkv" takes
//     "Dune.en.srt" and "Dune-poster.jpg", but not "Dune Part Two.mkv";
//   - and when that leaves its folder with no media of this kind in it at all,
//     the folder goes instead, so deleting a film, a Calibre book or the last
//     track of an album does not leave its cover and sidecars behind.
//
// Every path is checked to stay inside the shelf, and the shelf itself can
// never be named - the same care Save takes with an upload's path, because a
// backend's answer is somebody else's text.
func (l *Library) Resolve(kind media.Kind, rels []string) ([]string, error) {
	shelf := l.PathFor(kind)
	if shelf == "" {
		return nil, fmt.Errorf("no folder for %s", kind)
	}
	shelfRel, err := filepath.Rel(l.root, shelf)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, rel := range rels {
		full, err := l.inShelf(shelf, rel)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(full)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", rel, fs.ErrNotExist)
		}
		picked := []string{full}
		if !info.IsDir() {
			picked = withCompanions(full)
			parent := filepath.Dir(full)
			if parent != shelf && !otherMediaIn(parent, kind, picked) {
				picked = []string{parent}
			}
		}
		for _, p := range picked {
			r, err := filepath.Rel(l.root, p)
			if err != nil {
				return nil, err
			}
			out = append(out, filepath.ToSlash(r))
		}
	}
	out = outermost(out)
	for _, p := range out {
		if p == filepath.ToSlash(shelfRel) {
			return nil, fmt.Errorf("refusing to delete the whole %s folder", kind)
		}
	}
	return out, nil
}

// inShelf joins a backend-reported path onto the shelf and refuses anything
// that leaves it.
func (l *Library) inShelf(shelf, rel string) (string, error) {
	rel = strings.TrimSpace(filepath.ToSlash(rel))
	if rel == "" || strings.HasPrefix(rel, "/") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("%q is not a path inside the shelf", rel)
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == ".." {
			return "", fmt.Errorf("%q leaves the shelf", rel)
		}
	}
	full := filepath.Join(shelf, filepath.FromSlash(path.Clean(rel)))
	if full == shelf || !within(shelf, full) {
		return "", fmt.Errorf("%q leaves the shelf", rel)
	}
	return full, nil
}

// withCompanions is a file and the siblings named after it.
func withCompanions(file string) []string {
	out := []string{file}
	dir, base := filepath.Split(file)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	entries, err := os.ReadDir(dir)
	if err != nil || stem == "" {
		return out
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || name == base || len(name) <= len(stem) || !strings.HasPrefix(name, stem) {
			continue
		}
		// The character after the shared stem has to end the name - ".", "-"
		// or "_" - or "Dune.mkv" would take "Dune Part Two.srt".
		if !strings.ContainsRune(".-_", rune(name[len(stem)])) {
			continue
		}
		if companionExtensions[strings.ToLower(filepath.Ext(name))] {
			out = append(out, filepath.Join(dir, name))
		}
	}
	return out
}

// otherMediaIn reports whether dir holds media of this kind besides the files
// already picked - anywhere beneath it, since a film folder can keep extras in
// a subfolder and a series folder keeps seasons.
func otherMediaIn(dir string, kind media.Kind, picked []string) bool {
	exts := mediaExtensions[kind]
	skip := map[string]bool{}
	for _, p := range picked {
		skip[p] = true
	}
	found := false
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || found {
			return filepath.SkipDir
		}
		if !d.IsDir() && !skip[p] && exts[strings.ToLower(filepath.Ext(p))] {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// outermost drops any path inside another in the list, and duplicates.
func outermost(paths []string) []string {
	sort.Strings(paths)
	var out []string
	for _, p := range paths {
		if len(out) > 0 {
			last := out[len(out)-1]
			if p == last || strings.HasPrefix(p, last+"/") {
				continue
			}
		}
		out = append(out, p)
	}
	return out
}

// Measure counts the files and bytes under the given library-relative paths,
// for the confirmation that says what a delete will remove.
func (l *Library) Measure(paths []string) (files int, bytes int64) {
	for _, p := range paths {
		_ = filepath.WalkDir(filepath.Join(l.root, filepath.FromSlash(p)), func(_ string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if info, err := d.Info(); err == nil {
				files++
				bytes += info.Size()
			}
			return nil
		})
	}
	return files, bytes
}

// MoveToBin moves every item's paths into one new bin entry, and returns it.
// All or nothing: if a move fails part way, what already moved is put back
// before the error is returned, so a failed delete leaves the library as it
// was.
func (l *Library) MoveToBin(items []BinItem, by string) (BinEntry, error) {
	var all []string
	for _, it := range items {
		all = append(all, it.Paths...)
	}
	if len(all) == 0 {
		return BinEntry{}, errors.New("nothing to delete")
	}
	if len(all) > maxBinPaths {
		return BinEntry{}, fmt.Errorf("too many files at once (%d); delete fewer items", len(all))
	}
	files, bytes := l.Measure(all)

	id, err := newBinID()
	if err != nil {
		return BinEntry{}, err
	}
	entryDir := filepath.Join(l.root, binDir, id)
	if err := ensureDir(filepath.Join(entryDir, binFilesDir)); err != nil {
		return BinEntry{}, err
	}
	entry := BinEntry{ID: id, DeletedAt: time.Now().UTC(), By: by, Items: items, Files: files, Bytes: bytes}
	// The manifest first: an entry with its files moved and no manifest could
	// be neither undone nor swept.
	if err := writeManifest(entryDir, entry); err != nil {
		os.RemoveAll(entryDir)
		return BinEntry{}, err
	}

	var moved []string
	for _, rel := range all {
		from := filepath.Join(l.root, filepath.FromSlash(rel))
		to := filepath.Join(entryDir, binFilesDir, filepath.FromSlash(rel))
		if err := moveTree(from, to); err != nil {
			for i := len(moved) - 1; i >= 0; i-- {
				back := moved[i]
				_ = moveTree(filepath.Join(entryDir, binFilesDir, filepath.FromSlash(back)),
					filepath.Join(l.root, filepath.FromSlash(back)))
			}
			os.RemoveAll(entryDir)
			return BinEntry{}, fmt.Errorf("could not move %s to the bin: %w", rel, err)
		}
		moved = append(moved, rel)
		l.pruneEmptyParents(filepath.Dir(from))
	}
	l.Invalidate()
	return entry, nil
}

// Restore puts a bin entry's files back where they came from. A path whose
// place has been taken since - somebody uploaded the same file again - is
// left in the bin and reported rather than overwritten; everything else goes
// back, and the entry goes once it is empty.
func (l *Library) Restore(id string) (entry BinEntry, restored int, blocked []string, err error) {
	entryDir, err := l.entryDir(id)
	if err != nil {
		return entry, 0, nil, err
	}
	entry, err = readManifest(entryDir)
	if err != nil {
		return entry, 0, nil, ErrNotInBin
	}
	for _, it := range entry.Items {
		for _, rel := range it.Paths {
			from := filepath.Join(entryDir, binFilesDir, filepath.FromSlash(rel))
			to := filepath.Join(l.root, filepath.FromSlash(rel))
			if _, err := os.Lstat(from); err != nil {
				continue
			}
			if _, err := os.Lstat(to); err == nil {
				blocked = append(blocked, rel)
				continue
			}
			if err := ensureDir(filepath.Dir(to)); err != nil {
				blocked = append(blocked, rel)
				continue
			}
			if err := moveTree(from, to); err != nil {
				blocked = append(blocked, rel)
				continue
			}
			restored++
		}
	}
	if len(blocked) == 0 {
		os.RemoveAll(entryDir)
	}
	l.Invalidate()
	return entry, restored, blocked, nil
}

// SweepBin empties entries deleted longer ago than keep. It returns how many
// went. An entry whose manifest cannot be read is dated by its directory,
// so a damaged entry is still emptied in time rather than kept for ever.
func (l *Library) SweepBin(now time.Time, keep time.Duration) int {
	dir := filepath.Join(l.root, binDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	swept := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		entryDir := filepath.Join(dir, e.Name())
		when := time.Time{}
		if m, err := readManifest(entryDir); err == nil {
			when = m.DeletedAt
		} else if info, err := e.Info(); err == nil {
			when = info.ModTime()
		}
		if when.IsZero() || now.Sub(when) < keep {
			continue
		}
		if err := os.RemoveAll(entryDir); err == nil {
			swept++
			if l.log != nil {
				l.log.Info("emptied from the bin", "entry", e.Name(), "deleted", when.Format(time.DateOnly))
			}
		}
	}
	return swept
}

// entryDir is a bin entry's directory, for an id that can only name one.
func (l *Library) entryDir(id string) (string, error) {
	if id == "" || strings.ContainsAny(id, `/\.`) || len(id) > 64 {
		return "", ErrNotInBin
	}
	dir := filepath.Join(l.root, binDir, id)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return "", ErrNotInBin
	}
	return dir, nil
}

// pruneEmptyParents removes directories left empty by a delete, up to but
// never including a shelf folder - an artist folder with its only album gone,
// an author with their only book. A shelf's own placeholder keeps the shelf
// itself from ever being empty (see EnsurePlaceholders).
func (l *Library) pruneEmptyParents(dir string) {
	shelves := map[string]bool{}
	for _, f := range layout {
		shelves[filepath.Join(l.root, f.Name)] = true
	}
	for dir != l.root && within(l.root, dir) && !shelves[dir] {
		if err := os.Remove(dir); err != nil {
			return // not empty, or not ours to remove
		}
		dir = filepath.Dir(dir)
	}
}

func newBinID() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return time.Now().UTC().Format(binIDPattern) + "-" + hex.EncodeToString(b), nil
}

func writeManifest(entryDir string, e BinEntry) error {
	data, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(entryDir, binManifest), data, 0o644)
}

func readManifest(entryDir string) (BinEntry, error) {
	var e BinEntry
	data, err := os.ReadFile(filepath.Join(entryDir, binManifest))
	if err != nil {
		return e, err
	}
	return e, json.Unmarshal(data, &e)
}

// moveTree moves a file or folder, by rename where it can and by copying
// where the two sides are on different drives - the same fallback uploads
// have, for a shelf mounted from another disk.
func moveTree(from, to string) error {
	if err := ensureDir(filepath.Dir(to)); err != nil {
		return err
	}
	err := rename(from, to)
	if err == nil || !crossDevice(err) {
		return err
	}
	if err := copyTree(from, to); err != nil {
		os.RemoveAll(to)
		return err
	}
	return os.RemoveAll(from)
}

func copyTree(from, to string) error {
	return filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, p)
		if err != nil {
			return err
		}
		dest := filepath.Join(to, rel)
		if d.IsDir() {
			return ensureDir(dest)
		}
		if !d.Type().IsRegular() {
			return nil // a link or device file is not media
		}
		src, err := os.Open(p)
		if err != nil {
			return err
		}
		defer src.Close()
		dst, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o666)
		if err != nil {
			return err
		}
		if _, err := io.Copy(dst, src); err != nil {
			dst.Close()
			return err
		}
		return dst.Close()
	})
}
