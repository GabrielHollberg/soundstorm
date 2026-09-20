// Package httpapi is SoundStorm's only published surface.
//
// Everything a person touches comes through here: the UI, the login, the
// search, and the media bytes. The backends are on the internal compose network
// with no published ports, so this is the only door.
//
//	GET  /                              the UI
//	GET  /healthz                       liveness, no upstream calls
//	GET  /ca.crt                        the local TLS authority, to install once
//	GET  /api/session                   whether an account exists / we are signed in
//	POST /api/signup                    create the one account (first boot only)
//	POST /api/login
//	POST /api/logout
//	GET  /api/setup                     per-backend provisioning progress
//	GET  /api/users                     the accounts on this server   (owner)
//	POST /api/users                     add one                       (owner)
//	DELETE /api/users/{id}              remove one                    (owner)
//	POST /api/users/{id}/password       reset somebody's password     (owner)
//	PUT  /api/users/{id}/libraries      which shelves they can see    (owner)
//	POST /api/account/password          change your own
//	GET  /api/search?q=&kind=&limit=    federated search
//	GET  /api/stream/{source}/{id}      media bytes, proxied
//	GET  /api/art/{source}/{id}         artwork, proxied
//	GET  /api/playback/{source}/{id}    how to play it: direct, HLS, or a track list
//	PUT  /api/playback/{source}/{id}    where you are now, saved upstream
//
// Everything from /api/setup down requires a session.
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gabehollberg/soundstorm/internal/auth"
	"github.com/gabehollberg/soundstorm/internal/federate"
	"github.com/gabehollberg/soundstorm/internal/library"
	"github.com/gabehollberg/soundstorm/internal/media"
	"github.com/gabehollberg/soundstorm/internal/provision"
	"github.com/gabehollberg/soundstorm/internal/source"
	"github.com/gabehollberg/soundstorm/internal/state"
	"github.com/gabehollberg/soundstorm/internal/stream"
	"github.com/gabehollberg/soundstorm/internal/webui"
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
	library          *library.Library
	auth             *auth.Manager
	setup            *provision.Manager
	proxy            *stream.Proxy
	perSourceTimeout time.Duration
	log              *slog.Logger
	caPEM            []byte
}

// Config configures the server.
type Config struct {
	Registry         *source.Registry
	Store            *state.Store
	Library          *library.Library
	Auth             *auth.Manager
	Setup            *provision.Manager
	PerSourceTimeout time.Duration
	Log              *slog.Logger

	// CAPEM is the local certificate authority to offer for download, when
	// SoundStorm generated one. Nil when TLS is off or a real certificate was
	// supplied, in which case there is nothing for anybody to install.
	CAPEM []byte
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
		library:          cfg.Library,
		auth:             cfg.Auth,
		setup:            cfg.Setup,
		proxy:            stream.New(cfg.Registry, cfg.Log),
		perSourceTimeout: timeout,
		log:              cfg.Log,
		caPEM:            cfg.CAPEM,
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
	// Unauthenticated on purpose: this is a public certificate, and you need
	// it installed BEFORE the browser will let you reach a login page at all.
	mux.HandleFunc("GET /ca.crt", s.handleCA)
	mux.HandleFunc("GET /api/session", s.handleSession)
	mux.HandleFunc("POST /api/signup", s.handleSignup)
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.handleLogout)

	// Everything past here needs a session, media bytes very much included.
	guarded := http.NewServeMux()
	guarded.HandleFunc("GET /api/setup", s.handleSetup)
	guarded.HandleFunc("POST /api/account/password", s.handleChangeOwnPassword)
	guarded.HandleFunc("GET /api/library", s.handleLibrary)
	guarded.HandleFunc("GET /api/search", s.handleSearch)
	// {id...} rather than {id}: an OPDS acquisition reference is a path with
	// slashes in it ("opds/download/1/epub/"), and that is the id the adapter
	// needs back to fetch the book.
	guarded.HandleFunc("GET /api/stream/{source}/{id...}", s.handleStream)
	guarded.HandleFunc("HEAD /api/stream/{source}/{id...}", s.handleStream)
	guarded.HandleFunc("GET /api/art/{source}/{id...}", s.handleArt)

	// Video may need transcoding, in which case the client loads a playlist
	// instead of a file. /api/playback says which; /api/hls serves the
	// playlist and its segments.
	guarded.HandleFunc("GET /api/playback/{source}/{id...}", s.handlePlayback)
	// The same resource the other way round: GET says how to play it and where
	// you left off, PUT says where you are now.
	guarded.HandleFunc("PUT /api/playback/{source}/{id...}", s.handleSetPosition)
	guarded.HandleFunc("GET /api/hls/{source}/{path...}", s.handleHLS)
	guarded.HandleFunc("GET /api/subtitle/{source}/{track...}", s.handleSubtitle)

	// The reader's endpoints take source/id/path as query parameters rather
	// than path segments. Book ids and resource paths both contain slashes,
	// and two trailing wildcards in one pattern is not a thing.
	guarded.HandleFunc("GET /api/book/manifest", s.handleBookManifest)
	guarded.HandleFunc("GET /api/book/resource", s.handleBookResource)
	guarded.HandleFunc("GET /api/book/progress", s.handleGetProgress)
	guarded.HandleFunc("PUT /api/book/progress", s.handlePutProgress)
	// Account management is the one thing the owner can do and a member
	// cannot, so it gets its own guard rather than a check inside each handler.
	owner := http.NewServeMux()
	owner.HandleFunc("GET /api/users", s.handleListUsers)
	owner.HandleFunc("POST /api/users", s.handleCreateUser)
	owner.HandleFunc("DELETE /api/users/{id}", s.handleDeleteUser)
	owner.HandleFunc("POST /api/users/{id}/password", s.handleSetUserPassword)
	owner.HandleFunc("PUT /api/users/{id}/libraries", s.handleSetUserLibraries)
	guarded.Handle("/api/users", s.auth.RequireOwner(owner))
	guarded.Handle("/api/users/", s.auth.RequireOwner(owner))

	mux.Handle("/api/", s.auth.Require(s.withUserContext(guarded)))

	return s.withLogging(mux)
}

