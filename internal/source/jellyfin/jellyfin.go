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

	"github.com/GabrielHollberg/soundstorm/internal/httpx"
	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/source"
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
		"Recursive":        {"true"},
		"IncludeItemTypes": {s.itemTypes},
		"Limit":            {strconv.Itoa(q.LimitOr(25))},
		"Fields":           {"Overview,ProductionYear,ParentIndexNumber,IndexNumber"},
	}
	// Omitted entirely rather than sent empty. /Items with no searchTerm is
	// Jellyfin's own browse: it returns the library in order. Sending
	// searchTerm= would be asking it to match the empty string, which is a
	// different question and not one it answers usefully.
	if q.Text != "" {
		p.Set("searchTerm", q.Text)
	} else {
		// Nothing to rank a browse by, so name order - and SortName is the
		// field Jellyfin itself sorts on, which strips a leading "The".
		p.Set("SortBy", "SortName")
		p.Set("SortOrder", "Ascending")
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

// StreamTarget hands over the original file.
//
// This is only ever reached for video the browser can already decode, because
// Playback decides that first and routes anything else to HLS. Direct play is
// worth keeping: it is instant, costs the server nothing, and seeks by byte
// range like any other file.
//
// The credential goes in a header, not the query string. Jellyfin 12 removed
// the api_key query parameter that most guides on the internet still show.
func (s *Source) StreamTarget(_ context.Context, itemID string) (source.Target, error) {
	if itemID == "" {
		return source.Target{}, fmt.Errorf("jellyfin %q: empty item id", s.id)
	}
	return source.Target{
		URL: s.http.URL("/Videos/"+url.PathEscape(itemID)+"/stream", url.Values{
			"static": {"true"},
		}),
		Headers: map[string]string{"Authorization": authHeader(s.cfg.Token)},
	}, nil
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
// else - HEVC, MKV, DTS, TrueHD, VC-1 - gets transcoded to HLS.
// directPlayContainers is the containers we claim to handle untouched. It is
// referenced twice on purpose: once to tell Jellyfin, and once to check its
// answer.
const directPlayContainers = "mp4,m4v,webm"

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
				"Container":  "ts",
				"VideoCodec": "h264",
				"AudioCodec": "aac",
				"Protocol":   "hls",
				"Context":    "Streaming",
			},
		},
		// Text subtitles arrive as a separate WebVTT file rather than being
		// burned into the video, so turning them on costs no re-encode.
		"SubtitleProfiles": []map[string]any{
			{"Format": "vtt", "Method": "External"},
			{"Format": "subrip", "Method": "External"},
			{"Format": "ass", "Method": "External"},
			{"Format": "ssa", "Method": "External"},
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

// playbackInfoResponse is the part of PlaybackInfo we act on.
type playbackInfoResponse struct {
	MediaSources []struct {
		ID                   string        `json:"Id"`
		Container            string        `json:"Container"`
		SupportsDirectPlay   bool          `json:"SupportsDirectPlay"`
		SupportsDirectStream bool          `json:"SupportsDirectStream"`
		SupportsTranscoding  bool          `json:"SupportsTranscoding"`
		MediaStreams         []mediaStream `json:"MediaStreams"`
	} `json:"MediaSources"`
	PlaySessionID string `json:"PlaySessionId"`
}

type mediaStream struct {
	Index                int    `json:"Index"`
	Type                 string `json:"Type"`
	Codec                string `json:"Codec"`
	Language             string `json:"Language"`
	DisplayTitle         string `json:"DisplayTitle"`
	Title                string `json:"Title"`
	IsForced             bool   `json:"IsForced"`
	IsTextSubtitleStream bool   `json:"IsTextSubtitleStream"`
}

// textSubtitleCodecs are the ones that can become WebVTT.
//
// Everything else a file might carry - PGS, VOBSUB, DVB - is a picture of
// text, not text, and the only way to show it in a browser is to burn it into
// the video. Offering a track that silently renders nothing would be worse
// than not offering it.
var textSubtitleCodecs = map[string]bool{
	"subrip": true, "srt": true, "ass": true, "ssa": true,
	"webvtt": true, "vtt": true, "mov_text": true, "text": true,
	"microdvd": true, "subviewer": true,
}

// iso639 maps the three-letter codes backends report onto the two-letter tags
// a track element wants. Anything unlisted is passed through unchanged, which
// is no worse than the alternative.
var iso639 = map[string]string{
	"eng": "en", "spa": "es", "fra": "fr", "fre": "fr", "deu": "de", "ger": "de",
	"ita": "it", "por": "pt", "rus": "ru", "jpn": "ja", "kor": "ko",
	"zho": "zh", "chi": "zh", "ara": "ar", "hin": "hi", "nld": "nl", "dut": "nl",
	"swe": "sv", "nor": "no", "dan": "da", "fin": "fi", "pol": "pl",
	"tur": "tr", "ces": "cs", "cze": "cs", "ell": "el", "gre": "el",
	"heb": "he", "tha": "th", "vie": "vi", "ukr": "uk", "hun": "hu", "ron": "ro",
}

// subtitleTracks turns Jellyfin's stream list into offerable tracks.
func (s *Source) subtitleTracks(itemID, mediaSourceID string, streams []mediaStream) []source.SubtitleTrack {
	var tracks []source.SubtitleTrack
	for _, st := range streams {
		if !strings.EqualFold(st.Type, "Subtitle") {
			continue
		}
		if !st.IsTextSubtitleStream && !textSubtitleCodecs[strings.ToLower(st.Codec)] {
			continue
		}

		label := firstNonEmpty(st.Title, cleanDisplayTitle(st.DisplayTitle), st.Language, "Subtitles")
		if st.IsForced && !strings.Contains(strings.ToLower(label), "forced") {
			label += " (forced)"
		}
		language := strings.ToLower(st.Language)
		if mapped, ok := iso639[language]; ok {
			language = mapped
		}

		tracks = append(tracks, source.SubtitleTrack{
			ID:       fmt.Sprintf("%s/%s/%d", itemID, mediaSourceID, st.Index),
			Label:    label,
			Language: language,
			Forced:   st.IsForced,
		})
	}
	return tracks
}

// SubtitleTarget fetches one track, converted to WebVTT by Jellyfin.
//
// The URL is built rather than taken from PlaybackInfo's DeliveryUrl, which
// 12.1.0 leaves empty even when a subtitle profile is supplied. The endpoint
// itself works for embedded and sidecar tracks alike.
// Rescan asks Jellyfin to look at its libraries now.
//
// POST /Library/Refresh answers 204 and refreshes every library, not just the
// one this source reads - Jellyfin has no per-library trigger that does not
// need the library's own id, and the two Jellyfin sources share a server
// anyway. Verified against 12.1.0, where a made-up path under /Library answers
// 404, so the 204 means something.
func (s *Source) Rescan(ctx context.Context) error {
	resp, err := s.http.Do(ctx, httpx.Request{
		Method:  http.MethodPost,
		Path:    "/Library/Refresh",
		Headers: map[string]string{"Authorization": authHeader(s.cfg.Token)},
	})
	if err != nil {
		return fmt.Errorf("jellyfin %q: refresh: %w", s.id, err)
	}
	return resp.Err()
}

func (s *Source) SubtitleTarget(_ context.Context, trackID string) (source.Target, error) {
	parts := strings.Split(trackID, "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return source.Target{}, fmt.Errorf("jellyfin %q: bad subtitle track %q", s.id, trackID)
	}
	if _, err := strconv.Atoi(parts[2]); err != nil {
		return source.Target{}, fmt.Errorf("jellyfin %q: subtitle index %q is not a number", s.id, parts[2])
	}

	ref := "/Videos/" + url.PathEscape(parts[0]) +
		"/" + url.PathEscape(parts[1]) +
		"/Subtitles/" + url.PathEscape(parts[2]) + "/Stream.vtt"

	return source.Target{
		URL:     s.http.URL(ref, nil),
		Headers: map[string]string{"Authorization": authHeader(s.cfg.Token)},
	}, nil
}

// cleanDisplayTitle trims the machinery off Jellyfin's generated label.
//
// When a stream has no title of its own Jellyfin invents one like
// "English - SUBRIP - External", which describes the plumbing rather than the
// track. The leading segment is the part a person wants.
func cleanDisplayTitle(title string) string {
	if before, _, found := strings.Cut(title, " - "); found {
		return strings.TrimSpace(before)
	}
	return strings.TrimSpace(title)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// Playback decides whether this item can be handed over as a file or has to be
// transcoded into a playlist.
//
// Transcoding is served as HLS rather than a progressive stream, and the reason
// is seeking. A progressive transcode has no length until it has finished
// encoding, so the browser reports seekable.end of 0 and the scrubber does
// nothing - measured, not assumed. Jellyfin's HLS output is a VOD playlist: it
// lists every segment and its duration up front, which gives the browser a real
// timeline and makes seeking work the way it does for any other video.
func (s *Source) Playback(ctx context.Context, itemID string) (source.Playback, error) {
	if itemID == "" {
		return source.Playback{}, fmt.Errorf("jellyfin %q: empty item id", s.id)
	}

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
		return source.Playback{}, fmt.Errorf("jellyfin %q: playback info: %w", s.id, err)
	}
	if err := resp.Err(); err != nil {
		return source.Playback{}, fmt.Errorf("jellyfin %q: playback info: %w", s.id, err)
	}

	var info playbackInfoResponse
	if err := resp.JSON(&info); err != nil {
		return source.Playback{}, err
	}
	if len(info.MediaSources) == 0 {
		return source.Playback{}, fmt.Errorf("jellyfin %q: item %q has no media sources", s.id, itemID)
	}
	ms := info.MediaSources[0]

	// Offered in both modes: a direct-played file can have a sidecar, and a
	// transcode can carry embedded tracks.
	subtitles := s.subtitleTracks(itemID, ms.ID, ms.MediaStreams)

	if (ms.SupportsDirectPlay || ms.SupportsDirectStream) && s.browserCanPlay(ms.Container) {
		return source.Playback{Mode: source.PlaybackModeDirect, Subtitles: subtitles}, nil
	}

	return source.Playback{
		Mode:      source.PlaybackModeHLS,
		Path:      itemID + "/master.m3u8",
		Query:     s.hlsParams(itemID, ms.ID),
		Subtitles: subtitles,
	}, nil
}

// browserCanPlay is a second opinion on Jellyfin's direct-play answer.
//
// Jellyfin has been observed reporting SupportsDirectPlay for an MKV even when
// the device profile lists only mp4 - verified against 12.1.0. Trusting it
// alone reproduces the silent failure this whole mechanism exists to remove, so
// the container is checked against the same list the profile advertises.
func (s *Source) browserCanPlay(container string) bool {
	for _, ok := range strings.Split(directPlayContainers, ",") {
		if strings.EqualFold(strings.TrimSpace(ok), container) {
			return true
		}
	}
	return false
}

// hlsParams builds the query Jellyfin needs to produce a playlist.
func (s *Source) hlsParams(itemID, mediaSourceID string) url.Values {
	if mediaSourceID == "" {
		mediaSourceID = itemID
	}
	return url.Values{
		"mediaSourceId":        {mediaSourceID},
		"deviceId":             {deviceID},
		"videoCodec":           {"h264"},
		"audioCodec":           {"aac"},
		"transcodingContainer": {"ts"},
		"transcodingProtocol":  {"hls"},
		"segmentContainer":     {"ts"},
		"videoBitRate":         {strconv.Itoa(maxStreamingBitrate)},
		"maxAudioChannels":     {"2"},
	}
}

// HLSTarget maps a playlist or segment path onto Jellyfin's video namespace.
//
// Jellyfin's playlists reference their children relatively ("main.m3u8",
// "hls1/main/0.ts"), so as long as the client loads the master from a URL whose
// directory mirrors this namespace, every follow-up request lands here with the
// right path and no playlist rewriting is needed.
func (s *Source) HLSTarget(_ context.Context, path string, query url.Values) (source.Target, error) {
	clean := strings.TrimPrefix(path, "/")
	if clean == "" || strings.Contains(clean, "..") {
		return source.Target{}, fmt.Errorf("jellyfin %q: bad hls path %q", s.id, path)
	}
	return source.Target{
		URL:     s.http.URL("/videos/"+clean, query),
		Headers: map[string]string{"Authorization": authHeader(s.cfg.Token)},
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
