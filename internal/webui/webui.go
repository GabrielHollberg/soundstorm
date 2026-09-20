// Package webui serves atrium's browser UI out of the binary.
//
// The assets are embedded rather than mounted so deployment stays one artifact
// and there is no build step to forget. There is no framework and no bundler on
// purpose: the UI is a search box, a grid and two players, and a toolchain would
// be more code than the thing it builds.
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
	_, _ = w.Write(page)
}
