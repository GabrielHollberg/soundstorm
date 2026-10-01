package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/library"
	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/photoimport"
	"github.com/GabrielHollberg/soundstorm/internal/state"
)

// Bringing a photo library in (Settings > Your photos > Bring your photos in):
// a Google Takeout or iCloud download, as the zips those services email, sent
// in pieces so a dropped connection resumes, then sorted into the person's
// own folder by internal/photoimport, one download at a time.
//
// The zips wait in library/.imports/<account>/ - a top-level dot-folder like
// .uploads, mounted into no backend - with a small record each, so a restart
// neither loses an upload half sent nor forgets one waiting to be sorted.

// importChunkMax is the most one piece may be. The app sends 8MB.
const importChunkMax = 32 << 20

// importMaxSize is the largest download taken whole: Takeout's biggest zip
// is 50GB; this leaves room for anything sensible.
const importMaxSize = 200 << 30

// importsPerPerson is how many downloads one person may have waiting.
const importsPerPerson = 40

type importJob struct {
	ID       string               `json:"id"`
	User     string               `json:"user"`
	Name     string               `json:"name"`
	Size     int64                `json:"size"`
	Received int64                `json:"received"`
	State    string               `json:"state"` // uploading, queued, sorting, done, stopped, failed
	Progress photoimport.Progress `json:"progress"`
	Created  time.Time            `json:"created"`
	Updated  time.Time            `json:"updated"`
}

type photoImports struct {
	mu    sync.Mutex
	jobs  map[string]*importJob
	queue chan string
	stop  map[string]bool
	once  sync.Once
}

func (s *Server) importDir(userID string) string {
	return filepath.Join(s.library.Root(), ".imports", userID)
}

func (s *Server) saveJob(j *importJob) {
	j.Updated = time.Now().UTC()
	b, _ := json.Marshal(j)
	dir := s.importDir(j.User)
	_ = os.MkdirAll(dir, 0o777)
	tmp := filepath.Join(dir, j.ID+".json.part")
	if os.WriteFile(tmp, b, 0o666) == nil {
		_ = os.Rename(tmp, filepath.Join(dir, j.ID+".json"))
	}
}

func (s *Server) importsInit() {
	im := &s.photoImports
	im.once.Do(func() {
		im.jobs = map[string]*importJob{}
		im.stop = map[string]bool{}
		im.queue = make(chan string, 1024)
		root := filepath.Join(s.library.Root(), ".imports")
		users, _ := os.ReadDir(root)
		for _, u := range users {
			files, _ := filepath.Glob(filepath.Join(root, u.Name(), "*.json"))
			for _, f := range files {
				b, err := os.ReadFile(f)
				var j importJob
				if err != nil || json.Unmarshal(b, &j) != nil || j.ID == "" {
					continue
				}
				im.jobs[j.ID] = &j
			}
		}
	})
}

// RunPhotoImports sorts waiting downloads, one at a time, until ctx ends:
// those left waiting or half sorted by a restart first (sorting again is
// safe - what is already in the folder is skipped).
func (s *Server) RunPhotoImports(ctx context.Context) {
	s.importsInit()
	im := &s.photoImports
	im.mu.Lock()
	var waiting []*importJob
	for _, j := range im.jobs {
		if j.State == "queued" || j.State == "sorting" {
			waiting = append(waiting, j)
		}
	}
	im.mu.Unlock()
	sort.Slice(waiting, func(a, b int) bool { return waiting[a].Created.Before(waiting[b].Created) })
	for _, j := range waiting {
		im.queue <- j.ID
	}
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-im.queue:
			s.sortImport(id)
		}
	}
}

