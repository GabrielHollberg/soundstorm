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
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/gabehollberg/atrium/internal/httpx"
	"github.com/gabehollberg/atrium/internal/media"
	"github.com/gabehollberg/atrium/internal/source"
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
	Index int    `json:"index"`
	Ino   string `json:"ino"`
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
			Title:           md.Title,
			Subtitle:        md.Subtitle,
			DurationSeconds: li.Media.Duration,
			Extra:           map[string]string{},
		}
		if md.AuthorName != "" {
			item.Creators = []string{md.AuthorName}
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

// StreamTarget resolves an item to playable audio.
//
// Audiobookshelf addresses audio files by inode, not by item id, and the inode
// is not derivable from anything we already hold - so this costs one extra
// round trip per playback start. That is cheap next to the alternative of
// smuggling the inode through media.Item.ID, which would make the id opaque
// nonsense to every other layer.
//
// Only the first audio file is served. A multi-file audiobook therefore plays
// its first part only; real chapter navigation needs the playback-session API
// and a player that understands a track list, which is the natural next step.
func (s *Source) StreamTarget(ctx context.Context, itemID string) (source.Target, error) {
	if itemID == "" {
		return source.Target{}, fmt.Errorf("audiobookshelf %q: empty item id", s.id)
	}

	var item libraryItem
	path := "/api/items/" + url.PathEscape(itemID)
	if err := s.http.JSON(ctx, path, nil, &item); err != nil {
		return source.Target{}, fmt.Errorf("audiobookshelf %q: resolve item: %w", s.id, err)
	}
	if len(item.Media.AudioFiles) == 0 {
		return source.Target{}, fmt.Errorf("audiobookshelf %q: item %q has no audio files", s.id, itemID)
	}

	first := item.Media.AudioFiles[0]
	for _, f := range item.Media.AudioFiles {
		if f.Index < first.Index {
			first = f
		}
	}
	if first.Ino == "" {
		return source.Target{}, fmt.Errorf("audiobookshelf %q: item %q has no file handle", s.id, itemID)
	}

	return source.Target{
		URL:     s.http.URL(path+"/file/"+url.PathEscape(first.Ino), nil),
		Headers: map[string]string{"Authorization": "Bearer " + s.cfg.Token},
	}, nil
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
