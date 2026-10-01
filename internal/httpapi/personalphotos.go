package httpapi

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/library"
	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/photoimport"
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
		// Unknown, the body could be any size: a member with a limit sends
		// the size (the app and browsers always do). Without that, one
		// upload with no length filled the disk (a security review).
		return fmt.Errorf("%w: the size of the file was not given", errPhotoLimit)
	}
	if s.photoBytes(u)+size > limit {
		return fmt.Errorf("%w: your photos have used their %d GB", errPhotoLimit, limit>>30)
	}
	return nil
}

// personalUpload checks an upload to the picture shelf against the person's
// folder and limit (the owner's photos have none). Where in the folder it
// goes is decided once it has arrived: by when it was taken (datedPhoto).
func (s *Server) personalUpload(u state.User, kind media.Kind, rel string, size int64) (string, error) {
	if kind != media.KindPicture {
		return rel, nil
	}
	if _, err := s.library.EnsurePersonalFolder(u.Name); err != nil {
		return "", err
	}
	if !u.IsOwner() {
		if err := s.photoRoom(u, size); err != nil {
			return "", err
		}
	}
	return rel, nil
}

// personalPlan shows where pictures will go: the person's own folder, sorted
// by date once each has arrived (the drop panel shows the real place then).
func (s *Server) personalPlan(u state.User, placements []library.Placement) {
	for i, p := range placements {
		if p.Kind != media.KindPicture || p.Skipped || p.Waiting || p.Dest == "" {
			continue
		}
		placements[i].Dest = "pictures/" + library.PersonalFolder(u.Name) + "/by date/" + path.Base(p.Dest)
	}
}

// --- what each person already has --------------------------------------------

// photoIndex is the files in one person's dated folders by size, and the
// hashes of those it has had to compare, so a dropped photo or a download is checked
// against what is there without hashing the folder for every file.
type photoIndex struct {
	mu     sync.Mutex
	built  time.Time
	bySize map[int64][]string
	sums   map[string][32]byte
}

type photoIndexes struct {
	mu sync.Mutex
	m  map[string]*photoIndex
}

// indexFreshness is how long a folder's list is trusted: files copied in by
// hand in the meantime are found when it is made again.
const indexFreshness = 10 * time.Minute

func (s *Server) photoIndexFor(u state.User) *photoIndex {
	all := &s.photoIndexes
	all.mu.Lock()
	if all.m == nil {
		all.m = map[string]*photoIndex{}
	}
	ix, ok := all.m[u.ID]
	if !ok {
		ix = &photoIndex{}
		all.m[u.ID] = ix
	}
	all.mu.Unlock()
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if time.Since(ix.built) > indexFreshness {
		ix.bySize, ix.built = map[int64][]string{}, time.Now()
		if ix.sums == nil {
			ix.sums = map[string][32]byte{}
		}
		dir := filepath.Join(s.library.PathFor(media.KindPicture), filepath.FromSlash(library.PersonalFolder(u.Name)))
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.Type().IsRegular() && library.IsPictureFile(p) && managedPhoto(dir, p) {
				if info, err := d.Info(); err == nil {
					ix.bySize[info.Size()] = append(ix.bySize[info.Size()], p)
				}
			}
			return nil
		})
	}
	return ix
}

// has reports whether an identical file is already in the folder.
func (ix *photoIndex) has(size int64, sum [32]byte) bool {
	_, ok := ix.find(size, sum)
	return ok
}

// move follows a file the folder's list knows to a new place.
func (ix *photoIndex) move(from, to string) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	for size, ps := range ix.bySize {
		for i, p := range ps {
			if p == from {
				ps[i] = to
				ix.bySize[size] = ps
			}
		}
	}
	if sum, ok := ix.sums[from]; ok {
		delete(ix.sums, from)
		ix.sums[to] = sum
	}
}

// find is an identical file already in the folder, by its path.
func (ix *photoIndex) find(size int64, sum [32]byte) (string, bool) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	for _, p := range ix.bySize[size] {
		known, ok := ix.sums[p]
		if !ok {
			f, err := os.Open(p)
			if err != nil {
				continue
			}
			h := sha256.New()
			_, err = io.Copy(h, f)
			f.Close()
			if err != nil {
				continue
			}
			copy(known[:], h.Sum(nil))
			ix.sums[p] = known
		}
		if known == sum {
			return p, true
		}
	}
	return "", false
}

// add records a file just saved.
func (ix *photoIndex) add(p string, size int64, sum [32]byte) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if ix.bySize == nil {
		return
	}
	ix.bySize[size] = append(ix.bySize[size], p)
	ix.sums[p] = sum
}

