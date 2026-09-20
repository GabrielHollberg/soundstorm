// Package subsonic adapts a Subsonic-API music server, which for atrium means
// Navidrome.
//
// Navidrome is here rather than letting Jellyfin handle music because it is
// simply better at it: multi-value artist tags, album-artist vs artist,
// compilations, ReplayGain, smart playlists, and a scanner that handles a large
// library without complaint. atrium exists so you can have that without also
// having a second app to log into.
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

	"github.com/gabehollberg/atrium/internal/httpx"
	"github.com/gabehollberg/atrium/internal/media"
	"github.com/gabehollberg/atrium/internal/source"
)

const (
	apiVersion = "1.16.1"
	clientName = "atrium"
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
	id   string
	cfg  Config
	http *httpx.Client
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

func (s *Source) Search(ctx context.Context, q media.Query) ([]media.Item, error) {
	params, err := s.auth()
	if err != nil {
		return nil, err
	}
	params.Set("query", q.Text)
	params.Set("songCount", strconv.Itoa(q.LimitOr(25)))
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
// works, so no headers are needed. They never reach the browser: atrium fetches
// them itself and pipes the bytes through, so Navidrome needs no published port.
func (s *Source) StreamTarget(itemID string) (source.Target, error) {
	return s.mediaTarget("/rest/stream.view", itemID)
}

// ArtTarget builds an authenticated upstream target for cover art.
func (s *Source) ArtTarget(artID string) (source.Target, error) {
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
