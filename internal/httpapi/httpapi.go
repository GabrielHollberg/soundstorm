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
//	POST /api/upload/plan               where would these dropped files go
//	PUT  /api/upload?path=&kind=        one file, body is the file
//	GET  /api/search?q=&kind=&limit=    federated search
//	GET  /api/stream/{source}/{id}      media bytes, proxied
//	GET  /api/art/{source}/{id}         artwork, proxied
//	GET  /api/playback/{source}/{id}    how to play it: direct, HLS, or a track list
//	PUT  /api/playback/{source}/{id}    where you are now, saved upstream
//
// Everything from /api/setup down requires a session.
package httpapi

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/auth"
	"github.com/GabrielHollberg/soundstorm/internal/collections"
	"github.com/GabrielHollberg/soundstorm/internal/federate"
	"github.com/GabrielHollberg/soundstorm/internal/library"
	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/provision"
	"github.com/GabrielHollberg/soundstorm/internal/source"
	"github.com/GabrielHollberg/soundstorm/internal/state"
	"github.com/GabrielHollberg/soundstorm/internal/stream"
	"github.com/GabrielHollberg/soundstorm/internal/webui"
)

// maxCredentialBody caps a login or signup body. Credentials are short; this
// stops an unauthenticated endpoint from being a memory sink.
const maxCredentialBody = 4 << 10

// maxProgressBody caps a reading-position update. A CFI is a short string.
const maxProgressBody = 8 << 10

// Server wires everything to HTTP handlers.
type Server struct {
	setupCode        string
	collections      *collections.Store
	reg              *source.Registry
	store            *state.Store
	library          *library.Library
	auth             *auth.Manager
	setup            *provision.Manager
	proxy            *stream.Proxy
	perSourceTimeout time.Duration
	log              *slog.Logger
	caPEM            []byte
	lanHosts         []string
	publicName       func() string
	remoteReach      func(nonce string) (string, bool)
	remoteStatus     func() RemoteState
	setRemoteAccess  func(bool) error

	// uploads counts each account's in-flight uploads; see takeUploadSlot.
	uploadsMu sync.Mutex
	uploads   map[string]int

	// rescans coalesces "look at your folder now" requests, keyed by kind,
	// and lastRescan is when one last actually fired - see scheduleRescan.
	rescanMu     sync.Mutex
	rescanTimers map[media.Kind]*time.Timer
	lastRescan   map[media.Kind]time.Time
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

	// LANHosts is what to tell somebody else on the network to type.
	//
	// It comes from SOUNDSTORM_TLS_HOSTS, which the installer fills in with
	// the machine's LAN address at install time. That is not a reuse of
	// convenience: the container genuinely cannot work this out, because
	// inside Docker the only addresses it can see are the container's own.
	// Whoever ran the installer was on the host and could.
	LANHosts []string

	// PublicName reports the install's real certificate name, or "" while it
	// has none - see servetls auto mode. Nil when that mode is off.
	PublicName func() string

	// RemoteReachability answers a name-service reachability challenge - an
	// HMAC of the nonce under this install's registration token - or reports
	// that there is nothing to answer with. It is how the name service, before
	// pointing a public name here, confirms the open port really reaches this
	// install. Nil when remote access is not configured.
	RemoteReachability func(nonce string) (string, bool)

	// RemoteStatus reports the current state of remote access, for the account
	// panel. See RemoteState.
	RemoteStatus func() RemoteState

	// SetRemoteAccess turns remote access on or off: it persists the choice and
	// nudges the certificate loop to act on it. Owner-only at the handler.
	SetRemoteAccess func(enabled bool) error

	// SetupCode is what the first sign-up must present. See handleSignup.
	SetupCode string

	// Collections holds each person's favourites and playlists.
	Collections *collections.Store
}

