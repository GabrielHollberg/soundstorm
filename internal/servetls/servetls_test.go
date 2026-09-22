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

// Browsers send no SNI when you dial a bare IP address, so nothing in the
// handshake says which address was asked for. The local address of the
// connection looks like the answer and is not: behind Docker's published port
// it is the container's own, not the one the client dialled. So the addresses
// to answer for are configured, and they go in one certificate used whenever
// the handshake names nothing.
func TestAConnectionWithNoSNIGetsTheConfiguredNames(t *testing.T) {
	s := selfSigned(t, t.TempDir(), "192.168.0.19", "media.lan")

	// A handshake carrying no ServerName at all, which is what dialling an IP
	// produces, and arriving on an address that means nothing to anybody.
	conn := &fakeConn{local: &net.TCPAddr{IP: net.ParseIP("172.20.0.5"), Port: 8080}}
	cert, err := s.TLSConfig().GetCertificate(&tls.ClientHelloInfo{Conn: conn})
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}

	if !covers(cert, "192.168.0.19") {
		t.Errorf("certificate covers %v, not the configured LAN address", cert.Leaf.IPAddresses)
	}
	if !covers(cert, "localhost") || !covers(cert, "127.0.0.1") {
		t.Errorf("certificate stopped covering localhost: %v %v",
			cert.Leaf.DNSNames, cert.Leaf.IPAddresses)
	}
	// And it must not be answering for the container's own address, which is
	// the thing that looked right and was not.
	if covers(cert, "172.20.0.5") {
		t.Error("the certificate is built from the connection rather than the configuration")
	}
	if len(cert.Certificate) != 2 {
		t.Errorf("chain has %d certificates, want the leaf and its authority", len(cert.Certificate))
	}
}

// The whole point of configuring an address: a client dialling it, trusting
// only the published authority, must not see a warning.
func TestAConfiguredAddressVerifies(t *testing.T) {
	s := selfSigned(t, t.TempDir(), "192.168.0.19")

	cert, err := s.TLSConfig().GetCertificate(&tls.ClientHelloInfo{})
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(s.CAPEM) {
		t.Fatal("unusable authority")
	}
	if _, err := cert.Leaf.Verify(x509.VerifyOptions{
		DNSName: "192.168.0.19",
		Roots:   pool,
	}); err != nil {
		t.Errorf("a client dialling the configured address would see a warning: %v", err)
	}
}

// An address nobody configured cannot be answered for, and saying so here is
// the reason the installer writes SOUNDSTORM_TLS_HOSTS.
func TestAnUnconfiguredAddressIsNotCovered(t *testing.T) {
	s := selfSigned(t, t.TempDir())

	cert, err := s.TLSConfig().GetCertificate(&tls.ClientHelloInfo{})
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if covers(cert, "192.168.0.19") {
		t.Error("an address that was never configured is somehow covered")
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

// The server certificate has to survive a restart, not just the authority.
//
// It did not, and the consequence was not cosmetic. Most devices never install
// the authority; what people do instead is click through the browser's warning
// once, and a browser pins that exception to the exact certificate it saw. A
// fresh one on every start revoked it every time - so the warning returned
// after each restart, and with a service worker holding the shell in cache the
// symptom was a spinner that never stopped rather than a warning at all.
func TestTheServerCertificateSurvivesARestart(t *testing.T) {
	dir := t.TempDir()

	first, err := Load(Config{Mode: ModeSelfSigned, Dir: dir, Hosts: []string{"192.168.0.19"}, Log: testLog()})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	second, err := Load(Config{Mode: ModeSelfSigned, Dir: dir, Hosts: []string{"192.168.0.19"}, Log: testLog()})
	if err != nil {
		t.Fatalf("Load again: %v", err)
	}

	if first.fallback.Leaf.SerialNumber.Cmp(second.fallback.Leaf.SerialNumber) != 0 {
		t.Errorf("restart minted a new server certificate (%s then %s); every accepted "+
			"browser exception would be revoked",
			first.fallback.Leaf.SerialNumber, second.fallback.Leaf.SerialNumber)
	}
}

// But changing the addresses it has to cover must produce a new one, or
// setting SOUNDSTORM_TLS_HOSTS after the fact would do nothing.
func TestAddingAHostReissuesTheCertificate(t *testing.T) {
	dir := t.TempDir()

	before, err := Load(Config{Mode: ModeSelfSigned, Dir: dir, Log: testLog()})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	after, err := Load(Config{Mode: ModeSelfSigned, Dir: dir, Hosts: []string{"192.168.0.19"}, Log: testLog()})
	if err != nil {
		t.Fatalf("Load again: %v", err)
	}

	if before.fallback.Leaf.SerialNumber.Cmp(after.fallback.Leaf.SerialNumber) == 0 {
		t.Fatal("adding a host reused the old certificate, which does not cover it")
	}
	var found bool
	for _, ip := range after.fallback.Leaf.IPAddresses {
		if ip.String() == "192.168.0.19" {
			found = true
		}
	}
	if !found {
		t.Errorf("reissued certificate covers %v, not the new host", after.fallback.Leaf.IPAddresses)
	}
}
