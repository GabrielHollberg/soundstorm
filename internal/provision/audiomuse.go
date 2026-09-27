package provision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/httpx"
	"github.com/GabrielHollberg/soundstorm/internal/state"
)

// audiomuseNavidromeUser is the Navidrome account AudioMuse-AI listens
// through: its own, and not an administrator - it only needs to read songs.
const audiomuseNavidromeUser = "audiomuse"

// errWaitingForMusic is returned until Navidrome has been provisioned, since
// AudioMuse-AI can only be set up with an account on it. The retry loop
// waits it out.
var errWaitingForMusic = errors.New("waiting for the music library to be set up")

// provisionAudioMuse sets up a fresh AudioMuse-AI with nobody logging in.
// Its setup wizard is an API that answers without a login until the first
// save and never after - the same safety property as Jellyfin's startup
// wizard, checked against 3.6.3: a second unauthenticated save is a 401 and a
// made-up token is refused.
//
// SoundStorm gives it a non-admin Navidrome account of its own, generates its
// API token and admin password, and turns off everything that would reach
// outside the house (the third-party lyrics lookup) or cost hours for
// nothing SoundStorm shows (transcribing lyrics). Then it schedules a nightly
// look for new songs and starts the first listen.
func provisionAudioMuse(ctx context.Context, c *httpx.Client, store *state.Store, log *slog.Logger) (state.Backend, error) {
	resp, err := c.Do(ctx, httpx.Request{Path: "/api/health"})
	if err != nil {
		return state.Backend{}, fmt.Errorf("audiomuse not reachable: %w", err)
	}
	if err := resp.Err(); err != nil {
		return state.Backend{}, fmt.Errorf("audiomuse not ready: %w", err)
	}

	resp, err = c.Do(ctx, httpx.Request{Path: "/api/setup"})
	if err != nil {
		return state.Backend{}, fmt.Errorf("audiomuse setup: %w", err)
	}
	if resp.Status == http.StatusUnauthorized {
		return state.Backend{}, fmt.Errorf(
			"audiomuse is already set up but SoundStorm has no stored credentials for it; " +
				"either restore SoundStorm's state file or reset the audiomuse volumes")
	}
	if err := resp.Err(); err != nil {
		return state.Backend{}, fmt.Errorf("audiomuse setup: %w", err)
	}

	music, ok := store.Backend("navidrome")
	if !ok || music.Password == "" {
		return state.Backend{}, errWaitingForMusic
	}
	listenPassword, err := generatePassword()
	if err != nil {
		return state.Backend{}, err
	}
	if err := navidromeListener(ctx, music, listenPassword); err != nil {
		return state.Backend{}, err
	}

	token, err := generatePassword()
	if err != nil {
		return state.Backend{}, err
	}
	adminPassword, err := generatePassword()
	if err != nil {
		return state.Backend{}, err
	}
	resp, err = c.Do(ctx, httpx.Request{Method: http.MethodPost, Path: "/api/setup", Body: map[string]any{
		"navidrome_auth_mode": "password",
		"config": map[string]string{
			"MEDIASERVER_TYPE":   "navidrome",
			"NAVIDROME_URL":      music.BaseURL,
			"NAVIDROME_USER":     audiomuseNavidromeUser,
			"NAVIDROME_PASSWORD": listenPassword,
			"AUTH_ENABLED":       "true",
			"AUDIOMUSE_USER":     accountName,
			"AUDIOMUSE_PASSWORD": adminPassword,
			"API_TOKEN":          token,
			// Nothing leaves the house: the lyrics lookup asks a third party.
			"LYRICS_API_ENABLE": "false",
			// Transcribing every song's words would add hours to the first
			// listen for a feature SoundStorm does not show.
			"LYRICS_ENABLED":    "false",
			"LYRICS_ASR_ENABLE": "false",
		},
	}})
	if err != nil {
		return state.Backend{}, fmt.Errorf("audiomuse save setup: %w", err)
	}
	if err := resp.Err(); err != nil {
		return state.Backend{}, fmt.Errorf("audiomuse save setup: %w", err)
	}
	log.Info("set up audiomuse", "navidrome user", audiomuseNavidromeUser)

	// Saving restarts its web server; wait until the token answers.
	authed := map[string]string{"Authorization": "Bearer " + token}
	for i := 0; ; i++ {
		resp, err = c.Do(ctx, httpx.Request{Path: "/api/version", Headers: authed})
		if err == nil && resp.OK() {
			break
		}
		if i == 30 {
			return state.Backend{}, fmt.Errorf("audiomuse did not come back after setup")
		}
		select {
		case <-ctx.Done():
			return state.Backend{}, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}

	// A nightly look for new songs, and the first listen now. Neither
	// failing undoes the setup: SoundStorm starts one again later.
	if resp, err := c.Do(ctx, httpx.Request{Method: http.MethodPost, Path: "/api/cron", Headers: authed, Body: map[string]any{
		"name": "SoundStorm nightly listen", "task_type": "analysis", "cron_expr": "30 3 * * *", "enabled": true,
	}}); err != nil || !resp.OK() {
		log.Warn("audiomuse nightly schedule not saved", "err", err)
	}
	if resp, err := c.Do(ctx, httpx.Request{Method: http.MethodPost, Path: "/api/analysis/start", Headers: authed, Body: map[string]any{}}); err != nil || !resp.OK() {
		log.Warn("audiomuse first listen not started", "err", err)
	}

	return state.Backend{
		Type:          "audiomuse",
		BaseURL:       c.BaseURL().String(),
		Username:      accountName,
		Password:      adminPassword,
		Token:         token,
		ProvisionedAt: time.Now().UTC(),
	}, nil
}

// navidromeListener makes, or resets the password of, the non-admin Navidrome
// account AudioMuse-AI reads songs through. Navidrome's own API, not
// Subsonic's: checked against 0.64.1, a user made this way sees every song
// and is refused a scan.
func navidromeListener(ctx context.Context, music state.Backend, password string) error {
	nd, err := httpx.New(music.BaseURL, backendTimeout)
	if err != nil {
		return err
	}
	resp, err := nd.Do(ctx, httpx.Request{Method: http.MethodPost, Path: "/auth/login",
		Body: map[string]string{"username": music.Username, "password": music.Password}})
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

	user := map[string]any{"userName": audiomuseNavidromeUser, "name": "AudioMuse", "password": password, "isAdmin": false}
	resp, err = nd.Do(ctx, httpx.Request{Method: http.MethodPost, Path: "/api/user", Body: user})
	if err != nil {
		return fmt.Errorf("navidrome create listener: %w", err)
	}
	if resp.OK() {
		return nil
	}
	// It exists already - AudioMuse's volumes were reset, Navidrome's were
	// not. Give it the new password rather than failing for ever.
	resp, err = nd.Do(ctx, httpx.Request{Path: "/api/user"})
	if err != nil {
		return fmt.Errorf("navidrome users: %w", err)
	}
	var users []struct {
		ID       string `json:"id"`
		UserName string `json:"userName"`
	}
	if err := resp.Err(); err != nil || json.Unmarshal(resp.Body, &users) != nil {
		return fmt.Errorf("navidrome users: unreadable (%v)", err)
	}
	for _, u := range users {
		if u.UserName != audiomuseNavidromeUser {
			continue
		}
		user["id"] = u.ID
		resp, err = nd.Do(ctx, httpx.Request{Method: http.MethodPut, Path: "/api/user/" + u.ID, Body: user})
		if err != nil {
			return fmt.Errorf("navidrome reset listener: %w", err)
		}
		return resp.Err()
	}
	return fmt.Errorf("navidrome refused the listener account: %s", httpx.Snippet(resp.Body))
}
