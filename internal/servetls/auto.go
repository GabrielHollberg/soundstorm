package servetls

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/acme"
	"github.com/GabrielHollberg/soundstorm/internal/names"
)

// Auto mode: a real certificate, for a real name, with nobody signing up for
// anything.
//
// The install registers with the name service (internal/names), which gives
// it a name like k3x9m2p7qa.home.soundstorm.dev and points it at the LAN
// address the installer recorded. It then proves to Let's Encrypt that it
// controls that name with a DNS challenge the name service publishes, and gets
// a certificate every browser and phone already trusts. No warning, and the
// app installs to a home screen, which a self-signed origin never allows.
//
// Everything here runs in the background and every failure falls back rather
// than stopping anything: until the certificate exists - or if it never can,
// because the service is down or a router refuses to resolve the name - the
// local authority answers exactly as in self-signed mode, and plain HTTP keeps
// working on the same port (see Listener). An install that cannot get a real
// certificate is an install that works the way it did before this existed.

// Where auto mode keeps what it has been given. Beside the local authority,
// in the state volume: losing it costs a fresh name and certificate, never
// anybody's data.
const (
	registrationFile = "name.json"
	accountKeyFile   = "acme-account-key.pem"
	publicCertFile   = "public.pem"
	publicKeyFile    = "public-key.pem"
	// issuerFile records which authority issued the certificate. Without it,
	// moving from Let's Encrypt's staging environment to the real one kept
	// the staging certificate - which no browser trusts - until renewal, two
	// months later.
	issuerFile = "public-issuer.txt"
)

// issuer is what gets a certificate: acme.Client in life, a stub in tests.
type issuer interface {
	Obtain(ctx context.Context, domain string, certKey crypto.Signer, solver acme.Solver) ([]byte, error)
}

// Retry pacing. A failure is nearly always the service or the authority being
// briefly unavailable, so the first retry is soon; the cap keeps a permanently
// failing install from asking more than a couple of times a day.
var (
	firstRetry = 5 * time.Minute
	maxRetry   = 12 * time.Hour
	// A rate limit is the authority saying "not today", and asking again
	// sooner only extends it.
	rateLimitedRetry = 24 * time.Hour
	// How often a healthy install looks at its certificate's expiry.
	checkEvery = 12 * time.Hour
)

type autoCert struct {
	dir       string
	announce  string // the LAN address the name should point at
	directory string // the ACME directory certificates come from
	names     *names.Client
	newACME   func(key *ecdsa.PrivateKey) issuer
	log       *slog.Logger

	mu   sync.RWMutex
	reg  names.Registration
	cert *tls.Certificate
}

// current returns the public certificate if there is a usable one for name.
func (a *autoCert) current(name string) *tls.Certificate {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.cert == nil || !strings.EqualFold(name, a.reg.Name) {
		return nil
	}
	if time.Now().After(a.cert.Leaf.NotAfter) {
		return nil
	}
	return a.cert
}

// name is the public name, once there is a certificate to go with it.
func (a *autoCert) name() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.cert == nil || time.Now().After(a.cert.Leaf.NotAfter) {
		return ""
	}
	return a.reg.Name
}

