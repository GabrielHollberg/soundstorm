// Package subsonic adapts a Subsonic-API server (Navidrome, Airsonic, Gonic).
//
// Protocol notes: authentication is the salted-token scheme from Subsonic
// 1.13.0 - send the username, a random salt, and token=md5(password+salt).
// The plaintext password never crosses the wire, but note that the server
// must store it recoverably for this to work, so still use TLS.
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

func (s *Source) Search(ctx context.Context, q media.Query) ([]media.Item, error) {
	params, err := s.auth()
	if err != nil {
		return nil, err
	}
	limit := q.LimitOr(25)
	params.Set("query", q.Text)
	params.Set("songCount", strconv.Itoa(limit))
	params.Set("albumCount", "0")
	params.Set("artistCount", "0")

	var env envelope
	if err := s.http.JSON(ctx, "/rest/search3.view", params, &env); err != nil {
		return nil, err
	}
	if e := env.Response.Error; e != nil {
		return nil, fmt.Errorf("subsonic error %d: %s", e.Code, e.Message)
	}
	if env.Response.Status != "ok" {
		return nil, fmt.Errorf("subsonic status %q", env.Response.Status)
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
			DurationSeconds: float64(sg.Duration),
			OpenURL:         s.mediaURL("/rest/stream.view", sg.ID),
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
		if sg.CoverArt != "" {
			item.CoverURL = s.mediaURL("/rest/getCoverArt.view", sg.CoverArt)
		}
		items = append(items, item)
	}
	return items, nil
}

// mediaURL builds an authenticated URL for streaming or cover art.
//
// These URLs embed credentials in the query string, which is how the Subsonic
// protocol works. They are handed to the client, so only expose atrium over
// TLS or a private network (Tailscale).
func (s *Source) mediaURL(path, id string) string {
	params, err := s.auth()
	if err != nil {
		return ""
	}
	params.Set("id", id)
	return s.http.URL(path, params)
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
	if e := env.Response.Error; e != nil {
		return fmt.Errorf("subsonic error %d: %s", e.Code, e.Message)
	}
	if env.Response.Status != "ok" {
		return fmt.Errorf("subsonic status %q", env.Response.Status)
	}
	return nil
}

func (s *Source) Probe(ctx context.Context, q media.Query) ([]byte, string, error) {
	params, err := s.auth()
	if err != nil {
		return nil, "", err
	}
	params.Set("query", q.Text)
	params.Set("songCount", strconv.Itoa(q.LimitOr(5)))
	return s.http.Raw(ctx, "/rest/search3.view", params)
}
