package stream

import (
	"net/http"
	"strings"
	"testing"
)

// A book, a sidecar or a backend's upload can be a page with a script in it,
// and served from SoundStorm's origin that script runs with the session of
// whoever opened the link. Everything a browser would execute as a document is
// sandboxed; media and PDFs are not, because Chrome will not render a PDF in a
// sandboxed document and a video has nothing to run.
func TestActiveContentIsSandboxed(t *testing.T) {
	for _, ct := range []string{
		"text/html", "text/html; charset=utf-8", "application/xhtml+xml", "image/svg+xml",
		"text/xml", "application/xml", "application/octet-stream", "",
	} {
		h := http.Header{}
		h.Set("Content-Type", ct)
		GuardActiveContent(h)
		if !strings.HasPrefix(h.Get("Content-Security-Policy"), "sandbox") {
			t.Errorf("%q was not sandboxed", ct)
		}
	}
	for _, ct := range []string{"video/mp4", "audio/mpeg", "application/pdf", "image/jpeg", "text/vtt", "application/vnd.apple.mpegurl", "video/mp2t", "application/epub+zip"} {
		h := http.Header{}
		h.Set("Content-Type", ct)
		GuardActiveContent(h)
		if h.Get("Content-Security-Policy") != "" {
			t.Errorf("%q was sandboxed, and would stop working", ct)
		}
	}
}
