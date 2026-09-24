package httpapi

import (
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Every install is under soundstorm.dev, so to a browser another install is
// the same site and a SameSite=Lax cookie goes along with its requests. The
// check is on origin, not site.
func TestCrossSiteWritesAreRefused(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	h := sameOrigin(ok)
	cases := []struct {
		method string
		header map[string]string
		want   int
	}{
		{"POST", map[string]string{"Sec-Fetch-Site": "cross-site"}, 403},
		{"POST", map[string]string{"Sec-Fetch-Site": "same-site"}, 403},
		{"PUT", map[string]string{"Sec-Fetch-Site": "same-site"}, 403},
		{"DELETE", map[string]string{"Origin": "https://other.home.soundstorm.dev:8099"}, 403},
		{"POST", map[string]string{"Origin": "null"}, 403},
		{"POST", map[string]string{"Sec-Fetch-Site": "same-origin"}, 204},
		{"POST", map[string]string{"Origin": "http://mine.example:8099"}, 204},
		{"POST", nil, 204}, // not a browser page
		{"GET", map[string]string{"Sec-Fetch-Site": "cross-site"}, 204}, // reads are not writes
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, "http://mine.example:8099/api/users", nil)
		for k, v := range c.header {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != c.want {
			t.Errorf("%s %v: %d, want %d", c.method, c.header, w.Code, c.want)
		}
	}
}

// Another site framing the app to trick a click is refused; our own PDF
// iframe is not.
func TestResponsesRefuseForeignFraming(t *testing.T) {
	h := newHarness(t)
	resp, _ := h.do(t, http.MethodGet, "/", "")
	if got := resp.Header.Get("X-Frame-Options"); got != "SAMEORIGIN" {
		t.Errorf("X-Frame-Options = %q", got)
	}
	if resp.Header.Get("Strict-Transport-Security") != "" {
		t.Error("HSTS promised over plain http")
	}
}

// A body that trickles in is cut off rather than holding a connection for
// ever. A stream has no body and is never subject to it.
func TestASlowBodyIsCutOff(t *testing.T) {
	old := bodyTimeout
	bodyTimeout = 300 * time.Millisecond
	defer func() { bodyTimeout = old }()

	var readErr error
	srv := httptest.NewServer(bodyDeadline(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
	})))
	defer srv.Close()

	pr, pw := io.Pipe()
	go func() {
		pw.Write([]byte("{"))
		time.Sleep(2 * time.Second)
		pw.Close()
	}()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/login", pr)
	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err == nil {
		resp.Body.Close()
	}
	if readErr == nil || !strings.Contains(readErr.Error(), "timeout") {
		t.Errorf("read error = %v, want a timeout", readErr)
	}
	if time.Since(start) > 1500*time.Millisecond {
		t.Errorf("took %v; the deadline did not fire", time.Since(start))
	}
}

// The same, through the real route chain. The test above builds bodyDeadline on
// its own, and so passed for as long as the deadline was a no-op in production:
// the logging middleware's recorder had no Unwrap, so SetReadDeadline answered
// ErrNotSupported to every handler behind it and the error was discarded.
func TestASlowBodyIsCutOffThroughTheRealRoutes(t *testing.T) {
	old := bodyTimeout
	bodyTimeout = 300 * time.Millisecond
	defer func() { bodyTimeout = old }()

	h := newHarness(t)
	pr, pw := io.Pipe()
	go func() {
		pw.Write([]byte(`{"username":`))
		time.Sleep(3 * time.Second)
		pw.Close()
	}()
	req, _ := http.NewRequest(http.MethodPost, h.srv.URL+"/api/login", pr)
	start := time.Now()
	done := make(chan struct{})
	go func() {
		if resp, err := h.client.Do(req); err == nil {
			resp.Body.Close()
		}
		close(done)
	}()
	select {
	case <-done:
		if took := time.Since(start); took > 1500*time.Millisecond {
			t.Errorf("took %v; the deadline did not fire through the real routes", took)
		}
	case <-time.After(2500 * time.Millisecond):
		t.Fatal("a trickled body held the request open; the deadline is not reaching the connection")
	}
}

// HSTS is promised only on the real certificate's name, over TLS, and with a
// self-healing one-week life - never a year, because a home certificate can
// lapse and a year-long pin would brick a pinned browser with no way through.
func TestHSTSIsScopedAndShortLived(t *testing.T) {
	const name = "abc123.home.soundstorm.dev"
	s := &Server{publicName: func() string { return name }}
	h := s.secureHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	// Over TLS, addressed to the real name: present, one week, no
	// includeSubDomains, no preload.
	r := httptest.NewRequest(http.MethodGet, "https://"+name+":8099/", nil)
	r.TLS = &tls.ConnectionState{}
	r.Host = name + ":8099"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	got := w.Header().Get("Strict-Transport-Security")
	if got != "max-age=604800" {
		t.Errorf("HSTS = %q, want exactly max-age=604800 (one week)", got)
	}
	if strings.Contains(got, "includeSubDomains") || strings.Contains(got, "preload") {
		t.Errorf("HSTS carries includeSubDomains or preload: %q", got)
	}

	// Over TLS but addressed to something other than the real name: absent.
	r2 := httptest.NewRequest(http.MethodGet, "https://192.168.0.19:8099/", nil)
	r2.TLS = &tls.ConnectionState{}
	r2.Host = "192.168.0.19:8099"
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, r2)
	if w2.Header().Get("Strict-Transport-Security") != "" {
		t.Error("HSTS was sent for an address that is not the real certificate's name")
	}

	// No real certificate loaded (name empty): absent even on TLS.
	none := &Server{publicName: func() string { return "" }}
	hn := none.secureHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	r3 := httptest.NewRequest(http.MethodGet, "https://"+name+"/", nil)
	r3.TLS = &tls.ConnectionState{}
	r3.Host = name
	w3 := httptest.NewRecorder()
	hn.ServeHTTP(w3, r3)
	if w3.Header().Get("Strict-Transport-Security") != "" {
		t.Error("HSTS was promised while no real certificate was loaded")
	}
}
