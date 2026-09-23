package names

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeDNS records what the service asked a provider to do.
type fakeDNS struct {
	mu      sync.Mutex
	records map[string]string // "name TYPE" -> value
	deletes []string
	fail    bool
}

func newFakeDNS() *fakeDNS { return &fakeDNS{records: map[string]string{}} }

func (f *fakeDNS) Set(_ context.Context, name, typ, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errors.New("provider down")
	}
	f.records[name+" "+typ] = value
	return nil
}

func (f *fakeDNS) Delete(_ context.Context, name, typ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.records, name+" "+typ)
	f.deletes = append(f.deletes, name+" "+typ)
	return nil
}

func (f *fakeDNS) get(key string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.records[key]
}

func newService(t *testing.T) (*Server, *fakeDNS, *Client) {
	t.Helper()
	dns := newFakeDNS()
	s := &Server{
		Secret: []byte("0123456789abcdef0123456789abcdef"),
		Zone:   "soundstorm.dev",
		Label:  "home",
		DNS:    dns,
	}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return s, dns, &Client{Base: srv.URL}
}

const sampleChallenge = "LoqXcYV8q5ONbJQxbmR7SCTNo3tiAXDfowyjxAjEuX0"

func TestARegistrationIsANameAndAWorkingCredential(t *testing.T) {
	_, dns, c := newService(t)
	ctx := context.Background()

	reg, err := c.Register(ctx)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if reg.Name != reg.ID+".home.soundstorm.dev" {
		t.Errorf("name = %q for id %q", reg.Name, reg.ID)
	}
	if err := c.SetAddress(ctx, reg, "192.168.0.19"); err != nil {
		t.Fatalf("SetAddress with a fresh registration: %v", err)
	}
	if got := dns.get(reg.ID + ".home A"); got != "192.168.0.19" {
		t.Errorf("A record = %q", got)
	}
}

// The token is the whole of the authentication, so every way of getting it
// wrong has to be refused - including a valid token presented for a different
// id, which is what somebody would try with their own registration.
func TestCredentialsCannotBeForgedOrBorrowed(t *testing.T) {
	_, _, c := newService(t)
	ctx := context.Background()
	mine, _ := c.Register(ctx)
	theirs, _ := c.Register(ctx)

	for name, reg := range map[string]Registration{
		"wrong token":            {ID: mine.ID, Token: "not-it"},
		"my token for their id":  {ID: theirs.ID, Token: mine.Token},
		"no token":               {ID: mine.ID},
		"an id that is not one":  {ID: "../../etc", Token: mine.Token},
		"uppercase of a real id": {ID: strings.ToUpper(mine.ID), Token: mine.Token},
	} {
		err := c.SetAddress(ctx, reg, "192.168.0.19")
		var se *StatusError
		if !errors.As(err, &se) || se.Status != http.StatusUnauthorized {
			t.Errorf("%s: err = %v, want 401", name, err)
		}
	}
}

// A real certificate on a public address is a phishing kit with a
// soundstorm.dev name on it. Only addresses no stranger can reach are named.
func TestOnlyHomeNetworkAddressesCanBeNamed(t *testing.T) {
	_, dns, c := newService(t)
	ctx := context.Background()
	reg, _ := c.Register(ctx)

	for _, ip := range []string{"192.168.1.50", "10.0.0.2", "172.20.0.5", "100.101.102.103", "fd12:3456::1"} {
		if err := c.SetAddress(ctx, reg, ip); err != nil {
			t.Errorf("%s refused: %v", ip, err)
		}
	}
	if got := dns.get(reg.ID + ".home AAAA"); got != "fd12:3456::1" {
		t.Errorf("AAAA = %q", got)
	}
	// Moving to v6 has to clear the v4 record, or a browser may pick the old one.
	if got := dns.get(reg.ID + ".home A"); got != "" {
		t.Errorf("stale A record %q left behind after moving to IPv6", got)
	}

	for _, ip := range []string{"8.8.8.8", "127.0.0.1", "0.0.0.0", "169.254.1.1", "2001:4860::8888", "::1", "not an ip", "::ffff:8.8.8.8"} {
		err := c.SetAddress(ctx, reg, ip)
		var se *StatusError
		if !errors.As(err, &se) || se.Status != http.StatusUnprocessableEntity {
			t.Errorf("%s: err = %v, want refusal", ip, err)
		}
	}
}

