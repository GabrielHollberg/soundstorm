// Package audiobookshelf adapts an Audiobookshelf server.
//
// Audiobookshelf is here rather than letting Jellyfin or Navidrome hold
// audiobooks because it is the only one of the three that models a book as a
// book: an author rather than an "artist", a narrator, a series, and listening
// position tracked per title across devices. Shelving audiobooks in a music
// library loses all of that.
//
// Auth is a bearer token obtained by internal/provision creating the server's
// root account during its first-run flow.
package audiobookshelf

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"math"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/httpx"
	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/source"
)

// Config configures an Audiobookshelf source.
type Config struct {
	ID        string
	BaseURL   string
	Token     string
	LibraryID string
	Timeout   time.Duration

	// TokenFor resolves a SoundStorm account to an Audiobookshelf one.
	//
	// Only listening position uses it. Searching, artwork and audio bytes all
	// go through the shared account, because they are the same for everybody
	// and asking per person would cost an account on the backend for anyone
	// who ever ran a search. Position is different: Audiobookshelf stores it
	// per account, so without this two people lose each other's place.
	//
	// Nil means single-user: everything uses Token.
	TokenFor func(ctx context.Context, userID string) (string, error)
}

// Source is an Audiobookshelf library.
type Source struct {
	id   string
	cfg  Config
	http *httpx.Client
}

// New builds an Audiobookshelf source.
func New(cfg Config) (*Source, error) {
	if cfg.Token == "" {
		return nil, fmt.Errorf("audiobookshelf %q: token is required", cfg.ID)
	}
	if cfg.LibraryID == "" {
		return nil, fmt.Errorf("audiobookshelf %q: libraryId is required", cfg.ID)
	}
	c, err := httpx.New(cfg.BaseURL, cfg.Timeout)
	if err != nil {
		return nil, fmt.Errorf("audiobookshelf %q: %w", cfg.ID, err)
	}
	c.SetHeader("Authorization", "Bearer "+cfg.Token)
	c.SetHeader("Accept", "application/json")
	return &Source{id: cfg.ID, cfg: cfg, http: c}, nil
}

func (s *Source) ID() string       { return s.id }
func (s *Source) Kind() media.Kind { return media.KindAudiobook }

// searchResponse is the shape of GET /api/libraries/{id}/search.
//
// Verified against Audiobookshelf 2.36.1. Results are wrapped one level deeper
// than you would expect: book[].libraryItem, not book[].
type searchResponse struct {
	Book []struct {
		LibraryItem libraryItem `json:"libraryItem"`
	} `json:"book"`
}

type libraryItem struct {
	ID    string `json:"id"`
	Media struct {
		Duration   float64     `json:"duration"`
		CoverPath  string      `json:"coverPath"`
		AudioFiles []audioFile `json:"audioFiles"`
		Chapters   []chapter   `json:"chapters"`
		Metadata   struct {
			Title         string `json:"title"`
			Subtitle      string `json:"subtitle"`
			AuthorName    string `json:"authorName"`
			NarratorName  string `json:"narratorName"`
			SeriesName    string `json:"seriesName"`
			PublishedYear string `json:"publishedYear"`
			ISBN          string `json:"isbn"`
		} `json:"metadata"`
	} `json:"media"`
}

type audioFile struct {
	Index    int     `json:"index"`
	Ino      string  `json:"ino"`
	Duration float64 `json:"duration"`

	Metadata struct {
		Filename string `json:"filename"`
	} `json:"metadata"`

	// MetaTags is what was in the file's own ID3 tags. LibriVox puts the
	// chapter name in the title tag, which is the best name available for a
	// file whose own name is "fabula_01_001_esopo_64kb.mp3".
	MetaTags struct {
		Title string `json:"tagTitle"`
	} `json:"metaTags"`
}

