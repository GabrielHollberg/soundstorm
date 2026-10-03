package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/auth"
	"github.com/GabrielHollberg/soundstorm/internal/collections"
	"github.com/GabrielHollberg/soundstorm/internal/state"
)

// Profiles (the owner's design, after Prime Video's account-then-people): a
// shared device keeps the people who chose "keep me on this device" and asks
// "Who's listening?", switching between them without a password - with their
// PIN if they set one, and the owner always with their PIN or password. The
// device is its own cookie, not a sign-in (auth/profiles.go); who may be
// switched to on it is the server's record (state.Kept). Signing out takes
// you off the device; switching keeps everybody on it.

// switchFails counts wrong PINs (or passwords) per device and person: five
// and the switch waits a quarter of an hour, doubling; fifteen and that
// person is taken off the device.
type switchFails struct {
	mu sync.Mutex
	m  map[string]*switchFail
}

type switchFail struct {
	n     int
	until time.Time
}

const (
	switchFree     = 5
	switchWait     = 15 * time.Minute
	switchForgetAt = 15
)

// profileJSON is a person as the picker shows them, and what switching to
// them needs: their PIN, the owner's password (an owner without a PIN), or
// nothing.
func profileJSON(u state.User) map[string]any {
	needs := ""
	switch {
	case len(u.PINHash) > 0:
		needs = "pin"
	case u.IsOwner():
		needs = "password"
	}
	return map[string]any{"id": u.ID, "name": u.Name, "owner": u.IsOwner(), "needs": needs}
}

// GET /api/profiles: who this device may switch between, and who it is now.
// Open: a TV asks before anybody is signed in.
func (s *Server) handleProfiles(w http.ResponseWriter, r *http.Request) {
	people := []map[string]any{}
	if device := s.auth.ProfileDevice(r); device != "" {
		for _, u := range s.store.KeptOn(device) {
			people = append(people, s.withPicture(profileJSON(u), u.ID))
		}
	}
	current := ""
	if u, ok := s.auth.UserFor(r); ok {
		current = u.ID
	}
	writeJSON(w, http.StatusOK, map[string]any{"people": people, "current": current})
}

// POST /api/profiles/switch {"id", "pin" | "password"}: this device becomes
// that person, if they are kept on it and what they need is given.
func (s *Server) handleSwitchProfile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID       string `json:"id"`
		PIN      string `json:"pin"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "expected a JSON body with id")
		return
	}
	device := s.auth.ProfileDevice(r)
	var user state.User
	found := false
	if device != "" {
		for _, u := range s.store.KeptOn(device) {
			if u.ID == body.ID {
				user, found = u, true
			}
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, "that person is not kept on this device; sign in instead")
		return
	}
	// Each try is counted before it is checked, so a burst of guesses sent at
	// once is counted as a burst, not let through before any of it fails;
	// the right answer wipes the count.
	key := device + "/" + user.ID
	f := &s.switchFails
	f.mu.Lock()
	if f.m == nil {
		f.m = map[string]*switchFail{}
	}
	fail := f.m[key]
	if fail == nil {
		fail = &switchFail{}
		f.m[key] = fail
	}
	if time.Now().Before(fail.until) {
		f.mu.Unlock()
		writeError(w, http.StatusTooManyRequests, "too many wrong tries; wait a while, or sign in with the password")
		return
	}
	fail.n++
	tries := fail.n
	if tries > switchFree && tries%switchFree == 1 {
		fail.until = time.Now().Add(switchWait << (tries/switchFree - 1))
	}
	f.mu.Unlock()

	ok := true
	switch profileJSON(user)["needs"] {
	case "pin":
		ok = s.auth.CheckPIN(user, body.PIN)
	case "password":
		ok = s.auth.CheckPassword(user, body.Password)
	}
	if !ok {
		if tries >= switchForgetAt {
			f.mu.Lock()
			delete(f.m, key)
			f.mu.Unlock()
			_ = s.store.Unkeep(device, user.ID)
			s.log.Warn("a person was taken off a device after too many wrong tries", "for", user.Name)
		}
		writeError(w, http.StatusForbidden, "that is not right")
		return
	}
	f.mu.Lock()
	delete(f.m, key)
	f.mu.Unlock()

	// The session this device had is replaced, not left behind.
	s.auth.Revoke(r)
	token, expiry, err := s.auth.SessionFor(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not switch")
		return
	}
	s.auth.SetCookie(w, r, token, expiry)
	if err := s.auth.SetDeviceCookie(w, r, user); err != nil {
		s.log.Warn("could not mark this device as trusted", "err", err)
	}
	_ = s.store.Keep(device, user.ID) // the latest first
	writeJSON(w, http.StatusOK, map[string]any{"signedIn": true, "user": s.withPicture(publicUser(user), user.ID)})
}

// keepOnDevice is "keep me on this device", after a sign-in that asked for
// it, or a TV signed in from a phone.
func (s *Server) keepOnDevice(w http.ResponseWriter, r *http.Request, user state.User) {
	device := s.auth.EnsureProfileDevice(w, r)
	if err := s.store.Keep(device, user.ID); err != nil {
		s.log.Warn("could not keep a person on a device", "err", err)
	}
}

// POST /api/profiles/keep: the person signed in keeps themselves on this
// device.
func (s *Server) handleKeepProfile(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	s.keepOnDevice(w, r, user)
	writeJSON(w, http.StatusOK, map[string]any{"kept": true})
}

// DELETE /api/profiles/{id}: off this device - yourself, or anybody for the
// owner.
func (s *Server) handleUnkeepProfile(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if id != user.ID && !user.IsOwner() {
		writeError(w, http.StatusForbidden, "only the owner can take somebody else off a device")
		return
	}
	if device := s.auth.ProfileDevice(r); device != "" {
		_ = s.store.Unkeep(device, id)
	}
	writeJSON(w, http.StatusOK, map[string]any{"kept": false})
}

// PUT /api/account/pin {"password", "pin"}: this person's PIN for switching
// to them on a shared device; "" removes it.
func (s *Server) handleSetPIN(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var body struct {
		Password string `json:"password"`
		PIN      string `json:"pin"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxCredentialBody)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "expected a JSON body with password and pin")
		return
	}
	switch err := s.auth.SetPIN(r.Context(), clientOf(r), user, body.Password, body.PIN); {
	case errors.Is(err, auth.ErrBadPIN):
		writeError(w, http.StatusBadRequest, err.Error())
	case func() bool { _, ok := auth.IsThrottled(err); return ok }():
		t, _ := auth.IsThrottled(err)
		writeThrottled(w, t)
	case err != nil:
		writeError(w, http.StatusForbidden, "that password is not right")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"pin": body.PIN != ""})
	}
}

