package provision

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/gabehollberg/atrium/internal/httpx"
	"github.com/gabehollberg/atrium/internal/state"
)

// jellyfinAuthHeader identifies atrium to Jellyfin before we hold a token.
// Jellyfin rejects authentication requests that carry no client identity.
const jellyfinAuthHeader = `MediaBrowser Client="atrium", Device="atrium", DeviceId="atrium-gateway", Version="0.1.0"`

// jellyfinTokenHeader is the authenticated form. Jellyfin 12 accepts the token
// only here - not in X-Emby-Token, and not in an api_key query parameter.
func jellyfinTokenHeader(token string) string {
	return `MediaBrowser Client="atrium", Device="atrium", DeviceId="atrium-gateway", Version="0.1.0", Token="` + token + `"`
}

// provisionJellyfin walks Jellyfin's startup wizard, then logs in as the
// account it just created and registers the movie library.
//
// Jellyfin is the best-behaved of the backends here: the wizard the web UI
// shows on first run is a plain REST API that stops accepting calls once setup
// is complete, so driving it is both easy and safe to retry.
func provisionJellyfin(ctx context.Context, c *httpx.Client, t Target, log *slog.Logger) (state.Backend, error) {
	c.SetHeader("Authorization", jellyfinAuthHeader)
	c.SetHeader("Accept", "application/json")

	// /System/Info/Public needs no auth and tells us whether the wizard has
	// already run - which it will have on any restart after the first.
	var public struct {
		Version                string `json:"Version"`
		ServerName             string `json:"ServerName"`
		StartupWizardCompleted bool   `json:"StartupWizardCompleted"`
	}
	resp, err := c.Do(ctx, httpx.Request{Path: "/System/Info/Public"})
	if err != nil {
		return state.Backend{}, fmt.Errorf("jellyfin not reachable: %w", err)
	}
	if !resp.OK() {
		return state.Backend{}, fmt.Errorf("jellyfin /System/Info/Public returned %d: %s",
			resp.Status, httpx.Snippet(resp.Body))
	}
	if err := resp.JSON(&public); err != nil {
		return state.Backend{}, err
	}
	log.Info("jellyfin reachable", "version", public.Version, "wizardDone", public.StartupWizardCompleted)

	password, err := generatePassword()
	if err != nil {
		return state.Backend{}, err
	}

	if !public.StartupWizardCompleted {
		if err := runJellyfinWizard(ctx, c, password, log); err != nil {
			return state.Backend{}, err
		}
	} else {
		// The wizard already ran, so the account exists with a password we do
		// not have. Nothing to do but say so clearly.
		return state.Backend{}, fmt.Errorf(
			"jellyfin setup is already complete but atrium has no stored credentials for it; " +
				"either restore atrium's state file or reset the jellyfin volume")
	}

	token, userID, err := jellyfinLogin(ctx, c, accountName, password)
	if err != nil {
		return state.Backend{}, err
	}
	log.Info("authenticated to jellyfin", "userId", userID)

	// Now that we hold a token, point Jellyfin at the movie folder.
	if t.MediaPath != "" {
		if err := ensureJellyfinLibrary(ctx, c, token, t.MediaPath, log); err != nil {
			// A missing library is not fatal to provisioning: the credentials
			// are good and someone can add the folder by hand. Searches will
			// just come back empty until then.
			log.Warn("could not register jellyfin library", "err", err)
		}
	}

	return state.Backend{
		Type:          "jellyfin",
		BaseURL:       c.BaseURL().String(),
		Token:         token,
		UserID:        userID,
		ProvisionedAt: time.Now().UTC(),
	}, nil
}

