// Package jellyfin adapts a Jellyfin server for video.
//
// Jellyfin is here for what it is genuinely best at: video,
// hardware-accelerated transcoding and metadata for film and television.
// SoundStorm does not use its music support - Navidrome is better at that - and
// never exposes its web UI, because that would be the second login this project
// exists to remove.
//
// Auth is an access token obtained by internal/provision logging in as the
// account it created during Jellyfin's startup wizard. Nobody visits the
// Jellyfin dashboard to mint an API key.
package jellyfin

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gabehollberg/soundstorm/internal/httpx"
	"github.com/gabehollberg/soundstorm/internal/media"
	"github.com/gabehollberg/soundstorm/internal/source"
)

// ticksPerSecond is Jellyfin's RunTimeTicks unit: 100-nanosecond intervals.
const ticksPerSecond = 10_000_000

// Config configures a Jellyfin source.
//
// Kind and ItemTypes are what let one Jellyfin server appear as two sources:
// films and series live in separate folders, separate Jellyfin libraries and
// separate result kinds, but share one set of credentials. The Source interface
// has always said "a server that serves two kinds is configured as two
// sources"; until this existed, that was not actually possible.
type Config struct {
	ID      string
	BaseURL string
	Token   string // access token from provisioning
	UserID  string // the account SoundStorm created for itself
	Timeout time.Duration

	// Kind is what this source reports its results as. Defaults to video.
	Kind media.Kind

	// ItemTypes is Jellyfin's IncludeItemTypes filter. Defaults to films.
	ItemTypes string
}

// Source is a Jellyfin server serving video.
type Source struct {
	id        string
	cfg       Config
	kind      media.Kind
	itemTypes string
	http      *httpx.Client
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

	kind := cfg.Kind
	if kind == "" {
		kind = media.KindVideo
	}
	itemTypes := cfg.ItemTypes
	if itemTypes == "" {
		itemTypes = "Movie"
	}
	return &Source{id: cfg.ID, cfg: cfg, kind: kind, itemTypes: itemTypes, http: c}, nil
}

func (s *Source) ID() string       { return s.id }
func (s *Source) Kind() media.Kind { return s.kind }

type itemsResponse struct {
	Items            []jfItem `json:"Items"`
	TotalRecordCount int      `json:"TotalRecordCount"`
}

type jfItem struct {
	ID                string            `json:"Id"`
	Name              string            `json:"Name"`
	Type              string            `json:"Type"`
	Overview          string            `json:"Overview"`
	ProductionYear    int               `json:"ProductionYear"`
	RunTimeTicks      int64             `json:"RunTimeTicks"`
	SeriesName        string            `json:"SeriesName"`
	IndexNumber       *int              `json:"IndexNumber"`       // episode within season
	ParentIndexNumber *int              `json:"ParentIndexNumber"` // season
	ImageTags         map[string]string `json:"ImageTags"`
	CommunityRating   float64           `json:"CommunityRating"`
}

func (s *Source) searchParams(q media.Query) url.Values {
	p := url.Values{
		"searchTerm":       {q.Text},
		"Recursive":        {"true"},
		"IncludeItemTypes": {s.itemTypes},
		"Limit":            {strconv.Itoa(q.LimitOr(25))},
		"Fields":           {"Overview,ProductionYear,ParentIndexNumber,IndexNumber"},
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
			Kind:     s.kind,
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
		// "S01E04" is how people refer to an episode, and without it an episode
		// row is just a filename. Jellyfin only fills these in for episodes.
		if it.ParentIndexNumber != nil && it.IndexNumber != nil {
			item.Extra["episode"] = fmt.Sprintf("S%02dE%02d", *it.ParentIndexNumber, *it.IndexNumber)
		}
		items = append(items, item)
	}
	return items, nil
}

