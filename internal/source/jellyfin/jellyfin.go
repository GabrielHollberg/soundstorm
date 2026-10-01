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
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
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

	// MediaRoot is this source's folder as Jellyfin's container sees it
	// ("/media/movies"), which is how Jellyfin reports an item's path. Empty
	// means this source cannot name its files, so its items cannot be deleted.
	MediaRoot string
}

// Source is a Jellyfin server serving video.
type Source struct {
	id        string
	cfg       Config
	kind      media.Kind
	itemTypes string
	http      *httpx.Client
	owned     ownedCache
	shelf     media.ShelfCache
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
	SeriesID          string            `json:"SeriesId"`
	IndexNumber       *int              `json:"IndexNumber"`       // episode within season
	ParentIndexNumber *int              `json:"ParentIndexNumber"` // season
	ImageTags         map[string]string `json:"ImageTags"`
	CommunityRating   float64           `json:"CommunityRating"`
	Genres            []string          `json:"Genres"`
}

func (s *Source) searchParams(q media.Query) url.Values {
	p := url.Values{
		"Recursive":        {"true"},
		"IncludeItemTypes": {s.itemTypes},
		"Limit":            {strconv.Itoa(q.LimitOr(25))},
		"Fields":           {"Overview,ProductionYear,ParentIndexNumber,IndexNumber,Genres"},

		// Jellyfin can hold episodes that have no file: with a user's
		// "display missing episodes" preference on, it manufactures one per gap
		// from the series metadata. They look like ordinary results and there is
		// nothing behind them to play. SoundStorm creates its own Jellyfin
		// account and never turns that on, so this is insurance rather than a
		// fix - but it costs one parameter, and the two sibling backends both
		// turned out to hand over items for deleted files.
		//
		// IsMissing, and not IsVirtualItem, which looks like the more general
		// answer and is silently ignored: on 12.1.0 `IsVirtualItem=true`
		// returned a real film, exactly as a parameter name invented for the
		// test did. `IsMissing=true` returned nothing for the same film, and
		// `IsMissing=false` kept the film, the episode and the series - which is
		// the case that matters, because a Series has no file of its own and a
		// filter that dropped it would empty the television shelf.
		"IsMissing": {"false"},
	}
	// Omitted entirely rather than sent empty. /Items with no searchTerm is
	// Jellyfin's own browse: it returns the library in order. Sending
	// searchTerm= would be asking it to match the empty string, which is a
	// different question and not one it answers usefully.
	if q.Text != "" {
		p.Set("searchTerm", q.Text)
	} else {
		// Browsing television is browsing shows: their episodes are on each
		// show's own page, in order, not scattered among the shows by title.
		// A search still finds an episode by name.
		if strings.Contains(s.itemTypes, "Series") {
			p.Set("IncludeItemTypes", "Series")
		}
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

// Search browses or searches one Jellyfin library.
//
// Whole listings, ordered here: Jellyfin's SortName drops a leading "The", so
// its first N by name are not the merge's first N by title, and "The Matrix"
// at a page boundary would repeat or vanish (see media.Less). A search is
// returned whole for the merge to rank. Listings are cached briefly so
// scrolling costs one fetch.
func (s *Source) Search(ctx context.Context, q media.Query) ([]media.Item, error) {
	all, err := s.shelf.GetOrFetch(q.Text, func() ([]media.Item, error) { return s.fetchAll(ctx, q) })
	if err != nil {
		return nil, err
	}
	if q.Text == "" {
		return media.FirstN(all, q.LimitOr(25)), nil
	}
	return all, nil
}

// itemsPage is how many items one /Items call asks for; maxItems bounds a
// whole listing.
const (
	itemsPage = 1000
	maxItems  = 100_000
)

func (s *Source) fetchAll(ctx context.Context, q media.Query) ([]media.Item, error) {
	var all []media.Item
	for start := 0; start < maxItems; start += itemsPage {
		params := s.searchParams(q)
		params.Set("Limit", strconv.Itoa(itemsPage))
		params.Set("StartIndex", strconv.Itoa(start))
		page, err := s.fetchPage(ctx, params)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if len(page) < itemsPage {
			break
		}
	}
	return all, nil
}

func (s *Source) fetchPage(ctx context.Context, params url.Values) ([]media.Item, error) {
	var resp itemsResponse
	if err := s.http.JSON(ctx, "/Items", params, &resp); err != nil {
		return nil, err
	}

	items := make([]media.Item, 0, len(resp.Items))
	for _, it := range resp.Items {
		items = append(items, s.toItem(it))
	}
	return items, nil
}

// toItem is one Jellyfin item as SoundStorm shows it.
func (s *Source) toItem(it jfItem) media.Item {
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
	if len(it.Genres) > 0 {
		item.Extra["genre"] = strings.Join(it.Genres, ", ")
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
	// An episode Jellyfin found nothing about is named after its file,
	// "Show - S01E02 - The Title": the title is the part worth showing.
	if it.Type == "Episode" {
		if m := fileEpisodeName.FindStringSubmatch(item.Title); m != nil {
			item.Title = m[1]
		}
	}
	return item
}

var fileEpisodeName = regexp.MustCompile(`(?i)^.*?\bS\d{1,3}E\d{1,4}\b\s*[-.]\s*(.+)$`)

// StreamTarget hands over the original file.
//
// This is only ever reached for video the browser can already decode, because
// Playback decides that first and routes anything else to HLS. Direct play is
// worth keeping: it is instant, costs the server nothing, and seeks by byte
// range like any other file.
//
// The credential goes in a header, not the query string. Jellyfin 12 removed
// the api_key query parameter that most guides on the internet still show.
func (s *Source) StreamTarget(ctx context.Context, itemID string) (source.Target, error) {
	if itemID == "" {
		return source.Target{}, fmt.Errorf("jellyfin %q: empty item id", s.id)
	}
	if err := s.owns(ctx, itemID); err != nil {
		return source.Target{}, err
	}
	return source.Target{
		URL: s.http.URL("/Videos/"+url.PathEscape(itemID)+"/stream", url.Values{
			"static": {"true"},
		}),
		Headers: map[string]string{"Authorization": authHeader(s.cfg.Token)},
	}, nil
}

// --- playback negotiation ----------------------------------------------------

// maxStreamingBitrate caps what Jellyfin will transcode to when nobody asked
// for a quality (source.VideoQuality, chosen per device): 20 Mbit, enough for
// 1080p.
const maxStreamingBitrate = 20_000_000

// quality is the film quality asked for, or the old default.
func quality(ctx context.Context) source.VideoQuality {
	q, ok := source.VideoQualityFrom(ctx)
	if !ok || q.MaxBitrate <= 0 {
		return source.VideoQuality{MaxBitrate: maxStreamingBitrate, MaxAudioChannels: 2}
	}
	if q.MaxAudioChannels <= 0 {
		q.MaxAudioChannels = 2
	}
	return q
}

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

func deviceProfile(bitrate int) map[string]any {
	return map[string]any{
		"MaxStreamingBitrate": bitrate,
		"MaxStaticBitrate":    bitrate,
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
		DefaultAudioIndex    *int          `json:"DefaultAudioStreamIndex"`
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
	Profile              string `json:"Profile"`
	Channels             int    `json:"Channels"`
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
	s.shelf.Clear()
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

func (s *Source) SubtitleTarget(ctx context.Context, trackID string) (source.Target, error) {
	parts := strings.Split(trackID, "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return source.Target{}, fmt.Errorf("jellyfin %q: bad subtitle track %q", s.id, trackID)
	}
	if _, err := strconv.Atoi(parts[2]); err != nil {
		return source.Target{}, fmt.Errorf("jellyfin %q: subtitle index %q is not a number", s.id, parts[2])
	}
	if err := s.owns(ctx, parts[0]); err != nil {
		return source.Target{}, err
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
	if err := s.owns(ctx, itemID); err != nil {
		return source.Playback{}, err
	}

	params := url.Values{}
	if s.cfg.UserID != "" {
		params.Set("userId", s.cfg.UserID)
	}
	q := quality(ctx)

	resp, err := s.http.Do(ctx, httpx.Request{
		Method: http.MethodPost,
		Path:   "/Items/" + url.PathEscape(itemID) + "/PlaybackInfo",
		Params: params,
		Body: map[string]any{
			"DeviceProfile":       deviceProfile(q.MaxBitrate),
			"MaxStreamingBitrate": q.MaxBitrate,
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
	audio := audioTracks(ms.MediaStreams, ms.DefaultAudioIndex)

	// Another language than the file's default is a stream Jellyfin picks
	// out for the browser: a browser plays only the first audio track of a
	// file it is handed whole.
	chosen, picked := source.AudioStreamFrom(ctx)
	if picked && !hasAudio(audio, chosen) {
		picked = false
	}
	if !picked && (ms.SupportsDirectPlay || ms.SupportsDirectStream) && s.browserCanPlay(ms.Container) {
		return source.Playback{Mode: source.PlaybackModeDirect, Subtitles: subtitles, AudioTracks: audio}, nil
	}

	query := s.hlsParams(itemID, ms.ID, q)
	if picked {
		query.Set("AudioStreamIndex", strconv.Itoa(chosen))
	}
	// Jellyfin names a conversion's files after the file, the device and the
	// play session only - not the quality or the audio track - and this
	// gateway is always one device, so without a session every play of a
	// film shared one conversion: a film first played at 20 Mbit went on
	// being served from those pieces after the quality changed (seen on the
	// owner's projector), and another language could get the old track's.
	// The session PlaybackInfo hands out is new each time.
	if info.PlaySessionID != "" {
		query.Set("playSessionId", info.PlaySessionID)
	}
	// Converted, a track with more channels than this device takes comes out
	// with fewer, and the picker says so rather than promising 7.1.
	audio = playsAs(audio, ms.MediaStreams, q.MaxAudioChannels)
	return source.Playback{
		Mode:        source.PlaybackModeHLS,
		Path:        itemID + "/master.m3u8",
		Query:       query,
		Subtitles:   subtitles,
		AudioTracks: audio,
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
//
// Under q's bitrate a picture the browser can play is copied, not re-encoded
// (Jellyfin's stream copy, on by default); sound goes to AAC with up to q's
// channels, so 5.1 stays 5.1 where the device can play it.
func (s *Source) hlsParams(itemID, mediaSourceID string, q source.VideoQuality) url.Values {
	if mediaSourceID == "" {
		mediaSourceID = itemID
	}
	v := url.Values{
		"mediaSourceId":        {mediaSourceID},
		"deviceId":             {deviceID},
		"videoCodec":           {"h264"},
		"audioCodec":           {"aac"},
		"transcodingContainer": {"ts"},
		"transcodingProtocol":  {"hls"},
		"segmentContainer":     {"ts"},
		"videoBitRate":         {strconv.Itoa(q.MaxBitrate)},
		"maxAudioChannels":     {strconv.Itoa(q.MaxAudioChannels)},
	}
	if q.MaxWidth > 0 {
		v.Set("maxWidth", strconv.Itoa(q.MaxWidth))
	}
	return v
}

// HLSTarget maps a playlist or segment path onto Jellyfin's video namespace.
//
// Jellyfin's playlists reference their children relatively ("main.m3u8",
// "hls1/main/0.ts"), so as long as the client loads the master from a URL whose
// directory mirrors this namespace, every follow-up request lands here with the
// right path and no playlist rewriting is needed.
//
// The path is matched against the handful of shapes Jellyfin's playlists
// actually use, not merely checked for "..". It is forwarded with SoundStorm's
// administrator token, so anything looser is a proxy onto the whole Jellyfin
// API: "%2e%2e/System/Info" passed a ".." check, reached Jellyfin as a dot
// segment, and answered 200. And the item has to be this source's, or a
// member refused films could watch one by asking the television source.
func (s *Source) HLSTarget(ctx context.Context, path string, query url.Values) (source.Target, error) {
	clean := strings.TrimPrefix(path, "/")
	m := hlsPath.FindStringSubmatch(clean)
	if m == nil {
		return source.Target{}, fmt.Errorf("jellyfin %q: bad hls path %q", s.id, path)
	}
	if err := s.owns(ctx, m[1]); err != nil {
		return source.Target{}, err
	}
	// The query is the client's, and it reaches Jellyfin with our
	// administrator token. SoundStorm never asks for burned-in or delivered
	// subtitles over HLS - subtitles are a separate VTT endpoint - so a
	// Subtitle* key here can only have been added by a member calling this
	// endpoint by hand. Left in, SubtitleMethod=Hls makes Jellyfin write a
	// subtitle playlist URL carrying that token into the master playlist,
	// which is then piped straight back to the member. Dropping every
	// Subtitle* key removes that without touching anything real playback
	// sends.
	query = withoutSubtitleKeys(query)
	// Trickplay (seek-preview tiles) is the same leak by another door: with
	// it on, which is Jellyfin's default, a master playlist for an item that
	// has tiles names them with ApiKey=<our token>. SoundStorm shows no
	// tiles, so it is always off.
	query = withTrickplayOff(query)
	return source.Target{
		URL:     s.http.URL("/videos/"+clean, query),
		Headers: map[string]string{"Authorization": authHeader(s.cfg.Token)},
	}, nil
}

// withTrickplayOff returns a copy of query asking for no trickplay, whatever
// the client sent (Jellyfin reads parameter names case-blind).
func withTrickplayOff(query url.Values) url.Values {
	out := url.Values{}
	for k, v := range query {
		if !strings.EqualFold(k, "enableTrickplay") {
			out[k] = v
		}
	}
	out.Set("enableTrickplay", "false")
	return out
}

// withoutSubtitleKeys returns query with every subtitle-delivery parameter
// removed. Returns the same value unchanged when there is nothing to strip, so
// the common case allocates nothing.
func withoutSubtitleKeys(query url.Values) url.Values {
	var out url.Values
	for k := range query {
		// Anywhere in the name, not only at its start: EnableSubtitlesInManifest
		// writes a subtitle playlist with our token into the master as surely
		// as SubtitleMethod=Hls does.
		if strings.Contains(strings.ToLower(k), "subtitle") {
			if out == nil {
				out = url.Values{}
				for k2, v2 := range query {
					out[k2] = v2
				}
			}
			delete(out, k)
		}
	}
	if out == nil {
		return query
	}
	return out
}

// hlsPath is every path a Jellyfin HLS playlist leads to: the master, the
// variant, and its segments - MPEG-TS, or fMP4 with its -1 init segment.
var hlsPath = regexp.MustCompile(`^([A-Za-z0-9-]{1,64})/(?:master\.m3u8|main\.m3u8|hls1/[A-Za-z0-9_]{1,32}/-?[0-9]{1,9}\.(?:ts|mp4|m4s|aac))$`)

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
	// rather than inheriting a context that is already canceled.
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
func (s *Source) ArtTarget(ctx context.Context, artID string) (source.Target, error) {
	if artID == "" {
		return source.Target{}, fmt.Errorf("jellyfin %q: empty art id", s.id)
	}
	if err := s.owns(ctx, artID); err != nil {
		return source.Target{}, err
	}
	// At the width asked for (source.ArtSize), which Jellyfin resizes.
	var q url.Values
	if px := source.ArtSize(ctx); px > 0 {
		q = url.Values{"maxWidth": {strconv.Itoa(px)}, "quality": {"90"}}
	}
	return source.Target{
		URL:     s.http.URL("/Items/"+url.PathEscape(artID)+"/Images/Primary", q),
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

// Ownership. Films and television are two sources over one Jellyfin account,
// and an item id is just a Jellyfin id - so without this, anything addressed
// to one source could name an item of the other, and "no films for the
// seven-year-old" would be a hidden shelf with a working play button behind
// every film id. The id has to come back from a query restricted to this
// source's item types.

// ownedTTL is how long an answer is remembered. Every HLS segment asks, so
// this is what keeps a two-hour film from being two thousand lookups.
const ownedTTL = 10 * time.Minute

type ownedCache struct {
	mu sync.Mutex
	at map[string]time.Time
}

// errNotOwned is what a caller sees for an item of another source, and it is
// the same for one that does not exist at all.
var errNotOwned = errors.New("no such item")

func (s *Source) owns(ctx context.Context, itemID string) error {
	s.owned.mu.Lock()
	at, ok := s.owned.at[itemID]
	s.owned.mu.Unlock()
	if ok && time.Since(at) < ownedTTL {
		return nil
	}

	params := url.Values{
		"Ids":              {itemID},
		"Recursive":        {"true"},
		"IncludeItemTypes": {s.itemTypes},
		"Fields":           {"ParentId"},
		"Limit":            {"1"},
	}
	if s.cfg.UserID != "" {
		params.Set("userId", s.cfg.UserID)
	}
	var resp itemsResponse
	if err := s.http.JSON(ctx, "/Items", params, &resp); err != nil {
		return fmt.Errorf("jellyfin %q: look up item: %w", s.id, err)
	}
	// Checked, not assumed from the count: Jellyfin ignores a parameter it
	// does not understand, and an ignored Ids would hand back some other item.
	if len(resp.Items) == 0 || resp.Items[0].ID != itemID {
		return errNotOwned
	}

	s.owned.mu.Lock()
	if s.owned.at == nil || len(s.owned.at) > 4096 {
		s.owned.at = map[string]time.Time{}
	}
	s.owned.at[itemID] = time.Now()
	s.owned.mu.Unlock()
	return nil
}

// ItemFiles is the item's path relative to the shelf: a film's file, an
// episode's file, or a series' folder. Only for an item this source owns - the
// same check every other target makes, since one Jellyfin account serves both
// the film and the television source.
func (s *Source) ItemFiles(ctx context.Context, itemID string) ([]string, error) {
	if s.cfg.MediaRoot == "" {
		return nil, fmt.Errorf("jellyfin %q: no media folder configured", s.id)
	}
	if err := s.owns(ctx, itemID); err != nil {
		return nil, err
	}
	var item struct {
		Path string `json:"Path"`
	}
	path := "/Users/" + url.PathEscape(s.cfg.UserID) + "/Items/" + url.PathEscape(itemID)
	if err := s.http.JSON(ctx, path, url.Values{"Fields": {"Path"}}, &item); err != nil {
		return nil, fmt.Errorf("jellyfin %q: item path: %w", s.id, err)
	}
	rel, err := source.RelativeTo(s.cfg.MediaRoot, item.Path)
	if err != nil {
		return nil, fmt.Errorf("jellyfin %q: %w", s.id, err)
	}
	return []string{rel}, nil
}

// HasItem reports whether an id is one of this source's items - a film for the
// film source, an episode or series for the television one. It is what lets a
// watch position be kept for it: without the check the id is a free string,
// and every made-up one would be a new entry in the state file. Cached, like
// every other ownership check here.
func (s *Source) HasItem(ctx context.Context, itemID string) bool {
	return itemID != "" && s.owns(ctx, itemID) == nil
}

// ItemByID describes one item as a search would, for the Continue row, which
// knows a watched film only by the id its position was saved under.
func (s *Source) ItemByID(ctx context.Context, itemID string) (media.Item, bool) {
	if !s.HasItem(ctx, itemID) {
		return media.Item{}, false
	}
	params := s.searchParams(media.Query{})
	params.Del("SortBy")
	params.Set("Ids", itemID)
	items, err := s.fetchPage(ctx, params)
	if err != nil || len(items) == 0 {
		return media.Item{}, false
	}
	return items[0], true
}

// Recent is what arrived last, newest first. A series is ordered by when an
// episode was last added to it, so a new season brings the show forward.
func (s *Source) Recent(ctx context.Context, limit int) ([]media.Item, error) {
	params := s.searchParams(media.Query{})
	params.Set("SortOrder", "Descending")
	if s.kind == media.KindTV {
		params.Set("IncludeItemTypes", "Series")
		params.Set("SortBy", "DateLastContentAdded")
	} else {
		params.Set("SortBy", "DateCreated")
	}
	params.Set("Limit", strconv.Itoa(limit))
	return s.fetchPage(ctx, params)
}

// audioTracks lists a file's audio streams when there is a choice - one
// stream is no choice, and is not offered.
func audioTracks(streams []mediaStream, defaultIndex *int) []source.AudioTrack {
	var out []source.AudioTrack
	for _, st := range streams {
		if !strings.EqualFold(st.Type, "Audio") {
			continue
		}
		language := strings.ToLower(st.Language)
		if mapped, ok := iso639[language]; ok {
			language = mapped
		}
		out = append(out, source.AudioTrack{
			Index:    st.Index,
			Label:    audioLabel(st, len(out)+1),
			Language: language,
			Default:  defaultIndex != nil && *defaultIndex == st.Index,
		})
	}
	if len(out) < 2 {
		return nil
	}
	return out
}

// audioLabel names a track by what it is - language, format, channels - and
// adds the file's own name for it only when that says something more. A
// Blu-ray names its tracks "Surround 7.1", "Surround 5.1" and "Stereo", so the
// picker was a list of lookalikes, and "7.1" was what the disc called it, not
// what anybody heard (the owner: "it's lying to me").
func audioLabel(st mediaStream, n int) string {
	var parts []string
	if name := languageName(st.Language); name != "" {
		parts = append(parts, name)
	}
	format := strings.TrimSpace(strings.Join(nonEmpty(audioFormat(st.Codec, st.Profile), channelName(st.Channels)), " "))
	if format != "" {
		parts = append(parts, format)
	}
	label := strings.Join(parts, " · ")
	if t := strings.TrimSpace(st.Title); t != "" && !genericTrackTitle.MatchString(t) {
		if label == "" {
			return t
		}
		label += " - " + t
	}
	return firstNonEmpty(label, cleanDisplayTitle(st.DisplayTitle), fmt.Sprintf("Audio %d", n))
}

// genericTrackTitle is a track name that only repeats the channels or the
// format ("Surround 7.1", "Stereo", "DTS-HD MA 5.1"), which the label already
// says better.
var genericTrackTitle = regexp.MustCompile(`(?i)^\s*((surround|stereo|mono|dolby|digital|true ?hd|atmos|dts(-hd)?|ma|hra|ac-?3|e-?ac-?3|aac|flac|pcm|lpcm|plus|\+|[0-9.]+|ch|channels?)\s*)*$`)

func nonEmpty(values ...string) []string {
	var out []string
	for _, v := range values {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// audioFormat is a codec as people know it.
func audioFormat(codec, profile string) string {
	atmos := ""
	if strings.Contains(strings.ToLower(profile), "atmos") {
		atmos = " Atmos"
	}
	switch strings.ToLower(codec) {
	case "truehd":
		return "Dolby TrueHD" + atmos
	case "eac3":
		return "Dolby Digital Plus" + atmos
	case "ac3":
		return "Dolby Digital"
	case "dts":
		p := strings.ToLower(profile)
		switch {
		case strings.Contains(p, "dts:x") || strings.Contains(p, "dts-x"):
			return "DTS:X"
		case strings.Contains(p, "ma"):
			return "DTS-HD MA"
		case strings.Contains(p, "hra") || strings.Contains(p, "hd"):
			return "DTS-HD"
		}
		return "DTS"
	case "aac":
		return "AAC"
	case "flac":
		return "FLAC"
	case "mp3":
		return "MP3"
	case "opus":
		return "Opus"
	case "vorbis":
		return "Vorbis"
	}
	if strings.HasPrefix(strings.ToLower(codec), "pcm") {
		return "PCM"
	}
	return strings.ToUpper(codec)
}

// channelName is how many speakers a track is mixed for, as people say it.
func channelName(n int) string {
	switch {
	case n <= 0:
		return ""
	case n == 1:
		return "mono"
	case n == 2:
		return "stereo"
	case n == 6:
		return "5.1"
	case n == 7:
		return "6.1"
	case n == 8:
		return "7.1"
	}
	return fmt.Sprintf("%d channels", n)
}

// playsAs adds what a track will come out as when it is converted to fewer
// channels than it has.
func playsAs(tracks []source.AudioTrack, streams []mediaStream, maxChannels int) []source.AudioTrack {
	if maxChannels <= 0 {
		return tracks
	}
	channels := map[int]int{}
	for _, st := range streams {
		channels[st.Index] = st.Channels
	}
	for i, t := range tracks {
		if c := channels[t.Index]; c > maxChannels {
			tracks[i].Label = t.Label + " (plays as " + channelName(maxChannels) + ")"
		}
	}
	return tracks
}

// languageName is a language's English name from its ISO 639 code, or the
// code itself when it is not one of these.
func languageName(code string) string {
	c := strings.ToLower(strings.TrimSpace(code))
	if mapped, ok := iso639[c]; ok {
		c = mapped
	}
	if name, ok := languageNames[c]; ok {
		return name
	}
	if c == "" || c == "und" {
		return ""
	}
	return code
}

var languageNames = map[string]string{
	"en": "English", "es": "Spanish", "fr": "French", "de": "German", "it": "Italian",
	"pt": "Portuguese", "ru": "Russian", "ja": "Japanese", "ko": "Korean", "zh": "Chinese",
	"ar": "Arabic", "hi": "Hindi", "nl": "Dutch", "sv": "Swedish", "no": "Norwegian",
	"da": "Danish", "fi": "Finnish", "pl": "Polish", "tr": "Turkish", "cs": "Czech",
	"el": "Greek", "he": "Hebrew", "hu": "Hungarian", "th": "Thai", "uk": "Ukrainian",
	"vi": "Vietnamese", "id": "Indonesian", "ro": "Romanian", "bg": "Bulgarian", "hr": "Croatian",
	"sk": "Slovak", "sl": "Slovenian", "et": "Estonian", "lv": "Latvian", "lt": "Lithuanian",
	"is": "Icelandic", "ms": "Malay", "ta": "Tamil", "te": "Telugu", "fa": "Persian",
}

func hasAudio(tracks []source.AudioTrack, index int) bool {
	for _, t := range tracks {
		if t.Index == index {
			return true
		}
	}
	return false
}

// Episodes is a series' episodes in order, season by season, for its page.
func (s *Source) Episodes(ctx context.Context, seriesID string) ([]media.Item, error) {
	if err := s.owns(ctx, seriesID); err != nil {
		return nil, err
	}
	params := url.Values{
		"Fields":    {"Overview,ParentIndexNumber,IndexNumber"},
		"IsMissing": {"false"},
	}
	if s.cfg.UserID != "" {
		params.Set("userId", s.cfg.UserID)
	}
	var resp itemsResponse
	if err := s.http.JSON(ctx, "/Shows/"+url.PathEscape(seriesID)+"/Episodes", params, &resp); err != nil {
		return nil, err
	}
	items := make([]media.Item, 0, len(resp.Items))
	for _, it := range resp.Items {
		item := s.toItem(it)
		item.Extra["seriesId"] = seriesID
		if it.ParentIndexNumber != nil {
			item.Extra["season"] = strconv.Itoa(*it.ParentIndexNumber)
		}
		if it.IndexNumber != nil {
			item.Extra["number"] = strconv.Itoa(*it.IndexNumber)
		}
		items = append(items, item)
	}
	return items, nil
}

// NextEpisode is the one after this, in the series' own order.
func (s *Source) NextEpisode(ctx context.Context, episodeID string) (media.Item, bool, error) {
	if err := s.owns(ctx, episodeID); err != nil {
		return media.Item{}, false, err
	}
	params := url.Values{"Ids": {episodeID}, "Fields": {"ParentIndexNumber,IndexNumber"}}
	if s.cfg.UserID != "" {
		params.Set("userId", s.cfg.UserID)
	}
	var resp itemsResponse
	if err := s.http.JSON(ctx, "/Items", params, &resp); err != nil {
		return media.Item{}, false, err
	}
	if len(resp.Items) == 0 || resp.Items[0].SeriesID == "" {
		return media.Item{}, false, nil
	}
	episodes, err := s.Episodes(ctx, resp.Items[0].SeriesID)
	if err != nil {
		return media.Item{}, false, err
	}
	for i, e := range episodes {
		if e.ID == episodeID && i+1 < len(episodes) {
			return episodes[i+1], true, nil
		}
	}
	return media.Item{}, false, nil
}
