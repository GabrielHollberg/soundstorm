// Package servetls gives SoundStorm a TLS configuration without anybody running
// openssl.
//
// The problem this solves is specific to a home server. A public web service
// has a domain name and Let's Encrypt; a home media server has none of that.
// It is reached at http://192.168.1.50:8099, or at a hostname only the local
// router knows, from a machine behind NAT that no ACME challenge can reach. So
// the certificate has to be made locally, and that means a browser warning
// unless the user installs something once.
//
// What makes that bearable is a local certificate authority rather than a bare
// self-signed certificate. A user installs ONE CA certificate on each device -
// downloadable from /ca.crt - and from then on every certificate SoundStorm
// issues is trusted, including ones minted later for an address it had never
// seen. A bare self-signed leaf would have to be re-trusted every time it was
// renewed or the address changed. This is what mkcert and Caddy's internal
// issuer do, for the same reason.
//
// A hostname is easy: it arrives as SNI in the handshake, so a certificate is
// minted for whatever was asked for and nothing has to be configured.
//
// A bare IP address is not, and this is the part that took a wrong turn first.
// Browsers send no SNI when you dial an IP, so the name has to come from
// somewhere else - and the obvious somewhere, the local address of the
// accepted connection, is wrong here. SoundStorm's port is published by
// Docker, which NATs it: inside the container the local address is the
// container's own 172.20.0.5, not the 192.168.0.19 the client actually
// dialled. That reads correctly in a unit test with a synthetic connection,
// and correctly for a binary run directly on the host, and never once in the
// way the thing actually ships.
//
// So the addresses to answer to have to be told to it, through
// SOUNDSTORM_TLS_HOSTS, which the installer fills in with the machine's LAN
// address. Those go into one certificate, used for any handshake that does not
// name something else.
package servetls

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/acme"
	"github.com/GabrielHollberg/soundstorm/internal/names"
	"github.com/GabrielHollberg/soundstorm/internal/portmap"
)

const (
	// caLifetime is long because installing a CA on every device in a house is
	// a chore nobody wants to repeat. It is a private CA for one server on one
	// network; the risk it carries is not the risk a public root carries.
	caLifetime = 10 * 365 * 24 * time.Hour

	// leafLifetime stays under the 398 days Apple platforms will accept, so a
	// certificate is not rejected out of hand on an iPhone.
	leafLifetime = 395 * 24 * time.Hour

	// renewBefore re-mints a leaf while it is still valid, so nothing expires
	// mid-request on a server that has been up for a year.
	renewBefore = 30 * 24 * time.Hour

	// maxLeaves caps the on-demand cache. Every unrecognised name in a
	// handshake would otherwise cost a signature and a map entry, which is a
	// cheap thing for a stranger to ask for a great many of.
	maxLeaves = 64
)

// Mode is how TLS is configured.
const (
	ModeOff        = "off"
	ModeSelfSigned = "self-signed"
	ModeFile       = "file"
	// ModeAuto is self-signed plus a real certificate for a soundstorm.dev
	// name, once one can be had. See auto.go.
	ModeAuto = "auto"
)

// Config says what kind of TLS to set up.
type Config struct {
	// Mode is "off", "self-signed", "file" or "auto". Anything else is an error,
	// because silently serving plain HTTP when somebody asked for TLS is the
	// worst possible way to be wrong.
	Mode string

	// CertFile and KeyFile are a PEM pair for ModeFile - a real certificate
	// from a real authority, for somebody who has one.
	CertFile, KeyFile string

	// Dir is where generated material is kept. It must survive restarts, or
	// every restart hands out a new CA and every device has to trust it again.
	Dir string

	// Hosts are names to issue certificates for at startup. Not required:
	// names are learned from handshakes, so this only saves the first request
	// to each address a signature.
	Hosts []string

	// NamesURL and ACMEDirectory are auto mode's two outside services: the
	// name service and the certificate authority. Empty means the real ones.
	NamesURL, ACMEDirectory string

	// Remote is the starting state of remote access - reaching this install
	// from the internet - when RemoteEnabled is not set. Off changes nothing.
	Remote bool

	// RemoteEnabled, when set, is consulted on every certificate step so the
	// owner can turn remote access on and off at runtime. It overrides Remote.
	// Call Server.Refresh after it changes to have the change taken up at once.
	RemoteEnabled func() bool

	// Port is the port the install is reached on, published to the name
	// service so it can confirm the address is reachable there. Only used with
	// remote access.
	Port int

	// Gateway is the home router's LAN address, for opening the port
	// automatically when remote access is on (see internal/portmap). The
	// installer discovers it on the host and passes it in; the container cannot,
	// because its own default route is the Docker bridge, not the router. Zero
	// means no automatic port-forward - remote access then needs the port
	// forwarded by hand.
	Gateway netip.Addr

	// UPnPLocation is a UPnP-IGD device-description URL, the fallback method
	// after PCP and NAT-PMP. The installer discovers it on the host by SSDP
	// (which cannot cross the Docker bridge from the container) and passes it in.
	// Empty means the process tries SSDP itself, which only reaches the LAN on a
	// host-network or native run. UPnP forwards to the first configured Host
	// that is an IP - the LAN address - so it is only offered when there is one.
	UPnPLocation string

	// ACMEHTTP talks to the authority. Nil is an ordinary client; the
	// rehearsal against Pebble needs one that accepts Pebble's own
	// certificate.
	ACMEHTTP *http.Client

	Log *slog.Logger
}

