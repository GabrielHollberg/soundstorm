// Package state persists the little that SoundStorm must remember across
// restarts.
//
// This is a deliberate departure from the original design, which was proudly
// stateless. Two v1 requirements force it:
//
//   - Nobody types an API key. SoundStorm provisions each backend's credentials on
//     first boot, so it has to keep them somewhere it can read again.
//   - There are logins. Accounts and their sessions have to outlive a restart.
//
// What is NOT here: anything about the media itself. No metadata, no library
// index, no play counts. The backends own all of that, which is why this file
// stays a few kilobytes and why losing it costs a re-provision, not a library.
//
// One principled exception: reading position for ebooks. For music, film and
// audiobooks the backend owns play state, so SoundStorm does not. For ebooks
// there IS no backend - SoundStorm reads the folder itself - so if SoundStorm
// does not remember where you stopped reading, nothing does.
package state

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Backend is one provisioned upstream server and the credentials SoundStorm
// generated for itself on that server.
type Backend struct {
	Type    string `json:"type"`    // "navidrome", "jellyfin"
	BaseURL string `json:"baseUrl"` // internal URL, e.g. http://navidrome:4533

	// Subsonic-style backends authenticate per request with these.
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`

	// Jellyfin-style backends authenticate with an access token.
	Token  string `json:"token,omitempty"`
	UserID string `json:"userId,omitempty"`

	// LibraryID names which library on the backend SoundStorm should search, for
	// backends that can hold several (Audiobookshelf).
	LibraryID string `json:"libraryId,omitempty"`

	ProvisionedAt time.Time `json:"provisionedAt"`
}

// Roles. There is exactly one owner - whoever installed the server - and any
// number of members. The distinction is deliberately thin: an owner can manage
// accounts, and that is the only thing they can do that a member cannot. A
// media server for a household does not need a permission matrix.
const (
	RoleOwner  = "owner"
	RoleMember = "member"
)

// User is one account. Password verification lives in internal/auth; this
// package only stores the derived material.
type User struct {
	// ID is generated and never changes. Accounts are keyed by it rather than
	// by name so that deleting somebody and creating a new account with the
	// same name does not quietly hand over their listening history.
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`

	// Libraries is which media kinds this account may see. Nil means all of
	// them, which is what every account created before this existed gets, and
	// the default for a new one.
	//
	// No omitempty, deliberately. An account allowed nothing is a real state
	// and it serialises as []; with omitempty that would vanish from the file
	// and read back as nil, which means everything. The one mistake this field
	// must not make is failing open.
	Libraries []string `json:"libraries"`

	Salt       []byte    `json:"salt"`
	Hash       []byte    `json:"hash"`
	Iterations int       `json:"iterations"`
	CreatedAt  time.Time `json:"createdAt"`
}

// IsOwner reports whether this account can manage other accounts.
func (u User) IsOwner() bool { return u.Role == RoleOwner }

// Session is a live sign-in.
type Session struct {
	UserID  string    `json:"userId"`
	Expires time.Time `json:"expires"`
}

// UnmarshalJSON accepts both the current shape and the one that came before
// it, when there was one account and a session was just an expiry timestamp.
//
// Doing it here rather than in a migration pass keeps the old shape's
// existence in one place, and means an upgrade cannot sign everybody out. The
// missing UserID is filled in by the migration, which is the only thing that
// knows whose session it must have been.
func (s *Session) UnmarshalJSON(raw []byte) error {
	type shape Session
	var current shape
	if err := json.Unmarshal(raw, &current); err == nil {
		*s = Session(current)
		return nil
	}
	var expiry time.Time
	if err := json.Unmarshal(raw, &expiry); err != nil {
		return fmt.Errorf("parse session: %w", err)
	}
	s.Expires = expiry
	return nil
}

// Identity is the account SoundStorm holds on a backend for one of its users.
//
// Most backends are used through a single shared account, because nothing
// user-visible depends on who is asking. Audiobookshelf is the exception:
// it tracks listening position per user, so two people sharing one account
// there would overwrite each other's place in a book.
type Identity struct {
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
	Token    string `json:"token,omitempty"`
	RemoteID string `json:"remoteId,omitempty"`

	CreatedAt time.Time `json:"createdAt"`
}

