package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/GabrielHollberg/soundstorm/internal/collections"
	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/source"
)

// Favourites and playlists, per person. See internal/collections for why they
// are SoundStorm's rather than the backends'.
//
// An item is always looked up on its backend before it is kept - the snapshot
// is what the backend says, never what the browser sent - and through the
// registry, so the account's access applies both when something is added and
// every time a list is shown.

const maxCollectionBody = 8 << 10

// resolveItem finds the item named by ?source=&id= (or the same fields in a
// JSON body), writing the error itself and reporting false when it did.
func (s *Server) resolveItem(w http.ResponseWriter, r *http.Request, sourceID, itemID string) (media.Item, bool) {
	if sourceID == "" || itemID == "" || len(itemID) > maxProgressItemID {
		writeError(w, http.StatusBadRequest, "source and id are required")
		return media.Item{}, false
	}
	src, ok := s.reg.ByID(r.Context(), sourceID)
	if !ok {
		writeError(w, http.StatusNotFound, "no such item")
		return media.Item{}, false
	}
	getter, ok := src.(source.ItemGetter)
	if !ok {
		writeError(w, http.StatusBadRequest, "that cannot be kept in a list")
		return media.Item{}, false
	}
	item, ok := getter.ItemByID(r.Context(), itemID)
	if !ok {
		writeError(w, http.StatusNotFound, "no such item")
		return media.Item{}, false
	}
	return item, true
}

// visible drops entries on shelves this account can no longer see - a
// favourite kept before the owner took Films away is not a way back in.
func (s *Server) visible(r *http.Request, entries []collections.Entry) []media.Item {
	out := make([]media.Item, 0, len(entries))
	for _, e := range entries {
		if _, ok := s.reg.ByID(r.Context(), e.Item.SourceID); ok {
			out = append(out, e.Item)
		}
	}
	return out
}

func (s *Server) collectionsError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, collections.ErrNotFound):
		writeError(w, http.StatusNotFound, "no such playlist")
	case errors.Is(err, collections.ErrFull):
		writeError(w, http.StatusInsufficientStorage, "that list is full")
	default:
		s.log.Warn("collections", "err", err)
		writeError(w, http.StatusBadRequest, err.Error())
	}
}

// --- favourites ---------------------------------------------------------------

func (s *Server) handleFavourites(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	entries, err := s.collections.Favourites(user.ID)
	if err != nil {
		s.collectionsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": s.visible(r, entries)})
}

func (s *Server) handleAddFavourite(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	item, ok := s.resolveItem(w, r, r.URL.Query().Get("source"), r.URL.Query().Get("id"))
	if !ok {
		return
	}
	if err := s.collections.AddFavourite(user.ID, item); err != nil {
		s.collectionsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"favourite": true})
}

func (s *Server) handleRemoveFavourite(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	if err := s.collections.RemoveFavourite(user.ID, q.Get("source"), q.Get("id")); err != nil {
		s.collectionsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"favourite": false})
}

// --- playlists ----------------------------------------------------------------

func playlistSummary(p collections.Playlist) map[string]any {
	return map[string]any{"id": p.ID, "name": p.Name, "count": len(p.Items), "updatedAt": p.UpdatedAt}
}

func (s *Server) handlePlaylists(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	lists, err := s.collections.Playlists(user.ID)
	if err != nil {
		s.collectionsError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(lists))
	for _, p := range lists {
		out = append(out, playlistSummary(p))
	}
	writeJSON(w, http.StatusOK, map[string]any{"playlists": out})
}

func (s *Server) handleCreatePlaylist(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxCollectionBody)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "expected a JSON body with name")
		return
	}
	p, err := s.collections.CreatePlaylist(user.ID, body.Name)
	if err != nil {
		s.collectionsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, playlistSummary(p))
}

func (s *Server) handlePlaylist(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	p, err := s.collections.Playlist(user.ID, r.PathValue("id"))
	if err != nil {
		s.collectionsError(w, err)
		return
	}
	// Positions are the playlist's own, so an entry on a shelf this account
	// cannot see is left in place but not shown - and its position is kept, so
	// removing or moving by index still means the same entry.
	type row struct {
		media.Item
		Position int `json:"position"`
	}
	items := make([]row, 0, len(p.Items))
	for i, e := range p.Items {
		if _, ok := s.reg.ByID(r.Context(), e.Item.SourceID); ok {
			items = append(items, row{Item: e.Item, Position: i})
		}
	}
	out := playlistSummary(p)
	out["items"] = items
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleRenamePlaylist(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxCollectionBody)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "expected a JSON body with name")
		return
	}
	if err := s.collections.RenamePlaylist(user.ID, r.PathValue("id"), body.Name); err != nil {
		s.collectionsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"renamed": true})
}

func (s *Server) handleDeletePlaylist(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	if err := s.collections.DeletePlaylist(user.ID, r.PathValue("id")); err != nil {
		s.collectionsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

func (s *Server) handleAddToPlaylist(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var body struct {
		Source string `json:"source"`
		ID     string `json:"id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxCollectionBody)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "expected a JSON body with source and id")
		return
	}
	item, ok := s.resolveItem(w, r, body.Source, body.ID)
	if !ok {
		return
	}
	// A playlist is songs, played one after another in the audio player.
	if item.Kind != media.KindMusic {
		writeError(w, http.StatusBadRequest, "only music can go in a playlist")
		return
	}
	count, err := s.collections.AddToPlaylist(user.ID, r.PathValue("id"), item)
	if err != nil {
		s.collectionsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": count})
}

func (s *Server) handleRemoveFromPlaylist(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	index, err := strconv.Atoi(r.PathValue("position"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "position must be a number")
		return
	}
	if err := s.collections.RemoveFromPlaylist(user.ID, r.PathValue("id"), index); err != nil {
		s.collectionsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"removed": true})
}

func (s *Server) handleMoveInPlaylist(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var body struct {
		From int `json:"from"`
		To   int `json:"to"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxCollectionBody)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "expected a JSON body with from and to")
		return
	}
	if err := s.collections.MovePlaylistItem(user.ID, r.PathValue("id"), body.From, body.To); err != nil {
		s.collectionsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"moved": true})
}
