package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/source"
	"github.com/GabrielHollberg/soundstorm/internal/source/storyteller"
)

// Read-along: the page following the audiobook. Storyteller does the
// matching (see internal/source/storyteller); these endpoints hand it a pair
// from Read & listen, report how far it has got, and give the reader the
// synced book with a timeline of its sentences on the audiobook's own clock.

const maxReadAlongBody = 2048

// readAlong finds Storyteller through the registry, so an account that may not
// read ebooks never reaches it.
func (s *Server) readAlong(ctx context.Context) (*storyteller.Source, bool) {
	for _, src := range s.reg.All(ctx) {
		if st, ok := src.(*storyteller.Source); ok {
			return st, true
		}
	}
	return nil, false
}

type itemRef struct {
	SourceID string `json:"sourceId"`
	ID       string `json:"id"`
}

// pairStatuses is each pair's sync, where Storyteller has one, found by the
// audiobook's folder.
func (s *Server) pairStatuses(ctx context.Context, st *storyteller.Source, pairs []bookPair) map[int]storyteller.Status {
	out := map[int]storyteller.Status{}
	for i, p := range pairs {
		folder := p.Audiobook.Extra["folder"]
		if folder == "" {
			continue
		}
		b, ok, err := st.ByFolder(ctx, folder)
		if err != nil {
			return out
		}
		if ok && b.Ebook != nil {
			out[i] = storyteller.StatusOf(b)
		}
	}
	return out
}