// load picks up what a previous run saved. Any of it may be missing.
func (a *autoCert) load() {
	if raw, err := os.ReadFile(filepath.Join(a.dir, registrationFile)); err == nil {
		var reg names.Registration
		if json.Unmarshal(raw, &reg) == nil && reg.ID != "" && reg.Token != "" {
			a.reg = reg
		}
	}
	if a.reg.Name == "" {
		return
	}
	pair, err := tls.LoadX509KeyPair(filepath.Join(a.dir, publicCertFile), filepath.Join(a.dir, publicKeyFile))
	if err != nil {
		return
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil || leaf.VerifyHostname(a.reg.Name) != nil {
		return
	}
	if issuer, err := os.ReadFile(filepath.Join(a.dir, issuerFile)); err != nil || strings.TrimSpace(string(issuer)) != a.directory {
		// From some other authority, or from before this was recorded: get
		// one from the authority configured now.
		return
	}
	pair.Leaf = leaf
	a.cert = &pair
}

// run keeps the certificate current until ctx ends.
func (a *autoCert) run(ctx context.Context) {
	retry := firstRetry
	for {
		wait := checkEvery
		if err := a.step(ctx); err != nil {
			wait = retry
			if acme.RateLimited(err) {
				wait = rateLimitedRetry
			}
			a.log.Warn("could not get a real certificate yet; the local one is still serving",
				"err", err, "retryIn", wait.Round(time.Minute))
			if retry *= 2; retry > maxRetry {
				retry = maxRetry
			}
		} else {
			retry = firstRetry
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// step does whatever is next: register, announce, obtain or renew.
func (a *autoCert) step(ctx context.Context) error {
	a.mu.RLock()
	reg, cert := a.reg, a.cert
	a.mu.RUnlock()

	if reg.ID == "" {
		fresh, err := a.names.Register(ctx)
		if err != nil {
			return fmt.Errorf("register a name: %w", err)
		}
		raw, _ := json.MarshalIndent(fresh, "", "  ")
		// 0600: the token is what lets anybody move this install's name.
		if err := os.WriteFile(filepath.Join(a.dir, registrationFile), raw, 0o600); err != nil {
			return fmt.Errorf("save registration: %w", err)
		}
		a.mu.Lock()
		a.reg = fresh
		a.mu.Unlock()
		reg = fresh
		a.log.Info("registered a name for this install", "name", reg.Name)
	}

	// Announced every time rather than only when it changes: it is one cheap
	// call, and it is how a record somebody deleted, or a service that lost
	// track, heals without anybody noticing.
	if err := a.names.SetAddress(ctx, reg, a.announce); err != nil {
		var se *names.StatusError
		if errors.As(err, &se) && se.Status == 401 {
			// The service no longer recognises this registration - its secret
			// was rotated. Forget it, and the next step registers afresh.
			a.forget()
		}
		return fmt.Errorf("point %s at %s: %w", reg.Name, a.announce, err)
	}

	if cert != nil && !dueForRenewal(cert.Leaf, time.Now()) {
		return nil
	}

	key, err := a.accountKey()
	if err != nil {
		return err
	}
	certKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	a.log.Info("asking for a certificate", "name", reg.Name)
	chain, err := a.newACME(key).Obtain(ctx, reg.Name, certKey, namesSolver{a.names, reg})
	if err != nil {
		return fmt.Errorf("certificate for %s: %w", reg.Name, err)
	}

	keyDER, err := x509.MarshalECPrivateKey(certKey)
	if err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	pair, err := tls.X509KeyPair(chain, keyPEM)
	if err != nil {
		return fmt.Errorf("the certificate issued does not match its key: %w", err)
	}
	if pair.Leaf, err = x509.ParseCertificate(pair.Certificate[0]); err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(a.dir, publicKeyFile), keyPEM, 0o600); err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(a.dir, publicCertFile), chain, 0o644); err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(a.dir, issuerFile), []byte(a.directory), 0o644); err != nil {
		return err
	}

	a.mu.Lock()
	a.cert = &pair
	a.mu.Unlock()
	a.log.Info("real certificate installed", "name", reg.Name,
		"expires", pair.Leaf.NotAfter.Format("2006-01-02"))
	return nil
}

func (a *autoCert) forget() {
	a.mu.Lock()
	a.reg, a.cert = names.Registration{}, nil
	a.mu.Unlock()
	os.Remove(filepath.Join(a.dir, registrationFile))
}

// accountKey is the Let's Encrypt account, which is nothing but a key: an
// account registered without a contact has no other identity.
func (a *autoCert) accountKey() (*ecdsa.PrivateKey, error) {
	path := filepath.Join(a.dir, accountKeyFile)
	if raw, err := os.ReadFile(path); err == nil {
		if block, _ := pem.Decode(raw); block != nil {
			if key, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
				return key, nil
			}
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	if err := writeFileAtomic(path, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

// dueForRenewal is true once a third of the certificate's life is left:
// thirty days of a ninety-day certificate, and still sensible as Let's
// Encrypt shortens them. The slack is what lets a week-long outage of the
// name service pass unnoticed.
func dueForRenewal(leaf *x509.Certificate, now time.Time) bool {
	life := leaf.NotAfter.Sub(leaf.NotBefore)
	return now.After(leaf.NotAfter.Add(-life / 3))
}

// announceAddress picks the address to point the name at from the configured
// hosts: the first private one. A public address would be refused by the
// service anyway, and a hostname cannot go in an A record.
func announceAddress(hosts []string) string {
	for _, h := range hosts {
		addr, err := netip.ParseAddr(strings.TrimSpace(h))
		if err == nil && (addr.IsPrivate() || netip.MustParsePrefix("100.64.0.0/10").Contains(addr)) {
			return addr.String()
		}
	}
	return ""
}

type namesSolver struct {
	c   *names.Client
	reg names.Registration
}

func (s namesSolver) Present(ctx context.Context, _, value string) error {
	return s.c.SetChallenge(ctx, s.reg, value)
}

func (s namesSolver) CleanUp(ctx context.Context, _ string) error {
	return s.c.ClearChallenge(ctx, s.reg)
}

// writeFileAtomic replaces a file whole, so a crash mid-write cannot leave a
// certificate that parses as half of itself.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
