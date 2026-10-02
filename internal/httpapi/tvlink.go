package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/qr"
)

// Signing a TV in from a phone (the owner's asking): typing a password with
// a remote is the worst part of a TV app. The TV asks for a code and shows it,
// with a QR code of the address that approves it; a phone already signed in
// scans it (or has the code typed into Settings), sees which device is asking
// and as whom it would be signed in, and allows it. The TV, asking every few
// seconds, is then handed a session of its own - as the phone's person.
//
// The code is short to type, so it is not what the TV asks with: that is a
// 128-bit id only the TV knows, as a waiting sign-in's is. A code lives ten
// minutes, works once, and looking codes up is limited per person. What it
// cannot stop is somebody being talked into allowing a stranger's code, which
// is why the phone says plainly what is asking and that it will be signed in
// as them.
//
// A TV signed in this way skips the approval of new devices: the phone that
// allowed it is a device the account already uses.

// linkFor is how long a code waits to be allowed.
const linkFor = 10 * time.Minute

// linkAlphabet leaves out what reads as something else on a TV across a
// room: 0 and O, 1, I and L.
const linkAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

type tvLink struct {
	ID       string
	Code     string // six of linkAlphabet, shown as ABC-DEF
	Device   string
	URL      string // what the QR code opens
	At       time.Time
	answer   int // 0 waiting, 1 allowed, -1 refused
	userID   string
	userName string
}

type tvLinks struct {
	mu     sync.Mutex
	byID   map[string]*tvLink
	byCode map[string]*tvLink
}

func (l *tvLinks) pruneLocked() {
	for id, t := range l.byID {
		if time.Since(t.At) > linkFor {
			delete(l.byID, id)
			delete(l.byCode, t.Code)
		}
	}
}

// normalizeLinkCode is a code as typed: any case, the dash or spaces or not.
func normalizeLinkCode(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if strings.ContainsRune(linkAlphabet, r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func newLinkCode() string {
	raw := make([]byte, 6)
	_, _ = rand.Read(raw)
	out := make([]byte, 6)
	for i, b := range raw {
		// 31 letters into 256 values leaves a slight lean towards the first
		// eight; nothing here rests on codes being evenly spread.
		out[i] = linkAlphabet[int(b)%len(linkAlphabet)]
	}
	return string(out)
}

func showLinkCode(code string) string { return code[:3] + "-" + code[3:] }

// linkURL is the address a phone opens to allow a code: the install's real
// https name when it has one (a phone trusts its certificate, and that is
// where its sign-in lives), else the address the TV used.
func (s *Server) linkURL(r *http.Request, code string) string {
	host := r.Host
	if name := s.currentPublicName(); name != "" {
		port := ""
		if _, p, err := net.SplitHostPort(r.Host); err == nil {
			port = ":" + p
		}
		host = name + port
		return "https://" + host + "/link/" + code
	}
	scheme := "http"
	if s.auth.OverTLS(r) {
		scheme = "https"
	}
	return scheme + "://" + host + "/link/" + code
}

// handleLinkPage is the QR's address. A phone with the Android app opens it
// in the app (an App Link on the install's soundstorm.dev name: the names
// service vouches for the app); in a browser it becomes the page asking
// "Sign in a TV?".
func (s *Server) handleLinkPage(w http.ResponseWriter, r *http.Request) {
	code := strings.Map(func(c rune) rune {
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			return c
		}
		return -1
	}, r.PathValue("code"))
	if len(code) > 16 {
		code = code[:16]
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, "/?link="+strings.ToUpper(code), http.StatusFound)
}

// POST /api/link: a TV asks for a code. Not signed in, by definition, so it
// is limited by address and in all.
func (s *Server) handleNewLink(w http.ResponseWriter, r *http.Request) {
	if !s.linkAsks.allow(clientOf(r), time.Now(), 10, 30*time.Second) {
		writeError(w, http.StatusTooManyRequests, "too many codes asked for; wait a minute")
		return
	}
	l := &s.links
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.byID == nil {
		l.byID, l.byCode = map[string]*tvLink{}, map[string]*tvLink{}
	}
	l.pruneLocked()
	if len(l.byID) >= 300 {
		writeError(w, http.StatusTooManyRequests, "too many codes are waiting; try again in a few minutes")
		return
	}
	code := newLinkCode()
	for l.byCode[code] != nil {
		code = newLinkCode()
	}
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	t := &tvLink{
		ID: hex.EncodeToString(raw), Code: code, Device: deviceLabel(r.UserAgent()),
		URL: s.linkURL(r, code), At: time.Now(),
	}
	l.byID[t.ID] = t
	l.byCode[code] = t
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": t.ID, "code": showLinkCode(code), "url": t.URL, "expiresIn": int(linkFor / time.Second),
	})
}

