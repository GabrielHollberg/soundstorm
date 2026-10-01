// Package scrobble sends somebody's plays to their own ListenBrainz account.
//
// A scrobble is a record of a song played, kept by a service that turns a
// lifetime of them into statistics and recommendations. SoundStorm already
// keeps plays itself (internal/collections); this only repeats them to the
// account a person connected, from the server, so every device counts and
// the token never sits in a browser.
//
// ListenBrainz because it asks nothing of the project: each person pastes
// their own token, and there is no app key to register. Its rules, from its
// API documentation and checked against the live service: a listen once
// half the song or four minutes is heard (SoundStorm's own rule for a play),
// at most 1,000 per request, "single" for one and "import" for a batch, and
// validate-token answers 200 with valid:false for a bad token.
package scrobble

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/collections"
)

// DefaultListenBrainz is the public service.
const DefaultListenBrainz = "https://api.listenbrainz.org"

// maxPerRequest is ListenBrainz's MAX_LISTENS_PER_REQUEST.
const maxPerRequest = 1000

// ErrBadToken means the service refused the token: it was revoked or mistyped,
// and retrying will not help.
var ErrBadToken = errors.New("the service refused the token")

// Client talks to ListenBrainz.
type Client struct {
	BaseURL string
	HTTP    *http.Client
	// Version goes out as the submission client's version.
	Version string
}

// New is a client for the public service.
func New() *Client {
	return &Client{BaseURL: DefaultListenBrainz, HTTP: &http.Client{Timeout: 15 * time.Second}, Version: buildVersion()}
}

// buildVersion is the commit this binary was built from, or nothing.
func buildVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, kv := range info.Settings {
			if kv.Key == "vcs.revision" && len(kv.Value) >= 7 {
				return kv.Value[:7]
			}
		}
	}
	return ""
}

func (c *Client) do(ctx context.Context, method, path, token string, body any, out any) error {
	var r io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Token "+token)
	req.Header.Set("User-Agent", "SoundStorm (https://github.com/GabrielHollberg/soundstorm)")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		// Never the URL or the token: net/http errors carry the URL only.
		return fmt.Errorf("listenbrainz did not answer")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return ErrBadToken
	}
	if resp.StatusCode != http.StatusOK {
		return &statusError{code: resp.StatusCode}
	}
	if out != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
			return errors.New("listenbrainz: unreadable answer")
		}
	}
	return nil
}

// statusError is an answer other than 200 (and 401, which is ErrBadToken).
type statusError struct{ code int }

func (e *statusError) Error() string { return fmt.Sprintf("listenbrainz answered %d", e.code) }

// refused reports whether the service turned the request down for what was in
// it - a 4xx other than a bad token or being asked to slow down - so sending it
// again unchanged would only be refused again.
func refused(err error) bool {
	var se *statusError
	return errors.As(err, &se) && se.code >= 400 && se.code < 500 &&
		se.code != http.StatusUnauthorized && se.code != http.StatusTooManyRequests
}

// Validate checks a token and says whose it is.
func (c *Client) Validate(ctx context.Context, token string) (string, error) {
	var r struct {
		Valid    bool   `json:"valid"`
		UserName string `json:"user_name"`
	}
	if err := c.do(ctx, http.MethodGet, "/1/validate-token", token, nil, &r); err != nil {
		return "", err
	}
	if !r.Valid {
		return "", ErrBadToken
	}
	return r.UserName, nil
}

type trackMetadata struct {
	ArtistName     string         `json:"artist_name"`
	TrackName      string         `json:"track_name"`
	ReleaseName    string         `json:"release_name,omitempty"`
	AdditionalInfo map[string]any `json:"additional_info"`
}

type listen struct {
	ListenedAt    int64         `json:"listened_at,omitempty"`
	TrackMetadata trackMetadata `json:"track_metadata"`
}

func (c *Client) metadata(l collections.Listen) trackMetadata {
	info := map[string]any{"submission_client": "SoundStorm", "media_player": "SoundStorm"}
	if c.Version != "" {
		info["submission_client_version"] = c.Version
	}
	if l.Seconds > 0 {
		info["duration_ms"] = int64(l.Seconds * 1000)
	}
	return trackMetadata{ArtistName: l.Artist, TrackName: l.Title, ReleaseName: l.Album, AdditionalInfo: info}
}

// sendable is whether ListenBrainz will take a listen: it needs an artist and
// a title. One without is never sent, rather than failing a whole batch.
func sendable(l collections.Listen) bool {
	return strings.TrimSpace(l.Artist) != "" && strings.TrimSpace(l.Title) != ""
}

// Submit sends listens, oldest first, a request per thousand. It answers the
// listens that are done with - sent, never sendable, or refused - so the caller
// can take them off the queue, and how many of those the service refused; on
// an error, those before it are still done.
//
// A refusal is per listen, not per batch. ListenBrainz turns a whole request
// down (400) for one listen it will not take, and the queue keeps everything
// not done - so one such listen, resent with the rest every quarter hour, would
// hold up every scrobble after it for good. So a refused batch is sent again a
// listen at a time, and what is refused alone is dropped rather than retried.
func (c *Client) Submit(ctx context.Context, token string, listens []collections.Listen) (done []collections.Listen, dropped int, err error) {
	for start := 0; start < len(listens); start += maxPerRequest {
		batch := listens[start:min(start+maxPerRequest, len(listens))]
		var send []collections.Listen
		for _, l := range batch {
			// A listen with no moment has no listened_at to send - the zero time
			// is year one, which the service refuses - so it is never sendable.
			if sendable(l) && !l.At.IsZero() {
				send = append(send, l)
			} else {
				done = append(done, l)
			}
		}
		if len(send) == 0 {
			continue
		}
		err := c.submit(ctx, token, send)
		switch {
		case err == nil:
			done = append(done, send...)
		case refused(err) && len(send) == 1:
			done = append(done, send...)
			dropped++
		case refused(err):
			for _, l := range send {
				one := c.submit(ctx, token, []collections.Listen{l})
				switch {
				case one == nil:
					done = append(done, l)
				case refused(one):
					done = append(done, l)
					dropped++
				default:
					return done, dropped, one
				}
			}
		default:
			return done, dropped, err
		}
	}
	return done, dropped, nil
}

// submit sends listens in one request, "single" for one and "import" for more.
func (c *Client) submit(ctx context.Context, token string, listens []collections.Listen) error {
	payload := make([]listen, 0, len(listens))
	for _, l := range listens {
		payload = append(payload, listen{ListenedAt: l.At.Unix(), TrackMetadata: c.metadata(l)})
	}
	kind := "import"
	if len(payload) == 1 {
		kind = "single"
	}
	return c.do(ctx, http.MethodPost, "/1/submit-listens", token,
		map[string]any{"listen_type": kind, "payload": payload}, nil)
}

// NowPlaying says what somebody has just started, which the service shows
// on their profile until the next song.
func (c *Client) NowPlaying(ctx context.Context, token string, l collections.Listen) error {
	if !sendable(l) {
		return nil
	}
	return c.do(ctx, http.MethodPost, "/1/submit-listens", token,
		map[string]any{"listen_type": "playing_now", "payload": []listen{{TrackMetadata: c.metadata(l)}}}, nil)
}
