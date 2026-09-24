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
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/acme"
	"github.com/GabrielHollberg/soundstorm/internal/names"
	"github.com/GabrielHollberg/soundstorm/internal/portmap"
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
	Obtain(ctx context.Context, domains []string, certKey crypto.Signer, solver acme.Solver) ([]byte, error)
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

	// remoteEnabled reports whether reaching this install from the internet is
	// on. A function, not a flag, so the owner can toggle it at runtime and the
	// next step picks the change up. Nil means off.
	remoteEnabled func() bool
	port          int

	// kick nudges run to take a step at once, so a toggle takes effect now
	// rather than at the next scheduled check. Buffered so a send never blocks.
	kick chan struct{}

	// portMapper opens the inbound port on the home router when remote access is
	// on, or nil when the installer found no gateway to aim it at (see
	// internal/portmap) - in which case the port is forwarded by hand.
	portMapper *portmap.Maintainer

	mu         sync.RWMutex
	reg        names.Registration
	publicName string // the remote name, once the service has published it
	cert       *tls.Certificate
	// upstream is what stands between the home router and the internet, from
	// the router's own WAN address: carrier-grade NAT or a second router mean
	// no forward on this router can work, and the account panel says so
	// instead of asking for one.
	upstream portmap.Upstream
}

// remoteOn reports whether remote access is currently enabled.
func (a *autoCert) remoteOn() bool {
	return a.remoteEnabled != nil && a.remoteEnabled()
}

// Refresh asks the certificate loop to take a step now - after the owner turns
// remote access on or off, so it does not wait for the next scheduled check.
func (s *Server) Refresh() {
	if s == nil || s.auto == nil {
		return
	}
	select {
	case s.auto.kick <- struct{}{}:
	default:
	}
}

// current returns the public certificate if there is a usable one for name.
func (a *autoCert) current(name string) *tls.Certificate {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.cert == nil || time.Now().After(a.cert.Leaf.NotAfter) {
		return nil
	}
	// The certificate can carry both the LAN and the remote name, so it
	// answers for whichever the client dialled - not just the LAN one.
	for _, dns := range a.cert.Leaf.DNSNames {
		if strings.EqualFold(name, dns) {
			return a.cert
		}
	}
	return nil
}

// name is the LAN name a certificate covers, once there is one. This is the
// name the page moves itself to at home; the remote name is separate.
func (a *autoCert) name() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.cert == nil || time.Now().After(a.cert.Leaf.NotAfter) {
		return ""
	}
	return a.reg.Name
}

// checkUpstream asks the router for its WAN address and records what it says
// about the connection. Asked even when the port opened, because a router
// behind carrier-grade NAT opens it without complaint, on an address the
// internet cannot reach. An unanswered question changes nothing: without the
// router's word, the panel's ordinary advice (forward the port) stands.
func (a *autoCert) checkUpstream(ctx context.Context) {
	askCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	wan, err := a.portMapper.ExternalAddress(askCtx)
	if err != nil {
		a.log.Debug("could not learn the router's internet address", "err", err)
		return
	}
	up := portmap.ClassifyWAN(wan)
	a.mu.Lock()
	changed := a.upstream != up
	a.upstream = up
	a.mu.Unlock()
	if changed && up != portmap.UpstreamUnknown {
		a.log.Warn("the router is not directly on the internet, so a port forward cannot reach it",
			"routerAddress", wan, "upstream", string(up))
	}
}

// upstreamNow is the last word from the router about its connection.
func (a *autoCert) upstreamNow() portmap.Upstream {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.upstream
}

// remoteNameNow is the remote name a certificate covers, or "" when remote
// access is not up.
func (a *autoCert) remoteNameNow() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.cert == nil || a.publicName == "" || time.Now().After(a.cert.Leaf.NotAfter) {
		return ""
	}
	return a.publicName
}

// reachabilityAnswer computes this install's answer to a name-service
// reachability challenge, or reports that there is no registration to answer
// with. It is what the /api/remote-reachable handler serves.
func (a *autoCert) reachabilityAnswer(nonce string) (string, bool) {
	a.mu.RLock()
	token := a.reg.Token
	a.mu.RUnlock()
	// Only when remote access is on: the probe is part of publishing a public
	// name, which only happens then, so there is no reason to answer - and no
	// reason to expose the endpoint at all - otherwise.
	if !a.remoteOn() || token == "" {
		return "", false
	}
	return names.Reachability(token, nonce), true
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
		if err := a.stepRecovered(ctx); err != nil {
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
		case <-a.kick:
			// The owner toggled remote access; step again at once.
		case <-time.After(wait):
		}
	}
}

