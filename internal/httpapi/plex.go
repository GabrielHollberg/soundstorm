package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/plex"
)

// Importing from Plex, for somebody whose playlists are in Plexamp: sign in
// on Plex's own page, pick playlists, and each comes across through the same
// matching as an M3U file. Per person, and only when asked: nothing talks to
// plex.tv otherwise. The Plex token lives in memory for the length of the
// import (plexSessionFor) and is never sent to the browser or written down.

// plexSessionFor is how long a Plex sign-in is kept after its last use.
const plexSessionFor = 30 * time.Minute

type plexSession struct {
	// mu is held by whichever request is using the session: a poll, a
	// listing or an import.
	mu      sync.Mutex
	pin     plex.Pin
	token   string
	servers []plex.Server
	conns   map[string]*plex.Conn
	used    time.Time
}

type plexSessions struct {
	mu   sync.Mutex
	byID map[string]*plexSession
}

// get is the account's session, dropping any that have gone stale.
func (p *plexSessions) get(userID string) *plexSession {
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, sess := range p.byID {
		if time.Since(sess.used) > plexSessionFor {
			delete(p.byID, id)
		}
	}
	sess := p.byID[userID]
	if sess != nil {
		sess.used = time.Now()
	}
	return sess
}

func (p *plexSessions) put(userID string, sess *plexSession) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.byID == nil {
		p.byID = map[string]*plexSession{}
	}
	sess.used = time.Now()
	p.byID[userID] = sess
}

func (p *plexSessions) drop(userID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.byID, userID)
}

// plexClient names this install to Plex the same way every time - Plex lists
// each app signed in to an account by that name, so a fresh one per restart
// would pile up there - without saying anything about the install: an HMAC
// of the state's own device key.
func (s *Server) plexClient() (*plex.Client, error) {
	if s.plexOverride != nil {
		return s.plexOverride, nil
	}
	key, err := s.store.DeviceKey()
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("plex client id"))
	return &plex.Client{ClientID: "soundstorm-" + hex.EncodeToString(mac.Sum(nil))[:24]}, nil
}

// hasMusic is whether this account can see a music shelf to import into.
func (s *Server) hasMusic(ctx context.Context) bool {
	for _, src := range s.reg.All(ctx) {
		if src.Kind() == media.KindMusic {
			return true
		}
	}
	return false
}

// handlePlexSignIn starts a sign-in and answers the page to open.
func (s *Server) handlePlexSignIn(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	if !s.hasMusic(r.Context()) {
		writeError(w, http.StatusForbidden, "There is no music library to import into.")
		return
	}
	if !s.plexPins.allow(user.ID, time.Now(), 5, time.Minute) {
		writeError(w, http.StatusTooManyRequests, "Too many tries; wait a minute.")
		return
	}
	c, err := s.plexClient()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not start the Plex sign-in.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	pin, err := c.NewPin(ctx)
	if err != nil {
		s.log.Warn("plex sign-in could not start", "err", err)
		writeError(w, http.StatusBadGateway, "Plex did not answer. Try again in a moment.")
		return
	}
	s.plex.put(user.ID, &plexSession{pin: pin, conns: map[string]*plex.Conn{}})
	// Once signed in, Plex sends that tab to a page saying to come back.
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	forward := scheme + "://" + r.Host + "/static/plex-done.html"
	writeJSON(w, http.StatusOK, map[string]string{"authUrl": c.AuthURL(pin, forward)})
}

