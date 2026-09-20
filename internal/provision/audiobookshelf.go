package provision

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/gabehollberg/soundstorm/internal/httpx"
	"github.com/gabehollberg/soundstorm/internal/state"
)

// provisionAudiobookshelf creates SoundStorm's root account on a fresh
// Audiobookshelf and points it at the audiobook folder.
//
// ABS is the most cooperative of the four backends: /status says outright
// whether first-run has happened, and /init does the whole thing in one call.
func provisionAudiobookshelf(ctx context.Context, c *httpx.Client, t Target, log *slog.Logger) (state.Backend, error) {
	var status struct {
		App           string `json:"app"`
		ServerVersion string `json:"serverVersion"`
		IsInit        bool   `json:"isInit"`
	}
	resp, err := c.Do(ctx, httpx.Request{Path: "/status"})
	if err != nil {
		return state.Backend{}, fmt.Errorf("audiobookshelf not reachable: %w", err)
	}
	if err := resp.Err(); err != nil {
		return state.Backend{}, fmt.Errorf("audiobookshelf /status: %w", err)
	}
	if err := resp.JSON(&status); err != nil {
		return state.Backend{}, err
	}
	log.Info("audiobookshelf reachable", "version", status.ServerVersion, "initialised", status.IsInit)

	if status.IsInit {
		// Already set up, with a password we do not hold. Same situation as the
		// other backends: a human has to decide which volume to reset.
		return state.Backend{}, fmt.Errorf(
			"audiobookshelf is already initialised but SoundStorm has no stored credentials for it; " +
				"either restore SoundStorm's state file or reset the audiobookshelf volume")
	}

	password, err := generatePassword()
	if err != nil {
		return state.Backend{}, err
	}

	resp, err = c.Do(ctx, httpx.Request{
		Method: http.MethodPost,
		Path:   "/init",
		Body: map[string]any{
			"newRoot": map[string]string{
				"username": accountName,
				"password": password,
			},
		},
	})
	if err != nil {
		return state.Backend{}, fmt.Errorf("audiobookshelf /init: %w", err)
	}
	if err := resp.Err(); err != nil {
		return state.Backend{}, fmt.Errorf("audiobookshelf /init: %w", err)
	}
	log.Info("created audiobookshelf root account", "username", accountName)

	token, userID, err := audiobookshelfLogin(ctx, c, accountName, password)
	if err != nil {
		return state.Backend{}, err
	}

	libraryID, err := ensureAudiobookshelfLibrary(ctx, c, token, t.MediaPath, log)
	if err != nil {
		return state.Backend{}, err
	}

	return state.Backend{
		Type:          "audiobookshelf",
		BaseURL:       c.BaseURL().String(),
		Token:         token,
		UserID:        userID,
		LibraryID:     libraryID,
		ProvisionedAt: time.Now().UTC(),
	}, nil
}

// audiobookshelfLogin returns a token that does not expire.
//
// /login hands back two: user.accessToken, which carries an exp claim one hour
// out, and user.token, a legacy JWT with no exp at all. SoundStorm stores the
// second deliberately - the first would strand every backend an hour after
// provisioning unless SoundStorm also implemented the refresh dance. If a
// future ABS release drops the legacy token, this is where the refresh flow
// goes.
func audiobookshelfLogin(ctx context.Context, c *httpx.Client, username, password string) (string, string, error) {
	resp, err := c.Do(ctx, httpx.Request{
		Method: http.MethodPost,
		Path:   "/login",
		Body:   map[string]string{"username": username, "password": password},
	})
	if err != nil {
		return "", "", fmt.Errorf("audiobookshelf login: %w", err)
	}
	if err := resp.Err(); err != nil {
		return "", "", fmt.Errorf("audiobookshelf login: %w", err)
	}

	var out struct {
		User struct {
			ID          string `json:"id"`
			Token       string `json:"token"`
			AccessToken string `json:"accessToken"`
		} `json:"user"`
	}
	if err := resp.JSON(&out); err != nil {
		return "", "", err
	}

	token := out.User.Token
	if token == "" {
		token = out.User.AccessToken
	}
	if token == "" || out.User.ID == "" {
		return "", "", fmt.Errorf("audiobookshelf login returned no token")
	}
	return token, out.User.ID, nil
}

