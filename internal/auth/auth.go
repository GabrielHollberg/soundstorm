// Package auth is SoundStorm's login.
//
// The whole product claim is "one login". That means this package, not the four
// backends, is what a person authenticates against - and it means the backends
// must never be reachable from a browser, because their own logins still exist
// and SoundStorm is not in front of them if you can dial them directly.
//
// There are two roles and the gap between them is deliberately thin. The owner
// is whoever installed the server; they can add and remove accounts. Everyone
// else is a member. A media server for a household does not need a permission
// matrix, and every role beyond these two is a decision somebody has to make
// about their family.
package auth

import (
	"context"
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

	"github.com/gabehollberg/soundstorm/internal/state"
)

const (
	// iterations follows the OWASP recommendation for PBKDF2-HMAC-SHA256.
	// It costs a few hundred milliseconds per login, which is the point.
	iterations = 600_000
	saltLen    = 16
	keyLen     = 32

	// MinPasswordLength is enforced when a password is set, never when one is
	// checked. Rejecting a password that already exists would lock somebody
	// out of their own library.
	MinPasswordLength = 8

	sessionTTL = 30 * 24 * time.Hour
	CookieName = "soundstorm_session"
)

// ErrInvalidCredentials is returned for both a wrong name and a wrong
// password, so the response cannot be used to enumerate accounts.
var ErrInvalidCredentials = errors.New("invalid username or password")

// ErrForbidden is returned when a signed-in account may not do something.
var ErrForbidden = errors.New("only the owner can manage accounts")

// Manager owns accounts and sessions.
type Manager struct {
	store *state.Store

	// TrustForwardedProto makes X-Forwarded-Proto decide whether the session
	// cookie is marked Secure.
	//
	// Off by default, and it has to be: the header is a plain request header
	// that any client can set, so trusting it unconditionally would let anyone
	// claim their connection was encrypted. It is only meaningful when
	// SoundStorm is behind a proxy that sets it and strips an incoming one.
	TrustForwardedProto bool
}

// New builds a Manager over a state store.
func New(store *state.Store) *Manager { return &Manager{store: store} }

// --- context ----------------------------------------------------------------

type contextKey struct{}

// FromContext returns the account making this request.
//
// Only set on handlers behind Require, which is every handler that can reach
// anybody's data. A handler that needs a user and does not find one should
// treat that as a bug rather than as an anonymous request.
func FromContext(ctx context.Context) (state.User, bool) {
	u, ok := ctx.Value(contextKey{}).(state.User)
	return u, ok
}

// WithUser attaches an account to a context. Exported for tests and for the
// media proxy, which resolves a user before it reaches a source.
func WithUser(ctx context.Context, u state.User) context.Context {
	return context.WithValue(ctx, contextKey{}, u)
}

// --- accounts ---------------------------------------------------------------

// HasAccount reports whether anybody has signed up yet.
func (m *Manager) HasAccount() bool { return m.store.UserCount() > 0 }

// Signup creates the first account, which is always the owner.
//
// It fails once any account exists. Signup is a first-boot action; a second one
// would be a stranger claiming a server somebody else set up.
func (m *Manager) Signup(name, password string) (state.User, error) {
	if m.HasAccount() {
		return state.User{}, errors.New("an account already exists; sign in instead")
	}
	return m.create(name, password, state.RoleOwner)
}

// CreateUser adds an account. Only an owner may call it.
func (m *Manager) CreateUser(actor state.User, name, password, role string) (state.User, error) {
	if !actor.IsOwner() {
		return state.User{}, ErrForbidden
	}
	if role != state.RoleOwner && role != state.RoleMember {
		role = state.RoleMember
	}
	// One owner. Two would let either remove the other, which is a household
	// argument SoundStorm should not be the venue for.
	if role == state.RoleOwner {
		role = state.RoleMember
	}
	return m.create(name, password, role)
}

func (m *Manager) create(name, password, role string) (state.User, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return state.User{}, errors.New("username is required")
	}
	if len(name) > 64 {
		return state.User{}, errors.New("username is too long")
	}
	if err := checkPassword(password); err != nil {
		return state.User{}, err
	}

	salt, hash, err := derive(password, nil)
	if err != nil {
		return state.User{}, err
	}
	return m.store.AddUser(state.User{
		Name:       name,
		Role:       role,
		Salt:       salt,
		Hash:       hash,
		Iterations: iterations,
		CreatedAt:  time.Now().UTC(),
	})
}

// Users lists every account. Only an owner may call it.
func (m *Manager) Users(actor state.User) ([]state.User, error) {
	if !actor.IsOwner() {
		return nil, ErrForbidden
	}
	return m.store.Users(), nil
}

// DeleteUser removes an account and everything attached to it. Only an owner
// may call it, and nobody may remove themselves - an owner deleting their own
// account would leave a server no one could administer.
func (m *Manager) DeleteUser(actor state.User, id string) error {
	if !actor.IsOwner() {
		return ErrForbidden
	}
	if actor.ID == id {
		return errors.New("you cannot remove your own account")
	}
	return m.store.DeleteUser(id)
}

