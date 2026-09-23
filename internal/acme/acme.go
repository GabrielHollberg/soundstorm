// Package acme gets a certificate from Let's Encrypt, or any other ACME
// authority, using a DNS challenge.
//
// It is the smallest client that does that, written against RFC 8555 with the
// standard library alone. golang.org/x/crypto/acme exists and is good; it
// would also be the project's first dependency, and the part of ACME that
// SoundStorm needs - one account, one name, DNS-01, ES256 - is a few hundred
// lines. The same trade as internal/epub and internal/tags: read or speak the
// little that answers one question.
//
// What it deliberately leaves out: every challenge type but dns-01 (an install
// behind NAT cannot answer http-01 or tls-alpn-01, which is the whole reason
// for the name service), external account binding, key rollover, revocation,
// and contact addresses - there is nobody's email to give, because nobody
// signed up for anything.
package acme

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// LetsEncrypt and LetsEncryptStaging are the two directories that matter.
// Staging issues untrusted certificates under generous limits, and is where
// anything new should run first.
const (
	LetsEncrypt        = "https://acme-v02.api.letsencrypt.org/directory"
	LetsEncryptStaging = "https://acme-staging-v02.api.letsencrypt.org/directory"
)

// Solver publishes and removes a DNS-01 challenge value. Present must not
// return until the value is visible to the authority - asking it to look any
// earlier is a failed validation, and failures are rate limited.
type Solver interface {
	Present(ctx context.Context, domain, value string) error
	CleanUp(ctx context.Context, domain string) error
}

// Client is an ACME account.
type Client struct {
	Directory string
	Key       *ecdsa.PrivateKey // the account key; P-256
	HTTP      *http.Client

	mu     sync.Mutex
	dir    *directory
	kid    string
	nonces []string
}

type directory struct {
	NewNonce   string `json:"newNonce"`
	NewAccount string `json:"newAccount"`
	NewOrder   string `json:"newOrder"`
}

// Problem is an error document from the authority (RFC 7807), which is where
// the actual reason for a refusal lives.
type Problem struct {
	Type        string    `json:"type"`
	Detail      string    `json:"detail"`
	Status      int       `json:"status"`
	Subproblems []Problem `json:"subproblems"`
}

func (p *Problem) Error() string {
	msg := strings.TrimPrefix(p.Type, "urn:ietf:params:acme:error:") + ": " + p.Detail
	for _, sub := range p.Subproblems {
		msg += "; " + sub.Detail
	}
	return "acme: " + msg
}

// RateLimited reports whether err is the authority saying "not now". Retrying
// sooner makes it worse, so the caller should back off for a long while.
func RateLimited(err error) bool {
	var p *Problem
	return errors.As(err, &p) && strings.HasSuffix(p.Type, ":rateLimited")
}

// --- the flow ------------------------------------------------------------------

// Obtain gets a certificate for domain, whose private key is certKey, and
// returns the chain as PEM, leaf first.
func (c *Client) Obtain(ctx context.Context, domain string, certKey crypto.Signer, solver Solver) ([]byte, error) {
	if err := c.account(ctx); err != nil {
		return nil, err
	}

	var order struct {
		Status         string   `json:"status"`
		Authorizations []string `json:"authorizations"`
		Finalize       string   `json:"finalize"`
		Certificate    string   `json:"certificate"`
		Error          *Problem `json:"error"`
	}
	resp, err := c.post(ctx, c.dir.NewOrder, map[string]any{
		"identifiers": []map[string]string{{"type": "dns", "value": domain}},
	}, &order)
	if err != nil {
		return nil, fmt.Errorf("new order: %w", err)
	}
	orderURL := resp.Header.Get("Location")

	for _, authz := range order.Authorizations {
		if err := c.authorize(ctx, authz, solver); err != nil {
			return nil, err
		}
	}

	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{DNSNames: []string{domain}}, certKey)
	if err != nil {
		return nil, fmt.Errorf("make csr: %w", err)
	}
	if _, err := c.post(ctx, order.Finalize, map[string]string{"csr": b64(csr)}, &order); err != nil {
		return nil, fmt.Errorf("finalize: %w", err)
	}

	// Issuance is asynchronous: the order sits in "processing" until the
	// certificate exists.
	for order.Status != "valid" {
		switch order.Status {
		case "invalid":
			if order.Error != nil {
				return nil, fmt.Errorf("order failed: %w", order.Error)
			}
			return nil, errors.New("order failed")
		case "pending", "ready", "processing":
		default:
			return nil, fmt.Errorf("order in unexpected state %q", order.Status)
		}
		if err := wait(ctx, resp); err != nil {
			return nil, err
		}
		if resp, err = c.post(ctx, orderURL, nil, &order); err != nil {
			return nil, fmt.Errorf("poll order: %w", err)
		}
	}

	var chain []byte
	if _, err := c.postRaw(ctx, order.Certificate, &chain); err != nil {
		return nil, fmt.Errorf("download certificate: %w", err)
	}
	return chain, nil
}

