package httpapi

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/mp4hls"
	"github.com/GabrielHollberg/soundstorm/internal/source"
)

// A long audiobook kept as one MP4 (an .m4b) carries an index a player must
// have before the first sound, and an Audible book keeps it at the end - 33.5MB
// for the 52-hour Count of Monte Cristo, which took 33 seconds to start away
// from home (the owner's report, measured). Such a file is offered as HLS made
// from the file itself (internal/mp4hls): a playlist and ten-second fragments
// of its own bytes, so the player fetches a little and plays. A file with a
// small index - most books, and every MP3 - plays as it is.
//
// The fragments are SoundStorm's own files on the audiobook shelf, found
// through the backend's description of the book (AudioLayout) and checked
// against the shelf like every other path; access is the source's, as for
// /api/stream.

type bookHLS struct {
	mu sync.Mutex
	// Opened books, newest last; a few at once (a long one's index is tens of
	// megabytes in memory).
	open []*openBook
	// Whether a file is worth it, by path and its size and time.
	worth map[string]worthEntry
	// Where a book's files are, by source/item, so a fragment does not ask
	// the backend each time.
	files map[string]filesEntry
}

type openBook struct {
	key      string
	book     *mp4hls.Book
	playlist []byte // gzipped
}

type worthEntry struct {
	size  int64
	mtime time.Time
	yes   bool
}

type filesEntry struct {
	paths []string
	at    time.Time
}

const bookHLSOpen = 3

// bookHLSMinIndex is the index size worth playing in pieces (a test lowers it).
var bookHLSMinIndex int64 = mp4hls.MinIndex

// bookPaths is a book's audio files on SoundStorm's side of the shelf, in
// playing order, or nil when they are not all there as plain files.
func (s *Server) bookPaths(ctx context.Context, src source.Source, itemID string) []string {
	key := src.ID() + "/" + itemID
	s.bookHLS.mu.Lock()
	if e, ok := s.bookHLS.files[key]; ok && time.Since(e.at) < 10*time.Minute {
		s.bookHLS.mu.Unlock()
		return e.paths
	}
	s.bookHLS.mu.Unlock()
	layouter, ok := src.(source.AudioLayouter)
	if !ok {
		return nil
	}
	layout, err := layouter.AudioLayout(ctx, itemID)
	if err != nil || len(layout.Files) == 0 {
		return nil
	}
	shelf := s.library.PathFor(media.KindAudiobook)
	paths := make([]string, 0, len(layout.Files))
	for _, f := range layout.Files {
		p, err := inside(shelf, filepath.ToSlash(filepath.Join(layout.Folder, f.Name)))
		if err != nil {
			return nil
		}
		paths = append(paths, p)
	}
	s.bookHLS.mu.Lock()
	if s.bookHLS.files == nil {
		s.bookHLS.files = map[string]filesEntry{}
	}
	if len(s.bookHLS.files) > 500 {
		clear(s.bookHLS.files)
	}
	s.bookHLS.files[key] = filesEntry{paths, time.Now()}
	s.bookHLS.mu.Unlock()
	return paths
}

// worthHLS is whether a file is an MP4 whose index is big enough to keep a
// player waiting.
func (s *Server) worthHLS(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".m4b", ".m4a", ".mp4":
	default:
		return false
	}
	st, err := os.Stat(path)
	if err != nil {
		return false
	}
	s.bookHLS.mu.Lock()
	if e, ok := s.bookHLS.worth[path]; ok && e.size == st.Size() && e.mtime.Equal(st.ModTime()) {
		s.bookHLS.mu.Unlock()
		return e.yes
	}
	s.bookHLS.mu.Unlock()
	n, err := mp4hls.Layout(path)
	yes := err == nil && n >= bookHLSMinIndex
	s.bookHLS.mu.Lock()
	if s.bookHLS.worth == nil {
		s.bookHLS.worth = map[string]worthEntry{}
	}
	if len(s.bookHLS.worth) > 2000 {
		clear(s.bookHLS.worth)
	}
	s.bookHLS.worth[path] = worthEntry{st.Size(), st.ModTime(), yes}
	s.bookHLS.mu.Unlock()
	return yes
}

// bookHLSURL is the playlist address for a book's track.
func bookHLSURL(sourceID, itemID string, track int) string {
	return "/api/bookhls/" + url.PathEscape(sourceID) + "/" + url.PathEscape(itemID) + "/" + strconv.Itoa(track) + "/index.m3u8"
}