// SetPassword changes a password.
//
// Anyone may change their own, and an owner may change anybody's - which is how
// somebody who has forgotten theirs gets back in, since there is no email to
// send a reset to.
func (m *Manager) SetPassword(actor state.User, id, password string) error {
	if actor.ID != id && !actor.IsOwner() {
		return ErrForbidden
	}
	if err := checkPassword(password); err != nil {
		return err
	}
	salt, hash, err := derive(password, nil)
	if err != nil {
		return err
	}
	return m.store.SetPassword(id, salt, hash, iterations)
}

func checkPassword(password string) error {
	if len(password) < MinPasswordLength {
		return fmt.Errorf("password must be at least %d characters", MinPasswordLength)
	}
	return nil
}

// derive hashes a password, generating a salt when none is given.
func derive(password string, salt []byte) ([]byte, []byte, error) {
	if salt == nil {
		salt = make([]byte, saltLen)
		if _, err := rand.Read(salt); err != nil {
			return nil, nil, fmt.Errorf("generate salt: %w", err)
		}
	}
	hash, err := pbkdf2.Key(sha256.New, password, salt, iterations, keyLen)
	if err != nil {
		return nil, nil, fmt.Errorf("derive key: %w", err)
	}
	return salt, hash, nil
}

// --- signing in ---------------------------------------------------------------

// decoySalt gives an unknown username the same cost as a known one.
//
// With one account it was enough to hash unconditionally. With several, looking
// the name up first and only then hashing would make a wrong name return in
// microseconds and a wrong password in half a second - which is a reliable way
// to find out who has an account here.
var decoySalt = make([]byte, saltLen)

// Login verifies credentials and returns a new session token.
func (m *Manager) Login(name, password string) (string, time.Time, state.User, error) {
	user, found := m.store.UserByName(name)

	salt, want := decoySalt, []byte(nil)
	if found {
		salt, want = user.Salt, user.Hash
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iterationsOr(user.Iterations), keyLen)
	if err != nil {
		return "", time.Time{}, state.User{}, fmt.Errorf("derive key: %w", err)
	}
	if !found || subtle.ConstantTimeCompare(got, want) != 1 {
		return "", time.Time{}, state.User{}, ErrInvalidCredentials
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, state.User{}, fmt.Errorf("generate session token: %w", err)
	}
	token := hex.EncodeToString(raw)
	expiry := time.Now().Add(sessionTTL)
	if err := m.store.AddSession(token, user.ID, expiry); err != nil {
		return "", time.Time{}, state.User{}, err
	}
	return token, expiry, user, nil
}

// iterationsOr keeps a decoy hash the same cost as a real one.
func iterationsOr(n int) int {
	if n <= 0 {
		return iterations
	}
	return n
}

// Logout forgets the session named by the request's cookie.
func (m *Manager) Logout(r *http.Request) error {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return nil
	}
	return m.store.DeleteSession(c.Value)
}

// UserFor returns the account making a request, if it carries a live session.
func (m *Manager) UserFor(r *http.Request) (state.User, bool) {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return state.User{}, false
	}
	return m.store.SessionUser(c.Value)
}

// Authenticated reports whether the request carries a live session.
func (m *Manager) Authenticated(r *http.Request) bool {
	_, ok := m.UserFor(r)
	return ok
}

// --- cookies -------------------------------------------------------------------

// overTLS reports whether this request reached SoundStorm encrypted.
//
// A Secure cookie on a plain HTTP connection is silently dropped by the
// browser, which makes a login appear to succeed and do nothing - so getting
// this wrong in the permissive direction is not a small bug.
func (m *Manager) overTLS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return m.TrustForwardedProto &&
		strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
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
		Secure:   m.overTLS(r),
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
		Secure:   m.overTLS(r),
	})
}

// --- guards ---------------------------------------------------------------------

// Require rejects unauthenticated requests and attaches the account to the
// request context.
//
// This guards the stream and artwork endpoints as well as the JSON API. It has
// to: those endpoints are what actually hand out media bytes, and leaving them
// open would make the login decorative.
func (m *Manager) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := m.UserFor(r)
		if !ok {
			deny(w, http.StatusUnauthorized, "not signed in")
			return
		}
		next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), user)))
	})
}

// RequireOwner additionally refuses anybody who is not the owner. Used for
// account management, which is the one thing the role distinction exists for.
func (m *Manager) RequireOwner(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := FromContext(r.Context())
		if !ok {
			deny(w, http.StatusUnauthorized, "not signed in")
			return
		}
		if !user.IsOwner() {
			deny(w, http.StatusForbidden, ErrForbidden.Error())
			return
		}
		next.ServeHTTP(w, r)
	})
}

func deny(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"error":%q}`, msg)
}