// chapter is one entry in Audiobookshelf's own chapter list.
//
// Start and End are offsets into the whole book, not into any one file, so a
// chapter cannot be played by itself. They are read only to get at the titles,
// which for a book assembled from an external source are better than anything
// the files carry.
type chapter struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Title string  `json:"title"`
}

// decodeEntities undoes HTML escaping that arrives in metadata as literal text.
//
// LibriVox catalogue entries carry titles like "Las F&aacute;bulas de Esopo",
// and Audiobookshelf stores what it is given, so without this a reader sees the
// entity rather than the accent. Normalization at the edge: only the adapter
// knows its backend ships HTML in places that are not HTML.
func decodeEntities(value string) string {
	if !strings.Contains(value, "&") {
		return value
	}
	return html.UnescapeString(value)
}

func (s *Source) searchPath() string {
	return "/api/libraries/" + url.PathEscape(s.cfg.LibraryID) + "/search"
}

func (s *Source) Search(ctx context.Context, q media.Query) ([]media.Item, error) {
	params := url.Values{
		"q":     {q.Text},
		"limit": {strconv.Itoa(q.LimitOr(25))},
	}
	var resp searchResponse
	if err := s.http.JSON(ctx, s.searchPath(), params, &resp); err != nil {
		return nil, err
	}

	items := make([]media.Item, 0, len(resp.Book))
	for _, b := range resp.Book {
		li := b.LibraryItem
		md := li.Media.Metadata

		item := media.Item{
			ID:              li.ID,
			SourceID:        s.id,
			Kind:            media.KindAudiobook,
			Title:           decodeEntities(md.Title),
			Subtitle:        decodeEntities(md.Subtitle),
			DurationSeconds: li.Media.Duration,
			Extra:           map[string]string{},
		}
		if md.AuthorName != "" {
			item.Creators = []string{decodeEntities(md.AuthorName)}
		}
		// Only claim artwork when the server actually has a cover file.
		// Audiobookshelf answers /cover with a 404 otherwise, which would put a
		// broken image in every result.
		if li.Media.CoverPath != "" {
			item.ArtID = li.ID
		}
		if md.NarratorName != "" {
			item.Extra["narrator"] = md.NarratorName
		}
		if md.SeriesName != "" {
			item.Extra["series"] = md.SeriesName
		}
		if md.ISBN != "" {
			item.Extra["isbn"] = md.ISBN
		}
		if y, err := strconv.Atoi(md.PublishedYear); err == nil {
			item.Year = y
		}
		items = append(items, item)
	}
	return items, nil
}

// trackSeparator joins an item id to one of its file handles.
//
// Audiobookshelf item ids are uuids, or "li_" and a nanoid, and never contain a
// slash - so "{itemID}/{ino}" is unambiguous, and a track id can be handed
// straight to /api/stream/{source}/{id...} like any other id.
const trackSeparator = "/"

// fetchItem reads one library item in full, which is the only way to learn its
// files. A search result does not carry them.
func (s *Source) fetchItem(ctx context.Context, itemID string) (libraryItem, error) {
	var item libraryItem
	err := s.http.JSON(ctx, "/api/items/"+url.PathEscape(itemID), nil, &item)
	if err != nil {
		return item, fmt.Errorf("audiobookshelf %q: resolve item: %w", s.id, err)
	}
	return item, nil
}

// orderedFiles returns an item's audio files in playing order.
//
// Audiobookshelf numbers them from 1 in the order it decided they belong, but
// the array is not promised to arrive sorted, and for a thirty-part book getting
// that wrong means chapter 10 following chapter 1.
func orderedFiles(item libraryItem) []audioFile {
	files := make([]audioFile, 0, len(item.Media.AudioFiles))
	for _, f := range item.Media.AudioFiles {
		if f.Ino != "" {
			files = append(files, f)
		}
	}
	sort.SliceStable(files, func(i, j int) bool { return files[i].Index < files[j].Index })
	return files
}

