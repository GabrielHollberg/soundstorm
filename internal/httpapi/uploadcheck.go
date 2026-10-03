package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"
	"strconv"

	"github.com/GabrielHollberg/soundstorm/internal/auth"
	"github.com/GabrielHollberg/soundstorm/internal/library"
	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/source"
	"github.com/GabrielHollberg/soundstorm/internal/state"
)

// POST /api/upload/check {"files": [{"dest", "kind", "size"}]}: before a byte
// moves, which of these would land on a name already taken, and what is
// there - its size, and its Sample when the size matches (library/conflict.go).
// The page compares Samples itself: an exact copy is not sent at all, and a
// different file with the name is asked about.
//
// A photo is never asked about - a different photo with the name is kept
// beside it (savePhoto) - but an exact copy anywhere in the person's dated
// folders is reported, by the Samples of the files that size there.
func (s *Server) handleUploadCheck(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Files []struct {
			Dest string `json:"dest"`
			Kind string `json:"kind"`
			Size int64  `json:"size"`
		} `json:"files"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPlanBody)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "expected a JSON body with a list of files")
		return
	}
	if len(body.Files) > 5000 {
		writeError(w, http.StatusBadRequest, "too many files at once")
		return
	}
	user, _ := auth.FromContext(r.Context())
	access := source.AccessFrom(r.Context())
	type answer struct {
		Taken   bool     `json:"taken"`
		Size    int64    `json:"size,omitempty"`
		Sample  string   `json:"sample,omitempty"`
		Samples []string `json:"samples,omitempty"`
	}
	out := make([]answer, len(body.Files))
	var photos *photoIndex
	for i, f := range body.Files {
		kind, ok := media.ParseKind(f.Kind)
		if !ok || !access.Permits(kind) || f.Size < 0 {
			continue
		}
		if kind == media.KindPicture {
			if s.library.PathFor(media.KindPicture) == "" {
				continue
			}
			if photos == nil {
				photos = s.photoIndexFor(user)
			}
			out[i].Samples = photos.samplesOfSize(f.Size)
			out[i].Taken = len(out[i].Samples) > 0
			continue
		}
		st, err := s.library.CheckDest(kind, f.Dest, f.Size)
		if err != nil {
			continue
		}
		out[i] = answer{Taken: st.Taken, Size: st.Size, Sample: st.Sample}
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": out})
}

// samplesOfSize is the Samples of the files of this size in the folder.
func (ix *photoIndex) samplesOfSize(size int64) []string {
	ix.mu.Lock()
	paths := append([]string(nil), ix.bySize[size]...)
	ix.mu.Unlock()
	var out []string
	for _, p := range paths {
		if len(out) >= 20 {
			break
		}
		if s, err := library.Sample(p); err == nil {
			out = append(out, s)
		}
	}
	return out
}

// uploadConflict is what an upload asked to do with a taken name: keep both,
// or - the owner, as deleting is - replace, the old one going to the bin.
var errReplaceOwnerOnly = errors.New("only the owner can replace a file already in the library")

func (s *Server) uploadOptions(r *http.Request, kind media.Kind) (library.SaveOptions, error) {
	user, _ := auth.FromContext(r.Context())
	return s.saveOptions(user, kind, r.URL.Query().Get("conflict"), r.URL.Query().Get("as"))
}

// saveOptions is what to do about a taken name: keep both (under as, when it
// is free), or - the owner only, as deleting is - put the old one in the bin.
func (s *Server) saveOptions(user state.User, kind media.Kind, conflict, as string) (library.SaveOptions, error) {
	switch library.Conflict(conflict) {
	case library.ConflictKeep:
		return library.SaveOptions{Conflict: library.ConflictKeep, Name: as}, nil
	case library.ConflictReplace:
		if !user.IsOwner() {
			return library.SaveOptions{}, errReplaceOwnerOnly
		}
		return library.SaveOptions{Conflict: library.ConflictReplace, Replace: func(rel string) error {
			_, err := s.library.MoveToBin([]library.BinItem{{Title: path.Base(rel), Kind: kind, Paths: []string{rel}}}, user.Name)
			return err
		}}, nil
	}
	return library.SaveOptions{}, nil
}

// POST /api/upload/describe?kind=&dest=&size=&head=: the name a different
// file whose name dest is taken would be given - for what makes it
// different (library/distinct.go) - shown in the question, where it can be
// changed. The body is the file's first head bytes and its last, up to a
// megabyte each: enough for its camera, picture size, length or narrator.
func (s *Server) handleUploadDescribe(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	kind, ok := media.ParseKind(q.Get("kind"))
	if !ok || !source.AccessFrom(r.Context()).Permits(kind) {
		writeError(w, http.StatusBadRequest, "not a shelf you can add to")
		return
	}
	size, _ := strconv.ParseInt(q.Get("size"), 10, 64)
	head, _ := strconv.ParseInt(q.Get("head"), 10, 64)
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 2<<20+1))
	if err != nil || size <= 0 || head < 0 || head > int64(len(body)) || int64(len(body)) > size {
		writeError(w, http.StatusBadRequest, "expected the two ends of the file")
		return
	}
	ends := &fileEnds{head: body[:head], tail: body[head:], size: size}
	name, err := s.library.TakenName(kind, q.Get("dest"), ends, size)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name})
}

// fileEnds reads a file from its two ends only; the middle is not there.
type fileEnds struct {
	head, tail []byte
	size       int64
}

func (f *fileEnds) ReadAt(p []byte, off int64) (int, error) {
	end := off + int64(len(p))
	switch {
	case off < 0 || off >= f.size:
		return 0, io.EOF
	case end <= int64(len(f.head)):
		return copy(p, f.head[off:end]), nil
	case off >= f.size-int64(len(f.tail)):
		from := off - (f.size - int64(len(f.tail)))
		n := copy(p, f.tail[from:])
		if n < len(p) {
			return n, io.EOF
		}
		return n, nil
	case off < int64(len(f.head)):
		// The start of a read the head ends inside: what it has, then stop.
		return copy(p, f.head[off:]), io.ErrUnexpectedEOF
	}
	return 0, io.ErrUnexpectedEOF
}
