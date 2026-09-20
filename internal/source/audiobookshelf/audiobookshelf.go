// Package audiobookshelf adapts an Audiobookshelf server.
//
// Auth is a bearer token: in the ABS web UI, Settings > Users > (your user)
// exposes an API token.
//
// VERIFY: the search response shape below is the one ABS returns as of the
// 2.x series. If your server returns something different, run
//
//	curl 'http://host/api/libraries/<id>/search?q=test' -H 'Authorization: Bearer <token>'
//
// or hit atrium's own /api/probe/<sourceId>?q=test endpoint, and adjust the
// structs here. Only the field mapping should need to change.
package audiobookshelf

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/gabehollberg/atrium/internal/httpx"
	"github.com/gabehollberg/atrium/internal/media"
)

// Config configures an Audiobookshelf source.
type Config struct {
	ID        string
	BaseURL   string
	Token     string
	LibraryID string
	Timeout   time.Duration
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
		return nil, fmt.Errorf("audiobookshelf %q: libraryId is required (GET /api/libraries to list them)", cfg.ID)
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

type searchResponse struct {
	Book []struct {
		LibraryItem libraryItem `json:"libraryItem"`
	} `json:"book"`
}

type libraryItem struct {
	ID    string `json:"id"`
	Media struct {
		Duration float64 `json:"duration"`
		Metadata struct {
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

func (s *Source) searchPath() string {
	return "/api/libraries/" + s.cfg.LibraryID + "/search"
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
			Title:           md.Title,
			Subtitle:        md.Subtitle,
			DurationSeconds: li.Media.Duration,
			CoverURL:        s.http.URL("/api/items/"+li.ID+"/cover", nil),
			OpenURL:         s.http.URL("/item/"+li.ID, nil),
			Extra:           map[string]string{},
		}
		if md.AuthorName != "" {
			item.Creators = []string{md.AuthorName}
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

func (s *Source) Probe(ctx context.Context, q media.Query) ([]byte, string, error) {
	return s.http.Raw(ctx, s.searchPath(), url.Values{
		"q":     {q.Text},
		"limit": {strconv.Itoa(q.LimitOr(5))},
	})
}