// StreamTarget builds an authenticated upstream target for a video.
//
// It asks Jellyfin what to do rather than assuming. Handing over the original
// file works for h264/aac in mp4 and fails silently for everything else a real
// library contains - HEVC, MKV, DTS, TrueHD - where the video element simply
// displays nothing. Which of those applies depends on container, video codec,
// audio codec, profile and level, and Jellyfin already knows all five.
//
// The credential goes in a header, not the query string. Jellyfin 12 removed
// the api_key query parameter that most guides on the internet still show.
func (s *Source) StreamTarget(ctx context.Context, itemID string) (source.Target, error) {
	if itemID == "" {
		return source.Target{}, fmt.Errorf("jellyfin %q: empty item id", s.id)
	}

	play, err := s.negotiate(ctx, itemID)
	if err != nil {
		return source.Target{}, err
	}

	target := source.Target{
		URL:     s.http.URL(play.Ref, nil),
		Headers: map[string]string{"Authorization": authHeader(s.cfg.Token)},
	}
	if play.Transcoding {
		session := play.PlaySessionID
		target.OnDone = func() { s.stopTranscode(session) }
	}
	return target, nil
}

// --- playback negotiation ----------------------------------------------------

// maxStreamingBitrate caps what Jellyfin will transcode to. 20 Mbit is
// generous for a home network and well above what a browser needs for 1080p.
const maxStreamingBitrate = 20_000_000

// deviceProfile tells Jellyfin what this browser can decode.
//
// The list is deliberately conservative. Claiming a codec we cannot actually
// play is the worse failure of the two: Jellyfin hands over the original file,
// the video element refuses it, and nothing appears - silently, which is
// exactly the bug this whole mechanism exists to fix. Claiming too little just
// means an unnecessary transcode, which is slow but works.
//
// So: h264 in mp4 with aac or mp3, and the VPx/AV1 family in webm. Everything
// else - HEVC, MKV, DTS, TrueHD, VC-1 - gets transcoded.
func deviceProfile() map[string]any {
	return map[string]any{
		"MaxStreamingBitrate": maxStreamingBitrate,
		"MaxStaticBitrate":    maxStreamingBitrate,
		"DirectPlayProfiles": []map[string]any{
			{"Type": "Video", "Container": "mp4,m4v", "VideoCodec": "h264", "AudioCodec": "aac,mp3"},
			{"Type": "Video", "Container": "webm", "VideoCodec": "vp8,vp9,av1", "AudioCodec": "vorbis,opus"},
			{"Type": "Audio", "Container": "mp3"},
			{"Type": "Audio", "Container": "aac"},
		},
		"TranscodingProfiles": []map[string]any{
			{
				"Type":       "Video",
				"Container":  "mp4",
				"VideoCodec": "h264",
				"AudioCodec": "aac",
				// "http" rather than "hls": a progressive fragmented MP4 plays
				// in a plain <video> element, where HLS would need hls.js
				// vendored into the UI for every browser except Safari.
				"Protocol": "http",
				"Context":  "Streaming",
			},
		},
		"CodecProfiles": []map[string]any{
			{
				"Type":  "Video",
				"Codec": "h264",
				"Conditions": []map[string]any{
					// Levels above 5.1 turn up in files browsers choke on.
					{"Condition": "LessThanEqual", "Property": "VideoLevel", "Value": "51", "IsRequired": false},
					{"Condition": "EqualsAny", "Property": "VideoProfile",
						"Value": "high|main|baseline|constrained baseline", "IsRequired": false},
				},
			},
		},
	}
}

// playback is Jellyfin's answer to "how do I play this".
type playback struct {
	// Ref is the path and query to fetch, relative to the server root.
	Ref string

	// Transcoding is true when Jellyfin will re-encode rather than hand over
	// the original file.
	Transcoding bool

	// PlaySessionID identifies the transcode so it can be stopped.
	PlaySessionID string
}

type playbackInfoResponse struct {
	MediaSources []struct {
		ID                     string `json:"Id"`
		Container              string `json:"Container"`
		SupportsDirectPlay     bool   `json:"SupportsDirectPlay"`
		SupportsDirectStream   bool   `json:"SupportsDirectStream"`
		SupportsTranscoding    bool   `json:"SupportsTranscoding"`
		TranscodingURL         string `json:"TranscodingUrl"`
		TranscodingSubProtocol string `json:"TranscodingSubProtocol"`
	} `json:"MediaSources"`
	PlaySessionID string `json:"PlaySessionId"`
}

