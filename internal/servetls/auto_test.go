package servetls

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/acme"
	"github.com/GabrielHollberg/soundstorm/internal/names"
)

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// memDNS is a DNS provider that remembers.
type memDNS struct {
	mu      sync.Mutex
	records map[string]string
}

func (m *memDNS) Set(_ context.Context, name, typ, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records[name+" "+typ] = value
	return nil
}

func (m *memDNS) Delete(_ context.Context, name, typ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.records, name+" "+typ)
	return nil
}

// nameService runs a real name service over memDNS and counts registrations.
func nameService(t *testing.T, secret string) (string, *memDNS, *atomic.Int32) {
	t.Helper()
	dns := &memDNS{records: map[string]string{}}
	svc := &names.Server{Secret: []byte(secret), Zone: "soundstorm.dev", Label: "home", DNS: dns, Log: quietLog()}
	var registrations atomic.Int32
	h := svc.Handler()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/register" {
			registrations.Add(1)
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, dns, &registrations
}

// stubAuthority stands in for Let's Encrypt: it publishes a challenge through
// the solver as the real flow does, then signs whatever it was asked for.
type stubAuthority struct {
	ca       *x509.Certificate
	caKey    *ecdsa.PrivateKey
	lifetime time.Duration
	issued   atomic.Int32
	fail     error
}

func newStubAuthority(t *testing.T) *stubAuthority {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "stub authority"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	ca, _ := x509.ParseCertificate(der)
	return &stubAuthority{ca: ca, caKey: key, lifetime: 90 * 24 * time.Hour}
}

func (a *stubAuthority) Obtain(ctx context.Context, domain string, certKey crypto.Signer, solver acme.Solver) ([]byte, error) {
	if a.fail != nil {
		return nil, a.fail
	}
	if err := solver.Present(ctx, domain, "LoqXcYV8q5ONbJQxbmR7SCTNo3tiAXDfowyjxAjEuX0"); err != nil {
		return nil, err
	}
	defer solver.CleanUp(ctx, domain)
	n := a.issued.Add(1)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(int64(n) + 1), DNSNames: []string{domain},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(a.lifetime),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, a.ca, certKey.Public(), a.caKey)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
}

func loadAutoForTest(t *testing.T, dir, namesURL string, authority issuer) *Server {
	t.Helper()
	s, err := Load(Config{Mode: ModeAuto, Dir: dir, Hosts: []string{"192.168.0.19"}, NamesURL: namesURL, Log: quietLog()})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.auto == nil {
		t.Fatal("auto mode did not start its certificate manager")
	}
	s.auto.newACME = func(*ecdsa.PrivateKey) issuer { return authority }
	return s
}

func TestAutoModeGetsAndServesARealCertificate(t *testing.T) {
	dir := t.TempDir()
	namesURL, dns, registrations := nameService(t, "a-secret-that-is-long-enough-to-use")
	authority := newStubAuthority(t)
	s := loadAutoForTest(t, dir, namesURL, authority)

	// Before it has one: the local authority answers, and nothing claims a
	// public name - so the UI will not send anybody to an address that fails.
	if s.PublicName() != "" {
		t.Errorf("PublicName = %q before any certificate", s.PublicName())
	}

	if err := s.auto.step(context.Background()); err != nil {
		t.Fatalf("step: %v", err)
	}
	name := s.PublicName()
	if name == "" {
		t.Fatal("no public name after a successful step")
	}
	id := name[:len(name)-len(".home.soundstorm.dev")]
	if got := dns.records[id+".home A"]; got != "192.168.0.19" {
		t.Errorf("the name points at %q, want the LAN address", got)
	}
	if _, left := dns.records["_acme-challenge."+id+".home TXT"]; left {
		t.Error("the challenge record was left published")
	}

	cert, err := s.getCertificate(&tls.ClientHelloInfo{ServerName: name})
	if err != nil || cert.Leaf.Issuer.CommonName != "stub authority" {
		t.Errorf("a handshake for %s did not get the real certificate", name)
	}
	// Anything else - a bare IP above all - still gets the local one.
	local, _ := s.getCertificate(&tls.ClientHelloInfo{})
	if local.Leaf.Issuer.CommonName != "SoundStorm local authority" {
		t.Errorf("a handshake with no name got %q", local.Leaf.Issuer.CommonName)
	}

	// A restart must pick all of it back up: no second name, no second
	// certificate, which would spend the domain's weekly allowance.
	again := loadAutoForTest(t, dir, namesURL, authority)
	if again.PublicName() != name {
		t.Errorf("after a restart PublicName = %q, want %q", again.PublicName(), name)
	}
	if err := again.auto.step(context.Background()); err != nil {
		t.Fatalf("step after restart: %v", err)
	}
	if registrations.Load() != 1 || authority.issued.Load() != 1 {
		t.Errorf("registrations = %d, certificates = %d after a restart; want 1 and 1",
			registrations.Load(), authority.issued.Load())
	}
}