// RemoteState is the current state of remote access, for the account panel. It
// tells the owner not just whether it is on, but whether it actually worked -
// and if not, what to do about it (forward the port).
type RemoteState struct {
	// Available is whether remote access can be offered at all - it needs auto
	// TLS, the only mode with a name service and a real certificate.
	Available bool
	// Enabled is whether the owner has turned it on.
	Enabled bool
	// Name is the address to reach the install by from away, set only once it is
	// actually reachable there. Empty while off or not yet reachable.
	Name string
	// Port is the port the world reaches the install on - the one to forward by
	// hand when the automatic methods cannot.
	Port int
	// Upstream is what the router said stands in front of it: "shared"
	// (carrier-grade NAT) or "router" (double NAT). Either means a forward on
	// the home router cannot be reached, and the panel offers Tailscale
	// instead of asking for one. Empty when the connection looks direct.
	Upstream string
	// Mapped is whether the inbound port was opened automatically, and Method is
	// how ("UPnP", "PCP", "NAT-PMP"). Both empty/false when the port was not
	// mapped - remote access off, no method worked, or a hand-forwarded port.
	Mapped bool
	Method string
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
		lanHosts:         cfg.LANHosts,
		publicName:       cfg.PublicName,
		remoteReach:      cfg.RemoteReachability,
		remoteStatus:     cfg.RemoteStatus,
		setRemoteAccess:  cfg.SetRemoteAccess,
		setupCode:        NormalizeSetupCode(cfg.SetupCode),
		collections:      cfg.Collections,
		rescanTimers:     map[media.Kind]*time.Timer{},
		lastRescan:       map[media.Kind]time.Time{},
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
	// Both of these are served from the root rather than /static/, and the
	// reason is scope, not tidiness: a service worker may only control paths
	// at or below its own URL, so /static/sw.js could never intercept "/" -
	// the one request that has to work for the app to open offline. The
	// manifest sits beside it so start_url and scope read as the same origin
	// root they actually are.
	mux.HandleFunc("GET /sw.js", webui.ServeServiceWorker)
	mux.HandleFunc("GET /manifest.webmanifest", webui.ServeManifest)
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	// Unauthenticated on purpose: this is a public certificate, and you need
	// it installed BEFORE the browser will let you reach a login page at all.
	mux.HandleFunc("GET /ca.crt", s.handleCA)
	// Unauthenticated on purpose: the name service calls this over plain HTTP,
	// from the internet, before any certificate or session exists, to confirm
	// the open port really reaches this install. It reveals only an HMAC of a
	// nonce, which is nothing.
	mux.HandleFunc("GET /api/remote-reachable", s.handleRemoteReachable)
	mux.HandleFunc("GET /api/session", s.handleSession)
	mux.HandleFunc("POST /api/signup", s.handleSignup)
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.handleLogout)

	// Everything past here needs a session, media bytes very much included.
	guarded := http.NewServeMux()
	guarded.HandleFunc("GET /api/setup", s.handleSetup)
	guarded.HandleFunc("POST /api/account/password", s.handleChangeOwnPassword)
	guarded.HandleFunc("GET /api/library", s.handleLibrary)
	guarded.HandleFunc("POST /api/library/rescan", s.handleRescan)
	// Two steps rather than one multipart request. The plan is what lets the
	// UI say "14 files, 2 skipped, all going to Films" before a gigabyte
	// starts moving, and it is also what keeps a subtitle with its film: the
	// grouping needs to see the whole list, which a streamed upload does not.
	guarded.HandleFunc("POST /api/upload/plan", s.handleUploadPlan)
	guarded.HandleFunc("PUT /api/upload", s.handleUpload)
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
	guarded.HandleFunc("GET /api/continue", s.handleContinue)
	// Favourites and playlists, per person. See favourites.go.
	guarded.HandleFunc("GET /api/favourites", s.handleFavourites)
	guarded.HandleFunc("PUT /api/favourites", s.handleAddFavourite)
	guarded.HandleFunc("DELETE /api/favourites", s.handleRemoveFavourite)
	guarded.HandleFunc("GET /api/playlists", s.handlePlaylists)
	guarded.HandleFunc("POST /api/playlists", s.handleCreatePlaylist)
	guarded.HandleFunc("GET /api/playlists/{id}", s.handlePlaylist)
	guarded.HandleFunc("PATCH /api/playlists/{id}", s.handleRenamePlaylist)
	guarded.HandleFunc("DELETE /api/playlists/{id}", s.handleDeletePlaylist)
	guarded.HandleFunc("POST /api/playlists/{id}/items", s.handleAddToPlaylist)
	guarded.HandleFunc("DELETE /api/playlists/{id}/items/{position}", s.handleRemoveFromPlaylist)
	guarded.HandleFunc("POST /api/playlists/{id}/move", s.handleMoveInPlaylist)
	guarded.HandleFunc("PUT /api/book/progress", s.handlePutProgress)
	// Account management is the one thing the owner can do and a member
	// cannot, so it gets its own guard rather than a check inside each handler.
	owner := http.NewServeMux()
	owner.HandleFunc("GET /api/users", s.handleListUsers)
	owner.HandleFunc("POST /api/users", s.handleCreateUser)
	owner.HandleFunc("DELETE /api/users/{id}", s.handleDeleteUser)
	owner.HandleFunc("POST /api/users/{id}/password", s.handleSetUserPassword)
	owner.HandleFunc("PUT /api/users/{id}/libraries", s.handleSetUserLibraries)
	owner.HandleFunc("PUT /api/remote", s.handleSetRemote)
	owner.HandleFunc("POST /api/delete/preview", s.handleDeletePreview)
	owner.HandleFunc("POST /api/delete", s.handleDelete)
	owner.HandleFunc("POST /api/delete/undo", s.handleDeleteUndo)
	guarded.Handle("/api/users", s.auth.RequireOwner(owner))
	guarded.Handle("/api/users/", s.auth.RequireOwner(owner))
	// Turning remote access on or off is an owner decision too - it exposes the
	// whole server - so it mounts the same owner guard.
	guarded.Handle("/api/remote", s.auth.RequireOwner(owner))
	// Deleting is the owner's alone: every other account shares these shelves
	// with the rest of the house. See delete.go.
	guarded.Handle("/api/delete", s.auth.RequireOwner(owner))
	guarded.Handle("/api/delete/", s.auth.RequireOwner(owner))

	mux.Handle("/api/", s.auth.Require(s.withUserContext(guarded)))

	// Every route, signed in or not, gets the body deadline and the
	// browser-facing checks.
	return s.withLogging(s.secureHeaders(sameOrigin(bodyDeadline(mux))))
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
	if name := s.currentPublicName(); name != "" {
		// Any port: the published one lives in compose, and only the browser
		// knows it.
		webui.ServeShell(w, r, "https://"+name+":*")
		return
	}
	webui.ServeShell(w, r)
}

// handleCA hands over the local authority so a device can trust it.
//
// Downloaded and installed once per device, after which every certificate
// SoundStorm issues is trusted - including ones minted later for an address it
// had never seen. That is the difference between a local authority and a bare
// self-signed certificate, and the reason for the chore being a one-off.
// handleRemoteReachable answers the name service's reachability challenge, so
// it can confirm the open port reaches this install before pointing a public
// name here. See Config.RemoteReachability.
func (s *Server) handleRemoteReachable(w http.ResponseWriter, r *http.Request) {
	nonce := r.URL.Query().Get("nonce")
	if nonce == "" {
		writeError(w, http.StatusBadRequest, "nonce is required")
		return
	}
	if s.remoteReach == nil {
		http.Error(w, "remote access is not configured", http.StatusNotFound)
		return
	}
	answer, ok := s.remoteReach(nonce)
	if !ok {
		http.Error(w, "remote access is not configured", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write([]byte(answer))
}

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
	if !s.auth.HasAccount() {
		answer["setupCodeRequired"] = true
	}
	if user, ok := s.auth.UserFor(r); ok {
		answer["signedIn"] = true
		answer["user"] = publicUser(user)
		// Remote-access state, for the account panel: whether it can be offered
		// at all, whether it is on, and the address to reach the server by from
		// away once it is up. Only to a signed-in account - it is config, not
		// something an anonymous visitor needs.
		if s.remoteStatus != nil {
			answer["remote"] = remoteJSON(s.remoteStatus())
		}
	}
	// The install's real https address, offered to a page that is not already
	// on it. The page checks it can reach it before going there, because a
	// router refusing to resolve a name pointing at a home address is common
	// enough that the server cannot assume it works.
	if name := s.currentPublicName(); name != "" && !strings.EqualFold(requestHostname(r), name) {
		answer["secureName"] = name
	}
	writeJSON(w, http.StatusOK, answer)
}

// handleSetRemote turns remote access on or off. Owner-only (mounted behind the
// owner guard): it puts the whole server on, or off, the internet.
func (s *Server) handleSetRemote(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxCredentialBody))
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "expected a JSON body with enabled")
		return
	}
	if s.remoteStatus == nil || s.setRemoteAccess == nil {
		writeError(w, http.StatusNotImplemented, "remote access is not available")
		return
	}
	if !s.remoteStatus().Available {
		writeError(w, http.StatusPreconditionFailed, "remote access needs auto HTTPS to be on")
		return
	}
	if err := s.setRemoteAccess(body.Enabled); err != nil {
		s.log.Error("set remote access", "err", err)
		writeError(w, http.StatusInternalServerError, "could not change remote access")
		return
	}
	user, _ := auth.FromContext(r.Context())
	s.log.Info("remote access changed", "enabled", body.Enabled, "by", user.Name)
	writeJSON(w, http.StatusOK, remoteJSON(s.remoteStatus()))
}