// Server is a live TLS configuration.
type Server struct {
	cfg *tls.Config

	// CAPEM is the certificate to install on client devices, or nil when the
	// certificate came from a real authority and nothing needs installing.
	CAPEM []byte

	ca     tls.Certificate
	caLeaf *x509.Certificate
	log    *slog.Logger

	// fallback covers localhost, the loopback addresses and every name the
	// operator configured. It answers any handshake that does not name
	// something specific - which is every connection to a bare IP address,
	// because browsers send no SNI for those.
	fallback *tls.Certificate

	mu     sync.Mutex
	leaves map[string]*tls.Certificate

	// auto is set in ModeAuto: the real certificate, when there is one.
	auto *autoCert

	// sniff serves plain HTTP beside TLS on the same port - always in ModeAuto,
	// even when there is no address to name, because the installer prints an
	// http:// address for auto mode and it has to answer.
	sniff bool
}

// Start runs whatever the configuration needs in the background - in auto
// mode, getting and renewing the real certificate. A no-op otherwise.
func (s *Server) Start(ctx context.Context) {
	if s != nil && s.auto != nil {
		go s.auto.run(ctx)
		if s.auto.portMapper != nil {
			// Its own loop, on its own cadence: a router lease is measured in
			// hours, far shorter than the 12-hour certificate check, so the
			// mapping cannot be refreshed off the same timer.
			go s.auto.portMapper.Run(ctx, s.auto.remoteOn)
		}
	}
}

// PublicName is the name a real certificate answers to, or "" when there is
// none yet - which is always, outside auto mode.
func (s *Server) PublicName() string {
	if s == nil || s.auto == nil {
		return ""
	}
	return s.auto.name()
}

// SupportsRemote reports whether remote access can be offered at all - it needs
// auto mode, which is the only mode with a name service and a real certificate.
func (s *Server) SupportsRemote() bool { return s != nil && s.auto != nil }

// RemoteName is the name to reach this install by from outside the house, once
// remote access is up, or "" otherwise.
func (s *Server) RemoteName() string {
	if s == nil || s.auto == nil {
		return ""
	}
	return s.auto.remoteNameNow()
}

// RemotePort is the port the world reaches this install on - the one to forward
// by hand when the automatic methods cannot. Zero outside auto mode.
func (s *Server) RemotePort() int {
	if s == nil || s.auto == nil {
		return 0
	}
	return s.auto.port
}

// RemoteMapping reports how the inbound port was opened automatically, if it
// was: the method ("UPnP", "PCP", "NAT-PMP") and whether a mapping is currently
// held. Empty and false when nothing was mapped - either remote access is off,
// no method worked, or the port is forwarded by hand.
func (s *Server) RemoteMapping() (method string, mapped bool) {
	if s == nil || s.auto == nil || s.auto.portMapper == nil {
		return "", false
	}
	m, ok := s.auto.portMapper.Current()
	if !ok {
		return "", false
	}
	return m.Method, true
}

// ReachabilityAnswer answers a name-service reachability challenge, or reports
// that there is no registration to answer with (not auto mode, or not yet
// registered). It is what the install's /api/remote-reachable route serves.
func (s *Server) ReachabilityAnswer(nonce string) (string, bool) {
	if s == nil || s.auto == nil {
		return "", false
	}
	return s.auto.reachabilityAnswer(nonce)
}

