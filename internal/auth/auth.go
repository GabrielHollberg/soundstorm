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

	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/source"
	"github.com/GabrielHollberg/soundstorm/internal/state"
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

	// MaxPasswordLength is enforced both when a password is set and when one
	// is checked - the opposite of the minimum, and safely so. PBKDF2's cost
	// rises with the input's length past SHA-256's 64-byte block, so a login
	// guess sent thousands of bytes long (the JSON body allows up to 4KB)
	// costs several times what an ordinary attempt does, for the same one
	// hash the throttle was sized around. Because this is also enforced at
	// set time, no genuine password can ever be this long, which is what
	// makes it safe to refuse a longer guess before hashing it at all rather
	// than only when a password is chosen, the way the minimum is. 256 bytes
	// is well past anything anybody has ever typed on purpose.
	MaxPasswordLength = 256

	sessionTTL = 30 * 24 * time.Hour
	CookieName = "soundstorm_session"

	// SecureCookieName is the session cookie's name over TLS. Every install's
	// real address is a name under one shared domain that is not on the Public
	// Suffix List, so to a browser another install - anybody's, including one
	// somebody set up to attack this one - is the same site, and may set a cookie
	// for the whole domain. A plain-named cookie planted that way is sent here
	// ahead of the real one and signs the owner out, over and over. The __Host-
	// prefix is the browser's own answer: it refuses any cookie of that name that
	// names a Domain, so no other host can plant one. It requires Secure, which is
	// why plain HTTP on the LAN keeps the old name - and that is no loss, because
	// a cookie scoped to the shared domain never reaches a bare LAN address.
	SecureCookieName = "__Host-soundstorm_session"
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

	// throttle guards every password check a request can trigger.
	throttle *throttle
}

// New builds a Manager over a state store.
func New(store *state.Store) *Manager {
	return &Manager{store: store, throttle: newThrottle()}
}

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

// Access is what an account is allowed to reach.
//
// The owner is always unrestricted, whatever is stored against them. They are
// the only account that can change these, so an owner who locked themselves out
// of a library would have no way back in.
func Access(u state.User) source.Access {
	if u.IsOwner() || u.Libraries == nil {
		return source.Access{}
	}
	kinds := make([]media.Kind, 0, len(u.Libraries))
	for _, name := range u.Libraries {
		if k, ok := media.ParseKind(name); ok {
			kinds = append(kinds, k)
		}
	}
	// AccessTo of a non-nil empty slice permits nothing, which is the right
	// reading of "this account is allowed no libraries".
	return source.AccessTo(kinds)
}

