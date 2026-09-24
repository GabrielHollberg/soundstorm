// Package collections keeps each person's favourites and playlists.
//
// They are SoundStorm's, per person, rather than the backends': the house
// shares one Navidrome and one Jellyfin account (see "Accounts" in CLAUDE.md),
// so favourites or playlists kept there would be everybody's at once - the
// same reason a film's watch position is SoundStorm's.
//
// A file per person, not a field in state.json. That file is rewritten whole
// on every sign-in and every session change, under the lock every request
// takes; a household's worth of playlists in it would make every one of those
// slower. Here a change to one person's list writes one small file.
//
// Each entry carries a snapshot of the item as it was when it was added -
// title, artist, cover - so a list of five hundred songs is one file read,
// not five hundred calls to a backend. Playing it still goes through the
// backend by id, so a song since deleted fails to play rather than playing
// something else.
package collections

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"crypto/rand"
	"encoding/hex"

	"github.com/GabrielHollberg/soundstorm/internal/media"
)

// Limits, so no one person's file can grow without bound.
const (
	MaxFavourites    = 5000
	MaxPlaylists     = 200
	MaxPlaylistItems = 5000
	MaxNameLength    = 100
)

var (
	ErrNotFound = errors.New("no such playlist")
	ErrFull     = errors.New("that list is full")
)

// Entry is one item in a list, as it was when it was added.
type Entry struct {
	Item    media.Item `json:"item"`
	AddedAt time.Time  `json:"addedAt"`
}

