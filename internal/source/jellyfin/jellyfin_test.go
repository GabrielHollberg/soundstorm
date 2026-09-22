package jellyfin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/source"
)

// fakeJellyfin answers PlaybackInfo with a canned MediaSource and records the
// profile it was asked with.
func fakeJellyfin(t *testing.T, mediaSource map[string]any) (*Source, *map[string]any) {
	t.Helper()

	var sent map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/PlaybackInfo"):
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &sent)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"MediaSources":  []any{mediaSource},
				"PlaySessionId": "session-1",
			})
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(srv.Close)

	s, err := New(Config{ID: "jellyfin", BaseURL: srv.URL, Token: "tok", UserID: "user-1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s, &sent
}

// A file the browser can already decode must not be transcoded. Sending a whole
// film through ffmpeg when it would have played untouched is the expensive
// mistake in the other direction.
func TestDirectPlayIsUsedForAPlayableFile(t *testing.T) {
	s, _ := fakeJellyfin(t, map[string]any{
		"Id":                 "ms-1",
		"Container":          "mp4",
		"SupportsDirectPlay": true,
	})

	play, err := s.Playback(context.Background(), "item-1")
	if err != nil {
		t.Fatalf("Playback: %v", err)
	}
	if play.Mode != source.PlaybackModeDirect {
		t.Errorf("mode = %q, want direct", play.Mode)
	}
}

// The bug this whole mechanism exists to fix: HEVC in MKV used to be handed
// over untouched, and the video element displayed nothing at all.
func TestUnplayableContainerIsTranscodedToHLS(t *testing.T) {
	s, _ := fakeJellyfin(t, map[string]any{
		"Id":                  "ms-9",
		"Container":           "mkv",
		"SupportsDirectPlay":  false,
		"SupportsTranscoding": true,
	})

	play, err := s.Playback(context.Background(), "item-1")
	if err != nil {
		t.Fatalf("Playback: %v", err)
	}
	if play.Mode != source.PlaybackModeHLS {
		t.Fatalf("mode = %q, want hls", play.Mode)
	}
	// The path decides where the playlist's relative segment references land,
	// so it has to mirror the backend's own namespace.
	if play.Path != "item-1/master.m3u8" {
		t.Errorf("path = %q, want item-1/master.m3u8", play.Path)
	}
	if got := play.Query.Get("mediaSourceId"); got != "ms-9" {
		t.Errorf("mediaSourceId = %q, want the id Jellyfin chose", got)
	}
	for key, want := range map[string]string{
		"transcodingProtocol": "hls",
		"videoCodec":          "h264",
		"audioCodec":          "aac",
	} {
		if got := play.Query.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

// Jellyfin 12.1.0 has been observed answering SupportsDirectPlay for an MKV
// even when the profile offers only mp4. Believing it reproduces exactly the
// silent failure this code exists to remove, so the container is checked again.
func TestDirectPlayClaimIsNotTrustedForAnUnplayableContainer(t *testing.T) {
	s, _ := fakeJellyfin(t, map[string]any{
		"Id":                   "ms-1",
		"Container":            "mkv",
		"SupportsDirectPlay":   true,
		"SupportsDirectStream": true,
		"SupportsTranscoding":  true,
	})

	play, err := s.Playback(context.Background(), "item-1")
	if err != nil {
		t.Fatalf("Playback: %v", err)
	}
	if play.Mode != source.PlaybackModeHLS {
		t.Errorf("mode = %q: an mkv was accepted as direct play on Jellyfin's word alone", play.Mode)
	}
}

// The profile is the basis of Jellyfin's decision, so getting it wrong fails
// silently: claim a codec we cannot play and the video element shows nothing.
func TestDeviceProfileIsConservative(t *testing.T) {
	s, sent := fakeJellyfin(t, map[string]any{
		"Id": "ms-1", "Container": "mp4", "SupportsDirectPlay": true,
	})

	if _, err := s.Playback(context.Background(), "item-1"); err != nil {
		t.Fatalf("Playback: %v", err)
	}
	profile, ok := (*sent)["DeviceProfile"].(map[string]any)
	if !ok {
		t.Fatal("no DeviceProfile was sent; Jellyfin would be guessing")
	}

	direct, _ := json.Marshal(profile["DirectPlayProfiles"])
	for _, codec := range []string{"hevc", "h265", "dts", "truehd", "mkv", "matroska"} {
		if strings.Contains(strings.ToLower(string(direct)), codec) {
			t.Errorf("direct play profile claims %q, which browsers do not reliably decode", codec)
		}
	}
	if !strings.Contains(string(direct), "h264") {
		t.Error("direct play profile should accept h264, or everything transcodes needlessly")
	}

	transcoding, _ := json.Marshal(profile["TranscodingProfiles"])
	if !strings.Contains(string(transcoding), `"Protocol":"hls"`) {
		t.Errorf("transcoding profile = %s, want hls (progressive cannot be seeked)", transcoding)
	}
}

// The playlist references its segments relatively, so this mapping is what
// makes those references land back on the right Jellyfin path.
func TestHLSTargetMapsOntoTheVideoNamespace(t *testing.T) {
	s, _ := fakeJellyfin(t, map[string]any{"Id": "ms-1"})

	target, err := s.HLSTarget(context.Background(), "item-1/hls1/main/3.ts",
		url.Values{"mediaSourceId": {"ms-1"}})
	if err != nil {
		t.Fatalf("HLSTarget: %v", err)
	}
	if !strings.Contains(target.URL, "/videos/item-1/hls1/main/3.ts") {
		t.Errorf("url = %q, want the segment under /videos", target.URL)
	}
	if !strings.Contains(target.URL, "mediaSourceId=ms-1") {
		t.Errorf("url = %q, dropped the query the playlist carried", target.URL)
	}
	if target.Headers["Authorization"] == "" {
		t.Error("segments need the credential too; Jellyfin 12 ignores api_key")
	}
}

func TestHLSTargetRejectsTraversal(t *testing.T) {
	s, _ := fakeJellyfin(t, map[string]any{"Id": "ms-1"})
	for _, bad := range []string{"", "../Users/admin", "item/../../secret"} {
		if _, err := s.HLSTarget(context.Background(), bad, nil); err == nil {
			t.Errorf("HLSTarget(%q) should have been rejected", bad)
		}
	}
}

func TestStreamTargetIsStillDirect(t *testing.T) {
	s, _ := fakeJellyfin(t, map[string]any{"Id": "ms-1"})

	target, err := s.StreamTarget(context.Background(), "item-1")
	if err != nil {
		t.Fatalf("StreamTarget: %v", err)
	}
	if !strings.Contains(target.URL, "static=true") {
		t.Errorf("url = %q, want the untouched file", target.URL)
	}
	if _, err := s.StreamTarget(context.Background(), ""); err == nil {
		t.Error("want an error for an empty item id")
	}
}

// One Jellyfin serves films and series as two sources; this is the part that
// was hardcoded until TV support arrived.
func TestKindAndItemTypesAreConfigurable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Items":[],"TotalRecordCount":0}`))
	}))
	defer srv.Close()

	films, err := New(Config{ID: "jellyfin", BaseURL: srv.URL, Token: "t"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if films.Kind() != media.KindVideo {
		t.Errorf("default kind = %q, want video", films.Kind())
	}

	shows, err := New(Config{
		ID: "jellyfin-tv", BaseURL: srv.URL, Token: "t",
		Kind: media.KindTV, ItemTypes: "Series,Episode",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if shows.Kind() != media.KindTV {
		t.Errorf("kind = %q, want tv", shows.Kind())
	}
}

// Bitmap subtitles cannot become WebVTT - they are pictures of text. Offering
// one would attach a track that silently renders nothing, which is worse than
// not offering it.
func TestOnlyTextSubtitlesAreOffered(t *testing.T) {
	s, _ := fakeJellyfin(t, map[string]any{
		"Id": "ms-1", "Container": "mkv",
		"MediaStreams": []any{
			map[string]any{"Index": 0, "Type": "Video", "Codec": "hevc"},
			map[string]any{"Index": 1, "Type": "Audio", "Codec": "flac"},
			map[string]any{"Index": 2, "Type": "Subtitle", "Codec": "subrip", "Language": "eng", "Title": "English"},
			map[string]any{"Index": 3, "Type": "Subtitle", "Codec": "pgssub", "Language": "fra", "Title": "French"},
			map[string]any{"Index": 4, "Type": "Subtitle", "Codec": "dvdsub", "Language": "deu"},
		},
	})

	play, err := s.Playback(context.Background(), "item-1")
	if err != nil {
		t.Fatalf("Playback: %v", err)
	}
	if len(play.Subtitles) != 1 {
		t.Fatalf("offered %d tracks, want only the text one: %+v", len(play.Subtitles), play.Subtitles)
	}
	got := play.Subtitles[0]
	if got.Label != "English" {
		t.Errorf("label = %q", got.Label)
	}
	// Browsers want BCP-47 in srclang; backends report ISO 639-2.
	if got.Language != "en" {
		t.Errorf("language = %q, want the two-letter tag", got.Language)
	}
	if got.ID != "item-1/ms-1/2" {
		t.Errorf("id = %q, want item/source/index", got.ID)
	}
}

// Subtitles belong to the file, not to how the video happens to be delivered.
func TestSubtitlesAreOfferedForDirectPlayToo(t *testing.T) {
	s, _ := fakeJellyfin(t, map[string]any{
		"Id": "ms-1", "Container": "mp4", "SupportsDirectPlay": true,
		"MediaStreams": []any{
			map[string]any{"Index": 0, "Type": "Subtitle", "Codec": "subrip",
				"Language": "eng", "IsExternal": true, "DisplayTitle": "English - SUBRIP - External"},
		},
	})

	play, err := s.Playback(context.Background(), "item-1")
	if err != nil {
		t.Fatalf("Playback: %v", err)
	}
	if play.Mode != source.PlaybackModeDirect {
		t.Fatalf("mode = %q, want direct", play.Mode)
	}
	if len(play.Subtitles) != 1 {
		t.Fatalf("a sidecar subtitle was dropped for a direct-played file")
	}
	// Jellyfin invents "English - SUBRIP - External" when a stream has no
	// title; only the first part names the track.
	if play.Subtitles[0].Label != "English" {
		t.Errorf("label = %q, want the plumbing trimmed off", play.Subtitles[0].Label)
	}
}

func TestSubtitleTargetBuildsAVTTURL(t *testing.T) {
	s, _ := fakeJellyfin(t, map[string]any{"Id": "ms-1"})

	target, err := s.SubtitleTarget(context.Background(), "item-1/ms-1/2")
	if err != nil {
		t.Fatalf("SubtitleTarget: %v", err)
	}
	if !strings.Contains(target.URL, "/Videos/item-1/ms-1/Subtitles/2/Stream.vtt") {
		t.Errorf("url = %q", target.URL)
	}
	if target.Headers["Authorization"] == "" {
		t.Error("subtitles need the credential too")
	}

	for _, bad := range []string{"", "item-1", "item-1/ms-1", "item-1/ms-1/notanumber", "a/b/c/d"} {
		if _, err := s.SubtitleTarget(context.Background(), bad); err == nil {
			t.Errorf("SubtitleTarget(%q) should have been rejected", bad)
		}
	}
}

// Jellyfin will serve episodes that have no file - one per gap in a series,
// manufactured from its metadata, when a user has "display missing episodes"
// switched on. They arrive as ordinary results with nothing behind them to play.
//
// The parameter is asserted rather than the behaviour, because a fake server
// cannot manufacture a virtual episode. What it is standing in for was checked
// against Jellyfin 12.1.0 directly: IsMissing=true returned nothing for a real
// film, IsMissing=false kept the film, an episode and its parent series, and
// IsVirtualItem=false - the parameter that looks like the more general answer -
// was ignored as completely as a name invented for the test.
func TestSearchAsksJellyfinToLeaveOutItemsWithNoFile(t *testing.T) {
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Items":[],"TotalRecordCount":0}`))
	}))
	defer srv.Close()

	s, err := New(Config{ID: "jellyfin", BaseURL: srv.URL, Token: "t"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for _, q := range []media.Query{{}, {Text: "dune"}} {
		if _, err := s.Search(context.Background(), q); err != nil {
			t.Fatalf("Search(%q): %v", q.Text, err)
		}
		if got.Get("IsMissing") != "false" {
			t.Errorf("Search(%q) sent IsMissing=%q, want \"false\"", q.Text, got.Get("IsMissing"))
		}
	}
}
