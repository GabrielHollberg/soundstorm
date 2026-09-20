// Package opds adapts any OPDS 1.x catalog, which is how the Calibre content
// server exposes a library.
//
// OPDS is Atom with extra link relations, so this adapter is mostly XML
// decoding. Calibre serves its catalog at /opds and search at
// /opds/search/{terms}.
//
// Auth is optional HTTP Basic - Calibre only requires it if you enabled
// authentication on the content server.
package opds

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gabehollberg/atrium/internal/httpx"
	"github.com/gabehollberg/atrium/internal/media"
)

// Config configures an OPDS source.
type Config struct {
	ID         string
	BaseURL    string
	SearchPath string // default "/opds/search/{query}"
	Username   string
	Password   string
	Timeout    time.Duration
}

// Source is an OPDS catalog.
type Source struct {
	id         string
	searchPath string
	http       *httpx.Client
}

// New builds an OPDS source.
func New(cfg Config) (*Source, error) {
	c, err := httpx.New(cfg.BaseURL, cfg.Timeout)
	if err != nil {
		return nil, fmt.Errorf("opds %q: %w", cfg.ID, err)
	}
	if cfg.Username != "" {
		cred := base64.StdEncoding.EncodeToString([]byte(cfg.Username + ":" + cfg.Password))
		c.SetHeader("Authorization", "Basic "+cred)
	}
	sp := cfg.SearchPath
	if sp == "" {
		sp = "/opds/search/{query}"
	}
	return &Source{id: cfg.ID, searchPath: sp, http: c}, nil
}

func (s *Source) ID() string       { return s.id }
func (s *Source) Kind() media.Kind { return media.KindEbook }

// feed is the subset of an Atom/OPDS feed we care about.
type feed struct {
	Entries []entry `xml:"entry"`
}

type entry struct {
	Title   string `xml:"title"`
	ID      string `xml:"id"`
	Updated string `xml:"updated"`
	Issued  string `xml:"issued"`
	Summary string `xml:"summary"`
	Authors []struct {
		Name string `xml:"name"`
	} `xml:"author"`
	Links []struct {
		Rel  string `xml:"rel,attr"`
		Href string `xml:"href,attr"`
		Type string `xml:"type,attr"`
	} `xml:"link"`
	Categories []struct {
		Term string `xml:"term,attr"`
	} `xml:"category"`
}

// path substitutes the query into the configured search path.
func (s *Source) path(q string) string {
	if strings.Contains(s.searchPath, "{query}") {
		return strings.ReplaceAll(s.searchPath, "{query}", url.PathEscape(q))
	}
	return s.searchPath
}

// params returns query params when the search path does not embed the query.
func (s *Source) params(q string) url.Values {
	if strings.Contains(s.searchPath, "{query}") {
		return nil
	}
	return url.Values{"q": {q}}
}

func (s *Source) Search(ctx context.Context, q media.Query) ([]media.Item, error) {
	var f feed
	if err := s.http.XML(ctx, s.path(q.Text), s.params(q.Text), &f); err != nil {
		return nil, err
	}

	limit := q.LimitOr(25)
	items := make([]media.Item, 0, len(f.Entries))
	for _, e := range f.Entries {
		if len(items) >= limit {
			break
		}
		item := media.Item{
			ID:       e.ID,
			SourceID: s.id,
			Kind:     media.KindEbook,
			Title:    strings.TrimSpace(e.Title),
			Extra:    map[string]string{},
		}
		for _, a := range e.Authors {
			if a.Name != "" {
				item.Creators = append(item.Creators, a.Name)
			}
		}
		for _, l := range e.Links {
			switch {
			case strings.HasPrefix(l.Rel, "http://opds-spec.org/acquisition"):
				if item.OpenURL == "" {
					item.OpenURL = s.absolute(l.Href)
					if l.Type != "" {
						item.Extra["format"] = shortFormat(l.Type)
					}
				}
			case l.Rel == "http://opds-spec.org/image" || l.Rel == "http://opds-spec.org/cover":
				item.CoverURL = s.absolute(l.Href)
			case l.Rel == "http://opds-spec.org/image/thumbnail" && item.CoverURL == "":
				item.CoverURL = s.absolute(l.Href)
			}
		}
		if y := year(e.Issued, e.Updated); y > 0 {
			item.Year = y
		}
		var tags []string
		for _, c := range e.Categories {
			if c.Term != "" {
				tags = append(tags, c.Term)
			}
		}
		if len(tags) > 0 {
			item.Extra["tags"] = strings.Join(tags, ", ")
		}
		items = append(items, item)
	}
	return items, nil
}

// absolute resolves a possibly-relative OPDS href against the catalog base.
func (s *Source) absolute(href string) string {
	if href == "" {
		return ""
	}
	if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
		return href
	}
	return s.http.URL(href, nil)
}

func (s *Source) Health(ctx context.Context) error {
	var f feed
	// The catalog root is a valid feed even when empty, so a clean decode
	// means the server is up and we are authenticated.
	return s.http.XML(ctx, "/opds", nil, &f)
}

func (s *Source) Probe(ctx context.Context, q media.Query) ([]byte, string, error) {
	return s.http.Raw(ctx, s.path(q.Text), s.params(q.Text))
}

// year pulls a 4-digit year out of the first parseable timestamp.
func year(candidates ...string) int {
	for _, c := range candidates {
		if len(c) >= 4 {
			if y, err := strconv.Atoi(c[:4]); err == nil && y > 1000 && y < 3000 {
				return y
			}
		}
	}
	return 0
}

// shortFormat turns a MIME type into something worth showing a human.
func shortFormat(mime string) string {
	switch {
	case strings.Contains(mime, "epub"):
		return "epub"
	case strings.Contains(mime, "pdf"):
		return "pdf"
	case strings.Contains(mime, "mobi"):
		return "mobi"
	case strings.Contains(mime, "x-cbz"):
		return "cbz"
	default:
		return mime
	}
}