// Tracks lists the files a book is made of.
//
// This is what stops a multi-part audiobook playing its first chapter and
// going quiet. A LibriVox volume is one MP3 per chapter - thirty of them for
// Aesop - and each is separately addressable, so the whole list is worth one
// round trip at the moment somebody presses play.
func (s *Source) Tracks(ctx context.Context, itemID string) ([]source.Track, error) {
	if itemID == "" {
		return nil, fmt.Errorf("audiobookshelf %q: empty item id", s.id)
	}
	item, err := s.fetchItem(ctx, itemID)
	if err != nil {
		return nil, err
	}

	files := orderedFiles(item)
	titles := trackTitles(item, files)

	// Offsets into the whole book, which is the timeline Audiobookshelf
	// measures listening position on: its chapter list starts chapter two at
	// the running total of file one, so the two agree by construction.
	var start float64
	tracks := make([]source.Track, 0, len(files))
	for i, f := range files {
		tracks = append(tracks, source.Track{
			ID:              itemID + trackSeparator + f.Ino,
			Title:           titles[i],
			DurationSeconds: f.Duration,
			StartSeconds:    start,
		})
		start += f.Duration
	}
	return tracks, nil
}

// trackTitles names each file, best source first.
//
// Audiobookshelf's chapter list is preferred when there is exactly one chapter
// per file, which is what a per-chapter rip looks like and what all three
// multi-part books in the test library are. That equal-count test is a
// heuristic rather than proof - chapter offsets are into the whole book, so
// proving alignment means summing durations and picking a tolerance - but the
// cost of it being wrong is a mislabelled chapter, not a misplayed one.
//
// Failing that: the file's own title tag, then its name, then its position.
func trackTitles(item libraryItem, files []audioFile) []string {
	chapters := item.Media.Chapters
	aligned := len(chapters) == len(files) && len(files) > 0

	titles := make([]string, len(files))
	for i, f := range files {
		var title string
		if aligned {
			title = chapters[i].Title
		}
		if title == "" {
			title = f.MetaTags.Title
		}
		if title == "" {
			title = strings.TrimSuffix(f.Metadata.Filename, path.Ext(f.Metadata.Filename))
		}
		// decodeEntities for the same reason search needs it: LibriVox metadata
		// carries HTML entities into fields that are not HTML, and it reaches
		// the tags and the chapter names as readily as the title.
		title = strings.TrimSpace(decodeEntities(title))
		if title == "" {
			title = fmt.Sprintf("Part %d", i+1)
		}
		titles[i] = title
	}
	return titles
}

// StreamTarget resolves an item, or one file of it, to playable audio.
//
// Audiobookshelf addresses audio files by inode rather than by item id, and the
// inode is not derivable from anything we already hold. A track id from Tracks
// carries it, so playing a chapter list costs no extra round trips; asking for a
// bare item id still works and still costs one, which is what the direct-play
// fallback does when the track list has not arrived or was not wanted.
func (s *Source) StreamTarget(ctx context.Context, itemID string) (source.Target, error) {
	if itemID == "" {
		return source.Target{}, fmt.Errorf("audiobookshelf %q: empty item id", s.id)
	}

	if item, ino, ok := strings.Cut(itemID, trackSeparator); ok {
		if item == "" || ino == "" {
			return source.Target{}, fmt.Errorf("audiobookshelf %q: malformed track id %q", s.id, itemID)
		}
		return s.fileTarget(item, ino), nil
	}

	item, err := s.fetchItem(ctx, itemID)
	if err != nil {
		return source.Target{}, err
	}
	files := orderedFiles(item)
	if len(files) == 0 {
		return source.Target{}, fmt.Errorf("audiobookshelf %q: item %q has no audio files", s.id, itemID)
	}
	return s.fileTarget(itemID, files[0].Ino), nil
}

// fileTarget builds the upstream URL for one file of one item.
func (s *Source) fileTarget(itemID, ino string) source.Target {
	return source.Target{
		URL: s.http.URL("/api/items/"+url.PathEscape(itemID)+
			"/file/"+url.PathEscape(ino), nil),
		Headers: map[string]string{"Authorization": "Bearer " + s.cfg.Token},
	}
}

