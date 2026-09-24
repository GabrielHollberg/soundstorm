package acme

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/names"
)

// The whole chain, for real, on one machine: an install registers with the
// name service, which publishes its challenge to a DNS server, which an ACME
// authority queries before issuing. Pebble is Let's Encrypt's own test
// authority and pebble-challtestsrv its DNS server; neither needs an account,
// and Pebble verifies every signature and every challenge the way Let's
// Encrypt does. Run by scripts/acme-rehearsal.sh, skipped everywhere else.
//
// It exists because nothing short of a real authority can say whether a JWS is
// signed correctly or a thumbprint is computed the way the other side computes
// it - a fake server written alongside the client would share its mistakes.

type namesSolver struct {
	c   *names.Client
	reg names.Registration
}

func (s namesSolver) Present(ctx context.Context, _, value string) error {
	return s.c.SetChallenge(ctx, s.reg, value, false)
}

func (s namesSolver) CleanUp(ctx context.Context, _ string) error {
	return s.c.ClearChallenge(ctx, s.reg, false)
}

func TestPebbleIssuesACertificateForARegisteredName(t *testing.T) {
	directory := os.Getenv("ACME_PEBBLE_DIRECTORY")
	if directory == "" {
		t.Skip("set ACME_PEBBLE_DIRECTORY (see scripts/acme-rehearsal.sh)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	service := &names.Server{
		Secret:      []byte("rehearsal-secret-rehearsal-secret-!!"),
		Zone:        "soundstorm.dev",
		Label:       "home",
		DNS:         &names.ChallTestSrv{Domain: "soundstorm.dev", Base: os.Getenv("CHALLTESTSRV_URL")},
		Nameservers: []string{os.Getenv("CHALLTESTSRV_DNS")},
	}
	srv := httptest.NewServer(service.Handler())
	defer srv.Close()
	nc := &names.Client{Base: srv.URL}

	reg, err := nc.Register(ctx)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := nc.SetAddress(ctx, reg, "192.168.0.19"); err != nil {
		t.Fatalf("set address: %v", err)
	}

	// Pebble serves its API under a throwaway certificate of its own, which
	// is the one thing here that is not verified.
	pebbleHTTP := &http.Client{
		Timeout:   time.Minute,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
	}
	accountKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	certKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)

	client := &Client{Directory: directory, Key: accountKey, HTTP: pebbleHTTP}
	chainPEM, err := client.Obtain(ctx, []string{reg.Name}, certKey, namesSolver{nc, reg})
	if err != nil {
		t.Fatalf("obtain: %v", err)
	}
	leaf, intermediates := parseChain(t, chainPEM)

	if len(leaf.DNSNames) != 1 || leaf.DNSNames[0] != reg.Name {
		t.Errorf("certificate names %v, want [%s]", leaf.DNSNames, reg.Name)
	}
	if !leaf.PublicKey.(*ecdsa.PublicKey).Equal(&certKey.PublicKey) {
		t.Error("the certificate is not for the key that asked for it")
	}

	// Verified against Pebble's root, as a browser would verify a real one
	// against Let's Encrypt's.
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(fetch(t, pebbleHTTP, os.Getenv("ACME_PEBBLE_ROOT")))
	if _, err := leaf.Verify(x509.VerifyOptions{DNSName: reg.Name, Roots: roots, Intermediates: intermediates}); err != nil {
		t.Errorf("the certificate does not verify: %v", err)
	}

	// A renewal is a new process with the same account key: registering again
	// has to find the existing account, not fail or make another.
	again := &Client{Directory: directory, Key: accountKey, HTTP: pebbleHTTP}
	if _, err := again.Obtain(ctx, []string{reg.Name}, certKey, namesSolver{nc, reg}); err != nil {
		t.Fatalf("renewal with the same account key: %v", err)
	}
}

func parseChain(t *testing.T, chainPEM []byte) (*x509.Certificate, *x509.CertPool) {
	t.Helper()
	var certs []*x509.Certificate
	for rest := chainPEM; ; {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatalf("parse chain: %v", err)
		}
		certs = append(certs, c)
	}
	if len(certs) == 0 {
		t.Fatalf("no certificates in:\n%s", chainPEM)
	}
	pool := x509.NewCertPool()
	for _, c := range certs[1:] {
		pool.AddCert(c)
	}
	return certs[0], pool
}

func fetch(t *testing.T, c *http.Client, url string) []byte {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		t.Fatalf("fetch %s: %v", url, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "BEGIN CERTIFICATE") {
		t.Fatalf("%s did not return a certificate", url)
	}
	return b
}
