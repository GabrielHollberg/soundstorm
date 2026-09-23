package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

const testPublicName = "k3x9m2p7qa.home.soundstorm.dev"

func sessionFrom(t *testing.T, h *harness, host string) map[string]any {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, h.srv.URL+"/api/session", nil)
	if host != "" {
		req.Host = host
	}
	resp, err := h.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return out
}

// The page is told about the real address only when there is one and it is
// not already there - otherwise it would check, and move, to where it is.
func TestTheSessionOffersTheSecureNameOnlyWhenItHelps(t *testing.T) {
	h := newHarness(t)

	if _, offered := sessionFrom(t, h, "")["secureName"]; offered {
		t.Error("offered a secure name with no certificate behind it")
	}

	h.api.publicName = func() string { return testPublicName }
	if got := sessionFrom(t, h, "localhost:8099")["secureName"]; got != testPublicName {
		t.Errorf("from localhost, secureName = %v", got)
	}
	if _, offered := sessionFrom(t, h, testPublicName+":8099")["secureName"]; offered {
		t.Error("offered the secure name to a page already on it")
	}
}

// connect-src 'self' would refuse the check before it left the browser, and
// silently: the page would simply never move.
func TestThePageMayCheckTheSecureName(t *testing.T) {
	h := newHarness(t)
	resp, _ := h.do(t, http.MethodGet, "/", "")
	if strings.Contains(resp.Header.Get("Content-Security-Policy"), "soundstorm.dev") {
		t.Error("the policy names a public origin with no certificate behind it")
	}

	h.api.publicName = func() string { return testPublicName }
	resp, _ = h.do(t, http.MethodGet, "/", "")
	csp := resp.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "connect-src 'self' blob: https://"+testPublicName+":*") {
		t.Errorf("connect-src does not admit the secure name: %q", csp)
	}
	// Only connect-src: nothing else about the page should loosen.
	if !strings.Contains(csp, "script-src 'self';") {
		t.Errorf("script-src changed: %q", csp)
	}
}

// Once somebody is on the real name, that is the address to hand on - it is
// proven to work from here and needs no certificate installed anywhere.
func TestTheShareAddressIsTheSecureNameOnceOnIt(t *testing.T) {
	h := newHarness(t)
	h.api.lanHosts = []string{"192.168.0.19"}
	h.api.publicName = func() string { return testPublicName }

	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	r.Host = testPublicName + ":8099"
	if got := h.api.shareURL(r); got != "http://"+testPublicName+":8099" {
		// The harness serves plain HTTP; in life this request arrives over TLS
		// and the scheme follows.
		t.Errorf("shareURL on the secure name = %q", got)
	}

	r.Host = "localhost:8099"
	if got := h.api.shareURL(r); got != "http://192.168.0.19:8099" {
		t.Errorf("shareURL from localhost = %q, want the LAN address as before", got)
	}
}