// openHLS opens (or finds open) a file's fragments.
func (s *Server) openHLS(path string) (*openBook, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	key := path + "|" + strconv.FormatInt(st.Size(), 10) + "|" + st.ModTime().String()
	s.bookHLS.mu.Lock()
	for i, ob := range s.bookHLS.open {
		if ob.key == key {
			s.bookHLS.open = append(append(s.bookHLS.open[:i:i], s.bookHLS.open[i+1:]...), ob)
			s.bookHLS.mu.Unlock()
			return ob, nil
		}
	}
	s.bookHLS.mu.Unlock()
	b, err := mp4hls.Open(path)
	if err != nil {
		return nil, err
	}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write(b.Playlist())
	_ = zw.Close()
	ob := &openBook{key: key, book: b, playlist: gz.Bytes()}
	s.bookHLS.mu.Lock()
	s.bookHLS.open = append(s.bookHLS.open, ob)
	if len(s.bookHLS.open) > bookHLSOpen {
		s.bookHLS.open = s.bookHLS.open[len(s.bookHLS.open)-bookHLSOpen:]
	}
	s.bookHLS.mu.Unlock()
	return ob, nil
}

// handleBookHLS serves a book track's playlist, init segment or a fragment:
// GET /api/bookhls/{source}/{id}/{track}/{file}.
func (s *Server) handleBookHLS(w http.ResponseWriter, r *http.Request) {
	src, ok := s.reg.ByID(r.Context(), r.PathValue("source"))
	if !ok || src.Kind() != media.KindAudiobook {
		http.Error(w, "unknown source", http.StatusNotFound)
		return
	}
	track, err := strconv.Atoi(r.PathValue("track"))
	paths := s.bookPaths(r.Context(), src, r.PathValue("id"))
	if err != nil || track < 0 || track >= len(paths) || !s.worthHLS(paths[track]) {
		http.Error(w, "no such track", http.StatusNotFound)
		return
	}
	ob, err := s.openHLS(paths[track])
	if err != nil {
		s.log.Warn("book fragments", "err", err)
		http.Error(w, "this file cannot be played in pieces", http.StatusInternalServerError)
		return
	}
	// Audio is never kept by the browser (see audioNoStore): what is playing
	// lives in the player.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	file := r.PathValue("file")
	switch {
	case file == "index.m3u8":
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		// The playlist of a 52-hour book is 0.5MB of text; gzipped, a tenth.
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			w.Header().Set("Content-Encoding", "gzip")
			_, _ = w.Write(ob.playlist)
			return
		}
		zr, err := gzip.NewReader(bytes.NewReader(ob.playlist))
		if err == nil {
			_, _ = io.Copy(w, zr)
		}
	case file == "init.mp4":
		w.Header().Set("Content-Type", "audio/mp4")
		_, _ = w.Write(ob.book.Init())
	case strings.HasPrefix(file, "s") && strings.HasSuffix(file, ".m4s"):
		i, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(file, "s"), ".m4s"))
		if err != nil || i < 0 || i >= ob.book.Segments() {
			http.Error(w, "no such fragment", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "audio/mp4")
		var buf bytes.Buffer
		if err := ob.book.WriteSegment(&buf, i); err != nil {
			s.log.Warn("book fragment", "err", err)
			http.Error(w, "could not read the fragment", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
		_, _ = buf.WriteTo(w)
	default:
		http.Error(w, "no such file", http.StatusNotFound)
	}
}

// bookHLSAnswer changes a book's playback answer to fragments where its file
// is worth it: the single file's url, or each such track's.
func (s *Server) bookHLSAnswer(ctx context.Context, src source.Source, sourceID, itemID string, answer map[string]any) {
	if src.Kind() != media.KindAudiobook {
		return
	}
	paths := s.bookPaths(ctx, src, itemID)
	if len(paths) == 0 {
		return
	}
	tracks, _ := answer["tracks"].([]map[string]any)
	if len(tracks) == 0 {
		if len(paths) == 1 && s.worthHLS(paths[0]) {
			answer["mode"] = source.PlaybackModeHLS
			answer["url"] = bookHLSURL(sourceID, itemID, 0)
		}
		return
	}
	if len(tracks) != len(paths) {
		return
	}
	for i, t := range tracks {
		if s.worthHLS(paths[i]) {
			// The file's own address stays, for a download.
			t["file"] = t["url"]
			t["url"] = bookHLSURL(sourceID, itemID, i)
		}
	}
}