// withUserContext hands the account id down to the adapters.
//
// Done once here rather than in each handler, and through internal/source
// rather than internal/auth, so that an adapter can find out who is asking
// without depending on how signing in works.
func (s *Server) withUserContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.FromContext(r.Context())
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		ctx := source.WithUserID(r.Context(), user.ID)
		// And what they are allowed to reach. Set here, once, for every
		// guarded route - the Registry refuses anything outside it, so a
		// handler cannot forget to check.
		ctx = source.WithAccess(ctx, auth.Access(user))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	// One shell for every state. The page asks /api/session and renders the
	// signup form, the login form or the search UI accordingly.
	webui.ServeShell(w, r)
}

// handleCA hands over the local authority so a device can trust it.
//
// Downloaded and installed once per device, after which every certificate
// SoundStorm issues is trusted - including ones minted later for an address it
// had never seen. That is the difference between a local authority and a bare
// self-signed certificate, and the reason for the chore being a one-off.
func (s *Server) handleCA(w http.ResponseWriter, _ *http.Request) {
	if len(s.caPEM) == 0 {
		http.Error(w, "this server has no certificate authority to install", http.StatusNotFound)
		return
	}
	// application/x-x509-ca-cert is what makes a phone offer to install it
	// rather than showing it as text.
	w.Header().Set("Content-Type", "application/x-x509-ca-cert")
	w.Header().Set("Content-Disposition", `attachment; filename="soundstorm-ca.crt"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(s.caPEM)
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"sources": s.reg.Len(),
	})
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	answer := map[string]any{
		"hasAccount": s.auth.HasAccount(),
		"signedIn":   false,
	}
	if user, ok := s.auth.UserFor(r); ok {
		answer["signedIn"] = true
		answer["user"] = publicUser(user)
	}
	writeJSON(w, http.StatusOK, answer)
}

// publicUser is what an account looks like over the wire. The salt, the hash
// and the iteration count are not in it, and must never be: this is returned to
// whoever asks, including a member listing themselves.
func publicUser(u state.User) map[string]any {
	// libraries is always present and always concrete, never null: a client
	// deciding which tabs to show should not have to know that nil means
	// everything.
	kinds := auth.Access(u).Kinds()
	names := make([]string, 0, len(kinds))
	for _, k := range kinds {
		names = append(names, string(k))
	}
	return map[string]any{
		"id":        u.ID,
		"name":      u.Name,
		"role":      u.Role,
		"owner":     u.IsOwner(),
		"createdAt": u.CreatedAt,
		"libraries": names,
		// Whether the stored value is a restriction at all, which is what an
		// owner editing somebody needs in order to show "everything" rather
		// than five ticked boxes that mean the same thing today and would stop
		// meaning it if a sixth library were ever added.
		"allLibraries": auth.Access(u).Unrestricted(),
	}
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
	owner, err := s.auth.Signup(creds.Username, creds.Password)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.log.Info("owner account created", "username", creds.Username)

	// Sign them straight in; making someone log in immediately after choosing a
	// password is a pointless step.
	token, expiry, _, err := s.auth.Login(creds.Username, creds.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "account created but sign-in failed; try signing in")
		return
	}
	s.auth.SetCookie(w, r, token, expiry)
	// The account comes back here as well as from /api/login: the UI needs to
	// know it is the owner straight away, and without this it would not find
	// out until the page was reloaded.
	writeJSON(w, http.StatusOK, map[string]any{
		"signedIn": true,
		"user":     publicUser(owner),
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	creds, err := decodeCredentials(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	token, expiry, user, err := s.auth.Login(creds.Username, creds.Password)
	if err != nil {
		// The 600k-iteration key derivation makes each attempt cost a few
		// hundred milliseconds, which is the only brute-force defence here.
		// A real lockout belongs in front of a multi-user version.
		s.log.Warn("failed sign-in", "remote", r.RemoteAddr)
		writeError(w, http.StatusUnauthorized, auth.ErrInvalidCredentials.Error())
		return
	}
	s.auth.SetCookie(w, r, token, expiry)
	writeJSON(w, http.StatusOK, map[string]any{
		"signedIn": true,
		"user":     publicUser(user),
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.auth.Logout(r); err != nil {
		s.log.Warn("logout", "err", err)
	}
	s.auth.ClearCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]any{"signedIn": false})
}

// --- accounts ----------------------------------------------------------------

// requireUser returns the account making this request. Everything behind
// auth.Require has one; not finding it is a routing mistake, not a sign-in
// problem, so it is reported as a server error rather than a 401.
func (s *Server) requireUser(w http.ResponseWriter, r *http.Request) (state.User, bool) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		s.log.Error("a guarded handler ran without an account in its context",
			"path", r.URL.Path)
		writeError(w, http.StatusInternalServerError, "could not identify the signed-in account")
		return state.User{}, false
	}
	return user, true
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	users, err := s.auth.Users(user)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(users))
	for _, u := range users {
		out = append(out, publicUser(u))
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	creds, err := decodeCredentials(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	created, err := s.auth.CreateUser(actor, creds.Username, creds.Password, state.RoleMember)
	if err != nil {
		writeError(w, statusFor(err), err.Error())
		return
	}
	s.log.Info("account created", "username", created.Name, "by", actor.Name)
	writeJSON(w, http.StatusOK, map[string]any{"user": publicUser(created)})
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if actor.ID == id {
		writeError(w, http.StatusBadRequest, "you cannot remove your own account")
		return
	}
	if _, exists := s.store.User(id); !exists {
		writeError(w, http.StatusNotFound, "no such account")
		return
	}

	// The backend accounts go first, on purpose. Deleting the SoundStorm
	// account drops the record of which Audiobookshelf user belonged to it, and
	// after that nothing knows what to clean up - the orphan would sit there
	// with somebody's listening history in it.
	s.setup.ForgetUser(r.Context(), id)

	if err := s.auth.DeleteUser(actor, id); err != nil {
		writeError(w, statusFor(err), err.Error())
		return
	}
	s.log.Info("account removed", "id", id, "by", actor.Name)
	writeJSON(w, http.StatusOK, map[string]any{"removed": true})
}

func (s *Server) handleSetUserLibraries(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireUser(w, r)
	if !ok {
		return
	}

	var body struct {
		// A pointer so that an absent key, an explicit null and an empty list
		// are three different requests: leave alone, allow everything, allow
		// nothing.
		Libraries *[]string `json:"libraries"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxCredentialBody))
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "expected a JSON body with a list of libraries")
		return
	}

	var libraries []string
	if body.Libraries != nil {
		libraries = *body.Libraries
		if libraries == nil {
			libraries = []string{}
		}
	}
	if err := s.auth.SetLibraries(actor, r.PathValue("id"), libraries); err != nil {
		writeError(w, statusFor(err), err.Error())
		return
	}

	updated, _ := s.store.User(r.PathValue("id"))
	s.log.Info("libraries changed", "for", updated.Name, "by", actor.Name,
		"libraries", updated.Libraries)
	writeJSON(w, http.StatusOK, map[string]any{"user": publicUser(updated)})
}

