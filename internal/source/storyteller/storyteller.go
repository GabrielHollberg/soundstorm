// Package storyteller drives Storyteller, which lines an audiobook up with its
// ebook sentence by sentence - the expensive part of read-along, done by
// transcribing the recording and matching it to the text. It owns exactly
// that; SoundStorm still plays the audiobook through its own player, and only
// borrows Storyteller's timings to turn the page.
//
// Checked against Storyteller web-v2.14.21. Its API moves between versions,
// which is why the image is pinned by digest in the compose file.
package storyteller

import (
	"archive/zip"
	"context"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/epub"
	"github.com/GabrielHollberg/soundstorm/internal/httpx"
	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/source"
)

// Config says where Storyteller is, and how the folders it shares with
// SoundStorm look from each side.
type Config struct {
	ID      string
	BaseURL string
	Token   string
	Timeout time.Duration

	// DataDir is Storyteller's /data as SoundStorm sees it, mounted read-only:
	// where the synced books are written.
	DataDir string
	// AudiobooksRemote is the audiobook shelf as Storyteller sees it, mounted
	// read-only. The recordings are used where they are, never copied. The
	// ebook is uploaded - a copy in Storyteller's own storage, which it may
	// upgrade to EPUB 3 without the book on the shelf ever being touched.
	AudiobooksRemote string
}

// Source is Storyteller as SoundStorm uses it. It is registered as an ebook
// source so the registry's access check covers it - an account that may not
// read ebooks cannot open a synced one either - but it finds nothing in a
// search: the books it holds are the ebook shelf's, synced.
type Source struct {
	cfg  Config
	http *httpx.Client

	mu     sync.Mutex
	listed []Book
	listAt time.Time
}

// New builds the adapter.
func New(cfg Config) (*Source, error) {
	if cfg.ID == "" || cfg.Token == "" || cfg.DataDir == "" || cfg.AudiobooksRemote == "" {
		return nil, fmt.Errorf("storyteller: incomplete configuration")
	}
	c, err := httpx.New(cfg.BaseURL, cfg.Timeout)
	if err != nil {
		return nil, err
	}
	c.SetHeader("Authorization", "Bearer "+cfg.Token)
	return &Source{cfg: cfg, http: c}, nil
}

func (s *Source) ID() string                                                { return s.cfg.ID }
func (s *Source) Kind() media.Kind                                          { return media.KindEbook }
func (s *Source) Search(context.Context, media.Query) ([]media.Item, error) { return nil, nil }

// Health asks for the settings, which needs the token to be good.
func (s *Source) Health(ctx context.Context) error {
	resp, err := s.http.Do(ctx, httpx.Request{Path: "/api/v2/settings"})
	if err != nil {
		return err
	}
	return resp.Err()
}

// Book is Storyteller's record of one book, as much of it as SoundStorm reads.
type Book struct {
	UUID      string `json:"uuid"`
	Title     string `json:"title"`
	Ebook     *file  `json:"ebook"`
	Audiobook *file  `json:"audiobook"`
	Readaloud *struct {
		Status        string  `json:"status"`
		CurrentStage  string  `json:"currentStage"`
		StageProgress float64 `json:"stageProgress"`
		QueuePosition *int    `json:"queuePosition"`
		Filepath      string  `json:"filepath"`
		Error         *string `json:"error"`
	} `json:"readaloud"`
}

type file struct {
	Filepath string `json:"filepath"`
}

// Status is how far a sync has got, for the app.
type Status struct {
	UUID     string  `json:"uuid"`
	State    string  `json:"state"` // queued, working, ready, failed, stopped
	Stage    string  `json:"stage,omitempty"`
	Progress float64 `json:"progress"` // 0 to 1, over the whole sync
	// Place is where a waiting sync is in the queue: 1 is next.
	Place int `json:"place,omitempty"`
}

