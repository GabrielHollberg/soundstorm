// Package immich adapts Immich - the photo server - to SoundStorm's model.
//
// Immich owns everything expensive about photos: reading EXIF, decoding HEIC
// (which Go's standard library cannot), making thumbnails, transcoding phone
// videos, and recognising what is in a picture so "dog on a beach" finds one.
// SoundStorm owns the login and the grid. Nobody sees Immich: its port is not
// published and its phone apps have nothing to connect to.
package immich

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/httpx"
	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/source"
)

// Config configures the adapter.
type Config struct {
	ID        string
	BaseURL   string // internal, e.g. http://immich-server:2283
	APIKey    string
	LibraryID string
	Timeout   time.Duration

	// MediaRoot is the pictures folder as Immich's container sees it
	// ("/pictures"), which is how Immich reports an asset's original path.
	// Empty means this source cannot name its files.
	MediaRoot string
}

// Source is one Immich external library.
type Source struct {
	id   string
	cfg  Config
	http *httpx.Client
}

// New builds the adapter. It does no I/O.
func New(cfg Config) (*Source, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("immich %q: api key is required", cfg.ID)
	}
	if cfg.LibraryID == "" {
		return nil, fmt.Errorf("immich %q: library id is required", cfg.ID)
	}
	c, err := httpx.New(cfg.BaseURL, cfg.Timeout)
	if err != nil {
		return nil, fmt.Errorf("immich %q: %w", cfg.ID, err)
	}
	c.SetHeader("x-api-key", cfg.APIKey)
	c.SetHeader("Accept", "application/json")
	return &Source{id: cfg.ID, cfg: cfg, http: c}, nil
}

func (s *Source) ID() string       { return s.id }
func (s *Source) Kind() media.Kind { return media.KindPicture }

// asset is the part of Immich's AssetResponseDto SoundStorm uses.
type asset struct {
	ID               string `json:"id"`
	Type             string `json:"type"` // IMAGE, VIDEO
	OriginalFileName string `json:"originalFileName"`
	OriginalMimeType string `json:"originalMimeType"`
	LocalDateTime    string `json:"localDateTime"`
	Duration         *int64 `json:"duration"` // milliseconds, null for stills
	Width            int    `json:"width"`
	Height           int    `json:"height"`
	IsOffline        bool   `json:"isOffline"`
	OriginalPath     string `json:"originalPath"`
	LibraryID        string `json:"libraryId"`
	IsTrashed        bool   `json:"isTrashed"`
	ExifInfo         *struct {
		City    string `json:"city"`
		Country string `json:"country"`
	} `json:"exifInfo"`
}

type searchResponse struct {
	Assets struct {
		Items    []asset `json:"items"`
		NextPage *string `json:"nextPage"`
	} `json:"assets"`
}

// maxPage is Immich's largest page. Deep browsing asks for several.
const maxPage = 1000

// Search browses newest first with no text, or searches by what is in the
// picture with some.
//
// Both answers come back in an order that is not the title - newest first,
// or most relevant first - so every item carries a SortKey preserving
// Immich's own order. Paging a merged list depends on each source returning
// its own first N in the merged order, and this is how that stays true for a
// shelf nobody browses alphabetically.
func (s *Source) Search(ctx context.Context, q media.Query) ([]media.Item, error) {
	limit := q.LimitOr(25)
	var found []asset
	var err error
	if strings.TrimSpace(q.Text) == "" {
		found, err = s.page(ctx, "/api/search/metadata", map[string]any{
			"libraryId": s.cfg.LibraryID,
			"order":     "desc",
			"isOffline": false,
			"withExif":  true,
		}, limit)
	} else {
		// Smart search needs the machine-learning container. Without it -
		// switched off on a small machine, or still loading its models - a
		// search by file name still finds IMG_4031.
		found, err = s.page(ctx, "/api/search/smart", map[string]any{
			"query":    q.Text,
			"withExif": true,
		}, limit)
		if err != nil {
			found, err = s.page(ctx, "/api/search/metadata", map[string]any{
				"libraryId":        s.cfg.LibraryID,
				"originalFileName": q.Text,
				"order":            "desc",
				"isOffline":        false,
				"withExif":         true,
			}, limit)
		}
	}
	if err != nil {
		return nil, err
	}

	items := make([]media.Item, 0, len(found))
	for _, a := range found {
		// A file deleted from the folder is marked offline, then trashed on
		// the next scan. Either way there is nothing left to show.
		if a.IsOffline || a.IsTrashed {
			continue
		}
		items = append(items, s.item(a, len(items)))
	}
	return items, nil
}

