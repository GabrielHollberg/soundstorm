package httpapi

import (
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
