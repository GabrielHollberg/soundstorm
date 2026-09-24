package names

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestPublicAddressAcceptsOnlyPublicOnes(t *testing.T) {
	good := []string{"203.0.113.7", "8.8.8.8", "2606:4700:4700::1111"}
	for _, s := range good {
		if _, err := publicAddress(s); err != nil {
			t.Errorf("publicAddress(%q) = %v, want accepted", s, err)
		}
	}
	bad := []string{
		"192.168.0.19", "10.1.2.3", "172.16.5.5", // private
		"127.0.0.1", "::1", // loopback
		"169.254.1.1", "fe80::1", // link-local
		"100.64.0.1",    // CGNAT
		"0.0.0.0", "::", // unspecified
		"224.0.0.1", "ff02::1", // multicast
		"not-an-ip",
	}
	for _, s := range bad {
		if _, err := publicAddress(s); err == nil {
			t.Errorf("publicAddress(%q) was accepted; must be refused", s)
		}
	}
}

// The default prober fetches the install's reachability endpoint and checks it
// answers with the challenge only that install can compute. It must not follow
// a redirect, and a wrong or missing answer must fail.
func TestReachableVerifiesTheInstall(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef"
	s := &Server{Secret: []byte(secret)}
	id := "abcdefghij"
	token := tokenFor([]byte(secret), id)

	var mode string // "ok", "wrong", "redirect", "500"
	install := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch mode {
		case "redirect":
			http.Redirect(w, r, "http://169.254.169.254/latest/meta-data", http.StatusFound)
		case "500":
			w.WriteHeader(http.StatusInternalServerError)
		case "wrong":
			w.Write([]byte("not the right value"))
		default:
			nonce := r.URL.Query().Get("nonce")
			w.Write([]byte(Reachability(token, nonce)))
		}
	}))
	defer install.Close()
	port := install.Listener.Addr().(*net.TCPAddr).Port
	loop := netip.MustParseAddr("127.0.0.1")

	mode = "ok"
	if err := s.reachable(context.Background(), loop, port, id); err != nil {
		t.Errorf("a correctly-answering install failed the probe: %v", err)
	}
	for _, m := range []string{"wrong", "redirect", "500"} {
		mode = m
		if err := s.reachable(context.Background(), loop, port, id); err == nil {
			t.Errorf("mode %q passed the probe; it must fail", m)
		}
	}
	// A dead address fails rather than hanging.
	mode = "ok"
	install.Close()
	if err := s.reachable(context.Background(), loop, port, id); err == nil {
		t.Error("a closed port passed the probe")
	}
}

// The public name is published only once the probe passes, points at the
// caller's own source address (never one it supplies), and is refused when the
// source is not public.
func TestPublicNameNeedsAPassingProbe(t *testing.T) {
	dns := newFakeDNS()
	probed := ""
	s := &Server{
		Secret:         []byte("0123456789abcdef0123456789abcdef"),
		Zone:           "soundstorm.dev",
		Label:          "home",
		ClientIPHeader: "X-Real-Ip",
		DNS:            dns,
		probe: func(_ context.Context, addr netip.Addr, port int, id string) error {
			probed = addr.String()
			return nil
		},
	}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	c := &Client{Base: srv.URL}

	reg, err := c.Register(context.Background())
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	put := func(sourceIP, bodyIP string) *http.Response {
		body := `{"port":8099}`
		req, _ := http.NewRequest(http.MethodPut, srv.URL+"/v1/public", strings.NewReader(body))
		req.Header.Set("Authorization", reg.Credential())
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Real-Ip", sourceIP)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("do: %v", err)
		}
		return resp
	}

	// A public source address: published, pointed at that address.
	resp := put("203.0.113.7", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("public with a public source: %d", resp.StatusCode)
	}
	resp.Body.Close()
	if probed != "203.0.113.7" {
		t.Errorf("probed %q, want the source address", probed)
	}
	if got := dns.get(reg.ID + ".net A"); got != "203.0.113.7" {
		t.Errorf("A record = %q, want the source address", got)
	}

	// A private source address: refused, nothing published.
	dns2 := newFakeDNS()
	s.DNS = dns2
	resp = put("192.168.0.19", "")
	if resp.StatusCode == http.StatusOK {
		t.Errorf("a private source address was accepted: %d", resp.StatusCode)
	}
	resp.Body.Close()
	if got := dns2.get(reg.ID + ".net A"); got != "" {
		t.Errorf("a record was published for a private source: %q", got)
	}
}

// When the probe fails, no name is published and the caller is told to open the
// port.
func TestPublicNameNotPublishedWhenUnreachable(t *testing.T) {
	dns := newFakeDNS()
	s := &Server{
		Secret:         []byte("0123456789abcdef0123456789abcdef"),
		Zone:           "soundstorm.dev",
		Label:          "home",
		ClientIPHeader: "X-Real-Ip",
		DNS:            dns,
		probe: func(context.Context, netip.Addr, int, string) error {
			return errContext("port closed")
		},
	}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	c := &Client{Base: srv.URL}
	reg, _ := c.Register(context.Background())

	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/v1/public", strings.NewReader(`{"port":8099}`))
	req.Header.Set("Authorization", reg.Credential())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Real-Ip", "203.0.113.7")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Errorf("an unreachable install was published anyway: %d", resp.StatusCode)
	}
	if got := dns.get(reg.ID + ".net A"); got != "" {
		t.Errorf("a record was published for an unreachable install: %q", got)
	}
}

type errContext string

func (e errContext) Error() string { return string(e) }
