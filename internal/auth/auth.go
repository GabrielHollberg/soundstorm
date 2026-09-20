// Package auth is atrium's single-account login.
//
// The whole product claim is "one login". That means this package, not the four
// backends, is what a person authenticates against - and it means the backends
// must never be reachable from a browser, because their own logins still exist
// and atrium is not in front of them if you can dial them directly.
//
// Scope for now is deliberately one account. Multi-user needs per-user
// libraries and per-user play state, which is a real feature and not a slice.
package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gabehollberg/atrium/internal/state"
)

const (
	// iterations follows the OWASP recommendation for PBKDF2-HMAC-SHA256.
	// It costs a few hundred milliseconds per login, which is the point.
	iterations = 600_000
	saltLen    = 16
	keyLen     = 32

	// MinPasswordLength is enforced at signup only. Rejecting a password that
	// already exists would lock someone out of their own library.
	MinPasswordLength = 8

	sessionTTL = 30 * 24 * time.Hour
	CookieName = "atrium_session"
)

// ErrInvalidCredentials is returned for both a wrong name and a wrong
// password, so the response cannot be used to enumerate accounts.
var ErrInvalidCredentials = errors.New("invalid username or password")

// Manager owns account and session checks.
type Manager struct {
	store *state.Store
}

// New builds a Manager over a state store.
func New(store *state.Store) *Manager { return &Manager{store: store} }

// HasAccount reports whether signup has already happened.
func (m *Manager) HasAccount() bool { return m.store.User() != nil }

// Signup creates the one account. It fails if one already exists.
func (m *Manager) Signup(name, password string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("username is required")
	}
	if len(password) < MinPasswordLength {
		return fmt.Errorf("password must be at least %d characters", MinPasswordLength)
	}

	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return fmt.Errorf("generate salt: %w", err)
	}
	hash, err := pbkdf2.Key(sha256.New, password, salt, iterations, keyLen)
	if err != nil {
		return fmt.Errorf("derive key: %w", err)
	}

	return m.store.SetUser(state.User{
		Name:       name,
		Salt:       salt,
		Hash:       hash,
		Iterations: iterations,
		CreatedAt:  time.Now().UTC(),
	})
}

// Login verifies credentials and returns a new session token.
func (m *Manager) Login(name, password string) (string, time.Time, error) {
	user := m.store.User()
	if user == nil {
		return "", time.Time{}, ErrInvalidCredentials
	}

	// Derive regardless of whether the name matched, so a wrong name and a
	// wrong password take the same time.
	hash, err := pbkdf2.Key(sha256.New, password, user.Salt, user.Iterations, len(user.Hash))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("derive key: %w", err)
	}

	nameOK := subtle.ConstantTimeCompare([]byte(strings.TrimSpace(name)), []byte(user.Name))
	hashOK := subtle.ConstantTimeCompare(hash, user.Hash)
	if nameOK&hashOK != 1 {
		return "", time.Time{}, ErrInvalidCredentials
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, fmt.Errorf("generate session token: %w", err)
	}
	token := hex.EncodeToString(raw)
	expiry := time.Now().Add(sessionTTL)
	if err := m.store.AddSession(token, expiry); err != nil {
		return "", time.Time{}, err
	}
	return token, expiry, nil
}

// Logout forgets the session named by the request's cookie.
func (m *Manager) Logout(r *http.Request) error {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return nil
	}
	return m.store.DeleteSession(c.Value)
}

// Authenticated reports whether the request carries a live session.
func (m *Manager) Authenticated(r *http.Request) bool {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return false
	}
	return m.store.ValidSession(c.Value)
}

// SetCookie writes the session cookie.
func (m *Manager) SetCookie(w http.ResponseWriter, r *http.Request, token string, expiry time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		Expires:  expiry,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		// Set only over TLS: a Secure cookie on plain HTTP is silently
		// dropped, which would make login appear to succeed and do nothing.
		Secure: r.TLS != nil,
	})
}

// ClearCookie expires the session cookie.
func (m *Manager) ClearCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
	})
}

// Require rejects unauthenticated requests.
//
// This guards the stream and artwork endpoints as well as the JSON API. It has
// to: those endpoints are what actually hand out media bytes, and leaving them
// open would make the login decorative.
func (m *Manager) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !m.Authenticated(r) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"not signed in"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}
