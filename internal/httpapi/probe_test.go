package httpapi

import (
	"net/http"
	"testing"
)

func TestProbeSendsWhatWasAskedAndIsNeverCached(t *testing.T) {
	h := newHarness(t)
	h.signUp(t)

	resp, body := h.do(t, http.MethodGet, "/api/probe", "")
	if resp.StatusCode != http.StatusOK || len(body) != 128<<10 {
		t.Fatalf("default probe: status %d, %d bytes", resp.StatusCode, len(body))
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control %q, want no-store", resp.Header.Get("Cache-Control"))
	}
	if _, body := h.do(t, http.MethodGet, "/api/probe?b=1000000", ""); len(body) != maxProbe {
		t.Errorf("asked for 1MB, got %d bytes; the most is %d", len(body), maxProbe)
	}
}
