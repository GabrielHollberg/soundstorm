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
func provisionImmich(ctx context.Context, c *httpx.Client, t Target, sec secrets, log *slog.Logger) (state.Backend, error) {
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
	log.Info("immich reachable", "initialized", cfg.IsInitialized)
	password, kept, err := sec("password")
	if err != nil {
		return state.Backend{}, err
	}
	if cfg.IsInitialized && !kept {
		return state.Backend{}, fmt.Errorf(
			"immich already has an admin account but SoundStorm has no stored credentials for it; " +
				"either restore SoundStorm's state file or reset the immich volumes")
	}
	if cfg.IsInitialized {
		// Made by an earlier attempt that failed after it (signing in, the
		// key, the library): the kept password signs in and setup goes on.
		log.Info("immich admin made by an earlier attempt; carrying on")
	} else {
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
	}

	var login struct {
		AccessToken string `json:"accessToken"`
		UserID      string `json:"userId"`
	}
	resp, err := c.Do(ctx, httpx.Request{
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

// createImmichMember makes a member's own Immich account and their library of
// their own folder, and starts its first scan. Checked against Immich 3.2.2:
// the administrator makes the account (/api/admin/users) and its library,
// owned by the member, overlapping the administrator's library of the whole
// folder; signing in as the member is the only way to mint their API key.
// The password is generated, used once and kept, like Audiobookshelf's.
func createImmichMember(ctx context.Context, c *httpx.Client, sec secrets, adminKey, username, name, folder string) (state.Identity, error) {
	admin := map[string]string{"x-api-key": adminKey}
	email := username + "@soundstorm.invalid"
	password, kept, err := sec("password")
	if err != nil {
		return state.Identity{}, err
	}
	var user struct {
		ID string `json:"id"`
	}
	resp, err := c.Do(ctx, httpx.Request{
		Method:  http.MethodPost,
		Path:    "/api/admin/users",
		Headers: admin,
		Body:    map[string]any{"email": email, "password": password, "name": name},
	})
	switch {
	case err == nil && resp.Err() == nil:
		if err := resp.JSON(&user); err != nil || user.ID == "" {
			return state.Identity{}, fmt.Errorf("create photo account: no id in the answer")
		}
	case kept:
		// Made by an earlier try that never finished (its answer lost, or a
		// later step failed): the kept password signs in to it below, and
		// the sign-in says whose it is.
	case err != nil:
		return state.Identity{}, fmt.Errorf("create photo account: %w", err)
	default:
		return state.Identity{}, fmt.Errorf("create photo account: %w", resp.Err())
	}

	resp, err = c.Do(ctx, httpx.Request{
		Method: http.MethodPost,
		Path:   "/api/auth/login",
		Body:   map[string]any{"email": email, "password": password},
	})
	if err != nil {
		return state.Identity{}, fmt.Errorf("sign in to photo account: %w", err)
	}
	if err := resp.Err(); err != nil {
		return state.Identity{}, fmt.Errorf("sign in to photo account: %w", err)
	}
	var login struct {
		AccessToken string `json:"accessToken"`
		UserID      string `json:"userId"`
	}
	if err := resp.JSON(&login); err != nil || login.AccessToken == "" {
		return state.Identity{}, fmt.Errorf("sign in to photo account: no token")
	}
	if user.ID == "" {
		user.ID = login.UserID
	}
	if user.ID == "" {
		return state.Identity{}, fmt.Errorf("sign in to photo account: no id in the answer")
	}
	resp, err = c.Do(ctx, httpx.Request{
		Method:  http.MethodPost,
		Path:    "/api/api-keys",
		Headers: map[string]string{"Authorization": "Bearer " + login.AccessToken},
		Body:    map[string]any{"name": accountName, "permissions": []string{"all"}},
	})
	if err != nil {
		return state.Identity{}, fmt.Errorf("photo account key: %w", err)
	}
	if err := resp.Err(); err != nil {
		return state.Identity{}, fmt.Errorf("photo account key: %w", err)
	}
	var key struct {
		Secret string `json:"secret"`
	}
	if err := resp.JSON(&key); err != nil || key.Secret == "" {
		return state.Identity{}, fmt.Errorf("photo account key: no secret")
	}

	resp, err = c.Do(ctx, httpx.Request{
		Method:  http.MethodPost,
		Path:    "/api/libraries",
		Headers: admin,
		Body:    map[string]any{"ownerId": user.ID, "name": name, "importPaths": []string{folder}},
	})
	if err != nil {
		return state.Identity{}, fmt.Errorf("photo library: %w", err)
	}
	if err := resp.Err(); err != nil {
		return state.Identity{}, fmt.Errorf("photo library: %w", err)
	}
	var lib struct {
		ID string `json:"id"`
	}
	if err := resp.JSON(&lib); err != nil || lib.ID == "" {
		return state.Identity{}, fmt.Errorf("photo library: no id")
	}
	// The first scan; after it Immich's folder watching picks up new files.
	if resp, err := c.Do(ctx, httpx.Request{
		Method: http.MethodPost, Path: "/api/libraries/" + url.PathEscape(lib.ID) + "/scan", Headers: admin,
	}); err == nil {
		_ = resp.Err()
	}
	return state.Identity{
		Username:  email,
		Password:  password,
		Token:     key.Secret,
		RemoteID:  user.ID,
		LibraryID: lib.ID,
		CreatedAt: time.Now().UTC(),
	}, nil
}

// deleteImmichMember removes a member's Immich account and with it Immich's
// record of their library. The photos themselves stay where they are: Immich
// mounts the folder read-only.
func deleteImmichMember(ctx context.Context, c *httpx.Client, adminKey, remoteID string) error {
	resp, err := c.Do(ctx, httpx.Request{
		Method:  http.MethodDelete,
		Path:    "/api/admin/users/" + url.PathEscape(remoteID),
		Headers: map[string]string{"x-api-key": adminKey},
		Body:    map[string]any{"force": true},
	})
	if err != nil {
		return err
	}
	return resp.Err()
}
