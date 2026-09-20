package provision

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gabehollberg/soundstorm/internal/httpx"
	"github.com/gabehollberg/soundstorm/internal/state"
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
func provisionNavidrome(ctx context.Context, c *httpx.Client, log *slog.Logger) (state.Backend, error) {
	// Cheap reachability check first, so the retry loop reports "waiting for
	// navidrome" rather than a confusing createAdmin failure.
	if _, err := c.Do(ctx, httpx.Request{Path: "/ping"}); err != nil {
		return state.Backend{}, fmt.Errorf("navidrome not reachable: %w", err)
	}

	password, err := generatePassword()
	if err != nil {
		return state.Backend{}, err
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
			return state.Backend{}, fmt.Errorf(
				"navidrome already has an admin account but SoundStorm has no stored credentials for it; " +
					"either restore SoundStorm's state file or reset the navidrome volume")
		}
		return state.Backend{}, fmt.Errorf("navidrome createAdmin returned %d: %s",
			resp.Status, httpx.Snippet(resp.Body))
	}

	log.Info("created navidrome account", "username", accountName)
	return state.Backend{
		Type:          "navidrome",
		BaseURL:       c.BaseURL().String(),
		Username:      accountName,
		Password:      password,
		ProvisionedAt: time.Now().UTC(),
	}, nil
}
