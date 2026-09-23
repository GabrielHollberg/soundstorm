package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The manifest is what makes "Add to Home Screen" produce an app rather than a
// bookmark, and every one of these fields is load-bearing for that: Chrome
// silently declines to install without a name, a 192 and a 512 icon, a
// start_url and display: standalone. Silently is the problem - there is no
// error anywhere, the install option simply never appears.
func TestManifestIsInstallable(t *testing.T) {
	h := newHarness(t)
	resp, body := h.do(t, http.MethodGet, "/manifest.webmanifest", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	// Chrome ignores a manifest it is not handed as JSON, and Go's mime table
	// has no entry for .webmanifest - so this header has to be set by hand and
	// is worth asserting.
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/manifest+json") {
		t.Errorf("content type = %q, want application/manifest+json", ct)
	}

	var m struct {
		Name       string `json:"name"`
		ShortName  string `json:"short_name"`
		StartURL   string `json:"start_url"`
		Scope      string `json:"scope"`
		Display    string `json:"display"`
		ThemeColor string `json:"theme_color"`
		Icons      []struct {
			Src     string `json:"src"`
			Sizes   string `json:"sizes"`
			Type    string `json:"type"`
			Purpose string `json:"purpose"`
		} `json:"icons"`
	}
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}

	if m.Name == "" || m.ShortName == "" {
		t.Error("a manifest with no name cannot be installed")
	}
	if m.StartURL != "/" || m.Scope != "/" {
		t.Errorf("start_url = %q, scope = %q, want both /", m.StartURL, m.Scope)
	}
	if m.Display != "standalone" {
		t.Errorf("display = %q, want standalone - anything else keeps the browser chrome", m.Display)
	}
	if m.ThemeColor == "" {
		t.Error("no theme_color, so the status bar will not match the app")
	}

	// 192 and 512 are the two Chrome actually requires, and a maskable one is
	// what stops Android cropping the glyph off inside its circle.
	var has192, has512, hasMaskable bool
	for _, icon := range m.Icons {
		if icon.Sizes == "192x192" {
			has192 = true
		}
		if icon.Sizes == "512x512" && icon.Purpose != "maskable" {
			has512 = true
		}
		if icon.Purpose == "maskable" {
			hasMaskable = true
		}
	}
	if !has192 || !has512 {
		t.Errorf("icons = %+v, want both a 192 and a 512", m.Icons)
	}
	if !hasMaskable {
		t.Error("no maskable icon, so Android will crop the mark")
	}

	// An icon named in the manifest that 404s fails the install with the same
	// silence as having no icon at all.
	for _, icon := range m.Icons {
		iconResp, iconBody := h.do(t, http.MethodGet, icon.Src, "")
		if iconResp.StatusCode != http.StatusOK {
			t.Errorf("icon %s: status %d", icon.Src, iconResp.StatusCode)
			continue
		}
		if len(iconBody) < 8 || string(iconBody[1:4]) != "PNG" {
			t.Errorf("icon %s is not a PNG", icon.Src)
		}
	}
}

// A service worker may only control paths at or below its own URL. Served from
// /static/ it could never intercept "/", which is the only page SoundStorm has,
// and the install would appear to work while controlling nothing.
func TestServiceWorkerIsServedFromTheRoot(t *testing.T) {
	h := newHarness(t)
	resp, body := h.do(t, http.MethodGet, "/sw.js", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "javascript") {
		t.Errorf("content type = %q, want javascript - browsers refuse a worker served as anything else", ct)
	}
	// The worker decides what else gets cached, so a stale one is the single
	// file that can pin somebody to an old build.
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	if !strings.Contains(string(body), "addEventListener") {
		t.Error("that does not look like a service worker")
	}
}

// The two rules in sw.js that matter, asserted against the file itself rather
// than trusted to stay true: caching a search result serves stale state, and
// answering a range request without honouring Range breaks seeking in a way
// that looks exactly like a corrupt file.
func TestServiceWorkerRefusesAPIAndMedia(t *testing.T) {
	h := newHarness(t)
	_, body := h.do(t, http.MethodGet, "/sw.js", "")
	src := string(body)

	for _, must := range []string{"'/api/'", "range"} {
		if !strings.Contains(src, must) {
			t.Errorf("sw.js does not mention %s, so it may no longer exclude it", must)
		}
	}
	// Nothing under /api/ should appear in the precache list.
	if i := strings.Index(src, "const SHELL"); i >= 0 {
		shell := src[i:]
		if j := strings.Index(shell, "]"); j >= 0 {
			if strings.Contains(shell[:j], "/api/") {
				t.Error("the precache list contains an API path")
			}
		}
	}
}