// negotiate asks Jellyfin whether this item can be played as-is.
//
// This is the difference between "plays some of your films" and "plays your
// films". Guessing is not an option: whether a file needs transcoding depends
// on its container, video codec, audio codec, profile and level, and Jellyfin
// is the thing that already knows all five.
func (s *Source) negotiate(ctx context.Context, itemID string) (playback, error) {
	params := url.Values{}
	if s.cfg.UserID != "" {
		params.Set("userId", s.cfg.UserID)
	}

	resp, err := s.http.Do(ctx, httpx.Request{
		Method: http.MethodPost,
		Path:   "/Items/" + url.PathEscape(itemID) + "/PlaybackInfo",
		Params: params,
		Body: map[string]any{
			"DeviceProfile":       deviceProfile(),
			"MaxStreamingBitrate": maxStreamingBitrate,
			"StartTimeTicks":      0,
			"AutoOpenLiveStream":  true,
		},
	})
	if err != nil {
		return playback{}, fmt.Errorf("jellyfin %q: playback info: %w", s.id, err)
	}
	if err := resp.Err(); err != nil {
		return playback{}, fmt.Errorf("jellyfin %q: playback info: %w", s.id, err)
	}

	var info playbackInfoResponse
	if err := resp.JSON(&info); err != nil {
		return playback{}, err
	}
	if len(info.MediaSources) == 0 {
		return playback{}, fmt.Errorf("jellyfin %q: item %q has no media sources", s.id, itemID)
	}
	ms := info.MediaSources[0]

	if ms.SupportsDirectPlay || ms.SupportsDirectStream {
		return playback{
			Ref: "/Videos/" + url.PathEscape(itemID) + "/stream?" + url.Values{
				"static":        {"true"},
				"mediaSourceId": {ms.ID},
			}.Encode(),
			PlaySessionID: info.PlaySessionID,
		}, nil
	}

	if ms.TranscodingURL == "" {
		return playback{}, fmt.Errorf(
			"jellyfin %q: %s cannot be direct played and offers no transcode", s.id, ms.Container)
	}
	// A profile asking for Protocol "http" should never produce an HLS answer,
	// but if Jellyfin ever decides otherwise the failure should be legible
	// rather than a video element silently refusing a playlist.
	if strings.EqualFold(ms.TranscodingSubProtocol, "hls") {
		return playback{}, fmt.Errorf(
			"jellyfin %q: returned an HLS stream, which this player cannot use", s.id)
	}

	return playback{
		Ref:           ms.TranscodingURL,
		Transcoding:   true,
		PlaySessionID: info.PlaySessionID,
	}, nil
}

// stopTranscode tells Jellyfin to kill the ffmpeg process it started for us.
//
// Without this every abandoned playback leaves an encoder running until
// Jellyfin times it out, which on a home server is the difference between an
// idle machine and a hot one.
func (s *Source) stopTranscode(playSessionID string) {
	if playSessionID == "" {
		return
	}
	// The viewer has already gone, so this cleanup gets its own short budget
	// rather than inheriting a context that is already cancelled.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, _ = s.http.Do(ctx, httpx.Request{
		Method: http.MethodDelete,
		Path:   "/Videos/ActiveEncodings",
		Params: url.Values{
			"deviceId":      {deviceID},
			"playSessionId": {playSessionID},
		},
	})
}

// ArtTarget builds an authenticated upstream target for a poster.
func (s *Source) ArtTarget(_ context.Context, artID string) (source.Target, error) {
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

// deviceID identifies this gateway to Jellyfin. It ties a transcode session
// to us, which is what makes it stoppable.
const deviceID = "soundstorm-gateway"

// authHeader builds the only credential form Jellyfin 12 accepts.
//
// Verified against a live Jellyfin 12.1.0: X-Emby-Token returns 401, so does
// ?api_key=. Both appear in most documentation and in every older client, which
// is exactly why this is a named function with this comment attached rather
// than an inline string - the next person to hit a 401 here should find the
// answer.
func authHeader(token string) string {
	return `MediaBrowser Client="soundstorm", Device="soundstorm", DeviceId="` + deviceID + `", Version="0.1.0", Token="` + token + `"`
}
