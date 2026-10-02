package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"path"

	"github.com/GabrielHollberg/soundstorm/internal/auth"
	"github.com/GabrielHollberg/soundstorm/internal/library"
	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/source"
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
	switch library.Conflict(r.URL.Query().Get("conflict")) {
	case library.ConflictKeep:
		return library.SaveOptions{Conflict: library.ConflictKeep}, nil
	case library.ConflictReplace:
		user, _ := auth.FromContext(r.Context())
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
