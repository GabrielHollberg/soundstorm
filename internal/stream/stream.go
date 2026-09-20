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
	"net/http"
	"os"
	"time"

	"github.com/gabehollberg/soundstorm/internal/source"
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
	if target.ContentType != "" {
		// Set it explicitly so ServeContent does not sniff, and so an EPUB is
		// labelled as one rather than as a zip.
		w.Header().Set("Content-Type", target.ContentType)
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
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
	src, ok := p.reg.ByID(sourceID)
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
	src, ok := p.reg.ByID(sourceID)
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

// pipe delivers a target, whatever kind it is.
func (p *Proxy) pipe(w http.ResponseWriter, r *http.Request, target source.Target, what string) {
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
		p.log.Warn("upstream fetch failed", "what", what, "err", err)
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

	w.WriteHeader(resp.StatusCode)
	if method == http.MethodHead {
		return
	}
	if _, err := io.Copy(w, resp.Body); err != nil && r.Context().Err() == nil {
		p.log.Debug("stream copy ended early", "what", what, "err", err)
	}
}
