// Package jellyfin adapts a Jellyfin server for video.
//
// Jellyfin is here for what it is genuinely best at: video, hardware-accelerated
// transcoding and metadata for film and television. atrium does not use its
// music support - Navidrome is better at that - and never exposes its web UI,
// because that would be the second login this project exists to remove.
//
// Auth is an access token obtained by internal/provision logging in as the
// account it created during Jellyfin's startup wizard. Nobody visits the
// Jellyfin dashboard to mint an API key.
package jellyfin

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gabehollberg/atrium/internal/httpx"
	"github.com/gabehollberg/atrium/internal/media"
	"github.com/gabehollberg/atrium/internal/source"
)

// ticksPerSecond is Jellyfin's RunTimeTicks unit: 100-nanosecond intervals.
const ticksPerSecond = 10_000_000

// Config configures a Jellyfin source.
type Config struct {
	ID      string
	BaseURL string
	Token   string // access token from provisioning
	UserID  string // the account atrium created for itself
	Timeout time.Duration
}

// Source is a Jellyfin server serving video.
type Source struct {
	id   string
	cfg  Config
	http *httpx.Client
}

// New builds a Jellyfin source.
func New(cfg Config) (*Source, error) {
	if cfg.Token == "" {
		return nil, fmt.Errorf("jellyfin %q: access token is required", cfg.ID)
	}
	c, err := httpx.New(cfg.BaseURL, cfg.Timeout)
	if err != nil {
		return nil, fmt.Errorf("jellyfin %q: %w", cfg.ID, err)
	}
	c.SetHeader("Authorization", authHeader(cfg.Token))
	c.SetHeader("Accept", "application/json")
	return &Source{id: cfg.ID, cfg: cfg, http: c}, nil
}

func (s *Source) ID() string       { return s.id }
func (s *Source) Kind() media.Kind { return media.KindVideo }

type itemsResponse struct {
	Items            []jfItem `json:"Items"`
	TotalRecordCount int      `json:"TotalRecordCount"`
}

type jfItem struct {
	ID              string            `json:"Id"`
	Name            string            `json:"Name"`
	Type            string            `json:"Type"`
	Overview        string            `json:"Overview"`
	ProductionYear  int               `json:"ProductionYear"`
	RunTimeTicks    int64             `json:"RunTimeTicks"`
	SeriesName      string            `json:"SeriesName"`
	ImageTags       map[string]string `json:"ImageTags"`
	CommunityRating float64           `json:"CommunityRating"`
}

func (s *Source) searchParams(q media.Query) url.Values {
	p := url.Values{
		"searchTerm":       {q.Text},
		"Recursive":        {"true"},
		"IncludeItemTypes": {"Movie,Series,Episode"},
		"Limit":            {strconv.Itoa(q.LimitOr(25))},
		"Fields":           {"Overview,ProductionYear"},
	}
	if s.cfg.UserID != "" {
		p.Set("userId", s.cfg.UserID)
	}
	return p
}

func (s *Source) Search(ctx context.Context, q media.Query) ([]media.Item, error) {
	var resp itemsResponse
	if err := s.http.JSON(ctx, "/Items", s.searchParams(q), &resp); err != nil {
		return nil, err
	}

	items := make([]media.Item, 0, len(resp.Items))
	for _, it := range resp.Items {
		item := media.Item{
			ID:       it.ID,
			SourceID: s.id,
			Kind:     media.KindVideo,
			Title:    it.Name,
			Subtitle: it.SeriesName,
			Year:     it.ProductionYear,
			Extra:    map[string]string{"type": it.Type},
		}
		if it.RunTimeTicks > 0 {
			item.DurationSeconds = float64(it.RunTimeTicks) / ticksPerSecond
		}
		if it.Overview != "" {
			item.Extra["overview"] = truncate(it.Overview, 400)
		}
		if it.CommunityRating > 0 {
			item.Extra["rating"] = strconv.FormatFloat(it.CommunityRating, 'f', 1, 64)
		}
		if tag, ok := it.ImageTags["Primary"]; ok && tag != "" {
			item.ArtID = it.ID
		}
		items = append(items, item)
	}
	return items, nil
}

// StreamTarget builds an authenticated upstream target for a video.
//
// static=true asks Jellyfin to remux nothing and hand over the original file,
// which is right for anything a browser can already play (h264/aac in mp4).
// Real libraries contain plenty that a browser cannot - HEVC, DTS, MKV - and
// those need Jellyfin's HLS endpoint with a device profile instead. That is a
// known gap, not an oversight: it is the first thing to build after this slice
// proves the shape is right.
//
// The credential goes in a header, not the query string. Jellyfin 12 removed the
// api_key query parameter that most guides on the internet still show.
func (s *Source) StreamTarget(itemID string) (source.Target, error) {
	if itemID == "" {
		return source.Target{}, fmt.Errorf("jellyfin %q: empty item id", s.id)
	}
	return source.Target{
		URL:     s.http.URL("/Videos/"+url.PathEscape(itemID)+"/stream", url.Values{"static": {"true"}}),
		Headers: map[string]string{"Authorization": authHeader(s.cfg.Token)},
	}, nil
}

// ArtTarget builds an authenticated upstream target for a poster.
func (s *Source) ArtTarget(artID string) (source.Target, error) {
	if artID == "" {
		return source.Target{}, fmt.Errorf("jellyfin %q: empty art id", s.id)
	}
	return source.Target{
		URL:     s.http.URL("/Items/"+url.PathEscape(artID)+"/Images/Primary", nil),
		Headers: map[string]string{"Authorization": authHeader(s.cfg.Token)},
	}, nil
}

func (s *Source) Health(ctx context.Context) error {
	// /System/Info requires a valid token, so this checks reachability and
	// auth in one call.
	var info struct {
		Version    string `json:"Version"`
		ServerName string `json:"ServerName"`
	}
	if err := s.http.JSON(ctx, "/System/Info", nil, &info); err != nil {
		return err
	}
	if info.Version == "" {
		return fmt.Errorf("jellyfin returned no version; is the access token still valid?")
	}
	return nil
}

// truncate shortens s to at most n bytes without splitting a UTF-8 character.
func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8Start(s[cut]) {
		cut--
	}
	return strings.TrimSpace(s[:cut]) + "..."
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

// authHeader builds the only credential form Jellyfin 12 accepts.
//
// Verified against a live Jellyfin 12.1.0: X-Emby-Token returns 401, so does
// ?api_key=. Both appear in most documentation and in every older client, which
// is exactly why this is a named function with this comment attached rather than
// an inline string - the next person to hit a 401 here should find the answer.
func authHeader(token string) string {
	return `MediaBrowser Client="atrium", Device="atrium", DeviceId="atrium-gateway", Version="0.1.0", Token="` + token + `"`
}
