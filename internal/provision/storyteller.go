package provision

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/httpx"
	"github.com/GabrielHollberg/soundstorm/internal/state"
)

// storytellerEmail is the account SoundStorm creates on Storyteller, which
// wants an email address and sends nothing to it. .invalid, as for Immich.
const storytellerEmail = accountName + "@soundstorm.invalid"

// Storyteller's first-run form is a Next.js server action rather than an API
// call, but it works as a plain form post, so it can be driven like one. The
// action's id changes with every Storyteller build, so it is read off the page
// each time rather than written down here.
var storytellerAction = regexp.MustCompile(`name="(\$ACTION_ID_[0-9a-f]+)"`)

// provisionStoryteller creates SoundStorm's account on a fresh Storyteller,
// trades a login for a long-lived session, and sets it up for read-along:
// synced books written inside its own data folder (never beside the source,
// which is the library), chapters left uncut so every timing is relative to a
// chapter start, a transcription model that aligns real narration, and its
// OPDS catalogue off, since nothing reads it.
//
// Every step was checked by hand against web-v2.14.21 first.
func provisionStoryteller(ctx context.Context, c *httpx.Client, log *slog.Logger) (state.Backend, error) {
	base := c.BaseURL()
	// Its own client: the setup steps answer with redirects that carry what
	// is needed, and must not be followed.
	hc := &http.Client{
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	at := func(p string) string { return base.ResolveReference(&url.URL{Path: p}).String() }

	resp, err := c.Do(ctx, httpx.Request{Path: "/api/health"})
	if err != nil {
		return state.Backend{}, fmt.Errorf("storyteller not reachable: %w", err)
	}
	if err := resp.Err(); err != nil {
		return state.Backend{}, fmt.Errorf("storyteller not ready: %w", err)
	}

	page, err := fetch(ctx, hc, http.MethodGet, at("/init"), nil, "")
	if err != nil {
		return state.Backend{}, fmt.Errorf("storyteller setup page: %w", err)
	}
	action := storytellerAction.FindSubmatch(page.body)
	if page.status != http.StatusOK || action == nil {
		return state.Backend{}, fmt.Errorf(
			"storyteller already has an account but SoundStorm has no stored credentials for it; " +
				"either restore SoundStorm's state file or reset the storyteller volume")
	}

	password, err := generatePassword()
	if err != nil {
		return state.Backend{}, err
	}
	var form bytes.Buffer
	mw := multipart.NewWriter(&form)
	for k, v := range map[string]string{
		string(action[1]): "", "email": storytellerEmail, "fullName": "SoundStorm",
		"username": accountName, "password": password,
	} {
		_ = mw.WriteField(k, v)
	}
	mw.Close()
	created, err := fetch(ctx, hc, http.MethodPost, at("/init"), form.Bytes(), mw.FormDataContentType())
	if err != nil {
		return state.Backend{}, fmt.Errorf("storyteller create account: %w", err)
	}
	if created.status != http.StatusSeeOther {
		return state.Backend{}, fmt.Errorf("storyteller create account: answered %d", created.status)
	}
	log.Info("created storyteller account", "username", accountName)

	// A session lasts thirty days; the app token route trades one for a
	// session that outlives the install, as Storyteller's own phone app does.
	login, err := fetch(ctx, hc, http.MethodPost, at("/api/v2/token"),
		[]byte(url.Values{"usernameOrEmail": {accountName}, "password": {password}}.Encode()),
		"application/x-www-form-urlencoded")
	if err != nil || login.status != http.StatusOK {
		return state.Backend{}, fmt.Errorf("storyteller login failed: %v %d", err, login.status)
	}
	var session struct {
		AccessToken string `json:"access_token"`
	}
	if json.Unmarshal(login.body, &session) != nil || session.AccessToken == "" {
		return state.Backend{}, fmt.Errorf("storyteller login returned no token")
	}
	token := session.AccessToken

	// This route reads only the session cookie, not a bearer token: checked,
	// a bearer is sent to the login page.
	short, err := fetch(ctx, hc, http.MethodGet, at("/api/v2/token/app"), nil, "", map[string]string{"Cookie": "st_token=" + token})
	if err == nil && short.status == http.StatusFound {
		if u, perr := url.Parse(short.location); perr == nil && u.Query().Get("token") != "" {
			body, _ := json.Marshal(map[string]string{"token": u.Query().Get("token")})
			long, lerr := fetch(ctx, hc, http.MethodPost, at("/api/v2/token/app"), body, "application/json")
			var lasting struct {
				AccessToken string `json:"access_token"`
			}
			if lerr == nil && long.status == http.StatusOK &&
				json.Unmarshal(long.body, &lasting) == nil && lasting.AccessToken != "" {
				token = lasting.AccessToken
			}
		}
	}
	if token == session.AccessToken {
		// Not fatal: the password is kept, and a rejected token re-provisions
		// through reconnect's ordinary path. But say so.
		log.Warn("storyteller gave no long-lived session; using a thirty-day one")
	}

	resp, err = c.Do(ctx, httpx.Request{
		Method:  http.MethodPut,
		Path:    "/api/v2/settings",
		Headers: map[string]string{"Authorization": "Bearer " + token},
		Body: map[string]any{
			"readaloudLocationType":    "INTERNAL",
			"maxTrackLength":           10000,
			"whisperModel":             "base.en",
			"cleanCacheAfterReadaloud": true,
			"opdsEnabled":              false,
		},
	})
	if err != nil {
		return state.Backend{}, fmt.Errorf("storyteller settings: %w", err)
	}
	if err := resp.Err(); err != nil {
		return state.Backend{}, fmt.Errorf("storyteller settings: %w", err)
	}

	return state.Backend{
		Type:          "storyteller",
		BaseURL:       base.String(),
		Username:      accountName,
		Password:      password,
		Token:         token,
		ProvisionedAt: time.Now().UTC(),
	}, nil
}

type answer struct {
	status   int
	body     []byte
	location string
}

// fetch is one request that does not follow redirects, for the two setup
// steps whose answer is the redirect.
func fetch(ctx context.Context, hc *http.Client, method, target string, body []byte, contentType string, headers ...map[string]string) (answer, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, r)
	if err != nil {
		return answer{}, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for _, h := range headers {
		for k, v := range h {
			req.Header.Set(k, v)
		}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return answer{}, httpx.Redact(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return answer{}, err
	}
	return answer{status: resp.StatusCode, body: data, location: strings.TrimSpace(resp.Header.Get("Location"))}, nil
}