// errPhotoDuplicate is a photo already in the person's folder under any name:
// a skip, not a failure, as for any upload already there.
var errPhotoDuplicate = fmt.Errorf("%w: it is already in your photos", library.ErrAlreadyThere)

// datedPhoto decides where a dropped photo or video goes in its owner's
// folder, once it has arrived: by the year and month it was taken - the date
// inside it, else one in its name, else the date the file itself carries
// (hint, from the device: on a camera's card that is when it was taken),
// else Undated/ - and not at all if the same file is already there. A whole
// SD card or folder of old photos is sorted this way, like a download.
type photoPlace struct {
	rel     string // relative to pictures/
	meta    photoimport.Meta
	src     photoimport.DateSource
	exif    bool
	size    int64
	sum     [32]byte
	ix      *photoIndex
	dropped string
}

func (s *Server) datedPhoto(u state.User, staged, dropped string, hint int64) (*photoPlace, error) {
	f, err := os.Open(staged)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	head := make([]byte, 4<<20)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	h.Write(head)
	rest, err := io.Copy(h, f)
	if err != nil {
		return nil, err
	}
	pl := &photoPlace{size: int64(n) + rest, dropped: dropped}
	copy(pl.sum[:], h.Sum(nil))
	pl.ix = s.photoIndexFor(u)
	name := path.Base(dropped)
	if t, ok := photoimport.ExifTaken(head); ok {
		pl.meta.Taken, pl.src, pl.exif = t, photoimport.SourceExif, true
	} else if t, ok := photoimport.NameTaken(name); ok {
		pl.meta.Taken, pl.src = t, photoimport.SourceName
	} else if hint > 0 && time.UnixMilli(hint).Year() > 1990 {
		pl.meta.Taken, pl.src = time.UnixMilli(hint).UTC(), photoimport.SourceFile
	}
	// Already kept: not saved again, but what this copy knows (a date from
	// a better source) improves the one kept.
	if existing, ok := pl.ix.find(pl.size, pl.sum); ok {
		if s.improvePhoto(u, existing, pl.meta, pl.src) {
			return nil, fmt.Errorf("%w: it is already in your photos; its date was corrected from this copy", library.ErrAlreadyThere)
		}
		return nil, errPhotoDuplicate
	}
	rel := "Undated/" + name
	if !pl.meta.Taken.IsZero() {
		rel = fmt.Sprintf("%04d/%02d/%s", pl.meta.Taken.Year(), int(pl.meta.Taken.Month()), name)
	}
	// Another file of the same name taken the same month keeps both.
	if s.library.PersonalHas(u.Name, rel, 0) {
		rel = altName(rel, int64(pl.sum[0])<<16|int64(pl.sum[1])<<8|int64(pl.sum[2]))
	}
	if pl.rel, err = library.PersonalPath(u.Name, rel); err != nil {
		return nil, err
	}
	return pl, nil
}

