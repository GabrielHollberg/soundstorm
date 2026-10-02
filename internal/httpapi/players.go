package httpapi

// Playing on another device from your phone, and controlling it (the
// owner's design, 2026-10-02). Every open SoundStorm page is a player: it
// says hello, reports what it is playing, and waits for commands by asking
// the server (a long poll), so there is no pairing and no "same Wi-Fi" - the
// phone and the TV only ever talk to the server.
//
// One rule keeps accounts apart: a device acts as exactly one person, and
// only that person's phones may control it. A TV is the exception that
// proves it - it is shared by its nature, so anybody in the house may send
// something to it, and doing so switches the TV to them first (as picking
// them on "Who's listening?" would; their phone, signed in, vouches for
// them). Nobody's phone ever steers something acting as someone else.
//
// Taking over a TV somebody else is using asks on the TV first ("Nathan
// wants to play something. Let him?"); no answer in 15 seconds is a yes,
// since a TV playing to an empty room should not block anyone. A No holds
// that person off for 5 minutes, a second for 30; a TV that has stopped
// playing for a few minutes is free again, with no asking.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"regexp"
	"sync"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/state"
)

const (
	playerGone      = 90 * time.Second // no hello or poll for this long: not listed
	playerIdle      = 3 * time.Minute  // not playing for this long: a TV is free
	askTimeout      = 15 * time.Second
	switchCodeLife  = 2 * time.Minute
	maxPlayers      = 400
	maxPlayersEach  = 20
	maxQueued       = 20
	maxStateBytes   = 16 << 10
	maxCommandBytes = 64 << 10
	pollWait        = 25 * time.Second
)

var playerIDPattern = regexp.MustCompile(`^[a-f0-9]{16,64}$`)

type player struct {
	ID       string
	Name     string
	UserID   string
	TV       bool
	Seen     time.Time
	State    json.RawMessage
	Playing  bool
	ActiveAt time.Time // last time it reported playing
	queue    []json.RawMessage
	wake     chan struct{}
	asks     map[string]*playerAsk
}

type playerAsk struct {
	ID     string
	From   string // user id
	Name   string
	At     time.Time
	Answer int // 0 waiting, 1 yes, -1 no
	cmd    json.RawMessage
}

type denial struct {
	until time.Time
	count int
}

type switchCode struct {
	player, user string
	until        time.Time
}

type playerHub struct {
	mu     sync.Mutex
	m      map[string]*player
	denied map[string]*denial // player id + "/" + user id
	codes  map[string]switchCode
}

func (h *playerHub) init() {
	if h.m == nil {
		h.m = map[string]*player{}
		h.denied = map[string]*denial{}
		h.codes = map[string]switchCode{}
	}
}

func (h *playerHub) pruneLocked(now time.Time) {
	for id, p := range h.m {
		if now.Sub(p.Seen) > 10*time.Minute {
			delete(h.m, id)
		}
	}
	for k, c := range h.codes {
		if now.After(c.until) {
			delete(h.codes, k)
		}
	}
	for k, d := range h.denied {
		if now.After(d.until.Add(time.Hour)) {
			delete(h.denied, k)
		}
	}
}

// free is a TV nobody is watching or listening to.
func (p *player) free(now time.Time) bool {
	return !p.Playing && now.Sub(p.ActiveAt) > playerIdle || now.Sub(p.Seen) > playerGone
}

