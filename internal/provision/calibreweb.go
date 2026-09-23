package provision

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/httpx"
	"github.com/GabrielHollberg/soundstorm/internal/state"
)

// Calibre-Web ships with a published default login. Everyone knows it, which is
// precisely why SoundStorm can use it without a human being involved.
const (
	calibreWebUser     = "admin"
	calibreWebPassword = "admin123"
)

// csrfPattern pulls Flask-WTF's hidden token out of a rendered form.
var csrfPattern = regexp.MustCompile(`name="csrf_token"[^>]*value="([^"]+)"`)

// provisionCalibreWeb points Calibre-Web at the ebook library.
//
// This is the least pleasant of the four provisioners, and the reason is worth
// recording: Calibre-Web has no configuration API. Its setup is a Flask form
// with a session cookie and a CSRF token, so SoundStorm has to drive it the way
// a browser would - fetch the page, scrape the token, post the form. That is
// brittle across releases in a way the other three are not, and if this breaks
// after an upgrade, a changed field name is the first thing to check.
//
// Known limitation: the default password is NOT rotated. Both the admin form
// and the self-service form accept the POST, return success, and leave the
// password unchanged - verified against calibre-web running under linuxserver's
// image. Posting harder risks stripping the admin role, because the same form
// carries every permission as a checkbox and a partial post clears them.
//
// This used to be mitigated by network isolation - Calibre-Web ran in
// SoundStorm's own compose file, on a network only SoundStorm could reach.
// That container is gone; every remaining use of this code points
// SOUNDSTORM_CALIBREWEB_URL at "a Calibre server running elsewhere" (see
// CLAUDE.md), which SoundStorm neither runs nor controls the exposure of. So
// the well-known default credential this provisioner logs in with is only as
// safe as whatever network that server actually sits on - the warning below
// says that plainly rather than repeating the old, no-longer-true claim.
func provisionCalibreWeb(ctx context.Context, c *httpx.Client, t Target, log *slog.Logger) (state.Backend, error) {
	if err := c.EnableCookies(); err != nil {
		return state.Backend{}, err
	}

	if err := calibreWebLogin(ctx, c); err != nil {
		return state.Backend{}, err
	}
	log.Info("signed in to calibre-web", "username", calibreWebUser)

	if err := calibreWebSetLibrary(ctx, c, t.MediaPath, log); err != nil {
		return state.Backend{}, err
	}

	// The OPDS feed is what SoundStorm actually consumes, and it authenticates
	// separately with HTTP Basic. Check it before declaring success, so a
	// working login with a broken catalog is caught here rather than at the
	// first search.
	if err := calibreWebCheckOPDS(ctx, c); err != nil {
		return state.Backend{}, err
	}

	log.Warn("calibre-web is using its published default password (admin/admin123), and SoundStorm did not " +
		"set this server up or change that - if it is reachable by anything other than SoundStorm, change " +
		"the password in its own UI now")

	return state.Backend{
		Type:          "calibreweb",
		BaseURL:       c.BaseURL().String(),
		Username:      calibreWebUser,
		Password:      calibreWebPassword,
		ProvisionedAt: time.Now().UTC(),
	}, nil
}

// calibreWebLogin performs the browser login dance.
func calibreWebLogin(ctx context.Context, c *httpx.Client) error {
	token, err := calibreWebToken(ctx, c, "/login")
	if err != nil {
		return fmt.Errorf("calibre-web login page: %w", err)
	}

	resp, err := c.Do(ctx, httpx.Request{
		Method: http.MethodPost,
		Path:   "/login",
		Form: url.Values{
			"username":   {calibreWebUser},
			"password":   {calibreWebPassword},
			"csrf_token": {token},
			"submit":     {""},
			"next":       {"/"},
		},
	})
	if err != nil {
		return fmt.Errorf("calibre-web login: %w", err)
	}
	if err := resp.Err(); err != nil {
		return fmt.Errorf("calibre-web login: %w", err)
	}
	return nil
}

// calibreWebSetLibrary tells Calibre-Web where the Calibre database lives.
func calibreWebSetLibrary(ctx context.Context, c *httpx.Client, path string, log *slog.Logger) error {
	token, err := calibreWebToken(ctx, c, "/admin/dbconfig")
	if err != nil {
		return fmt.Errorf("calibre-web dbconfig page: %w", err)
	}

	resp, err := c.Do(ctx, httpx.Request{
		Method: http.MethodPost,
		Path:   "/admin/dbconfig",
		Form: url.Values{
			"config_calibre_dir": {path},
			"csrf_token":         {token},
			"submit":             {""},
		},
	})
	if err != nil {
		return fmt.Errorf("calibre-web set library: %w", err)
	}
	if err := resp.Err(); err != nil {
		return fmt.Errorf("calibre-web set library: %w", err)
	}
	log.Info("pointed calibre-web at the ebook library", "path", path)
	return nil
}

// calibreWebToken fetches a page and extracts its CSRF token.
func calibreWebToken(ctx context.Context, c *httpx.Client, path string) (string, error) {
	resp, err := c.Do(ctx, httpx.Request{Path: path})
	if err != nil {
		return "", err
	}
	if err := resp.Err(); err != nil {
		return "", err
	}
	match := csrfPattern.FindSubmatch(resp.Body)
	if match == nil {
		return "", fmt.Errorf("no csrf_token in %s (calibre-web may have changed its forms)", path)
	}
	return string(match[1]), nil
}

// calibreWebCheckOPDS confirms the catalog answers over HTTP Basic.
func calibreWebCheckOPDS(ctx context.Context, c *httpx.Client) error {
	resp, err := c.Do(ctx, httpx.Request{
		Path: "/opds",
		// Explicitly Basic rather than the session cookie: this is the path the
		// adapter will use, so it is the one worth testing.
		Headers: map[string]string{"Authorization": basicAuth(calibreWebUser, calibreWebPassword)},
	})
	if err != nil {
		return fmt.Errorf("calibre-web opds: %w", err)
	}
	if err := resp.Err(); err != nil {
		return fmt.Errorf("calibre-web opds: %w", err)
	}
	return nil
}