func (s *Server) handleSetUserPassword(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	s.changePassword(w, r, actor, r.PathValue("id"))
}

// handleChangeOwnPassword lets anybody change their own, which is the only
// account operation a member can perform.
func (s *Server) handleChangeOwnPassword(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	s.changePassword(w, r, actor, actor.ID)
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request, actor state.User, id string) {
	var body struct {
		Password string `json:"password"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxCredentialBody))
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "expected a JSON body with a password")
		return
	}
	if err := s.auth.SetPassword(actor, id, body.Password); err != nil {
		writeError(w, statusFor(err), err.Error())
		return
	}
	s.log.Info("password changed", "id", id, "by", actor.Name)
	writeJSON(w, http.StatusOK, map[string]any{"changed": true})
}

// statusFor maps an account error onto a status code. Being refused for lack of
// permission and being refused for a name already taken are different things,
// and a client that shows the message either way still wants the distinction.
func statusFor(err error) int {
	if errors.Is(err, auth.ErrForbidden) {
		return http.StatusForbidden
	}
	return http.StatusBadRequest
}

func (s *Server) handleSetup(w http.ResponseWriter, _ *http.Request) {
	statuses := s.setup.Statuses()
	writeJSON(w, http.StatusOK, map[string]any{
		"allReady": s.setup.AllReady(),
		"backends": statuses,
	})
}

// handleLibrary describes where media goes and how much is there.
//
// This is what the UI shows instead of an empty grid. A new user's first screen
// should tell them what to do next, and "your four folders are here and they
// are empty" is more useful than nothing at all.
func (s *Server) handleLibrary(w http.ResponseWriter, r *http.Request) {
	// Folders for libraries this account cannot see are left out entirely.
	// Listing a shelf somebody is not allowed to open, with a count of what is
	// on it, would be a strange thing to show a child account.
	access := source.AccessFrom(r.Context())
	var folders []library.Folder
	for _, f := range s.library.Folders() {
		if access.Permits(f.Kind) {
			folders = append(folders, f)
		}
	}

	// Pair each folder with what its backend has actually indexed. The gap
	// between the two is the interesting number: files on disk but nothing
	// searchable means a scan is still running, not that anything is broken.
	indexed := map[media.Kind]int{}
	for _, src := range s.reg.All(r.Context()) {
		if counter, ok := src.(interface{ Count() int }); ok {
			indexed[src.Kind()] += counter.Count()
		}
	}

	out := make([]map[string]any, 0, len(folders))
	empty := true
	for _, f := range folders {
		if f.Files > 0 {
			empty = false
		}
		entry := map[string]any{
			"kind":        f.Kind,
			"name":        f.Name,
			"path":        f.Hint,
			"description": f.Description,
			"example":     f.Example,
			"files":       f.Files,
		}
		if n, ok := indexed[f.Kind]; ok {
			entry["indexed"] = n
		}
		out = append(out, entry)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"empty":   empty,
		"folders": out,
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

// handlePlayback answers "how do I play this".
//
// The client asks before touching a player, because the answer decides which
// one to build: a plain <video src> for a file, hls.js attached to a playlist
// for anything the browser cannot decode, and a chapter list for a book that is
// thirty separate MP3s.
func (s *Server) handlePlayback(w http.ResponseWriter, r *http.Request) {
	sourceID, itemID := r.PathValue("source"), r.PathValue("id")
	src, ok := s.reg.ByID(r.Context(), sourceID)
	if !ok {
		writeError(w, http.StatusNotFound, "unknown source "+strconv.Quote(sourceID))
		return
	}

	// Direct play is the default and the fallback. If negotiation fails it is
	// better to hand over the file than to refuse: a playable file plays, and
	// an unplayable one gets a media error from the browser rather than
	// silence from SoundStorm.
	answer := map[string]any{
		"mode": source.PlaybackModeDirect,
		"url":  streamURL(sourceID, itemID),
	}

	// Only video negotiates. Everything else is handed over as it is.
	if negotiator, ok := src.(source.Negotiator); ok {
		play, err := negotiator.Playback(r.Context(), itemID)
		if err != nil {
			s.log.Warn("playback negotiation failed",
				"source", sourceID, "item", itemID, "err", err)
		} else {
			if play.Mode == source.PlaybackModeHLS {
				playlist := "/api/hls/" + url.PathEscape(sourceID) + "/" + escapePath(play.Path)
				if len(play.Query) > 0 {
					playlist += "?" + play.Query.Encode()
				}
				answer["mode"] = source.PlaybackModeHLS
				answer["url"] = playlist
			}
			// Subtitles are independent of how the video itself is delivered.
			if tracks := subtitleList(sourceID, play.Subtitles); len(tracks) > 0 {
				answer["subtitles"] = tracks
			}
		}
	}

	// A multi-file item has to say so, or a client plays the first file and
	// stops. Sent only when there is more than one: a single file is what the
	// direct url above already is, and a chapter list of one is noise.
	if lister, ok := src.(source.TrackLister); ok {
		tracks, err := lister.Tracks(r.Context(), itemID)
		if err != nil {
			s.log.Warn("track list failed",
				"source", sourceID, "item", itemID, "err", err)
		} else if len(tracks) > 1 {
			answer["tracks"] = trackList(sourceID, tracks)
		}
	}

	// Where they left off. The key being present is what tells a client this
	// item is worth saving a position for at all - a four minute song is not,
	// and a source with no backend to write it to could not anyway.
	if tracker, ok := src.(source.PositionTracker); ok {
		pos, err := tracker.Position(r.Context(), itemID)
		if err != nil {
			s.log.Warn("read position failed",
				"source", sourceID, "item", itemID, "err", err)
		}
		// Reported even after a failure, and even at zero: losing a saved
		// position is a small harm, but silently refusing to record new ones
		// for the rest of the session is a larger one.
		answer["position"] = pos
	}

	writeJSON(w, http.StatusOK, answer)
}

// handleSetPosition records how far into an item somebody got.
//
// It goes upstream rather than into SoundStorm's state. Audiobookshelf keeps
// position per title and syncs it to its own apps, so a chapter finished in the
// car is where a browser picks up. A private copy here would fork from the one
// every other client reads.
func (s *Server) handleSetPosition(w http.ResponseWriter, r *http.Request) {
	sourceID, itemID := r.PathValue("source"), r.PathValue("id")
	src, ok := s.reg.ByID(r.Context(), sourceID)
	if !ok {
		writeError(w, http.StatusNotFound, "unknown source "+strconv.Quote(sourceID))
		return
	}
	tracker, ok := src.(source.PositionTracker)
	if !ok {
		writeError(w, http.StatusNotImplemented, "this source does not remember position")
		return
	}

	var body struct {
		Seconds  float64 `json:"seconds"`
		Duration float64 `json:"duration"`
		Finished bool    `json:"finished"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxProgressBody))
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "expected a JSON body with seconds")
		return
	}
	// Audiobookshelf stores what it is given without checking it, so a NaN from
	// a confused player would land in a record its own apps then read back.
	// Nothing leaves here that is not a time.
	if !isTime(body.Seconds) || !isTime(body.Duration) {
		writeError(w, http.StatusBadRequest, "seconds and duration must be positive numbers")
		return
	}

	pos := source.Position{
		Seconds:  body.Seconds,
		Duration: body.Duration,
		Finished: body.Finished,
	}
	if err := tracker.SetPosition(r.Context(), itemID, pos); err != nil {
		s.log.Warn("save position failed",
			"source", sourceID, "item", itemID, "err", err)
		writeError(w, http.StatusBadGateway, "could not save your place")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"saved": true})
}

