package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"runtime/debug"
	"time"
)

// Syncing books for read-along by themselves: as soon as there is both an
// ebook and an audiobook of a book, it is handed to Storyteller, which works
// through them one at a time. On unless the owner turns it off in Settings -
// a sync is the better part of an hour of the computer's time for a long book,
// which somebody may reasonably not want.

const (
	// autoFirstDelay gives the backends a moment after startup to finish
	// provisioning and listing their shelves.
	autoFirstDelay = 2 * time.Minute
	// autoEvery catches books that arrived some way the app did not see.
	autoEvery = 30 * time.Minute
	// autoAfterKick is how long after a new book is scanned to look: the
	// audiobook server indexes a folder in its own time.
	autoAfterKick = 3 * time.Minute
)

// RunAutoReadAlong looks for new pairs to sync until ctx ends. Started once
// with go, answering to no request, so it recovers its own panics.
func (s *Server) RunAutoReadAlong(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("read-along auto-sync panicked; recovered", "panic", r, "stack", string(debug.Stack()))
		}
	}()
	next := time.NewTimer(autoFirstDelay)
	defer next.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.autoKick:
			next.Reset(autoAfterKick)
			continue
		case <-next.C:
		}
		s.autoReadAlongOnce(ctx)
		next.Reset(autoEvery)
	}
}

// kickAutoReadAlong asks for a look soon, without waiting for the half hour.
func (s *Server) kickAutoReadAlong() {
	select {
	case s.autoKick <- struct{}{}:
	default:
	}
}

// autoReadAlongOnce starts every pair Storyteller does not have yet. With no
// account on the context the registry answers unrestricted, which is right
// for work the server does on its own.
func (s *Server) autoReadAlongOnce(parent context.Context) {
	if !s.store.AutoReadAlong() {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	st, ok := s.readAlong(ctx)
	if !ok {
		return
	}
	for _, p := range s.listPairs(ctx) {
		folder := p.Audiobook.Extra["folder"]
		if folder == "" {
			continue
		}
		skipKey := p.Ebook.SourceID + "/" + p.Ebook.ID + "|" + folder
		s.autoMu.Lock()
		skip := s.autoSkip[skipKey]
		s.autoMu.Unlock()
		if skip {
			continue
		}
		existing, found, err := st.ByFolder(ctx, folder)
		if err != nil {
			return // Storyteller is not answering; try again next time
		}
		if found && existing.Ebook != nil {
			continue // synced, syncing, or failed - none of which is ours to redo
		}
		ebookPath, err := s.ebookFile(ctx, itemRef{p.Ebook.SourceID, p.Ebook.ID})
		if err == nil {
			var audio []string
			if _, audio, err = s.audiobookFiles(ctx, itemRef{p.Audiobook.SourceID, p.Audiobook.ID}); err == nil {
				_, err = st.Sync(ctx, folder, ebookPath, audio)
			}
		}
		if err != nil {
			s.log.Info("read-along could not sync a book by itself", "book", p.Ebook.Title, "err", err)
			s.autoMu.Lock()
			s.autoSkip[skipKey] = true
			s.autoMu.Unlock()
			continue
		}
		s.log.Info("read-along sync started by itself", "book", p.Ebook.Title)
	}
}

// handleSetAutoReadAlong turns syncing by itself on or off. Owner-only: it
// spends the server's time.
func (s *Server) handleSetAutoReadAlong(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "expected a JSON body with enabled")
		return
	}
	if err := s.store.SetAutoReadAlong(body.Enabled); err != nil {
		writeError(w, http.StatusInternalServerError, "could not save the setting")
		return
	}
	if body.Enabled {
		// Turned on: look now rather than in half an hour.
		s.autoMu.Lock()
		s.autoSkip = map[string]bool{}
		s.autoMu.Unlock()
		go s.autoReadAlongOnce(context.Background())
	}
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": body.Enabled})
}
