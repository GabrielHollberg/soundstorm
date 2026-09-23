// Package httpx is a small HTTP helper shared by the adapters and the
// provisioners.
//
// It exists so callers can say what they want (a path, some params, a
// destination struct) without repeating URL joining, status checking and body
// decoding in every file.
package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

// maxBody caps how much we buffer from an upstream response. A media server
// returning a gigabyte of JSON is a bug we should not amplify. Streaming
// responses bypass this entirely via Open.
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
	if u.Host == "" {
		return nil, fmt.Errorf("base url %q has no host", baseURL)
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

// SetHeader sets a header sent on every request (auth tokens, mostly). Call it
// during construction only; a Client is read-only once shared.
func (c *Client) SetHeader(key, value string) { c.headers[key] = value }

// EnableCookies gives the client a cookie jar.
//
// Needed for backends whose "API" is really a browser login form - Calibre-Web
// authenticates with a session cookie and a CSRF token, not a bearer token.
func (c *Client) EnableCookies() error {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return fmt.Errorf("create cookie jar: %w", err)
	}
	c.hc.Jar = jar
	return nil
}

// BaseURL returns a copy of the client's base URL.
func (c *Client) BaseURL() *url.URL {
	u := *c.base
	return &u
}

// URL resolves a reference against the base URL and returns it as a string.
//
// ref is a URI reference, not a literal string: any %XX already in it is an
// escape and is preserved rather than escaped a second time. This is the whole
// reason this method is not two lines - assigning to url.URL.Path instead
// double-encodes every reference that already contains an escape, which turns
// a search for "the hobbit" into a search for the literal text "the%20hobbit".
func (c *Client) URL(ref string, params url.Values) string {
	u := c.resolve(ref)
	if len(params) > 0 {
		u.RawQuery = params.Encode()
	}
	return u.String()
}

// resolve anchors a possibly-relative, possibly-already-encoded reference
// under the base URL.
func (c *Client) resolve(ref string) *url.URL {
	// Make the reference relative so ResolveReference keeps any path prefix on
	// the base URL (a backend reverse-proxied at /jellyfin, say) instead of
	// replacing it.
	parsed, err := url.Parse(strings.TrimPrefix(ref, "/"))
	// An absolute reference would change the host, a second leading slash
	// would drop the base's path prefix, and a dot segment would climb out of
	// the path it was meant for - "videos/%2e%2e/System/Info" reached
	// Jellyfin's admin API with SoundStorm's own token, from a member's
	// playlist request. No legitimate reference has any of them, and a
	// malformed one is the caller's bug, so all of them resolve to a path
	// nothing answers: an upstream 404 rather than a silently wrong URL.
	if err != nil || parsed.IsAbs() || parsed.Host != "" ||
		strings.HasPrefix(parsed.Path, "/") || hasDotSegment(parsed.Path) {
		u := *c.base
		u.Path = strings.TrimSuffix(u.Path, "/") + "/soundstorm-refused-path"
		u.RawPath = ""
		u.RawQuery = ""
		return &u
	}
	base := *c.base
	// ResolveReference treats the last path segment as a file and drops it, so
	// the base must end in a slash for its path to act as a directory.
	if !strings.HasSuffix(base.Path, "/") {
		base.Path += "/"
		if base.RawPath != "" {
			base.RawPath += "/"
		}
	}
	return base.ResolveReference(parsed)
}

// hasDotSegment reports whether a decoded path has a "." or ".." segment.
// url.Parse has already decoded %2e, which is the point: an encoded dot is
// still a dot to the server at the other end.
func hasDotSegment(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == "." || seg == ".." {
			return true
		}
	}
	return false
}

// Redact strips the URL out of a transport error. Go's *url.Error quotes the
// whole request URL, and a Subsonic URL carries a replayable credential in
// its query string - which then reached members' screens in a failed search
// and the log in a failed stream. What remains says what went wrong, not
// where or with what.
func Redact(err error) error {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		return fmt.Errorf("%s: %w", uerr.Op, uerr.Err)
	}
	return err
}

// Request is one upstream call.
type Request struct {
	Method  string            // defaults to GET
	Path    string            // URI reference, resolved against the base URL
	Params  url.Values        // query parameters
	Body    any               // marshalled as JSON when non-nil
	Form    url.Values        // form-encoded body; mutually exclusive with Body
	Headers map[string]string // merged over the client's headers
}

// Response is a buffered upstream response.
type Response struct {
	Status      int
	Body        []byte
	ContentType string
}