// --- listening position ------------------------------------------------------

// progressPath is Audiobookshelf's per-user media progress for one item.
func progressPath(itemID string) string {
	return "/api/me/progress/" + url.PathEscape(itemID)
}

// mediaProgress is what GET /api/me/progress/{id} returns.
//
// Its fields are decoded leniently, because 2.36.1 stores a progress record
// without validating it: PATCHing the string "x" as currentTime is accepted
// with a 200 and read straight back. Strict decoding would turn one junk record
// - written by any client, ours or otherwise - into a hard error, and resume
// would then fail for that book forever while the log filled with warnings.
// A value that is not a number means the same thing as no record at all.
type mediaProgress struct {
	CurrentTime looseFloat `json:"currentTime"`
	Duration    looseFloat `json:"duration"`
	IsFinished  looseBool  `json:"isFinished"`
}

// looseFloat is a number that tolerates not being one.
type looseFloat float64

func (v *looseFloat) UnmarshalJSON(raw []byte) error {
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil
	}
	*v = looseFloat(f)
	return nil
}

// looseBool is a flag that tolerates not being one.
type looseBool bool

func (v *looseBool) UnmarshalJSON(raw []byte) error {
	var b bool
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil
	}
	*v = looseBool(b)
	return nil
}

// actAs returns the Authorization header to use for the caller's own data.
//
// An error rather than a fallback to the shared account: quietly writing one
// person's position into another's record is worse than not saving it, and
// "your place was not saved" is at least something a log can say.
func (s *Source) actAs(ctx context.Context) (map[string]string, error) {
	token := s.cfg.Token
	if s.cfg.TokenFor != nil {
		if userID := source.UserID(ctx); userID != "" {
			resolved, err := s.cfg.TokenFor(ctx, userID)
			if err != nil {
				return nil, fmt.Errorf("audiobookshelf %q: no account for this user: %w", s.id, err)
			}
			token = resolved
		}
	}
	if token == "" {
		return nil, fmt.Errorf("audiobookshelf %q: no credentials for this request", s.id)
	}
	return map[string]string{"Authorization": "Bearer " + token}, nil
}

// Position reads how far into a book somebody listened.
//
// A book nobody has started answers 404, which is not an error worth reporting:
// "never opened" and "at the start" are the same instruction to a player.
func (s *Source) Position(ctx context.Context, itemID string) (source.Position, error) {
	if itemID == "" {
		return source.Position{}, fmt.Errorf("audiobookshelf %q: empty item id", s.id)
	}

	headers, err := s.actAs(ctx)
	if err != nil {
		return source.Position{}, err
	}

	resp, err := s.http.Do(ctx, httpx.Request{Path: progressPath(itemID), Headers: headers})
	if err != nil {
		return source.Position{}, fmt.Errorf("audiobookshelf %q: read position: %w", s.id, err)
	}
	if err := resp.Err(); err != nil {
		var statusErr *httpx.StatusError
		if errors.As(err, &statusErr) && statusErr.Status == http.StatusNotFound {
			return source.Position{}, nil
		}
		return source.Position{}, fmt.Errorf("audiobookshelf %q: read position: %w", s.id, err)
	}
	var progress mediaProgress
	if err := resp.JSON(&progress); err != nil {
		return source.Position{}, fmt.Errorf("audiobookshelf %q: read position: %w", s.id, err)
	}

	seconds, duration := float64(progress.CurrentTime), float64(progress.Duration)
	// A stored value can still be a number and not be a time - json decodes
	// 1e999 to +Inf without complaint, and that reaches a browser as invalid
	// JSON rather than as a position.
	if !isPlayableTime(seconds) {
		return source.Position{}, nil
	}
	if !isPlayableTime(duration) {
		duration = 0
	}
	return source.Position{
		Seconds:  seconds,
		Duration: duration,
		Finished: bool(progress.IsFinished),
	}, nil
}

