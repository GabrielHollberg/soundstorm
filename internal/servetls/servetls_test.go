package servetls

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func testLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func selfSigned(t *testing.T, dir string, hosts ...string) *Server {
	t.Helper()
	s, err := Load(Config{Mode: ModeSelfSigned, Dir: dir, Hosts: hosts, Log: testLog()})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return s
}

// serve starts a real TLS listener and returns a client that trusts the
// authority - which is the whole point of having one, so it is what the tests
// use.
//
// Not httptest.StartTLS: it installs a certificate of its own, and Go's TLS
// only consults GetCertificate when Certificates is empty or SNI was sent. A
// client dialling 127.0.0.1 sends no SNI, so the whole mechanism under test
// would be bypassed and the test would be measuring httptest.
func serve(t *testing.T, s *Server) (string, *http.Client) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})}
	go func() { _ = srv.Serve(tls.NewListener(ln, s.TLSConfig())) }()
	t.Cleanup(func() { _ = srv.Close() })

	pool := x509.NewCertPool()
	if s.CAPEM != nil && !pool.AppendCertsFromPEM(s.CAPEM) {
		t.Fatal("the published authority is not a usable certificate")
	}
	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool},
	}}
	return "https://" + ln.Addr().String(), client
}

// Off has to be a nil Server rather than an error: "serve plain HTTP" is a
// legitimate answer, and the caller distinguishes them.
func TestOffIsNotAnError(t *testing.T) {
	s, err := Load(Config{Mode: ModeOff})
	if err != nil || s != nil {
		t.Fatalf("Load(off) = %v, %v", s, err)
	}
	s, err = Load(Config{})
	if err != nil || s != nil {
		t.Fatalf("Load(empty) = %v, %v", s, err)
	}
}

// Serving plain HTTP when somebody asked for TLS is the worst way to be wrong,
// so a typo has to stop the process rather than quietly downgrade it.
func TestAnUnknownModeIsRefused(t *testing.T) {
	if _, err := Load(Config{Mode: "yes-please", Dir: t.TempDir()}); err == nil {
		t.Error("an unknown mode was accepted")
	}
	if _, err := Load(Config{Mode: ModeFile, CertFile: "cert.pem"}); err == nil {
		t.Error("a file mode with no key was accepted")
	}
}

// The point of the local authority: install it once and everything SoundStorm
// serves is trusted, including certificates it has not issued yet.
func TestAClientThatTrustsTheAuthorityGetsNoWarning(t *testing.T) {
	s := selfSigned(t, t.TempDir())
	url, client := serve(t, s)

	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
	if resp.TLS == nil {
		t.Fatal("the connection was not TLS")
	}
}

// A client that does not have the authority must fail, or the authority is
// doing nothing.
func TestAClientThatDoesNotTrustItIsRejected(t *testing.T) {
	s := selfSigned(t, t.TempDir())
	url, _ := serve(t, s)

	if _, err := (&http.Client{}).Get(url); err == nil {
		t.Error("an untrusting client connected anyway")
	}
}

// SoundStorm runs in a container, so the addresses on its own interfaces are
// the container's - never the 192.168.1.50 somebody actually types. The
// handshake carries the real one, and this is the case with no SNI at all:
// browsers send none when you dial an IP.
func TestACertificateIsMintedForTheAddressThatWasDialled(t *testing.T) {
	s := selfSigned(t, t.TempDir())

	// A handshake with no ServerName, arriving on an address the config was
	// never told about.
	conn := &fakeConn{local: &net.TCPAddr{IP: net.ParseIP("192.168.1.50"), Port: 8099}}
	cert, err := s.TLSConfig().GetCertificate(&tls.ClientHelloInfo{Conn: conn})
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}

	found := false
	for _, ip := range cert.Leaf.IPAddresses {
		if ip.String() == "192.168.1.50" {
			found = true
		}
	}
	if !found {
		t.Errorf("certificate covers %v, not the address it was reached on", cert.Leaf.IPAddresses)
	}
	if len(cert.Certificate) != 2 {
		t.Errorf("chain has %d certificates, want the leaf and its authority", len(cert.Certificate))
	}
}