// remoteJSON is the wire shape of remote-access state, shared by the session and
// the toggle response so the account panel reads one thing. Reachable is derived
// (a name is only set once the install actually answers there), so the UI does
// not have to infer it.
func remoteJSON(st RemoteState) map[string]any {
	remote := map[string]any{
		"available": st.Available,
		"enabled":   st.Enabled,
		"reachable": st.Name != "",
		"port":      st.Port,
		"mapped":    st.Mapped,
	}
	if st.Name != "" {
		remote["name"] = st.Name
	}
	if st.Method != "" {
		remote["method"] = st.Method
	}
	if st.Upstream != "" {
		remote["upstream"] = st.Upstream
	}
	return remote
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
	Username  string `json:"username"`
	Password  string `json:"password"`
	SetupCode string `json:"setupCode"`
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
	// created yet.
	//
	// Until then it needs the setup code, because until then whoever reaches
	// the port first owns the server - and once it faces the internet, that
	// may not be whoever installed it. "Only from the home network" was the
	// first answer and cannot be checked: under Docker Desktop every
	// connection, local or forwarded from the internet, arrives from Docker's
	// own 172.20.0.1. Measured, not assumed. The installer puts the code in
	// the address it opens, so the person installing never sees it.
	if s.auth.HasAccount() {
		writeError(w, http.StatusConflict, "an account already exists; sign in instead")
		return
	}
	creds, err := decodeCredentials(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	given := NormalizeSetupCode(creds.SetupCode)
	if s.setupCode == "" || subtle.ConstantTimeCompare([]byte(given), []byte(s.setupCode)) != 1 {
		s.log.Warn("sign-up refused: wrong setup code", "remote", r.RemoteAddr)
		msg := "that setup code is not right"
		if given == "" {
			msg = "a setup code is needed to create the first account"
		}
		writeError(w, http.StatusForbidden, msg)
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
	// The browser that created the account is one the owner uses; mark it, as
	// a sign-in would.
	if err := s.auth.SetDeviceCookie(w, r, owner); err != nil {
		s.log.Warn("could not mark this device as trusted", "err", err)
	}
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
	token, expiry, user, err := s.auth.SignIn(r.Context(), clientOf(r), creds.Username, creds.Password,
		auth.DeviceTokens(r)...)
	if t, ok := auth.IsThrottled(err); ok {
		s.log.Warn("sign-in throttled", "remote", r.RemoteAddr)
		writeThrottled(w, t)
		return
	}
	if err != nil {
		s.log.Warn("failed sign-in", "remote", r.RemoteAddr)
		writeError(w, http.StatusUnauthorized, auth.ErrInvalidCredentials.Error())
		return
	}
	s.auth.SetCookie(w, r, token, expiry)
	// Mark this browser as one the account uses, so a stranger guessing at its
	// name cannot hold its next sign-in in backoff. Best effort: without it the
	// sign-in still worked, it just has no protection from that next time.
	if err := s.auth.SetDeviceCookie(w, r, user); err != nil {
		s.log.Warn("could not mark this device as trusted", "err", err)
	}
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
	// Their favourites and playlists go with them. Best effort, like the
	// backend accounts: a file that will not delete must not stop the removal.
	if s.collections != nil {
		if err := s.collections.Forget(id); err != nil {
			s.log.Warn("could not remove a person's favourites and playlists", "err", err)
		}
	}

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
		// Raw, so that an absent key can be told apart from an explicit null.
		// A *[]string cannot: JSON decodes both to a nil pointer, which made a
		// body of {} - no libraries named at all - lift every restriction on the
		// account. This is the one field that must never fail open.
		Libraries json.RawMessage `json:"libraries"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxCredentialBody))
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "expected a JSON body with a list of libraries")
		return
	}
	if len(body.Libraries) == 0 {
		writeError(w, http.StatusBadRequest, "libraries is required: a list, or null for every library")
		return
	}

	// null is "every library"; a list, including an empty one, is exactly those.
	var libraries []string
	if string(bytes.TrimSpace(body.Libraries)) != "null" {
		if err := json.Unmarshal(body.Libraries, &libraries); err != nil {
			writeError(w, http.StatusBadRequest, "libraries must be a list of library names, or null")
			return
		}
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
	var body struct {
		Password string `json:"password"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxCredentialBody))
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "expected a JSON body with a password")
		return
	}
	id := r.PathValue("id")
	if err := s.auth.ResetPassword(actor, id, body.Password, s.auth.SessionToken(r)); err != nil {
		writeError(w, statusFor(err), err.Error())
		return
	}
	s.log.Info("password reset", "id", id, "by", actor.Name)
	writeJSON(w, http.StatusOK, map[string]any{"changed": true})
}

