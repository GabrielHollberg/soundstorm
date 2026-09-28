// Package plex brings somebody's playlists across from Plex - what Plexamp
// plays from - so a household moving to SoundStorm keeps them.
//
// It signs in the way apps do on Plex: SoundStorm asks plex.tv for a PIN, the
// person signs in on Plex's own page with it, and plex.tv hands SoundStorm a
// token for that account. SoundStorm never sees a Plex password. With the
// token it lists the account's servers (plex.tv knows every address each one
// answers on), finds one that answers, and reads the audio playlists and their
// songs. The token is held in memory for the import and then forgotten.
//
// Nothing here talks to plex.tv unless somebody asks to import.
package plex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultAPI is plex.tv's account API.
const DefaultAPI = "https://plex.tv"

// DefaultAuthPage is where the person signs in.
const DefaultAuthPage = "https://app.plex.tv/auth"

// maxBody bounds anything read from plex.tv or a Plex server: a playlist of
// 5,000 songs is a few megabytes of JSON.
const maxBody = 16 << 20

// Client talks to plex.tv and to Plex servers on one person's behalf.
type Client struct {
	// API and AuthPage default to plex.tv's; tests point them elsewhere.
	API      string
	AuthPage string
	// ClientID names this install to Plex, which lists it among the
	// account's signed-in apps. It must stay the same across restarts.
	ClientID string
	// Version is SoundStorm's, shown beside that name.
	Version string
	HTTP    *http.Client
	// AllowHost decides whether a server address plex.tv gave may be
	// dialed; nil means AllowedHost.
	AllowHost func(host string) bool
}

// ErrNotSignedIn is a PIN the person has not finished signing in with yet.
var ErrNotSignedIn = errors.New("not signed in to Plex yet")

// ErrPinExpired is a PIN plex.tv no longer knows: it lasts a quarter hour.
var ErrPinExpired = errors.New("the Plex sign-in expired; start again")

// Pin is a sign-in in progress.
type Pin struct {
	ID   int    `json:"id"`
	Code string `json:"code"`
}

// Server is one Plex Media Server the account can reach.
type Server struct {
	ID          string // plex.tv's clientIdentifier
	Name        string
	Owned       bool
	token       string
	connections []connection
}

