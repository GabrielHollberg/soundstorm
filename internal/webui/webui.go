// Package webui serves SoundStorm's browser UI out of the binary.
//
// The assets are embedded rather than mounted so deployment stays one artifact
// and there is no build step to forget. There is no framework and no bundler on
// purpose: the UI is a search box, a grid and two players, and a toolchain
// would be more code than the thing it builds.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed assets
var assetsFS embed.FS

// Assets returns a handler for the static files.
func Assets() http.Handler {
	sub, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		// Unreachable: the embed directive guarantees the directory exists.
		panic(err)
	}
	return http.FileServer(http.FS(sub))
}

// contentSecurityPolicy is load-bearing security, not hardening theatre.
//
// EPUB files may contain scripts. The reader renders book content in an iframe
// backed by a blob: URL, and a blob: document inherits the creating page's
// origin - so without a policy, a book downloaded from anywhere could run
// JavaScript with full access to SoundStorm's session cookie and every API it
// guards. "Open this book" would be "run this stranger's code as me".
//
// script-src 'self' stops that: only SoundStorm's own scripts execute. The cost
// is that books relying on embedded scripting will not be interactive, which is
// the trade foliate-js's own documentation recommends making.
//
// The rest is ordinary, with one deliberate allowance: a book's own
// stylesheets and fonts arrive as blob: URLs too, so blocking blob: in
// style-src renders every book unstyled. Stylesheets cannot execute code, so
// permitting them costs nothing that script-src was protecting.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self' 'unsafe-inline' blob:; " +
	"font-src 'self' data: blob:; " +
	"img-src 'self' data: blob:; " +
	"media-src 'self' blob:; " +
	"frame-src 'self' blob:; " +
	"connect-src 'self' blob:; " +
	// hls.js demuxes in a worker it creates from a blob.
	"worker-src 'self' blob:; " +
	"object-src 'none'; " +
	"base-uri 'none'; " +
	"form-action 'self'"

// ServeServiceWorker writes the worker that makes SoundStorm installable.
//
// Served from "/" because a service worker's default scope is its own
// directory: from /static/ it could never control the page at "/", which is
// the only page there is.
//
// No-store on purpose. A stale worker is the one cached file that can pin
// somebody to an old build permanently, since it is the thing that decides
// what else gets cached. Browsers already bypass the HTTP cache for worker
// updates, but saying so costs one header and removes the question.
func ServeServiceWorker(w http.ResponseWriter, r *http.Request) {
	body, err := assetsFS.ReadFile("assets/sw.js")
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(body)
}

// ServeManifest writes the web app manifest.
//
// Explicitly rather than through the file server because Go's mime table has
// no entry for .webmanifest, so it would be sniffed or sent as octet-stream,
// and Chrome ignores a manifest it is not handed as JSON.
func ServeManifest(w http.ResponseWriter, r *http.Request) {
	body, err := assetsFS.ReadFile("assets/manifest.webmanifest")
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/manifest+json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(body)
}

// ServeShell writes the single-page shell. The page decides for itself whether
// to show signup, login or the search UI, by asking /api/session.
//
// connectAlso adds origins the page may fetch from. It is how the page is
// allowed to check that the install's real https address works from this
// browser before moving there; with connect-src 'self' alone that probe is
// refused before it leaves.
func ServeShell(w http.ResponseWriter, r *http.Request, connectAlso ...string) {
	page, err := assetsFS.ReadFile("assets/index.html")
	if err != nil {
		http.Error(w, "ui unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// The shell is tiny and gates on live session state, so caching it only
	// creates confusing stale-login behaviour.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	csp := contentSecurityPolicy
	if len(connectAlso) > 0 {
		csp = strings.Replace(csp, "connect-src 'self' blob:", "connect-src 'self' blob: "+strings.Join(connectAlso, " "), 1)
	}
	w.Header().Set("Content-Security-Policy", csp)
	_, _ = w.Write(page)
}