// handleChangeOwnPassword lets anybody change their own, which is the only
// account operation a member can perform. It needs the current password, and
// signs out every other device.
func (s *Server) handleChangeOwnPassword(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var body struct {
		Current  string `json:"current"`
		Password string `json:"password"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxCredentialBody))
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "expected a JSON body with a password")
		return
	}
	err := s.auth.ChangeOwnPassword(r.Context(), clientOf(r), actor,
		body.Current, body.Password, s.auth.SessionToken(r))
	if t, ok := auth.IsThrottled(err); ok {
		writeThrottled(w, t)
		return
	}
	if errors.Is(err, auth.ErrWrongCurrentPassword) {
		s.log.Warn("wrong current password on change", "id", actor.ID, "remote", r.RemoteAddr)
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	if err != nil {
		writeError(w, statusFor(err), err.Error())
		return
	}
	s.log.Info("password changed", "id", actor.ID)
	// The new password retired every device token issued under the old one,
	// this browser's included; give this one a fresh token, since it is the
	// device that just proved it knows both.
	if updated, ok := s.store.User(actor.ID); ok {
		if err := s.auth.SetDeviceCookie(w, r, updated); err != nil {
			s.log.Warn("could not mark this device as trusted", "err", err)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"changed": true})
}

// clientOf names who is asking, for the sign-in throttle. The address without
// its port: a port is chosen fresh for every connection.
func clientOf(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// writeThrottled refuses a request the throttle stopped, saying when to retry.
func writeThrottled(w http.ResponseWriter, t *auth.ThrottledError) {
	secs := int(t.RetryAfter.Round(time.Second) / time.Second)
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	writeError(w, http.StatusTooManyRequests, t.Error())
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

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	statuses := s.setup.Statuses()
	// Why a backend failed is the owner's to fix and quotes upstream
	// responses; a member is told only that it is not ready.
	if !user.IsOwner() {
		for i := range statuses {
			statuses[i].Error = ""
		}
	}
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
		"empty": empty,
		// What to tell everyone else in the house to type. Behind the session
		// guard like the rest of this endpoint, and omitted rather than
		// guessed when nothing here knows it.
		"shareURL": s.shareURL(r),
		// The root as the user sees it, so the UI can name the one folder
		// everything lives under without stitching it back together from the
		// five paths below.
		"root":    s.library.Hint(),
		"folders": out,
	})
}

// shareURL is the address to hand somebody else on this network, or "" when
// there is nothing honest to say.
//
// Three parts from three places, because not one of them knows all of it:
//
//   - The host comes from SOUNDSTORM_TLS_HOSTS, written by the installer,
//     which ran on the host and could see its LAN address. This process
//     cannot: inside Docker the only addresses visible are the container's.
//   - The port comes from the Host header of this very request. The published
//     port lives in compose's port mapping and is never passed into the
//     container, so the only thing that knows it is the browser that just
//     used it.
//   - The scheme comes from whether this request arrived over TLS, asked of
//     the same code that decides whether the session cookie is Secure.
//
// Empty rather than a guess. The project has already shipped one address that
// resolved on the machine under test and nowhere else, and a printed URL that
// does not work costs more than printing none.
func (s *Server) shareURL(r *http.Request) string {
	scheme := "http"
	if s.auth.OverTLS(r) {
		scheme = "https"
	}

	// Already on the real name: that address is proven to work from this
	// network, has a certificate every device trusts, and is the one worth
	// handing on.
	if name := s.currentPublicName(); name != "" && strings.EqualFold(requestHostname(r), name) {
		return scheme + "://" + r.Host
	}

	// r.Host may or may not carry a port. SplitHostPort errors when it does
	// not, which is the ordinary case behind a proxy on 443.
	reqHost, port, err := net.SplitHostPort(r.Host)
	if err != nil {
		reqHost, port = r.Host, ""
	}

	host := ""
	for _, candidate := range s.lanHosts {
		if candidate = strings.TrimSpace(candidate); candidate != "" {
			host = candidate
			break
		}
	}
	if host == "" {
		// Nothing configured. If this request did not arrive on loopback then
		// whatever the browser typed is reachable from at least one other
		// machine, which is better evidence than anything this process could
		// derive on its own.
		if reqHost == "" || isLoopbackHost(reqHost) {
			return ""
		}
		host = reqHost
	}

	if port == "" {
		return scheme + "://" + host
	}
	return scheme + "://" + net.JoinHostPort(host, port)
}

func (s *Server) currentPublicName() string {
	if s.publicName == nil {
		return ""
	}
	return s.publicName()
}

// requestHostname is the host a request was addressed to, without its port.
func requestHostname(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		return r.Host
	}
	return host
}

// isLoopbackHost covers the names and addresses that mean "this machine", and
// are therefore useless to anybody else.
func isLoopbackHost(host string) bool {
	host = strings.ToLower(strings.Trim(host, "[]"))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || ip.IsUnspecified()
	}
	return false
}

// handleRescan asks every library this account can see to look at its folder.
//
// Uploads already trigger this by themselves. The button exists for the other
// way media arrives - copied into the folder from a file manager, or from
// another machine over the network - which nothing here can know about, and
// which otherwise waits for the backend's own sweep.
func (s *Server) handleRescan(w http.ResponseWriter, r *http.Request) {
	access := source.AccessFrom(r.Context())

	asked := make([]string, 0, len(media.AllKinds()))
	for _, kind := range media.AllKinds() {
		if !access.Permits(kind) {
			continue
		}
		// Through the same debounce as an upload, which doubles as the rate
		// limit: leaning on the button pushes one scan back rather than
		// starting twenty.
		s.scheduleRescan(kind)
		asked = append(asked, string(kind))
	}

	user, _ := auth.FromContext(r.Context())
	s.log.Info("scan requested by hand", "by", user.Name, "libraries", asked)
	writeJSON(w, http.StatusOK, map[string]any{"libraries": asked})
}

// maxPlanBody caps a drop manifest. Five thousand paths of a hundred
// characters is a fifth of this, and past that the browser gave up first.
const maxPlanBody = 4 << 20

// handleUploadPlan says where a set of dropped files would go, and asks about
// the ones it will not guess at.
//
// Nothing is written. This exists so somebody dropping a folder finds out what
// SoundStorm made of it - which shelf, what it is skipping, and what it needs
// told - before any bytes move.
func (s *Server) handleUploadPlan(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Paths []string `json:"paths"`
		// Choices answer questions a previous plan asked, keyed by group.
		Choices map[string]string `json:"choices"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxPlanBody))
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "expected a JSON body with a list of paths")
		return
	}
	if len(body.Paths) == 0 {
		writeError(w, http.StatusBadRequest, "no files were dropped")
		return
	}

	access := source.AccessFrom(r.Context())

	choices := make(map[string]media.Kind, len(body.Choices))
	for group, name := range body.Choices {
		kind, ok := media.ParseKind(name)
		if !ok {
			writeError(w, http.StatusBadRequest, "there is no library called "+strconv.Quote(name))
			return
		}
		if !access.Permits(kind) {
			writeError(w, http.StatusForbidden, "you do not have the "+name+" library")
			return
		}
		choices[group] = kind
	}

	placements, questions := s.library.Plan(body.Paths, choices)

	// A shelf somebody may not see is not a shelf they may add to, and the
	// automatic sorter has to be told so too - otherwise dropping a film on
	// the window would be a way around a restriction.
	accepted, waiting := 0, 0
	for i, p := range placements {
		switch {
		case p.Skipped:
		case p.Waiting:
			waiting++
		case !access.Permits(p.Kind):
			placements[i] = library.Placement{
				Path:    p.Path,
				Group:   p.Group,
				Skipped: true,
				Reason:  "you do not have the " + string(p.Kind) + " library",
			}
		default:
			accepted++
		}
	}

	// And a question must not offer a shelf they cannot use. If that leaves
	// one option it stops being a question and becomes the answer.
	questions = narrowQuestions(questions, access)

	// How much room there is, so the browser can compare it with what it is
	// about to send. This is the only part of the plan that is not about
	// placement, and it earns its place: ninety-one audiobooks failed one at a
	// time against a full disk, each with its own request, and nothing knew
	// until the first write failed. The plan is given paths and not sizes, so
	// the client owns the comparison - it is the only side that knows both.
	//
	// Omitted when it cannot be measured rather than sent as zero, which would
	// read as "no room at all" and stop every upload on a native Windows build.
	resp := map[string]any{
		"files":     placements,
		"questions": questions,
		"accepted":  accepted,
		"waiting":   waiting,
	}
	if free, ok := s.library.FreeSpace(); ok {
		resp["freeBytes"] = free
	}
	writeJSON(w, http.StatusOK, resp)
}