// A page load must reach the browser's own networking, never the cache. The
// worker used to answer a failed load with the cached shell, and a browser
// pins a clicked-through certificate exception to one exact certificate - so
// after a reinstall or the yearly renewal the cached shell loaded, everything
// behind it failed TLS, and the browser's warning, the only way out, never
// appeared. Not in a new tab either, because the worker answered that too.
func TestServiceWorkerNeverAnswersAPageLoad(t *testing.T) {
	h := newHarness(t)
	_, body := h.do(t, http.MethodGet, "/sw.js", "")
	src := string(body)

	if !strings.Contains(src, "request.mode === 'navigate') return false") {
		t.Error("sw.js no longer refuses navigations, so a changed certificate can hide behind the cache again")
	}
	if strings.Contains(src, "caches.match('/')") {
		t.Error("sw.js falls back to a cached page")
	}
	if i := strings.Index(src, "const SHELL"); i >= 0 {
		shell := src[i:]
		if j := strings.Index(shell, "]"); j >= 0 && strings.Contains(shell[:j], "'/'") {
			t.Error("the page itself is in the precache list")
		}
	}
}

// Registration is in the shell, so a worker that fails to install can never be
// the reason the app does not start - and the iOS tags are the whole of what
// iOS reads, since Safari ignores the manifest entirely.
func TestShellCarriesTheInstallTags(t *testing.T) {
	h := newHarness(t)
	_, body := h.do(t, http.MethodGet, "/", "")
	page := string(body)

	for _, want := range []string{
		`rel="manifest"`,
		`name="theme-color"`,
		`name="apple-mobile-web-app-capable"`,
		`rel="apple-touch-icon"`,
		// Without viewport-fit=cover, env(safe-area-inset-*) is zero and an
		// installed app draws under the notch and the home indicator.
		`viewport-fit=cover`,
		`/static/sw-register.js`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("shell is missing %s", want)
		}
	}
}

// The shell is served with script-src 'self', so an inline <script> is refused
// and the only sign is a console message nobody is watching. The registration
// shipped inline first and was blocked exactly this way: everything looked
// fine and "Add to Home Screen" produced a bookmark.
//
// The policy is not the thing to relax - it is what stops an EPUB running its
// own JavaScript against the session cookie - so the shell must simply never
// carry executable inline script.
func TestShellHasNoInlineScript(t *testing.T) {
	h := newHarness(t)
	resp, body := h.do(t, http.MethodGet, "/", "")
	page := string(body)

	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") {
		t.Fatalf("script-src 'self' is gone from the CSP: %q", csp)
	}

	for rest := page; ; {
		i := strings.Index(rest, "<script")
		if i < 0 {
			break
		}
		rest = rest[i:]
		open := strings.Index(rest, ">")
		close := strings.Index(rest, "</script>")
		if open < 0 || close < 0 {
			break
		}
		tag, inner := rest[:open], strings.TrimSpace(rest[open+1:close])
		// A tag with a src is fetched as a separate resource and is fine; only
		// a body between the tags would be refused.
		if !strings.Contains(tag, "src=") && inner != "" {
			t.Errorf("inline script in the shell, which script-src 'self' will silently refuse:\n%.120s", inner)
		}
		rest = rest[close+len("</script>"):]
	}

	// And the file it points at has to actually be there.
	regResp, regBody := h.do(t, http.MethodGet, "/static/sw-register.js", "")
	if regResp.StatusCode != http.StatusOK {
		t.Fatalf("sw-register.js: status %d", regResp.StatusCode)
	}
	if !strings.Contains(string(regBody), "serviceWorker.register") {
		t.Error("sw-register.js does not register anything")
	}
}

// The library screen names the one folder everything lives under, so the API
// has to send it - and it has to be the hint, not the path SoundStorm sees.
// Inside a container the root is /library, which is a path that exists on
// nobody's computer; the hint is "./library", which is where it actually is
// from where they ran compose.
func TestLibraryReportsTheRootTheUserSees(t *testing.T) {
	h := newHarness(t)
	h.signUp(t)

	_, body := h.do(t, http.MethodGet, "/api/library", "")
	var out struct {
		Root    string `json:"root"`
		Folders []struct {
			Kind string `json:"kind"`
			Name string `json:"name"`
		} `json:"folders"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// The harness opens its library with this hint, standing in for what
	// compose passes.
	if out.Root != "./library" {
		t.Errorf("root = %q, want the hint ./library rather than the container path", out.Root)
	}
	if len(out.Folders) == 0 {
		t.Fatal("no folders, so the screen would have nothing to summarise")
	}
	// Every folder needs a kind, because the UI looks up what to call a shelf
	// in a sentence by kind - "tv" reads like a typo mid-sentence, "TV" does
	// not - and falls back to the raw folder name when it cannot.
	for _, f := range out.Folders {
		if f.Kind == "" {
			t.Errorf("folder %q has no kind", f.Name)
		}
	}
}