// authorize proves control of one identifier.
func (c *Client) authorize(ctx context.Context, url string, solver Solver) error {
	var authz struct {
		Status     string `json:"status"`
		Identifier struct {
			Value string `json:"value"`
		} `json:"identifier"`
		Challenges []struct {
			Type  string `json:"type"`
			URL   string `json:"url"`
			Token string `json:"token"`
		} `json:"challenges"`
	}
	if _, err := c.post(ctx, url, nil, &authz); err != nil {
		return fmt.Errorf("fetch authorization: %w", err)
	}
	// Authorizations are reused for a while after one succeeds, so a renewal
	// often needs no challenge at all.
	if authz.Status == "valid" {
		return nil
	}

	var chURL, token string
	for _, ch := range authz.Challenges {
		if ch.Type == "dns-01" {
			chURL, token = ch.URL, ch.Token
		}
	}
	if chURL == "" {
		return fmt.Errorf("no dns-01 challenge offered for %s", authz.Identifier.Value)
	}

	thumb, err := thumbprint(&c.Key.PublicKey)
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(token + "." + thumb))
	domain := authz.Identifier.Value
	if err := solver.Present(ctx, domain, b64(sum[:])); err != nil {
		return fmt.Errorf("publish challenge: %w", err)
	}
	// Taken down whatever happens, with a context of its own: the one passed
	// in may be the reason this is returning.
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		solver.CleanUp(cleanup, domain)
	}()

	// An empty object, not an empty payload: that is what tells the
	// authority to go and look.
	if _, err := c.post(ctx, chURL, struct{}{}, nil); err != nil {
		return fmt.Errorf("start validation: %w", err)
	}

	for {
		var poll struct {
			Status     string `json:"status"`
			Challenges []struct {
				Type  string   `json:"type"`
				Error *Problem `json:"error"`
			} `json:"challenges"`
		}
		resp, err := c.post(ctx, url, nil, &poll)
		if err != nil {
			return fmt.Errorf("poll authorization: %w", err)
		}
		switch poll.Status {
		case "valid":
			return nil
		case "pending", "processing":
		default:
			for _, ch := range poll.Challenges {
				if ch.Type == "dns-01" && ch.Error != nil {
					return fmt.Errorf("validation of %s failed: %w", domain, ch.Error)
				}
			}
			return fmt.Errorf("authorization for %s is %s", domain, poll.Status)
		}
		if err := wait(ctx, resp); err != nil {
			return err
		}
	}
}

// account fetches the directory and registers the key, once per client.
// Registering a key that already has an account returns that account, so this
// is also how an existing account is found again after a restart.
func (c *Client) account(ctx context.Context) error {
	c.mu.Lock()
	ready := c.kid != ""
	c.mu.Unlock()
	if ready {
		return nil
	}

	if c.dir == nil {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Directory, nil)
		if err != nil {
			return err
		}
		resp, err := c.client().Do(req)
		if err != nil {
			return fmt.Errorf("acme directory: %w", err)
		}
		defer resp.Body.Close()
		var dir directory
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&dir); err != nil || dir.NewOrder == "" {
			return fmt.Errorf("acme directory at %s is unreadable", c.Directory)
		}
		c.dir = &dir
	}

	resp, err := c.post(ctx, c.dir.NewAccount, map[string]any{"termsOfServiceAgreed": true}, nil)
	if err != nil {
		return fmt.Errorf("register account: %w", err)
	}
	kid := resp.Header.Get("Location")
	if kid == "" {
		return errors.New("register account: no account URL in the reply")
	}
	c.mu.Lock()
	c.kid = kid
	c.mu.Unlock()
	return nil
}

// --- JWS -----------------------------------------------------------------------

// post sends a signed request and decodes a JSON reply into into. A nil
// payload is POST-as-GET, which is how ACME reads anything.
func (c *Client) post(ctx context.Context, url string, payload, into any) (*http.Response, error) {
	resp, body, err := c.send(ctx, url, payload, "")
	if err != nil {
		return resp, err
	}
	if into != nil && len(body) > 0 {
		if err := json.Unmarshal(body, into); err != nil {
			return resp, fmt.Errorf("unreadable reply from %s: %w", url, err)
		}
	}
	return resp, nil
}

// postRaw is POST-as-GET for a certificate, which comes back as PEM.
func (c *Client) postRaw(ctx context.Context, url string, into *[]byte) (*http.Response, error) {
	resp, body, err := c.send(ctx, url, nil, "application/pem-certificate-chain")
	*into = body
	return resp, err
}