// narrowQuestions drops options an account may not use, and drops the question
// entirely when nothing is left to choose between.
func narrowQuestions(questions []library.Question, access source.Access) []library.Question {
	out := make([]library.Question, 0, len(questions))
	for _, q := range questions {
		var options []media.Kind
		for _, o := range q.Options {
			if access.Permits(o) {
				options = append(options, o)
			}
		}
		if len(options) < 2 {
			// One option is not a choice, and none means the files will be
			// refused anyway - either way there is nothing to ask.
			continue
		}
		q.Options = options
		out = append(out, q)
	}
	return out
}

// handleUpload receives one file.
//
// One request per file, with the body being the file and nothing else. No
// multipart: the destination is already known from the plan, so there is
// nothing else to carry, and a plain body streams to disk without a parser in
// between. It also gives the browser per-file progress for free.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}

	kind, err := s.uploadKind(r, r.URL.Query().Get("kind"))
	if err != nil {
		writeError(w, statusForUpload(err), err.Error())
		return
	}
	if kind == "" {
		writeError(w, http.StatusBadRequest, "kind is required")
		return
	}

	// Refused before a byte moves when the size is known and would leave the
	// disk under its reserve; Save checks again as the bytes arrive.
	if !s.library.Room(r.ContentLength) {
		writeError(w, http.StatusInsufficientStorage, "the library disk is nearly full")
		return
	}
	user, _ := auth.FromContext(r.Context())
	release, ok := s.takeUploadSlot(user.ID)
	if !ok {
		writeError(w, http.StatusTooManyRequests, "too many uploads at once; wait for one to finish")
		return
	}
	defer release()

	// A rolling deadline rather than a fixed one: a film on a slow uplink can
	// take an hour, and that is fine as long as it keeps moving.
	body := &stallReader{r: r.Body, rc: http.NewResponseController(w)}
	defer func() { _ = body.rc.SetReadDeadline(time.Time{}) }()

	dest, err := s.library.Save(kind, path, body)
	if err != nil {
		if errors.Is(err, library.ErrDiskReserve) {
			writeError(w, http.StatusInsufficientStorage, err.Error())
			return
		}
		if errors.Is(err, library.ErrAlreadyThere) || library.IsDuplicate(err) {
			// Not an error worth a stack trace in the log: re-dropping an
			// album somebody already added is an ordinary thing to do, and so
			// is importing a library that bought one song twice. A 409 is a
			// skip, not a failure, and the client shows it as one.
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": err.Error(),
				"path":  path,
			})
			return
		}
		s.log.Warn("upload failed", "path", path, "kind", kind, "err", err)
		writeError(w, http.StatusBadRequest, desensitizeFSError(err))
		return
	}

	s.log.Info("file added to the library", "dest", dest, "by", user.Name)

	// Ask whoever indexes that shelf to look, rather than leaving the file
	// sitting there unsearchable until their next sweep.
	s.scheduleRescan(kind)

	writeJSON(w, http.StatusOK, map[string]any{"dest": dest})
}

// rescanDelay is how long to wait for more files before asking a backend to
// scan.
//
// A dropped folder arrives as one upload per file, so triggering on each would
// ask Navidrome to scan thirty times for one album. Waiting a moment and
// coalescing turns that into one scan, and two seconds is far below the
// minute somebody would otherwise be waiting.
const rescanDelay = 2 * time.Second

// minRescanIntervalDefault is the least time between two real scans of one
// kind, whatever asked for them.
//
// The debounce above coalesces one burst into one scan, but does nothing
// against a stream of separate triggers spaced further apart than
// rescanDelay: the manual "check for new files" button is one call for any
// signed-in member, not just the owner, and each real scan is Jellyfin
// refreshing every library or Navidrome walking the music folder. A script
// calling it every three seconds would otherwise get a real scan every three
// seconds, forever. Half a minute keeps an occasional click instant while
// capping that to two scans a minute - well under what the backends' own
// timers already cost by themselves.
const minRescanIntervalDefault = 30 * time.Second