// isTime rejects anything a media element could not seek to. A JSON number is
// never NaN, but "1e999" decodes to +Inf without complaint.
func isTime(seconds float64) bool {
	return !math.IsNaN(seconds) && !math.IsInf(seconds, 0) && seconds >= 0
}

// streamURL is where a client fetches an item's, or a track's, bytes.
func streamURL(sourceID, id string) string {
	return "/api/stream/" + url.PathEscape(sourceID) + "/" + escapePath(id)
}

// subtitleList turns a source's tracks into something the browser can attach.
func subtitleList(sourceID string, tracks []source.SubtitleTrack) []map[string]any {
	out := make([]map[string]any, 0, len(tracks))
	for _, t := range tracks {
		out = append(out, map[string]any{
			"label":    t.Label,
			"language": t.Language,
			"forced":   t.Forced,
			"url":      "/api/subtitle/" + url.PathEscape(sourceID) + "/" + escapePath(t.ID),
		})
	}
	return out
}

// trackList turns an item's files into a playable chapter list. The client
// never sees a track id, only the url to fetch it from.
func trackList(sourceID string, tracks []source.Track) []map[string]any {
	out := make([]map[string]any, 0, len(tracks))
	for _, t := range tracks {
		entry := map[string]any{
			"title":        t.Title,
			"url":          streamURL(sourceID, t.ID),
			"startSeconds": t.StartSeconds,
		}
		if t.DurationSeconds > 0 {
			entry["durationSeconds"] = t.DurationSeconds
		}
		out = append(out, entry)
	}
	return out
}