func TestAChallengeIsPublishedWhereLetsEncryptLooks(t *testing.T) {
	_, dns, c := newService(t)
	ctx := context.Background()
	reg, _ := c.Register(ctx)

	if err := c.SetChallenge(ctx, reg, sampleChallenge); err != nil {
		t.Fatalf("SetChallenge: %v", err)
	}
	if got := dns.get("_acme-challenge." + reg.ID + ".home TXT"); got != sampleChallenge {
		t.Errorf("TXT = %q", got)
	}
	if err := c.ClearChallenge(ctx, reg); err != nil {
		t.Fatalf("ClearChallenge: %v", err)
	}
	if got := dns.get("_acme-challenge." + reg.ID + ".home TXT"); got != "" {
		t.Errorf("TXT still %q after clearing", got)
	}
}

// Only an ACME value, so the record cannot be put to any other use -
// a site-verification token for somebody else's account, say.
func TestOnlyACMEValuesArePublished(t *testing.T) {
	_, _, c := newService(t)
	ctx := context.Background()
	reg, _ := c.Register(ctx)
	for _, v := range []string{"", "google-site-verification=abc", sampleChallenge + "x", strings.Repeat("a", 42) + "="} {
		err := c.SetChallenge(ctx, reg, v)
		var se *StatusError
		if !errors.As(err, &se) || se.Status != http.StatusUnprocessableEntity {
			t.Errorf("%q: err = %v, want refusal", v, err)
		}
	}
}

func TestRegistrationIsRateLimitedPerAddress(t *testing.T) {
	_, _, c := newService(t)
	ctx := context.Background()
	for i := 0; i < registerRate.n; i++ {
		if _, err := c.Register(ctx); err != nil {
			t.Fatalf("registration %d refused: %v", i+1, err)
		}
	}
	_, err := c.Register(ctx)
	var se *StatusError
	if !errors.As(err, &se) || se.Status != http.StatusTooManyRequests {
		t.Errorf("err = %v, want 429 past the limit", err)
	}
}

// Every certificate under the zone spends the domain's weekly Let's Encrypt
// allowance, so one install asking over and over must be stopped - and so
// must many installs together.
func TestChallengesAreCappedPerInstallAndOverall(t *testing.T) {
	s, _, c := newService(t)
	ctx := context.Background()
	reg, _ := c.Register(ctx)

	for i := 0; i < challengeRate.n; i++ {
		if err := c.SetChallenge(ctx, reg, sampleChallenge); err != nil {
			t.Fatalf("challenge %d refused: %v", i+1, err)
		}
	}
	var se *StatusError
	if err := c.SetChallenge(ctx, reg, sampleChallenge); !errors.As(err, &se) || se.Status != http.StatusTooManyRequests {
		t.Errorf("per-install: err = %v, want 429", err)
	}

	// Spend the global budget directly rather than registering hundreds.
	for s.limits.allow("challenge:*", globalChallengeRate) {
	}
	other, _ := c.Register(ctx)
	if err := c.SetChallenge(ctx, other, sampleChallenge); !errors.As(err, &se) || se.Status != http.StatusTooManyRequests {
		t.Errorf("global: err = %v, want 429", err)
	}
}

// The proxy appends the address it saw, so with a header configured it is the
// last entry that counts - the first is whatever the client claimed.
func TestClientAddressComesFromTheProxyNotTheClient(t *testing.T) {
	s := &Server{ClientIPHeader: "X-Forwarded-For"}
	r := httptest.NewRequest(http.MethodPost, "/v1/register", nil)
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 203.0.113.9")
	if got := s.clientIP(r); got != "203.0.113.9" {
		t.Errorf("clientIP = %q, want the proxy's entry", got)
	}
}

