package provision

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/httpx"
	"github.com/GabrielHollberg/soundstorm/internal/state"
)

// provisionNavidrome creates SoundStorm's account on a fresh Navidrome.
//
// Navidrome has no "initial admin" environment variable: on first run its web
// UI shows a create-admin form, which POSTs to /auth/createAdmin. We post the
// same thing. That endpoint only works while no user exists, which is exactly
// the guarantee we want - it cannot be used to hijack an existing install.
//
// The library folder needs no API call here: Navidrome scans ND_MUSICFOLDER,
// which compose sets.
func provisionNavidrome(ctx context.Context, c *httpx.Client, sec secrets, log *slog.Logger) (state.Backend, error) {
	// Cheap reachability check first, so the retry loop reports "waiting for
	// navidrome" rather than a confusing createAdmin failure.
	if _, err := c.Do(ctx, httpx.Request{Path: "/ping"}); err != nil {
		return state.Backend{}, fmt.Errorf("navidrome not reachable: %w", err)
	}

	password, kept, err := sec("password")
	if err != nil {
		return state.Backend{}, err
	}
	creds := state.Backend{
		Type:          "navidrome",
		BaseURL:       c.BaseURL().String(),
		Username:      accountName,
		Password:      password,
		ProvisionedAt: time.Now().UTC(),
	}

	resp, err := c.Do(ctx, httpx.Request{
		Method: http.MethodPost,
		Path:   "/auth/createAdmin",
		Body: map[string]string{
			"username": accountName,
			"password": password,
		},
	})
	if err != nil {
		return state.Backend{}, fmt.Errorf("navidrome createAdmin: %w", err)
	}

	if !resp.OK() {
		// The most likely cause by far: this Navidrome already has users, but
		// SoundStorm's state file was wiped or never saved. Say so precisely,
		// because the fix is a human decision (reset which volume?) and no
		// amount of retrying will help.
		if resp.Status == http.StatusForbidden || resp.Status == http.StatusConflict ||
			strings.Contains(strings.ToLower(string(resp.Body)), "already") {
			// Made by an earlier attempt whose answer never arrived: the
			// password was kept, so the account is ours.
			if kept && navidromeAnswers(ctx, c, password) {
				log.Info("navidrome account made by an earlier attempt; carrying on", "username", accountName)
				return creds, nil
			}
			return state.Backend{}, fmt.Errorf(
				"navidrome already has an admin account but SoundStorm has no stored credentials for it; " +
					"either restore SoundStorm's state file or reset the navidrome volume")
		}
		return state.Backend{}, fmt.Errorf("navidrome createAdmin returned %d: %s",
			resp.Status, httpx.Snippet(resp.Body))
	}

	log.Info("created navidrome account", "username", accountName)
	return creds, nil
}

// navidromeAnswers reports whether Navidrome takes SoundStorm's account with
// this password (a Subsonic ping).
func navidromeAnswers(ctx context.Context, c *httpx.Client, password string) bool {
	resp, err := c.Do(ctx, httpx.Request{Path: "/rest/ping.view", Params: url.Values{
		"u": {accountName}, "p": {password}, "v": {"1.16.1"}, "c": {"soundstorm-app"}, "f": {"json"},
	}})
	return err == nil && resp.OK() && strings.Contains(string(resp.Body), `"ok"`)
}