// handleSubtitle proxies one subtitle track, converted upstream to WebVTT.
func (s *Server) handleSubtitle(w http.ResponseWriter, r *http.Request) {
	sourceID := r.PathValue("source")
	src, ok := s.reg.ByID(r.Context(), sourceID)
	if !ok {
		http.Error(w, "unknown source", http.StatusNotFound)
		return
	}
	provider, ok := src.(source.SubtitleProvider)
	if !ok {
		http.Error(w, "source has no subtitles", http.StatusNotImplemented)
		return
	}

	target, err := provider.SubtitleTarget(r.Context(), r.PathValue("track"))
	if err != nil {
		s.log.Warn("subtitle target", "source", sourceID, "err", err)
		http.Error(w, "could not build subtitle url", http.StatusBadGateway)
		return
	}
	s.proxy.Serve(w, r, target, "subtitle "+sourceID)
}

// handleHLS proxies a playlist or one of its segments.
//
// The path is passed through untouched because the playlist refers to its
// segments relatively: as long as this route mirrors the backend's own
// namespace, the browser resolves them onto here by itself and no playlist
// needs rewriting.
func (s *Server) handleHLS(w http.ResponseWriter, r *http.Request) {
	sourceID := r.PathValue("source")
	src, ok := s.reg.ByID(r.Context(), sourceID)
	if !ok {
		http.Error(w, "unknown source", http.StatusNotFound)
		return
	}
	provider, ok := src.(source.HLSProvider)
	if !ok {
		http.Error(w, "source does not serve playlists", http.StatusNotImplemented)
		return
	}

	target, err := provider.HLSTarget(r.Context(), r.PathValue("path"), r.URL.Query())
	if err != nil {
		s.log.Warn("hls target", "source", sourceID, "err", err)
		http.Error(w, "could not build playlist url", http.StatusBadGateway)
		return
	}
	s.proxy.Serve(w, r, target, "hls "+sourceID)
}

