package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/auth"
	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/source"
	"github.com/GabrielHollberg/soundstorm/internal/voices"
)

// Making an audiobook from an ebook: Make an audiobook in an ebook's menu
// reads it aloud on the box with an open-source voice (Kokoro, a backend of
// its own), a chapter a file, and puts it on the Audiobooks shelf marked as
// an AI voice. Only the owner may, or someone the owner allows in People.
// See "Making an audiobook from an ebook" in CLAUDE.md.

var voiceID = regexp.MustCompile(`^[a-z]{2}_[a-z0-9_]{1,30}$`)

// voiceHealth remembers for a little while whether the voice backend answers.
var voiceHealth struct {
	sync.Mutex
	at time.Time
	ok bool
}

func (s *Server) voicesUp(ctx context.Context) bool {
	if s.voices == nil {
		return false
	}
	voiceHealth.Lock()
	defer voiceHealth.Unlock()
	if time.Since(voiceHealth.at) < 30*time.Second {
		return voiceHealth.ok
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	voiceHealth.ok = s.voices.Voice().Health(ctx) == nil
	voiceHealth.at = time.Now()
	return voiceHealth.ok
}

// canMakeBooks is whether the person asking may start one.
func (s *Server) canMakeBooks(r *http.Request) (string, bool) {
	u, ok := auth.FromContext(r.Context())
	if !ok {
		return "", false
	}
	return u.ID, u.IsOwner() || u.CanMakeBooks
}

// GET /api/voices: whether audiobooks can be made here, and the voices.
func (s *Server) handleVoices(w http.ResponseWriter, r *http.Request) {
	_, allowed := s.canMakeBooks(r)
	out := map[string]any{"available": false, "allowed": allowed, "voices": []any{}, "ebooks": s.earsUp(r.Context())}
	if !s.voicesUp(r.Context()) {
		writeJSON(w, http.StatusOK, out)
		return
	}
	ids, err := s.voices.Voice().Voices(r.Context())
	if err != nil {
		writeJSON(w, http.StatusOK, out)
		return
	}
	list := []map[string]string{}
	for _, id := range ids {
		if !voiceID.MatchString(id) {
			continue
		}
		accent := voices.VoiceAccent(id)
		// The English voices; the rest read English badly.
		if !strings.HasPrefix(accent, "American") && !strings.HasPrefix(accent, "British") {
			continue
		}
		list = append(list, map[string]string{"id": id, "name": voices.VoiceName(id), "accent": accent})
	}
	out["available"] = true
	out["voices"] = list
	writeJSON(w, http.StatusOK, out)
}

// GET /api/voices/sample?voice=af_heart: a few seconds of a voice, made once.
func (s *Server) handleVoiceSample(w http.ResponseWriter, r *http.Request) {
	voice := r.URL.Query().Get("voice")
	if !voiceID.MatchString(voice) || s.voices == nil {
		http.Error(w, "no such voice", http.StatusNotFound)
		return
	}
	if _, allowed := s.canMakeBooks(r); !allowed {
		http.Error(w, "not allowed", http.StatusForbidden)
		return
	}
	dir := filepath.Join(s.voicesDir(), "samples")
	path := filepath.Join(dir, voice+".mp3")
	if _, err := os.Stat(path); err != nil {
		_ = os.MkdirAll(dir, 0o700)
		f, err := os.Create(path + ".part")
		if err != nil {
			http.Error(w, "could not make a sample", http.StatusInternalServerError)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
		err = s.voices.Voice().Speak(ctx, "Hello. This is how I would read your book to you, chapter by chapter.", voice, f)
		cancel()
		f.Close()
		if err != nil {
			os.Remove(path + ".part")
			http.Error(w, "the voice did not answer", http.StatusBadGateway)
			return
		}
		_ = os.Rename(path+".part", path)
	}
	w.Header().Set("Content-Type", "audio/mpeg")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeFile(w, r, path)
}

func (s *Server) voicesDir() string { return s.voicesDirPath }

// POST /api/voices/make {source, id, voice, anyway}: queue an ebook.
func (s *Server) handleMakeAudiobook(w http.ResponseWriter, r *http.Request) {
	userID, allowed := s.canMakeBooks(r)
	if !allowed {
		writeError(w, http.StatusForbidden, "only the owner, or someone the owner allows, can make audiobooks")
		return
	}
	if !s.voicesUp(r.Context()) {
		writeError(w, http.StatusServiceUnavailable, "the voice is not running on this server")
		return
	}
	var body struct {
		Source string `json:"source"`
		ID     string `json:"id"`
		Voice  string `json:"voice"`
		Anyway bool   `json:"anyway"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil || !voiceID.MatchString(body.Voice) {
		writeError(w, http.StatusBadRequest, "expected {source, id, voice}")
		return
	}
	ref := itemRef{body.Source, body.ID}
	path, err := s.ebookFile(r.Context(), ref)
	if err != nil {
		writeError(w, http.StatusBadRequest, "only an EPUB ebook can be read aloud")
		return
	}
	src, _ := s.reg.ByID(r.Context(), body.Source)
	item, _ := itemByID(r.Context(), src, body.ID)
	// Already an audiobook of it? Said, and asked, unless told to go ahead.
	if !body.Anyway {
		for _, p := range s.listPairs(r.Context()) {
			if p.Ebook.SourceID == ref.SourceID && p.Ebook.ID == ref.ID {
				writeJSON(w, http.StatusConflict, map[string]any{"existing": p.Audiobook})
				return
			}
		}
	}
	author := ""
	if len(item.Creators) > 0 {
		author = item.Creators[0]
	}
	rel, _ := filepath.Rel(s.library.PathFor(media.KindEbook), path)
	job, err := s.voices.Add(userID, body.Source, body.ID, path, filepath.ToSlash(rel), item.Title, author, body.Voice)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.jobJSON(job))
}

func itemByID(ctx context.Context, src source.Source, id string) (media.Item, bool) {
	if g, ok := src.(source.ItemGetter); ok && src != nil {
		return g.ItemByID(ctx, id)
	}
	return media.Item{}, false
}

// jobJSON is a job as the page shows it, with the time left.
func (s *Server) jobJSON(j voices.Job) map[string]any {
	out := map[string]any{
		"id": j.ID, "title": j.Title, "author": j.Author, "voice": voices.VoiceName(j.Voice),
		"state": j.State, "chapters": j.Chapters, "chaptersDone": j.ChapDone,
		"problem": j.Problem, "dest": j.Dest, "source": j.Source, "item": j.Item,
		"kind": "audiobook",
	}
	if j.Kind == "ebook" {
		out["kind"], out["voice"] = "ebook", ""
		if j.Seconds > 0 {
			out["fraction"] = j.SecondsDone / j.Seconds
			if j.State == "working" && j.Pace > 0 {
				out["secondsLeft"] = int((j.Seconds - j.SecondsDone) / j.Pace)
			}
		}
		return out
	}
	if j.Words > 0 {
		out["fraction"] = float64(j.WordsDone) / float64(j.Words)
		if j.State == "working" && j.Pace > 0 {
			out["secondsLeft"] = int(float64(j.Words-j.WordsDone) / 2.5 / j.Pace)
		}
	}
	return out
}

// GET /api/voices/jobs: the queue - everyone's for the owner, one's own else.
func (s *Server) handleVoiceJobs(w http.ResponseWriter, r *http.Request) {
	u, ok := auth.FromContext(r.Context())
	if !ok || s.voices == nil {
		writeJSON(w, http.StatusOK, map[string]any{"jobs": []any{}})
		return
	}
	list := []map[string]any{}
	for _, j := range s.voices.Jobs() {
		if u.IsOwner() || j.UserID == u.ID {
			list = append(list, s.jobJSON(j))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": list})
}

// DELETE /api/voices/jobs/{id}: stop one - the owner any, a person their own.
func (s *Server) handleCancelVoiceJob(w http.ResponseWriter, r *http.Request) {
	u, ok := auth.FromContext(r.Context())
	if !ok || s.voices == nil {
		writeError(w, http.StatusNotFound, "no such job")
		return
	}
	id := r.PathValue("id")
	for _, j := range s.voices.Jobs() {
		if j.ID == id && (u.IsOwner() || j.UserID == u.ID) {
			if s.voices.Cancel(id) {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
	}
	writeError(w, http.StatusNotFound, "no such job")
}

// PUT /api/users/{id}/make-books {"allowed": true}: the owner gives or takes it.
func (s *Server) handleSetCanMakeBooks(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Allowed *bool `json:"allowed"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&body); err != nil || body.Allowed == nil {
		writeError(w, http.StatusBadRequest, `expected {"allowed": true or false}`)
		return
	}
	target, found := s.store.User(r.PathValue("id"))
	if !found {
		writeError(w, http.StatusNotFound, "no such account")
		return
	}
	if target.IsOwner() {
		writeError(w, http.StatusBadRequest, "the owner always can")
		return
	}
	if err := s.store.SetCanMakeBooks(target.ID, *body.Allowed); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"canMakeBooks": *body.Allowed})
}

