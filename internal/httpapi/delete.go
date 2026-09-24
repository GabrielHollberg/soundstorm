package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/GabrielHollberg/soundstorm/internal/auth"
	"github.com/GabrielHollberg/soundstorm/internal/library"
	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/source"
)

// Deleting items, for the owner only.
//
// Only the owner, because every other account shares these shelves with the
// rest of the house: a member who could delete could empty a shelf everybody
// uses. And never at once - everything goes to the library's bin (see
// internal/library/bin.go) for thirty days, with an undo, because a wrong tap
// on a film collection should be a mistake and not a disaster.
//
// Three calls: a preview, so the confirmation can say exactly what goes
// ("41 files, 2.3 GB") before anything moves; the delete; and the undo.

const (
	maxDeleteItems = 500
	maxDeleteBody  = 128 << 10
)

type deleteRequest struct {
	Items []struct {
		Source string `json:"source"`
		ID     string `json:"id"`
		Title  string `json:"title"`
	} `json:"items"`
}

// resolveDeletion asks each item's shelf for its files and works out what goes
// with them. It writes the error response itself and reports false when it
// did.
func (s *Server) resolveDeletion(w http.ResponseWriter, r *http.Request) ([]library.BinItem, bool) {
	var req deleteRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxDeleteBody))
	if err := dec.Decode(&req); err != nil || len(req.Items) == 0 {
		writeError(w, http.StatusBadRequest, "expected a JSON body with the items to delete")
		return nil, false
	}
	if len(req.Items) > maxDeleteItems {
		writeError(w, http.StatusRequestEntityTooLarge, "that is too many items at once; delete fewer")
		return nil, false
	}
	var items []library.BinItem
	for _, it := range req.Items {
		title := it.Title
		if title == "" {
			title = "that item"
		}
		// The same lookup every other route makes, which is what applies the
		// account's access - an owner sees every shelf, but the check stays
		// in one place.
		src, ok := s.reg.ByID(r.Context(), it.Source)
		if !ok {
			writeError(w, http.StatusNotFound, "could not find "+title)
			return nil, false
		}
		lister, ok := src.(source.FileLister)
		if !ok {
			writeError(w, http.StatusBadRequest, title+" cannot be deleted from SoundStorm")
			return nil, false
		}
		files, err := lister.ItemFiles(r.Context(), it.ID)
		if err != nil {
			s.log.Warn("delete: could not find an item's files", "source", it.Source, "id", it.ID, "err", err)
			writeError(w, http.StatusBadGateway, "could not find the files for "+title)
			return nil, false
		}
		paths, err := s.library.Resolve(src.Kind(), files)
		if err != nil {
			s.log.Warn("delete: refused a path", "source", it.Source, "id", it.ID, "err", err)
			writeError(w, http.StatusConflict, "could not delete "+title+": its files are not where SoundStorm expected")
			return nil, false
		}
		items = append(items, library.BinItem{Title: title, Kind: src.Kind(), Paths: paths})
	}
	return items, true
}

func (s *Server) handleDeletePreview(w http.ResponseWriter, r *http.Request) {
	items, ok := s.resolveDeletion(w, r)
	if !ok {
		return
	}
	var all []string
	for _, it := range items {
		all = append(all, it.Paths...)
	}
	files, bytes := s.library.Measure(all)
	writeJSON(w, http.StatusOK, map[string]any{
		"items": len(items),
		"files": files,
		"bytes": bytes,
	})
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	items, ok := s.resolveDeletion(w, r)
	if !ok {
		return
	}
	user, _ := auth.FromContext(r.Context())
	entry, err := s.library.MoveToBin(items, user.Name)
	if err != nil {
		s.log.Warn("delete failed", "err", err)
		writeError(w, http.StatusInternalServerError, "could not delete: "+desensitizeFSError(err))
		return
	}
	s.log.Info("moved to the bin", "entry", entry.ID, "items", len(items), "files", entry.Files, "by", user.Name)
	s.rescanKinds(items)
	writeJSON(w, http.StatusOK, map[string]any{
		"entry": entry.ID,
		"items": len(items),
		"files": entry.Files,
		"bytes": entry.Bytes,
	})
}

func (s *Server) handleDeleteUndo(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Entry string `json:"entry"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxCredentialBody))
	if err := dec.Decode(&body); err != nil || body.Entry == "" {
		writeError(w, http.StatusBadRequest, "expected a JSON body with entry")
		return
	}
	entry, restored, blocked, err := s.library.Restore(body.Entry)
	if errors.Is(err, library.ErrNotInBin) {
		writeError(w, http.StatusNotFound, "that is no longer in the bin")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not undo: "+desensitizeFSError(err))
		return
	}
	s.rescanKinds(entry.Items)
	writeJSON(w, http.StatusOK, map[string]any{
		"restored": restored,
		"blocked":  len(blocked),
	})
}

// rescanKinds tells each shelf touched to look now, so a deleted item leaves
// search within seconds rather than at the backend's next sweep - and an
// undone one comes back as fast.
func (s *Server) rescanKinds(items []library.BinItem) {
	seen := map[media.Kind]bool{}
	for _, it := range items {
		if !seen[it.Kind] {
			seen[it.Kind] = true
			s.scheduleRescan(it.Kind)
		}
	}
}