// Playlist is an ordered list of songs with a name.
type Playlist struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Items     []Entry   `json:"items"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type collection struct {
	Favourites []Entry     `json:"favourites"`
	Playlists  []*Playlist `json:"playlists"`
}

// Store holds everybody's collections, one file each under dir.
type Store struct {
	dir   string
	mu    sync.Mutex
	cache map[string]*collection
}

// Open prepares the directory. Nothing is read until somebody asks.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("collections: %w", err)
	}
	return &Store{dir: dir, cache: map[string]*collection{}}, nil
}

func (s *Store) path(userID string) (string, error) {
	// Account ids are SoundStorm's own, but a file name is a file name.
	if userID == "" || strings.ContainsAny(userID, `/\.`) {
		return "", fmt.Errorf("collections: bad account id")
	}
	return filepath.Join(s.dir, userID+".json"), nil
}

// load returns one person's collection, reading it the first time. Callers
// hold s.mu.
func (s *Store) load(userID string) (*collection, error) {
	if c, ok := s.cache[userID]; ok {
		return c, nil
	}
	p, err := s.path(userID)
	if err != nil {
		return nil, err
	}
	c := &collection{}
	data, err := os.ReadFile(p)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(data, c); err != nil {
			return nil, fmt.Errorf("collections: read %s: %w", userID, err)
		}
	}
	s.cache[userID] = c
	return c, nil
}

// save writes one person's collection: to a temporary name, then renamed over,
// so a crash mid-write leaves the old file rather than half a new one.
func (s *Store) save(userID string, c *collection) error {
	p, err := s.path(userID)
	if err != nil {
		return err
	}
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, "."+userID+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

func sameItem(e Entry, sourceID, itemID string) bool {
	return e.Item.SourceID == sourceID && e.Item.ID == itemID
}

// --- favourites -------------------------------------------------------------

// Favourites is one person's favourites, most recently added first.
func (s *Store) Favourites(userID string) ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.load(userID)
	if err != nil {
		return nil, err
	}
	out := append([]Entry(nil), c.Favourites...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].AddedAt.After(out[j].AddedAt) })
	return out, nil
}

// AddFavourite adds an item. Adding one already there is not an error.
func (s *Store) AddFavourite(userID string, item media.Item) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.load(userID)
	if err != nil {
		return err
	}
	for _, e := range c.Favourites {
		if sameItem(e, item.SourceID, item.ID) {
			return nil
		}
	}
	if len(c.Favourites) >= MaxFavourites {
		return ErrFull
	}
	c.Favourites = append(c.Favourites, Entry{Item: item, AddedAt: time.Now().UTC()})
	return s.save(userID, c)
}

// RemoveFavourite removes an item. Removing one not there is not an error.
func (s *Store) RemoveFavourite(userID, sourceID, itemID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.load(userID)
	if err != nil {
		return err
	}
	kept := c.Favourites[:0]
	for _, e := range c.Favourites {
		if !sameItem(e, sourceID, itemID) {
			kept = append(kept, e)
		}
	}
	c.Favourites = kept
	return s.save(userID, c)
}

// --- playlists --------------------------------------------------------------

// Playlists is one person's playlists, most recently changed first.
func (s *Store) Playlists(userID string) ([]Playlist, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.load(userID)
	if err != nil {
		return nil, err
	}
	out := make([]Playlist, 0, len(c.Playlists))
	for _, p := range c.Playlists {
		out = append(out, *p)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

// Playlist is one playlist, whole.
func (s *Store) Playlist(userID, id string) (Playlist, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, _, err := s.find(userID, id)
	if err != nil {
		return Playlist{}, err
	}
	out := *p
	out.Items = append([]Entry(nil), p.Items...)
	return out, nil
}

func (s *Store) find(userID, id string) (*Playlist, *collection, error) {
	c, err := s.load(userID)
	if err != nil {
		return nil, nil, err
	}
	for _, p := range c.Playlists {
		if p.ID == id {
			return p, c, nil
		}
	}
	return nil, c, ErrNotFound
}

// CleanName trims a playlist name and says whether it is usable.
func CleanName(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > MaxNameLength {
		return "", false
	}
	return name, true
}

// CreatePlaylist makes an empty playlist.
func (s *Store) CreatePlaylist(userID, name string) (Playlist, error) {
	name, ok := CleanName(name)
	if !ok {
		return Playlist{}, fmt.Errorf("a playlist needs a name of up to %d characters", MaxNameLength)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.load(userID)
	if err != nil {
		return Playlist{}, err
	}
	if len(c.Playlists) >= MaxPlaylists {
		return Playlist{}, ErrFull
	}
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return Playlist{}, err
	}
	now := time.Now().UTC()
	p := &Playlist{ID: hex.EncodeToString(raw), Name: name, Items: []Entry{}, CreatedAt: now, UpdatedAt: now}
	c.Playlists = append(c.Playlists, p)
	if err := s.save(userID, c); err != nil {
		return Playlist{}, err
	}
	return *p, nil
}

// RenamePlaylist changes a playlist's name.
func (s *Store) RenamePlaylist(userID, id, name string) error {
	name, ok := CleanName(name)
	if !ok {
		return fmt.Errorf("a playlist needs a name of up to %d characters", MaxNameLength)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, c, err := s.find(userID, id)
	if err != nil {
		return err
	}
	p.Name = name
	p.UpdatedAt = time.Now().UTC()
	return s.save(userID, c)
}

// DeletePlaylist removes a playlist. The songs in it are untouched - a
// playlist only ever pointed at them.
func (s *Store) DeletePlaylist(userID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, c, err := s.find(userID, id)
	if err != nil {
		return err
	}
	kept := c.Playlists[:0]
	for _, p := range c.Playlists {
		if p.ID != id {
			kept = append(kept, p)
		}
	}
	c.Playlists = kept
	return s.save(userID, c)
}

// AddToPlaylist appends a song. The same song twice is allowed - a playlist
// is an order, and somebody may want a song to come round again.
func (s *Store) AddToPlaylist(userID, id string, item media.Item) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, c, err := s.find(userID, id)
	if err != nil {
		return 0, err
	}
	if len(p.Items) >= MaxPlaylistItems {
		return 0, ErrFull
	}
	p.Items = append(p.Items, Entry{Item: item, AddedAt: time.Now().UTC()})
	p.UpdatedAt = time.Now().UTC()
	return len(p.Items), s.save(userID, c)
}

// RemoveFromPlaylist removes the entry at index.
func (s *Store) RemoveFromPlaylist(userID, id string, index int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, c, err := s.find(userID, id)
	if err != nil {
		return err
	}
	if index < 0 || index >= len(p.Items) {
		return fmt.Errorf("no entry %d", index)
	}
	p.Items = append(p.Items[:index], p.Items[index+1:]...)
	p.UpdatedAt = time.Now().UTC()
	return s.save(userID, c)
}

// MovePlaylistItem moves the entry at from to position to.
func (s *Store) MovePlaylistItem(userID, id string, from, to int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, c, err := s.find(userID, id)
	if err != nil {
		return err
	}
	n := len(p.Items)
	if from < 0 || from >= n || to < 0 || to >= n {
		return fmt.Errorf("no such position")
	}
	e := p.Items[from]
	p.Items = append(p.Items[:from], p.Items[from+1:]...)
	p.Items = append(p.Items[:to], append([]Entry{e}, p.Items[to:]...)...)
	p.UpdatedAt = time.Now().UTC()
	return s.save(userID, c)
}

// Forget removes everything one person kept, for when they are removed.
func (s *Store) Forget(userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cache, userID)
	p, err := s.path(userID)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Export reads every person's file in dir, for a backup: account id to the
// file's contents. Read straight off the disk rather than through a Store -
// the backup runs as its own process beside a stopped server, and has no
// reason to hold anything in memory or write anything back.
func Export(dir string) (map[string]json.RawMessage, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]json.RawMessage{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		out[strings.TrimSuffix(name, ".json")] = raw
	}
	return out, nil
}

// Import writes people's files back from a backup, replacing any already there
// for the same account. Every file is checked to be a collection before
// anything is written, so a damaged backup changes nothing. It returns the
// paths written, so the caller can give them to the right owner.
func Import(dir string, files map[string]json.RawMessage) ([]string, error) {
	if err := Validate(files); err != nil {
		return nil, err
	}
	s := &Store{dir: dir}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	var written []string
	for id, raw := range files {
		var c collection
		_ = json.Unmarshal(raw, &c)
		if err := s.save(id, &c); err != nil {
			return written, err
		}
		p, _ := s.path(id)
		written = append(written, p)
	}
	return written, nil
}

// Validate checks a backup's lists without writing anything: every account id
// must be usable as a file name and every file must read as a collection.
func Validate(files map[string]json.RawMessage) error {
	s := &Store{}
	for id, raw := range files {
		if _, err := s.path(id); err != nil {
			return fmt.Errorf("backup names an account %q that cannot be a file name", id)
		}
		var c collection
		if err := json.Unmarshal(raw, &c); err != nil {
			return fmt.Errorf("favourites and playlists for %s do not read: %w", id, err)
		}
	}
	return nil
}
