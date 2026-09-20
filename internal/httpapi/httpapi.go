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
	"fmt"
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
	"github.com/gabehollberg/atrium/internal/state"
	"github.com/gabehollberg/atrium/internal/stream"
	"github.com/gabehollberg/atrium/internal/webui"
)

// maxCredentialBody caps a login or signup body. Credentials are short; this
// stops an unauthenticated endpoint from being a memory sink.
const maxCredentialBody = 4 << 10

// maxProgressBody caps a reading-position update. A CFI is a short string.
const maxProgressBody = 8 << 10

// Server wires everything to HTTP handlers.
type Server struct {
	reg              *source.Registry
	store            *state.Store
	auth             *auth.Manager
	setup            *provision.Manager
	proxy            *stream.Proxy
	perSourceTimeout time.Duration
	log              *slog.Logger
}

// Config configures the server.
type Config struct {
	Registry         *source.Registry
	Store            *state.Store
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
		store:            cfg.Store,
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

	// The reader's endpoints take source/id/path as query parameters rather
	// than path segments. Book ids and resource paths both contain slashes,
	// and two trailing wildcards in one pattern is not a thing.
	guarded.HandleFunc("GET /api/book/manifest", s.handleBookManifest)
	guarded.HandleFunc("GET /api/book/resource", s.handleBookResource)
	guarded.HandleFunc("GET /api/book/progress", s.handleGetProgress)
	guarded.HandleFunc("PUT /api/book/progress", s.handlePutProgress)
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

// openBook resolves the source/id query pair to a readable book.
func (s *Server) openBook(r *http.Request) (source.OpenBook, string, error) {
	sourceID := r.URL.Query().Get("source")
	itemID := r.URL.Query().Get("id")
	if sourceID == "" || itemID == "" {
		return nil, "", errors.New("source and id are required")
	}
	src, ok := s.reg.ByID(sourceID)
	if !ok {
		return nil, "", fmt.Errorf("unknown source %s", strconv.Quote(sourceID))
	}
	opener, ok := src.(source.BookOpener)
	if !ok {
		return nil, "", fmt.Errorf("source %s cannot be read in place", strconv.Quote(sourceID))
	}
	book, err := opener.OpenBook(r.Context(), itemID)
	if err != nil {
		return nil, "", err
	}
	return book, progressKey(sourceID, itemID), nil
}

// handleBookManifest lists what is inside a book, which is what the reader's
// resource loader needs before it can ask for anything.
func (s *Server) handleBookManifest(w http.ResponseWriter, r *http.Request) {
	book, _, err := s.openBook(r)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	defer book.Close()

	writeJSON(w, http.StatusOK, map[string]any{"entries": book.Entries()})
}

// handleBookResource serves one file from inside a book.
func (s *Server) handleBookResource(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}

	book, _, err := s.openBook(r)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	defer book.Close()

	data, contentType, err := book.Resource(path)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	// Book resources are immutable for the life of the file, and a reader
	// fetches the same chapter every time you page back into it.
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) handleGetProgress(w http.ResponseWriter, r *http.Request) {
	sourceID := r.URL.Query().Get("source")
	itemID := r.URL.Query().Get("id")
	if sourceID == "" || itemID == "" {
		writeError(w, http.StatusBadRequest, "source and id are required")
		return
	}
	p, ok := s.store.Progress(progressKey(sourceID, itemID))
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"found": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"found":     true,
		"location":  p.Location,
		"fraction":  p.Fraction,
		"updatedAt": p.UpdatedAt,
	})
}

func (s *Server) handlePutProgress(w http.ResponseWriter, r *http.Request) {
	sourceID := r.URL.Query().Get("source")
	itemID := r.URL.Query().Get("id")
	if sourceID == "" || itemID == "" {
		writeError(w, http.StatusBadRequest, "source and id are required")
		return
	}

	var body struct {
		Location string  `json:"location"`
		Fraction float64 `json:"fraction"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxProgressBody))
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "expected a JSON body with location and fraction")
		return
	}
	if body.Location == "" {
		writeError(w, http.StatusBadRequest, "location is required")
		return
	}

	err := s.store.SetProgress(progressKey(sourceID, itemID), state.Progress{
		Location:  body.Location,
		Fraction:  body.Fraction,
		UpdatedAt: time.Now().UTC(),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not save reading position")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"saved": true})
}

// progressKey namespaces a book by its source, so two libraries holding the
// same filename do not share a bookmark.
func progressKey(sourceID, itemID string) string {
	// Source ids are ours and never contain a slash, so this cannot be
	// ambiguous however many slashes the item id has.
	return sourceID + "/" + itemID
}

func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		// Media requests are numerous and boring; log them at debug so a movie
		// does not bury everything else.
		level := slog.LevelInfo
		// Book resources include probes for optional files that most books do
		// not have (encryption.xml, Apple and Kobo display options), so a 404
		// here is the expected answer rather than a problem.
		if strings.HasPrefix(r.URL.Path, "/api/stream/") ||
			strings.HasPrefix(r.URL.Path, "/api/art/") ||
			strings.HasPrefix(r.URL.Path, "/api/book/") ||
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