// handlePlexStatus says whether the sign-in has finished, and lists the
// account's servers once it has.
func (s *Server) handlePlexStatus(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	sess := s.plex.get(user.ID)
	if sess == nil {
		writeJSON(w, http.StatusOK, map[string]any{"state": "none"})
		return
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	c, err := s.plexClient()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not ask Plex.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if sess.token == "" {
		token, err := c.Token(ctx, sess.pin)
		switch {
		case errors.Is(err, plex.ErrNotSignedIn):
			writeJSON(w, http.StatusOK, map[string]any{"state": "waiting"})
			return
		case errors.Is(err, plex.ErrPinExpired):
			s.plex.drop(user.ID)
			writeJSON(w, http.StatusOK, map[string]any{"state": "expired"})
			return
		case err != nil:
			s.log.Warn("plex sign-in check failed", "err", err)
			writeJSON(w, http.StatusOK, map[string]any{"state": "waiting"})
			return
		}
		sess.token = token
	}
	if sess.servers == nil {
		servers, err := c.Servers(ctx, sess.token)
		if err != nil {
			s.log.Warn("plex servers could not be listed", "err", err)
			writeError(w, http.StatusBadGateway, "Signed in, but Plex would not list your servers. Try again.")
			return
		}
		sess.servers = servers
	}
	list := []map[string]any{}
	for _, srv := range sess.servers {
		list = append(list, map[string]any{"id": srv.ID, "name": srv.Name, "owned": srv.Owned})
	}
	writeJSON(w, http.StatusOK, map[string]any{"state": "ready", "servers": list})
}

// plexConn is a connection to one of the session's servers, found once.
func (s *Server) plexConn(ctx context.Context, sess *plexSession, serverID string) (*plex.Conn, error) {
	if conn := sess.conns[serverID]; conn != nil {
		return conn, nil
	}
	c, err := s.plexClient()
	if err != nil {
		return nil, err
	}
	for _, srv := range sess.servers {
		if srv.ID == serverID {
			conn, err := c.Open(ctx, srv)
			if err != nil {
				return nil, err
			}
			sess.conns[serverID] = conn
			return conn, nil
		}
	}
	return nil, errors.New("no such Plex server")
}

// readySession is the account's signed-in session, locked - the caller
// unlocks it - or a written error.
func (s *Server) readySession(w http.ResponseWriter, userID string) (*plexSession, bool) {
	sess := s.plex.get(userID)
	if sess != nil {
		sess.mu.Lock()
		if sess.token != "" && sess.servers != nil {
			return sess, true
		}
		sess.mu.Unlock()
	}
	writeError(w, http.StatusConflict, "Sign in to Plex first.")
	return nil, false
}

// handlePlexPlaylists lists a server's audio playlists.
func (s *Server) handlePlexPlaylists(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	sess, ok := s.readySession(w, user.ID)
	if !ok {
		return
	}
	defer sess.mu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	conn, err := s.plexConn(ctx, sess, r.URL.Query().Get("server"))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	lists, err := conn.Playlists(ctx)
	if err != nil {
		s.log.Warn("plex playlists could not be listed", "err", err)
		writeError(w, http.StatusBadGateway, "That Plex server would not list its playlists.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"playlists": lists})
}

// maxPlexImport is how many playlists one request may bring across.
const maxPlexImport = 50

// handlePlexImport brings the chosen playlists across.
func (s *Server) handlePlexImport(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	if !s.imports.allow(user.ID, time.Now(), 10, 30*time.Second) {
		writeError(w, http.StatusTooManyRequests, "Too many imports at once; wait a moment.")
		return
	}
	var body struct {
		Server    string `json:"server"`
		Playlists []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"playlists"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil || len(body.Playlists) == 0 {
		writeError(w, http.StatusBadRequest, "expected a server and the playlists to import")
		return
	}
	if len(body.Playlists) > maxPlexImport {
		writeError(w, http.StatusBadRequest, "Import up to 50 playlists at a time.")
		return
	}
	sess, ok := s.readySession(w, user.ID)
	if !ok {
		return
	}
	defer sess.mu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	conn, err := s.plexConn(ctx, sess, body.Server)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	ix := s.musicIndex(r)
	if ix == nil {
		writeError(w, http.StatusServiceUnavailable, "The music library is not available right now.")
		return
	}
	results := []importResult{}
	for _, pl := range body.Playlists {
		tracks, err := conn.Tracks(ctx, pl.ID)
		if err != nil {
			s.log.Warn("plex playlist could not be read", "err", err)
			results = append(results, importResult{Name: pl.Title, Missing: []string{}, Error: "Plex would not give its songs."})
			continue
		}
		entries := make([]m3uEntry, 0, len(tracks))
		for _, t := range tracks {
			e := m3uEntry{Location: t.File, Artist: t.Artist, Title: t.Title, Seconds: t.Seconds}
			if t.AlbumArtist != t.Artist {
				e.AltArtist = t.AlbumArtist
			}
			entries = append(entries, e)
		}
		results = append(results, s.importEntries(user.ID, pl.Title, entries, ix))
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// handlePlexForget drops the Plex sign-in now rather than when it times out.
func (s *Server) handlePlexForget(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	s.plex.drop(user.ID)
	writeJSON(w, http.StatusOK, map[string]bool{"forgotten": true})
}
