package collections

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/media"
)

// Listens: every play, when it happened, for the year recap and for
// scrobbling. History (above) keeps a count per song, which says what is
// played most but not when, and a year in music is about when.
//
// One file per person per year, appended a line at a time - so recording a
// play costs one small write, never a rewrite, and a year is one file to
// read. Each line carries what the song was, so a recap needs nothing from
// the backends and a song deleted since still counts. About 150 bytes a play:
// a heavy listener's year is a few megabytes. Not in the backup, which
// carries the collections themselves; a move between computers carries the
// whole state folder and these with it.

// Listen is one play.
type Listen struct {
	At       time.Time `json:"at"`
	SourceID string    `json:"s"`
	ID       string    `json:"id"`
	Title    string    `json:"t"`
	Artist   string    `json:"a,omitempty"`
	Album    string    `json:"al,omitempty"`
	ArtID    string    `json:"art,omitempty"`
	Seconds  float64   `json:"d,omitempty"`
	Genre    string    `json:"g,omitempty"`
}

// ListenOf is a play of item at a moment.
func ListenOf(item media.Item, at time.Time) Listen {
	l := Listen{At: at.UTC(), SourceID: item.SourceID, ID: item.ID, Title: item.Title,
		Album: item.Extra["album"], ArtID: item.ArtID, Seconds: item.DurationSeconds, Genre: item.Extra["genre"]}
	if len(item.Creators) > 0 {
		l.Artist = item.Creators[0]
	}
	return l
}

func (s *Store) listenPath(userID string, year int) (string, error) {
	if _, err := s.path(userID); err != nil {
		return "", err
	}
	return filepath.Join(s.dir, "listens", userID+"-"+strconv.Itoa(year)+".jsonl"), nil
}

// logListen appends one play. Callers hold s.mu.
func (s *Store) logListen(userID string, l Listen) error {
	p, err := s.listenPath(userID, l.At.Year())
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	line, err := json.Marshal(l)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// maxListenLine bounds one line; a line past it is damage, and skipped.
const maxListenLine = 64 << 10

// Listens is one person's plays in a year, oldest first. A damaged line - a
// crash mid-write leaves at most the last one short - is skipped, not fatal.
func (s *Store) Listens(userID string, year int) ([]Listen, error) {
	p, err := s.listenPath(userID, year)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Listen
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 4096), maxListenLine)
	for sc.Scan() {
		var l Listen
		if json.Unmarshal(sc.Bytes(), &l) == nil && !l.At.IsZero() && l.ID != "" {
			out = append(out, l)
		}
	}
	if err := sc.Err(); err != nil && !errors.Is(err, bufio.ErrTooLong) {
		return out, fmt.Errorf("collections: read listens: %w", err)
	}
	return out, nil
}

// ListenYears is the years somebody has listens for, newest first.
func (s *Store) ListenYears(userID string) []int {
	var years []int
	now := time.Now().Year()
	for y := now; y >= now-30; y-- {
		if p, err := s.listenPath(userID, y); err == nil {
			if _, err := os.Stat(p); err == nil {
				years = append(years, y)
			}
		}
	}
	return years
}

// forgetListens removes all of somebody's listens. Callers hold s.mu.
func (s *Store) forgetListens(userID string) {
	matches, _ := filepath.Glob(filepath.Join(s.dir, "listens", userID+"-*.jsonl"))
	for _, m := range matches {
		_ = os.Remove(m)
	}
}

// --- scrobbling ---------------------------------------------------------------

// Scrobbler is where somebody's plays are also sent, and what is waiting to
// go. Per person, like everything here: each person connects their own
// account, and the token is theirs.
type Scrobbler struct {
	Service  string   `json:"service"` // "listenbrainz"
	Token    string   `json:"token"`
	UserName string   `json:"userName,omitempty"`
	Pending  []Listen `json:"pending,omitempty"`
	// Problem is set when the service refused the token; sending stops
	// until they connect again.
	Problem string `json:"problem,omitempty"`
}

// MaxPending bounds what waits to be sent while a service is unreachable -
// about a week of heavy listening. Past it the oldest go first.
const MaxPending = 1000

// ScrobblerFor is somebody's connected service, if any.
func (s *Store) ScrobblerFor(userID string) (Scrobbler, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.load(userID)
	if err != nil || c.Scrobbler == nil {
		return Scrobbler{}, false, err
	}
	sc := *c.Scrobbler
	sc.Pending = append([]Listen(nil), c.Scrobbler.Pending...)
	return sc, true, nil
}

// SetScrobbler connects a service, or with nil disconnects it.
func (s *Store) SetScrobbler(userID string, sc *Scrobbler) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.load(userID)
	if err != nil {
		return err
	}
	c.Scrobbler = sc
	return s.save(userID, c)
}

// Sent takes listens off the queue once they have gone. Matched by song and
// moment, so plays queued (or old ones dropped) meanwhile are left alone.
func (s *Store) Sent(userID string, sent []Listen) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.load(userID)
	if err != nil || c.Scrobbler == nil {
		return err
	}
	gone := map[string]bool{}
	for _, l := range sent {
		gone[l.SourceID+"/"+l.ID+"@"+l.At.Format(time.RFC3339Nano)] = true
	}
	kept := c.Scrobbler.Pending[:0:0]
	for _, l := range c.Scrobbler.Pending {
		if !gone[l.SourceID+"/"+l.ID+"@"+l.At.Format(time.RFC3339Nano)] {
			kept = append(kept, l)
		}
	}
	c.Scrobbler.Pending = kept
	return s.save(userID, c)
}
