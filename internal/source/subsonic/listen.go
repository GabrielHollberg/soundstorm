package subsonic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/httpx"
	"github.com/GabrielHollberg/soundstorm/internal/source"
)

// Songs are handed to the server's own hearing (internal/beats) through a
// transcoding of SoundStorm's: mono, 11025 a second, as FLAC - small, and
// the one format the server reads with the standard library. Navidrome's
// own "flac audio" will not do: checked on 0.64, it refuses to turn a lossy
// song into a lossless format and sends Opus instead. A format under a name
// of our own is converted as asked (checked: an MP3 came back as FLAC).
//
// Navidrome only takes a new transcoding with ND_ENABLETRANSCODINGCONFIG on
// (without it the API answers 405), which docker-compose.yml sets.
const (
	listenFormat  = "sslisten"
	listenName    = "SoundStorm listening"
	listenCommand = "ffmpeg -i %s -map 0:a:0 -v 0 -ac 1 -ar 11025 -c:a flac -f flac -"
)

// ErrListeningOff is Navidrome refusing to take the transcoding.
var ErrListeningOff = errors.New("navidrome does not allow new transcodings (ND_ENABLETRANSCODINGCONFIG)")

// listening remembers that the transcoding is in place, or when it last
// could not be, so a refusal is not asked again on every song.
type listening struct {
	mu     sync.Mutex
	ready  bool
	failed time.Time
	err    error
}

// PrepareListening makes sure Navidrome has the transcoding, adding it or
// putting its command right.
func (s *Source) PrepareListening(ctx context.Context) error {
	s.listen.mu.Lock()
	defer s.listen.mu.Unlock()
	if s.listen.ready {
		return nil
	}
	if s.listen.err != nil && time.Since(s.listen.failed) < 10*time.Minute {
		return s.listen.err
	}
	err := s.ensureListening(ctx)
	if err != nil {
		s.listen.err, s.listen.failed = err, time.Now()
		return err
	}
	s.listen.ready, s.listen.err = true, nil
	return nil
}

func (s *Source) ensureListening(ctx context.Context) error {
	// Navidrome's own API, not Subsonic's: a client of its own, so the token
	// never rides along on Subsonic calls.
	nd, err := httpx.New(s.cfg.BaseURL, 20*time.Second)
	if err != nil {
		return err
	}
	resp, err := nd.Do(ctx, httpx.Request{Method: http.MethodPost, Path: "/auth/login",
		Body: map[string]string{"username": s.cfg.Username, "password": s.cfg.Password}})
	if err != nil {
		return fmt.Errorf("navidrome login: %w", err)
	}
	if err := resp.Err(); err != nil {
		return fmt.Errorf("navidrome login: %w", err)
	}
	var login struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(resp.Body, &login) != nil || login.Token == "" {
		return errors.New("navidrome login returned no token")
	}
	nd.SetHeader("x-nd-authorization", "Bearer "+login.Token)

	resp, err = nd.Do(ctx, httpx.Request{Path: "/api/transcoding"})
	if err != nil {
		return fmt.Errorf("navidrome transcodings: %w", err)
	}
	if err := resp.Err(); err != nil {
		return fmt.Errorf("navidrome transcodings: %w", err)
	}
	var have []struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		TargetFormat string `json:"targetFormat"`
		Command      string `json:"command"`
	}
	if err := json.Unmarshal(resp.Body, &have); err != nil {
		return fmt.Errorf("navidrome transcodings: %w", err)
	}
	want := map[string]any{"name": listenName, "targetFormat": listenFormat, "command": listenCommand, "defaultBitRate": 0}
	for _, t := range have {
		if t.TargetFormat != listenFormat && t.Name != listenName {
			continue
		}
		if t.TargetFormat == listenFormat && t.Name == listenName && t.Command == listenCommand {
			return nil
		}
		want["id"] = t.ID
		resp, err = nd.Do(ctx, httpx.Request{Method: http.MethodPut, Path: "/api/transcoding/" + url.PathEscape(t.ID), Body: want})
		return transcodingAnswer(resp, err)
	}
	resp, err = nd.Do(ctx, httpx.Request{Method: http.MethodPost, Path: "/api/transcoding", Body: want})
	return transcodingAnswer(resp, err)
}

func transcodingAnswer(resp *httpx.Response, err error) error {
	if err != nil {
		return fmt.Errorf("navidrome transcoding: %w", err)
	}
	if resp.Status == http.StatusMethodNotAllowed || resp.Status == http.StatusForbidden {
		return ErrListeningOff
	}
	if err := resp.Err(); err != nil {
		return fmt.Errorf("navidrome transcoding: %w", err)
	}
	return nil
}

// ListenTarget is a song as the server's hearing wants it: mono FLAC.
func (s *Source) ListenTarget(ctx context.Context, itemID string) (source.Target, error) {
	t, err := s.mediaTarget("/rest/stream.view", itemID)
	if err != nil {
		return t, err
	}
	u, err := url.Parse(t.URL)
	if err != nil {
		return source.Target{}, err
	}
	q := u.Query()
	q.Set("format", listenFormat)
	u.RawQuery = q.Encode()
	t.URL = u.String()
	return t, nil
}
