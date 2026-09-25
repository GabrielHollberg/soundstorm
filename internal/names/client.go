package names

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// DefaultService is where installs register unless told otherwise.
const DefaultService = "https://names.soundstorm.dev"

// Client talks to the name service from an install.
type Client struct {
	Base string // e.g. DefaultService
	HTTP *http.Client
}

// Register asks for a new name.
func (c *Client) Register(ctx context.Context) (Registration, error) {
	var reg Registration
	err := c.do(ctx, http.MethodPost, "/v1/register", "", nil, &reg)
	if err == nil && (!validID(reg.ID) || reg.Token == "" || reg.Name == "") {
		err = fmt.Errorf("name service returned an incomplete registration")
	}
	return reg, err
}

// SetAddress points the registration's name at ip.
func (c *Client) SetAddress(ctx context.Context, reg Registration, ip string) error {
	return c.do(ctx, http.MethodPut, "/v1/address", reg.Credential(), map[string]string{"ip": ip}, nil)
}

// SetPublic points the registration's remote-access name at the install's
// public address, which the service takes from the request's source - the
// install does not name it. port is the port the install serves, which the
// service probes to confirm it is reachable before publishing. Returns the
// public name, or an error the caller can show (a closed port, no public
// address, the provider refusing).
func (c *Client) SetPublic(ctx context.Context, reg Registration, port int) (string, error) {
	return c.setPublic(ctx, reg, port, "")
}

// SetPublicVia is SetPublic over a specific address family, "tcp4" or "tcp6", so
// the install can publish an A and an AAAA record from two calls. The service
// takes the address from the request's source, so which family the request
// leaves on decides which record is published - the SSRF-safe design means the
// install cannot simply name an IPv6 address to publish; it has to actually
// reach the service over IPv6, which proves it has one. A box with no address of
// that family fails to dial, which the caller reads as "that family is not
// available here" rather than an error.
func (c *Client) SetPublicVia(ctx context.Context, reg Registration, port int, network string) (string, error) {
	return c.setPublic(ctx, reg, port, network)
}

func (c *Client) setPublic(ctx context.Context, reg Registration, port int, network string) (string, error) {
	var out struct {
		Name string `json:"name"`
	}
	err := c.doVia(ctx, network, http.MethodPut, "/v1/public", reg.Credential(), map[string]int{"port": port}, &out)
	return out.Name, err
}

// ClearPublic removes the remote-access name, for when the owner turns remote
// access off. Best effort - the LAN name working does not depend on it.
func (c *Client) ClearPublic(ctx context.Context, reg Registration) error {
	return c.do(ctx, http.MethodDelete, "/v1/public", reg.Credential(), nil, nil)
}

// SetChallenge publishes an ACME DNS-01 value, returning once the service
// reports it visible - which can take minutes. public chooses which of the
// install's two names the challenge is for: the remote name when true, the LAN
// name when false. A single multi-name certificate needs one challenge under
// each.
func (c *Client) SetChallenge(ctx context.Context, reg Registration, value string, public bool) error {
	return c.do(ctx, http.MethodPut, "/v1/challenge", reg.Credential(),
		map[string]any{"value": value, "public": public}, nil)
}

// ClearChallenge takes a challenge down. public selects the same name
// SetChallenge published under.
func (c *Client) ClearChallenge(ctx context.Context, reg Registration, public bool) error {
	path := "/v1/challenge"
	if public {
		path += "?public=1"
	}
	return c.do(ctx, http.MethodDelete, path, reg.Credential(), nil, nil)
}

// clientFor returns the HTTP client to use. With no forced family it is the
// configured client (or a default with a long timeout, since a challenge can
// take minutes to become visible). A forced family gets a fresh client whose
// dialer connects only over that family, with a shorter timeout - the only calls
// that pin a family are the reachability-gated public ones, which never wait on a
// challenge, and a box lacking that family should fail fast rather than hang.
func (c *Client) clientFor(network string) *http.Client {
	if network == "" {
		if c.HTTP != nil {
			return c.HTTP
		}
		return &http.Client{Timeout: 6 * time.Minute}
	}
	d := &net.Dialer{Timeout: 10 * time.Second}
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
				return d.DialContext(ctx, network, addr)
			},
		},
	}
}

// StatusError is a refusal from the service, with its reason.
type StatusError struct {
	Status  int
	Message string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("name service: %s (status %d)", e.Message, e.Status)
}

func (c *Client) do(ctx context.Context, method, path, credential string, body, into any) error {
	return c.doVia(ctx, "", method, path, credential, body, into)
}

// doVia is do, optionally pinned to an address family ("tcp4"/"tcp6"). Pinning
// forces a fresh client whose dialer only connects over that family, so the
// request's source address - which the service reads - is of that family.
func (c *Client) doVia(ctx context.Context, network, method, path, credential string, body, into any) error {
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	client := c.clientFor(network)
	// A request that never got an answer is tried again, twice, a moment
	// apart. The service's host dropped about one connection in four for a
	// while (measured: 2 of 8 connects timed out, the rest answered in a
	// quarter of a second), and a single miss at start-up used to cost an
	// install its remote name. Only requests that are safe to repeat: every
	// PUT and DELETE here sets or removes a record to one value. Registering
	// (POST) is not retried; an answer of any status is never retried.
	attempts := 1
	if method == http.MethodPut || method == http.MethodDelete || method == http.MethodGet {
		attempts = 3
	}
	var resp *http.Response
	for attempt := 1; ; attempt++ {
		var rd io.Reader
		if raw != nil {
			rd = bytes.NewReader(raw)
		}
		req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.Base, "/")+path, rd)
		if err != nil {
			return err
		}
		if raw != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if credential != "" {
			req.Header.Set("Authorization", credential)
		}
		resp, err = client.Do(req)
		if err == nil {
			break
		}
		if attempt >= attempts || ctx.Err() != nil {
			return fmt.Errorf("name service: %w", err)
		}
		select {
		case <-time.After(time.Duration(attempt) * time.Second):
		case <-ctx.Done():
			return fmt.Errorf("name service: %w", err)
		}
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))

	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		json.Unmarshal(data, &e)
		if e.Error == "" {
			e.Error = http.StatusText(resp.StatusCode)
		}
		return &StatusError{Status: resp.StatusCode, Message: e.Error}
	}
	if into != nil {
		if err := json.Unmarshal(data, into); err != nil {
			return fmt.Errorf("name service: unreadable reply: %w", err)
		}
	}
	return nil
}