// StatusOf turns Storyteller's record into the app's words.
func StatusOf(b Book) Status {
	st := Status{UUID: b.UUID, State: "queued"}
	if b.Readaloud == nil {
		return st
	}
	stages := map[string]float64{"SPLIT_TRACKS": 0, "TRANSCRIBE_CHAPTERS": 1, "SYNC_CHAPTERS": 2}
	done := stages[b.Readaloud.CurrentStage]
	if b.Readaloud.Status == "QUEUED" && b.Readaloud.QueuePosition != nil {
		st.Place = *b.Readaloud.QueuePosition
	}
	switch b.Readaloud.Status {
	case "ALIGNED":
		st.State, st.Progress = "ready", 1
	case "ERROR":
		st.State = "failed"
	case "STOPPED":
		st.State = "stopped"
	case "PROCESSING":
		st.State = "working"
		st.Stage = map[string]string{
			"SPLIT_TRACKS": "Preparing the recording", "TRANSCRIBE_CHAPTERS": "Listening to the recording",
			"SYNC_CHAPTERS": "Matching it to the book",
		}[b.Readaloud.CurrentStage]
		// Transcribing is nearly all of the time, so it gets most of the bar.
		weights := [3]float64{0.05, 0.85, 0.10}
		var p float64
		for i := 0; i < int(done); i++ {
			p += weights[i]
		}
		p += weights[int(done)] * b.Readaloud.StageProgress
		st.Progress = p
	}
	return st
}

// Books lists what Storyteller holds, cached for a few seconds: the app asks
// once a second or two while a sync is running.
func (s *Source) Books(ctx context.Context) ([]Book, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listed != nil && time.Since(s.listAt) < 3*time.Second {
		return s.listed, nil
	}
	var books []Book
	if err := s.http.JSON(ctx, "/api/v2/books", nil, &books); err != nil {
		return nil, err
	}
	s.listed, s.listAt = books, time.Now()
	return books, nil
}

func (s *Source) forget() {
	s.mu.Lock()
	s.listed = nil
	s.mu.Unlock()
}

// ByFolder finds the book synced from an audiobook, by its folder on the
// shelf. Nothing about read-along is stored in SoundStorm: Storyteller's own
// record of where the recording is, is the record.
func (s *Source) ByFolder(ctx context.Context, folder string) (Book, bool, error) {
	books, err := s.Books(ctx)
	if err != nil {
		return Book{}, false, err
	}
	want := path.Join(s.cfg.AudiobooksRemote, folder)
	for _, b := range books {
		if b.Audiobook != nil && path.Clean(b.Audiobook.Filepath) == want {
			return b, true, nil
		}
	}
	return Book{}, false, nil
}

// Sync hands a pair to Storyteller and starts it working. The recording is
// added where it is on the shelf (audio names relative to the audiobook
// shelf, all in one folder); the ebook is then uploaded into the same book.
//
// Two steps rather than one call naming both files, which is what the API
// offers and does not work: in web-v2.14.21, files from two different folders
// each try to create the book, and the second fails on a duplicate id. Found
// on the first real run, which is why this was checked rather than assumed.
//
// Asking again for a pair already there starts it again only if it had
// stopped or failed, or finishes a sync that lost its ebook half.
func (s *Source) Sync(ctx context.Context, folder, ebookPath string, audio []string) (Status, error) {
	existing, ok, err := s.ByFolder(ctx, folder)
	if err != nil {
		return Status{}, err
	}
	var uuid string
	if ok {
		uuid = existing.UUID
		if existing.Ebook != nil {
			st := StatusOf(existing)
			if st.State == "failed" || st.State == "stopped" {
				if err := s.process(ctx, uuid, "full"); err != nil {
					return Status{}, err
				}
				st.State, st.Progress = "queued", 0
			}
			return st, nil
		}
	} else {
		paths := make([]string, 0, len(audio))
		for _, a := range audio {
			paths = append(paths, path.Join(s.cfg.AudiobooksRemote, a))
		}
		var created Book
		resp, err := s.http.Do(ctx, httpx.Request{
			Method: http.MethodPost,
			Path:   "/api/v2/books",
			Body:   map[string]any{"paths": paths, "importMode": "reference"},
		})
		if err != nil {
			return Status{}, err
		}
		if err := resp.Err(); err != nil {
			return Status{}, fmt.Errorf("storyteller could not take the recording: %w", err)
		}
		if err := resp.JSON(&created); err != nil || created.UUID == "" {
			return Status{}, fmt.Errorf("storyteller took the recording but returned no id")
		}
		uuid = created.UUID
	}
	if err := s.upload(ctx, uuid, ebookPath); err != nil {
		return Status{}, err
	}
	s.forget()
	if err := s.process(ctx, uuid, ""); err != nil {
		return Status{}, err
	}
	return Status{UUID: uuid, State: "queued"}, nil
}

