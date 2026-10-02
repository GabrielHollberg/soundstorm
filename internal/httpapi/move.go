package httpapi

// Moving an item to another shelf, for the owner (as deleting is: every
// other account shares these shelves). Any item's menu offers "Move to": a
// film that was a home video, a music folder that was an audiobook. The
// files are filed by the new shelf's own rule (library.MoveItems); to the
// photos, they go to the owner's own folder by when each was taken.
//
//	POST /api/move {"items": [{source, id, title}], "to": "tv"}

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"

	"github.com/GabrielHollberg/soundstorm/internal/auth"
	"github.com/GabrielHollberg/soundstorm/internal/library"
	"github.com/GabrielHollberg/soundstorm/internal/media"
)

func (s *Server) handleMove(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Items []libraryItemRef `json:"items"`
		To    string           `json:"to"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxDeleteBody)).Decode(&req); err != nil || len(req.Items) == 0 {
		writeError(w, http.StatusBadRequest, "expected a JSON body with the items to move and where to")
		return
	}
	to, ok := media.ParseKind(req.To)
	if !ok || s.library.PathFor(to) == "" {
		writeError(w, http.StatusBadRequest, "there is no such library")
		return
	}
	if len(req.Items) > maxDeleteItems {
		writeError(w, http.StatusRequestEntityTooLarge, "that is too many items at once; move fewer")
		return
	}
	items, ok := s.resolveItemFiles(w, r, req.Items)
	if !ok {
		return
	}
	user, _ := auth.FromContext(r.Context())
	var place library.Place
	saved := map[string]*photoPlace{}
	if to == media.KindPicture {
		if _, err := s.library.EnsurePersonalFolder(user.Name); err != nil {
			writeError(w, http.StatusInternalServerError, "could not make your photo folder")
			return
		}
		place = func(abs, rel string) (string, error) {
			pl, err := s.datedPhoto(user, abs, rel, 0)
			if err != nil {
				return "", err
			}
			saved[abs] = pl
			return pl.rel, nil
		}
	}
	results, err := s.library.MoveItems(items, to, place)
	if errors.Is(err, library.ErrShelfRefuses) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, desensitizeFSError(err))
		return
	}
	moved, kept := 0, 0
	dest := ""
	for _, res := range results {
		if res.To == "" {
			kept++
			continue
		}
		moved++
		dest = res.To
		// A photo that arrived: its index, its date beside it, its space.
		if pl := saved[filepath.Join(s.library.Root(), filepath.FromSlash(res.From))]; pl != nil {
			s.photoSaved(user, res.To, pl)
		}
	}
	s.log.Info("moved to another library", "items", len(items), "files", moved, "to", to, "by", user.Name)
	s.rescanKinds(items)
	s.scheduleRescan(to)
	writeJSON(w, http.StatusOK, map[string]any{"moved": moved, "stayed": kept, "dest": dest})
}
