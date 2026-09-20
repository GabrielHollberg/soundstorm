// Package httpapi exposes the gateway over HTTP.
//
// Endpoints:
//
//	GET /healthz                     liveness, no upstream calls
//	GET /api/sources                 per-source health
//	GET /api/search?q=&kind=&limit=  federated search
//	GET /api/probe/{id}?q=           raw upstream response (opt-in)
package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gabehollberg/atrium/internal/federate"
	"github.com/gabehollberg/atrium/internal/media"
	"github.com/gabehollberg/atrium/internal/source"
)

// Server wires the registry to HTTP handlers.
type Server struct {
	reg              *source.Registry
	perSourceTimeout time.Duration
	enableProbe      bool
	log              *slog.Logger
}

// New builds the HTTP server.
func New(reg *source.Registry, perSourceTimeout time.Duration, enableProbe bool, log *slog.Logger) *Server {
	return &Server{reg: reg, perSourceTimeout: perSourceTimeout, enableProbe: enableProbe, log: log}
}

// Routes returns the mux with every endpoint registered.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /api/sources", s.handleSources)
	mux.HandleFunc("GET /api/search", s.handleSearch)
	mux.HandleFunc("GET /api/probe/{id}", s.handleProbe)
	return s.withLogging(mux)
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"sources": s.reg.Len(),
	})
}

func (s *Server) handleSources(w http.ResponseWriter, r *http.Request) {
	statuses := federate.HealthAll(r.Context(), s.reg, s.perSourceTimeout)
	allOK := true
	for _, st := range statuses {
		if !st.OK {
			allOK = false
			break
		}
	}
	// Report 200 even when a source is down: the gateway itself is healthy,
	// and the body says exactly which backend is not.
	writeJSON(w, http.StatusOK, map[string]any{
		"allOk":   allOK,
		"sources": statuses,
	})
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	text := strings.TrimSpace(q.Get("q"))
	if text == "" {
		writeError(w, http.StatusBadRequest, "query parameter q is required")
		return
	}

	query := media.Query{Text: text}

	// kind may repeat (?kind=music&kind=ebook) or be comma-separated.
	for _, raw := range q["kind"] {
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			k, ok := media.ParseKind(part)
			if !ok {
				writeError(w, http.StatusBadRequest, "unknown kind "+strconv.Quote(part))
				return
			}
			query.Kinds = append(query.Kinds, k)
		}
	}

	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 200 {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 200")
			return
		}
		query.Limit = n
	}

	result := federate.Search(r.Context(), s.reg, query, s.perSourceTimeout)
	writeJSON(w, http.StatusOK, result)
}

// handleProbe returns a source's raw upstream response, which is how you fix
// a field mapping against a real server instead of guessing at its schema.
// It is off unless enableProbe is set, because raw responses can contain
// credentials and more detail than a public endpoint should leak.
func (s *Server) handleProbe(w http.ResponseWriter, r *http.Request) {
	if !s.enableProbe {
		writeError(w, http.StatusNotFound, "probe endpoint is disabled; set enableProbe: true to turn it on")
		return
	}
	id := r.PathValue("id")
	src, ok := s.reg.ByID(id)
	if !ok {
		writeError(w, http.StatusNotFound, "no source with id "+strconv.Quote(id))
		return
	}
	prober, ok := src.(source.Prober)
	if !ok {
		writeError(w, http.StatusNotImplemented, "source "+strconv.Quote(id)+" does not support probing")
		return
	}

	text := strings.TrimSpace(r.URL.Query().Get("q"))
	if text == "" {
		text = "test"
	}
	body, contentType, err := prober.Probe(r.Context(), media.Query{Text: text, Limit: 5})
	if err != nil && len(body) == 0 {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if contentType == "" {
		contentType = "text/plain; charset=utf-8"
	}
	w.Header().Set("Content-Type", contentType)
	if err != nil {
		w.Header().Set("X-Probe-Error", err.Error())
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.log.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"tookMs", time.Since(start).Milliseconds(),
		)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(payload)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