// savePhoto saves an upload to the picture shelf into the person's folder by
// date, writing the date beside it for Immich when it did not come from
// inside the photo (the saved file is new, and Immich would date it today).
func (s *Server) savePhoto(u state.User, dropped string, body io.Reader, hint int64) (string, error) {
	var pl *photoPlace
	dest, err := s.library.SaveDecided(media.KindPicture, dropped, body, func(staged string) (string, error) {
		var err error
		pl, err = s.datedPhoto(u, staged, dropped, hint)
		if err != nil {
			return "", err
		}
		return pl.rel, nil
	})
	if err != nil {
		return "", err
	}
	full := filepath.Join(s.library.Root(), filepath.FromSlash(dest))
	pl.ix.add(full, pl.size, pl.sum)
	if !pl.exif && !pl.meta.Taken.IsZero() {
		_ = os.WriteFile(full+".xmp", photoimport.XMPSidecarFrom(pl.meta, pl.src), 0o666)
	}
	if !u.IsOwner() {
		s.addPhotoBytes(u, pl.size)
	}
	return dest, nil
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
	if !s.backupAccount(w, r, u) {
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
	if !s.backupAccount(w, r, u) {
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

// backupAccount refuses a phone's backup made for somebody else. The app
// sends the account backup was turned on for: on a shared phone, with the
// next person signed in, the cookie is theirs, and the first person's new
// photos went into the second's folder (a security review).
func (s *Server) backupAccount(w http.ResponseWriter, r *http.Request, u state.User) bool {
	want := r.URL.Query().Get("account")
	if want == "" || want == u.ID {
		return true
	}
	writeError(w, http.StatusForbidden, "this phone's backup was turned on by another account; turn it on again to back up here")
	return false
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

// datedFolder is a folder SoundStorm made by date (2019/07, or Undated) rather
// than one somebody arranged and copied in.
//
// Only photos in these count as already kept (the owner's rule): a copy of a
// photo that sits only in a folder somebody arranged themselves is still
// filed into the dated folders, and a duplicate improves only a copy here.
// Their own folders are theirs, left exactly as they are.
var datedFolder = regexp.MustCompile(`^((19|20)\d{2}/(0[1-9]|1[0-2])|Undated)$`)

// improvePhoto gives a photo kept in a person's folder what a duplicate of it
// knew and it did not: a date from a better source (the date inside it beats
// Google's or Apple's record, which beats one in a name, which beats a file's
// own date) or a place where it had none, written in the sidecar beside it;
// and, when the date improved, moves it to that date's folder - out of
// Undated, or the wrong month - unless it sits in a folder somebody arranged.
// The photo itself never changes. Reports whether anything did.
func (s *Server) improvePhoto(u state.User, existing string, inc photoimport.Meta, incSrc photoimport.DateSource) bool {
	folder := filepath.Join(s.library.PathFor(media.KindPicture), filepath.FromSlash(library.PersonalFolder(u.Name)))
	rel, err := filepath.Rel(folder, existing)
	if err != nil || strings.HasPrefix(rel, "..") {
		return false
	}
	f, err := os.Open(existing)
	if err != nil {
		return false
	}
	head := make([]byte, 4<<20)
	n, _ := io.ReadFull(f, head)
	f.Close()
	head = head[:n]

	var have photoimport.Meta
	haveSrc := photoimport.SourceNone
	exifDate := false
	if t, ok := photoimport.ExifTaken(head); ok {
		have.Taken, haveSrc, exifDate = t, photoimport.SourceExif, true
	}
	side, _ := os.ReadFile(existing + ".xmp")
	if len(side) > 0 {
		sm, ssrc := photoimport.ReadSidecar(side)
		if !exifDate && !sm.Taken.IsZero() {
			have.Taken, haveSrc = sm.Taken, ssrc
		}
		if sm.HasPlace {
			have.Lat, have.Lon, have.HasPlace = sm.Lat, sm.Lon, true
		}
	}
	haveGPS := have.HasPlace || photoimport.ExifHasPlace(head)
	merged, src, dateBetter, changed := photoimport.Better(have, haveSrc, haveGPS, inc, incSrc)
	if !changed {
		return false
	}
	write := merged
	if exifDate && len(side) == 0 {
		// The date inside the photo stands; the sidecar adds only the place.
		write.Taken = time.Time{}
	}
	if err := os.WriteFile(existing+".xmp", photoimport.XMPSidecarFrom(write, src), 0o666); err != nil {
		return false
	}
	slash := filepath.ToSlash(rel)
	if dateBetter && datedFolder.MatchString(path.Dir(slash)) {
		want := fmt.Sprintf("%04d/%02d/%s", merged.Taken.Year(), int(merged.Taken.Month()), path.Base(slash))
		if want != slash {
			// Never over another photo: a name taken, then a free one beside it
			// (a security review found the second name was not checked).
			dest := ""
			for i := 0; i < 20; i++ {
				try := want
				if i > 0 {
					try = altName(want, merged.Taken.Unix()+int64(i-1))
				}
				p := filepath.Join(folder, filepath.FromSlash(try))
				if _, err := os.Lstat(p); os.IsNotExist(err) {
					if _, err := os.Lstat(p + ".xmp"); os.IsNotExist(err) {
						dest = p
						break
					}
				}
			}
			if dest != "" && os.MkdirAll(filepath.Dir(dest), 0o777) == nil && library.MoveNoClobber(existing, dest) == nil {
				_ = library.MoveNoClobber(existing+".xmp", dest+".xmp")
				s.photoIndexFor(u).move(existing, dest)
				_ = os.Remove(filepath.Dir(existing)) // the month it left, if now empty
			}
		}
	}
	s.log.Info("a photo's details improved from a copy of it", "by", u.Name, "photo", slash, "date", dateBetter)
	s.scheduleRescan(media.KindPicture)
	return true
}

// managedPhoto reports whether a file in a person's folder is in one of the
// folders SoundStorm sorts photos into, by date.
func managedPhoto(personal, file string) bool {
	rel, err := filepath.Rel(personal, file)
	if err != nil {
		return false
	}
	return datedFolder.MatchString(path.Dir(filepath.ToSlash(rel)))
}