func (s *Server) sortImport(id string) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("photo import panicked", "id", id, "panic", r)
		}
	}()
	im := &s.photoImports
	im.mu.Lock()
	j, ok := im.jobs[id]
	if !ok || (j.State != "queued" && j.State != "sorting") {
		im.mu.Unlock()
		return
	}
	j.State = "sorting"
	s.saveJob(j)
	im.mu.Unlock()

	u, ok := s.store.User(j.User)
	if !ok {
		s.finishImport(j, "failed", "the account is gone")
		return
	}
	zipPath := filepath.Join(s.importDir(j.User), j.ID+".zip")
	target := &personTarget{s: s, u: u}
	if _, err := s.library.EnsurePersonalFolder(u.Name); err != nil {
		s.finishImport(j, "failed", "could not make the photo folder")
		return
	}
	stopped := func() bool {
		im.mu.Lock()
		defer im.mu.Unlock()
		return im.stop[id]
	}
	last := time.Now()
	result, err := photoimport.Run(zipPath, target, func(p photoimport.Progress) {
		im.mu.Lock()
		j.Progress = p
		if time.Since(last) > 2*time.Second {
			s.saveJob(j)
			last = time.Now()
		}
		im.mu.Unlock()
	}, stopped)
	im.mu.Lock()
	j.Progress = result
	im.mu.Unlock()
	if result.Added > 0 {
		s.scheduleRescan(media.KindPicture)
	}
	switch {
	case stopped():
		s.finishImport(j, "stopped", "")
	case err != nil:
		var room photoimport.ErrNoRoom
		if errors.As(err, &room) {
			s.finishImport(j, "stopped", room.Error())
			return
		}
		s.log.Warn("photo import failed", "id", id, "err", err)
		s.finishImport(j, "failed", "the download could not be read as a zip")
	default:
		s.log.Info("photos imported", "by", u.Name, "added", result.Added, "duplicates", result.Duplicates, "failed", result.Failed, "source", result.Source)
		s.finishImport(j, "done", "")
	}
}

// finishImport records how a download ended and deletes it: its photos are
// in the folder, and a 50GB zip is not something to leave lying about.
func (s *Server) finishImport(j *importJob, st, problem string) {
	im := &s.photoImports
	im.mu.Lock()
	defer im.mu.Unlock()
	j.State = st
	if problem != "" {
		j.Progress.Problem = problem
	}
	delete(im.stop, j.ID)
	_ = os.Remove(filepath.Join(s.importDir(j.User), j.ID+".zip"))
	s.saveJob(j)
}

// personTarget is a person's own folder, as an import sees it.
type personTarget struct {
	s  *Server
	u  state.User
	ix *photoIndex
}

func (t *personTarget) IsMedia(name string) bool { return library.IsPictureFile(name) }

func (t *personTarget) folder() string {
	return filepath.Join(t.s.library.PathFor(media.KindPicture), filepath.FromSlash(library.PersonalFolder(t.u.Name)))
}

// Existing compares against files of the same size only: hashing a whole
// photo folder to import one zip would take as long as the import.
func (t *personTarget) Existing(size int64, sum [32]byte) (string, bool) {
	if t.ix == nil {
		t.ix = t.s.photoIndexFor(t.u)
	}
	return t.ix.find(size, sum)
}

// Improve gives the copy kept what a duplicate knew (existing is a path the
// import saved, library-relative, or one the folder's list holds).
func (t *personTarget) Improve(existing string, m photoimport.Meta, src photoimport.DateSource) bool {
	if !filepath.IsAbs(existing) {
		existing = filepath.Join(t.s.library.Root(), filepath.FromSlash(existing))
	}
	return t.s.improvePhoto(t.u, existing, m, src)
}

func (t *personTarget) Room(size int64) error {
	if !t.s.library.Room(size) {
		return errors.New("the library disk is nearly full")
	}
	if t.u.IsOwner() {
		return nil
	}
	return t.s.photoRoom(t.u, size)
}

