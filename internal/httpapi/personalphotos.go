package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/library"
	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/state"
)

// Everyone's own photos (the owner's design): each member has their own
// folder, pictures/Personal/<name>/, which their phone backs up to and which
// everything they add to the picture shelf goes into. A member sees only
// their own photos - their own Immich account and library, see
// provision.PhotoAccountFor - and the owner sees everybody's. Whether a member
// has photos at all is the picture library on their account, as for any
// shelf. Each member's folder has a limit, the household default (100 GB
// unless the owner changes it) or one the owner sets for them; the owner's own
// photos have none.

// usageFresh is how long a measured folder size is trusted before it is
// walked again; uploads in between are added to it as they land.
const usageFresh = time.Minute

type photoUsage struct {
	mu    sync.Mutex
	sizes map[string]usageEntry
}

type usageEntry struct {
	bytes int64
	at    time.Time
}

// used is how many bytes a person's photo folder holds.
func (s *Server) photoBytes(u state.User) int64 {
	c := &s.photoUsage
	c.mu.Lock()
	e, ok := c.sizes[u.ID]
	c.mu.Unlock()
	if ok && time.Since(e.at) < usageFresh {
		return e.bytes
	}
	n := s.library.PersonalUsage(u.Name)
	c.mu.Lock()
	if c.sizes == nil {
		c.sizes = map[string]usageEntry{}
	}
	c.sizes[u.ID] = usageEntry{bytes: n, at: time.Now()}
	c.mu.Unlock()
	return n
}

// addPhotoBytes counts a file just saved, without walking the folder again.
func (s *Server) addPhotoBytes(u state.User, n int64) {
	c := &s.photoUsage
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.sizes[u.ID]; ok {
		e.bytes += n
		c.sizes[u.ID] = e
	}
}

// photoLimitBytes is a person's limit in bytes, or -1 for none.
func (s *Server) photoLimitBytes(u state.User) int64 {
	gb := s.store.PhotoLimitGB(u.ID)
	if gb < 0 {
		return -1
	}
	return int64(gb) << 30
}

var errPhotoLimit = errors.New("photo limit reached")

// photoRoom refuses a file that would take a person past their limit. size
// may be unknown (-1), in which case only a folder already full is refused.
func (s *Server) photoRoom(u state.User, size int64) error {
	limit := s.photoLimitBytes(u)
	if limit < 0 {
		return nil
	}
	if size < 0 {
		size = 0
	}
	if s.photoBytes(u)+size > limit {
		return fmt.Errorf("%w: your photos have used their %d GB", errPhotoLimit, limit>>30)
	}
	return nil
}

// personalUpload turns an upload to the picture shelf into one into the
// person's own folder, for everyone but the owner: a member's photos are
// theirs, so they can never land somewhere the household sees. It also
// checks their limit.
func (s *Server) personalUpload(u state.User, kind media.Kind, rel string, size int64) (string, error) {
	if kind != media.KindPicture || u.IsOwner() {
		return rel, nil
	}
	if _, err := s.library.EnsurePersonalFolder(u.Name); err != nil {
		return "", err
	}
	if err := s.photoRoom(u, size); err != nil {
		return "", err
	}
	return library.PersonalPath(u.Name, rel)
}

// personalPlan shows a member where their pictures will really go.
func (s *Server) personalPlan(u state.User, placements []library.Placement) {
	if u.IsOwner() {
		return
	}
	for i, p := range placements {
		if p.Kind != media.KindPicture || p.Skipped || p.Waiting || p.Dest == "" {
			continue
		}
		rel := strings.TrimPrefix(p.Dest, "pictures/")
		if dest, err := library.PersonalPath(u.Name, rel); err == nil {
			placements[i].Dest = "pictures/" + dest
		}
	}
}

// photoUsageJSON is a person's photo space as the app shows it.
func (s *Server) photoUsageJSON(u state.User) map[string]any {
	out := map[string]any{"usedBytes": s.photoBytes(u)}
	if u.IsOwner() {
		out["usedBytes"] = nil // the owner's photos are the whole folder, not limited
	}
	if limit := s.photoLimitBytes(u); limit >= 0 {
		out["limitBytes"] = limit
	} else {
		out["limitBytes"] = nil
	}
	out["folder"] = "pictures/" + library.PersonalFolder(u.Name)
	return out
}

// GET /api/photos/usage: how much of their limit this person's photos use.
func (s *Server) handlePhotoUsage(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.photoUsageJSON(u))
}

// backupItem is one photo or video a phone has, as it describes it.
type backupItem struct {
	Name  string `json:"name"`
	Taken int64  `json:"taken"` // when it was taken, Unix milliseconds
	Size  int64  `json:"size"`
}

// backupPath is where a phone's photo goes inside its owner's folder: by the
// year and month it was taken, so the folder reads like a camera roll.
func backupPath(it backupItem) (string, error) {
	name := strings.TrimSpace(path.Base(strings.ReplaceAll(it.Name, "\\", "/")))
	if name == "" || name == "." || name == "/" {
		return "", errors.New("the photo has no name")
	}
	when := time.UnixMilli(it.Taken).UTC()
	if it.Taken <= 0 {
		when = time.Now().UTC()
	}
	return fmt.Sprintf("%04d/%02d/%s", when.Year(), int(when.Month()), name), nil
}

// maxBackupCheck is how many photos one check may ask about.
const maxBackupCheck = 2000