func (c *Client) send(ctx context.Context, url string, payload any, accept string) (*http.Response, []byte, error) {
	// A nonce can be refused as stale, and the answer to that is always a
	// fresh one and the same request again. Pebble refuses a share of them on
	// purpose, so clients cannot get away without this. Ten tries rather than
	// a handful: at the 50% refusal rate the rehearsal was also run at, five
	// failed about one request in sixty-four, and a retry costs nothing.
	for attempt := 0; ; attempt++ {
		body, err := c.sign(ctx, url, payload)
		if err != nil {
			return nil, nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, nil, err
		}
		req.Header.Set("Content-Type", "application/jose+json")
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		resp, err := c.client().Do(req)
		if err != nil {
			return nil, nil, err
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		c.keepNonce(resp)

		if resp.StatusCode < 300 {
			return resp, data, nil
		}
		prob := &Problem{Status: resp.StatusCode}
		if json.Unmarshal(data, prob) != nil || prob.Type == "" {
			prob.Type = "unknown"
			prob.Detail = fmt.Sprintf("status %d from %s", resp.StatusCode, url)
		}
		if strings.HasSuffix(prob.Type, ":badNonce") && attempt < 10 {
			continue
		}
		return resp, data, prob
	}
}

// sign wraps payload in a flattened JWS. Before there is an account the key is
// sent whole ("jwk"); afterwards the account URL stands in for it ("kid").
func (c *Client) sign(ctx context.Context, url string, payload any) ([]byte, error) {
	nonce, err := c.nonce(ctx)
	if err != nil {
		return nil, err
	}
	protected := map[string]any{"alg": "ES256", "nonce": nonce, "url": url}
	c.mu.Lock()
	kid := c.kid
	c.mu.Unlock()
	if kid != "" {
		protected["kid"] = kid
	} else {
		jwk, err := jwkOf(&c.Key.PublicKey)
		if err != nil {
			return nil, err
		}
		protected["jwk"] = jwk
	}

	head, _ := json.Marshal(protected)
	var body string
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = b64(raw)
	}
	input := b64(head) + "." + body

	digest := sha256.Sum256([]byte(input))
	r, s, err := ecdsa.Sign(rand.Reader, c.Key, digest[:])
	if err != nil {
		return nil, err
	}
	// JWS wants R and S as fixed-width big-endian halves, not the DER that
	// crypto/ecdsa produces by default.
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])

	return json.Marshal(map[string]string{
		"protected": b64(head),
		"payload":   body,
		"signature": b64(sig),
	})
}

func (c *Client) nonce(ctx context.Context) (string, error) {
	c.mu.Lock()
	if n := len(c.nonces); n > 0 {
		nonce := c.nonces[n-1]
		c.nonces = c.nonces[:n-1]
		c.mu.Unlock()
		return nonce, nil
	}
	c.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, c.dir.NewNonce, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("acme nonce: %w", err)
	}
	resp.Body.Close()
	nonce := resp.Header.Get("Replay-Nonce")
	if nonce == "" {
		return "", errors.New("acme: no nonce in reply")
	}
	return nonce, nil
}

func (c *Client) keepNonce(resp *http.Response) {
	if n := resp.Header.Get("Replay-Nonce"); n != "" {
		c.mu.Lock()
		c.nonces = append(c.nonces, n)
		c.mu.Unlock()
	}
}

func (c *Client) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: time.Minute}
}

// jwkOf is the public half of an account key as a JWK. Only P-256 is spoken.
func jwkOf(pub *ecdsa.PublicKey) (map[string]string, error) {
	point, err := pub.ECDH()
	if err != nil {
		return nil, fmt.Errorf("account key: %w", err)
	}
	raw := point.Bytes() // 0x04 || X || Y
	if len(raw) != 65 {
		return nil, errors.New("account key must be P-256")
	}
	return map[string]string{
		"crv": "P-256",
		"kty": "EC",
		"x":   b64(raw[1:33]),
		"y":   b64(raw[33:]),
	}, nil
}

// thumbprint is the RFC 7638 thumbprint of a key: the hash of its JWK with the
// required members in lexicographic order and no whitespace. Both sides
// compute it independently, so the order is not a matter of taste - a
// different one is a different hash and a failed challenge.
func thumbprint(pub *ecdsa.PublicKey) (string, error) {
	jwk, err := jwkOf(pub)
	if err != nil {
		return "", err
	}
	canonical := `{"crv":"` + jwk["crv"] + `","kty":"` + jwk["kty"] + `","x":"` + jwk["x"] + `","y":"` + jwk["y"] + `"}`
	sum := sha256.Sum256([]byte(canonical))
	return b64(sum[:]), nil
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// wait sleeps for as long as the last reply asked, within reason.
func wait(ctx context.Context, resp *http.Response) error {
	d := 2 * time.Second
	if resp != nil {
		if secs, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && secs > 0 {
			d = time.Duration(secs) * time.Second
		}
	}
	if d > time.Minute {
		d = time.Minute
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}
