// Package collections keeps each person's favorites and playlists.
//
// They are SoundStorm's, per person, rather than the backends': the house
// shares one Navidrome and one Jellyfin account (see "Accounts" in CLAUDE.md),
// so favorites or playlists kept there would be everybody's at once - the
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
	MaxFavorites     = 5000
	MaxPlaylists     = 200
	MaxPlaylistItems = 5000
	MaxNameLength    = 100
)

var (
	ErrNotFound = errors.New("no such playlist")
	ErrFull     = errors.New("that list is full")
	// ErrAlreadyIn is a song added to a playlist it is already in: a
	// playlist holds each song once.
	ErrAlreadyIn = errors.New("that song is already in this playlist")
	// ErrBadSort is an order a playlist does not offer.
	ErrBadSort = errors.New("that is not a way to order a playlist")
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
	// Sort is how its songs are shown and played: "" is A to Z by title,
	// "artist", "added" (newest first), or "custom" - the order they were
	// put in and dragged to, which Items always keeps whatever the sort.
	Sort string `json:"sort,omitempty"`
}

// PlaylistSorts are the orders a playlist can be shown in.
var PlaylistSorts = map[string]bool{"": true, "title": true, "artist": true, "added": true, "custom": true}

type collection struct {
	// "favourites" is the name on disk since the first file: kept, or every
	// saved list would read as empty.
	Favorites []Entry     `json:"favourites"`
	Playlists []*Playlist `json:"playlists"`
	// History is what this person has listened to, keyed "source/id", for
	// Recently played, Most played and Rediscover. Per person for the same
	// reason as everything else here: Navidrome's play counts would be the
	// whole house's, and only count plays somebody reported to it.
	History map[string]*Play `json:"history,omitempty"`
	// Prefs are this person's small choices, which follow them from device
	// to device.
	Prefs *Prefs `json:"prefs,omitempty"`
	// Scrobbler is where this person's plays are also sent, if anywhere.
	Scrobbler *Scrobbler `json:"scrobbler,omitempty"`
	// Art is this person's own covers: a key naming what it replaces
	// ("song:<source>/<id>" or "art:<source>/<art id>") to the image's file
	// name under art/<account>/.
	Art map[string]string `json:"art,omitempty"`
}

// Play is how often and when one song was listened to.
type Play struct {
	Item  media.Item `json:"item"`
	Count int        `json:"count"`
	First time.Time  `json:"first"`
	Last  time.Time  `json:"last"`
}

// MaxHistory bounds one person's history; past it, the songs least recently
// played are forgotten first.
const MaxHistory = 5000

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

// --- favorites -------------------------------------------------------------

// Favorites is one person's favorites, most recently added first.
func (s *Store) Favorites(userID string) ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.load(userID)
	if err != nil {
		return nil, err
	}
	out := append([]Entry(nil), c.Favorites...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].AddedAt.After(out[j].AddedAt) })
	return out, nil
}

// AddFavorite adds an item. Adding one already there is not an error.
func (s *Store) AddFavorite(userID string, item media.Item) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.load(userID)
	if err != nil {
		return err
	}
	for _, e := range c.Favorites {
		if sameItem(e, item.SourceID, item.ID) {
			return nil
		}
	}
	if len(c.Favorites) >= MaxFavorites {
		return ErrFull
	}
	c.Favorites = append(c.Favorites, Entry{Item: item, AddedAt: time.Now().UTC()})
	return s.save(userID, c)
}