// SetLibraries chooses which media kinds an account may see. Only an owner may
// call it, and the owner's own access cannot be narrowed - nobody else could
// widen it again.
func (m *Manager) SetLibraries(actor state.User, id string, libraries []string) error {
	if !actor.IsOwner() {
		return ErrForbidden
	}
	target, ok := m.store.User(id)
	if !ok {
		return errors.New("no such account")
	}
	if target.IsOwner() {
		return errors.New("the owner always sees every library")
	}

	// nil means everything. Anything else is normalised and checked here, so a
	// typo becomes an error now rather than a library that silently never
	// appears.
	if libraries != nil {
		cleaned := make([]string, 0, len(libraries))
		seen := map[media.Kind]bool{}
		for _, name := range libraries {
			k, ok := media.ParseKind(name)
			if !ok {
				return fmt.Errorf("there is no library called %q", name)
			}
			if seen[k] {
				continue
			}
			seen[k] = true
			cleaned = append(cleaned, string(k))
		}
		libraries = cleaned
	}
	return m.store.SetLibraries(id, libraries)
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

// ChangeOwnPassword changes the signed-in account's password, and signs out
// every other session it has.
//
// The current password is required. A session is a cookie, and a cookie can be
// left on a shared computer or lifted off an unencrypted network; without this
// check, holding one would be enough to change the password and keep the
// account. The check goes through the same throttle as signing in, or it would
// be a second, unthrottled place to guess.
//
// Other sessions go because changing a password is what somebody does when
// they think it is known - and a password change that leaves the other
// person signed in has fixed nothing. keepToken is the session making the
// request, so changing it does not sign you out of the device you did it on.
func (m *Manager) ChangeOwnPassword(ctx context.Context, client string, actor state.User, current, password, keepToken string) error {
	// Checked first, so a password that would be refused anyway costs no hash
	// and no strike.
	if err := checkPassword(password); err != nil {
		return err
	}
	err := m.throttle.guarded(ctx, client, actor.Name, func() error {
		return m.verify(actor.ID, current)
	})
	if errors.Is(err, ErrInvalidCredentials) {
		return ErrWrongCurrentPassword
	}
	if err != nil {
		return err
	}
	if err := m.SetPassword(actor, actor.ID, password); err != nil {
		return err
	}
	return m.store.DeleteSessionsFor(actor.ID, keepToken)
}

// ErrWrongCurrentPassword is a failed ChangeOwnPassword. It is not
// ErrInvalidCredentials because the person is signed in, and "invalid username
// or password" would be a confusing thing to tell them.
var ErrWrongCurrentPassword = errors.New("your current password is not right")

// ErrResetSelf refuses an owner resetting their own password through the admin
// path, which would skip the current-password check ChangeOwnPassword makes.
var ErrResetSelf = errors.New("use the change-password form to change your own password")

// ResetPassword is the owner setting somebody else's password, which signs
// that account out everywhere - the usual reason for doing it is that somebody
// else knows the old one.
//
// It refuses the owner's own account on purpose. ChangeOwnPassword requires
// the current password precisely so that a stolen session cookie cannot lock
// the real owner out by changing the password without knowing it; letting the
// owner reset *themselves* through this admin path, which takes no current
// password, would hand that capability straight back. Your own password
// changes through ChangeOwnPassword, everyone else's through here.
func (m *Manager) ResetPassword(actor state.User, id, password, keepToken string) error {
	if actor.ID == id {
		return ErrResetSelf
	}
	if err := m.SetPassword(actor, id, password); err != nil {
		return err
	}
	return m.store.DeleteSessionsFor(id, keepToken)
}

// verify checks a password against an account by id.
func (m *Manager) verify(id, password string) error {
	if len(password) > MaxPasswordLength {
		return ErrInvalidCredentials
	}
	user, ok := m.store.User(id)
	if !ok {
		return ErrInvalidCredentials
	}
	got, err := pbkdf2.Key(sha256.New, password, user.Salt, iterationsOr(user.Iterations), keyLen)
	if err != nil {
		return fmt.Errorf("derive key: %w", err)
	}
	if subtle.ConstantTimeCompare(got, user.Hash) != 1 {
		return ErrInvalidCredentials
	}
	return nil
}

func checkPassword(password string) error {
	if len(password) < MinPasswordLength {
		return fmt.Errorf("password must be at least %d characters", MinPasswordLength)
	}
	if len(password) > MaxPasswordLength {
		return fmt.Errorf("password must be at most %d characters", MaxPasswordLength)
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

// SignIn is Login for a request from the network: behind the throttle, so
// that neither guessing nor the cost of hashing is free. client identifies who
// is asking, normally their address. A refusal from the throttle is a
// *ThrottledError, and costs no hash.
func (m *Manager) SignIn(ctx context.Context, client, name, password string) (string, time.Time, state.User, error) {
	var (
		token  string
		expiry time.Time
		user   state.User
	)
	err := m.throttle.guarded(ctx, client, name, func() error {
		var err error
		token, expiry, user, err = m.Login(name, password)
		return err
	})
	return token, expiry, user, err
}

// Login verifies credentials and returns a new session token.
//
// It is not throttled; anything answering the network must use SignIn.
func (m *Manager) Login(name, password string) (string, time.Time, state.User, error) {
	// Refused before the account is even looked up, so a wrong name and an
	// over-long password are indistinguishable in cost - see
	// MaxPasswordLength for why hashing it at all would be worth doing.
	if len(password) > MaxPasswordLength {
		return "", time.Time{}, state.User{}, ErrInvalidCredentials
	}

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
	token, _, ok := m.liveSession(r)
	if !ok {
		return nil
	}
	return m.store.DeleteSession(token)
}

// SessionToken returns the request's live session token, or "" if it has none.
func (m *Manager) SessionToken(r *http.Request) string {
	token, _, _ := m.liveSession(r)
	return token
}

// UserFor returns the account making a request, if it carries a live session.
func (m *Manager) UserFor(r *http.Request) (state.User, bool) {
	_, user, ok := m.liveSession(r)
	return user, ok
}

// liveSession finds the request's session among every session cookie it sent.
//
// It looks at all of them rather than the first, because the first is not
// necessarily ours: a cookie planted for the shared parent domain by another
// install arrives alongside the real one, and a browser sends a cookie with a
// longer path first. Taking only the first would let a junk one sign somebody
// out on every request. The __Host- cookies go first, since nothing but this
// host can have set them.
func (m *Manager) liveSession(r *http.Request) (string, state.User, bool) {
	for _, name := range []string{SecureCookieName, CookieName} {
		for _, c := range r.Cookies() {
			if c.Name != name || c.Value == "" {
				continue
			}
			if user, ok := m.store.SessionUser(c.Value); ok {
				return c.Value, user, true
			}
		}
	}
	return "", state.User{}, false
}

// Authenticated reports whether the request carries a live session.
func (m *Manager) Authenticated(r *http.Request) bool {
	_, ok := m.UserFor(r)
	return ok
}

// --- cookies -------------------------------------------------------------------

// OverTLS reports whether this request reached SoundStorm encrypted.
//
// A Secure cookie on a plain HTTP connection is silently dropped by the
// browser, which makes a login appear to succeed and do nothing - so getting
// this wrong in the permissive direction is not a small bug.
//
// Exported because the UI also has to name a scheme, when it prints the
// address to give somebody else on the network. Two answers to "are we on
// https" that could drift apart is one more than this needs.
func (m *Manager) OverTLS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return m.TrustForwardedProto &&
		strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// SetCookie writes the session cookie: the __Host- one over TLS, where no other
// host can shadow it, and the plain one over plain HTTP, where __Host- is not
// allowed. Over TLS the plain one is expired as well, so a browser that signed in
// before this changed stops carrying the name another install could plant.
func (m *Manager) SetCookie(w http.ResponseWriter, r *http.Request, token string, expiry time.Time) {
	if m.OverTLS(r) {
		http.SetCookie(w, &http.Cookie{
			Name:     SecureCookieName,
			Value:    token,
			Path:     "/",
			Expires:  expiry,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			Secure:   true,
		})
		expireCookie(w, CookieName, true)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		Expires:  expiry,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearCookie expires the session cookie, under whichever names it may have.
func (m *Manager) ClearCookie(w http.ResponseWriter, r *http.Request) {
	secure := m.OverTLS(r)
	expireCookie(w, CookieName, secure)
	if secure {
		// A __Host- cookie can only be set, including to expire it, over TLS.
		expireCookie(w, SecureCookieName, true)
	}
}

func expireCookie(w http.ResponseWriter, name string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
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