// Progress is where the reader left off in one book.
type Progress struct {
	// Location is an EPUB CFI - a content-anchored pointer, not a page number.
	// Page numbers are meaningless in reflowable text: change the font size and
	// "page 47" is different words.
	Location  string    `json:"location"`
	Fraction  float64   `json:"fraction"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// currentVersion is the state file's schema version. Version 1 had a single
// `user` object; version 2 has a map of accounts; version 3 keys a session by
// a hash of its token rather than the token itself.
const currentVersion = 3

type data struct {
	Version int `json:"version"`

	// User is version 1's single account, read on upgrade and then dropped.
	// It is never written: `omitempty` plus a nil pointer means the key
	// disappears from the file the first time it is saved.
	User *User `json:"user,omitempty"`

	Users    map[string]User     `json:"users"`
	Sessions map[string]Session  `json:"sessions"`
	Backends map[string]Backend  `json:"backends"`
	Progress map[string]Progress `json:"progress"`

	// Identities are per-user accounts on a backend, keyed by user id and then
	// by backend id.
	Identities map[string]map[string]Identity `json:"identities,omitempty"`

	// StarterInstalled records that the bundled sample library has had its one
	// chance to unpack. Without it the unpack runs on every boot into any shelf
	// with no media on it, so somebody who deleted the samples on purpose got
	// them back the next time the server restarted.
	//
	// This is the one thing here that is not a credential or an account, and it
	// is deliberately not a fact about anybody's media - it says what SoundStorm
	// has done, not what is in the library. The alternative was a marker file in
	// the library folder, which is worse for a reason that is easy to miss:
	// Windows Explorer does not hide dot-files, so it would be the single stray
	// item at the top of "the folders are the interface", and deleting it - the
	// obvious thing to do with a file you do not recognise - would bring the
	// samples back.
	//
	// It does mean `docker compose down -v` forgets, and a reinstall over an
	// existing library can put samples on a shelf that happens to be empty.
	// That is the documented full reset, and arriving at a fresh install with
	// something to look at is the behaviour this feature exists for.
	StarterInstalled bool `json:"starterInstalled,omitempty"`

	// RemoteAccess is whether the owner has turned on reaching this install
	// from the internet. A pointer so absent means "not chosen" - which falls
	// back to the SOUNDSTORM_REMOTE_ACCESS default - rather than "off", the
	// same reason User.Libraries is careful about nil. Once the owner toggles
	// it in the app, their choice is what stands.
	RemoteAccess *bool `json:"remoteAccess,omitempty"`
	// OnlineLyrics is whether the owner lets SoundStorm look up missing
	// lyrics on LRCLIB. Off unless turned on: it sends a song's artist and
	// title to an outside service, which nothing else here does.
	OnlineLyrics bool `json:"onlineLyrics,omitempty"`
	// ReadAlongManual is the owner turning off syncing books for read-along
	// by themselves. Absent means on - the default is to sync - so it is
	// stored the other way round from OnlineLyrics.
	ReadAlongManual bool `json:"readAlongManual,omitempty"`
	// NotPairs are Read & listen matches the owner has said are wrong, each
	// "ebookSource/id|audiobookSource/id". A decision somebody made, like
	// StarterInstalled - not a fact read off the media, so nothing here can
	// go stale in a way that matters: an id that no longer exists simply
	// never matches again.
	NotPairs []string `json:"notPairs,omitempty"`
	// ManualPairs are ebooks and audiobooks the owner has paired by hand,
	// where the titles or authors differ too much to match, same shape.
	ManualPairs []string `json:"manualPairs,omitempty"`

	// DeviceKey signs the tokens that mark a browser as one an account has
	// signed in on before (see auth.Manager.SignIn). Made on first use; a
	// secret like the credentials beside it, since whoever holds it could mint
	// a token that skips the sign-in backoff - though never one that skips the
	// password.
	DeviceKey []byte `json:"deviceKey,omitempty"`
}

// Store is the on-disk state, guarded for concurrent use.
type Store struct {
	mu   sync.Mutex
	path string
	d    data
}

// Open loads the state file, creating an empty one if it does not exist.
func Open(path string) (*Store, error) {
	s := &Store{
		path: path,
		d: data{
			Version:    currentVersion,
			Users:      map[string]User{},
			Sessions:   map[string]Session{},
			Backends:   map[string]Backend{},
			Progress:   map[string]Progress{},
			Identities: map[string]map[string]Identity{},
		},
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}

	raw, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		// First boot. Write it now so a permissions problem fails here, at
		// startup, rather than at the moment someone signs up.
		if err := s.save(); err != nil {
			return nil, err
		}
		return s, nil
	case err != nil:
		return nil, fmt.Errorf("read state: %w", err)
	}

	if err := json.Unmarshal(raw, &s.d); err != nil {
		return nil, fmt.Errorf("parse state %s: %w", path, err)
	}
	if s.d.Users == nil {
		s.d.Users = map[string]User{}
	}
	if s.d.Sessions == nil {
		s.d.Sessions = map[string]Session{}
	}
	if s.d.Backends == nil {
		s.d.Backends = map[string]Backend{}
	}
	if s.d.Progress == nil {
		s.d.Progress = map[string]Progress{}
	}
	if s.d.Identities == nil {
		s.d.Identities = map[string]map[string]Identity{}
	}
	s.migrateLocked()
	s.pruneLocked()
	return s, s.save()
}

// migrateLocked brings a version 1 file up to date.
//
// Version 1 had one account, sessions that were bare expiry timestamps, and
// reading positions keyed by source and item alone. All three become wrong the
// moment there are two people, so the single account becomes the owner, its
// sessions get its id, and its bookmarks get its prefix. Nobody is signed out
// and nobody loses their place, which is the whole point of doing this rather
// than starting the file again.
func (s *Store) migrateLocked() {
	// Independent of the account migration below, and checked first: a file
	// already at version 2 still needs this one.
	if s.d.Version < 3 {
		s.hashSessionTokensLocked()
	}

	if s.d.Version >= currentVersion {
		s.d.Version = currentVersion
		return
	}

	if s.d.User != nil {
		owner := *s.d.User
		owner.ID = newID()
		owner.Role = RoleOwner
		s.d.Users[owner.ID] = owner

		for token, session := range s.d.Sessions {
			if session.UserID == "" {
				session.UserID = owner.ID
				s.d.Sessions[token] = session
			}
		}

		moved := make(map[string]Progress, len(s.d.Progress))
		for key, p := range s.d.Progress {
			moved[owner.ID+"/"+key] = p
		}
		s.d.Progress = moved
	}

	s.d.User = nil
	s.d.Version = currentVersion
}

// hashSessionTokensLocked rewrites the session map so its keys are a hash of
// each token rather than the token itself.
//
// Every earlier version kept the literal cookie value as the map key, so
// state.json - the file a backup copies, the file left in an old volume,
// anything that gets hold of it without the server around it - was itself
// enough to sign in as anybody with a live session, owner included, for as
// long as it had left to run. A session token has as much entropy as a
// password reset token: nothing to be slow against, since the only way to
// find one is to already have it, but no reason to keep it in the clear
// either.
//
// This is lossless. The key being replaced *is* the original raw token, so
// hashing it in place computes exactly what a real request presenting that
// same cookie will look up next. Nobody is signed out by it, unlike most ways
// of changing how a credential is stored.
func (s *Store) hashSessionTokensLocked() {
	if len(s.d.Sessions) == 0 {
		return
	}
	hashed := make(map[string]Session, len(s.d.Sessions))
	for token, session := range s.d.Sessions {
		hashed[hashSessionToken(token)] = session
	}
	s.d.Sessions = hashed
}

// hashSessionToken is what a session is actually keyed by, on disk and in
// memory. SHA-256 rather than anything slow: the input is 256 bits from
// crypto/rand, not a password somebody chose, so brute force is not the
// threat a hash has to answer here - reading it off the disk is.
func hashSessionToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// newID generates an account identifier.
func newID() string {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		// crypto/rand failing is not a condition a media server can do
		// anything useful about, and a duplicate id would be worse.
		panic("state: no randomness available: " + err.Error())
	}
	return hex.EncodeToString(raw)
}

// DeviceKey returns the secret device tokens are signed with, making and saving
// one the first time it is asked for.
func (s *Store) DeviceKey() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.d.DeviceKey) == 0 {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("generate device key: %w", err)
		}
		s.d.DeviceKey = key
		if err := s.save(); err != nil {
			s.d.DeviceKey = nil
			return nil, err
		}
	}
	return append([]byte(nil), s.d.DeviceKey...), nil
}

// encodeState renders the state as indented JSON without HTML escaping. The
// escaping turns every <, > and & into six bytes - pointless in a file nothing
// renders, and a way for text a client supplies to weigh six times as much in a
// file that is rewritten whole on every change.
func encodeState(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// save writes the state file atomically. Callers must hold the mutex, except
// Open before it publishes the Store.
func (s *Store) save() error {
	encoded, err := encodeState(s.d)
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, encoded, 0o600); err != nil {
		return fmt.Errorf("write state: %w", err)
	}

	// Keep the version being replaced, before replacing it.
	//
	// This file is the only copy of the credentials SoundStorm generated for
	// four backends, and those backends cannot be re-provisioned: an account
	// already exists on each of them and nothing else knows its password. A
	// bad write here is not "lose your settings", it is "lose the servers".
	//
	// It is a sibling rather than a second location on purpose. The rename
	// below already survives a crash, so what this adds is a way back from a
	// write that succeeded and should not have - a wrong edit, a truncation, a
	// migration that went sideways. Surviving the volume being deleted is a
	// different problem and needs somewhere else entirely, which is what
	// BackupTo exists for.
	if previous, err := os.ReadFile(s.path); err == nil {
		// Best effort. A backup that cannot be written is not a reason to
		// refuse the write that matters.
		_ = os.WriteFile(s.path+".bak", previous, 0o600)
	}

	// os.Rename replaces an existing file on both Unix and Windows, so a
	// crash mid-write leaves the previous state intact rather than a partial
	// file that would strand every provisioned credential.
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("replace state: %w", err)
	}
	return nil
}

// BackendIDs lists the backends this state holds credentials for, sorted so
// the order is the same every time it is printed.
func (s *Store) BackendIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]string, 0, len(s.d.Backends))
	for id := range s.d.Backends {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Summary describes a state file without opening it as a Store.
type Summary struct {
	Version  int
	Users    int
	Backends []string
}

// Inspect reads a state file and reports what is in it, writing nothing.
//
// state.Open cannot be used for this: it migrates and saves unconditionally,
// so merely looking at a state file with it rewrites the file and hands
// ownership to whoever asked. A backup that modifies the thing it is backing
// up is not a backup.
func Inspect(path string) (Summary, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Summary{}, err
	}
	var d data
	if err := json.Unmarshal(raw, &d); err != nil {
		return Summary{}, fmt.Errorf("parse state %s: %w", path, err)
	}
	ids := make([]string, 0, len(d.Backends))
	for id := range d.Backends {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return Summary{Version: d.Version, Users: len(d.Users), Backends: ids}, nil
}

// CopyTo copies a state file byte for byte, touching neither end's contents.
func CopyTo(path, dest string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read state: %w", err)
	}
	if dir := filepath.Dir(dest); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create backup directory: %w", err)
		}
	}
	if err := os.WriteFile(dest, raw, 0o600); err != nil {
		return fmt.Errorf("write backup: %w", err)
	}
	return nil
}

// BackupTo writes a copy of the state somewhere that is not this volume.
//
// The sibling .bak above covers a bad write. It does nothing for the failure
// that actually ends an install: `docker compose down -v`, a wiped volume, a
// replaced machine. After that the backends are still there, still holding
// accounts SoundStorm created, with passwords that existed in exactly one
// file. The provisioners detect it and say so; they cannot fix it.
//
// 0600, and the caller chooses where. This file is worth as much as the
// server, so it is never written anywhere by default - somewhere the media
// folder gets synced to would be the obvious wrong place.
func (s *Store) BackupTo(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	encoded, err := encodeState(s.d)
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create backup directory: %w", err)
		}
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		return fmt.Errorf("write backup: %w", err)
	}
	return nil
}

// RestoreFrom replaces the state with a backup, after checking it is one.
//
// Refusing a file that is not a SoundStorm state is the whole value here: the
// alternative is overwriting a working install with a typo and discovering it
// at the next restart.
func RestoreFrom(backup, path string) error {
	raw, err := os.ReadFile(backup)
	if err != nil {
		return fmt.Errorf("read backup: %w", err)
	}
	return RestoreBytes(raw, backup, path)
}

// RestoreBytes is RestoreFrom for a backup already read - from standard input,
// say. name is what to call it in an error.
func RestoreBytes(raw []byte, name, path string) error {
	backup := name
	var probe data
	if err := json.Unmarshal(raw, &probe); err != nil {
		return fmt.Errorf("%s is not a SoundStorm backup: %w", backup, err)
	}
	if probe.Version == 0 {
		return fmt.Errorf("%s has no version field, so it is not a SoundStorm backup", backup)
	}
	if probe.Version > currentVersion {
		return fmt.Errorf("%s was written by a newer SoundStorm (version %d, this one understands %d)",
			backup, probe.Version, currentVersion)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	// Whatever is there now becomes the .bak, so restoring the wrong file is
	// itself undoable.
	if previous, err := os.ReadFile(path); err == nil {
		_ = os.WriteFile(path+".bak", previous, 0o600)
	}
	tmp := path + ".restore"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace state: %w", err)
	}
	return nil
}

// UserCount reports how many accounts exist. Zero means nobody has signed up.
func (s *Store) UserCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.d.Users)
}

// Users returns every account, oldest first, so a list does not reshuffle
// itself between page loads.
func (s *Store) Users() []User {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]User, 0, len(s.d.Users))
	for _, u := range s.d.Users {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

// User returns one account by id.
func (s *Store) User(id string) (User, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.d.Users[id]
	return u, ok
}

// UserByName finds an account by name, case-insensitively.
//
// Names are matched loosely but stored as typed: somebody who signed up as
// "Gabe" should not have to remember that when they sign in, and should not be
// greeted as "gabe" either.
func (s *Store) UserByName(name string) (User, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	want := strings.ToLower(strings.TrimSpace(name))
	for _, u := range s.d.Users {
		if strings.ToLower(u.Name) == want {
			return u, true
		}
	}
	return User{}, false
}

// AddUser stores a new account, generating its id.
//
// It refuses a name already in use, case-insensitively: two accounts that
// differ only in capitalisation would make signing in ambiguous.
func (s *Store) AddUser(u User) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	want := strings.ToLower(strings.TrimSpace(u.Name))
	for _, existing := range s.d.Users {
		if strings.ToLower(existing.Name) == want {
			return User{}, fmt.Errorf("there is already an account called %q", existing.Name)
		}
	}

	u.ID = newID()
	// Role is decided here, under the lock, not trusted from the caller - so a
	// second first-boot signup that raced past HasAccount and also asked to be
	// owner cannot become a second one. The first account is always the owner;
	// everyone after it is a member, whatever was requested.
	if len(s.d.Users) == 0 {
		u.Role = RoleOwner
	} else {
		u.Role = RoleMember
	}
	s.d.Users[u.ID] = u
	return u, s.save()
}

// SetLibraries records which media kinds an account may see. Nil means all.
func (s *Store) SetLibraries(id string, libraries []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.d.Users[id]
	if !ok {
		return fmt.Errorf("no such account")
	}
	u.Libraries = libraries
	s.d.Users[id] = u
	return s.save()
}

// SetPassword replaces an account's derived password material.
func (s *Store) SetPassword(id string, salt, hash []byte, iterations int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.d.Users[id]
	if !ok {
		return fmt.Errorf("no such account")
	}
	u.Salt, u.Hash, u.Iterations = salt, hash, iterations
	s.d.Users[id] = u
	return s.save()
}

// DeleteUser removes an account along with everything attached to it: its
// sessions, its bookmarks, and the accounts SoundStorm made for it on the
// backends.
//
// The owner cannot be deleted. Nothing could then manage accounts, and the
// server would need its state file edited by hand to recover - which is
// precisely the kind of thing this project exists to avoid.
func (s *Store) DeleteUser(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	u, ok := s.d.Users[id]
	if !ok {
		return fmt.Errorf("no such account")
	}
	if u.IsOwner() {
		return fmt.Errorf("the owner account cannot be removed")
	}

	delete(s.d.Users, id)
	delete(s.d.Identities, id)
	for key, session := range s.d.Sessions {
		if session.UserID == id {
			delete(s.d.Sessions, key)
		}
	}
	prefix := id + "/"
	for key := range s.d.Progress {
		if strings.HasPrefix(key, prefix) {
			delete(s.d.Progress, key)
		}
	}
	return s.save()
}

// Identity returns the account SoundStorm holds for a user on one backend.
func (s *Store) Identity(userID, backendID string) (Identity, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.d.Identities[userID][backendID]
	return id, ok
}

// SetIdentity records an account SoundStorm created for a user on a backend.
func (s *Store) SetIdentity(userID, backendID string, id Identity) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.d.Identities[userID] == nil {
		s.d.Identities[userID] = map[string]Identity{}
	}
	s.d.Identities[userID][backendID] = id
	return s.save()
}

// Backend returns a provisioned backend's credentials.
func (s *Store) Backend(id string) (Backend, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.d.Backends[id]
	return b, ok
}

// StarterInstalled reports whether the bundled sample library has already had
// its one chance to unpack.
func (s *Store) StarterInstalled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.d.StarterInstalled
}

// MarkStarterInstalled records that it has, so it never runs again.
//
// Set whether or not anything was actually written: a boot that found every
// shelf occupied has answered the question just as well as one that unpacked
// ten books, and leaving the flag clear in that case would keep the samples
// waiting for the first shelf somebody empties.
func (s *Store) MarkStarterInstalled() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.d.StarterInstalled {
		return nil // already recorded; no reason to write the file
	}
	s.d.StarterInstalled = true
	return s.save()
}

// RemoteAccess reports whether remote access is on, and whether the owner has
// made a choice at all - when they have not, the caller uses the configured
// default rather than reading the false as a decision.
func (s *Store) RemoteAccess() (on, chosen bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.d.RemoteAccess == nil {
		return false, false
	}
	return *s.d.RemoteAccess, true
}

// SetRemoteAccess records the owner's choice.
func (s *Store) SetRemoteAccess(on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.d.RemoteAccess != nil && *s.d.RemoteAccess == on {
		return nil
	}
	s.d.RemoteAccess = &on
	return s.save()
}

// SetBackend records a freshly provisioned backend.
func (s *Store) SetBackend(id string, b Backend) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.d.Backends[id] = b
	return s.save()
}

// Progress returns where the reader left off, if anywhere.
func (s *Store) Progress(key string) (Progress, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.d.Progress[key]
	return p, ok
}

// ProgressWithPrefix returns every reading position whose key starts with
// prefix - one account's, for the Continue row.
func (s *Store) ProgressWithPrefix(prefix string) map[string]Progress {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]Progress{}
	for k, p := range s.d.Progress {
		if strings.HasPrefix(k, prefix) {
			out[k] = p
		}
	}
	return out
}

// SetProgress records a reading position. Callers should throttle: this writes
// the state file, and a reader emits a location on every page turn.
//
// maxKeys caps how many distinct positions one prefix (one account) may hold,
// so a member cannot grow the state file without bound by bookmarking an
// endless stream of made-up item ids - the whole file is rewritten on every
// save. An existing key is always updatable; only a new one past the cap is
// refused. A non-positive maxKeys means no cap.
func (s *Store) SetProgress(key string, p Progress, prefix string, maxKeys int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.d.Progress[key]; !exists && maxKeys > 0 {
		n := 0
		for k := range s.d.Progress {
			if strings.HasPrefix(k, prefix) {
				n++
			}
		}
		if n >= maxKeys {
			return fmt.Errorf("too many saved positions")
		}
	}
	s.d.Progress[key] = p
	return s.save()
}

// AddSession records a session token, whose it is, and when it expires.
//
// The token itself is never written - only its hash. See
// hashSessionTokensLocked for why.
func (s *Store) AddSession(token, userID string, expiry time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	s.capSessionsLocked(userID, maxSessionsPerUser-1)
	s.d.Sessions[hashSessionToken(token)] = Session{UserID: userID, Expires: expiry}
	return s.save()
}

// maxSessionsPerUser bounds how many signed-in devices one account keeps. Each
// sign-in adds a session that lives for weeks and is written into the file every
// request's lock guards, so without a cap one account signing in over and over
// grows it without end. Far more devices than a person owns; the oldest goes
// first, which is the one least likely to still be in use.
const maxSessionsPerUser = 50

// capSessionsLocked removes an account's soonest-expiring sessions until it has
// at most keep. Every session is created with the same lifetime, so the soonest
// to expire is the oldest.
func (s *Store) capSessionsLocked(userID string, keep int) {
	var mine []string
	for key, sess := range s.d.Sessions {
		if sess.UserID == userID {
			mine = append(mine, key)
		}
	}
	if len(mine) <= keep {
		return
	}
	sort.Slice(mine, func(i, j int) bool {
		return s.d.Sessions[mine[i]].Expires.Before(s.d.Sessions[mine[j]].Expires)
	})
	for _, key := range mine[:len(mine)-keep] {
		delete(s.d.Sessions, key)
	}
}

// SessionUser returns the account a live session belongs to.
//
// A session whose account has been deleted is not live, which is what makes
// removing somebody take effect immediately rather than whenever their cookie
// happened to expire.
func (s *Store) SessionUser(token string) (User, bool) {
	if token == "" {
		return User{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.d.Sessions[hashSessionToken(token)]
	if !ok || !time.Now().Before(session.Expires) {
		return User{}, false
	}
	u, ok := s.d.Users[session.UserID]
	return u, ok
}

// DeleteSession forgets a session (logout).
func (s *Store) DeleteSession(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := hashSessionToken(token)
	if _, ok := s.d.Sessions[key]; !ok {
		return nil
	}
	delete(s.d.Sessions, key)
	return s.save()
}

// DeleteSessionsFor signs an account out everywhere except keep, which may be
// empty. Used when a password changes: the old one may be known to somebody,
// and their session would otherwise outlive the change by up to a month.
func (s *Store) DeleteSessionsFor(userID, keep string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	keepKey := hashSessionToken(keep)
	changed := false
	for key, session := range s.d.Sessions {
		if session.UserID == userID && key != keepKey {
			delete(s.d.Sessions, key)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return s.save()
}

// pruneLocked drops expired sessions. Callers must hold the mutex.
func (s *Store) pruneLocked() {
	now := time.Now()
	for key, session := range s.d.Sessions {
		if now.After(session.Expires) {
			delete(s.d.Sessions, key)
		}
	}
}

// OnlineLyrics reports whether looking up missing lyrics online is on.
func (s *Store) OnlineLyrics() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.d.OnlineLyrics
}

// SetOnlineLyrics turns looking up missing lyrics online on or off.
func (s *Store) SetOnlineLyrics(on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.d.OnlineLyrics == on {
		return nil
	}
	s.d.OnlineLyrics = on
	return s.save()
}

// AutoReadAlong reports whether books are synced for read-along as soon as
// there is both an ebook and an audiobook of them.
func (s *Store) AutoReadAlong() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.d.ReadAlongManual
}

// SetAutoReadAlong turns syncing books by themselves on or off.
func (s *Store) SetAutoReadAlong(on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.d.ReadAlongManual == !on {
		return nil
	}
	s.d.ReadAlongManual = !on
	return s.save()
}

// MaxNotPairs bounds the wrong-match list.
const MaxNotPairs = 1000

// NotPairs is the set of Read & listen matches marked wrong.
func (s *Store) NotPairs() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]bool, len(s.d.NotPairs))
	for _, k := range s.d.NotPairs {
		out[k] = true
	}
	return out
}

// SetNotPair marks a match wrong, or right again.
func (s *Store) SetNotPair(key string, wrong bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	at := -1
	for i, k := range s.d.NotPairs {
		if k == key {
			at = i
		}
	}
	switch {
	case wrong && at < 0:
		if len(s.d.NotPairs) >= MaxNotPairs {
			return fmt.Errorf("too many matches marked wrong")
		}
		s.d.NotPairs = append(s.d.NotPairs, key)
	case !wrong && at >= 0:
		s.d.NotPairs = append(s.d.NotPairs[:at], s.d.NotPairs[at+1:]...)
	default:
		return nil
	}
	return s.save()
}

// ManualPairs is the list of pairs made by hand.
func (s *Store) ManualPairs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.d.ManualPairs...)
}

// SetManualPair pairs an ebook and an audiobook by hand, or unpairs them.
// Pairing also takes the two off the wrong-match list: the owner has just
// said they are the same.
func (s *Store) SetManualPair(key string, paired bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	remove := func(list []string) ([]string, bool) {
		for i, k := range list {
			if k == key {
				return append(list[:i], list[i+1:]...), true
			}
		}
		return list, false
	}
	changed := false
	if paired {
		if l, ok := remove(s.d.NotPairs); ok {
			s.d.NotPairs, changed = l, true
		}
		if _, ok := remove(append([]string(nil), s.d.ManualPairs...)); !ok {
			if len(s.d.ManualPairs) >= MaxNotPairs {
				return fmt.Errorf("too many books paired by hand")
			}
			s.d.ManualPairs = append(s.d.ManualPairs, key)
			changed = true
		}
	} else if l, ok := remove(s.d.ManualPairs); ok {
		s.d.ManualPairs, changed = l, true
	}
	if !changed {
		return nil
	}
	return s.save()
}
