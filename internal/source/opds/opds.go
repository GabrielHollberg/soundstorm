// Package opds adapts an OPDS catalog, which for atrium means Calibre-Web.
//
// OPDS is Atom with extra link relations, so this adapter is mostly XML
// decoding. Calibre-Web serves its catalog at /opds and search at
// /opds/search/{terms}, behind HTTP Basic auth.
//
// A note on the search path, because this adapter had a silent bug once and the
// shape of it is easy to reintroduce: the query is escaped into the *path*, not
// a query parameter. Escaping it a second time - which is what happens if you
// build the URL by assigning to url.URL.Path - turns a search for "a wizard"
// into a search for the literal text "a%20wizard", which matches nothing and
// reports no error. httpx.Client.URL is careful about this and
// internal/httpx has a regression test for it. Do not route around it.
package opds

import (
	"context"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gabehollberg/atrium/internal/httpx"
	"github.com/gabehollberg/atrium/internal/media"
	"github.com/gabehollberg/atrium/internal/source"
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
	authHeader string
	http       *httpx.Client
}

// New builds an OPDS source.
func New(cfg Config) (*Source, error) {
	c, err := httpx.New(cfg.BaseURL, cfg.Timeout)
	if err != nil {
		return nil, fmt.Errorf("opds %q: %w", cfg.ID, err)
	}

	var authHeader string
	if cfg.Username != "" {
		cred := base64.StdEncoding.EncodeToString([]byte(cfg.Username + ":" + cfg.Password))
		authHeader = "Basic " + cred
		c.SetHeader("Authorization", authHeader)
	}

	sp := cfg.SearchPath
	if sp == "" {
		sp = "/opds/search/{query}"
	}
	return &Source{id: cfg.ID, searchPath: sp, authHeader: authHeader, http: c}, nil
}

func (s *Source) ID() string       { return s.id }
func (s *Source) Kind() media.Kind { return media.KindEbook }

// feed is the subset of an Atom/OPDS feed we care about.
type feed struct {
	Entries []entry `xml:"entry"`
}

type entry struct {
	Title     string `xml:"title"`
	ID        string `xml:"id"`
	Updated   string `xml:"updated"`
	Published string `xml:"published"`
	Summary   string `xml:"summary"`
	Authors   []struct {
		Name string `xml:"name"`
	} `xml:"author"`
	Links []struct {
		Rel   string `xml:"rel,attr"`
		Href  string `xml:"href,attr"`
		Type  string `xml:"type,attr"`
		Title string `xml:"title,attr"`
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
	body, _, err := s.http.Raw(ctx, s.path(q.Text), s.params(q.Text))
	if err != nil {
		return nil, err
	}
	var f feed
	if err := xml.Unmarshal(body, &f); err != nil {
		return nil, fmt.Errorf("decode opds feed: %w (body started %s)", err, httpx.Snippet(body))
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
				if !strings.HasPrefix(item.ID, "dl:") { // keep the first only
					// The acquisition href is what atrium must fetch to hand
					// over the book, so it becomes the streamable id. It
					// contains slashes ("/opds/download/1/epub/"), which is why
					// the stream route uses a trailing wildcard.
					item.ID = "dl:" + strings.TrimPrefix(l.Href, "/")
					if l.Type != "" {
						item.Extra["format"] = shortFormat(l.Type)
					}
				}
			case l.Rel == "http://opds-spec.org/image" || l.Rel == "http://opds-spec.org/cover":
				item.ArtID = "art:" + strings.TrimPrefix(l.Href, "/")
			case l.Rel == "http://opds-spec.org/image/thumbnail" && item.ArtID == "":
				item.ArtID = "art:" + strings.TrimPrefix(l.Href, "/")
			}
		}

		// A catalog entry with no acquisition link is a navigation entry, not a
		// book. Dropping it keeps unopenable rows out of the results.
		if !strings.HasPrefix(item.ID, "dl:") {
			continue
		}

		// Only <published>. <updated> is when the catalog last touched the
		// entry, so falling back to it labels every book with the date it was
		// imported - which looks like a publication year and is not one.
		if y := year(e.Published); y > 0 {
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

// StreamTarget turns the acquisition reference back into a fetchable URL.
func (s *Source) StreamTarget(_ context.Context, itemID string) (source.Target, error) {
	return s.target(itemID, "dl:")
}

// ArtTarget turns the cover reference back into a fetchable URL.
func (s *Source) ArtTarget(_ context.Context, artID string) (source.Target, error) {
	return s.target(artID, "art:")
}

func (s *Source) target(id, prefix string) (source.Target, error) {
	ref, ok := strings.CutPrefix(id, prefix)
	if !ok || ref == "" {
		return source.Target{}, fmt.Errorf("opds %q: %q is not a %s reference", s.id, id, strings.TrimSuffix(prefix, ":"))
	}
	t := source.Target{URL: s.http.URL(ref, nil)}
	if s.authHeader != "" {
		t.Headers = map[string]string{"Authorization": s.authHeader}
	}
	return t, nil
}

func (s *Source) Health(ctx context.Context) error {
	body, _, err := s.http.Raw(ctx, "/opds", nil)
	if err != nil {
		return err
	}
	// The catalog root is a valid feed even when empty, so a clean decode means
	// the server is up and we are authenticated. A login page would not decode.
	var f feed
	if err := xml.Unmarshal(body, &f); err != nil {
		return fmt.Errorf("opds root did not decode as a feed: %w (body started %s)", err, httpx.Snippet(body))
	}
	return nil
}

// year pulls a 4-digit year out of the first plausible timestamp.
//
// Calibre-Web emits "101-01-01T00:00:00+00:00" for books with no publication
// date, so the range check is doing real work here, not being defensive: it is
// what turns a nonsense year into no year at all.
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