// minRescanInterval is a variable only so a test can shrink it rather than
// waiting out the real thing.
var minRescanInterval = minRescanIntervalDefault

// scheduleRescan asks the backends that own a kind to look at their folder,
// shortly, once - and no sooner than minRescanInterval after the last time
// one actually ran, however many requests ask for it in between.
//
// Every backend indexes on a timer - Navidrome every minute, the ebook scanner
// every two - so without this a file is on disk and unsearchable for up to two
// minutes after somebody watched it upload. Each of them has a "scan now"
// call; this is simply using it.
func (s *Server) scheduleRescan(kind media.Kind) {
	s.rescanMu.Lock()
	defer s.rescanMu.Unlock()

	// Never shorter than rescanDelay, and never sooner than minRescanInterval
	// after the last scan actually started. Recomputed on every call, not
	// just the first: a later trigger with a tighter floor (the previous scan
	// finished starting in the meantime) must not let an in-flight reset
	// shrink the wait back below it.
	delay := rescanDelay
	if last, ok := s.lastRescan[kind]; ok {
		if floor := minRescanInterval - time.Since(last); floor > delay {
			delay = floor
		}
	}

	if timer, ok := s.rescanTimers[kind]; ok {
		// Still waiting: push the moment back rather than adding a second one,
		// so a long upload results in one scan after the last file.
		timer.Reset(delay)
		return
	}
	s.rescanTimers[kind] = time.AfterFunc(delay, func() {
		s.rescanMu.Lock()
		delete(s.rescanTimers, kind)
		s.lastRescan[kind] = time.Now()
		s.rescanMu.Unlock()
		s.rescanNow(kind)
	})
}

// rescanNow tells every source of a kind to scan. Best effort: a backend that
// will not scan is not a reason to have failed an upload that already worked,
// and its own timer will find the file anyway.
func (s *Server) rescanNow(kind media.Kind) {
	// A fresh context: the request that triggered this finished long ago.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Before asking, not after. A backend that finds its folder completely
	// empty declines to remove anything from it - it cannot tell a deletion
	// from an unmounted disk - so a scan triggered a moment after somebody
	// emptied a folder by hand would report success and change nothing, and
	// what they deleted would stay searchable indefinitely. Putting the
	// placeholder back first is what makes the scan able to see the deletion.
	s.library.EnsurePlaceholders()

	for _, src := range s.reg.All(ctx) {
		if src.Kind() != kind {
			continue
		}
		rescanner, ok := src.(source.Rescanner)
		if !ok {
			continue
		}
		if err := rescanner.Rescan(ctx); err != nil {
			s.log.Warn("could not ask for a scan", "source", src.ID(), "err", err)
			continue
		}
		s.log.Info("asked for a scan", "source", src.ID())
	}
}

// errNoSuchLibrary and errNotYourLibrary separate "that is not a shelf" from
// "that is not your shelf", which are a 400 and a 403.
var (
	errNoSuchLibrary  = errors.New("there is no such library")
	errNotYourLibrary = errors.New("you do not have that library")
)

// uploadKind validates a requested shelf against what this account may see.
//
// An empty name is an error for an upload: by the time bytes are moving the
// plan has already said where they go.
func (s *Server) uploadKind(r *http.Request, name string) (media.Kind, error) {
	if strings.TrimSpace(name) == "" {
		return "", nil
	}
	kind, ok := media.ParseKind(name)
	if !ok {
		return "", errNoSuchLibrary
	}
	if !source.AccessFrom(r.Context()).Permits(kind) {
		return "", errNotYourLibrary
	}
	return kind, nil
}

func statusForUpload(err error) int {
	if errors.Is(err, errNotYourLibrary) {
		return http.StatusForbidden
	}
	return http.StatusBadRequest
}

// desensitizeFSError strips the container's own absolute path out of an
// upload failure before it reaches a browser.
//
// Most of what library.Save refuses for is already a message written for a
// person - "there is no music library", the file type check, a duplicate.
// What it is not written for is the rare case where the disk itself
// misbehaves: os.MkdirAll, os.CreateTemp and a failed rename all quote the
// full path they were given on error, and inside the container that path is
// /library/... or /var/lib/... - the internal layout, in a 400 body, to
// whoever's upload happened to trip over it. The original, path and all, is
// already in the log by the time this runs.
func desensitizeFSError(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return fmt.Sprintf("%s: %s", pe.Op, pe.Err)
	}
	var le *os.LinkError
	if errors.As(err, &le) {
		return fmt.Sprintf("%s: %s", le.Op, le.Err)
	}
	return err.Error()
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	// An empty q is a browse, not a mistake: it means "everything on this
	// shelf", which is what the UI shows the moment somebody picks a filter
	// and before they have typed anything. Each adapter turns it into
	// whichever call its backend offers for listing rather than searching,
	// and federate.Relevance already scores every item 0 for an empty query -
	// so the merged list falls through to its title tiebreak and comes back
	// alphabetical, which is the right order for a list nobody asked a
	// question of.
	text := strings.TrimSpace(q.Get("q"))

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

	// Where this page starts in the merged list. Paging is global rather than
	// per-source, because a merged order cannot survive being assembled from
	// per-source pages - see federate.Search.
	if raw := q.Get("offset"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 || n > federate.MaxDepth {
			writeError(w, http.StatusBadRequest,
				"offset must be between 0 and "+strconv.Itoa(federate.MaxDepth))
			return
		}
		query.Offset = n
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
	for i, st := range result.Sources {
		if st.Error != "" {
			s.log.Warn("source failed during search", "source", st.SourceID, "err", st.Error)
			result.Sources[i].Error = publicSourceError(st.Error)
		}
	}
	writeJSON(w, http.StatusOK, result)
}