// Sniffs reports whether this configuration serves plain HTTP and TLS on one
// port, in which case it must be served through Listener.
func (s *Server) Sniffs() bool { return s != nil && s.sniff }

// TLSConfig returns the configuration to hand to http.Server.
func (s *Server) TLSConfig() *tls.Config { return s.cfg }

// Load builds a TLS configuration, generating a local authority if asked to.
//
// It returns a nil Server for ModeOff, which callers should read as "serve
// plain HTTP" rather than as an error.
func Load(cfg Config) (*Server, error) {
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}

	switch strings.ToLower(strings.TrimSpace(cfg.Mode)) {
	case "", ModeOff:
		return nil, nil

	case ModeFile:
		if cfg.CertFile == "" || cfg.KeyFile == "" {
			return nil, fmt.Errorf("tls mode %q needs both a certificate and a key", ModeFile)
		}
		pair, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("load tls certificate: %w", err)
		}
		return &Server{
			cfg: baseConfig(func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
				return &pair, nil
			}),
		}, nil

	case ModeSelfSigned:
		return loadSelfSigned(cfg)

	case ModeAuto:
		return loadAuto(cfg)

	default:
		return nil, fmt.Errorf("unknown tls mode %q: want off, self-signed, file or auto", cfg.Mode)
	}
}

func baseConfig(get func(*tls.ClientHelloInfo) (*tls.Certificate, error)) *tls.Config {
	return &tls.Config{
		GetCertificate: get,
		// 1.2 rather than 1.3 only: a Chromecast, an older smart TV and a
		// 2017 tablet are all plausible clients for a home media server, and
		// none of them will be updated.
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"h2", "http/1.1"},
	}
}

// loadAuto is self-signed with a real certificate layered over it. The local
// authority is not a leftover: it is what answers before the real certificate
// arrives, for a bare IP address, and for good if it never can.
func loadAuto(cfg Config) (*Server, error) {
	s, err := loadSelfSigned(cfg)
	if err != nil {
		return nil, err
	}
	s.sniff = true
	announce := announceAddress(cfg.Hosts)
	if announce == "" {
		// Not fatal: this is self-signed mode with http beside it, which
		// works. But say why the real certificate will never come.
		cfg.Log.Warn("tls auto mode needs this machine's LAN address in SOUNDSTORM_TLS_HOSTS " +
			"to name it; serving with the local authority only")
		return s, nil
	}
	namesURL := cfg.NamesURL
	if namesURL == "" {
		namesURL = names.DefaultService
	}
	directory := cfg.ACMEDirectory
	if directory == "" {
		directory = acme.LetsEncrypt
	}
	remoteEnabled := cfg.RemoteEnabled
	if remoteEnabled == nil {
		on := cfg.Remote
		remoteEnabled = func() bool { return on }
	}
	s.auto = &autoCert{
		dir:           cfg.Dir,
		announce:      announce,
		directory:     directory,
		remoteEnabled: remoteEnabled,
		port:          cfg.Port,
		kick:          make(chan struct{}, 1),
		names:         &names.Client{Base: namesURL},
		newACME: func(key *ecdsa.PrivateKey) issuer {
			return &acme.Client{Directory: directory, Key: key, HTTP: cfg.ACMEHTTP}
		},
		log: cfg.Log,
	}
	// The LAN address UPnP forwards to - the first configured host that parses
	// as an IP. PCP and NAT-PMP do not need it (the router reads the request's
	// source), so its absence only rules out UPnP.
	var internalClient netip.Addr
	for _, h := range cfg.Hosts {
		if addr, err := netip.ParseAddr(strings.TrimSpace(h)); err == nil {
			internalClient = addr
			break
		}
	}

	// A port-mapper whenever there is a port to open and at least one method to
	// try it with: a gateway (PCP/NAT-PMP) or a LAN client (UPnP). Without any,
	// remote access still works with a hand-forwarded port; the mapper just is
	// not there to do it automatically.
	if cfg.Port > 0 && (cfg.Gateway.IsValid() || internalClient.IsValid()) {
		s.auto.portMapper = &portmap.Maintainer{
			Gateway:        cfg.Gateway,
			Proto:          portmap.TCP,
			InternalPort:   uint16(cfg.Port),
			ExternalPort:   uint16(cfg.Port),
			Lifetime:       2 * time.Hour,
			InternalClient: internalClient,
			UPnPLocation:   cfg.UPnPLocation,
			Log:            cfg.Log,
		}
	}
	s.auto.load()
	if name := s.auto.name(); name != "" {
		cfg.Log.Info("real certificate loaded", "name", name,
			"expires", s.auto.cert.Leaf.NotAfter.Format("2006-01-02"))
	}
	return s, nil
}