func TestAProviderFailureIsReportedNotSwallowed(t *testing.T) {
	_, dns, c := newService(t)
	ctx := context.Background()
	reg, _ := c.Register(ctx)
	dns.fail = true
	var se *StatusError
	if err := c.SetAddress(ctx, reg, "192.168.0.19"); !errors.As(err, &se) || se.Status != http.StatusBadGateway {
		t.Errorf("err = %v, want 502", err)
	}
}

// --- Porkbun -------------------------------------------------------------------

type porkbunCall struct {
	Path string
	Body map[string]string
}

// fakePorkbun answers like Porkbun's API and records every call.
func fakePorkbun(t *testing.T, existing []porkbunRecord, status string) (*Porkbun, *[]porkbunCall) {
	t.Helper()
	var calls []porkbunCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]string
		json.Unmarshal(raw, &body)
		calls = append(calls, porkbunCall{Path: r.URL.Path, Body: body})
		reply := map[string]any{"status": status}
		if status != "SUCCESS" {
			reply["message"] = "Domain is not opted in to API access."
		}
		if strings.Contains(r.URL.Path, "retrieveByNameType") {
			reply["records"] = existing
		}
		json.NewEncoder(w).Encode(reply)
	}))
	t.Cleanup(srv.Close)
	return &Porkbun{Domain: "soundstorm.dev", APIKey: "pk1_x", SecretAPIKey: "sk1_y", Base: srv.URL}, &calls
}

func TestPorkbunCreatesARecordThatIsNotThere(t *testing.T) {
	p, calls := fakePorkbun(t, nil, "SUCCESS")
	if err := p.Set(context.Background(), "k3x9m2p7qa.home", "A", "192.168.0.19"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if len(*calls) != 2 {
		t.Fatalf("calls = %+v, want a lookup then a create", *calls)
	}
	if got := (*calls)[0].Path; got != "/dns/retrieveByNameType/soundstorm.dev/A/k3x9m2p7qa.home" {
		t.Errorf("lookup path = %q", got)
	}
	create := (*calls)[1]
	if create.Path != "/dns/create/soundstorm.dev" {
		t.Errorf("create path = %q", create.Path)
	}
	for k, want := range map[string]string{
		"name": "k3x9m2p7qa.home", "type": "A", "content": "192.168.0.19",
		"ttl": "600", "apikey": "pk1_x", "secretapikey": "sk1_y",
	} {
		if create.Body[k] != want {
			t.Errorf("create %s = %q, want %q", k, create.Body[k], want)
		}
	}
}

// An install re-announces its address on every start, and nearly always the
// answer has not changed. That must cost one lookup, not a write.
func TestPorkbunLeavesAnUnchangedRecordAlone(t *testing.T) {
	p, calls := fakePorkbun(t, []porkbunRecord{{Content: "192.168.0.19"}}, "SUCCESS")
	if err := p.Set(context.Background(), "k3x9m2p7qa.home", "A", "192.168.0.19"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if len(*calls) != 1 {
		t.Errorf("calls = %+v, want the lookup alone", *calls)
	}
}

func TestPorkbunEditsARecordThatChanged(t *testing.T) {
	p, calls := fakePorkbun(t, []porkbunRecord{{Content: "192.168.0.7"}}, "SUCCESS")
	if err := p.Set(context.Background(), "k3x9m2p7qa.home", "A", "192.168.0.19"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if len(*calls) != 2 || (*calls)[1].Path != "/dns/editByNameType/soundstorm.dev/A/k3x9m2p7qa.home" {
		t.Fatalf("calls = %+v, want a lookup then an edit", *calls)
	}
	if got := (*calls)[1].Body["content"]; got != "192.168.0.19" {
		t.Errorf("edit content = %q", got)
	}
}

// "Domain is not opted in to API access" is the first thing anybody setting
// this up will see, so it has to reach the log as Porkbun said it.
func TestPorkbunSaysWhyItRefused(t *testing.T) {
	p, _ := fakePorkbun(t, nil, "ERROR")
	err := p.Set(context.Background(), "x.home", "A", "192.168.0.19")
	if err == nil || !strings.Contains(err.Error(), "not opted in to API access") {
		t.Errorf("err = %v, want Porkbun's own message", err)
	}
}