func TestACertificateIsMintedForAHostnameFromSNI(t *testing.T) {
	s := selfSigned(t, t.TempDir())

	cert, err := s.TLSConfig().GetCertificate(&tls.ClientHelloInfo{ServerName: "media.lan"})
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if len(cert.Leaf.DNSNames) != 1 || cert.Leaf.DNSNames[0] != "media.lan" {
		t.Errorf("certificate covers %v, not the requested name", cert.Leaf.DNSNames)
	}
}

// Minting is a signature and a map entry, and a stranger can ask for one per
// handshake. It must not grow without bound.
func TestTheCertificateCacheIsBounded(t *testing.T) {
	s := selfSigned(t, t.TempDir())

	for i := 0; i < maxLeaves*3; i++ {
		_, err := s.TLSConfig().GetCertificate(&tls.ClientHelloInfo{
			ServerName: "host" + string(rune('a'+i%26)) + string(rune('a'+i/26)) + ".test",
		})
		if err != nil {
			t.Fatalf("GetCertificate: %v", err)
		}
	}
	s.mu.Lock()
	held := len(s.leaves)
	s.mu.Unlock()
	if held > maxLeaves {
		t.Errorf("cache holds %d certificates, over the %d cap", held, maxLeaves)
	}
}

// A new authority on every restart would mean re-trusting it on every device,
// which is the one chore this design exists to make a one-off.
func TestTheAuthoritySurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	first := selfSigned(t, dir)
	second := selfSigned(t, dir)

	if string(first.CAPEM) != string(second.CAPEM) {
		t.Error("a restart produced a new authority; every device would have to trust it again")
	}

	// And the second instance's certificates must verify against the first's
	// published authority, or "trusted once" is not true.
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(first.CAPEM)
	cert, err := second.TLSConfig().GetCertificate(&tls.ClientHelloInfo{ServerName: "media.lan"})
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if _, err := cert.Leaf.Verify(x509.VerifyOptions{DNSName: "media.lan", Roots: pool}); err != nil {
		t.Errorf("verify against the earlier authority: %v", err)
	}
}

// The key can mint a certificate for any name, so it is the one genuinely
// sensitive file SoundStorm writes.
func TestTheAuthorityKeyIsNotWorldReadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Windows has no mode bits to check; the file is protected by its
		// ACL, which os.WriteFile does not set from a Unix mode.
		t.Skip("unix permission bits")
	}
	dir := t.TempDir()
	selfSigned(t, dir)

	info, err := os.Stat(filepath.Join(dir, "ca-key.pem"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		t.Errorf("ca-key.pem is %v; it signs for any name", mode)
	}
}

// A real certificate from a real authority needs nothing installed, and saying
// otherwise would send somebody off to install a CA that does not exist.
func TestARealCertificatePublishesNoAuthority(t *testing.T) {
	dir := t.TempDir()
	// Reuse the generator to produce a plausible pair on disk.
	gen := selfSigned(t, dir, "example.test")
	cert, err := gen.certFor("example.test", []string{"example.test"})
	if err != nil {
		t.Fatal(err)
	}
	certPath, keyPath := writePair(t, dir, cert)

	s, err := Load(Config{Mode: ModeFile, CertFile: certPath, KeyFile: keyPath, Log: testLog()})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.CAPEM != nil {
		t.Error("a supplied certificate advertised an authority to install")
	}
}

// --- helpers ----------------------------------------------------------------

type fakeConn struct {
	net.Conn
	local net.Addr
}

func (c *fakeConn) LocalAddr() net.Addr { return c.local }

func writePair(t *testing.T, dir string, cert *tls.Certificate) (string, string) {
	t.Helper()
	certPath := filepath.Join(dir, "server.pem")
	keyPath := filepath.Join(dir, "server-key.pem")

	certPEM := pemEncode("CERTIFICATE", cert.Certificate[0])
	keyDER, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pemEncode("PRIVATE KEY", keyDER), 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

func pemEncode(kind string, der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der})
}