func (t *personTarget) Save(rel string, r io.Reader, size int64, sum [32]byte) (string, error) {
	// A different photo of the same name taken the same month keeps both.
	if t.s.library.PersonalHas(t.u.Name, rel, 0) {
		rel = altName(rel, int64(sum[0])<<16|int64(sum[1])<<8|int64(sum[2]))
	}
	dest, err := library.PersonalPath(t.u.Name, rel)
	if err != nil {
		return "", err
	}
	saved, err := t.s.library.Save(media.KindPicture, dest, r)
	if err != nil {
		return "", err
	}
	if t.ix == nil {
		t.ix = t.s.photoIndexFor(t.u)
	}
	t.ix.add(filepath.Join(t.s.library.Root(), filepath.FromSlash(saved)), size, sum)
	t.s.addPhotoBytes(t.u, size)
	return saved, nil
}

// Sidecar writes "<photo>.xmp" beside a saved photo, for Immich to read the
// date and place a Takeout download knew. saved is library-relative.
func (t *personTarget) Sidecar(saved string, data []byte) error {
	p := filepath.Join(t.s.library.Root(), filepath.FromSlash(saved)) + ".xmp"
	if !strings.HasPrefix(p, t.folder()) {
		return errors.New("outside the folder")
	}
	return os.WriteFile(p, data, 0o666)
}

// --- the routes -----------------------------------------------------------------

func newImportID() string {
	b := make([]byte, 9)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Server) importUser(w http.ResponseWriter, r *http.Request) (state.User, bool) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return u, false
	}
	if _, err := s.uploadKind(r, string(media.KindPicture)); err != nil {
		writeError(w, statusForUpload(err), err.Error())
		return u, false
	}
	s.importsInit()
	return u, true
}

func importJSON(j *importJob) map[string]any {
	return map[string]any{
		"id": j.ID, "name": j.Name, "size": j.Size, "received": j.Received,
		"state": j.State, "progress": j.Progress, "updated": j.Updated,
	}
}

// GET /api/photos/import: this person's downloads, newest first.
func (s *Server) handleImports(w http.ResponseWriter, r *http.Request) {
	u, ok := s.importUser(w, r)
	if !ok {
		return
	}
	im := &s.photoImports
	im.mu.Lock()
	var out []*importJob
	for _, j := range im.jobs {
		if j.User == u.ID {
			out = append(out, j)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Created.After(out[b].Created) })
	list := make([]map[string]any, 0, len(out))
	for _, j := range out {
		list = append(list, importJSON(j))
	}
	im.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"imports": list, "usage": s.photoUsageJSON(u)})
}

// POST /api/photos/import {name, size}: starts sending a download, or picks
// up one of the same name and size that was half sent.
func (s *Server) handleStartImport(w http.ResponseWriter, r *http.Request) {
	u, ok := s.importUser(w, r)
	if !ok {
		return
	}
	var body struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 4096)).Decode(&body); err != nil || body.Size <= 0 {
		writeError(w, http.StatusBadRequest, "expected the download's name and size")
		return
	}
	name := path.Base(strings.ReplaceAll(body.Name, "\\", "/"))
	if !strings.HasSuffix(strings.ToLower(name), ".zip") {
		writeError(w, http.StatusBadRequest, "a download to bring in is a .zip file")
		return
	}
	if body.Size > importMaxSize {
		writeError(w, http.StatusBadRequest, "that download is larger than SoundStorm takes in one piece")
		return
	}
	im := &s.photoImports
	im.mu.Lock()
	defer im.mu.Unlock()
	count := 0
	for _, j := range im.jobs {
		if j.User != u.ID {
			continue
		}
		if j.Name == name && j.Size == body.Size && j.State == "uploading" {
			writeJSON(w, http.StatusOK, importJSON(j))
			return
		}
		if j.State == "uploading" || j.State == "queued" {
			count++
		}
	}
	if count >= importsPerPerson {
		writeError(w, http.StatusTooManyRequests, "too many downloads waiting; let some finish first")
		return
	}
	// The zip and the photos it unpacks to, both on the disk for a while.
	if !s.library.Room(2 * body.Size) {
		writeError(w, http.StatusInsufficientStorage, "the library disk does not have room for this download and its photos")
		return
	}
	j := &importJob{ID: newImportID(), User: u.ID, Name: name, Size: body.Size, State: "uploading", Created: time.Now().UTC()}
	if err := os.MkdirAll(s.importDir(u.ID), 0o777); err != nil {
		writeError(w, http.StatusInternalServerError, "could not prepare the download")
		return
	}
	f, err := os.Create(filepath.Join(s.importDir(u.ID), j.ID+".zip"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not prepare the download")
		return
	}
	f.Close()
	im.jobs[j.ID] = j
	s.saveJob(j)
	writeJSON(w, http.StatusOK, importJSON(j))
}