func loadSelfSigned(cfg Config) (*Server, error) {
	if cfg.Dir == "" {
		return nil, fmt.Errorf("tls mode %q needs a directory to keep its authority in", ModeSelfSigned)
	}
	if err := os.MkdirAll(cfg.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("create tls directory: %w", err)
	}

	ca, caLeaf, caPEM, err := loadOrMakeCA(cfg.Dir, cfg.Hosts)
	if err != nil {
		return nil, err
	}

	s := &Server{
		CAPEM:  caPEM,
		ca:     ca,
		caLeaf: caLeaf,
		log:    cfg.Log,
		leaves: map[string]*tls.Certificate{},
	}
	s.cfg = baseConfig(s.getCertificate)

	// One certificate covering localhost and everything the operator named.
	//
	// These SANs are visible to anybody who opens a connection, which is why
	// the list is exactly what was configured rather than every address the
	// machine happens to have - enumerating somebody's Tailscale address and
	// IPv6 prefixes to the local network would be a poor trade for saving them
	// a setting.
	names := unique(append([]string{"localhost", "127.0.0.1", "::1"}, cfg.Hosts...))
	fallback, err := loadOrIssueFallback(cfg.Dir, ca, caLeaf, names)
	if err != nil {
		return nil, err
	}
	s.fallback = fallback

	cfg.Log.Info("tls enabled with a local authority",
		"install", "/ca.crt",
		"answersTo", strings.Join(names, ","),
		"caExpires", caLeaf.NotAfter.Format("2006-01-02"))
	return s, nil
}

// getCertificate answers a handshake, minting a certificate if this is an
// address nobody has asked for before.
//
// ServerName is empty when a client dials an IP address, because browsers do
// not put an IP in SNI. The local address of the connection is then the answer:
// it is the address the client actually dialled, which is precisely what its
// certificate has to match.
func (s *Server) getCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	name := strings.TrimSpace(hello.ServerName)
	if s.auto != nil {
		if cert := s.auto.current(name); cert != nil {
			return cert, nil
		}
	}
	if name == "" {
		// A bare IP address, which browsers send no SNI for. Nothing in the
		// handshake says which address was dialled - behind Docker's NAT the
		// connection's local address is the container's own - so this is what
		// the configured names are for.
		return s.fallback, nil
	}
	if covers(s.fallback, name) {
		return s.fallback, nil
	}
	// A hostname nobody configured. SNI is trustworthy enough to answer for,
	// and minting keeps a name somebody set up in their router working without
	// it also having to be listed here.
	return s.certFor(name, []string{name})
}

// covers reports whether a certificate already answers for a name.
func covers(cert *tls.Certificate, name string) bool {
	if cert == nil || cert.Leaf == nil {
		return false
	}
	if ip := net.ParseIP(name); ip != nil {
		for _, known := range cert.Leaf.IPAddresses {
			if known.Equal(ip) {
				return true
			}
		}
		return false
	}
	for _, known := range cert.Leaf.DNSNames {
		if strings.EqualFold(known, name) {
			return true
		}
	}
	return false
}

// certFor returns a cached certificate for key, minting one covering names if
// there is not a usable one already.
func (s *Server) certFor(key string, names []string) (*tls.Certificate, error) {
	s.mu.Lock()
	if cert, ok := s.leaves[key]; ok && !expiringSoon(cert) {
		s.mu.Unlock()
		return cert, nil
	}
	// A stranger opening handshakes for a thousand names should not be able to
	// make this grow without bound. Emptying it rather than evicting one entry
	// keeps the bookkeeping to nothing; the cost is re-minting, which is a
	// signature.
	if len(s.leaves) >= maxLeaves {
		s.leaves = map[string]*tls.Certificate{}
	}
	s.mu.Unlock()

	cert, err := issue(s.ca, s.caLeaf, unique(names))
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.leaves[key] = cert
	s.mu.Unlock()
	if s.log != nil {
		s.log.Debug("issued a certificate", "for", key)
	}
	return cert, nil
}

