package httpapi

import (
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/GabrielHollberg/soundstorm/internal/library"
	"github.com/GabrielHollberg/soundstorm/internal/media"
)

// Photos deleted from a person's folder, remembered so a phone's backup does
// not send them back. The phones keep no list of what they sent any more (the
// owner's asking, 2026-10-03): each run asks the server about every photo,
// and the server is the only record - of what it has, and of what was
// deleted. Without this, deleting a backed-up photo would bring it straight
// back the next run. Putting a photo back from the bin forgets it again.
//
// Kept in the state folder (photos-deleted.json), never beside the photos:
// paths within the pictures shelf ("Personal/gabriel/2024/01/IMG_1.jpg") and
// each file's size. A phone that names its photos and gives a size (Android)
// is matched on both; one that cannot size a photo without downloading it
// (the iPhone, for iCloud photos) on the path alone.
type deletedPhotos struct {
	mu     sync.Mutex
	file   string
	loaded bool
	m      map[string]int64
}

const maxDeletedPhotos = 200000

func (d *deletedPhotos) load() {
	if d.loaded {
		return
	}
	d.loaded = true
	d.m = map[string]int64{}
	if d.file == "" {
		return
	}
	if data, err := os.ReadFile(d.file); err == nil {
		_ = json.Unmarshal(data, &d.m)
	}
}

func (d *deletedPhotos) save() {
	if d.file == "" {
		return
	}
	data, err := json.Marshal(d.m)
	if err != nil {
		return
	}
	tmp := d.file + ".new"
	if os.WriteFile(tmp, data, 0o600) == nil {
		_ = os.Rename(tmp, d.file)
	}
}

// personalFiles lists the files under paths (relative to the library root)
// that are in somebody's own photo folder, as paths within the pictures shelf
// with their sizes. A folder is walked.
func (s *Server) personalFiles(paths []string) map[string]int64 {
	shelf := s.library.PathFor(media.KindPicture)
	out := map[string]int64{}
	for _, p := range paths {
		full := filepath.Join(s.library.Root(), filepath.FromSlash(p))
		rel, err := filepath.Rel(shelf, full)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		rel = filepath.ToSlash(rel)
		if !strings.HasPrefix(rel, library.PersonalDir+"/") {
			continue
		}
		_ = filepath.WalkDir(full, func(fp string, e fs.DirEntry, err error) error {
			if err != nil || e.IsDir() || strings.HasSuffix(strings.ToLower(fp), ".xmp") {
				return nil
			}
			info, err := e.Info()
			if err != nil {
				return nil
			}
			r, err := filepath.Rel(shelf, fp)
			if err != nil {
				return nil
			}
			r = filepath.ToSlash(r)
			out[r] = info.Size()
			// A photo kept beside another of its name was named for what
			// makes it different ("IMG_1 - iPhone 15.jpg"); a phone offers it
			// under its own name.
			base := path.Base(r)
			ext := path.Ext(base)
			if i := strings.Index(strings.TrimSuffix(base, ext), " - "); i > 0 {
				out[path.Join(path.Dir(r), base[:i]+ext)] = info.Size()
			}
			return nil
		})
	}
	return out
}

// rememberDeleted notes the photos about to go to the bin.
func (s *Server) rememberDeleted(items []library.BinItem) {
	var paths []string
	for _, it := range items {
		if it.Kind == media.KindPicture {
			paths = append(paths, it.Paths...)
		}
	}
	files := s.personalFiles(paths)
	if len(files) == 0 {
		return
	}
	d := &s.deleted
	d.mu.Lock()
	defer d.mu.Unlock()
	d.load()
	for k, v := range files {
		if len(d.m) >= maxDeletedPhotos {
			break
		}
		d.m[k] = v
	}
	d.save()
}

// forgetDeleted is the bin putting photos back: they are wanted again.
func (s *Server) forgetDeleted(items []library.BinItem) {
	var paths []string
	for _, it := range items {
		if it.Kind == media.KindPicture {
			paths = append(paths, it.Paths...)
		}
	}
	files := s.personalFiles(paths)
	if len(files) == 0 {
		return
	}
	d := &s.deleted
	d.mu.Lock()
	defer d.mu.Unlock()
	d.load()
	for k := range files {
		delete(d.m, k)
	}
	d.save()
}

// wasDeleted is whether a photo a phone offers was deleted from the person's
// folder: rel is within their folder ("2024/01/IMG_1.jpg"); size 0 matches on
// the path alone.
func (s *Server) wasDeleted(name, rel string, size int64) bool {
	p, err := library.PersonalPath(name, rel)
	if err != nil {
		return false
	}
	d := &s.deleted
	d.mu.Lock()
	defer d.mu.Unlock()
	d.load()
	had, ok := d.m[p]
	return ok && (size <= 0 || had == size)
}