// ensureAudiobookshelfLibrary finds or creates the library covering path, then
// asks for a scan.
func ensureAudiobookshelfLibrary(ctx context.Context, c *httpx.Client, token, path string, log *slog.Logger) (string, error) {
	authed := map[string]string{"Authorization": "Bearer " + token}

	var existing struct {
		Libraries []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Folders []struct {
				FullPath string `json:"fullPath"`
			} `json:"folders"`
		} `json:"libraries"`
	}
	resp, err := c.Do(ctx, httpx.Request{Path: "/api/libraries", Headers: authed})
	if err != nil {
		return "", err
	}
	if resp.OK() {
		if err := resp.JSON(&existing); err == nil {
			for _, lib := range existing.Libraries {
				for _, f := range lib.Folders {
					if f.FullPath == path {
						log.Info("audiobookshelf library already present", "name", lib.Name)
						return lib.ID, scanAudiobookshelfLibrary(ctx, c, lib.ID, authed)
					}
				}
			}
		}
	}

	resp, err = c.Do(ctx, httpx.Request{
		Method:  http.MethodPost,
		Path:    "/api/libraries",
		Headers: authed,
		Body: map[string]any{
			"name":      "Audiobooks",
			"folders":   []map[string]string{{"fullPath": path}},
			"mediaType": "book",
			"provider":  "audible",
		},
	})
	if err != nil {
		return "", err
	}
	if err := resp.Err(); err != nil {
		return "", fmt.Errorf("create audiobookshelf library: %w", err)
	}

	var created struct {
		ID string `json:"id"`
	}
	if err := resp.JSON(&created); err != nil {
		return "", err
	}
	if created.ID == "" {
		return "", fmt.Errorf("audiobookshelf returned no library id")
	}
	log.Info("registered audiobookshelf library", "path", path, "libraryId", created.ID)
	return created.ID, scanAudiobookshelfLibrary(ctx, c, created.ID, authed)
}

func scanAudiobookshelfLibrary(ctx context.Context, c *httpx.Client, libraryID string, authed map[string]string) error {
	resp, err := c.Do(ctx, httpx.Request{
		Method:  http.MethodPost,
		Path:    "/api/libraries/" + url.PathEscape(libraryID) + "/scan",
		Headers: authed,
	})
	if err != nil {
		return err
	}
	// A scan that will not start is not fatal: the watcher picks files up
	// anyway, it just takes longer for them to appear.
	if !resp.OK() {
		return nil
	}
	return nil
}

// --- per-user accounts --------------------------------------------------------

// createAudiobookshelfUser makes an ordinary account on Audiobookshelf and
// returns its id and a token for it.
//
// Two things about this were established against 2.36.1 rather than read
// anywhere, and both are load-bearing:
//
// isActive must be sent. Without it an account is created inactive, POST
// returns a perfectly ordinary 200 with a token in it, and that token answers
// Unauthorized to everything. There is nothing in the response to suggest a
// problem.
//
// The response carries a token, so there is no second login call and no reason
// to keep the password. The one generated here is written once and never read
// again - nobody signs in to Audiobookshelf, because nobody can reach it.
func createAudiobookshelfUser(ctx context.Context, c *httpx.Client, adminToken, username string) (state.Identity, error) {
	password, err := generatePassword()
	if err != nil {
		return state.Identity{}, err
	}

	resp, err := c.Do(ctx, httpx.Request{
		Method:  http.MethodPost,
		Path:    "/api/users",
		Headers: map[string]string{"Authorization": "Bearer " + adminToken},
		Body: map[string]any{
			"username": username,
			"password": password,
			"type":     "user",
			"isActive": true,
		},
	})
	if err != nil {
		return state.Identity{}, fmt.Errorf("create audiobookshelf user: %w", err)
	}
	if err := resp.Err(); err != nil {
		return state.Identity{}, fmt.Errorf("create audiobookshelf user: %w", err)
	}

	var created struct {
		User struct {
			ID       string `json:"id"`
			Token    string `json:"token"`
			IsActive bool   `json:"isActive"`
		} `json:"user"`
	}
	if err := resp.JSON(&created); err != nil {
		return state.Identity{}, err
	}
	if created.User.ID == "" || created.User.Token == "" {
		return state.Identity{}, fmt.Errorf("audiobookshelf created a user with no token")
	}
	if !created.User.IsActive {
		// Refusing here rather than storing it: an inactive account's token is
		// rejected by every endpoint, so keeping it would turn one silent
		// failure into a permanent one.
		return state.Identity{}, fmt.Errorf("audiobookshelf created %q inactive; its token would be refused", username)
	}

	return state.Identity{
		Username:  username,
		Password:  password,
		Token:     created.User.Token,
		RemoteID:  created.User.ID,
		CreatedAt: time.Now().UTC(),
	}, nil
}

// deleteAudiobookshelfUser removes an account and, with it, its listening
// history. Deleting invalidates the token immediately.
func deleteAudiobookshelfUser(ctx context.Context, c *httpx.Client, adminToken, remoteID string) error {
	resp, err := c.Do(ctx, httpx.Request{
		Method:  http.MethodDelete,
		Path:    "/api/users/" + url.PathEscape(remoteID),
		Headers: map[string]string{"Authorization": "Bearer " + adminToken},
	})
	if err != nil {
		return err
	}
	return resp.Err()
}
