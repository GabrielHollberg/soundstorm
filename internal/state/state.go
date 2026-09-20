// Package state persists the little that atrium must remember across restarts.
//
// This is a deliberate departure from the original design, which was proudly
// stateless. Two v1 requirements force it:
//
//   - Nobody types an API key. atrium provisions each backend's credentials on
//     first boot, so it has to keep them somewhere it can read again.
//   - There is one login. A user and their sessions have to outlive a restart.
//
// What is NOT here: anything about the media itself. No metadata, no library
// index, no play counts. The backends own all of that, which is why this file
// stays a few kilobytes and why losing it costs a re-provision, not a library.
//
// One principled exception: reading position for ebooks. For music, film and
// audiobooks the backend owns play state, so atrium does not. For ebooks there
// IS no backend - atrium reads the folder itself - so if atrium does not
// remember where you stopped reading, nothing does.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Backend is one provisioned upstream server and the credentials atrium
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

	// LibraryID names which library on the backend atrium should search, for
	// backends that can hold several (Audiobookshelf).
	LibraryID string `json:"libraryId,omitempty"`

	ProvisionedAt time.Time `json:"provisionedAt"`
}

// User is atrium's single account. Password verification lives in
// internal/auth; this package only stores the derived material.
type User struct {
	Name       string    `json:"name"`
	Salt       []byte    `json:"salt"`
	Hash       []byte    `json:"hash"`
	Iterations int       `json:"iterations"`
	CreatedAt  time.Time `json:"createdAt"`
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

type data struct {
	Version  int                  `json:"version"`
	User     *User                `json:"user"`
	Sessions map[string]time.Time `json:"sessions"`
	Backends map[string]Backend   `json:"backends"`
	Progress map[string]Progress  `json:"progress"`
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
			Version:  1,
			Sessions: map[string]time.Time{},
			Backends: map[string]Backend{},
			Progress: map[string]Progress{},
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
	if s.d.Sessions == nil {
		s.d.Sessions = map[string]time.Time{}
	}
	if s.d.Backends == nil {
		s.d.Backends = map[string]Backend{}
	}
	if s.d.Progress == nil {
		s.d.Progress = map[string]Progress{}
	}
	s.pruneLocked()
	return s, s.save()
}

// save writes the state file atomically. Callers must hold the mutex, except
// Open before it publishes the Store.
func (s *Store) save() error {
	encoded, err := json.MarshalIndent(s.d, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, encoded, 0o600); err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	// os.Rename replaces an existing file on both Unix and Windows, so a
	// crash mid-write leaves the previous state intact rather than a partial
	// file that would strand every provisioned credential.
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("replace state: %w", err)
	}
	return nil
}

// User returns the account, or nil when nobody has signed up yet.
func (s *Store) User() *User {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.d.User == nil {
		return nil
	}
	u := *s.d.User
	return &u
}

// SetUser stores the account. It refuses to overwrite an existing one: signup
// is a first-boot action, and a second signup would be an account takeover.
func (s *Store) SetUser(u User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.d.User != nil {
		return fmt.Errorf("an account already exists")
	}
	s.d.User = &u
	return s.save()
}

// Backend returns a provisioned backend's credentials.
func (s *Store) Backend(id string) (Backend, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.d.Backends[id]
	return b, ok
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

// SetProgress records a reading position. Callers should throttle: this writes
// the state file, and a reader emits a location on every page turn.
func (s *Store) SetProgress(key string, p Progress) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.d.Progress[key] = p
	return s.save()
}

// AddSession records a session token and its expiry.
func (s *Store) AddSession(token string, expiry time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	s.d.Sessions[token] = expiry
	return s.save()
}

// ValidSession reports whether the token names a live session.
func (s *Store) ValidSession(token string) bool {
	if token == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	expiry, ok := s.d.Sessions[token]
	return ok && time.Now().Before(expiry)
}

// DeleteSession forgets a session (logout).
func (s *Store) DeleteSession(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.d.Sessions[token]; !ok {
		return nil
	}
	delete(s.d.Sessions, token)
	return s.save()
}

// pruneLocked drops expired sessions. Callers must hold the mutex.
func (s *Store) pruneLocked() {
	now := time.Now()
	for token, expiry := range s.d.Sessions {
		if now.After(expiry) {
			delete(s.d.Sessions, token)
		}
	}
}