// withPicture adds where a person's own picture is to m, when they have one.
// The address changes with the picture, so it can be kept for good.
func (s *Server) withPicture(m map[string]any, userID string) map[string]any {
	if _, name, ok := s.collections.ProfilePicture(userID); ok {
		m["picture"] = "/api/profiles/" + userID + "/picture?v=" + name[:12]
	}
	return m
}

// GET /api/profiles/{id}/picture: a person's own picture, for the circle at
// the top of the screen and "Who's listening?". Open, because that screen
// is shown before anybody is signed in - but only for a person kept on this
// device, or to somebody signed in to this server (the household sees each
// other's circles).
func (s *Server) handleProfilePicture(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	allowed := false
	if _, ok := s.auth.UserFor(r); ok {
		allowed = true
	} else if device := s.auth.ProfileDevice(r); device != "" {
		for _, u := range s.store.KeptOn(device) {
			allowed = allowed || u.ID == id
		}
	}
	path, name, ok := s.collections.ProfilePicture(id)
	if !allowed || !ok {
		http.NotFound(w, r)
		return
	}
	types := map[string]string{"jpg": "image/jpeg", "png": "image/png", "webp": "image/webp"}
	w.Header().Set("Content-Type", types[name[strings.LastIndex(name, ".")+1:]])
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox")
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	http.ServeFile(w, r, path)
}

// PUT /api/account/picture (the body an image) sets this person's own
// picture; DELETE takes it away, back to their initial.
func (s *Server) handleSetProfilePicture(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodDelete {
		if err := s.collections.RemoveArt(user.ID, []string{collections.ProfileArtKey}); err != nil {
			s.collectionsError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	if !s.artUploads.allow(user.ID, time.Now(), 20, 3*time.Second) {
		writeError(w, http.StatusTooManyRequests, "Too many at once; wait a moment.")
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, collections.MaxArtBytes+1))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "That picture is too big.")
		return
	}
	switch err := s.collections.SetArt(user.ID, []string{collections.ProfileArtKey}, data); {
	case errors.Is(err, collections.ErrBadArt):
		writeError(w, http.StatusUnsupportedMediaType, "Choose a JPEG, PNG or WebP picture.")
		return
	case err != nil:
		s.collectionsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.withPicture(map[string]any{}, user.ID))
}
