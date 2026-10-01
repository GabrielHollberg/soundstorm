package auth

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/state"
)

// Profiles: a shared device (the living-room TV) keeps the people who said
// "keep me on this device", and switches between them without a password -
// with their PIN if they set one, and always with the owner's PIN or
// password, since the owner's account can delete media and manage people.
//
// The device is told apart by a cookie of its own, a random id that is not a
// sign-in and names nobody; the server keeps who may be switched to on it
// (state.Kept), keyed by a hash of that id. So one design serves a browser,
// which can hold one session cookie per site, and every app.

// The profile cookie's names, plain and over TLS (as the session's).
const (
	ProfileCookieName       = "soundstorm_profiles"
	SecureProfileCookieName = "__Host-soundstorm_profiles"
)

// profileTTL is how long a device keeps its id without being used.
const profileTTL = 400 * 24 * time.Hour

// ErrBadPIN is a PIN that is not 4 to 8 digits.
var ErrBadPIN = errors.New("a PIN is 4 to 8 digits")

// ProfileDevice is the hashed id of this device's profile cookie, or "" if
// it has none.
func (m *Manager) ProfileDevice(r *http.Request) string {
	for _, name := range []string{SecureProfileCookieName, ProfileCookieName} {
		if c, err := r.Cookie(name); err == nil && len(c.Value) == 32 {
			if _, err := hex.DecodeString(c.Value); err == nil {
				return state.HashDevice(c.Value)
			}
		}
	}
	return ""
}

// EnsureProfileDevice gives this device a profile id if it has none, and
// returns its hash.
func (m *Manager) EnsureProfileDevice(w http.ResponseWriter, r *http.Request) string {
	if d := m.ProfileDevice(r); d != "" {
		return d
	}
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	id := hex.EncodeToString(raw)
	secure := m.OverTLS(r)
	name := ProfileCookieName
	if secure {
		name = SecureProfileCookieName
	}
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: id, Path: "/", MaxAge: int(profileTTL / time.Second),
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: secure,
	})
	return state.HashDevice(id)
}

// pinIterations is lower than a password's: a PIN of four digits is guessed
// by trying, not by hashing, and the switch is limited on wrong PINs.
const pinIterations = 100_000

// SetPIN sets this person's PIN for switching, or with "" removes it. It
// needs their password: a session left open is not enough to change how
// they are protected on shared devices.
//
// The password is checked behind the throttle, as changing a password is:
// otherwise this was a second, unthrottled place to guess it (a review).
func (m *Manager) SetPIN(ctx context.Context, client string, actor state.User, password, pin string) error {
	if err := m.throttle.guarded(ctx, client, actor.Name, func() error { return m.verify(actor.ID, password) }); err != nil {
		return err
	}
	if pin == "" {
		return m.store.SetPIN(actor.ID, nil, nil)
	}
	if len(pin) < 4 || len(pin) > 8 {
		return ErrBadPIN
	}
	for _, c := range pin {
		if c < '0' || c > '9' {
			return ErrBadPIN
		}
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	hash, err := pbkdf2.Key(sha256.New, pin, salt, pinIterations, keyLen)
	if err != nil {
		return err
	}
	return m.store.SetPIN(actor.ID, salt, hash)
}

// CheckPIN reports whether pin is this person's PIN.
func (m *Manager) CheckPIN(user state.User, pin string) bool {
	if len(user.PINHash) == 0 || len(pin) > 8 {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, pin, user.PINSalt, pinIterations, keyLen)
	return err == nil && subtle.ConstantTimeCompare(got, user.PINHash) == 1
}

// CheckPassword reports whether password is this person's, for switching to
// the owner when they have no PIN. The switch limits wrong tries itself.
func (m *Manager) CheckPassword(user state.User, password string) bool {
	return m.verify(user.ID, password) == nil
}

// Revoke ends the session this request carries, if any: a switch to another
// person replaces it rather than leaving it behind.
func (m *Manager) Revoke(r *http.Request) {
	if tok := m.SessionToken(r); tok != "" {
		_ = m.store.DeleteSession(tok)
	}
}