// SetPosition records how far into a book somebody listened.
//
// Two things about this endpoint are worth knowing, both established against
// 2.36.1 rather than read anywhere:
//
// It does not derive progress from currentTime. Send one without the other and
// the position moves while the percentage stays where it was, so Audiobookshelf's
// own shelf and its "continue listening" row disagree with its player. The
// fraction is computed here for that reason.
//
// isFinished is one-way. Sending false for a book already marked finished does
// not merely clear the flag - it resets currentTime and progress to zero, which
// is what "mark as unfinished" means in the Audiobookshelf UI. Somebody who
// reached the end and then scrubbed back would lose their place. So the flag is
// sent only when it is true; a later position on a finished book clears it as a
// side effect anyway.
func (s *Source) SetPosition(ctx context.Context, itemID string, pos source.Position) error {
	if itemID == "" {
		return fmt.Errorf("audiobookshelf %q: empty item id", s.id)
	}
	if !isPlayableTime(pos.Seconds) || !isPlayableTime(pos.Duration) {
		return fmt.Errorf("audiobookshelf %q: position %v of %v is not a time",
			s.id, pos.Seconds, pos.Duration)
	}

	payload := map[string]any{"currentTime": pos.Seconds}
	if pos.Duration > 0 {
		payload["duration"] = pos.Duration
		payload["progress"] = math.Min(pos.Seconds/pos.Duration, 1)
	}
	if pos.Finished {
		payload["isFinished"] = true
		payload["progress"] = float64(1)
	}

	headers, err := s.actAs(ctx)
	if err != nil {
		return err
	}
	resp, err := s.http.Do(ctx, httpx.Request{
		Method:  http.MethodPatch,
		Path:    progressPath(itemID),
		Headers: headers,
		Body:    payload,
	})
	if err != nil {
		return fmt.Errorf("audiobookshelf %q: save position: %w", s.id, err)
	}
	if err := resp.Err(); err != nil {
		return fmt.Errorf("audiobookshelf %q: save position: %w", s.id, err)
	}
	return nil
}

// isPlayableTime rejects anything a media element could not seek to.
func isPlayableTime(seconds float64) bool {
	return !math.IsNaN(seconds) && !math.IsInf(seconds, 0) && seconds >= 0
}

// ArtTarget builds an authenticated upstream target for a cover.
func (s *Source) ArtTarget(_ context.Context, artID string) (source.Target, error) {
	if artID == "" {
		return source.Target{}, fmt.Errorf("audiobookshelf %q: empty art id", s.id)
	}
	return source.Target{
		URL:     s.http.URL("/api/items/"+url.PathEscape(artID)+"/cover", nil),
		Headers: map[string]string{"Authorization": "Bearer " + s.cfg.Token},
	}, nil
}

// Rescan asks Audiobookshelf to look at the audiobook folder now.
//
// It watches the folder too, so this is belt and braces rather than the only
// way a new book is noticed - but a watcher can miss a file copied in by
// something it did not expect, and asking costs one call.
func (s *Source) Rescan(ctx context.Context) error {
	resp, err := s.http.Do(ctx, httpx.Request{
		Method: http.MethodPost,
		Path:   "/api/libraries/" + url.PathEscape(s.cfg.LibraryID) + "/scan",
	})
	if err != nil {
		return fmt.Errorf("audiobookshelf %q: scan: %w", s.id, err)
	}
	return resp.Err()
}

func (s *Source) Health(ctx context.Context) error {
	var resp struct {
		Libraries []struct {
			ID string `json:"id"`
		} `json:"libraries"`
	}
	if err := s.http.JSON(ctx, "/api/libraries", nil, &resp); err != nil {
		return err
	}
	for _, l := range resp.Libraries {
		if l.ID == s.cfg.LibraryID {
			return nil
		}
	}
	return fmt.Errorf("library %q not found on this server", s.cfg.LibraryID)
}