func TestAutoModeRenewsWithAThirdOfTheLifeLeft(t *testing.T) {
	now := time.Now()
	leaf := &x509.Certificate{NotBefore: now.Add(-50 * 24 * time.Hour), NotAfter: now.Add(40 * 24 * time.Hour)}
	if dueForRenewal(leaf, now) {
		t.Error("renewing with 40 of 90 days left")
	}
	leaf.NotBefore, leaf.NotAfter = now.Add(-61*24*time.Hour), now.Add(29*24*time.Hour)
	if !dueForRenewal(leaf, now) {
		t.Error("not renewing with 29 of 90 days left")
	}

	// And end to end: a certificate issued short enough to be due already
	// is replaced on the next step.
	namesURL, _, _ := nameService(t, "a-secret-that-is-long-enough-to-use")
	authority := newStubAuthority(t)
	authority.lifetime = 10 * time.Second // issued a minute back-dated, so already in its last third
	s := loadAutoForTest(t, t.TempDir(), namesURL, authority)
	s.auto.step(context.Background())
	authority.lifetime = 90 * 24 * time.Hour
	s.auto.step(context.Background())
	if authority.issued.Load() != 2 {
		t.Errorf("certificates issued = %d, want a renewal", authority.issued.Load())
	}
}

// A failure must leave the install exactly as self-signed mode would.
func TestAutoModeFailureLeavesTheLocalAuthorityServing(t *testing.T) {
	namesURL, _, _ := nameService(t, "a-secret-that-is-long-enough-to-use")
	authority := newStubAuthority(t)
	authority.fail = errors.New("authority unavailable")
	s := loadAutoForTest(t, t.TempDir(), namesURL, authority)

	if err := s.auto.step(context.Background()); err == nil {
		t.Fatal("step succeeded against a failing authority")
	}
	if s.PublicName() != "" {
		t.Errorf("PublicName = %q with no certificate", s.PublicName())
	}
	cert, err := s.getCertificate(&tls.ClientHelloInfo{})
	if err != nil || cert == nil {
		t.Fatalf("no local certificate to fall back on: %v", err)
	}
}

// Rotating the service's secret invalidates every token. An install has to
// notice and register again rather than failing its renewals for ever.
func TestAutoModeRegistersAgainWhenTheServiceForgetsIt(t *testing.T) {
	dir := t.TempDir()
	oldURL, _, _ := nameService(t, "the-first-secret-long-enough-to-use")
	authority := newStubAuthority(t)
	s := loadAutoForTest(t, dir, oldURL, authority)
	if err := s.auto.step(context.Background()); err != nil {
		t.Fatalf("step: %v", err)
	}
	first := s.PublicName()

	rotatedURL, _, registrations := nameService(t, "a-rotated-secret-that-is-long-enough")
	s.auto.names = &names.Client{Base: rotatedURL}
	if err := s.auto.step(context.Background()); err == nil {
		t.Fatal("a step with a stale registration succeeded")
	}
	if err := s.auto.step(context.Background()); err != nil {
		t.Fatalf("step after forgetting: %v", err)
	}
	if registrations.Load() != 1 || s.PublicName() == first || s.PublicName() == "" {
		t.Errorf("registrations = %d, name %q -> %q; want a fresh registration", registrations.Load(), first, s.PublicName())
	}
}

func TestAutoModeWithoutALANAddressIsJustSelfSigned(t *testing.T) {
	s, err := Load(Config{Mode: ModeAuto, Dir: t.TempDir(), Hosts: []string{"media.lan"}, Log: quietLog()})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.auto != nil || s.Sniffs() {
		t.Error("auto mode started with no address to announce")
	}
}

// --- one port, both protocols -------------------------------------------------

