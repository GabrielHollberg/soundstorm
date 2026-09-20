// Package httpx is a small HTTP helper shared by the source adapters.
//
// It exists so each adapter can say what it wants (a path, some params, a
// destination struct) without repeating URL joining, timeout handling,
// status checking and body decoding five times.
package httpx

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// maxBody caps how much we will read from an upstream response. A media
// server returning a gigabyte of JSON is a bug we should not amplify.
const maxBody = 8 << 20 // 8 MiB

// Client is a base-URL-scoped HTTP client.
type Client struct {
	base    *url.URL
	hc      *http.Client
	headers map[string]string
}

// New returns a Client rooted at baseURL.
func New(baseURL string, timeout time.Duration) (*Client, error) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return nil, fmt.Errorf("parse base url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("base url must be http or https, got %q", u.Scheme)
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Client{
		base:    u,
		hc:      &http.Client{Timeout: timeout},
		headers: map[string]string{},
	}, nil
}

// SetHeader sets a header sent on every request (auth tokens, mostly).
func (c *Client) SetHeader(key, value string) { c.headers[key] = value }

// URL resolves a path and query against the base URL and returns it as a
// string. Adapters use this to build the absolute OpenURL and CoverURL that
// go back to the client.
func (c *Client) URL(path string, params url.Values) string {
	u := *c.base
	u.Path = joinPath(c.base.Path, path)
	if len(params) > 0 {
		u.RawQuery = params.Encode()
	}
	return u.String()
}

// Raw performs a GET and returns the body bytes and content type.
func (c *Client) Raw(ctx context.Context, path string, params url.Values) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL(path, params), nil)
	if err != nil {
		return nil, "", err
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, "", fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return body, resp.Header.Get("Content-Type"),
			fmt.Errorf("upstream returned %s: %s", resp.Status, snippet(body))
	}
	return body, resp.Header.Get("Content-Type"), nil
}

// JSON performs a GET and decodes the body into out.
func (c *Client) JSON(ctx context.Context, path string, params url.Values, out any) error {
	body, _, err := c.Raw(ctx, path, params)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode json: %w (body started %s)", err, snippet(body))
	}
	return nil
}

// XML performs a GET and decodes the body into out.
func (c *Client) XML(ctx context.Context, path string, params url.Values, out any) error {
	body, _, err := c.Raw(ctx, path, params)
	if err != nil {
		return err
	}
	if err := xml.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode xml: %w (body started %s)", err, snippet(body))
	}
	return nil
}

func joinPath(basePath, path string) string {
	b := strings.TrimSuffix(basePath, "/")
	p := strings.TrimPrefix(path, "/")
	if p == "" {
		if b == "" {
			return "/"
		}
		return b
	}
	return b + "/" + p
}

// snippet returns a short, log-safe prefix of a response body.
func snippet(b []byte) string {
	const n = 160
	s := strings.TrimSpace(string(b))
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > n {
		s = s[:n] + "..."
	}
	if s == "" {
		return "(empty)"
	}
	return s
}