// page asks for up to limit results, a page of at most maxPage at a time.
func (s *Source) page(ctx context.Context, path string, body map[string]any, limit int) ([]asset, error) {
	var out []asset
	for page := 1; len(out) < limit; page++ {
		size := min(limit-len(out), maxPage)
		req := map[string]any{"page": page, "size": size}
		for k, v := range body {
			req[k] = v
		}
		resp, err := s.http.Do(ctx, httpx.Request{Method: http.MethodPost, Path: path, Body: req})
		if err != nil {
			return nil, fmt.Errorf("immich %q: %w", s.id, err)
		}
		if err := resp.Err(); err != nil {
			return nil, fmt.Errorf("immich %q %s: %w", s.id, path, err)
		}
		var r searchResponse
		if err := resp.JSON(&r); err != nil {
			return nil, err
		}
		out = append(out, r.Assets.Items...)
		if r.Assets.NextPage == nil || *r.Assets.NextPage == "" || len(r.Assets.Items) < size {
			break
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// item converts one asset. rank is its position in Immich's own order.
func (s *Source) item(a asset, rank int) media.Item {
	it := media.Item{
		ID:       a.ID,
		SourceID: s.id,
		Kind:     media.KindPicture,
		Title:    a.OriginalFileName,
		ArtID:    a.ID,
		// "~" sorts after every letter and digit, so in a merged browse of
		// everything the photos follow the titled media rather than
		// interleaving with it; the number keeps Immich's order.
		SortKey: fmt.Sprintf("~%09d", rank),
		Extra: map[string]string{
			"type": strings.ToLower(a.Type),
		},
	}
	if t, err := time.Parse("2006-01-02T15:04:05.000Z", a.LocalDateTime); err == nil {
		it.Year = t.Year()
		it.Subtitle = t.Format("2 Jan 2006")
		it.Extra["taken"] = t.Format("2006-01-02T15:04:05")
	}
	if a.ExifInfo != nil {
		place := strings.Trim(strings.Join([]string{a.ExifInfo.City, a.ExifInfo.Country}, ", "), ", ")
		if place != "" {
			it.Extra["place"] = place
		}
	}
	if a.Width > 0 && a.Height > 0 {
		it.Extra["width"] = fmt.Sprint(a.Width)
		it.Extra["height"] = fmt.Sprint(a.Height)
	}
	if a.Duration != nil && *a.Duration > 0 {
		it.DurationSeconds = float64(*a.Duration) / 1000
	}
	return it
}

// StreamTarget is the original file for a photo - offered as a download,
// since a browser cannot show a HEIC or a raw file - and a playable video for
// a clip, which Immich transcodes when the original will not play.
func (s *Source) StreamTarget(ctx context.Context, itemID string) (source.Target, error) {
	if itemID == "" {
		return source.Target{}, fmt.Errorf("immich %q: empty item id", s.id)
	}
	var a asset
	if err := s.http.JSON(ctx, "/api/assets/"+url.PathEscape(itemID), nil, &a); err != nil {
		return source.Target{}, err
	}
	path := "/api/assets/" + url.PathEscape(itemID) + "/original"
	if a.Type == "VIDEO" {
		path = "/api/assets/" + url.PathEscape(itemID) + "/video/playback"
	}
	return source.Target{
		URL:     s.http.URL(path, nil),
		Headers: map[string]string{"x-api-key": s.cfg.APIKey},
	}, nil
}

// PreviewSuffix asks ArtTarget for the large rendition rather than the grid
// thumbnail: "<id>@preview". It is how a photo is actually looked at, since
// the original may be a HEIC or a raw file no browser can decode.
const PreviewSuffix = "@preview"

// ArtTarget is the grid thumbnail, or with PreviewSuffix the large preview.
// Both are JPEG or WebP whatever the original was.
func (s *Source) ArtTarget(_ context.Context, artID string) (source.Target, error) {
	id, preview := strings.CutSuffix(artID, PreviewSuffix)
	if id == "" {
		return source.Target{}, fmt.Errorf("immich %q: empty art id", s.id)
	}
	size := "thumbnail"
	if preview {
		size = "preview"
	}
	return source.Target{
		URL:     s.http.URL("/api/assets/"+url.PathEscape(id)+"/thumbnail", url.Values{"size": {size}}),
		Headers: map[string]string{"x-api-key": s.cfg.APIKey},
	}, nil
}

// Rescan asks Immich to look at the pictures folder now.
func (s *Source) Rescan(ctx context.Context) error {
	resp, err := s.http.Do(ctx, httpx.Request{
		Method: http.MethodPost,
		Path:   "/api/libraries/" + url.PathEscape(s.cfg.LibraryID) + "/scan",
	})
	if err != nil {
		return fmt.Errorf("immich %q: scan: %w", s.id, err)
	}
	return resp.Err()
}

// Health checks the key still works and the library is still there.
func (s *Source) Health(ctx context.Context) error {
	var lib struct {
		ID string `json:"id"`
	}
	if err := s.http.JSON(ctx, "/api/libraries/"+url.PathEscape(s.cfg.LibraryID), nil, &lib); err != nil {
		return err
	}
	if lib.ID != s.cfg.LibraryID {
		return fmt.Errorf("library %q not found on this server", s.cfg.LibraryID)
	}
	return nil
}

// ItemFiles is the photo or clip's original file. Only for an asset in the
// external library SoundStorm made: anything uploaded to Immich some other
// way lives in Immich's own storage, which is not the pictures folder and not
// SoundStorm's to delete.
func (s *Source) ItemFiles(ctx context.Context, itemID string) ([]string, error) {
	if s.cfg.MediaRoot == "" {
		return nil, fmt.Errorf("immich %q: no media folder configured", s.id)
	}
	if itemID == "" {
		return nil, fmt.Errorf("immich %q: empty item id", s.id)
	}
	var a asset
	if err := s.http.JSON(ctx, "/api/assets/"+url.PathEscape(itemID), nil, &a); err != nil {
		return nil, err
	}
	if a.LibraryID != "" && a.LibraryID != s.cfg.LibraryID {
		return nil, fmt.Errorf("immich %q: %q is not in the pictures folder", s.id, itemID)
	}
	rel, err := source.RelativeTo(s.cfg.MediaRoot, a.OriginalPath)
	if err != nil {
		return nil, fmt.Errorf("immich %q: %w", s.id, err)
	}
	return []string{rel}, nil
}

// ItemByID describes one photo or clip, for a favourite, which knows it only
// by id. Its sort key is left empty: a favourites list is ordered by when
// something was added, not by Immich's timeline.
func (s *Source) ItemByID(ctx context.Context, itemID string) (media.Item, bool) {
	if itemID == "" {
		return media.Item{}, false
	}
	var a asset
	if err := s.http.JSON(ctx, "/api/assets/"+url.PathEscape(itemID), nil, &a); err != nil {
		return media.Item{}, false
	}
	if a.IsTrashed || a.IsOffline {
		return media.Item{}, false
	}
	it := s.item(a, 0)
	it.SortKey = ""
	return it, true
}

// Recent is the newest photos - Immich's own browsing order already.
func (s *Source) Recent(ctx context.Context, limit int) ([]media.Item, error) {
	return s.Search(ctx, media.Query{Limit: limit})
}
