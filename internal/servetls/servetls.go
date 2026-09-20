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
// Certificates are minted on demand from the connection itself. SoundStorm runs
// in a container, so the addresses it can see on its own interfaces are the
// container's - 172.18.0.5, not the 192.168.1.50 a person actually types. The
// container has no way to learn the latter. But the TLS handshake carries it:
// SNI for a hostname, and for a bare IP, which browsers send no SNI for, the
// local address of the accepted connection is exactly the address the client
// dialled. So GetCertificate answers for whatever it was asked about and
// nothing has to be configured.
package servetls

import (
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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
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
)

// Config says what kind of TLS to set up.
type Config struct {
	// Mode is "off", "self-signed", or "file". Anything else is an error,
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
	mu     sync.Mutex
	leaves map[string]*tls.Certificate
}

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

	default:
		return nil, fmt.Errorf("unknown tls mode %q: want off, self-signed or file", cfg.Mode)
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

func loadSelfSigned(cfg Config) (*Server, error) {
	if cfg.Dir == "" {
		return nil, fmt.Errorf("tls mode %q needs a directory to keep its authority in", ModeSelfSigned)
	}
	if err := os.MkdirAll(cfg.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("create tls directory: %w", err)
	}

	ca, caLeaf, caPEM, err := loadOrMakeCA(cfg.Dir)
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

	// Mint the always-true names up front, which both saves the first request a
	// signature and proves at boot that the authority can actually sign.
	if _, err := s.certFor("localhost", []string{"localhost", "127.0.0.1", "::1"}); err != nil {
		return nil, err
	}
	// Anything else named explicitly gets its own certificate, under its own
	// key, so a handshake for it is a cache hit.
	//
	// One name per certificate rather than one certificate listing them all:
	// every SAN in a leaf is visible to anybody who opens a connection, and a
	// certificate enumerating a machine's Tailscale address, its IPv6 prefixes
	// and its Windows hostname tells a stranger on the LAN more than they
	// asked. Nothing needs them in one certificate - the handshake names the
	// one address that matters.
	for _, host := range unique(cfg.Hosts) {
		if _, err := s.certFor(host, []string{host}); err != nil {
			return nil, err
		}
	}

	cfg.Log.Info("tls enabled with a local authority",
		"install", "/ca.crt",
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
	if name == "" && hello.Conn != nil {
		if host, _, err := net.SplitHostPort(hello.Conn.LocalAddr().String()); err == nil {
			name = host
		}
	}
	if name == "" {
		name = "localhost"
	}
	return s.certFor(name, []string{name})
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

func loadOrMakeCA(dir string) (tls.Certificate, *x509.Certificate, []byte, error) {
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