func expiringSoon(cert *tls.Certificate) bool {
	if cert.Leaf == nil {
		return false
	}
	return time.Now().Add(renewBefore).After(cert.Leaf.NotAfter)
}

// --- the authority ----------------------------------------------------------

func loadOrMakeCA(dir string, hosts []string) (tls.Certificate, *x509.Certificate, []byte, error) {
	certPath := filepath.Join(dir, "ca.pem")
	keyPath := filepath.Join(dir, "ca-key.pem")

	certPEM, certErr := os.ReadFile(certPath)
	keyPEM, keyErr := os.ReadFile(keyPath)
	if certErr == nil && keyErr == nil {
		pair, err := tls.X509KeyPair(certPEM, keyPEM)
		if err == nil {
			leaf, err := x509.ParseCertificate(pair.Certificate[0])
			// A CA that expires while in use would break every device that
			// trusts it, so it is replaced well before that - which does mean
			// re-installing it, once a decade.
			if err == nil && time.Now().Add(renewBefore).Before(leaf.NotAfter) {
				pair.Leaf = leaf
				return pair, leaf, certPEM, nil
			}
		}
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, nil, fmt.Errorf("generate authority key: %w", err)
	}
	serial, err := serialNumber()
	if err != nil {
		return tls.Certificate{}, nil, nil, err
	}

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			// Named for what it is, because this string is what somebody sees
			// in their operating system's certificate list years later.
			CommonName:   "SoundStorm local authority",
			Organization: []string{"SoundStorm"},
		},
		NotBefore: time.Now().Add(-time.Hour),
		NotAfter:  time.Now().Add(caLifetime),
		KeyUsage:  x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		// MaxPathLenZero: this authority signs server certificates and must not
		// be usable to mint another authority.
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,

		// Name constraints bound what this authority is allowed to vouch for.
		// Without them, a stolen ca-key.pem is trusted by every device that
		// installed /ca.crt for *every* name on the internet - it could mint a
		// certificate for a bank and intercept it. SoundStorm only ever signs
		// certificates for a home network: private IP ranges, and a short list
		// of names a home server legitimately answers to. Constraining the
		// authority to exactly that turns a leaked key from "intercept
		// anything" into "impersonate this household's own server", which the
		// key holder could largely do anyway.
		//
		// Critical, so a verifier that does not understand the extension
		// refuses the authority rather than silently ignoring the limit - the
		// whole point is that the limit is enforced. Every modern browser and
		// operating system understands it.
		PermittedIPRanges:           privateIPRanges(),
		PermittedDNSDomains:         permittedCADomains(hosts),
		PermittedDNSDomainsCritical: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, nil, nil, fmt.Errorf("create authority certificate: %w", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, nil, nil, fmt.Errorf("encode authority key: %w", err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return tls.Certificate{}, nil, nil, fmt.Errorf("write authority certificate: %w", err)
	}
	// 0600: this key can mint a certificate for any name, so it is the one
	// genuinely sensitive file SoundStorm writes.
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return tls.Certificate{}, nil, nil, fmt.Errorf("write authority key: %w", err)
	}

	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return tls.Certificate{}, nil, nil, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf},
		leaf, certPEM, nil
}

// privateIPRanges is every address block a home server can live on and
// nothing a public one can. A leaf whose IP is outside these is refused by any
// device that trusts the authority, so a leaked key cannot mint a certificate
// for a public address.
func privateIPRanges() []*net.IPNet {
	cidrs := []string{
		"127.0.0.0/8", "::1/128", // loopback
		"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", // RFC 1918
		"169.254.0.0/16", "fe80::/10", // link-local
		"fc00::/7",      // IPv6 unique-local
		"100.64.0.0/10", // carrier-grade NAT, which is also Tailscale's range
	}
	ranges := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		if _, n, err := net.ParseCIDR(c); err == nil {
			ranges = append(ranges, n)
		}
	}
	return ranges
}

