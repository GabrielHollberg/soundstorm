package stream

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/GabrielHollberg/soundstorm/internal/source"
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

// A script type is never served as one: pulled in by a <script src> from a book
// chapter on this origin, it would run with the reader's session, and the
// document sandbox does nothing for a script. It is demoted to text, which a
// browser under nosniff will not execute.
func TestScriptTypesAreNeverServed(t *testing.T) {
	for _, ct := range []string{"text/javascript", "application/javascript; charset=utf-8", "text/ecmascript", "module"} {
		h := http.Header{}
		h.Set("Content-Type", ct)
		GuardActiveContent(h)
		if got := h.Get("Content-Type"); IsScriptType(got) {
			t.Errorf("%q was left runnable as %q", ct, got)
		}
	}
}

// A local target with no declared type gets one from its name before the guard
// runs; otherwise ServeContent would fill in text/javascript afterwards.
func TestLocalScriptIsServedAsText(t *testing.T) {
	p := New(source.NewRegistry(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/art/ebooks/x", nil)
	p.Serve(rec, req, source.Target{Bytes: []byte("alert(1)"), Name: "cover.js"}, "test")

	if got := rec.Header().Get("Content-Type"); IsScriptType(got) || !strings.HasPrefix(got, "text/plain") {
		t.Errorf("a .js target was served as %q", got)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("nosniff missing")
	}
}

// Nothing served here is a script, worker or stylesheet, so a request made as
// one - the way an injected <script src> would ask - is refused.
func TestScriptDestinationsAreRefused(t *testing.T) {
	p := New(source.NewRegistry(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, dest := range []string{"script", "worker", "serviceworker", "style"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/art/ebooks/x", nil)
		req.Header.Set("Sec-Fetch-Dest", dest)
		p.Serve(rec, req, source.Target{Bytes: []byte("x"), Name: "cover.jpg"}, "test")
		if rec.Code != http.StatusForbidden {
			t.Errorf("Sec-Fetch-Dest %q = %d, want 403", dest, rec.Code)
		}
	}
	for _, dest := range []string{"image", "video", "audio", "iframe", "empty", ""} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/art/ebooks/x", nil)
		if dest != "" {
			req.Header.Set("Sec-Fetch-Dest", dest)
		}
		p.Serve(rec, req, source.Target{Bytes: []byte("x"), Name: "cover.jpg"}, "test")
		if rec.Code != http.StatusOK {
			t.Errorf("Sec-Fetch-Dest %q = %d, want 200", dest, rec.Code)
		}
	}
}
