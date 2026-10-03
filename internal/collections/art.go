package collections

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Covers of one's own: somebody picks an image to show instead of a song's
// or an album's cover, for their account alone. The image is kept under
// art/<account>/, named by its content, so the same picture used for a whole
// album is stored once; the collection maps what it replaces to that name.

// Limits, so a client cannot fill the disk with pictures.
const (
	MaxArtBytes = 4 << 20 // one image; the app sends a shrunk JPEG far smaller
	MaxArtKeys  = 5000    // covers replaced
	MaxArtFiles = 1000    // distinct pictures
	// MaxArtTotal is what one person's pictures may take between them, so
	// 1,000 pictures of 4MB cannot fill the state disk the media servers'
	// databases share (a review).
	MaxArtTotal = 300 << 20
	maxArtKey   = 400
)

// ErrBadArt is an upload that is not a picture SoundStorm will show.
var ErrBadArt = errors.New("that is not a JPEG, PNG or WebP picture")

// artKey is what a key may look like: which kind, a source, and an id - or
// one of this person's playlists, whose picture replaces its collage.
var artKey = regexp.MustCompile(`^(?:(song|art):[A-Za-z0-9_-]{1,64}/[^\x00-\x1f]{1,300}|playlist:[0-9a-f]{1,64}|me)$`)

// PlaylistArtKey is the key a playlist's own picture is kept under.
func PlaylistArtKey(id string) string { return "playlist:" + id }

// ProfileArtKey is the key a person's own picture - the one in their circle
// at the top of the screen and on "Who's listening?" - is kept under.
const ProfileArtKey = "me"

// ProfilePicture is where userID's own picture is on disk and its file name
// (which changes with the picture), or false without one.
func (s *Store) ProfilePicture(userID string) (path, name string, ok bool) {
	art, err := s.Art(userID)
	if err != nil || art[ProfileArtKey] == "" {
		return "", "", false
	}
	dir, err := s.artDir(userID)
	if err != nil {
		return "", "", false
	}
	return filepath.Join(dir, art[ProfileArtKey]), art[ProfileArtKey], true
}

// artFile is what a stored file's name may look like.
var artFile = regexp.MustCompile(`^[0-9a-f]{64}\.(jpg|png|webp)$`)

// imageExt names data's format from its first bytes, or "" if it is not one
// of the three a browser shows everywhere. SVG is refused: it can script.
func imageExt(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}):
		return "jpg"
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return "png"
	case len(data) > 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "webp"
	}
	return ""
}

func (s *Store) artDir(userID string) (string, error) {
	if _, err := s.path(userID); err != nil {
		return "", err
	}
	return filepath.Join(s.dir, "art", userID), nil
}

// SetArt shows data instead of the covers keys name, for userID.
func (s *Store) SetArt(userID string, keys []string, data []byte) error {
	if len(keys) == 0 || len(keys) > 500 {
		return fmt.Errorf("collections: name between 1 and 500 covers")
	}
	for _, k := range keys {
		if len(k) > maxArtKey || !artKey.MatchString(k) {
			return fmt.Errorf("collections: bad cover key")
		}
	}
	if len(data) == 0 || len(data) > MaxArtBytes {
		return ErrBadArt
	}
	ext := imageExt(data)
	if ext == "" {
		return ErrBadArt
	}
	sum := sha256.Sum256(data)
	name := hex.EncodeToString(sum[:]) + "." + ext

	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.load(userID)
	if err != nil {
		return err
	}
	if c.Art == nil {
		c.Art = map[string]string{}
	}
	added := 0
	for _, k := range keys {
		if _, ok := c.Art[k]; !ok {
			added++
		}
	}
	if len(c.Art)+added > MaxArtKeys {
		return ErrFull
	}
	files := map[string]bool{name: true}
	for _, f := range c.Art {
		files[f] = true
	}
	if len(files) > MaxArtFiles {
		return ErrFull
	}
	dir, err := s.artDir(userID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	dest := filepath.Join(dir, name)
	if _, err := os.Stat(dest); err != nil {
		if artDirBytes(dir)+int64(len(data)) > MaxArtTotal {
			return ErrFull
		}
		tmp := dest + ".tmp"
		if err := os.WriteFile(tmp, data, 0o600); err != nil {
			return err
		}
		if err := os.Rename(tmp, dest); err != nil {
			os.Remove(tmp)
			return err
		}
	}
	for _, k := range keys {
		c.Art[k] = name
	}
	if err := s.save(userID, c); err != nil {
		return err
	}
	s.sweepArt(userID, c)
	return nil
}

// RemoveArt puts the original covers back for keys.
func (s *Store) RemoveArt(userID string, keys []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.load(userID)
	if err != nil {
		return err
	}
	for _, k := range keys {
		delete(c.Art, k)
	}
	if err := s.save(userID, c); err != nil {
		return err
	}
	s.sweepArt(userID, c)
	return nil
}

// Art is userID's covers: key to file name.
func (s *Store) Art(userID string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.load(userID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(c.Art))
	for k, v := range c.Art {
		out[k] = v
	}
	return out, nil
}

// ArtPath is where one of userID's own pictures is on disk, or false if the
// name is not one of theirs.
func (s *Store) ArtPath(userID, name string) (string, bool) {
	if !artFile.MatchString(name) {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.load(userID)
	if err != nil {
		return "", false
	}
	for _, f := range c.Art {
		if f == name {
			dir, err := s.artDir(userID)
			if err != nil {
				return "", false
			}
			return filepath.Join(dir, name), true
		}
	}
	return "", false
}

// sweepArt removes pictures nothing uses any more. Callers hold s.mu.
func (s *Store) sweepArt(userID string, c *collection) {
	dir, err := s.artDir(userID)
	if err != nil {
		return
	}
	used := map[string]bool{}
	for _, f := range c.Art {
		used[f] = true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !used[e.Name()] && !strings.HasSuffix(e.Name(), ".tmp") {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// artDirBytes is the size of the pictures in dir.
func artDirBytes(dir string) int64 {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	var n int64
	for _, e := range entries {
		if info, err := e.Info(); err == nil {
			n += info.Size()
		}
	}
	return n
}