func (p *player) enqueueLocked(cmd json.RawMessage) {
	p.queue = append(p.queue, cmd)
	if len(p.queue) > maxQueued {
		p.queue = p.queue[len(p.queue)-maxQueued:]
	}
	if p.wake != nil {
		close(p.wake)
		p.wake = nil
	}
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// POST /api/players/hello {"id", "name", "tv"}: this page is a player.
func (s *Server) handlePlayerHello(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var body struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		TV   bool   `json:"tv"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || !playerIDPattern.MatchString(body.ID) {
		writeError(w, http.StatusBadRequest, "expected a player id")
		return
	}
	name := []rune(body.Name)
	if len(name) > 60 {
		name = name[:60]
	}
	if len(name) == 0 {
		name = []rune(deviceLabel(r.UserAgent()))
	}
	now := time.Now()
	h := &s.players
	h.mu.Lock()
	defer h.mu.Unlock()
	h.init()
	h.pruneLocked(now)
	p, exists := h.m[body.ID]
	if !exists {
		mine := 0
		for _, q := range h.m {
			if q.UserID == user.ID {
				mine++
			}
		}
		if mine >= maxPlayersEach || len(h.m) >= maxPlayers {
			writeError(w, http.StatusTooManyRequests, "too many devices are open")
			return
		}
		p = &player{ID: body.ID, asks: map[string]*playerAsk{}}
		h.m[body.ID] = p
	}
	if p.UserID != user.ID {
		// A device used by somebody else now: nothing of the last person's
		// carries over.
		p.queue, p.State, p.Playing = nil, nil, false
		p.asks = map[string]*playerAsk{}
	}
	p.UserID, p.Name, p.TV, p.Seen = user.ID, string(name), body.TV, now
	writeJSON(w, http.StatusOK, map[string]any{"id": p.ID})
}

// ownPlayer is a player acting as this person, or nil after writing why not.
func (s *Server) ownPlayer(w http.ResponseWriter, r *http.Request, user state.User) *player {
	p, ok := s.players.m[r.PathValue("id")]
	if !ok || p.UserID != user.ID {
		writeError(w, http.StatusNotFound, "that device is not open")
		return nil
	}
	return p
}

// GET /api/players/{id}/next: the commands waiting for this player, waiting
// up to 25 seconds for one.
func (s *Server) handlePlayerNext(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	deadline := time.NewTimer(pollWait)
	defer deadline.Stop()
	for {
		h := &s.players
		h.mu.Lock()
		h.init()
		p := s.ownPlayer(w, r, user)
		if p == nil {
			h.mu.Unlock()
			return
		}
		p.Seen = time.Now()
		if len(p.queue) > 0 {
			// Up to a switch and no further: the TV makes itself again as
			// the new person, and what follows waits for that page.
			n := len(p.queue)
			for i, c := range p.queue {
				var head struct {
					Type string `json:"type"`
				}
				if json.Unmarshal(c, &head) == nil && head.Type == "switch" {
					n = i + 1
					break
				}
			}
			out := p.queue[:n:n]
			p.queue = append([]json.RawMessage(nil), p.queue[n:]...)
			h.mu.Unlock()
			writeJSON(w, http.StatusOK, map[string]any{"commands": out})
			return
		}
		if p.wake == nil {
			p.wake = make(chan struct{})
		}
		wake := p.wake
		h.mu.Unlock()
		select {
		case <-wake:
		case <-deadline.C:
			writeJSON(w, http.StatusOK, map[string]any{"commands": []any{}})
			return
		case <-r.Context().Done():
			return
		}
	}
}

// POST /api/players/{id}/state: what this player is doing, for the phones
// controlling it. {"playing": bool, ...} - the rest is the page's own.
func (s *Server) handlePlayerState(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	raw := json.RawMessage{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxStateBytes)).Decode(&raw); err != nil {
		writeError(w, http.StatusBadRequest, "expected the player's state")
		return
	}
	var flags struct {
		Playing bool `json:"playing"`
	}
	_ = json.Unmarshal(raw, &flags)
	h := &s.players
	h.mu.Lock()
	defer h.mu.Unlock()
	h.init()
	p := s.ownPlayer(w, r, user)
	if p == nil {
		return
	}
	now := time.Now()
	p.State, p.Playing, p.Seen = raw, flags.Playing, now
	if flags.Playing {
		p.ActiveAt = now
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// GET /api/players?self=<id>: the devices this person may play on - their
// own, and every TV - with what their own are doing.
func (s *Server) handlePlayers(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	self := r.URL.Query().Get("self")
	only := r.PathValue("id")
	now := time.Now()
	h := &s.players
	h.mu.Lock()
	h.init()
	type out struct {
		ID     string          `json:"id"`
		Name   string          `json:"name"`
		TV     bool            `json:"tv"`
		Mine   bool            `json:"mine"`
		Person string          `json:"person,omitempty"`
		Busy   bool            `json:"busy"`
		State  json.RawMessage `json:"state,omitempty"`
	}
	list := []out{}
	for _, p := range h.m {
		if p.ID == self || now.Sub(p.Seen) > playerGone || (only != "" && p.ID != only) {
			continue
		}
		mine := p.UserID == user.ID
		if !mine && !p.TV {
			continue
		}
		o := out{ID: p.ID, Name: p.Name, TV: p.TV, Mine: mine}
		if mine {
			o.State = p.State
		} else {
			o.Busy = !p.free(now)
			if u, ok := s.store.User(p.UserID); ok {
				o.Person = u.Name
			}
		}
		list = append(list, o)
	}
	h.mu.Unlock()
	if only != "" {
		if len(list) == 0 {
			writeError(w, http.StatusNotFound, "that device is not open")
			return
		}
		writeJSON(w, http.StatusOK, list[0])
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"players": list})
}

// POST /api/players/{id}/command {"type": "play"|"control"|"volume"|"stop"|"claim", ...}
//
// "claim" is a phone choosing the device to control (the device picker): it
// plays nothing, and on somebody else's TV it takes it over as "play" does.
func (s *Server) handlePlayerCommand(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	raw := json.RawMessage{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxCommandBytes)).Decode(&raw); err != nil {
		writeError(w, http.StatusBadRequest, "expected a command")
		return
	}
	var head struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(raw, &head)
	switch head.Type {
	case "play", "control", "volume", "stop", "claim":
	default:
		writeError(w, http.StatusBadRequest, "not a command a player takes")
		return
	}
	now := time.Now()
	h := &s.players
	h.mu.Lock()
	defer h.mu.Unlock()
	h.init()
	p, ok := h.m[r.PathValue("id")]
	if !ok || now.Sub(p.Seen) > playerGone || (p.UserID != user.ID && !p.TV) {
		writeError(w, http.StatusNotFound, "that device is not open")
		return
	}
	if p.UserID == user.ID {
		if head.Type == "claim" {
			writeJSON(w, http.StatusOK, map[string]any{"sent": true})
			return
		}
		p.enqueueLocked(withFrom(raw, user.Name))
		writeJSON(w, http.StatusOK, map[string]any{"sent": true})
		return
	}
	// Somebody else's TV: only sending something to it, which takes it over.
	if head.Type != "play" && head.Type != "claim" {
		writeError(w, http.StatusForbidden, "that TV is playing as someone else; send something to it to take it over")
		return
	}
	if p.free(now) {
		s.switchPlayerLocked(p, user, raw)
		writeJSON(w, http.StatusOK, map[string]any{"sent": true, "switched": true})
		return
	}
	key := p.ID + "/" + user.ID
	if d := h.denied[key]; d != nil && now.Before(d.until) {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"error":   "they said no; you can ask again later",
			"retryIn": int(d.until.Sub(now).Seconds()) + 1,
		})
		return
	}
	for _, a := range p.asks {
		if a.From == user.ID && a.Answer == 0 && now.Sub(a.At) < askTimeout {
			writeJSON(w, http.StatusAccepted, map[string]any{"asking": a.ID})
			return
		}
	}
	a := &playerAsk{ID: randomHex(12), From: user.ID, Name: user.Name, At: now, cmd: raw}
	p.asks[a.ID] = a
	ask, _ := json.Marshal(map[string]any{"type": "ask", "ask": a.ID, "from": user.Name})
	p.enqueueLocked(ask)
	writeJSON(w, http.StatusAccepted, map[string]any{"asking": a.ID})
}

// withFrom adds who sent a command, for the TV's "from Nathan's phone".
func withFrom(raw json.RawMessage, name string) json.RawMessage {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return raw
	}
	m["from"] = name
	out, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return out
}

// switchPlayerLocked switches a TV to user and then hands it the command: the
// TV is given a one-time code to sign in with, and the command waits behind
// it, for the TV as that person.
func (s *Server) switchPlayerLocked(p *player, user state.User, cmd json.RawMessage) {
	h := &s.players
	code := randomHex(16)
	h.codes[code] = switchCode{player: p.ID, user: user.ID, until: time.Now().Add(switchCodeLife)}
	sw, _ := json.Marshal(map[string]any{"type": "switch", "code": code, "from": user.Name})
	p.queue = nil
	p.enqueueLocked(sw)
	p.enqueueLocked(withFrom(cmd, user.Name))
	// Still the last person's until it has signed in with the code (it has
	// to fetch the code as them); then it is the new person's
	// (handlePlayerSwitch), with the command waiting for it.
	p.Playing = false
}

// GET /api/players/{id}/ask/{ask}: how the asking went, for the phone.
// POST with {"allow": bool}: the TV's answer.
func (s *Server) handlePlayerAsk(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	now := time.Now()
	h := &s.players
	h.mu.Lock()
	defer h.mu.Unlock()
	h.init()
	p, ok := h.m[r.PathValue("id")]
	if !ok {
		writeError(w, http.StatusNotFound, "that device is not open")
		return
	}
	a, ok := p.asks[r.PathValue("ask")]
	if !ok {
		writeError(w, http.StatusNotFound, "that question is over")
		return
	}
	if r.Method == http.MethodPost {
		// The TV's person answers.
		if p.UserID != user.ID {
			writeError(w, http.StatusForbidden, "only the TV answers")
			return
		}
		var body struct {
			Allow bool `json:"allow"`
		}
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body)
		if a.Answer == 0 {
			a.Answer = -1
			if body.Allow {
				a.Answer = 1
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"answered": true})
		return
	}
	if a.From != user.ID {
		writeError(w, http.StatusNotFound, "that question is over")
		return
	}
	if a.Answer == 0 && now.Sub(a.At) >= askTimeout {
		a.Answer = 1 // nobody there to say no
	}
	switch a.Answer {
	case 0:
		writeJSON(w, http.StatusOK, map[string]any{"waiting": true})
	case 1:
		delete(p.asks, a.ID)
		s.switchPlayerLocked(p, user, a.cmd)
		writeJSON(w, http.StatusOK, map[string]any{"allowed": true})
	default:
		delete(p.asks, a.ID)
		key := p.ID + "/" + user.ID
		d := h.denied[key]
		if d == nil {
			d = &denial{}
			h.denied[key] = d
		}
		d.count++
		wait := 5 * time.Minute
		if d.count >= 2 {
			wait = 30 * time.Minute
		}
		d.until = now.Add(wait)
		writeJSON(w, http.StatusOK, map[string]any{"denied": true, "retryIn": int(wait.Seconds())})
	}
}

// POST /api/players/switch {"code", "id"}: a TV, told to switch person,
// signs in as them with the one-time code. Open, as the TV is still the last
// person until it has: the code is 128 bits, for one TV, used once.
func (s *Server) handlePlayerSwitch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"code"`
		ID   string `json:"id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "expected a code")
		return
	}
	h := &s.players
	h.mu.Lock()
	h.init()
	c, ok := h.codes[body.Code]
	if ok {
		delete(h.codes, body.Code)
	}
	h.mu.Unlock()
	if !ok || c.player != body.ID || time.Now().After(c.until) {
		writeError(w, http.StatusForbidden, "that switch has run out")
		return
	}
	user, ok := s.store.User(c.user)
	if !ok {
		writeError(w, http.StatusNotFound, "that account is gone")
		return
	}
	h.mu.Lock()
	if p, ok := h.m[c.player]; ok {
		p.UserID, p.asks = user.ID, map[string]*playerAsk{}
	}
	h.mu.Unlock()
	s.auth.Revoke(r)
	token, expiry, err := s.auth.SessionFor(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not switch")
		return
	}
	s.auth.SetCookie(w, r, token, expiry)
	// A TV is shared: whoever takes it over is one of its people.
	s.keepOnDevice(w, r, user)
	if err := s.auth.SetDeviceCookie(w, r, user); err != nil {
		s.log.Warn("could not mark this device as trusted", "err", err)
	}
	s.log.Info("a TV was taken over from a phone", "for", user.Name)
	writeJSON(w, http.StatusOK, map[string]any{"signedIn": true, "user": publicUser(user)})
}