// stepRecovered calls step with a panic turned into an ordinary error.
//
// run is started with go (see Start) and answers to no request, so nothing
// above it recovers a panic the way net/http does for a handler. step talks
// to two things outside this process - Let's Encrypt's ACME endpoint and the
// name service - through client code that parses their responses, and a
// malformed one finding an edge case there would otherwise crash SoundStorm
// entirely rather than doing what every other failure in this loop already
// does: log it, back off, and try again next time.
func (a *autoCert) stepRecovered(ctx context.Context) (err error) {
	defer func() {
		if r := recover(); r != nil {
			a.log.Error("certificate step panicked; recovered", "panic", r, "stack", string(debug.Stack()))
			err = fmt.Errorf("panicked: %v", r)
		}
	}()
	return a.step(ctx)
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

	// The names a certificate should cover: always the LAN name, plus the
	// remote name when remote access is on and the service confirms it is
	// reachable. A failure to make it public is not fatal - the LAN name still
	// works and the box just is not reachable from away, which is what a closed
	// port means anyway - so it is logged and the remote name dropped.
	domains := []string{reg.Name}
	publicName := ""
	if a.remoteOn() {
		// Open the port on the router first, so the reachability probe that
		// SetPublic triggers finds it already open on the first try instead of
		// after a manual forward. Best effort: where no gateway was configured,
		// or the router speaks neither protocol, this does nothing and the probe
		// falls back to whatever the owner forwarded by hand.
		if a.portMapper != nil {
			if _, err := a.portMapper.EnsureNow(ctx); err != nil {
				a.log.Info("could not open the port automatically; a manual forward may be needed",
					"port", a.port, "err", err)
			}
			a.checkUpstream(ctx)
		}
		if name := a.publishRemote(ctx, reg); name != "" {
			publicName = name
			domains = append(domains, name)
		} else {
			a.log.Warn("remote access is not reachable on any address; serving on the LAN name only")
		}
	} else {
		a.mu.RLock()
		had := a.publicName
		a.mu.RUnlock()
		if had != "" {
			// Remote access was just turned off. Close the port and take the
			// public record down so the name stops resolving; both best effort,
			// since the LAN name working does not depend on either.
			if a.portMapper != nil {
				a.portMapper.DropNow(ctx)
			}
			a.mu.Lock()
			a.upstream = portmap.UpstreamUnknown
			a.mu.Unlock()
			if err := a.names.ClearPublic(ctx, reg); err != nil {
				a.log.Warn("could not remove the public name", "err", err)
			} else {
				a.log.Info("remote access turned off; public name removed")
			}
		}
	}
	a.mu.Lock()
	a.publicName = publicName
	a.mu.Unlock()

	if cert != nil && certCovers(cert.Leaf, domains) && !dueForRenewal(cert.Leaf, time.Now()) {
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
	a.log.Info("asking for a certificate", "names", strings.Join(domains, ","))
	chain, err := a.newACME(key).Obtain(ctx, domains, certKey, namesSolver{a.names, reg, publicName})
	if err != nil {
		return fmt.Errorf("certificate for %s: %w", strings.Join(domains, ","), err)
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

// publishRemote points the remote name at this install over whichever address
// families both reach the service and prove reachable back: IPv4 through the
// forwarded port, and IPv6 directly, since IPv6 has no NAT to punch. Either
// alone is enough, and the name is the same for both - a visitor connects on the
// family it has. The two calls are pinned to a family (see Client.SetPublicVia)
// so the service sees a source of that family and publishes the matching record;
// a box without one simply fails to dial, which is the norm for IPv6 and not
// worth a warning on every step.
func (a *autoCert) publishRemote(ctx context.Context, reg names.Registration) string {
	var name string
	if n, err := a.names.SetPublicVia(ctx, reg, a.port, "tcp4"); err != nil {
		a.log.Info("remote access over IPv4 is not reachable", "err", err)
	} else {
		name = n
		a.log.Info("remote access is reachable over IPv4", "name", n)
	}
	if n, err := a.names.SetPublicVia(ctx, reg, a.port, "tcp6"); err != nil {
		a.log.Debug("remote access over IPv6 is not reachable", "err", err)
	} else {
		name = n
		a.log.Info("remote access is reachable over IPv6", "name", n)
	}
	return name
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

// certCovers reports whether a certificate already carries exactly the names
// wanted - so turning remote access on or off, which changes the set, forces a
// re-issue rather than being mistaken for a certificate that is still fine.
func certCovers(leaf *x509.Certificate, domains []string) bool {
	if len(leaf.DNSNames) != len(domains) {
		return false
	}
	have := make(map[string]bool, len(leaf.DNSNames))
	for _, d := range leaf.DNSNames {
		have[strings.ToLower(d)] = true
	}
	for _, d := range domains {
		if !have[strings.ToLower(d)] {
			return false
		}
	}
	return true
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
	c          *names.Client
	reg        names.Registration
	publicName string // the remote name, or "" when only the LAN name is being certified
}

func (s namesSolver) Present(ctx context.Context, domain, value string) error {
	return s.c.SetChallenge(ctx, s.reg, value, s.isPublic(domain))
}

func (s namesSolver) CleanUp(ctx context.Context, domain string) error {
	return s.c.ClearChallenge(ctx, s.reg, s.isPublic(domain))
}

// isPublic reports whether a challenge is for the remote name rather than the
// LAN one, so it is published under the right label.
func (s namesSolver) isPublic(domain string) bool {
	return s.publicName != "" && strings.EqualFold(domain, s.publicName)
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
