package httpapi

import (
	"crypto/tls"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/GabrielHollberg/soundstorm/internal/auth"
	"github.com/GabrielHollberg/soundstorm/internal/state"
)

// serverWithHosts builds just enough Server to ask it for an address.
func serverWithHosts(t *testing.T, hosts ...string) *Server {
	t.Helper()
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	return New(Config{
		Store:    store,
		Auth:     auth.New(store),
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		LANHosts: hosts,
	})
}

// The address other people type. Getting this wrong prints a URL that does
// not work, which is worse than printing none - the .local attempt already
// taught that once.
func TestShareURL(t *testing.T) {
	cases := []struct {
		name  string
		hosts []string
		host  string // the request's Host header
		tls   bool
		want  string
	}{
		{
			// The ordinary install: the installer recorded the LAN address,
			// and the owner is looking at the page on the server itself.
			name:  "configured host, browsing on localhost",
			hosts: []string{"192.168.0.19"},
			host:  "localhost:8099",
			want:  "http://192.168.0.19:8099",
		},
		{
			// The port has to come from the request, because compose's port
			// mapping is never passed into the container. 8099 here proves
			// the container's own :8080 was not used.
			name:  "port comes from the request, not the listener",
			hosts: []string{"192.168.0.19"},
			host:  "localhost:9001",
			want:  "http://192.168.0.19:9001",
		},
		{
			name:  "https when the request arrived encrypted",
			hosts: []string{"192.168.0.19"},
			host:  "localhost:8099",
			tls:   true,
			want:  "https://192.168.0.19:8099",
		},
		{
			// Nothing configured, but this request did not come from this
			// machine - so the address in front of us demonstrably works for
			// somebody else.
			name: "falls back to a non-loopback request host",
			host: "192.168.1.50:8099",
			want: "http://192.168.1.50:8099",
		},
		{
			// Nothing configured and nothing to learn from: say nothing.
			name: "nothing known, nothing claimed",
			host: "localhost:8099",
			want: "",
		},
		{name: "127.0.0.1 is not an answer either", host: "127.0.0.1:8099", want: ""},
		{name: "0.0.0.0 is not an answer either", host: "0.0.0.0:8099", want: ""},
		{name: "::1 is not an answer either", host: "[::1]:8099", want: ""},
		{
			// Behind a proxy on 443 there is no port in Host at all, and
			// appending one would be inventing it.
			name:  "no port in Host means no port in the answer",
			hosts: []string{"media.example"},
			host:  "media.example",
			want:  "http://media.example",
		},
		{
			// An IPv6 literal has to keep its brackets or it is not an address.
			name:  "IPv6 keeps its brackets",
			hosts: []string{"fd00::1"},
			host:  "localhost:8099",
			want:  "http://[fd00::1]:8099",
		},
		{
			// Blank entries in SOUNDSTORM_TLS_HOSTS are easy to write by hand
			// and must not become "http://:8099".
			name:  "blank entries are skipped",
			hosts: []string{"", "  ", "192.168.0.19"},
			host:  "localhost:8099",
			want:  "http://192.168.0.19:8099",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := serverWithHosts(t, tc.hosts...)
			req := httptest.NewRequest(http.MethodGet, "/api/library", nil)
			req.Host = tc.host
			if tc.tls {
				req.TLS = &tls.ConnectionState{}
			}
			if got := s.shareURL(req); got != tc.want {
				t.Errorf("shareURL = %q, want %q", got, tc.want)
			}
		})
	}
}

// And it reaches the screen that shows it.
func TestLibraryCarriesTheShareURL(t *testing.T) {
	h := newHarness(t)
	h.signUp(t)

	_, body := h.do(t, http.MethodGet, "/api/library", "")
	var out struct {
		ShareURL string `json:"shareURL"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// The harness serves on 127.0.0.1 with no LAN host configured, so the
	// honest answer is none. A non-empty string here would mean the fallback
	// had started handing out loopback addresses.
	if out.ShareURL != "" {
		t.Errorf("shareURL = %q, want empty for a loopback-only server", out.ShareURL)
	}
}