// placeMadeBook puts a finished book's files on the audiobook shelf, as
// <Author>/<Title> (AI voice)/, beside any other edition, never over one.
func (s *Server) placeMadeBook(j *voices.Job, staged string) (string, error) {
	shelf := s.library.PathFor(media.KindAudiobook)
	author := folderName(j.Author, "Unknown Author")
	base := folderName(j.Title, "Untitled") + " (AI voice)"
	rel := filepath.ToSlash(filepath.Join(author, base))
	for n := 2; ; n++ {
		p, err := inside(shelf, rel)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(p); os.IsNotExist(err) {
			break
		}
		if n > 50 {
			return "", fmt.Errorf("too many editions of this book already")
		}
		rel = filepath.ToSlash(filepath.Join(author, fmt.Sprintf("%s %d", base, n)))
	}
	dest, _ := inside(shelf, rel)
	// Made in a hidden folder beside it, then renamed into place: the
	// audiobook server never sees half a book (as uploads do).
	tmp := filepath.Join(filepath.Dir(dest), "."+filepath.Base(dest)+".making")
	if err := os.MkdirAll(tmp, 0o777); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(staged)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".part") {
			continue
		}
		if err := copyFile(filepath.Join(staged, e.Name()), filepath.Join(tmp, e.Name())); err != nil {
			_ = os.RemoveAll(tmp)
			return "", err
		}
	}
	_ = os.Chmod(tmp, 0o777)
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.RemoveAll(tmp)
		return "", err
	}
	s.scheduleRescan(media.KindAudiobook)
	return rel, nil
}

