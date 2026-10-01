package httpapi

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/auth"
	"github.com/GabrielHollberg/soundstorm/internal/state"
)

// New devices need approval (an owner setting, off unless turned on).
//
// The sign-in throttle holds a guesser to about one try a minute per account
// from any number of addresses, but a guess that is right still gets in. With
// this on, the right password is not enough on a device the account has never
// signed in on: the sign-in waits until somebody approves it from a device
// already signed in to that account, or the owner approves it - so a password
// guessed through Tor, or leaked from another site, opens nothing.
//
// Whether a device is new is the device token every sign-in leaves (see
// auth.SetDeviceCookie), not where the request came from: behind Docker
// Desktop every connection looks the same, and a request's own say about its
// address can be faked. A password change retires every device token, so after
// one every other device asks again - which is what a password change is for.
//
// The owner signing in on a device of their own with nothing else signed in
// has nobody to ask. The setup code in .env (the one the first sign-up used)
// approves a device too: whoever can read the server's files owns it anyway.

// pendingFor is how long a sign-in waits for its approval.
const pendingFor = 10 * time.Minute

type pendingSignIn struct {
	ID       string
	UserID   string
	UserName string
	Device   string // what the browser said it was, for the person approving
	At       time.Time
	// No session until approved: one made at once counted towards the
	// account's 50 and pushed out real ones (a review).
	answer   int  // 0 waiting, 1 approved, -1 refused
	keep     bool // "keep me on this device", applied once it is in
}

type pendingSignIns struct {
	mu sync.Mutex
	m  map[string]*pendingSignIn
}

// add holds a sign-in that has the right password but a new device.
func (s *Server) holdSignIn(r *http.Request, user state.User, keep bool) *pendingSignIn {
	p := &s.pending
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.m == nil {
		p.m = map[string]*pendingSignIn{}
	}
	s.prunePendingLocked()
	// A handful at once per account, a couple of hundred in all: each holds a
	// session, and they are only ever made with the right password.
	mine := 0
	for _, q := range p.m {
		if q.UserID == user.ID {
			mine++
		}
	}
	if mine >= 5 || len(p.m) >= 200 {
		return nil
	}
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	q := &pendingSignIn{
		ID: hex.EncodeToString(raw), UserID: user.ID, UserName: user.Name,
		Device: deviceLabel(r.UserAgent()), At: time.Now(), keep: keep,
	}
	p.m[q.ID] = q
	return q
}

func (s *Server) prunePendingLocked() {
	for id, q := range s.pending.m {
		if time.Since(q.At) > pendingFor {
			delete(s.pending.m, id)
		}
	}
}

// GET /api/login/pending/{id}: the waiting device asks how it went. The id is
// 128 random bits, known only to the device that signed in.
// POST with {"setupCode"} approves it with the server's setup code.
func (s *Server) handlePendingSignIn(w http.ResponseWriter, r *http.Request) {
	p := &s.pending
	p.mu.Lock()
	s.prunePendingLocked()
	q, ok := p.m[r.PathValue("id")]
	if !ok {
		p.mu.Unlock()
		writeError(w, http.StatusNotFound, "that sign-in is no longer waiting; sign in again")
		return
	}
	if r.Method == http.MethodPost {
		var body struct {
			SetupCode string `json:"setupCode"`
		}
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body)
		given := NormalizeSetupCode(body.SetupCode)
		if s.approvalCode == "" || given == "" || subtle.ConstantTimeCompare([]byte(given), []byte(s.approvalCode)) != 1 {
			p.mu.Unlock()
			writeError(w, http.StatusForbidden, "that is not the setup code")
			return
		}
		q.answer = 1
		s.log.Info("a new device approved with the setup code", "for", q.UserName)
	}
	switch q.answer {
	case 0:
		p.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"waiting": true})
		return
	case -1:
		delete(p.m, q.ID)
		p.mu.Unlock()
		writeError(w, http.StatusForbidden, "this sign-in was refused")
		return
	}
	delete(p.m, q.ID)
	p.mu.Unlock()
	user, ok := s.store.User(q.UserID)
	if !ok {
		writeError(w, http.StatusNotFound, "that account is gone")
		return
	}
	token, expiry, err := s.auth.SessionFor(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not sign in")
		return
	}
	s.auth.SetCookie(w, r, token, expiry)
	if q.keep {
		s.keepOnDevice(w, r, user)
	}
	if err := s.auth.SetDeviceCookie(w, r, user); err != nil {
		s.log.Warn("could not mark this device as trusted", "err", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"signedIn": true, "user": publicUser(user)})
}

