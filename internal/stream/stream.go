// Package stream pipes media bytes from a backend to the browser.
//
// This reverses an explicit decision in SoundStorm's first design, which said
// results carry absolute upstream URLs and the client streams from the source.
// That kept SoundStorm tiny and out of the data path. It also made the product
// impossible: an upstream URL only works if the browser can reach the upstream,
// which means publishing Jellyfin and Navidrome on their own ports, which means
// their own login screens are one URL away and SoundStorm's single login is a
// decoration. You cannot have "one login" and "never touch the bytes" at once.
//
// So SoundStorm is in the data path, and the backends need no published port.
// The costs are real and worth naming: SoundStorm's bandwidth is now the
// ceiling, and restarting it interrupts playback. Both are acceptable on a home
// server where SoundStorm and the backends are the same machine; neither is
// acceptable at scale, and the escape hatch if it ever matters is signed
// short-lived URLs plus a path-based reverse proxy, which is the same idea with
// the proxy moved.
//
// Range requests are forwarded intact, which is what makes seeking work.
package stream

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/httpx"
	"github.com/GabrielHollberg/soundstorm/internal/source"
)

// forwardedRequestHeaders are the client headers that must reach the upstream
// for seeking and caching to work.
var forwardedRequestHeaders = []string{
	"Range",
	"If-Range",
	"If-None-Match",
	"If-Modified-Since",
}

// forwardedResponseHeaders are the upstream headers the browser needs to treat
// the response as seekable media rather than an opaque download.
var forwardedResponseHeaders = []string{
	"Content-Type",
	"Content-Length",
	"Content-Range",
	"Accept-Ranges",
	"ETag",
	"Last-Modified",
	"Cache-Control",
}

// Proxy serves media and artwork from the registered sources.
// serveFile delivers a file from SoundStorm's own disk.
func (p *Proxy) serveFile(w http.ResponseWriter, r *http.Request, target source.Target) {
	f, err := os.Open(target.FilePath)
	if err != nil {
		p.log.Warn("could not open local file", "path", target.FilePath, "err", err)
		http.Error(w, "file unavailable", http.StatusNotFound)
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		http.Error(w, "file unavailable", http.StatusNotFound)
		return
	}

	setContentHeaders(w, target)
	http.ServeContent(w, r, target.Name, info.ModTime(), f)
}

// serveBytes delivers something SoundStorm built in memory.
func (p *Proxy) serveBytes(w http.ResponseWriter, r *http.Request, target source.Target) {
	setContentHeaders(w, target)
	http.ServeContent(w, r, target.Name, target.ModTime, bytes.NewReader(target.Bytes))
}

