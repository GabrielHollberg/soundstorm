// Package subsonic adapts a Subsonic-API music server, which for SoundStorm
// means Navidrome.
//
// Navidrome is here rather than letting Jellyfin handle music because it is
// simply better at it: multi-value artist tags, album-artist vs artist,
// compilations, ReplayGain, smart playlists, and a scanner that handles a large
// library without complaint. SoundStorm exists so you can have that without
// also having a second app to log into.
//
// Protocol notes: authentication is the salted-token scheme from Subsonic
// 1.13.0 - send the username, a random salt, and token=md5(password+salt). The
// plaintext password never crosses the wire. The server must store the password
// recoverably for this to work, which Navidrome does on purpose.
package subsonic

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/httpx"
	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/source"
)

const (
	apiVersion = "1.16.1"
	clientName = "soundstorm"
)

// Config configures a Subsonic source.
type Config struct {
	ID       string
	BaseURL  string
	Username string
	Password string
	Timeout  time.Duration
}

// Source is a Subsonic-API music server.
type Source struct {
	id    string
	cfg   Config
	http  *httpx.Client
	shelf media.ShelfCache
}

// New builds a Subsonic source.
func New(cfg Config) (*Source, error) {
	if cfg.Username == "" || cfg.Password == "" {
		return nil, fmt.Errorf("subsonic %q: username and password are required", cfg.ID)
	}
	c, err := httpx.New(cfg.BaseURL, cfg.Timeout)
	if err != nil {
		return nil, fmt.Errorf("subsonic %q: %w", cfg.ID, err)
	}
	return &Source{id: cfg.ID, cfg: cfg, http: c}, nil
}

func (s *Source) ID() string       { return s.id }
func (s *Source) Kind() media.Kind { return media.KindMusic }

// auth returns the per-request authentication parameters.
func (s *Source) auth() (url.Values, error) {
	saltBytes := make([]byte, 8)
	if _, err := rand.Read(saltBytes); err != nil {
		return nil, fmt.Errorf("generate salt: %w", err)
	}
	salt := hex.EncodeToString(saltBytes)
	sum := md5.Sum([]byte(s.cfg.Password + salt))

	return url.Values{
		"u": {s.cfg.Username},
		"t": {hex.EncodeToString(sum[:])},
		"s": {salt},
		"v": {apiVersion},
		"c": {clientName},
		"f": {"json"},
	}, nil
}

// envelope is the outer shape every Subsonic response shares.
type envelope struct {
	Response struct {
		Status  string `json:"status"`
		Version string `json:"version"`
		Error   *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		SearchResult3 struct {
			Song []song `json:"song"`
		} `json:"searchResult3"`
	} `json:"subsonic-response"`
}

type song struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Album    string `json:"album"`
	Artist   string `json:"artist"`
	Year     int    `json:"year"`
	Duration int    `json:"duration"` // seconds
	CoverArt string `json:"coverArt"`
	Suffix   string `json:"suffix"`
}

// check turns a Subsonic envelope into an error when the server reported one.
// Subsonic signals failure with HTTP 200 and an error object, so the status
// code alone is not enough.
func (e *envelope) check() error {
	if err := e.Response.Error; err != nil {
		return fmt.Errorf("subsonic error %d: %s", err.Code, err.Message)
	}
	if e.Response.Status != "ok" {
		return fmt.Errorf("subsonic status %q", e.Response.Status)
	}
	return nil
}

// Search browses or searches the music library.
//
// The whole matching set is fetched, not the first N, because search3 returns
// songs in an order of its own - neither by title nor by any relevance
// SoundStorm can reproduce - and merged paging needs the first N in
// SoundStorm's order (see media.Less). Measured before this: of the first 50
// songs by title in a 4,413-song library, Navidrome's first 50 held none, so
// every scroll repeated some songs and skipped others. A browse is then
// ordered and cut here; a search is returned whole for the merge to rank.
// Listings are cached briefly so scrolling costs one fetch.
func (s *Source) Search(ctx context.Context, q media.Query) ([]media.Item, error) {
	all, err := s.shelf.GetOrFetch(q.Text, func() ([]media.Item, error) { return s.fetchAll(ctx, q.Text) })
	if err != nil {
		return nil, err
	}
	if q.Text == "" {
		return media.FirstN(all, q.LimitOr(25)), nil
	}
	return all, nil
}