// OK reports whether the status was 2xx.
func (r *Response) OK() bool { return r.Status >= 200 && r.Status < 300 }

// JSON decodes the response body into out.
func (r *Response) JSON(out any) error {
	if err := json.Unmarshal(r.Body, out); err != nil {
		return fmt.Errorf("decode json: %w (body started %s)", err, Snippet(r.Body))
	}
	return nil
}

// StatusError is a non-2xx response, carrying the status so callers can tell
// "you are not authorised" from "I am still starting up".
//
// That distinction is load-bearing. A backend that answers 503 while it boots
// is not rejecting our credentials, and treating it as though it were means
// throwing away working credentials on every restart.
type StatusError struct {
	Status int
	Body   string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("upstream returned %d: %s", e.Status, e.Body)
}

// Temporary reports whether the status is one a backend returns while it is
// coming up or under load, rather than a refusal.
func (e *StatusError) Temporary() bool {
	return e.Status >= 500 || e.Status == http.StatusTooManyRequests
}

// Err returns a descriptive error when the status was not 2xx, else nil.
func (r *Response) Err() error {
	if r.OK() {
		return nil
	}
	return &StatusError{Status: r.Status, Body: Snippet(r.Body)}
}

// Do performs a request and buffers the response.
//
// A non-2xx status is NOT an error here: provisioning needs to tell "409, the
// admin already exists" from "the host is unreachable". Use Response.Err or the
// JSON helper when you want a non-2xx to be fatal.
func (c *Client) Do(ctx context.Context, r Request) (*Response, error) {
	req, err := c.request(ctx, r)
	if err != nil {
		return nil, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, Redact(err)
	}
	defer resp.Body.Close()

	// Read one byte past the cap so an oversized body is reported as such
	// rather than as mangled JSON.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if len(body) > maxBody {
		return nil, fmt.Errorf("upstream response exceeded %d bytes", maxBody)
	}
	return &Response{
		Status:      resp.StatusCode,
		Body:        body,
		ContentType: resp.Header.Get("Content-Type"),
	}, nil
}

// Open performs a request and returns the live response for streaming. The
// caller owns the body and must close it.
func (c *Client) Open(ctx context.Context, r Request) (*http.Response, error) {
	req, err := c.request(ctx, r)
	if err != nil {
		return nil, err
	}
	resp, err := c.hc.Do(req)
	return resp, Redact(err)
}

func (c *Client) request(ctx context.Context, r Request) (*http.Request, error) {
	method := r.Method
	if method == "" {
		method = http.MethodGet
	}

	var body io.Reader
	switch {
	case r.Body != nil && r.Form != nil:
		return nil, fmt.Errorf("request has both a JSON body and a form body")
	case r.Body != nil:
		encoded, err := json.Marshal(r.Body)
		if err != nil {
			return nil, fmt.Errorf("encode request body: %w", err)
		}
		body = bytes.NewReader(encoded)
	case r.Form != nil:
		body = strings.NewReader(r.Form.Encode())
	}

	req, err := http.NewRequestWithContext(ctx, method, c.URL(r.Path, r.Params), body)
	if err != nil {
		return nil, err
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	for k, v := range r.Headers {
		req.Header.Set(k, v)
	}
	switch {
	case r.Body != nil:
		req.Header.Set("Content-Type", "application/json")
	case r.Form != nil:
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	return req, nil
}

// JSON performs a GET and decodes a 2xx body into out. A non-2xx is an error.
func (c *Client) JSON(ctx context.Context, path string, params url.Values, out any) error {
	resp, err := c.Do(ctx, Request{Path: path, Params: params})
	if err != nil {
		return err
	}
	if err := resp.Err(); err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	return resp.JSON(out)
}

// Raw performs a GET and returns the body bytes and content type.
func (c *Client) Raw(ctx context.Context, path string, params url.Values) ([]byte, string, error) {
	resp, err := c.Do(ctx, Request{Path: path, Params: params})
	if err != nil {
		return nil, "", err
	}
	return resp.Body, resp.ContentType, resp.Err()
}

// Snippet returns a short, log-safe prefix of a response body.
func Snippet(b []byte) string {
	const n = 200
	s := strings.TrimSpace(string(b))
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		// Trim back to a rune boundary so the message stays valid UTF-8.
		cut := n
		for cut > 0 && !isRuneStart(s[cut]) {
			cut--
		}
		s = s[:cut] + "..."
	}
	if s == "" {
		return "(empty)"
	}
	return s
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }
