package provision

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/httpx"
	"github.com/GabrielHollberg/soundstorm/internal/state"
)

// immichEmail is the account SoundStorm creates on Immich. Immich identifies
// users by email and validates the shape, so the account needs one; nothing is
// ever sent to it. .invalid is reserved by RFC 2606 for exactly this - a
// domain guaranteed never to exist.
const immichEmail = accountName + "@soundstorm.invalid"

// provisionImmich creates SoundStorm's admin account on a fresh Immich, mints
// an API key, and points an external library at the pictures folder.
//
// Every step is a documented API call, which is what makes Immich usable here
// at all: its docs describe creating an external library through the admin
// screens, but those screens are an API client like any other.
//
// The library is external - indexed in place, mounted read-only - rather than
// Immich's own upload storage. That keeps "the folders are the interface"
// true: a photo is a file in pictures/ whether it arrived through SoundStorm
// or a file manager, and Immich can never move, rename or delete one.
func provisionImmich(ctx context.Context, c *httpx.Client, t Target, log *slog.Logger) (state.Backend, error) {
	var ping struct {
		Res string `json:"res"`
	}
	if err := c.JSON(ctx, "/api/server/ping", nil, &ping); err != nil {
		return state.Backend{}, fmt.Errorf("immich not reachable: %w", err)
	}
	if ping.Res != "pong" {
		return state.Backend{}, fmt.Errorf("immich /api/server/ping answered %q", ping.Res)
	}

	var cfg struct {
		IsInitialized bool `json:"isInitialized"`
	}
	if err := c.JSON(ctx, "/api/server/config", nil, &cfg); err != nil {
		return state.Backend{}, fmt.Errorf("immich /api/server/config: %w", err)
	}
	log.Info("immich reachable", "initialised", cfg.IsInitialized)
	if cfg.IsInitialized {
		return state.Backend{}, fmt.Errorf(
			"immich already has an admin account but SoundStorm has no stored credentials for it; " +
				"either restore SoundStorm's state file or reset the immich volumes")
	}

	password, err := generatePassword()
	if err != nil {
		return state.Backend{}, err
	}
	resp, err := c.Do(ctx, httpx.Request{
		Method: http.MethodPost,
		Path:   "/api/auth/admin-sign-up",
		Body:   map[string]string{"email": immichEmail, "password": password, "name": "SoundStorm"},
	})
	if err != nil {
		return state.Backend{}, fmt.Errorf("immich admin sign-up: %w", err)
	}
	if err := resp.Err(); err != nil {
		return state.Backend{}, fmt.Errorf("immich admin sign-up: %w", err)
	}
	log.Info("created immich admin account", "email", immichEmail)

	var login struct {
		AccessToken string `json:"accessToken"`
		UserID      string `json:"userId"`
	}
	resp, err = c.Do(ctx, httpx.Request{
		Method: http.MethodPost,
		Path:   "/api/auth/login",
		Body:   map[string]string{"email": immichEmail, "password": password},
	})
	if err != nil {
		return state.Backend{}, fmt.Errorf("immich login: %w", err)
	}
	if err := resp.Err(); err != nil {
		return state.Backend{}, fmt.Errorf("immich login: %w", err)
	}
	if err := resp.JSON(&login); err != nil || login.AccessToken == "" || login.UserID == "" {
		return state.Backend{}, fmt.Errorf("immich login returned no token")
	}

	// An API key rather than the session token: a session expires, a key does
	// not, and SoundStorm has no one to ask for the password again. The
	// password itself is not kept at all.
	var key struct {
		Secret string `json:"secret"`
	}
	resp, err = c.Do(ctx, httpx.Request{
		Method:  http.MethodPost,
		Path:    "/api/api-keys",
		Headers: map[string]string{"Authorization": "Bearer " + login.AccessToken},
		Body:    map[string]any{"name": accountName, "permissions": []string{"all"}},
	})
	if err != nil {
		return state.Backend{}, fmt.Errorf("immich api key: %w", err)
	}
	if err := resp.Err(); err != nil {
		return state.Backend{}, fmt.Errorf("immich api key: %w", err)
	}
	if err := resp.JSON(&key); err != nil || key.Secret == "" {
		return state.Backend{}, fmt.Errorf("immich returned no api key")
	}

	libraryID, err := ensureImmichLibrary(ctx, c, key.Secret, login.UserID, t.MediaPath, log)
	if err != nil {
		return state.Backend{}, err
	}

	return state.Backend{
		Type:          "immich",
		BaseURL:       c.BaseURL().String(),
		Token:         key.Secret,
		UserID:        login.UserID,
		LibraryID:     libraryID,
		ProvisionedAt: time.Now().UTC(),
	}, nil
}

