package library

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/GabrielHollberg/soundstorm/internal/media"
)

// Moving an item to another shelf (the owner's "Move to", in any item's
// menu): a film that was a home video, a music folder that was an audiobook.
// Each file is filed by the new shelf's own rule - music and audiobooks by
// their tags, a photo by when it was taken (the caller's place), the rest as
// they were - and moved, never copied, never over a file already there.

// Place is where a file goes on the new shelf, relative to it, when the
// caller decides (photos go to a person's dated folder); "" files it by the
// shelf's own rule.
type Place func(abs, rel string) (string, error)

// MoveResult says what happened to one file.
type MoveResult struct {
	From string // relative to the library root
	To   string // relative to the library root; "" if it stayed
	Err  error
}

// ErrShelfRefuses is a move of a file the new shelf does not keep: a film to
// the music shelf.
var ErrShelfRefuses = errors.New("that library does not keep files like these")

// moveFile is one file to move: where it is, and its name relative to the
// item (a folder item keeps its own folder and what is inside it).
type moveFile struct {
	abs, rel, from string
}

// MoveItems moves items to the shelf to. Every file is checked first, so a
// move the new shelf would refuse moves nothing.
func (l *Library) MoveItems(items []BinItem, to media.Kind, place Place) ([]MoveResult, error) {
	folder := l.PathFor(to)
	if folder == "" {
		return nil, fmt.Errorf("there is no %s library", to)
	}
	var files []moveFile
	for _, it := range items {
		if it.Kind == to {
			return nil, fmt.Errorf("%s is already there", it.Title)
		}
		for _, p := range it.Paths {
			abs := filepath.Join(l.root, filepath.FromSlash(p))
			if !within(l.root, abs) {
				return nil, fmt.Errorf("that path does not stay inside the library")
			}
			info, err := os.Lstat(abs)
			if err != nil {
				continue
			}
			parent := filepath.Dir(abs)
			if !info.IsDir() {
				files = append(files, moveFile{abs, filepath.Base(abs), p})
				continue
			}
			_ = filepath.WalkDir(abs, func(f string, d fs.DirEntry, err error) error {
				if err != nil || !d.Type().IsRegular() {
					return nil
				}
				rel, rerr := filepath.Rel(parent, f)
				if rerr == nil {
					from, _ := filepath.Rel(l.root, f)
					files = append(files, moveFile{f, filepath.ToSlash(rel), filepath.ToSlash(from)})
				}
				return nil
			})
		}
	}
	for _, f := range files {
		if !shelfTakes(to, f.rel) {
			return nil, fmt.Errorf("%w: %s", ErrShelfRefuses, strings.ToLower(path.Ext(f.rel)))
		}
	}

	results := make([]MoveResult, 0, len(files))
	dirs := map[string]bool{}
	for _, f := range files {
		r := MoveResult{From: f.from}
		decided := ""
		if place != nil {
			var err error
			if decided, err = place(f.abs, f.rel); err != nil {
				r.Err = err
				results = append(results, r)
				continue
			}
		}
		rel := decided
		if rel == "" {
			rel = shelveUnder(to, f.rel, f.abs, folder)
		}
		clean, err := cleanRelPath(rel)
		if err != nil {
			r.Err = err
			results = append(results, r)
			continue
		}
		dest := filepath.Join(folder, filepath.FromSlash(clean))
		if !within(folder, dest) {
			r.Err = fmt.Errorf("that path does not stay inside the library")
			results = append(results, r)
			continue
		}
		if err := ensureDir(filepath.Dir(dest)); err != nil {
			r.Err = err
			results = append(results, r)
			continue
		}
		if err := placeFile(f.abs, dest); err != nil {
			r.Err = err
			results = append(results, r)
			continue
		}
		// Across drives placeFile copies; the original goes once the copy
		// is in place.
		_ = os.Remove(f.abs)
		dirs[filepath.Dir(f.abs)] = true
		r.To = folderName(to) + "/" + clean
		results = append(results, r)
	}
	for d := range dirs {
		l.pruneEmptyParents(d)
	}
	l.Invalidate()
	return results, nil
}