// fetchPage is how many songs one search3 call asks for.
const fetchPage = 1000

// maxSongs bounds one listing. Far past any household's library, and short
// of letting a runaway answer eat the server's memory.
const maxSongs = 200_000

// fetchAll pages through every song matching text.
func (s *Source) fetchAll(ctx context.Context, text string) ([]media.Item, error) {
	var items []media.Item
	for offset := 0; offset < maxSongs; offset += fetchPage {
		page, err := s.fetchPage(ctx, text, offset)
		if err != nil {
			return nil, err
		}
		items = append(items, page...)
		if len(page) < fetchPage {
			break
		}
	}
	return items, nil
}

func (s *Source) fetchPage(ctx context.Context, text string, offset int) ([]media.Item, error) {
	params, err := s.auth()
	if err != nil {
		return nil, err
	}
	params.Set("query", text)
	params.Set("songCount", strconv.Itoa(fetchPage))
	params.Set("songOffset", strconv.Itoa(offset))
	params.Set("albumCount", "0")
	params.Set("artistCount", "0")

	var env envelope
	if err := s.http.JSON(ctx, "/rest/search3.view", params, &env); err != nil {
		return nil, err
	}
	if err := env.check(); err != nil {
		return nil, err
	}

	items := make([]media.Item, 0, len(env.Response.SearchResult3.Song))
	for _, sg := range env.Response.SearchResult3.Song {
		item := media.Item{
			ID:              sg.ID,
			SourceID:        s.id,
			Kind:            media.KindMusic,
			Title:           sg.Title,
			Subtitle:        sg.Album,
			Year:            sg.Year,
			ArtID:           sg.CoverArt,
			DurationSeconds: float64(sg.Duration),
			Extra:           map[string]string{},
		}
		if sg.Artist != "" {
			item.Creators = []string{sg.Artist}
		}
		if sg.Album != "" {
			item.Extra["album"] = sg.Album
		}
		if sg.Suffix != "" {
			item.Extra["format"] = sg.Suffix
		}
		items = append(items, item)
	}
	return items, nil
}

// StreamTarget builds an authenticated upstream target for a track.
//
// Subsonic carries credentials in the query string, which is how the protocol
// works, so no headers are needed. They never reach the browser: SoundStorm
// fetches them itself and pipes the bytes through, so Navidrome needs no
// published port.
func (s *Source) StreamTarget(_ context.Context, itemID string) (source.Target, error) {
	return s.mediaTarget("/rest/stream.view", itemID)
}

// ArtTarget builds an authenticated upstream target for cover art.
func (s *Source) ArtTarget(_ context.Context, artID string) (source.Target, error) {
	return s.mediaTarget("/rest/getCoverArt.view", artID)
}

func (s *Source) mediaTarget(path, id string) (source.Target, error) {
	if id == "" {
		return source.Target{}, fmt.Errorf("subsonic %q: empty id", s.id)
	}
	params, err := s.auth()
	if err != nil {
		return source.Target{}, err
	}
	params.Set("id", id)
	return source.Target{URL: s.http.URL(path, params)}, nil
}

// Rescan asks Navidrome to look at the music folder now.
//
// Verified against 0.64.0: /rest/startScan answers with a scanStatus saying
// scanning is true, and the scan it starts is the quick kind - it looks at
// what changed rather than re-reading every tag, which is what makes it cheap
// enough to fire after an upload.
func (s *Source) Rescan(ctx context.Context) error {
	s.shelf.Clear()
	params, err := s.auth()
	if err != nil {
		return err
	}
	var env envelope
	if err := s.http.JSON(ctx, "/rest/startScan.view", params, &env); err != nil {
		return fmt.Errorf("subsonic %q: start scan: %w", s.id, err)
	}
	return env.check()
}

func (s *Source) Health(ctx context.Context) error {
	params, err := s.auth()
	if err != nil {
		return err
	}
	var env envelope
	if err := s.http.JSON(ctx, "/rest/ping.view", params, &env); err != nil {
		return err
	}
	return env.check()
}
