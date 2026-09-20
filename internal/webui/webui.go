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
	"object-src 'none'; " +
	"base-uri 'none'; " +
	"form-action 'self'"

// ServeShell writes the single-page shell. The page decides for itself whether
// to show signup, login or the search UI, by asking /api/session.
func ServeShell(w http.ResponseWriter, r *http.Request) {
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
	w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
	_, _ = w.Write(page)
}