// PUT /api/photos/import/{id}?offset=N: the next piece. A piece at the wrong
// offset is refused with where to carry on from; the last one queues the
// download for sorting.
func (s *Server) handleImportChunk(w http.ResponseWriter, r *http.Request) {
	u, ok := s.importUser(w, r)
	if !ok {
		return
	}
	im := &s.photoImports
	im.mu.Lock()
	j, found := im.jobs[r.PathValue("id")]
	if !found || j.User != u.ID {
		im.mu.Unlock()
		writeError(w, http.StatusNotFound, "no such download")
		return
	}
	if j.State != "uploading" {
		im.mu.Unlock()
		writeJSON(w, http.StatusOK, importJSON(j))
		return
	}
	offset, err := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	if err != nil || offset != j.Received {
		resp := importJSON(j)
		im.mu.Unlock()
		writeJSON(w, http.StatusConflict, resp)
		return
	}
	im.mu.Unlock()

	if r.ContentLength <= 0 || r.ContentLength > importChunkMax || offset+r.ContentLength > j.Size {
		writeError(w, http.StatusBadRequest, "a piece is up to 32MB and within the download")
		return
	}
	f, err := os.OpenFile(filepath.Join(s.importDir(u.ID), j.ID+".zip"), os.O_WRONLY, 0)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "the download's file is gone")
		return
	}
	body := &stallReader{r: io.LimitReader(r.Body, r.ContentLength), rc: http.NewResponseController(w)}
	defer func() { _ = body.rc.SetReadDeadline(time.Time{}) }()
	_, err = f.Seek(offset, io.SeekStart)
	var wrote int64
	if err == nil {
		wrote, err = io.Copy(f, body)
	}
	f.Close()
	im.mu.Lock()
	defer im.mu.Unlock()
	if err != nil || wrote != r.ContentLength {
		// Whatever arrived is kept no further than the last whole piece: the
		// file is cut back so the next try starts clean.
		_ = os.Truncate(filepath.Join(s.importDir(u.ID), j.ID+".zip"), j.Received)
		writeJSON(w, http.StatusConflict, importJSON(j))
		return
	}
	j.Received += wrote
	if j.Received == j.Size {
		j.State = "queued"
		s.saveJob(j)
		select {
		case im.queue <- j.ID:
		default:
		}
	} else if time.Since(j.Updated) > 5*time.Second {
		s.saveJob(j)
	}
	writeJSON(w, http.StatusOK, importJSON(j))
}

// DELETE /api/photos/import/{id}: stops a download, or forgets a finished
// one. Photos already sorted stay in the folder.
func (s *Server) handleCancelImport(w http.ResponseWriter, r *http.Request) {
	u, ok := s.importUser(w, r)
	if !ok {
		return
	}
	im := &s.photoImports
	im.mu.Lock()
	defer im.mu.Unlock()
	j, found := im.jobs[r.PathValue("id")]
	if !found || j.User != u.ID {
		writeError(w, http.StatusNotFound, "no such download")
		return
	}
	if j.State == "sorting" {
		im.stop[j.ID] = true
		writeJSON(w, http.StatusOK, importJSON(j))
		return
	}
	_ = os.Remove(filepath.Join(s.importDir(u.ID), j.ID+".zip"))
	_ = os.Remove(filepath.Join(s.importDir(u.ID), j.ID+".json"))
	delete(im.jobs, j.ID)
	writeJSON(w, http.StatusOK, map[string]any{"removed": true})
}
