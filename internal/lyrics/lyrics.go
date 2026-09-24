// Package lyrics finds lyrics a song's own files do not have, on LRCLIB.
//
// LRCLIB (lrclib.net) is a free, open database of synced lyrics that needs no
// account and no key, which is why it was chosen over the alternatives:
// Musixmatch needs a paid licence to show whole lyrics, Genius's API has no
// lyrics in it, and the Chinese streaming services' APIs are unofficial. See
// "Lyrics from LRCLIB" in CLAUDE.md.
//
// It is only ever asked when the owner has turned it on, only for a song with
// no lyrics of its own, and only when that song is played - never in bulk.
// Every answer, "none" included, is kept on disk, so a song is asked about
// once.
package lyrics

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/source"
)

// DefaultBaseURL is LRCLIB's API.
const DefaultBaseURL = "https://lrclib.net"

// A song LRCLIB has nothing for is asked about again after this long, in
// case somebody has added it since.
const noneFor = 30 * 24 * time.Hour

// Song is what LRCLIB matches on. Duration matters: it is what picks the
// album version over a live cut whose timings would be wrong.
type Song struct {
	Key      string // SoundStorm's own id for the song, for the cache
	Artist   string
	Title    string
	Album    string
	Duration float64 // seconds
}

// Finder looks lyrics up and remembers the answers.
type Finder struct {
	BaseURL  string
	CacheDir string
	Client   *http.Client
}

// New is a Finder for LRCLIB, caching under dir.
func New(dir string) (*Finder, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Finder{BaseURL: DefaultBaseURL, CacheDir: dir, Client: &http.Client{Timeout: 8 * time.Second}}, nil
}

type cached struct {
	Found  bool          `json:"found"`
	At     time.Time     `json:"at"`
	Lyrics source.Lyrics `json:"lyrics"`
}

func (f *Finder) cachePath(key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(f.CacheDir, hex.EncodeToString(sum[:16])+".json")
}

// Find returns a song's lyrics from the cache or LRCLIB. found is false when
// LRCLIB has none; an error means it could not be asked, and is not cached.
func (f *Finder) Find(ctx context.Context, song Song) (source.Lyrics, bool, error) {
	path := f.cachePath(song.Key)
	if data, err := os.ReadFile(path); err == nil {
		var c cached
		if json.Unmarshal(data, &c) == nil && (c.Found || time.Since(c.At) < noneFor) {
			return c.Lyrics, c.Found, nil
		}
	}
	lyrics, found, err := f.ask(ctx, song)
	if err != nil {
		return source.Lyrics{}, false, err
	}
	if data, err := json.Marshal(cached{Found: found, At: time.Now().UTC(), Lyrics: lyrics}); err == nil {
		tmp := path + ".tmp"
		if os.WriteFile(tmp, data, 0o600) == nil {
			_ = os.Rename(tmp, path)
		}
	}
	return lyrics, found, nil
}

func (f *Finder) ask(ctx context.Context, song Song) (source.Lyrics, bool, error) {
	if song.Title == "" || song.Artist == "" {
		return source.Lyrics{}, false, nil
	}
	q := url.Values{"track_name": {song.Title}, "artist_name": {song.Artist}}
	if song.Album != "" {
		q.Set("album_name", song.Album)
	}
	if song.Duration > 0 {
		q.Set("duration", strconv.Itoa(int(song.Duration+0.5)))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(f.BaseURL, "/")+"/api/get?"+q.Encode(), nil)
	if err != nil {
		return source.Lyrics{}, false, err
	}
	// LRCLIB asks clients to say who they are.
	req.Header.Set("User-Agent", "SoundStorm (https://github.com/GabrielHollberg/soundstorm)")
	resp, err := f.Client.Do(req)
	if err != nil {
		return source.Lyrics{}, false, fmt.Errorf("lrclib: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return source.Lyrics{}, false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return source.Lyrics{}, false, fmt.Errorf("lrclib: status %d", resp.StatusCode)
	}
	var body struct {
		Instrumental bool   `json:"instrumental"`
		PlainLyrics  string `json:"plainLyrics"`
		SyncedLyrics string `json:"syncedLyrics"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 1<<20)).Decode(&body); err != nil {
		return source.Lyrics{}, false, errors.New("lrclib: unreadable answer")
	}
	if body.Instrumental {
		return source.Lyrics{}, false, nil
	}
	if l := ParseLRC(body.SyncedLyrics); len(l.Lines) > 0 {
		return l, true, nil
	}
	if strings.TrimSpace(body.PlainLyrics) != "" {
		var lines []source.LyricLine
		for _, text := range strings.Split(strings.ReplaceAll(body.PlainLyrics, "\r", ""), "\n") {
			lines = append(lines, source.LyricLine{Start: -1, Text: text})
		}
		return source.Lyrics{Lines: lines}, true, nil
	}
	return source.Lyrics{}, false, nil
}

// A time tag, [mm:ss], [mm:ss.xx] or [mm:ss.xxx]. A line may carry several,
// for a chorus sung more than once.
var lrcTag = regexp.MustCompile(`\[(\d{1,3}):(\d{2})(?:[.:](\d{1,3}))?\]`)

// ParseLRC reads the .lrc format - each line's words after the times they are
// sung - into synced lines in order.
func ParseLRC(text string) source.Lyrics {
	var lines []source.LyricLine
	for _, raw := range strings.Split(strings.ReplaceAll(text, "\r", ""), "\n") {
		tags := lrcTag.FindAllStringSubmatchIndex(raw, -1)
		if len(tags) == 0 {
			continue // [ar:...] and other metadata, or a blank line
		}
		words := strings.TrimSpace(raw[tags[len(tags)-1][1]:])
		for _, t := range tags {
			min, _ := strconv.Atoi(raw[t[2]:t[3]])
			sec, _ := strconv.Atoi(raw[t[4]:t[5]])
			ms := 0
			if t[6] >= 0 {
				frac := raw[t[6]:t[7]]
				ms, _ = strconv.Atoi((frac + "00")[:3])
			}
			lines = append(lines, source.LyricLine{Start: (min*60+sec)*1000 + ms, Text: words})
		}
	}
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].Start < lines[j].Start })
	return source.Lyrics{Synced: len(lines) > 0, Lines: lines}
}