// GET /api/link/{id}: the TV asks how it went. Allowed, it is signed in here.
func (s *Server) handleLinkStatus(w http.ResponseWriter, r *http.Request) {
	l := &s.links
	l.mu.Lock()
	l.pruneLocked()
	t, ok := l.byID[r.PathValue("id")]
	if !ok {
		l.mu.Unlock()
		writeError(w, http.StatusNotFound, "that code has run out; ask for a new one")
		return
	}
	switch t.answer {
	case 0:
		l.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"waiting": true, "code": showLinkCode(t.Code)})
		return
	case -1:
		delete(l.byID, t.ID)
		delete(l.byCode, t.Code)
		l.mu.Unlock()
		writeError(w, http.StatusForbidden, "that sign-in was not allowed")
		return
	}
	// Allowed: used up, whatever happens next.
	delete(l.byID, t.ID)
	delete(l.byCode, t.Code)
	l.mu.Unlock()
	user, ok := s.store.User(t.userID)
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
	// A TV is shared: whoever signed it in is one of its people.
	s.keepOnDevice(w, r, user)
	if err := s.auth.SetDeviceCookie(w, r, user); err != nil {
		s.log.Warn("could not mark this device as trusted", "err", err)
	}
	s.log.Info("a TV was signed in from a phone", "for", user.Name, "device", t.Device)
	writeJSON(w, http.StatusOK, map[string]any{"signedIn": true, "user": publicUser(user)})
}

// GET /api/link/{id}/qr.png: the code's address as a QR code, for a TV
// without a QR maker of its own (the page's TV mode).
func (s *Server) handleLinkQR(w http.ResponseWriter, r *http.Request) {
	l := &s.links
	l.mu.Lock()
	l.pruneLocked()
	t, ok := l.byID[r.PathValue("id")]
	var url string
	if ok {
		url = t.URL
	}
	l.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "that code has run out")
		return
	}
	code, err := qr.Encode(url)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not draw the code")
		return
	}
	img, err := code.PNG(8)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not draw the code")
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(img)
}

// lookUpLink finds a waiting code for a signed-in person. Looking up is
// limited per person, so codes cannot be tried one after another.
func (s *Server) lookUpLink(w http.ResponseWriter, r *http.Request, userID string) *tvLink {
	if !s.linkLookups.allow(userID, time.Now(), 10, 6*time.Second) {
		writeError(w, http.StatusTooManyRequests, "too many codes tried; wait a minute")
		return nil
	}
	l := &s.links
	l.pruneLocked()
	t := l.byCode[normalizeLinkCode(r.PathValue("code"))]
	if t == nil || t.answer != 0 {
		writeError(w, http.StatusNotFound, "no TV is showing that code; check it, or ask the TV for a new one")
		return nil
	}
	return t
}

// GET /api/link/code/{code}: what is asking, for the phone to show before it
// is allowed.
func (s *Server) handleLinkLookup(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	l := &s.links
	l.mu.Lock()
	defer l.mu.Unlock()
	t := s.lookUpLink(w, r, user.ID)
	if t == nil {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"code": showLinkCode(t.Code), "device": t.Device, "as": user.Name})
}

// POST /api/link/code/{code} {"approve": bool}: the phone's answer. Allowed,
// the TV is signed in as whoever allowed it.
func (s *Server) handleLinkAnswer(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var body struct {
		Approve bool `json:"approve"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "expected a JSON body with approve")
		return
	}
	l := &s.links
	l.mu.Lock()
	defer l.mu.Unlock()
	t := s.lookUpLink(w, r, user.ID)
	if t == nil {
		return
	}
	if body.Approve {
		t.answer, t.userID, t.userName = 1, user.ID, user.Name
	} else {
		t.answer = -1
	}
	writeJSON(w, http.StatusOK, map[string]any{"approved": body.Approve, "device": t.Device})
}
