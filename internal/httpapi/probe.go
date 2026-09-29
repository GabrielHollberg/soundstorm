package httpapi

import (
	"math/rand/v2"
	"net/http"
	"strconv"
)

// probeBytes is what /api/probe sends: bytes no compression can shrink, made
// once, so the timing a client takes of it measures the link and nothing else.
var probeBytes = func() []byte {
	b := make([]byte, maxProbe)
	r := rand.New(rand.NewPCG(1, 2))
	for i := range b {
		b[i] = byte(r.Uint32())
	}
	return b
}()

const maxProbe = 256 << 10

// handleProbe sends ?b= bytes (128KB unless asked, at most 256KB) for the app
// to time. A phone away from home uses it once a session to learn whether the
// link from the house can carry full-quality music, and starts at a lower
// quality from the first song when it cannot - rather than finding out by a
// song that sits at 0:00. Never cached, or it would time the cache.
func (s *Server) handleProbe(w http.ResponseWriter, r *http.Request) {
	n := 128 << 10
	if v, err := strconv.Atoi(r.URL.Query().Get("b")); err == nil && v > 0 {
		n = min(v, maxProbe)
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.Itoa(n))
	_, _ = w.Write(probeBytes[:n])
}