// RemoveFavorite removes an item. Removing one not there is not an error.
func (s *Store) RemoveFavorite(userID, sourceID, itemID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.load(userID)
	if err != nil {
		return err
	}
	kept := c.Favorites[:0]
	for _, e := range c.Favorites {
		if !sameItem(e, sourceID, itemID) {
			kept = append(kept, e)
		}
	}
	c.Favorites = kept
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

// ImportPlaylist makes a playlist holding items, in their order, each once and
// no more than a playlist holds, in one write. It keeps that order (a custom
// sort): the order was somebody's choice in the player it came from.
func (s *Store) ImportPlaylist(userID, name string, items []media.Item) (Playlist, error) {
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
	p := &Playlist{ID: hex.EncodeToString(raw), Name: name, Items: []Entry{}, CreatedAt: now, UpdatedAt: now, Sort: "custom"}
	seen := map[string]bool{}
	for _, it := range items {
		key := it.SourceID + "/" + it.ID
		if seen[key] || len(p.Items) >= MaxPlaylistItems {
			continue
		}
		seen[key] = true
		p.Items = append(p.Items, Entry{Item: it, AddedAt: now})
	}
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

// SetPlaylistSort chooses how a playlist's songs are ordered. Items keep
// their own order, so choosing "custom" again brings a hand-made order back.
func (s *Store) SetPlaylistSort(userID, id, sort string) error {
	if !PlaylistSorts[sort] {
		return ErrBadSort
	}
	if sort == "title" {
		sort = ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, c, err := s.find(userID, id)
	if err != nil {
		return err
	}
	p.Sort = sort
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
	for _, e := range p.Items {
		if sameItem(e, item.SourceID, item.ID) {
			return 0, ErrAlreadyIn
		}
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
	s.forgetListens(userID)
	os.RemoveAll(filepath.Join(s.dir, "art", userID))
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
			return fmt.Errorf("favorites and playlists for %s do not read: %w", id, err)
		}
	}
	return nil
}

// RecordPlay notes that somebody listened to a song.
func (s *Store) RecordPlay(userID string, item media.Item, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.load(userID)
	if err != nil {
		return err
	}
	if c.History == nil {
		c.History = map[string]*Play{}
	}
	key := item.SourceID + "/" + item.ID
	p, ok := c.History[key]
	if !ok {
		if len(c.History) >= MaxHistory {
			oldest, oldestAt := "", at
			for k, v := range c.History {
				if v.Last.Before(oldestAt) {
					oldest, oldestAt = k, v.Last
				}
			}
			delete(c.History, oldest)
		}
		p = &Play{First: at}
		c.History[key] = p
	}
	p.Item = item
	p.Count++
	p.Last = at
	l := ListenOf(item, at)
	if err := s.logListen(userID, l); err != nil {
		return err
	}
	if c.Scrobbler != nil {
		c.Scrobbler.Pending = append(c.Scrobbler.Pending, l)
		if over := len(c.Scrobbler.Pending) - MaxPending; over > 0 {
			c.Scrobbler.Pending = c.Scrobbler.Pending[over:]
		}
	}
	return s.save(userID, c)
}

// History is one person's plays, in no particular order.
func (s *Store) History(userID string) ([]Play, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.load(userID)
	if err != nil {
		return nil, err
	}
	out := make([]Play, 0, len(c.History))
	for _, p := range c.History {
		out = append(out, *p)
	}
	return out, nil
}

// --- preferences --------------------------------------------------------------

// Prefs are one person's small choices that follow them between devices: the
// order of the pills in each tab, whether read-along lights the sentence being
// read, and how fast audiobooks play.
type Prefs struct {
	Pills map[string][]string `json:"pills,omitempty"`
	// HiddenPills is each row's categories put away with the + button. A row
	// absent here has the app's defaults; present, even empty, it is the
	// person's own choice.
	HiddenPills map[string][]string `json:"hiddenPills,omitempty"`
	// Highlight is nil for the default, which is on.
	Highlight *bool `json:"readAlongHighlight,omitempty"`
	// BookSpeed is 0 for the default, which is normal speed.
	BookSpeed float64 `json:"audiobookSpeed,omitempty"`
	// CoverSpin is whether Now Playing's cover is a spinning disc; nil is the
	// default, a square.
	CoverSpin *bool `json:"coverSpin,omitempty"`
	// CoverStyle is how Now Playing shows the cover: "square", "spin",
	// "vinyl", or one of the visualizers ("pulse" is the orb). Empty falls
	// back to CoverSpin, which came first.
	CoverStyle string `json:"coverStyle,omitempty"`
}

// CoverStyles are the ways Now Playing can show a cover.
var CoverStyles = map[string]bool{"square": true, "spin": true, "vinyl": true, "pulse": true,
	"bars": true, "warp": true, "waves": true, "kaleido": true, "fireworks": true}

// Limits on what a preference can hold, so a client cannot grow the file.
const (
	maxPillRows   = 12
	maxPillsInRow = 16
	maxPillKey    = 32
	MinBookSpeed  = 0.5
	MaxBookSpeed  = 3.5
)

// Prefs is one person's preferences.
func (s *Store) Prefs(userID string) (Prefs, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.load(userID)
	if err != nil {
		return Prefs{}, err
	}
	if c.Prefs == nil {
		return Prefs{}, nil
	}
	return *c.Prefs, nil
}

// PrefsChange is what a client may change; a nil field is left as it is.
type PrefsChange struct {
	Pills       map[string][]string `json:"pills"`
	HiddenPills map[string][]string `json:"hiddenPills"`
	Highlight   *bool               `json:"readAlongHighlight"`
	BookSpeed   *float64            `json:"audiobookSpeed"`
	CoverSpin   *bool               `json:"coverSpin"`
	CoverStyle  *string             `json:"coverStyle"`
}

// ErrBadPrefs is a preference outside what is allowed.
var ErrBadPrefs = errors.New("that preference is not allowed")

// ChangePrefs applies a change to one person's preferences and saves them.
func (s *Store) ChangePrefs(userID string, ch PrefsChange) (Prefs, error) {
	for _, rows := range []map[string][]string{ch.Pills, ch.HiddenPills} {
		if len(rows) > maxPillRows {
			return Prefs{}, ErrBadPrefs
		}
		for row, keys := range rows {
			if row == "" || len(row) > maxPillKey || len(keys) > maxPillsInRow {
				return Prefs{}, ErrBadPrefs
			}
			for _, k := range keys {
				if k == "" || len(k) > maxPillKey {
					return Prefs{}, ErrBadPrefs
				}
			}
		}
	}
	if ch.BookSpeed != nil && (*ch.BookSpeed < MinBookSpeed || *ch.BookSpeed > MaxBookSpeed) {
		return Prefs{}, ErrBadPrefs
	}
	if ch.CoverStyle != nil && !CoverStyles[*ch.CoverStyle] {
		return Prefs{}, ErrBadPrefs
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.load(userID)
	if err != nil {
		return Prefs{}, err
	}
	if c.Prefs == nil {
		c.Prefs = &Prefs{}
	}
	p := c.Prefs
	for row, keys := range ch.Pills {
		if p.Pills == nil {
			p.Pills = map[string][]string{}
		}
		if len(p.Pills) >= maxPillRows && p.Pills[row] == nil {
			return Prefs{}, ErrBadPrefs
		}
		p.Pills[row] = append([]string(nil), keys...)
	}
	for row, keys := range ch.HiddenPills {
		if p.HiddenPills == nil {
			p.HiddenPills = map[string][]string{}
		}
		if len(p.HiddenPills) >= maxPillRows && p.HiddenPills[row] == nil {
			return Prefs{}, ErrBadPrefs
		}
		// Never nil: an empty list is a choice (nothing put away), where a
		// missing one means the defaults - which put Genres away.
		p.HiddenPills[row] = append(make([]string, 0, len(keys)), keys...)
	}
	if ch.Highlight != nil {
		on := *ch.Highlight
		p.Highlight = &on
	}
	if ch.CoverSpin != nil {
		on := *ch.CoverSpin
		p.CoverSpin = &on
	}
	if ch.CoverStyle != nil {
		p.CoverStyle = *ch.CoverStyle
	}
	if ch.BookSpeed != nil {
		p.BookSpeed = *ch.BookSpeed
	}
	if err := s.save(userID, c); err != nil {
		return Prefs{}, err
	}
	return *p, nil
}

// Ordered is a playlist's entries in the order its sort shows them, each with
// its place in Items - what removing and moving go by.
func (p Playlist) Ordered() []OrderedEntry {
	out := make([]OrderedEntry, len(p.Items))
	for i, e := range p.Items {
		out[i] = OrderedEntry{Entry: e, Position: i}
	}
	low := func(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
	artist := func(e Entry) string {
		if len(e.Item.Creators) > 0 {
			return low(e.Item.Creators[0])
		}
		return ""
	}
	switch p.Sort {
	case "custom":
	case "added":
		sort.SliceStable(out, func(i, j int) bool { return out[i].AddedAt.After(out[j].AddedAt) })
	case "artist":
		sort.SliceStable(out, func(i, j int) bool {
			a, b := out[i].Entry, out[j].Entry
			if artist(a) != artist(b) {
				return artist(a) < artist(b)
			}
			if low(a.Item.Extra["album"]) != low(b.Item.Extra["album"]) {
				return low(a.Item.Extra["album"]) < low(b.Item.Extra["album"])
			}
			return low(a.Item.Title) < low(b.Item.Title)
		})
	default:
		sort.SliceStable(out, func(i, j int) bool {
			a, b := out[i].Entry, out[j].Entry
			if low(a.Item.Title) != low(b.Item.Title) {
				return low(a.Item.Title) < low(b.Item.Title)
			}
			return artist(a) < artist(b)
		})
	}
	return out
}

// OrderedEntry is an entry and its place in its playlist's Items.
type OrderedEntry struct {
	Entry
	Position int
}