// publicSourceError is what a failed source is reported as to the browser.
// The real error names upstream addresses and quotes upstream bodies - and
// once, before httpx redacted them, a Subsonic URL with its credential in the
// query string - which is detail for the log, not for every member's screen.
func publicSourceError(detail string) string {
	if strings.Contains(detail, "deadline exceeded") || strings.Contains(detail, "Timeout") {
		return "took too long to answer"
	}
	return "did not answer"
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

// isFraction reports whether f is a real number between 0 and 1, which is
// the only thing "how much of this book has been read" can mean. Rejects the
// same class of bad input isTime does - 1e400 decodes to +Inf without error,
// and a JSON body could as easily hand back a negative number or ten.
func isFraction(f float64) bool {
	return !math.IsNaN(f) && !math.IsInf(f, 0) && f >= 0 && f <= 1
}

// maxLocationLength bounds an EPUB CFI. A real one is a few dozen characters;
// this is generous for a nested, multi-range one and still small next to
// maxProgressBody - and small enough that a book opened once a minute for a
// year would add a few hundred KB to state.json, not the whole quota each time.
const maxLocationLength = 2 << 10

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

	if contentType == "" {
		contentType = "application/octet-stream"
	}
	// A book's own content can name an absolute URL back at this endpoint -
	// <script src="/api/book/resource?...path=x.js"> - and the reader renders
	// chapters in a same-origin iframe, so such a script would run as us with
	// the reader's session. foliate rewrites *relative* refs to blob: URLs the
	// shell CSP already blocks, but leaves absolute ones alone, and an
	// absolute ref to our own origin counts as script-src 'self'. The response
	// CSP below does nothing about that: it governs this resource opened as a
	// document, not this resource pulled in as a subresource by something else.
	//
	// Two guards close it. The reader only ever reaches this through fetch(),
	// whose Sec-Fetch-Dest is "empty"; a <script>/<img>/<iframe> is not, so
	// refuse anything that is not a plain fetch (absent header = an old
	// browser or a non-browser client, allowed so the reader keeps working).
	// And for a client that sends no Sec-Fetch-Dest at all, never hand back a
	// runnable script content-type: with nosniff, a non-JS type cannot be
	// executed as one, and the reader reads bytes rather than <script>-loading
	// anything, so neutralising it costs nothing.
	if dest := r.Header.Get("Sec-Fetch-Dest"); dest != "" && dest != "empty" {
		writeError(w, http.StatusForbidden, "book resources are for the reader, not direct loading")
		return
	}
	if stream.IsScriptType(contentType) {
		contentType = "text/plain; charset=utf-8"
	}
	w.Header().Set("Content-Type", contentType)
	// Belt-and-suspenders for a client that opens this URL as a document.
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
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
	sourceID, itemID, ok := s.progressTarget(w, r)
	if !ok {
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
	sourceID, itemID, ok := s.progressTarget(w, r)
	if !ok {
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
	// Unlike handleSetPosition's isTime, nothing here checked at all: a
	// fraction is a JSON number like any other, so 1e400 silently decodes to
	// +Inf (the same trap noted for Audiobookshelf's own API) and would sit in
	// state.json forever, read back into a progress bar computing width from
	// it. And an EPUB CFI is normally a few dozen characters, so a location
	// with no length check is an easy way to grow that file - which every
	// login and session change reads and rewrites whole - one book at a time.
	if !isFraction(body.Fraction) {
		writeError(w, http.StatusBadRequest, "fraction must be a number between 0 and 1")
		return
	}
	if len(body.Location) > maxLocationLength {
		writeError(w, http.StatusBadRequest, "location is too long")
		return
	}

	err := s.store.SetProgress(progressKey(user.ID, sourceID, itemID), state.Progress{
		Location:  body.Location,
		Fraction:  body.Fraction,
		UpdatedAt: time.Now().UTC(),
	}, user.ID+"/", maxProgressPerUser)
	if err != nil {
		writeError(w, http.StatusInsufficientStorage, "too many saved positions for this account")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"saved": true})
}

// maxProgressItemID bounds a book id in a progress key. A localbooks id is a
// relative path, already far shorter than this; the endpoint takes it as a
// free query parameter, so it is bounded here rather than trusted.
const maxProgressItemID = 1024

// maxProgressPerUser caps how many reading positions one account may hold.
// state.json is rewritten whole on every save, so an unbounded number of
// made-up ids from one member is a way to bloat it and slow every write; no
// real reader has ten thousand books open.
const maxProgressPerUser = 10_000

// progressTarget resolves and authorises the source/id pair both progress
// endpoints take. reg.ByID is what applies the account's library
// restriction, exactly as on every other guarded route, and also confirms the
// source is real - so a member cannot store or read a position against a
// source they cannot see, or against a free-form string that is no source at
// all.
func (s *Server) progressTarget(w http.ResponseWriter, r *http.Request) (sourceID, itemID string, ok bool) {
	sourceID = r.URL.Query().Get("source")
	itemID = r.URL.Query().Get("id")
	if sourceID == "" || itemID == "" {
		writeError(w, http.StatusBadRequest, "source and id are required")
		return "", "", false
	}
	if len(itemID) > maxProgressItemID {
		writeError(w, http.StatusBadRequest, "id is too long")
		return "", "", false
	}
	src, found := s.reg.ByID(r.Context(), sourceID)
	if !found {
		writeError(w, http.StatusNotFound, "unknown source "+strconv.Quote(sourceID))
		return "", "", false
	}
	// Only a real book can have a place kept in it. Without this the id was a
	// free string, and every made-up one was a new entry in state.json - the file
	// every login and every save rewrites whole, under the lock every request
	// takes - so one member could slow the server for everybody. A source that
	// cannot say which books it has (none today but the book shelves read here)
	// keeps no reading positions.
	//
	// A film or an episode can have one too - how far into it somebody
	// watched, kept here rather than in Jellyfin, because SoundStorm shares one
	// Jellyfin account across the house and a position there would be
	// everybody's at once. Jellyfin says whether an id is one of its items.
	known := false
	if books, ok := src.(interface{ HasBook(string) bool }); ok {
		known = books.HasBook(itemID)
	} else if videos, ok := src.(interface {
		HasItem(context.Context, string) bool
	}); ok {
		known = videos.HasItem(r.Context(), itemID)
	}
	if !known {
		writeError(w, http.StatusNotFound, "no such item")
		return "", "", false
	}
	return sourceID, itemID, true
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

// Unwrap lets http.ResponseController reach the real connection through the
// recorder. Without it SetReadDeadline answers ErrNotSupported - and every read
// deadline set behind this middleware (bodyDeadline's thirty seconds, an
// upload's rolling window) silently did nothing, because each caller discards
// that error. Their unit tests built the handler without this middleware and so
// never saw it; a test through the real route chain did.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

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

// NormalizeSetupCode makes a typed code compare equal to the printed one:
// case, spaces and dashes are presentation.
func NormalizeSetupCode(code string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(code) {
		if r == '-' || r == ' ' || r == '	' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// bodyDeadline stops a client holding a connection open by sending its
// request body a byte at a time. Every body here but an upload is a few
// hundred bytes of JSON, so half a minute is generous; an upload is given a
// rolling deadline by handleUpload instead. Requests without a body - which
// includes every stream - are left alone, because a read deadline still
// pending while a film is written out would cancel it.
func bodyDeadline(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && r.Body != http.NoBody && r.URL.Path != "/api/upload" {
			rc := http.NewResponseController(w)
			_ = rc.SetReadDeadline(time.Now().Add(bodyTimeout))
			defer func() { _ = rc.SetReadDeadline(time.Time{}) }()
		}
		next.ServeHTTP(w, r)
	})
}

// bodyTimeout and uploadStall are variables only so a test can wait for them.
var (
	bodyTimeout = bodyTimeoutDefault
	uploadStall = uploadStallDefault
)

const (
	bodyTimeoutDefault = 30 * time.Second
	// uploadStallDefault is the window an upload must make progress in, and
	// uploadMinProgress is how much counts as progress. A slow connection
	// moving a big film is fine; one that has stopped, or is sending a byte at
	// a time to keep the connection open, is not.
	uploadStallDefault = time.Minute
	uploadMinProgress  = 16 << 10
	// maxUploadsPerUser caps how many uploads one account has in flight. The
	// app sends one file at a time, so this leaves room for a few tabs or
	// devices at once and none for opening connections by the hundred.
	maxUploadsPerUser = 4
)

// stallReader gives an upload a rolling read deadline that only moves forward
// when the upload is actually moving.
//
// It used to push the deadline back on every read, however little arrived - so
// one byte every fifty-nine seconds held an upload open for ever, and with it a
// connection, a goroutine, a staging file and a file descriptor. Now the
// deadline moves only once uploadMinProgress bytes have arrived since it last
// did: sixteen kilobytes a minute, which a bad mobile link manages easily and a
// trickle never does.
type stallReader struct {
	r        io.Reader
	rc       *http.ResponseController
	started  bool
	progress int
}

func (s *stallReader) Read(p []byte) (int, error) {
	if !s.started {
		s.started = true
		_ = s.rc.SetReadDeadline(time.Now().Add(uploadStall))
	}
	n, err := s.r.Read(p)
	if s.progress += n; s.progress >= uploadMinProgress {
		s.progress = 0
		_ = s.rc.SetReadDeadline(time.Now().Add(uploadStall))
	}
	return n, err
}

// takeUploadSlot reserves one of an account's in-flight uploads, reporting
// false when it already has maxUploadsPerUser. The returned func gives it back.
func (s *Server) takeUploadSlot(userID string) (func(), bool) {
	s.uploadsMu.Lock()
	defer s.uploadsMu.Unlock()
	if s.uploads == nil {
		s.uploads = map[string]int{}
	}
	if s.uploads[userID] >= maxUploadsPerUser {
		return nil, false
	}
	s.uploads[userID]++
	return func() {
		s.uploadsMu.Lock()
		defer s.uploadsMu.Unlock()
		if s.uploads[userID]--; s.uploads[userID] <= 0 {
			delete(s.uploads, userID)
		}
	}, true
}

// sameOrigin refuses a state-changing request that another site's page sent.
//
// The session cookie is SameSite=Lax, and that is not enough on its own:
// every install's name is under soundstorm.dev, which until the zone is on
// the Public Suffix List makes every other install the same *site* - so a
// page on somebody else's name could post here with this household's cookie.
// Sec-Fetch-Site says what the browser saw, and every current browser sends
// it; Origin is the fallback for one that does not. A request with neither
// is not from a browser page at all - the installer, curl - and has no
// cookie a page could have borrowed.
func sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
			if site != "same-origin" && site != "none" {
				writeError(w, http.StatusForbidden, "cross-site request refused")
				return
			}
		} else if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || !strings.EqualFold(u.Host, r.Host) {
				writeError(w, http.StatusForbidden, "cross-site request refused")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// secureHeaders are the answers a browser needs on every response.
//
//   - Framing only by our own pages. A PDF opens in an iframe of ours, so
//     not DENY; anybody else's page framing the app to trick a click is out.
//   - HSTS on the real certificate's name only. That name has a certificate
//     every browser trusts, so promising https for it can only help; on an
//     IP address, localhost or a self-signed name it would be a promise the
//     next reinstall breaks.
//
// hstsMaxAgeSeconds is one week - see secureHeaders for why it is not a year.
const hstsMaxAgeSeconds = 7 * 24 * 60 * 60

func (s *Server) secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		// HSTS, but only on the real certificate's name and only while a valid
		// one is actually loaded - currentPublicName is empty otherwise, so it
		// is never promised over the local-authority fallback.
		//
		// A week, not the usual year. A home server's certificate can genuinely
		// lapse - the name service, Let's Encrypt or Railway being unreachable
		// through the whole renewal window - and once it does, the fallback is
		// the untrusted local authority, which a pinned browser refuses with no
		// way through. A year of that is a bricked install; a week self-heals,
		// and each visit while the certificate works pushes the week back out,
		// so a regularly-used server always carries the protection. No
		// includeSubDomains (a sibling install is a subdomain) and no preload
		// (that is the permanence this is avoiding).
		if r.TLS != nil {
			if name := s.currentPublicName(); name != "" && strings.EqualFold(requestHostname(r), name) {
				h.Set("Strict-Transport-Security", "max-age="+strconv.Itoa(hstsMaxAgeSeconds))
			}
		}
		next.ServeHTTP(w, r)
	})
}