func serveSniffing(t *testing.T) (string, *Server) {
	t.Helper()
	s, err := Load(Config{Mode: ModeAuto, Dir: t.TempDir(), Hosts: []string{"192.168.0.19"}, NamesURL: "http://127.0.0.1:1", Log: quietLog()})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "tls=%v proto=%s", r.TLS != nil, r.Proto)
		}),
		TLSConfig: s.TLSConfig(),
	}
	ln := s.Listener(inner)
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return inner.Addr().String(), s
}

func get(t *testing.T, c *http.Client, url string) string {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func TestOnePortServesPlainHTTPAndTLS(t *testing.T) {
	addr, _ := serveSniffing(t)

	if got := get(t, http.DefaultClient, "http://"+addr+"/"); got != "tls=false proto=HTTP/1.1" {
		t.Errorf("plain HTTP: %q", got)
	}

	// r.TLS set is what makes the session cookie Secure, and HTTP/2 is what
	// a browser will negotiate; both have to survive the sniffing.
	secure := &http.Client{Transport: &http.Transport{
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true},
		ForceAttemptHTTP2: true,
	}}
	if got := get(t, secure, "https://"+addr+"/"); got != "tls=true proto=HTTP/2.0" {
		t.Errorf("TLS: %q", got)
	}
}

// A connection that opens and says nothing must not stall the ones behind it.
func TestASilentConnectionDoesNotBlockOthers(t *testing.T) {
	addr, _ := serveSniffing(t)
	for i := 0; i < 5; i++ {
		idle, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		defer idle.Close()
	}
	c := &http.Client{Timeout: 3 * time.Second}
	if got := get(t, c, "http://"+addr+"/"); got != "tls=false proto=HTTP/1.1" {
		t.Errorf("behind silent connections: %q", got)
	}
}

// --- the real thing, against Pebble -------------------------------------------

// TestPebbleAutoModeEndToEnd is the whole of auto mode against a real ACME
// authority: register, announce, challenge, issue, and then a client that
// trusts only the authority's root connecting by name to the port that also
// still answers plain HTTP. Run by scripts/acme-rehearsal.sh.
func TestPebbleAutoModeEndToEnd(t *testing.T) {
	directory := os.Getenv("ACME_PEBBLE_DIRECTORY")
	if directory == "" {
		t.Skip("set ACME_PEBBLE_DIRECTORY (see scripts/acme-rehearsal.sh)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	svc := &names.Server{
		Secret:      []byte("rehearsal-secret-rehearsal-secret-!!"),
		Zone:        "soundstorm.dev",
		Label:       "home",
		DNS:         &names.ChallTestSrv{Domain: "soundstorm.dev", Base: os.Getenv("CHALLTESTSRV_URL")},
		Nameservers: []string{os.Getenv("CHALLTESTSRV_DNS")},
		Log:         quietLog(),
	}
	namesSrv := httptest.NewServer(svc.Handler())
	defer namesSrv.Close()

	pebbleHTTP := &http.Client{Timeout: time.Minute, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}
	dir := t.TempDir()
	s, err := Load(Config{
		Mode: ModeAuto, Dir: dir, Hosts: []string{"192.168.0.19"},
		NamesURL: namesSrv.URL, ACMEDirectory: directory, ACMEHTTP: pebbleHTTP, Log: quietLog(),
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := s.auto.step(ctx); err != nil {
		t.Fatalf("step: %v", err)
	}
	name := s.PublicName()
	if name == "" {
		t.Fatal("no public name")
	}

	inner, _ := net.Listen("tcp", "127.0.0.1:0")
	srv := &http.Server{
		Handler:   http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") }),
		TLSConfig: s.TLSConfig(),
	}
	go srv.Serve(s.Listener(inner))
	defer srv.Close()

	resp, err := pebbleHTTP.Get(os.Getenv("ACME_PEBBLE_ROOT"))
	if err != nil {
		t.Fatalf("fetch Pebble's root: %v", err)
	}
	rootPEM, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(rootPEM)

	// Connecting by name, trusting nothing but the authority: exactly what a
	// browser does with Let's Encrypt. The dial goes to the test listener,
	// because the name points at an address this container does not have.
	verified := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, inner.Addr().String())
		},
	}}
	if got := get(t, verified, "https://"+name+"/"); got != "ok" {
		t.Errorf("over verified TLS by name: %q", got)
	}
	if got := get(t, http.DefaultClient, "http://"+inner.Addr().String()+"/"); got != "ok" {
		t.Errorf("plain HTTP on the same port: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, publicCertFile)); err != nil {
		t.Errorf("certificate not saved: %v", err)
	}
}
