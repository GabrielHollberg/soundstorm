package httpapi

import (
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/GabrielHollberg/soundstorm/internal/auth"
	"github.com/GabrielHollberg/soundstorm/internal/library"
	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/source"
	"github.com/GabrielHollberg/soundstorm/internal/state"
)

// Bringing media in from a USB drive plugged into the box (the owner's
// asking: most people's films and music are on a drive or a laptop, and a
// browser over Wi-Fi is no way to move a terabyte).
//
// The box's own system mounts each drive plugged in, read-only, as a folder
// under SOUNDSTORM_DRIVES_DIR (box/rootfs). SoundStorm only reads it. The
// page plans as for files dropped on it - the same plan, questions, review
// and taken names, reading the few megabytes it needs through /read - and
// then hands the plan back here, and the server copies each file from the
// drive through the same filing as an upload (addFile). Nothing crosses the
// network; it carries on with the page closed, and the page follows it with
// the Android app's upload status, so the sheet and its chip are unchanged.
//
// The owner's alone: a drive plugged into the house's box may hold anybody's
// files, and a member who could list it could read them.

// Limits: what one drive may list (a library of a few hundred thousand files
// is a big one; past it the page says so), and what one read may ask for.
const (
	maxDriveFiles = 200_000
	maxDriveRead  = 8 << 20
)

