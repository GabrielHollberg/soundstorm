package jellyfin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gabehollberg/soundstorm/internal/media"
)

// fakeJellyfin answers PlaybackInfo with a canned MediaSource and records what
// profile it was asked with.
func fakeJellyfin(t *testing.T, mediaSource map[string]any, sessionID string) (*Source, *map[string]any) {
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
				"PlaySessionId": sessionID,
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
func TestDirectPlayIsUsedWhenOffered(t *testing.T) {
	s, _ := fakeJellyfin(t, map[string]any{
		"Id":                 "ms-1",
		"Container":          "mp4",
		"SupportsDirectPlay": true,
	}, "session-1")

	target, err := s.StreamTarget(context.Background(), "item-1")
	if err != nil {
		t.Fatalf("StreamTarget: %v", err)
	}
	if !strings.Contains(target.URL, "/Videos/item-1/stream") {
		t.Errorf("url = %q, want the direct stream endpoint", target.URL)
	}
	if !strings.Contains(target.URL, "static=true") {
		t.Errorf("url = %q, want static=true", target.URL)
	}
	if target.OnDone != nil {
		t.Error("direct play starts no transcode, so there is nothing to stop")
	}
	if target.Headers["Authorization"] == "" {
		t.Error("the credential must travel in a header; Jellyfin 12 ignores api_key")
	}
}

// The bug this whole mechanism exists to fix: HEVC in MKV used to be handed
// over untouched, and the video element displayed nothing at all.
func TestTranscodeIsUsedWhenDirectPlayIsImpossible(t *testing.T) {
	s, _ := fakeJellyfin(t, map[string]any{
		"Id":                     "ms-1",
		"Container":              "mkv",
		"SupportsDirectPlay":     false,
		"SupportsDirectStream":   false,
		"SupportsTranscoding":    true,
		"TranscodingUrl":         "/videos/item-1/stream.mp4?VideoCodec=h264&AudioCodec=aac&PlaySessionId=session-2",
		"TranscodingSubProtocol": "http",
	}, "session-2")

	target, err := s.StreamTarget(context.Background(), "item-1")
	if err != nil {
		t.Fatalf("StreamTarget: %v", err)
	}
	if !strings.Contains(target.URL, "/videos/item-1/stream.mp4") {
		t.Errorf("url = %q, want the transcode endpoint", target.URL)
	}
	// The query Jellyfin built has to survive intact - it carries the codec
	// decisions and the session id.
	if !strings.Contains(target.URL, "VideoCodec=h264") ||
		!strings.Contains(target.URL, "PlaySessionId=session-2") {
		t.Errorf("url = %q, lost the parameters Jellyfin chose", target.URL)
	}
	if target.OnDone == nil {
		t.Error("a transcode leaves an ffmpeg process running and must be stoppable")
	}
}

// The profile is the whole basis of Jellyfin's decision, so getting it wrong is
// silent: claim a codec we cannot play and the video element shows nothing.
func TestDeviceProfileIsConservative(t *testing.T) {
	s, sent := fakeJellyfin(t, map[string]any{"Id": "ms-1", "SupportsDirectPlay": true}, "s")

	if _, err := s.StreamTarget(context.Background(), "item-1"); err != nil {
		t.Fatalf("StreamTarget: %v", err)
	}
	profile, ok := (*sent)["DeviceProfile"].(map[string]any)
	if !ok {
		t.Fatal("no DeviceProfile was sent; Jellyfin would guess")
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
	if !strings.Contains(string(transcoding), `"Protocol":"http"`) {
		t.Errorf("transcoding profile = %s, want progressive http (HLS would need hls.js)", transcoding)
	}
}

// If Jellyfin ever answers with HLS despite being asked for progressive, that
// must be a legible error rather than a video element silently refusing a
// playlist it cannot parse.
func TestHLSAnswerIsRejectedLoudly(t *testing.T) {
	s, _ := fakeJellyfin(t, map[string]any{
		"Id":                     "ms-1",
		"SupportsTranscoding":    true,
		"TranscodingUrl":         "/videos/item-1/master.m3u8",
		"TranscodingSubProtocol": "hls",
	}, "s")

	_, err := s.StreamTarget(context.Background(), "item-1")
	if err == nil {
		t.Fatal("want an error for an HLS answer")
	}
	if !strings.Contains(err.Error(), "HLS") {
		t.Errorf("error should name the problem, got %v", err)
	}
}

func TestNoPlayableSourceIsAnError(t *testing.T) {
	s, _ := fakeJellyfin(t, map[string]any{
		"Id":        "ms-1",
		"Container": "mkv",
	}, "s")

	if _, err := s.StreamTarget(context.Background(), "item-1"); err == nil {
		t.Error("want an error when a file can be neither direct played nor transcoded")
	}
}

// One Jellyfin serves films and series as two sources; this is the part that
// was hardcoded until TV support arrived.
func TestKindAndItemTypesAreConfigurable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
