// Package jellyfin adapts a Jellyfin (or Emby) server for video.
//
// Auth is an API key, sent as X-Emby-Token. Create one in the Jellyfin admin
// dashboard under Advanced > API Keys.
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
)

// ticksPerSecond is Jellyfin's RunTimeTicks unit: 100-nanosecond intervals.
const ticksPerSecond = 10_000_000

// Config configures a Jellyfin source.
type Config struct {
	ID      string
	BaseURL string
	APIKey  string
	UserID  string // optional; scopes results to a user's libraries
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
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("jellyfin %q: apiKey is required", cfg.ID)
	}
	c, err := httpx.New(cfg.BaseURL, cfg.Timeout)
	if err != nil {
		return nil, fmt.Errorf("jellyfin %q: %w", cfg.ID, err)
	}
	c.SetHeader("X-Emby-Token", cfg.APIKey)
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
			OpenURL:  s.http.URL("/web/index.html", nil) + "#!/details?id=" + url.QueryEscape(it.ID),
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
			item.CoverURL = s.http.URL("/Items/"+it.ID+"/Images/Primary", url.Values{"tag": {tag}})
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *Source) Health(ctx context.Context) error {
	// /System/Info requires a valid key, so this checks reachability and auth
	// in one call.
	var info struct {
		Version    string `json:"Version"`
		ServerName string `json:"ServerName"`
	}
	if err := s.http.JSON(ctx, "/System/Info", nil, &info); err != nil {
		return err
	}
	if info.Version == "" {
		return fmt.Errorf("jellyfin returned no version; is the API key valid?")
	}
	return nil
}

func (s *Source) Probe(ctx context.Context, q media.Query) ([]byte, string, error) {
	p := s.searchParams(q)
	p.Set("Limit", strconv.Itoa(q.LimitOr(5)))
	return s.http.Raw(ctx, "/Items", p)
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