func setContentHeaders(w http.ResponseWriter, target source.Target) {
	// Resolved here, not left to ServeContent: ServeContent fills in a type from
	// the name's extension only after this function has run, so a file named
	// .js would otherwise reach the browser as JavaScript having slipped past
	// GuardActiveContent's check.
	ct := target.ContentType
	if ct == "" {
		ct = mime.TypeByExtension(filepath.Ext(target.Name))
	}
	if ct != "" {
		// Set it explicitly so ServeContent does not sniff, and so an EPUB is
		// labelled as one rather than as a zip.
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	GuardActiveContent(w.Header())
}

type Proxy struct {
	reg *source.Registry
	log *slog.Logger
	hc  *http.Client
}

// New builds a Proxy.
func New(reg *source.Registry, log *slog.Logger) *Proxy {
	return &Proxy{
		reg: reg,
		log: log,
		hc: &http.Client{
			// No overall timeout: this is a feature-length video, not an API
			// call. The transport bounds the part that can actually hang -
			// waiting for the upstream to start responding.
			Timeout: 0,
			Transport: &http.Transport{
				ResponseHeaderTimeout: 30 * time.Second,
				IdleConnTimeout:       90 * time.Second,
				MaxIdleConnsPerHost:   4,
			},
		},
	}
}

// ServeMedia streams an item's bytes.
func (p *Proxy) ServeMedia(w http.ResponseWriter, r *http.Request, sourceID, itemID string) {
	src, ok := p.reg.ByID(r.Context(), sourceID)
	if !ok {
		http.Error(w, "unknown source", http.StatusNotFound)
		return
	}
	streamer, ok := src.(source.Streamer)
	if !ok {
		http.Error(w, "source cannot stream", http.StatusNotImplemented)
		return
	}
	target, err := streamer.StreamTarget(r.Context(), itemID)
	if err != nil {
		p.log.Error("build stream target", "source", sourceID, "item", itemID, "err", err)
		http.Error(w, "could not build stream url", http.StatusBadGateway)
		return
	}
	p.pipe(w, r, target, fmt.Sprintf("stream %s/%s", sourceID, itemID))
}

// ServeArt streams an item's artwork.
func (p *Proxy) ServeArt(w http.ResponseWriter, r *http.Request, sourceID, artID string) {
	src, ok := p.reg.ByID(r.Context(), sourceID)
	if !ok {
		http.Error(w, "unknown source", http.StatusNotFound)
		return
	}
	provider, ok := src.(source.ArtProvider)
	if !ok {
		http.Error(w, "source has no artwork", http.StatusNotImplemented)
		return
	}
	target, err := provider.ArtTarget(r.Context(), artID)
	if err != nil {
		http.Error(w, "could not build artwork url", http.StatusBadGateway)
		return
	}
	p.pipe(w, r, target, fmt.Sprintf("art %s/%s", sourceID, artID))
}

// Serve delivers a target the caller has already resolved. Used by the HLS
// route, where the path rather than an item id decides what to fetch.
func (p *Proxy) Serve(w http.ResponseWriter, r *http.Request, target source.Target, what string) {
	p.pipe(w, r, target, what)
}

// pipe delivers a target, whatever kind it is.
func (p *Proxy) pipe(w http.ResponseWriter, r *http.Request, target source.Target, what string) {
	// Nothing served here is ever meant to be loaded as a script, a worker or a
	// stylesheet - it is media, artwork, a book or a playlist. A book chapter
	// rendered on this origin can name any of these URLs in a <script src>, and
	// script-src 'self' would allow it, so refuse the request outright rather
	// than rely only on the content type (a client may send no Sec-Fetch-Dest;
	// GuardActiveContent covers that case).
	if RefusedDestination(r) {
		http.Error(w, "not available to this kind of request", http.StatusForbidden)
		return
	}
	// Local targets do not involve an upstream at all. http.ServeContent gives
	// Range, ETag and If-Modified-Since handling for free, which is strictly
	// better than what the proxy path below reimplements.
	switch {
	case target.FilePath != "":
		p.serveFile(w, r, target)
		return
	case target.Bytes != nil:
		p.serveBytes(w, r, target)
		return
	case target.URL == "":
		http.Error(w, "source produced an empty target", http.StatusBadGateway)
		return
	}

	// Whatever the target started on our behalf gets stopped, on every exit
	// path: a finished stream, a client that navigated away, a failure here.
	if target.OnDone != nil {
		defer target.OnDone()
	}

	method := r.Method
	if method != http.MethodHead {
		method = http.MethodGet
	}

	req, err := http.NewRequestWithContext(r.Context(), method, target.URL, nil)
	if err != nil {
		http.Error(w, "bad upstream url", http.StatusBadGateway)
		return
	}
	for _, h := range forwardedRequestHeaders {
		if v := r.Header.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}

	// Whatever credential this backend needs. Subsonic needs none here (its
	// credentials are already in the query string); Jellyfin needs an
	// Authorization header.
	for k, v := range target.Headers {
		req.Header.Set(k, v)
	}

	resp, err := p.hc.Do(req)
	if err != nil {
		// A cancelled client request is the normal way a video ends - somebody
		// hit stop. Do not shout about it.
		if r.Context().Err() != nil {
			return
		}
		p.log.Warn("upstream fetch failed", "what", what, "err", httpx.Redact(err))
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for _, h := range forwardedResponseHeaders {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	// Nothing upstream should be able to talk the browser into sniffing a
	// different content type than the one it declared.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	GuardActiveContent(w.Header())

	w.WriteHeader(resp.StatusCode)
	if method == http.MethodHead {
		return
	}
	if _, err := io.Copy(w, resp.Body); err != nil && r.Context().Err() == nil {
		p.log.Debug("stream copy ended early", "what", what, "err", err)
	}
}

// GuardActiveContent sandboxes a response the browser would run as a page.
// A book, a sidecar or a backend's upload can be HTML, XHTML or SVG with a
// script in it, and served from SoundStorm's origin that script would run
// with the session cookie of whoever opened the link - the same threat the
// shell's CSP answers for the reader, reached by pasting the URL instead.
// Media, PDFs and images are left alone: Chrome's PDF viewer will not render
// in a sandboxed document, and nothing else here can script.
//
// It also never lets a script content type through. JavaScript is not a
// document, so the sandbox above does nothing for it; what makes it dangerous is
// being pulled in by a <script src> on this origin (a book chapter, say), which
// the shell's script-src 'self' permits. With nosniff, a browser refuses to run
// text/plain as a script, so the bytes are still readable and never runnable.
func GuardActiveContent(h http.Header) {
	if IsScriptType(h.Get("Content-Type")) {
		h.Set("Content-Type", "text/plain; charset=utf-8")
	}
	if ActiveContent(h.Get("Content-Type")) {
		h.Set("Content-Security-Policy", "sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data:")
	}
}

// IsScriptType reports whether a browser would run this content type as script
// when it is loaded by a <script> or a worker.
func IsScriptType(contentType string) bool {
	ct := strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	switch ct {
	case "text/javascript", "application/javascript", "application/x-javascript",
		"text/ecmascript", "application/ecmascript", "text/jscript",
		"application/node", "module", "text/x-javascript", "application/x-ecmascript":
		return true
	}
	return false
}

// RefusedDestination reports whether a request is the browser loading a URL as
// a script, a worker or a stylesheet - never a legitimate way to fetch media,
// artwork, a book or a playlist, and the way an injected <script src> would.
func RefusedDestination(r *http.Request) bool {
	switch strings.ToLower(r.Header.Get("Sec-Fetch-Dest")) {
	case "script", "worker", "sharedworker", "serviceworker",
		"audioworklet", "paintworklet", "style", "xslt":
		return true
	}
	return false
}

// ActiveContent reports whether a content type is one a browser executes as a
// document. An empty type counts: with nothing declared, the browser guesses.
func ActiveContent(contentType string) bool {
	ct := strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	switch {
	case ct == "", ct == "text/html", ct == "text/xml", ct == "application/xml",
		ct == "text/xsl", ct == "image/svg+xml", ct == "application/octet-stream":
		return true
	case strings.HasSuffix(ct, "+xml"), strings.Contains(ct, "html"):
		return true
	}
	return false
}
