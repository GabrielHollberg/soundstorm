// Package httpapi is atrium's only published surface.
//
// Everything a person touches comes through here: the UI, the login, the search,
// and the media bytes. The backends are on the internal compose network with no
// published ports, so this is the only door.
//
//	GET  /                              the UI
//	GET  /healthz                       liveness, no upstream calls
//	GET  /api/session                   whether an account exists / we are signed in
//	POST /api/signup                    create the one account (first boot only)
//	POST /api/login
//	POST /api/logout
//	GET  /api/setup                     per-backend provisioning progress
//	GET  /api/search?q=&kind=&limit=    federated search
//	GET  /api/stream/{source}/{id}      media bytes, proxied
//	GET  /api/art/{source}/{id}         artwork, proxied
//
// Everything from /api/setup down requires a session.
package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gabehollberg/atrium/internal/auth"
	"github.com/gabehollberg/atrium/internal/federate"
	"github.com/gabehollberg/atrium/internal/media"
	"github.com/gabehollberg/atrium/internal/provision"
	"github.com/gabehollberg/atrium/internal/source"
	"github.com/gabehollberg/atrium/internal/stream"
	"github.com/gabehollberg/atrium/internal/webui"
)

// maxCredentialBody caps a login or signup body. Credentials are short; this
// stops an unauthenticated endpoint from being a memory sink.
const maxCredentialBody = 4 << 10

// Server wires everything to HTTP handlers.
type Server struct {
	reg              *source.Registry
	auth             *auth.Manager
	setup            *provision.Manager
	proxy            *stream.Proxy
	perSourceTimeout time.Duration
	log              *slog.Logger
}

// Config configures the server.
type Config struct {
	Registry         *source.Registry
	Auth             *auth.Manager
	Setup            *provision.Manager
	PerSourceTimeout time.Duration
	Log              *slog.Logger
}

// New builds the HTTP server.
func New(cfg Config) *Server {
	timeout := cfg.PerSourceTimeout
	if timeout <= 0 {
		timeout = federate.DefaultPerSourceTimeout
	}
	return &Server{
		reg:              cfg.Registry,
		auth:             cfg.Auth,
		setup:            cfg.Setup,
		proxy:            stream.New(cfg.Registry, cfg.Log),
		perSourceTimeout: timeout,
		log:              cfg.Log,
	}
}

// Routes returns the mux with every endpoint registered.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	// Open endpoints: the shell, its assets, and just enough to decide which
	// form to show someone who is not signed in.
	mux.Handle("GET /static/", http.StripPrefix("/static/", webui.Assets()))
	// {$} anchors this to exactly "/". A bare "GET /" would be a catch-all that
	// ServeMux refuses to combine with the method-less "/api/" guard below.
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /api/session", s.handleSession)
	mux.HandleFunc("POST /api/signup", s.handleSignup)
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.handleLogout)

	// Everything past here needs a session, media bytes very much included.
	guarded := http.NewServeMux()
	guarded.HandleFunc("GET /api/setup", s.handleSetup)
	guarded.HandleFunc("GET /api/search", s.handleSearch)
	// {id...} rather than {id}: an OPDS acquisition reference is a path with
	// slashes in it ("opds/download/1/epub/"), and that is the id the adapter
	// needs back to fetch the book.
	guarded.HandleFunc("GET /api/stream/{source}/{id...}", s.handleStream)
	guarded.HandleFunc("HEAD /api/stream/{source}/{id...}", s.handleStream)
	guarded.HandleFunc("GET /api/art/{source}/{id...}", s.handleArt)
	mux.Handle("/api/", s.auth.Require(guarded))

	return s.withLogging(mux)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	// One shell for every state. The page asks /api/session and renders the
	// signup form, the login form or the search UI accordingly.
	webui.ServeShell(w, r)
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"sources": s.reg.Len(),
	})
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"hasAccount": s.auth.HasAccount(),
		"signedIn":   s.auth.Authenticated(r),
	})
}

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func decodeCredentials(r *http.Request) (credentials, error) {
	var c credentials
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxCredentialBody))
	if err := dec.Decode(&c); err != nil {
		return c, errors.New("expected a JSON body with username and password")
	}
	return c, nil
}

func (s *Server) handleSignup(w http.ResponseWriter, r *http.Request) {
	// Signup is open exactly once. After that it is a 409 forever, so a
	// stranger who finds the port cannot create the account you have not
	// created yet... but note that until you do, they can. Claim it right away.
	if s.auth.HasAccount() {
		writeError(w, http.StatusConflict, "an account already exists; sign in instead")
		return
	}
	creds, err := decodeCredentials(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.auth.Signup(creds.Username, creds.Password); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.log.Info("account created", "username", creds.Username)

	// Sign them straight in; making someone log in immediately after choosing a
	// password is a pointless step.
	token, expiry, err := s.auth.Login(creds.Username, creds.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "account created but sign-in failed; try signing in")
		return
	}
	s.auth.SetCookie(w, r, token, expiry)
	writeJSON(w, http.StatusOK, map[string]any{"signedIn": true})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	creds, err := decodeCredentials(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	token, expiry, err := s.auth.Login(creds.Username, creds.Password)
	if err != nil {
		// The 600k-iteration key derivation makes each attempt cost a few
		// hundred milliseconds, which is the only brute-force defence here.
		// A real lockout belongs in front of a multi-user version.
		s.log.Warn("failed sign-in", "remote", r.RemoteAddr)
		writeError(w, http.StatusUnauthorized, auth.ErrInvalidCredentials.Error())
		return
	}
	s.auth.SetCookie(w, r, token, expiry)
	writeJSON(w, http.StatusOK, map[string]any{"signedIn": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.auth.Logout(r); err != nil {
		s.log.Warn("logout", "err", err)
	}
	s.auth.ClearCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]any{"signedIn": false})
}

func (s *Server) handleSetup(w http.ResponseWriter, _ *http.Request) {
	statuses := s.setup.Statuses()
	writeJSON(w, http.StatusOK, map[string]any{
		"allReady": s.setup.AllReady(),
		"backends": statuses,
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

	// kind may repeat (?kind=music&kind=video) or be comma-separated.
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

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	s.proxy.ServeMedia(w, r, r.PathValue("source"), r.PathValue("id"))
}

func (s *Server) handleArt(w http.ResponseWriter, r *http.Request) {
	s.proxy.ServeArt(w, r, r.PathValue("source"), r.PathValue("id"))
}

func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		// Media requests are numerous and boring; log them at debug so a movie
		// does not bury everything else.
		level := slog.LevelInfo
		if strings.HasPrefix(r.URL.Path, "/api/stream/") ||
			strings.HasPrefix(r.URL.Path, "/api/art/") ||
			strings.HasPrefix(r.URL.Path, "/static/") {
			level = slog.LevelDebug
		}
		s.log.Log(r.Context(), level, "request",
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

// Flush lets the recorder sit in front of a streaming response without
// swallowing flushes.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(payload)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
