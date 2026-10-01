package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The app is served from / only, with its CSP. /static/ answering a folder
// gave the whole signed-in app without it (a security review), and listings.
func TestStaticServesNoPageAndNoFolder(t *testing.T) {
	h := http.StripPrefix("/static/", Assets())
	for _, path := range []string{"/static/", "/static/index.html", "/static/./index.html", "/static/vendor/", "/static/vendor/foliate-js/", "/static/icons/"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code == http.StatusOK || strings.Contains(w.Body.String(), "app.js") || strings.Contains(w.Body.String(), "<a href") {
			t.Errorf("%s: status %d, served %q", path, w.Code, strings.SplitN(w.Body.String(), "\n", 2)[0])
		}
	}
}

func TestStaticFilesStillServedUnderAPolicy(t *testing.T) {
	h := http.StripPrefix("/static/", Assets())
	for _, path := range []string{"/static/app.js", "/static/style.css", "/static/plex-done.html", "/static/favicon.svg"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK {
			t.Errorf("%s: status %d", path, w.Code)
		}
		if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") {
			t.Errorf("%s: CSP %q", path, csp)
		}
	}
}