// escapePath escapes each segment but keeps the separators, so an id or
// playlist path containing slashes survives.
func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	s.proxy.ServeMedia(w, r, r.PathValue("source"), r.PathValue("id"))
}

func (s *Server) handleArt(w http.ResponseWriter, r *http.Request) {
	s.proxy.ServeArt(w, r, r.PathValue("source"), r.PathValue("id"))
}

// openBook resolves the source/id query pair to a readable book.
func (s *Server) openBook(r *http.Request) (source.OpenBook, error) {
	sourceID := r.URL.Query().Get("source")
	itemID := r.URL.Query().Get("id")
	if sourceID == "" || itemID == "" {
		return nil, errors.New("source and id are required")
	}
	src, ok := s.reg.ByID(r.Context(), sourceID)
	if !ok {
		return nil, fmt.Errorf("unknown source %s", strconv.Quote(sourceID))
	}
	opener, ok := src.(source.BookOpener)
	if !ok {
		return nil, fmt.Errorf("source %s cannot be read in place", strconv.Quote(sourceID))
	}
	return opener.OpenBook(r.Context(), itemID)
}

// handleBookManifest lists what is inside a book, which is what the reader's
// resource loader needs before it can ask for anything.
func (s *Server) handleBookManifest(w http.ResponseWriter, r *http.Request) {
	book, err := s.openBook(r)
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

	book, err := s.openBook(r)
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
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	sourceID := r.URL.Query().Get("source")
	itemID := r.URL.Query().Get("id")
	if sourceID == "" || itemID == "" {
		writeError(w, http.StatusBadRequest, "source and id are required")
		return
	}
	p, ok := s.store.Progress(progressKey(user.ID, sourceID, itemID))
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
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
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

	err := s.store.SetProgress(progressKey(user.ID, sourceID, itemID), state.Progress{
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

// progressKey namespaces a bookmark by who it belongs to and which library it
// came from, so two people reading the same book keep their own places and two
// libraries holding the same filename do not collide.
func progressKey(userID, sourceID, itemID string) string {
	// Account ids and source ids are both ours and neither contains a slash,
	// so this cannot be ambiguous however many slashes the item id has.
	return userID + "/" + sourceID + "/" + itemID
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
