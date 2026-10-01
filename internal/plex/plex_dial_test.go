package plex

import (
	"net/netip"
	"testing"
)

// A Plex server's address is its owner's to write: never this machine, the
// compose network the media servers are on, link-local or 100.64/10 - but a
// server on the home network, yes.
func TestDialRefusesTheInsideAndAllowsTheHomeNetwork(t *testing.T) {
	compose := []netip.Prefix{netip.MustParsePrefix("172.20.0.0/16")}
	for ip, want := range map[string]bool{
		"192.168.0.40": true, "10.0.0.5": true, "203.0.113.9": true,
		"172.20.0.3": false, "127.0.0.1": false, "169.254.169.254": false, "100.83.119.105": false, "::1": false,
		"192.168.65.254": false, // Docker Desktop's way to the host's localhost
	} {
		if got := DialAllowed(netip.MustParseAddr(ip), compose); got != want {
			t.Errorf("%s: allowed %v, want %v", ip, got, want)
		}
	}
}

// Only the host and port of an address plex.tv gave are kept.
func TestCandidatesKeepOnlyWhere(t *testing.T) {
	c := &Client{AllowHost: func(string) bool { return true }}
	got := c.candidates(Server{connections: []connection{{URI: "http://192.168.0.40:32400/api/v2/admin?x=1"}}})
	if len(got) != 1 || got[0] != "http://192.168.0.40:32400" {
		t.Fatalf("candidates = %v", got)
	}
}