// permittedCADomains is the DNS names the authority may vouch for: localhost,
// the TLDs reserved for private and local use, SoundStorm's own zone (the
// real certificate's name falls back to the local authority before it
// arrives), and whatever DNS names the operator configured. A permitted domain
// covers itself and anything to its left, so "lan" covers "media.lan".
//
// A public name the operator did not configure is deliberately absent, which
// is the whole protection: the authority cannot vouch for "yourbank.com".
func permittedCADomains(hosts []string) []string {
	domains := []string{
		"localhost",
		"local", "lan", "home", "home.arpa", "internal", "intranet", "corp", "test",
		"soundstorm.dev",
	}
	for _, h := range hosts {
		h = strings.TrimSpace(h)
		// An IP host is covered by privateIPRanges, not here.
		if h == "" || net.ParseIP(h) != nil {
			continue
		}
		domains = append(domains, h)
	}
	return unique(domains)
}

// loadOrIssueFallback reuses the server certificate across restarts.
//
// A fresh one every start looks harmless and is not. Nobody has installed the
// authority yet on most devices, so what they did instead was click through
// the browser's warning once - and a browser pins that exception to the exact
// certificate it saw. Re-minting on every start revoked it every time: the
// warning came back after each restart, and with a service worker in front of
// the page the result was not even a warning but a spinner that never stopped,
// because every request failed TLS while the cached shell kept loading.
//
// Reissued when it is near expiry, or when the names no longer match - a LAN
// address changing has to produce a certificate that covers the new one.
func loadOrIssueFallback(dir string, ca tls.Certificate, caLeaf *x509.Certificate, names []string) (*tls.Certificate, error) {
	certPath := filepath.Join(dir, "server.pem")
	keyPath := filepath.Join(dir, "server-key.pem")

	certPEM, certErr := os.ReadFile(certPath)
	keyPEM, keyErr := os.ReadFile(keyPath)
	if certErr == nil && keyErr == nil {
		if pair, err := tls.X509KeyPair(certPEM, keyPEM); err == nil {
			if leaf, err := x509.ParseCertificate(pair.Certificate[0]); err == nil {
				pair.Leaf = leaf
				if time.Now().Add(renewBefore).Before(leaf.NotAfter) && sameNames(leaf, names) {
					return &pair, nil
				}
			}
		}
	}

	fresh, err := issue(ca, caLeaf, names)
	if err != nil {
		return nil, err
	}
	// Best effort: a certificate that cannot be saved still works for this
	// run, and refusing to serve because of it would be the worse failure.
	if pemPair, err := encodePair(fresh); err == nil {
		_ = os.WriteFile(certPath, pemPair.cert, 0o644)
		_ = os.WriteFile(keyPath, pemPair.key, 0o600)
	}
	return fresh, nil
}

// sameNames reports whether a certificate covers exactly the names asked for,
// so that adding or changing SOUNDSTORM_TLS_HOSTS takes effect on restart.
func sameNames(leaf *x509.Certificate, names []string) bool {
	have := map[string]bool{}
	for _, dns := range leaf.DNSNames {
		have[strings.ToLower(dns)] = true
	}
	for _, ip := range leaf.IPAddresses {
		have[ip.String()] = true
	}
	if len(have) != len(names) {
		return false
	}
	for _, name := range names {
		key := strings.ToLower(name)
		if parsed := net.ParseIP(name); parsed != nil {
			key = parsed.String()
		}
		if !have[key] {
			return false
		}
	}
	return true
}

type pemPair struct{ cert, key []byte }

func encodePair(cert *tls.Certificate) (pemPair, error) {
	key, ok := cert.PrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		return pemPair{}, fmt.Errorf("unexpected key type %T", cert.PrivateKey)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return pemPair{}, err
	}
	return pemPair{
		cert: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]}),
		key:  pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
	}, nil
}

// issue signs a server certificate for the given names.
func issue(ca tls.Certificate, caLeaf *x509.Certificate, names []string) (*tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate server key: %w", err)
	}
	serial, err := serialNumber()
	if err != nil {
		return nil, err
	}

	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: names[0], Organization: []string{"SoundStorm"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(leafLifetime),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	for _, name := range names {
		if ip := net.ParseIP(name); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, name)
		}
	}

	der, err := x509.CreateCertificate(rand.Reader, template, caLeaf, &key.PublicKey, ca.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("sign server certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &tls.Certificate{
		// The authority is sent alongside the leaf so a client that already
		// trusts it can build the chain without having it locally.
		Certificate: [][]byte{der, ca.Certificate[0]},
		PrivateKey:  key,
		Leaf:        leaf,
	}, nil
}

func serialNumber() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, fmt.Errorf("generate serial number: %w", err)
	}
	return serial, nil
}

func unique(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
