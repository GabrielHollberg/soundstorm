// Package kiwix adapts a kiwix-serve instance (offline Wikipedia, Stack
// Exchange, Gutenberg and friends served from ZIM files).
//
// VERIFY: kiwix-serve's search endpoint has changed shape across releases.
// This adapter targets the OpenSearch-style XML response from
// /search?pattern=...&format=xml, which recent kiwix-serve builds return.
// If yours differs, hit atrium's /api/probe/<sourceId>?q=test and adjust the
// structs below; nothing outside this file needs to change.
package kiwix

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gabehollberg/atrium/internal/httpx"
	"github.com/gabehollberg/atrium/internal/media"
)

// Config configures a Kiwix source.
type Config struct {
	ID      string
	BaseURL string
	Book    string // optional: restrict to one ZIM by name
	Timeout time.Duration
}

// Source is a kiwix-serve instance.
type Source struct {
	id   string
	book string
	http *httpx.Client
}

// New builds a Kiwix source.
func New(cfg Config) (*Source, error) {
	c, err := httpx.New(cfg.BaseURL, cfg.Timeout)
	if err != nil {
		return nil, err
	}
	return &Source{id: cfg.ID, book: cfg.Book, http: c}, nil
}

func (s *Source) ID() string       { return s.id }
func (s *Source) Kind() media.Kind { return media.KindArticle }

// rss is the OpenSearch response kiwix-serve returns for format=xml.
type rss struct {
	Channel struct {
		Items []struct {
			Title       string `xml:"title"`
			Link        string `xml:"link"`
			Description string `xml:"description"`
			BookTitle   string `xml:"book_title"`
		} `xml:"item"`
	} `xml:"channel"`
}

func (s *Source) searchParams(q media.Query) url.Values {
	p := url.Values{
		"pattern":    {q.Text},
		"format":     {"xml"},
		"pageLength": {strconv.Itoa(q.LimitOr(20))},
	}
	if s.book != "" {
		p.Set("books.name", s.book)
	}
	return p
}

func (s *Source) Search(ctx context.Context, q media.Query) ([]media.Item, error) {
	var r rss
	if err := s.http.XML(ctx, "/search", s.searchParams(q), &r); err != nil {
		return nil, err
	}

	items := make([]media.Item, 0, len(r.Channel.Items))
	for _, it := range r.Channel.Items {
		item := media.Item{
			ID:       it.Link,
			SourceID: s.id,
			Kind:     media.KindArticle,
			Title:    strings.TrimSpace(it.Title),
			Subtitle: it.BookTitle,
			OpenURL:  s.absolute(it.Link),
			Extra:    map[string]string{},
		}
		if snippet := stripTags(it.Description); snippet != "" {
			item.Extra["snippet"] = snippet
		}
		if it.BookTitle != "" {
			item.Extra["book"] = it.BookTitle
		}
		items = append(items, item)
	}
	return items, nil
}

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
	// The OPDS catalog root is served by every modern kiwix-serve and is
	// cheap, so it makes a good liveness probe.
	_, _, err := s.http.Raw(ctx, "/catalog/v2/root.xml", nil)
	return err
}

func (s *Source) Probe(ctx context.Context, q media.Query) ([]byte, string, error) {
	return s.http.Raw(ctx, "/search", s.searchParams(q))
}

// stripTags removes the HTML markup kiwix wraps around search snippets.
func stripTags(s string) string {
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch r {
		case '<':
			depth++
		case '>':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				b.WriteRune(r)
			}
		}
	}
	return strings.TrimSpace(strings.Join(strings.Fields(b.String()), " "))
}