// GET /api/devices/pending: sign-ins waiting for this person's approval - their
// own account's, or everybody's for the owner.
func (s *Server) handleListPending(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	p := &s.pending
	p.mu.Lock()
	s.prunePendingLocked()
	out := []map[string]any{}
	for _, q := range p.m {
		if q.answer == 0 && (q.UserID == user.ID || user.IsOwner()) {
			out = append(out, map[string]any{"id": q.ID, "user": q.UserName, "device": q.Device, "at": q.At})
		}
	}
	p.mu.Unlock()
	sort.Slice(out, func(a, b int) bool { return out[a]["at"].(time.Time).Before(out[b]["at"].(time.Time)) })
	writeJSON(w, http.StatusOK, map[string]any{"pending": out})
}

// POST /api/devices/pending/{id} {"approve": bool}: allow or refuse one.
func (s *Server) handleAnswerPending(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var body struct {
		Approve bool `json:"approve"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, `expected {"approve": true or false}`)
		return
	}
	p := &s.pending
	p.mu.Lock()
	defer p.mu.Unlock()
	s.prunePendingLocked()
	q, found := p.m[r.PathValue("id")]
	if !found || (q.UserID != user.ID && !user.IsOwner()) {
		writeError(w, http.StatusNotFound, "no such sign-in waiting")
		return
	}
	if body.Approve {
		q.answer = 1
	} else {
		q.answer = -1
	}
	s.log.Info("a new device answered", "for", q.UserName, "by", user.Name, "approved", body.Approve)
	writeJSON(w, http.StatusOK, map[string]any{"approved": body.Approve})
}

// PUT /api/settings/new-devices {"enabled": bool}: owner only.
func (s *Server) handleSetApproveDevices(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "expected a JSON body with enabled")
		return
	}
	if err := s.store.SetApproveNewDevices(body.Enabled); err != nil {
		writeError(w, http.StatusInternalServerError, "could not save the setting")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": body.Enabled})
}

// needsApproval reports whether a sign-in with the right password must wait.
func (s *Server) needsApproval(r *http.Request, user state.User) bool {
	return s.store.ApproveNewDevices() && !s.auth.KnownDevice(user.Name, auth.DeviceTokens(r))
}

// deviceLabel is a plain name for a browser, from what it says it is.
func deviceLabel(ua string) string {
	app := "A browser"
	switch {
	case strings.Contains(ua, "SoundStormTV"):
		app = "The TV app"
	case strings.Contains(ua, "SoundStormApp"):
		app = "The SoundStorm app"
	case strings.Contains(ua, "Edg/"):
		app = "Edge"
	case strings.Contains(ua, "Firefox/"):
		app = "Firefox"
	case strings.Contains(ua, "Chrome/"):
		app = "Chrome"
	case strings.Contains(ua, "Safari/"):
		app = "Safari"
	}
	system := ""
	switch {
	case strings.Contains(ua, "iPhone"):
		system = "an iPhone"
	case strings.Contains(ua, "iPad"):
		system = "an iPad"
	case strings.Contains(ua, "Android"):
		system = "Android"
	case strings.Contains(ua, "Windows"):
		system = "Windows"
	case strings.Contains(ua, "Mac OS X"):
		system = "a Mac"
	case strings.Contains(ua, "Linux"):
		system = "Linux"
	}
	if system == "" {
		return app
	}
	return app + " on " + system
}

// POST /api/users/new-passwords: everybody, the owner included, must choose
// a new password before they can do anything else. Signed-in devices stay
// signed in and show the screen for it; choosing one signs that person's
// other devices out, as changing a password always does.
func (s *Server) handleRequireNewPasswords(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	n, err := s.store.RequirePasswordChanges()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not save it")
		return
	}
	s.log.Info("everyone asked to choose a new password", "by", actor.Name, "accounts", n)
	writeJSON(w, http.StatusOK, map[string]any{"accounts": n})
}
