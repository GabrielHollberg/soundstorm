package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// The bug this guards against: building a URL by assigning to url.URL.Path
// re-escapes any %XX already in the reference, so "the%20hobbit" goes out as
// "the%2520hobbit" and the upstream searches for the literal text
// "the%20hobbit". It failed silently - an empty result set, never an error -
// which is exactly why it survived in the original code.
func TestURLDoesNotDoubleEncode(t *testing.T) {
	c, err := New("http://books.example.com", time.Second)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cases := []struct {
		name string
		ref  string
		want string
	}{
		{
			name: "escaped space survives",
			ref:  "/opds/search/the%20hobbit",
			want: "http://books.example.com/opds/search/the%20hobbit",
		},
		{
			name: "escaped comma survives",
			ref:  "/get/EPUB/1/Guns%2C%20Germs%2C%20and%20Steel",
			want: "http://books.example.com/get/EPUB/1/Guns%2C%20Germs%2C%20and%20Steel",
		},
		{
			name: "plain path is untouched",
			ref:  "/rest/search3.view",
			want: "http://books.example.com/rest/search3.view",
		},
		{
			name: "relative reference is anchored",
			ref:  "Items",
			want: "http://books.example.com/Items",
		},
		{
			name: "query string in the reference is preserved, not escaped",
			ref:  "/opds/nav?offset=20",
			want: "http://books.example.com/opds/nav?offset=20",
		},
		{
			name: "absolute reference passes through",
			ref:  "https://other.example.com/a%20b",
			want: "https://other.example.com/a%20b",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := c.URL(tc.ref, nil); got != tc.want {
				t.Errorf("URL(%q) = %q, want %q", tc.ref, got, tc.want)
			}
		})
	}
}

// A base URL with a path prefix is what you get when a backend sits behind a
// reverse proxy. The prefix must survive, which plain ResolveReference on an
// absolute-path reference would not do.
func TestURLKeepsBasePathPrefix(t *testing.T) {
	c, err := New("http://host.example.com/jellyfin", time.Second)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got, want := c.URL("/Items", nil), "http://host.example.com/jellyfin/Items"; got != want {
		t.Errorf("URL = %q, want %q", got, want)
	}
	if got, want := c.URL("/Videos/7/stream", url.Values{"static": {"true"}}),
		"http://host.example.com/jellyfin/Videos/7/stream?static=true"; got != want {
		t.Errorf("URL = %q, want %q", got, want)
	}
}

// What the server actually receives is the thing that matters, so check the
// wire rather than only the string we built.
func TestRequestPathReachesUpstreamUnmangled(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// r.URL.Path is the decoded path; that is the form the upstream
		// application logic sees.
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c, err := New(srv.URL, 2*time.Second)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var out struct{ OK bool }
	if err := c.JSON(context.Background(), "/search/the%20hobbit", url.Values{"q": {"a b"}}, &out); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if want := "/search/the hobbit"; gotPath != want {
		t.Errorf("upstream saw path %q, want %q", gotPath, want)
	}
	if want := "q=a+b"; gotQuery != want {
		t.Errorf("upstream saw query %q, want %q", gotQuery, want)
	}
	if !out.OK {
		t.Error("response was not decoded")
	}
}

func TestNewRejectsBadBaseURLs(t *testing.T) {
	for _, bad := range []string{"ftp://example.com", "example.com", "", "http://"} {
		if _, err := New(bad, time.Second); err == nil {
			t.Errorf("New(%q) should have failed", bad)
		}
	}
}

// A non-2xx must not be an error at the Do layer: provisioning needs to tell
// "409, already set up" from "the host is unreachable".
func TestDoReportsStatusWithoutError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "already exists", http.StatusConflict)
	}))
	defer srv.Close()

	c, err := New(srv.URL, 2*time.Second)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := c.Do(context.Background(), Request{Path: "/auth/createAdmin"})
	if err != nil {
		t.Fatalf("Do returned a transport error for a 409: %v", err)
	}
	if resp.Status != http.StatusConflict {
		t.Errorf("status = %d, want 409", resp.Status)
	}
	if resp.OK() {
		t.Error("OK() should be false for a 409")
	}
	if resp.Err() == nil {
		t.Error("Err() should describe the 409 for callers that want it fatal")
	}
}

func TestSnippetStaysValidUTF8(t *testing.T) {
	// 300 bytes of a 3-byte rune: truncation must not split one.
	long := ""
	for range 100 {
		long += "✓"
	}
	got := Snippet([]byte(long))
	for i, r := range got {
		if r == '�' {
			t.Fatalf("Snippet produced an invalid rune at byte %d: %q", i, got)
		}
	}
}