// runJellyfinWizard performs the first-run sequence the setup UI would.
func runJellyfinWizard(ctx context.Context, c *httpx.Client, password string, log *slog.Logger) error {
	steps := []struct {
		name string
		req  httpx.Request
	}{
		{"locale", httpx.Request{
			Method: http.MethodPost,
			Path:   "/Startup/Configuration",
			Body: map[string]string{
				"UICulture":                 "en-US",
				"MetadataCountryCode":       "US",
				"PreferredMetadataLanguage": "en",
			},
		}},
		// Jellyfin expects the wizard's user step to be read before it is
		// written; skipping the GET leaves some versions in a state where the
		// POST is rejected.
		{"read user", httpx.Request{Path: "/Startup/User"}},
		{"create user", httpx.Request{
			Method: http.MethodPost,
			Path:   "/Startup/User",
			Body: map[string]string{
				"Name":     accountName,
				"Password": password,
			},
		}},
		{"remote access", httpx.Request{
			Method: http.MethodPost,
			Path:   "/Startup/RemoteAccess",
			Body: map[string]bool{
				"EnableRemoteAccess":         true,
				"EnableAutomaticPortMapping": false,
			},
		}},
		{"complete", httpx.Request{Method: http.MethodPost, Path: "/Startup/Complete"}},
	}

	for _, step := range steps {
		resp, err := c.Do(ctx, step.req)
		if err != nil {
			return fmt.Errorf("jellyfin wizard %s: %w", step.name, err)
		}
		if !resp.OK() {
			return fmt.Errorf("jellyfin wizard %s returned %d: %s",
				step.name, resp.Status, httpx.Snippet(resp.Body))
		}
		log.Info("jellyfin wizard step done", "step", step.name)
	}
	return nil
}

// jellyfinLogin exchanges the generated password for a long-lived access token.
func jellyfinLogin(ctx context.Context, c *httpx.Client, username, password string) (string, string, error) {
	resp, err := c.Do(ctx, httpx.Request{
		Method: http.MethodPost,
		Path:   "/Users/AuthenticateByName",
		Body: map[string]string{
			"Username": username,
			"Pw":       password,
		},
	})
	if err != nil {
		return "", "", fmt.Errorf("jellyfin authenticate: %w", err)
	}
	if !resp.OK() {
		return "", "", fmt.Errorf("jellyfin authenticate returned %d: %s",
			resp.Status, httpx.Snippet(resp.Body))
	}

	var out struct {
		AccessToken string `json:"AccessToken"`
		User        struct {
			ID string `json:"Id"`
		} `json:"User"`
	}
	if err := resp.JSON(&out); err != nil {
		return "", "", err
	}
	if out.AccessToken == "" || out.User.ID == "" {
		return "", "", fmt.Errorf("jellyfin authenticate returned no token")
	}
	return out.AccessToken, out.User.ID, nil
}

// ensureJellyfinLibrary adds a movie library at path, unless one already covers
// it, then asks for a scan.
func ensureJellyfinLibrary(ctx context.Context, c *httpx.Client, token, path string, log *slog.Logger) error {
	authed := map[string]string{"Authorization": jellyfinTokenHeader(token)}

	var existing []struct {
		Name      string   `json:"Name"`
		Locations []string `json:"Locations"`
	}
	resp, err := c.Do(ctx, httpx.Request{Path: "/Library/VirtualFolders", Headers: authed})
	if err != nil {
		return err
	}
	if resp.OK() {
		if err := resp.JSON(&existing); err == nil {
			for _, lib := range existing {
				for _, loc := range lib.Locations {
					if loc == path {
						log.Info("jellyfin library already present", "name", lib.Name)
						return triggerJellyfinScan(ctx, c, authed)
					}
				}
			}
		}
	}

	resp, err = c.Do(ctx, httpx.Request{
		Method: http.MethodPost,
		Path:   "/Library/VirtualFolders",
		Params: url.Values{
			"name":           {"Movies"},
			"collectionType": {"movies"},
			"refreshLibrary": {"true"},
		},
		Headers: authed,
		Body: map[string]any{
			"LibraryOptions": map[string]any{
				"PathInfos":             []map[string]string{{"Path": path}},
				"EnableRealtimeMonitor": true,
			},
		},
	})
	if err != nil {
		return err
	}
	if !resp.OK() {
		return fmt.Errorf("add library returned %d: %s", resp.Status, httpx.Snippet(resp.Body))
	}
	log.Info("registered jellyfin movie library", "path", path)
	return triggerJellyfinScan(ctx, c, authed)
}

func triggerJellyfinScan(ctx context.Context, c *httpx.Client, authed map[string]string) error {
	resp, err := c.Do(ctx, httpx.Request{
		Method:  http.MethodPost,
		Path:    "/Library/Refresh",
		Headers: authed,
	})
	if err != nil {
		return err
	}
	if !resp.OK() {
		return fmt.Errorf("library refresh returned %d: %s", resp.Status, httpx.Snippet(resp.Body))
	}
	return nil
}