type connection struct {
	URI      string `json:"uri"`
	Address  string `json:"address"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
	Local    bool   `json:"local"`
	Relay    bool   `json:"relay"`
}

// Playlist is an audio playlist on a server.
type Playlist struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Count int    `json:"count"`
	Smart bool   `json:"smart"`
}

// Track is one song in a playlist, as Plex describes it.
type Track struct {
	Title       string
	Artist      string // the track's own artist, or the album's
	AlbumArtist string
	Album       string
	Seconds     float64
	File        string // the path on the Plex server's disk
}

func (c *Client) api() string {
	if c.API != "" {
		return strings.TrimRight(c.API, "/")
	}
	return DefaultAPI
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 20 * time.Second}
}

func (c *Client) headers(req *http.Request, token string) {
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Plex-Product", "SoundStorm")
	req.Header.Set("X-Plex-Client-Identifier", c.ClientID)
	if c.Version != "" {
		req.Header.Set("X-Plex-Version", c.Version)
	}
	if token != "" {
		req.Header.Set("X-Plex-Token", token)
	}
}

func (c *Client) getJSON(ctx context.Context, client *http.Client, method, rawURL, token string, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		return 0, err
	}
	c.headers(req, token)
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return resp.StatusCode, fmt.Errorf("plex answered %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(out); err != nil {
		return resp.StatusCode, fmt.Errorf("plex answered something unreadable: %w", err)
	}
	return resp.StatusCode, nil
}

// NewPin starts a sign-in.
func (c *Client) NewPin(ctx context.Context) (Pin, error) {
	var p Pin
	_, err := c.getJSON(ctx, c.http(), http.MethodPost, c.api()+"/api/v2/pins?strong=true", "", &p)
	if err == nil && (p.ID == 0 || p.Code == "") {
		err = errors.New("plex gave no sign-in code")
	}
	return p, err
}

// AuthURL is Plex's sign-in page for pin. forward, if set, is where Plex
// sends the browser once signed in.
func (c *Client) AuthURL(pin Pin, forward string) string {
	page := c.AuthPage
	if page == "" {
		page = DefaultAuthPage
	}
	q := url.Values{}
	q.Set("clientID", c.ClientID)
	q.Set("code", pin.Code)
	q.Set("context[device][product]", "SoundStorm")
	if forward != "" {
		q.Set("forwardUrl", forward)
	}
	// Plex's sign-in page reads its parameters from the fragment.
	return page + "#?" + q.Encode()
}

// Token answers the account token once the person has signed in with pin.
func (c *Client) Token(ctx context.Context, pin Pin) (string, error) {
	var got struct {
		AuthToken string `json:"authToken"`
	}
	status, err := c.getJSON(ctx, c.http(), http.MethodGet, c.api()+"/api/v2/pins/"+strconv.Itoa(pin.ID)+"?code="+url.QueryEscape(pin.Code), "", &got)
	if status == http.StatusNotFound {
		return "", ErrPinExpired
	}
	if err != nil {
		return "", err
	}
	if got.AuthToken == "" {
		return "", ErrNotSignedIn
	}
	return got.AuthToken, nil
}

// Servers lists the Plex Media Servers the account can reach.
func (c *Client) Servers(ctx context.Context, token string) ([]Server, error) {
	var resources []struct {
		Name             string       `json:"name"`
		ClientIdentifier string       `json:"clientIdentifier"`
		Provides         string       `json:"provides"`
		Owned            bool         `json:"owned"`
		AccessToken      string       `json:"accessToken"`
		Connections      []connection `json:"connections"`
	}
	if _, err := c.getJSON(ctx, c.http(), http.MethodGet, c.api()+"/api/v2/resources?includeHttps=1&includeRelay=1", token, &resources); err != nil {
		return nil, err
	}
	var out []Server
	for _, r := range resources {
		if !strings.Contains(","+r.Provides+",", ",server,") || r.ClientIdentifier == "" {
			continue
		}
		tok := r.AccessToken
		if tok == "" {
			tok = token
		}
		out = append(out, Server{ID: r.ClientIdentifier, Name: r.Name, Owned: r.Owned, token: tok, connections: r.Connections})
	}
	return out, nil
}

// AllowedHost is the default rule for dialing a server address plex.tv gave:
// Plex's own *.plex.direct names, or an IP address that is not this machine,
// link-local (where cloud metadata lives) or unspecified. Names on the
// compose network (jellyfin, navidrome...) are never among them.
func AllowedHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if strings.HasSuffix(host, ".plex.direct") {
		return true
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	return !addr.IsLoopback() && !addr.IsLinkLocalUnicast() && !addr.IsLinkLocalMulticast() &&
		!addr.IsUnspecified() && !addr.IsMulticast()
}

// candidates are the addresses to try for a server, best first: on the home
// network before across the internet, Plex's relay last. A local address is
// also tried as plain http to its IP, since a router that refuses to resolve
// plex.direct names (rebinding protection) is common.
func (c *Client) candidates(srv Server) []string {
	allow := c.AllowHost
	if allow == nil {
		allow = AllowedHost
	}
	rank := func(cn connection) int {
		switch {
		case cn.Relay:
			return 2
		case cn.Local:
			return 0
		}
		return 1
	}
	seen := map[string]bool{}
	var out []string
	add := func(raw string) {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || !allow(u.Hostname()) || seen[raw] {
			return
		}
		seen[raw] = true
		out = append(out, strings.TrimRight(raw, "/"))
	}
	for r := 0; r <= 2; r++ {
		for _, cn := range srv.connections {
			if rank(cn) != r {
				continue
			}
			add(cn.URI)
			if cn.Local && cn.Address != "" && cn.Port > 0 {
				add("http://" + net.JoinHostPort(cn.Address, strconv.Itoa(cn.Port)))
			}
		}
	}
	return out
}

// Open finds an address the server answers on, trying each for a few
// seconds, and answers a handle to it.
func (c *Client) Open(ctx context.Context, srv Server) (*Conn, error) {
	// A server's address is plex.tv's say-so, not ours: no redirects, so a
	// server cannot send SoundStorm somewhere the rule above would refuse.
	client := &http.Client{
		Timeout:       60 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	if c.HTTP != nil && c.HTTP.Transport != nil {
		client.Transport = c.HTTP.Transport
	}
	for _, base := range c.candidates(srv) {
		try, cancel := context.WithTimeout(ctx, 5*time.Second)
		var probe struct{}
		_, err := c.getJSON(try, client, http.MethodGet, base+"/identity", srv.token, &probe)
		cancel()
		if err == nil {
			return &Conn{c: c, http: client, base: base, token: srv.token}, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, fmt.Errorf("could not reach the Plex server %q from here", srv.Name)
}

// Conn is a server that answered.
type Conn struct {
	c     *Client
	http  *http.Client
	base  string
	token string
}

// Playlists lists the server's audio playlists.
func (s *Conn) Playlists(ctx context.Context) ([]Playlist, error) {
	var got struct {
		MediaContainer struct {
			Metadata []struct {
				RatingKey string `json:"ratingKey"`
				Title     string `json:"title"`
				LeafCount int    `json:"leafCount"`
				Smart     bool   `json:"smart"`
			} `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if _, err := s.c.getJSON(ctx, s.http, http.MethodGet, s.base+"/playlists?playlistType=audio", s.token, &got); err != nil {
		return nil, err
	}
	out := []Playlist{}
	for _, m := range got.MediaContainer.Metadata {
		if m.RatingKey == "" {
			continue
		}
		out = append(out, Playlist{ID: m.RatingKey, Title: m.Title, Count: m.LeafCount, Smart: m.Smart})
	}
	return out, nil
}

// maxTracks is as many as a SoundStorm playlist holds.
const maxTracks = 5000

// Tracks lists a playlist's songs, in order.
func (s *Conn) Tracks(ctx context.Context, playlistID string) ([]Track, error) {
	var got struct {
		MediaContainer struct {
			Metadata []struct {
				Title            string `json:"title"`
				GrandparentTitle string `json:"grandparentTitle"` // album artist
				OriginalTitle    string `json:"originalTitle"`    // track artist, when different
				ParentTitle      string `json:"parentTitle"`      // album
				Duration         int64  `json:"duration"`         // ms
				Media            []struct {
					Part []struct {
						File string `json:"file"`
					} `json:"Part"`
				} `json:"Media"`
			} `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	u := s.base + "/playlists/" + url.PathEscape(playlistID) + "/items?X-Plex-Container-Start=0&X-Plex-Container-Size=" + strconv.Itoa(maxTracks)
	if _, err := s.c.getJSON(ctx, s.http, http.MethodGet, u, s.token, &got); err != nil {
		return nil, err
	}
	var out []Track
	for _, m := range got.MediaContainer.Metadata {
		t := Track{Title: m.Title, Artist: m.OriginalTitle, AlbumArtist: m.GrandparentTitle, Album: m.ParentTitle,
			Seconds: float64(m.Duration) / 1000}
		if t.Artist == "" {
			t.Artist = m.GrandparentTitle
		}
		if len(m.Media) > 0 && len(m.Media[0].Part) > 0 {
			t.File = m.Media[0].Part[0].File
		}
		out = append(out, t)
	}
	return out, nil
}