// handleStartReadAlong hands a pair to Storyteller.
func (s *Server) handleStartReadAlong(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Ebook     itemRef `json:"ebook"`
		Audiobook itemRef `json:"audiobook"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxReadAlongBody)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "expected an ebook and an audiobook")
		return
	}
	st, ok := s.readAlong(r.Context())
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "read-along is not set up on this server")
		return
	}
	ebookPath, err := s.ebookFile(r.Context(), body.Ebook)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	layout, audio, err := s.audiobookFiles(r.Context(), body.Audiobook)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	status, err := st.Sync(r.Context(), layout.Folder, ebookPath, audio)
	if err != nil {
		s.log.Warn("read-along could not start", "err", err)
		writeError(w, http.StatusBadGateway, "read-along could not start this book")
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// handleNotSameBook marks a Read & listen match wrong - or, undone, right
// again. Owner-only, like deleting: it changes the library for everyone, and
// it deletes whatever Storyteller made of the pair, synced or waiting, since
// following one book with another's voice is the thing to avoid. Storyteller
// only ever deletes inside its own storage; the recording is the shelf's,
// mounted read-only to it besides.
func (s *Server) handleNotSameBook(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Ebook     itemRef `json:"ebook"`
		Audiobook itemRef `json:"audiobook"`
		Wrong     bool    `json:"wrong"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxReadAlongBody)).Decode(&body); err != nil ||
		body.Ebook.ID == "" || body.Audiobook.ID == "" || len(body.Ebook.ID)+len(body.Audiobook.ID) > 400 {
		writeError(w, http.StatusBadRequest, "expected an ebook and an audiobook")
		return
	}
	if err := s.store.SetNotPair(notPairKey(body.Ebook, body.Audiobook), body.Wrong); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.Wrong {
		if st, ok := s.readAlong(r.Context()); ok {
			if layout, _, err := s.audiobookFiles(r.Context(), body.Audiobook); err == nil {
				if book, found, err := st.ByFolder(r.Context(), layout.Folder); err == nil && found {
					if err := st.Delete(r.Context(), book.UUID); err != nil {
						s.log.Warn("read-along could not delete a wrong match", "err", err)
					}
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"wrong": body.Wrong})
}

// handleReadAlongNext moves a pair's waiting sync to the front of the queue.
func (s *Server) handleReadAlongNext(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Audiobook itemRef `json:"audiobook"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxReadAlongBody)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "expected an audiobook")
		return
	}
	st, ok := s.readAlong(r.Context())
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "read-along is not set up on this server")
		return
	}
	layout, _, err := s.audiobookFiles(r.Context(), body.Audiobook)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	book, found, err := st.ByFolder(r.Context(), layout.Folder)
	if err != nil || !found {
		writeError(w, http.StatusNotFound, "this book is not waiting to sync")
		return
	}
	if err := st.SyncNext(r.Context(), book.UUID); err != nil {
		s.log.Warn("read-along could not reorder its queue", "err", err)
		writeError(w, http.StatusBadGateway, "could not move it up")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleReadAlong is a synced pair, ready to read: the synced book for the
// reader, and every sentence's place on the audiobook's timeline.
func (s *Server) handleReadAlong(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ebook := itemRef{q.Get("ebookSource"), q.Get("ebookId")}
	audiobook := itemRef{q.Get("audiobookSource"), q.Get("audiobookId")}
	st, ok := s.readAlong(r.Context())
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "read-along is not set up on this server")
		return
	}
	if _, err := s.ebookFile(r.Context(), ebook); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	layout, _, err := s.audiobookFiles(r.Context(), audiobook)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	book, found, err := st.ByFolder(r.Context(), layout.Folder)
	if err != nil {
		writeError(w, http.StatusBadGateway, "read-along did not answer")
		return
	}
	if !found || book.Ebook == nil {
		writeError(w, http.StatusNotFound, "this book has not been synced")
		return
	}
	status := storyteller.StatusOf(book)
	if status.State != "ready" {
		writeJSON(w, http.StatusOK, map[string]any{"status": status})
		return
	}
	clips, err := st.Clips(r.Context(), book.UUID)
	if err != nil {
		s.log.Warn("read-along could not read the synced book", "err", err)
		writeError(w, http.StatusBadGateway, "the synced book could not be read")
		return
	}
	timeline, err := storyteller.Timeline(clips, layout)
	if err != nil {
		// The book still opens and reads; it just cannot follow.
		s.log.Warn("read-along timeline", "book", book.UUID, "err", err)
		timeline = nil
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   status,
		"item":     media.Item{ID: book.UUID, SourceID: st.ID(), Kind: media.KindEbook, Title: book.Title},
		"timeline": timeline,
		"problem":  errText(err),
	})
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// ebookFile is the EPUB behind an ebook, on SoundStorm's side of the library.
func (s *Server) ebookFile(ctx context.Context, ref itemRef) (string, error) {
	src, ok := s.reg.ByID(ctx, ref.SourceID)
	if !ok || src.Kind() != media.KindEbook || ref.ID == "" {
		return "", errors.New("no such ebook")
	}
	lister, ok := src.(source.FileLister)
	if !ok {
		return "", errors.New("this ebook cannot be synced")
	}
	files, err := lister.ItemFiles(ctx, ref.ID)
	if err != nil {
		return "", errors.New("no such ebook")
	}
	shelf := s.library.PathFor(media.KindEbook)
	for _, f := range files {
		if !strings.EqualFold(filepath.Ext(f), ".epub") {
			continue
		}
		p, err := inside(shelf, f)
		if err != nil {
			return "", err
		}
		return p, nil
	}
	return "", errors.New("only an EPUB can be read along")
}

// audiobookFiles is an audiobook's layout, and its files named relative to the
// audiobook shelf. Storyteller wants them all in one folder, so a book split
// into disc folders cannot be synced.
func (s *Server) audiobookFiles(ctx context.Context, ref itemRef) (source.AudioLayout, []string, error) {
	src, ok := s.reg.ByID(ctx, ref.SourceID)
	if !ok || src.Kind() != media.KindAudiobook || ref.ID == "" {
		return source.AudioLayout{}, nil, errors.New("no such audiobook")
	}
	layouter, ok := src.(source.AudioLayouter)
	if !ok {
		return source.AudioLayout{}, nil, errors.New("this audiobook cannot be synced")
	}
	layout, err := layouter.AudioLayout(ctx, ref.ID)
	if err != nil || len(layout.Files) == 0 {
		return source.AudioLayout{}, nil, errors.New("no such audiobook")
	}
	shelf := s.library.PathFor(media.KindAudiobook)
	var audio []string
	for _, f := range layout.Files {
		if strings.ContainsAny(f.Name, `/\`) {
			return source.AudioLayout{}, nil, errors.New("this audiobook is split into folders, which read-along cannot use")
		}
		rel := filepath.ToSlash(filepath.Join(layout.Folder, f.Name))
		p, err := inside(shelf, rel)
		if err != nil {
			return source.AudioLayout{}, nil, err
		}
		if _, err := os.Stat(p); err != nil {
			return source.AudioLayout{}, nil, errors.New("this audiobook's files are not all in its folder, which read-along needs")
		}
		audio = append(audio, rel)
	}
	return layout, audio, nil
}

// inside joins a shelf-relative path onto the shelf and refuses anything that
// would land outside it.
func inside(shelf, rel string) (string, error) {
	p := filepath.Join(shelf, filepath.FromSlash(rel))
	if shelf == "" || !strings.HasPrefix(p, filepath.Clean(shelf)+string(filepath.Separator)) {
		return "", errors.New("that file is not on its shelf")
	}
	return p, nil
}