// driveID is what a drive's folder may be called: the box names it after the
// drive's label, made safe.
var driveID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,63}$`)

// Folders a drive keeps for its own system, never anybody's media.
var driveSystemDirs = map[string]bool{
	"$recycle.bin": true, "system volume information": true, "lost+found": true, "recycler": true,
}

// GET /api/drives: the drives plugged in, and how big each is.
func (s *Server) handleDrives(w http.ResponseWriter, r *http.Request) {
	out := []map[string]any{}
	if s.drivesDir != "" {
		entries, _ := os.ReadDir(s.drivesDir)
		for _, e := range entries {
			if !e.IsDir() || !driveID.MatchString(e.Name()) {
				continue
			}
			dir := filepath.Join(s.drivesDir, e.Name())
			// A folder left behind by a drive taken out is empty: not a drive.
			if inside, err := os.ReadDir(dir); err != nil || len(inside) == 0 {
				continue
			}
			// hasMedia: whether there is anything on it to bring in - an
			// empty drive was most likely bought for backups.
			d := map[string]any{"id": e.Name(), "label": e.Name(), "hasMedia": driveHasMedia(dir), "backups": false}
			if free, total, ok := library.DiskSize(dir); ok {
				d["size"], d["used"] = total, total-free
			}
			out = append(out, d)
		}
	}
	// available: whether this install takes drives at all (a box), so others
	// show no USB card.
	writeJSON(w, http.StatusOK, map[string]any{"available": s.drivesDir != "", "drives": out, "importing": s.driveImports.busy()})
}

// driveHasMedia looks for one file some shelf keeps, giving up after 20,000
// entries (a drive of nothing but other files is not worth longer).
func driveHasMedia(root string) bool {
	seen, found := 0, false
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if seen++; seen > 20_000 {
			return fs.SkipAll
		}
		name := d.Name()
		if p != root && (strings.HasPrefix(name, ".") || driveSystemDirs[strings.ToLower(name)]) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() && library.IsMediaFile(name) {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	return found
}

// driveRoot is a plugged-in drive's folder, or false.
func (s *Server) driveRoot(id string) (string, bool) {
	if s.drivesDir == "" || !driveID.MatchString(id) {
		return "", false
	}
	dir := filepath.Join(s.drivesDir, id)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return "", false
	}
	return dir, true
}

// drivePath is a file on a drive, by its path within it - never outside it,
// whatever the path or a link on the drive says.
func drivePath(root, rel string) (string, bool) {
	rel = strings.TrimPrefix(path.Clean("/"+rel), "/")
	if rel == "" || rel == "." {
		return "", false
	}
	full := filepath.Join(root, filepath.FromSlash(rel))
	real, err := filepath.EvalSymlinks(full)
	if err != nil {
		return "", false
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", false
	}
	if inside, err := filepath.Rel(realRoot, real); err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
		return "", false
	}
	return real, true
}

// GET /api/drives/{id}/files: every file on the drive, as the page plans
// with: its path, size and date. Hidden files and a drive's own system
// folders are left out, and links are not followed.
func (s *Server) handleDriveFiles(w http.ResponseWriter, r *http.Request) {
	root, ok := s.driveRoot(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "that drive is not plugged in")
		return
	}
	type file struct {
		Path     string `json:"path"`
		Size     int64  `json:"size"`
		Modified int64  `json:"lastModified"`
	}
	files := []file{}
	truncated := false
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable folder is passed over, not the end
		}
		if r.Context().Err() != nil {
			return fs.SkipAll
		}
		name := d.Name()
		if p != root && (strings.HasPrefix(name, ".") || driveSystemDirs[strings.ToLower(name)]) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if len(files) >= maxDriveFiles {
			truncated = true
			return fs.SkipAll
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		files = append(files, file{Path: filepath.ToSlash(rel), Size: info.Size(), Modified: info.ModTime().UnixMilli()})
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	writeJSON(w, http.StatusOK, map[string]any{"files": files, "truncated": truncated})
}

// GET /api/drives/{id}/read?path=&from=&to=: a piece of a file on the drive,
// for the page's planning (a sample to spot a copy, the ends to name a
// different one, a zip's table of contents) - 8MB at most.
func (s *Server) handleDriveRead(w http.ResponseWriter, r *http.Request) {
	root, ok := s.driveRoot(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "that drive is not plugged in")
		return
	}
	q := r.URL.Query()
	full, ok := drivePath(root, q.Get("path"))
	if !ok {
		writeError(w, http.StatusNotFound, "no such file on the drive")
		return
	}
	from, err1 := strconv.ParseInt(q.Get("from"), 10, 64)
	to, err2 := strconv.ParseInt(q.Get("to"), 10, 64)
	if err1 != nil || err2 != nil || from < 0 || to < from || to-from > maxDriveRead {
		writeError(w, http.StatusBadRequest, "from and to, at most 8MB apart")
		return
	}
	f, err := os.Open(full)
	if err != nil {
		writeError(w, http.StatusNotFound, "could not read that file")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.Copy(w, io.NewSectionReader(f, from, to-from))
}

// driveImports is the one import running (the box copies one drive at a
// time), and what it did, for the page to follow: the shape of the Android
// app's upload status (Uploads.kt), so the page shows it the same way.
type driveImports struct {
	mu   sync.Mutex
	cur  *driveImport
	stop bool
}

type driveImport struct {
	Drive      string     `json:"drive"`
	By         string     `json:"-"`
	Jobs       []driveJob `json:"jobs"`
	Done       int        `json:"done"`
	DoneBytes  int64      `json:"doneBytes"`
	TotalBytes int64      `json:"totalBytes"`
	Current    string     `json:"current"`
	Sent       int64      `json:"currentSent"`
	Running    bool       `json:"running"`
	Problem    string     `json:"problem,omitempty"`
	Seen       bool       `json:"seen"`
}

type driveJob struct {
	ID       string `json:"-"` // its path on the drive
	Name     string `json:"name"`
	Path     string `json:"path"` // where the plan said it goes
	Group    string `json:"group,omitempty"`
	Kind     string `json:"kind"`
	Conflict string `json:"-"`
	As       string `json:"-"`
	Taken    int64  `json:"-"`
	Size     int64  `json:"-"`
	State    string `json:"state"` // waiting, done, skipped, stopped, failed
	Dest     string `json:"dest,omitempty"`
	Error    string `json:"error,omitempty"`
}

func (d *driveImports) busy() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cur != nil && d.cur.Running
}

// POST /api/drives/{id}/import {jobs}: copies the planned files in, in the
// background. One import at a time.
func (s *Server) handleDriveImport(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	root, ok := s.driveRoot(id)
	if !ok {
		writeError(w, http.StatusNotFound, "that drive is not plugged in")
		return
	}
	var body struct {
		Jobs []struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Path     string `json:"path"`
			Group    string `json:"group"`
			Kind     string `json:"kind"`
			Conflict string `json:"conflict"`
			As       string `json:"as"`
			Taken    int64  `json:"taken"`
		} `json:"jobs"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<20)).Decode(&body); err != nil || len(body.Jobs) == 0 || len(body.Jobs) > maxDriveFiles {
		writeError(w, http.StatusBadRequest, "expected the files to bring in")
		return
	}
	user, _ := auth.FromContext(r.Context())
	access := source.AccessFrom(r.Context())
	imp := &driveImport{Drive: id, By: user.Name, Running: true}
	for _, j := range body.Jobs {
		kind, ok := media.ParseKind(j.Kind)
		full, inside := drivePath(root, j.ID)
		if !ok || !access.Permits(kind) || !inside || j.Path == "" {
			writeError(w, http.StatusBadRequest, "one of the files is not one to bring in")
			return
		}
		st, err := os.Stat(full)
		if err != nil || !st.Mode().IsRegular() {
			writeError(w, http.StatusBadRequest, "a file is no longer on the drive: "+j.Name)
			return
		}
		imp.Jobs = append(imp.Jobs, driveJob{ID: j.ID, Name: j.Name, Path: j.Path, Group: j.Group, Kind: string(kind),
			Conflict: j.Conflict, As: j.As, Taken: j.Taken, Size: st.Size(), State: "waiting"})
		imp.TotalBytes += st.Size()
	}
	d := &s.driveImports
	d.mu.Lock()
	if d.cur != nil && d.cur.Running {
		d.mu.Unlock()
		writeError(w, http.StatusConflict, "already bringing files in from a drive; wait for it to finish")
		return
	}
	d.cur, d.stop = imp, false
	d.mu.Unlock()
	go s.runDriveImport(user, access, root, imp)
	writeJSON(w, http.StatusOK, map[string]any{"added": len(imp.Jobs)})
}