// folderName makes a title or name one safe folder name.
func folderName(s, fallback string) string {
	s = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return '-'
		}
		if r < 32 {
			return -1
		}
		return r
	}, s)
	s = strings.Trim(strings.TrimSpace(s), ". ")
	if len(s) > 100 {
		s = strings.TrimSpace(s[:100])
	}
	if s == "" || strings.Trim(s, ".") == "" {
		return fallback
	}
	return s
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o666)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// RunVoices works through the queue of books being read aloud.
func (s *Server) RunVoices(ctx context.Context) {
	if s.voices == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("the audiobook queue stopped", "panic", r)
		}
	}()
	s.voices.Run(ctx)
}

// Making an ebook from an audiobook: Make an ebook in an audiobook's menu
// writes the recording down on the box (Whisper, a backend of its own) and
// puts an EPUB on the Ebooks shelf, titled "<Title> (from the audiobook)",
// which carries every sentence's moment in the recording - so it reads along
// with it from the start, with no sync step.

var earsHealth struct {
	sync.Mutex
	at time.Time
	ok bool
}

func (s *Server) earsUp(ctx context.Context) bool {
	if s.voices == nil || s.voices.Ears() == nil {
		return false
	}
	earsHealth.Lock()
	defer earsHealth.Unlock()
	if time.Since(earsHealth.at) < 30*time.Second {
		return earsHealth.ok
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	earsHealth.ok = s.voices.Ears().Health(ctx) == nil
	earsHealth.at = time.Now()
	return earsHealth.ok
}

// POST /api/voices/make-ebook {source, id, anyway}: queue an audiobook.
func (s *Server) handleMakeEbook(w http.ResponseWriter, r *http.Request) {
	userID, allowed := s.canMakeBooks(r)
	if !allowed {
		writeError(w, http.StatusForbidden, "only the owner, or someone the owner allows, can make ebooks")
		return
	}
	if !s.earsUp(r.Context()) {
		writeError(w, http.StatusServiceUnavailable, "writing books down is not running on this server")
		return
	}
	var body struct {
		Source string `json:"source"`
		ID     string `json:"id"`
		Anyway bool   `json:"anyway"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "expected {source, id}")
		return
	}
	ref := itemRef{body.Source, body.ID}
	layout, rels, err := s.audiobookFiles(r.Context(), ref)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	src, _ := s.reg.ByID(r.Context(), body.Source)
	item, _ := itemByID(r.Context(), src, body.ID)
	// Already an ebook of it? Said, and asked, unless told to go ahead.
	if !body.Anyway {
		for _, p := range s.listPairs(r.Context()) {
			if p.Audiobook.SourceID == ref.SourceID && p.Audiobook.ID == ref.ID {
				writeJSON(w, http.StatusConflict, map[string]any{"existing": p.Ebook})
				return
			}
		}
	}
	shelf := s.library.PathFor(media.KindAudiobook)
	audio := make([]voices.AudioFile, 0, len(rels))
	var chapters []voices.BookChapter
	for i, rel := range rels {
		p, err := inside(shelf, rel)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		f := layout.Files[i]
		audio = append(audio, voices.AudioFile{Path: p, Start: f.StartSeconds, Len: f.DurationSeconds})
	}
	if cl, ok := src.(source.ChapterLister); ok {
		if list, err := cl.Chapters(r.Context(), body.ID); err == nil {
			for _, c := range list {
				chapters = append(chapters, voices.BookChapter{Title: c.Title, Start: c.StartSeconds})
			}
		}
	}
	if len(chapters) == 0 && len(layout.Files) > 1 {
		// No chapter list: a book of a file per chapter is named by its files.
		for _, f := range layout.Files {
			name := strings.TrimSuffix(f.Name, filepath.Ext(f.Name))
			chapters = append(chapters, voices.BookChapter{Title: name, Start: f.StartSeconds})
		}
	}
	cover := ""
	if dir, err := inside(shelf, layout.Folder+"/x"); err == nil {
		dir = filepath.Dir(dir)
		for _, n := range []string{"cover.jpg", "cover.jpeg", "cover.png", "folder.jpg", "folder.png"} {
			if st, err := os.Stat(filepath.Join(dir, n)); err == nil && st.Mode().IsRegular() {
				cover = filepath.Join(dir, n)
				break
			}
		}
	}
	author := ""
	if len(item.Creators) > 0 {
		author = item.Creators[0]
	} else if a, _, ok := strings.Cut(layout.Folder, "/"); ok {
		// No author tag: the shelf is Author/Title, so the folder says.
		author = a
	}
	title := item.Title
	if title == "" {
		title = filepath.Base(layout.Folder)
	}
	job, err := s.voices.AddEbook(userID, body.Source, body.ID, title, author, layout.Folder, cover, audio, chapters)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.jobJSON(job))
}

// placeMadeEbook puts a made EPUB on the ebook shelf, as
// <Author>/<Title> (from the audiobook)/<Title> (from the audiobook).epub.
func (s *Server) placeMadeEbook(j *voices.Job, staged string) (string, error) {
	shelf := s.library.PathFor(media.KindEbook)
	author := folderName(j.Author, "Unknown Author")
	base := folderName(j.Title, "Untitled") + " (from the audiobook)"
	rel := filepath.ToSlash(filepath.Join(author, base))
	for n := 2; ; n++ {
		p, err := inside(shelf, rel)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(p); os.IsNotExist(err) {
			break
		}
		if n > 50 {
			return "", fmt.Errorf("too many editions of this book already")
		}
		rel = filepath.ToSlash(filepath.Join(author, fmt.Sprintf("%s %d", base, n)))
	}
	dest, _ := inside(shelf, rel)
	tmp := filepath.Join(filepath.Dir(dest), "."+filepath.Base(dest)+".making")
	if err := os.MkdirAll(tmp, 0o777); err != nil {
		return "", err
	}
	if err := copyFile(filepath.Join(staged, "book.epub"), filepath.Join(tmp, base+".epub")); err != nil {
		_ = os.RemoveAll(tmp)
		return "", err
	}
	_ = os.Chmod(tmp, 0o777)
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.RemoveAll(tmp)
		return "", err
	}
	s.scheduleRescan(media.KindEbook)
	return rel, nil
}

// madeTimeline is a made ebook's own timeline, when the ebook is one and was
// made from this audiobook folder.
func (s *Server) madeTimeline(ctx context.Context, ebook itemRef, folder string) ([]voices.Moment, bool) {
	path, err := s.ebookFile(ctx, ebook)
	if err != nil {
		return nil, false
	}
	t, ok := voices.ReadMadeTimeline(path)
	if !ok || (folder != "" && t.AudiobookFolder != folder) {
		return nil, false
	}
	return t.Timeline, true
}

// madeTitle is whether a title is a made ebook's: a cheap look before
// opening the book to read its timeline.
func madeTitle(title string) bool { return strings.HasSuffix(title, "(from the audiobook)") }

func (s *Server) madeTimelineOK(ctx context.Context, ebook itemRef, folder string) bool {
	_, ok := s.madeTimeline(ctx, ebook, folder)
	return ok
}