// POST /api/photos/backup/check: which of these does the server already have?
// A phone asks before sending, so reinstalling the app or a new phone does
// not send a camera roll twice.
func (s *Server) handleBackupCheck(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	if _, err := s.uploadKind(r, string(media.KindPicture)); err != nil {
		writeError(w, statusForUpload(err), err.Error())
		return
	}
	var body struct {
		Items []backupItem `json:"items"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err := dec.Decode(&body); err != nil || len(body.Items) > maxBackupCheck {
		writeError(w, http.StatusBadRequest, "expected a JSON list of up to 2000 items")
		return
	}
	have := make([]bool, len(body.Items))
	for i, it := range body.Items {
		if rel, err := backupPath(it); err == nil {
			have[i] = s.library.PersonalHas(u.Name, rel, it.Size)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"have": have, "usage": s.photoUsageJSON(u)})
}

// PUT /api/photos/backup?name=&taken=: one photo or video from a phone, the
// whole request body, into the person's own folder by when it was taken. The
// owner's phone backs up the same way, into their own folder.
func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	if _, err := s.uploadKind(r, string(media.KindPicture)); err != nil {
		writeError(w, statusForUpload(err), err.Error())
		return
	}
	taken, _ := strconv.ParseInt(r.URL.Query().Get("taken"), 10, 64)
	it := backupItem{Name: r.URL.Query().Get("name"), Taken: taken, Size: r.ContentLength}
	rel, err := backupPath(it)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.library.PersonalHas(u.Name, rel, it.Size) {
		writeJSON(w, http.StatusOK, map[string]any{"already": true})
		return
	}
	if !s.library.Room(r.ContentLength) {
		writeError(w, http.StatusInsufficientStorage, "the library disk is nearly full")
		return
	}
	if _, err := s.library.EnsurePersonalFolder(u.Name); err != nil {
		writeError(w, http.StatusInternalServerError, desensitizeFSError(err))
		return
	}
	if !u.IsOwner() {
		if err := s.photoRoom(u, it.Size); err != nil {
			writeError(w, http.StatusInsufficientStorage, err.Error())
			return
		}
	}
	// Another, different file of the same name taken the same month (a
	// different phone, a camera that restarted its numbering) is kept beside
	// it rather than refused: decided before a byte is read, as Save reads the
	// whole body before it can say the name is taken.
	if s.library.PersonalHas(u.Name, rel, 0) {
		rel = altName(rel, it.Taken)
	}
	dest, err := library.PersonalPath(u.Name, rel)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	release, ok := s.takeUploadSlot(u.ID)
	if !ok {
		writeError(w, http.StatusTooManyRequests, "too many uploads at once; wait for one to finish")
		return
	}
	defer release()
	body := &stallReader{r: r.Body, rc: http.NewResponseController(w)}
	defer func() { _ = body.rc.SetReadDeadline(time.Time{}) }()
	saved, err := s.library.Save(media.KindPicture, dest, body)
	if err != nil {
		switch {
		case errors.Is(err, library.ErrDiskReserve):
			writeError(w, http.StatusInsufficientStorage, err.Error())
		case errors.Is(err, library.ErrAlreadyThere):
			writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
		default:
			s.log.Warn("backup failed", "name", it.Name, "err", err)
			writeError(w, http.StatusBadRequest, desensitizeFSError(err))
		}
		return
	}
	s.addPhotoBytes(u, it.Size)
	s.scheduleRescan(media.KindPicture)
	writeJSON(w, http.StatusOK, map[string]any{"dest": saved})
}

// altName is rel with the moment it was taken before the extension, for a
// second, different photo of the same name taken the same month.
func altName(rel string, taken int64) string {
	ext := path.Ext(rel)
	return strings.TrimSuffix(rel, ext) + " " + strconv.FormatInt(taken, 10) + ext
}

// --- the owner's controls ------------------------------------------------------

// PUT /api/users/{id}/photo-limit {"gb": 50 | -1 | null}: one person's limit
// (null: the household default, -1: none).
func (s *Server) handleSetPhotoLimit(w http.ResponseWriter, r *http.Request) {
	gb, ok := decodeLimit(w, r)
	if !ok {
		return
	}
	target, found := s.store.User(r.PathValue("id"))
	if !found {
		writeError(w, http.StatusNotFound, "no such account")
		return
	}
	if target.IsOwner() {
		writeError(w, http.StatusBadRequest, "the owner's photos have no limit")
		return
	}
	if err := s.store.SetPhotoLimitGB(target.ID, gb); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.photoUsageJSON(target))
}

// GET/PUT /api/photos/limit-default {"gb": 100 | -1 | null}: the household
// default for each person's photos.
func (s *Server) handlePhotoLimitDefault(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPut {
		gb, ok := decodeLimit(w, r)
		if !ok {
			return
		}
		if err := s.store.SetPhotoLimitDefaultGB(gb); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"gb": s.store.PhotoLimitDefaultGB(), "standardGB": state.DefaultPhotoLimitGB})
}

// decodeLimit reads {"gb": n}: a whole number of GB from 1 to 100,000, -1 for
// none, or null for the default.
func decodeLimit(w http.ResponseWriter, r *http.Request) (*int, bool) {
	var body struct {
		GB *int `json:"gb"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1024)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, `expected {"gb": a number of GB, -1 for no limit, or null}`)
		return nil, false
	}
	if body.GB != nil && *body.GB != -1 && (*body.GB < 1 || *body.GB > 100_000) {
		writeError(w, http.StatusBadRequest, "a limit is from 1 to 100,000 GB, or -1 for none")
		return nil, false
	}
	return body.GB, true
}

// photoFieldsFor adds a person's photo space to the owner's list of people.
func (s *Server) photoFieldsFor(u state.User, out map[string]any) {
	usage := s.photoUsageJSON(u)
	out["photoUsedBytes"] = usage["usedBytes"]
	out["photoLimitBytes"] = usage["limitBytes"]
	out["photoLimitDefault"] = u.PhotoLimitGB == nil
	out["photoFolder"] = usage["folder"]
}