// upload sends the ebook into an existing book, over Storyteller's tus
// endpoint: one request to open the upload, one carrying the whole file.
func (s *Source) upload(ctx context.Context, uuid, ebookPath string) error {
	f, err := os.Open(ebookPath)
	if err != nil {
		return fmt.Errorf("storyteller: could not read the ebook")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("storyteller: could not read the ebook")
	}
	b64 := func(v string) string { return base64.StdEncoding.EncodeToString([]byte(v)) }
	meta := strings.Join([]string{
		"bookUuid " + b64(uuid), "filename " + b64("book.epub"),
		"filetype " + b64("application/epub+zip"), "totalFiles " + b64("1"),
	}, ",")
	hc := &http.Client{Timeout: 5 * time.Minute}
	base := s.http.BaseURL()
	create, err := http.NewRequestWithContext(ctx, http.MethodPost,
		base.ResolveReference(&url.URL{Path: "/api/v2/books/upload"}).String(), nil)
	if err != nil {
		return err
	}
	create.Header.Set("Authorization", "Bearer "+s.cfg.Token)
	create.Header.Set("Tus-Resumable", "1.0.0")
	create.Header.Set("Upload-Length", strconv.FormatInt(info.Size(), 10))
	create.Header.Set("Upload-Metadata", meta)
	resp, err := hc.Do(create)
	if err != nil {
		return httpx.Redact(err)
	}
	resp.Body.Close()
	loc, err := url.Parse(resp.Header.Get("Location"))
	if resp.StatusCode != http.StatusCreated || err != nil || loc.String() == "" {
		return fmt.Errorf("storyteller would not take the ebook (%d)", resp.StatusCode)
	}
	// The upload's address, which must be on Storyteller itself.
	target := base.ResolveReference(loc)
	if target.Host != base.Host || !strings.HasPrefix(target.Path, "/api/v2/books/upload/") {
		return fmt.Errorf("storyteller answered with an unexpected upload address")
	}
	patch, err := http.NewRequestWithContext(ctx, http.MethodPatch, target.String(), f)
	if err != nil {
		return err
	}
	patch.ContentLength = info.Size()
	patch.Header.Set("Authorization", "Bearer "+s.cfg.Token)
	patch.Header.Set("Tus-Resumable", "1.0.0")
	patch.Header.Set("Upload-Offset", "0")
	patch.Header.Set("Content-Type", "application/offset+octet-stream")
	resp, err = hc.Do(patch)
	if err != nil {
		return httpx.Redact(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("storyteller did not finish taking the ebook (%d)", resp.StatusCode)
	}
	return nil
}

// SyncNext moves a waiting sync to the front of the queue, after whatever is
// being synced now, which carries on. Storyteller has no way to reorder its
// queue, but it places a book at the back when it is queued and resumes from
// where it had got to, so the waiting books are taken out and queued again
// with this one first - nothing done so far is lost.
func (s *Source) SyncNext(ctx context.Context, uuid string) error {
	s.forget()
	books, err := s.Books(ctx)
	if err != nil {
		return err
	}
	type waiting struct {
		uuid  string
		place int
	}
	var queue []waiting
	found := false
	for _, b := range books {
		if b.Readaloud == nil {
			continue
		}
		if b.UUID == uuid {
			found = true
		}
		if b.Readaloud.Status == "QUEUED" && b.UUID != uuid {
			place := 1 << 30
			if b.Readaloud.QueuePosition != nil {
				place = *b.Readaloud.QueuePosition
			}
			queue = append(queue, waiting{b.UUID, place})
		}
	}
	if !found {
		return fmt.Errorf("storyteller: no sync %q", uuid)
	}
	sort.SliceStable(queue, func(i, j int) bool { return queue[i].place < queue[j].place })
	for _, w := range append([]waiting{{uuid: uuid}}, queue...) {
		if err := s.cancel(ctx, w.uuid); err != nil {
			return err
		}
	}
	if err := s.process(ctx, uuid, ""); err != nil {
		return err
	}
	for _, w := range queue {
		if err := s.process(ctx, w.uuid, ""); err != nil {
			return err
		}
	}
	return nil
}

func (s *Source) cancel(ctx context.Context, uuid string) error {
	resp, err := s.http.Do(ctx, httpx.Request{
		Method: http.MethodDelete,
		Path:   "/api/v2/books/" + url.PathEscape(uuid) + "/process",
	})
	if err != nil {
		return err
	}
	s.forget()
	return resp.Err()
}

func (s *Source) process(ctx context.Context, uuid, restart string) error {
	params := url.Values{}
	if restart != "" {
		params.Set("restart", restart)
	}
	resp, err := s.http.Do(ctx, httpx.Request{
		Method: http.MethodPost,
		Path:   "/api/v2/books/" + url.PathEscape(uuid) + "/process",
		Params: params,
	})
	if err != nil {
		return err
	}
	s.forget()
	return resp.Err()
}

// readaloudPath is the synced book on SoundStorm's side of the shared volume.
func (s *Source) readaloudPath(ctx context.Context, uuid string) (string, error) {
	books, err := s.Books(ctx)
	if err != nil {
		return "", err
	}
	for _, b := range books {
		if b.UUID != uuid || b.Readaloud == nil || b.Readaloud.Status != "ALIGNED" {
			continue
		}
		rel, err := source.RelativeTo("/data", b.Readaloud.Filepath)
		if err != nil {
			return "", fmt.Errorf("storyteller: the synced book is not where it was expected")
		}
		local := filepath.Join(s.cfg.DataDir, filepath.FromSlash(rel))
		if !strings.HasPrefix(local, filepath.Clean(s.cfg.DataDir)+string(filepath.Separator)) {
			return "", fmt.Errorf("storyteller: the synced book is not where it was expected")
		}
		return local, nil
	}
	return "", fmt.Errorf("storyteller: no synced book %q", uuid)
}

// OpenBook serves the synced book to the reader: the ebook with every
// sentence wrapped and named, which is what lets the page follow the voice.
func (s *Source) OpenBook(ctx context.Context, itemID string) (source.OpenBook, error) {
	p, err := s.readaloudPath(ctx, itemID)
	if err != nil {
		return nil, err
	}
	opened, err := epub.Open(p)
	if err != nil {
		return nil, fmt.Errorf("storyteller: could not open the synced book")
	}
	return &openBook{Book: opened}, nil
}

type openBook struct{ *epub.Book }

func (o *openBook) Entries() []source.BookEntry {
	inner := o.Book.Entries()
	out := make([]source.BookEntry, len(inner))
	for i, e := range inner {
		out[i] = source.BookEntry{Name: e.Name, Size: e.Size}
	}
	return out
}

// Clip is one sentence: where it is in the book, and where it is in the
// recording - in Storyteller's own pieces of it, which are numbered by file
// (1 up, in name order) and by chapter within the file.
type Clip struct {
	Href  string  // path inside the book, "#", the sentence's id
	File  int     // which audio file, from 1
	Piece int     // which piece of that file, from 1: its chapters
	Begin float64 // seconds into the piece
	End   float64
}

var pieceName = regexp.MustCompile(`(\d+)-(\d+)\.[A-Za-z0-9]+$`)

// Clips reads the synced book's media overlays: every sentence, in reading
// order, with its time.
func (s *Source) Clips(ctx context.Context, uuid string) ([]Clip, error) {
	p, err := s.readaloudPath(ctx, uuid)
	if err != nil {
		return nil, err
	}
	zr, err := zip.OpenReader(p)
	if err != nil {
		return nil, fmt.Errorf("storyteller: could not open the synced book")
	}
	defer zr.Close()
	return readClips(&zr.Reader)
}

func readClips(zr *zip.Reader) ([]Clip, error) {
	entries := map[string]*zip.File{}
	for _, f := range zr.File {
		entries[f.Name] = f
	}
	read := func(name string) ([]byte, error) {
		f, ok := entries[name]
		if !ok {
			return nil, fmt.Errorf("storyteller: %s is missing from the synced book", name)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(io.LimitReader(rc, 8<<20))
	}

	raw, err := read("META-INF/container.xml")
	if err != nil {
		return nil, err
	}
	var container struct {
		Rootfiles []struct {
			FullPath string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if err := xml.Unmarshal(raw, &container); err != nil || len(container.Rootfiles) == 0 {
		return nil, fmt.Errorf("storyteller: the synced book has no package file")
	}
	opfPath := container.Rootfiles[0].FullPath
	raw, err = read(opfPath)
	if err != nil {
		return nil, err
	}
	var opf struct {
		Items []struct {
			ID           string `xml:"id,attr"`
			Href         string `xml:"href,attr"`
			MediaOverlay string `xml:"media-overlay,attr"`
		} `xml:"manifest>item"`
		Spine []struct {
			IDRef string `xml:"idref,attr"`
		} `xml:"spine>itemref"`
	}
	if err := xml.Unmarshal(raw, &opf); err != nil {
		return nil, fmt.Errorf("storyteller: the synced book's package file does not parse")
	}
	byID := map[string]string{}
	overlay := map[string]string{}
	for _, it := range opf.Items {
		byID[it.ID] = resolve(opfPath, it.Href)
		if it.MediaOverlay != "" {
			overlay[it.ID] = it.MediaOverlay
		}
	}

	var clips []Clip
	for _, ref := range opf.Spine {
		smilID, ok := overlay[ref.IDRef]
		if !ok {
			continue
		}
		smilPath, ok := byID[smilID]
		if !ok {
			continue
		}
		raw, err := read(smilPath)
		if err != nil {
			return nil, err
		}
		got, err := parseSMIL(smilPath, raw)
		if err != nil {
			return nil, err
		}
		clips = append(clips, got...)
	}
	return clips, nil
}

// parseSMIL reads every <par> in a media overlay, however deeply the <seq>s
// nest them.
func parseSMIL(smilPath string, raw []byte) ([]Clip, error) {
	dec := xml.NewDecoder(strings.NewReader(string(raw)))
	var clips []Clip
	var text, audio string
	var begin, end string
	inPar := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("storyteller: a media overlay does not parse")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			attr := func(name string) string {
				for _, a := range t.Attr {
					if a.Name.Local == name {
						return a.Value
					}
				}
				return ""
			}
			switch t.Name.Local {
			case "par":
				inPar, text, audio, begin, end = true, "", "", "", ""
			case "text":
				if inPar {
					text = attr("src")
				}
			case "audio":
				if inPar {
					audio, begin, end = attr("src"), attr("clipBegin"), attr("clipEnd")
				}
			}
		case xml.EndElement:
			if t.Name.Local != "par" || !inPar {
				continue
			}
			inPar = false
			m := pieceName.FindStringSubmatch(audio)
			if text == "" || m == nil {
				continue
			}
			fileN, _ := strconv.Atoi(m[1])
			pieceN, _ := strconv.Atoi(m[2])
			b, okB := clock(begin)
			e, okE := clock(end)
			if !okB || !okE {
				continue
			}
			target, frag, _ := strings.Cut(text, "#")
			clips = append(clips, Clip{
				Href: resolve(smilPath, target) + "#" + frag,
				File: fileN, Piece: pieceN, Begin: b, End: e,
			})
		}
	}
	return clips, nil
}

// resolve turns a reference inside a book into a path from the book's root.
func resolve(from, ref string) string {
	ref, _ = url.PathUnescape(ref)
	return path.Clean(path.Join(path.Dir(from), ref))
}

// clock reads a SMIL clock value: "2.37s", "1500ms", "0:01:02.5", "01:02.5".
func clock(v string) (float64, bool) {
	v = strings.TrimSpace(v)
	switch {
	case v == "":
		return 0, false
	case strings.HasSuffix(v, "ms"):
		n, err := strconv.ParseFloat(strings.TrimSuffix(v, "ms"), 64)
		return n / 1000, err == nil
	case strings.HasSuffix(v, "s") && !strings.Contains(v, ":"):
		n, err := strconv.ParseFloat(strings.TrimSuffix(v, "s"), 64)
		return n, err == nil
	case strings.HasSuffix(v, "min"):
		n, err := strconv.ParseFloat(strings.TrimSuffix(v, "min"), 64)
		return n * 60, err == nil
	case strings.HasSuffix(v, "h"):
		n, err := strconv.ParseFloat(strings.TrimSuffix(v, "h"), 64)
		return n * 3600, err == nil
	}
	parts := strings.Split(v, ":")
	var total float64
	for _, p := range parts {
		n, err := strconv.ParseFloat(p, 64)
		if err != nil {
			return 0, false
		}
		total = total*60 + n
	}
	return total, true
}

// Moment is one sentence on the recording's own timeline: seconds from the
// start of the whole book, which is where SoundStorm's player measures.
type Moment struct {
	Start float64 `json:"t"`
	End   float64 `json:"e"`
	Href  string  `json:"h"`
}

// Timeline puts Storyteller's clips onto the whole book's timeline.
//
// Storyteller numbers the audio files it read in name order, and cuts each at
// its chapter marks (the extra cutting of long chapters is switched off when
// SoundStorm provisions it). So piece p of file f starts where that file's
// p-th chapter does. The layout is Audiobookshelf's view of the same files and
// chapters; if the two do not line up piece for piece, there is no timeline
// rather than a wrong one - a page that follows the wrong sentence is worse
// than a page that does not follow.
func Timeline(clips []Clip, layout source.AudioLayout) ([]Moment, error) {
	if len(clips) == 0 {
		return nil, fmt.Errorf("the synced book has no timings")
	}
	files := append([]source.AudioFile(nil), layout.Files...)
	sort.SliceStable(files, func(i, j int) bool { return files[i].Name < files[j].Name })

	// Each file's chapter starts, measured from the start of that file.
	starts := make([][]float64, len(files))
	for i, f := range files {
		for _, c := range layout.Chapters {
			if c >= f.StartSeconds-0.5 && c < f.StartSeconds+f.DurationSeconds-0.5 {
				starts[i] = append(starts[i], c-f.StartSeconds)
			}
		}
		if len(starts[i]) == 0 {
			// No chapter marks: Storyteller keeps the file whole.
			starts[i] = []float64{0}
		}
	}

	maxPiece := map[int]int{}
	for _, c := range clips {
		if c.File < 1 || c.File > len(files) {
			return nil, fmt.Errorf("the synced book names an audio file the audiobook does not have")
		}
		if c.Piece > maxPiece[c.File] {
			maxPiece[c.File] = c.Piece
		}
	}
	for f, n := range maxPiece {
		if n > len(starts[f-1]) {
			return nil, fmt.Errorf("the synced book is cut into more pieces than the audiobook has chapters")
		}
	}

	out := make([]Moment, 0, len(clips))
	for _, c := range clips {
		f := files[c.File-1]
		base := f.StartSeconds + starts[c.File-1][c.Piece-1]
		out = append(out, Moment{Start: base + c.Begin, End: base + c.End, Href: c.Href})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out, nil
}