// runDriveImport copies an import's files in, one at a time, through the
// same filing as an upload.
func (s *Server) runDriveImport(user state.User, access source.Access, root string, imp *driveImport) {
	d := &s.driveImports
	defer func() {
		if p := recover(); p != nil {
			s.log.Error("drive import crashed", "panic", p, "stack", string(debug.Stack()))
		}
		d.mu.Lock()
		imp.Running, imp.Current, imp.Sent = false, "", 0
		for i := range imp.Jobs {
			if imp.Jobs[i].State == "waiting" {
				imp.Jobs[i].State = "stopped"
			}
		}
		d.mu.Unlock()
		s.log.Info("drive import finished", "drive", imp.Drive, "files", len(imp.Jobs), "by", user.Name)
	}()
	for i := range imp.Jobs {
		d.mu.Lock()
		stop := d.stop
		job := imp.Jobs[i]
		imp.Current, imp.Sent = job.Name, 0
		d.mu.Unlock()
		if stop {
			return
		}
		state, dest, msg := s.copyFromDrive(user, access, root, job, func(n int64) {
			d.mu.Lock()
			imp.Sent += n
			d.mu.Unlock()
		})
		d.mu.Lock()
		imp.Jobs[i].State, imp.Jobs[i].Dest, imp.Jobs[i].Error = state, dest, msg
		imp.Done++
		imp.DoneBytes += job.Size
		imp.Sent = 0
		if state == "failed" && errorStops(msg) {
			imp.Problem = msg
			d.stop = true
		}
		d.mu.Unlock()
	}
}

// errorStops is a failure every later file would meet too: the disk or a
// person's photo space is full.
func errorStops(msg string) bool {
	return msg == errLibraryFull.Error() || msg == library.ErrDiskReserve.Error() || msg == errPhotoLimit.Error()
}

func (s *Server) copyFromDrive(user state.User, access source.Access, root string, job driveJob, sent func(int64)) (string, string, string) {
	full, ok := drivePath(root, job.ID)
	if !ok {
		return "failed", "", "no longer on the drive"
	}
	f, err := os.Open(full)
	if err != nil {
		return "failed", "", "could not read it from the drive"
	}
	defer f.Close()
	kind, _ := media.ParseKind(job.Kind)
	dest, err := s.addFile(addRequest{User: user, Access: access, Kind: kind, Path: job.Path, Size: job.Size,
		Taken: job.Taken, Conflict: job.Conflict, As: job.As}, &countingReader{r: f, n: sent})
	switch {
	case err == nil:
		return "done", dest, ""
	case addSkipped(err):
		return "skipped", "", addMessage(err)
	default:
		return "failed", "", addMessage(err)
	}
}

// countingReader tells how much has been read, as it is.
type countingReader struct {
	r io.Reader
	n func(int64)
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.n(int64(n))
	}
	return n, err
}

// GET /api/drives/import: how the import is going, in the shape the page
// follows the Android app's uploads with.
func (s *Server) handleDriveImportStatus(w http.ResponseWriter, r *http.Request) {
	d := &s.driveImports
	d.mu.Lock()
	defer d.mu.Unlock()
	imp := d.cur
	if imp == nil {
		writeJSON(w, http.StatusOK, map[string]any{"total": 0, "waiting": 0, "done": 0, "jobs": []driveJob{}})
		return
	}
	waiting := 0
	for _, j := range imp.Jobs {
		if j.State == "waiting" {
			waiting++
		}
	}
	data, _ := json.Marshal(imp)
	var out map[string]any
	_ = json.Unmarshal(data, &out)
	out["total"], out["waiting"] = len(imp.Jobs), waiting
	writeJSON(w, http.StatusOK, out)
}

// POST /api/drives/import/stop: no more files after the one being copied.
func (s *Server) handleDriveImportStop(w http.ResponseWriter, r *http.Request) {
	d := &s.driveImports
	d.mu.Lock()
	d.stop = true
	d.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{})
}

// POST /api/drives/import/seen: the page has shown how it ended.
func (s *Server) handleDriveImportSeen(w http.ResponseWriter, r *http.Request) {
	d := &s.driveImports
	d.mu.Lock()
	if d.cur != nil && !d.cur.Running {
		d.cur.Seen = true
	}
	d.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{})
}