// ensureImmichLibrary finds or creates the external library covering path,
// switches on watching, and asks for a first scan.
func ensureImmichLibrary(ctx context.Context, c *httpx.Client, apiKey, ownerID, path string, log *slog.Logger) (string, error) {
	authed := map[string]string{"x-api-key": apiKey}

	var existing []struct {
		ID          string   `json:"id"`
		ImportPaths []string `json:"importPaths"`
	}
	resp, err := c.Do(ctx, httpx.Request{Path: "/api/libraries", Headers: authed})
	if err != nil {
		return "", fmt.Errorf("immich libraries: %w", err)
	}
	if err := resp.Err(); err != nil {
		return "", fmt.Errorf("immich libraries: %w", err)
	}
	if err := resp.JSON(&existing); err != nil {
		return "", err
	}

	var id string
	for _, l := range existing {
		for _, p := range l.ImportPaths {
			if p == path {
				id = l.ID
			}
		}
	}
	if id == "" {
		var created struct {
			ID string `json:"id"`
		}
		resp, err := c.Do(ctx, httpx.Request{
			Method:  http.MethodPost,
			Path:    "/api/libraries",
			Headers: authed,
			Body:    map[string]any{"ownerId": ownerID, "name": "Pictures", "importPaths": []string{path}},
		})
		if err != nil {
			return "", fmt.Errorf("immich create library: %w", err)
		}
		if err := resp.Err(); err != nil {
			return "", fmt.Errorf("immich create library: %w", err)
		}
		if err := resp.JSON(&created); err != nil || created.ID == "" {
			return "", fmt.Errorf("immich created a library but returned no id")
		}
		id = created.ID
		log.Info("registered immich library", "path", path, "libraryId", id)
	}

	if err := enableImmichWatching(ctx, c, authed); err != nil {
		// Not fatal: uploads through SoundStorm trigger a scan anyway, and
		// Immich scans every library once a day regardless. What watching
		// adds is files copied in by hand appearing without either.
		log.Warn("could not switch on immich folder watching", "err", err)
	}

	resp, err = c.Do(ctx, httpx.Request{
		Method:  http.MethodPost,
		Path:    "/api/libraries/" + url.PathEscape(id) + "/scan",
		Headers: authed,
	})
	if err != nil {
		return "", fmt.Errorf("immich scan: %w", err)
	}
	return id, resp.Err()
}

// enableImmichWatching turns on Immich's filesystem watcher for external
// libraries, which is off by default. The config endpoint takes the whole
// document back, so it is read, changed in one place and returned whole -
// anything this does not know about passes through untouched.
func enableImmichWatching(ctx context.Context, c *httpx.Client, authed map[string]string) error {
	var config map[string]any
	resp, err := c.Do(ctx, httpx.Request{Path: "/api/system-config", Headers: authed})
	if err != nil {
		return err
	}
	if err := resp.Err(); err != nil {
		return err
	}
	if err := resp.JSON(&config); err != nil {
		return err
	}
	library, ok := config["library"].(map[string]any)
	if !ok {
		return fmt.Errorf("system config has no library section")
	}
	watch, ok := library["watch"].(map[string]any)
	if !ok {
		return fmt.Errorf("system config has no library.watch section")
	}
	if enabled, _ := watch["enabled"].(bool); enabled {
		return nil
	}
	watch["enabled"] = true

	resp, err = c.Do(ctx, httpx.Request{Method: http.MethodPut, Path: "/api/system-config", Headers: authed, Body: config})
	if err != nil {
		return err
	}
	return resp.Err()
}
