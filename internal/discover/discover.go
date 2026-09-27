// Package discover finds out about an artist from open music services: who
// MusicBrainz says they are, who ListenBrainz says they sound like, and what
// Wikipedia says about them. No account and no key - all three are run for
// anyone to ask - and it is only ever asked when the owner has turned music
// discovery on (it sends artist names out of the house, like online lyrics).
//
// Every answer is kept on disk, "nothing found" included, so an artist is
// asked about once and not again for months. MusicBrainz asks for no more
// than one request a second from a client and a User-Agent saying who it is;
// both are kept to.
package discover

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
	"strings"
	"sync"
	"time"
)

// The services, overridable for tests.
const (
	DefaultMusicBrainz  = "https://musicbrainz.org"
	DefaultListenBrainz = "https://labs.api.listenbrainz.org"
	DefaultWikidata     = "https://www.wikidata.org"
	DefaultWikipedia    = "https://en.wikipedia.org"
)

// similarAlgorithm is ListenBrainz's similar-artists dataset: artists people
// listen to in the same sessions, over twenty years of listens.
const similarAlgorithm = "session_based_days_7500_session_300_contribution_5_threshold_10_limit_100_filter_True_skip_30"

const (
	foundFor   = 180 * 24 * time.Hour
	noneFor    = 30 * 24 * time.Hour
	maxSimilar = 50
	userAgent  = "SoundStorm (https://github.com/GabrielHollberg/soundstorm)"
)

// Artist is another artist, by name and MusicBrainz id.
type Artist struct {
	Name string `json:"name"`
	MBID string `json:"mbid"`
}

// Info is what is known about an artist.
type Info struct {
	MBID    string   `json:"mbid,omitempty"`
	Similar []Artist `json:"similar,omitempty"`
	Bio     string   `json:"bio,omitempty"`
	BioURL  string   `json:"bioUrl,omitempty"`
}

// Finder asks the services and keeps their answers.
type Finder struct {
	MusicBrainz, ListenBrainz, Wikidata, Wikipedia string
	CacheDir                                       string
	Client                                         *http.Client
	// MBGap is the least time between two MusicBrainz requests.
	MBGap time.Duration

	mu     sync.Mutex
	lastMB time.Time
}

// New makes a Finder keeping its answers in dir.
func New(dir string) (*Finder, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Finder{
		MusicBrainz: DefaultMusicBrainz, ListenBrainz: DefaultListenBrainz,
		Wikidata: DefaultWikidata, Wikipedia: DefaultWikipedia,
		CacheDir: dir, Client: &http.Client{Timeout: 10 * time.Second}, MBGap: 1100 * time.Millisecond,
	}, nil
}

type cached struct {
	Found bool      `json:"found"`
	At    time.Time `json:"at"`
	Info  Info      `json:"info"`
}

func (f *Finder) path(name string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(name))))
	return filepath.Join(f.CacheDir, hex.EncodeToString(sum[:16])+".json")
}

// Cached is what is already known about an artist, without asking anyone.
func (f *Finder) Cached(name string) (Info, bool) {
	data, err := os.ReadFile(f.path(name))
	if err != nil {
		return Info{}, false
	}
	var c cached
	if json.Unmarshal(data, &c) != nil || !c.Found || time.Since(c.At) > foundFor {
		return Info{}, false
	}
	return c.Info, true
}

// Artist is what is known about an artist, from the cache or the services.
// found is false when MusicBrainz does not know them; an error means a
// service could not be asked, and nothing is kept.
func (f *Finder) Artist(ctx context.Context, name string) (Info, bool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Info{}, false, nil
	}
	if data, err := os.ReadFile(f.path(name)); err == nil {
		var c cached
		if json.Unmarshal(data, &c) == nil {
			limit := noneFor
			if c.Found {
				limit = foundFor
			}
			if time.Since(c.At) < limit {
				return c.Info, c.Found, nil
			}
		}
	}
	info, found, err := f.ask(ctx, name)
	if err != nil {
		return Info{}, false, err
	}
	if data, err := json.Marshal(cached{Found: found, At: time.Now().UTC(), Info: info}); err == nil {
		tmp := f.path(name) + ".tmp"
		if os.WriteFile(tmp, data, 0o600) == nil {
			_ = os.Rename(tmp, f.path(name))
		}
	}
	return info, found, nil
}

func (f *Finder) ask(ctx context.Context, name string) (Info, bool, error) {
	mbid, err := f.lookUp(ctx, name)
	if err != nil || mbid == "" {
		return Info{}, false, err
	}
	info := Info{MBID: mbid}
	if info.Similar, err = f.similar(ctx, mbid); err != nil {
		return Info{}, false, err
	}
	if info.Bio, info.BioURL, err = f.bio(ctx, mbid); err != nil {
		return Info{}, false, err
	}
	return info, true, nil
}

// get fetches JSON. A 404 is found=false, not an error.
func (f *Finder) get(ctx context.Context, rawURL string, out any) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := f.Client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("%s answered %d", req.URL.Host, resp.StatusCode)
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 4<<20)).Decode(out); err != nil {
		return false, errors.New(req.URL.Host + ": unreadable answer")
	}
	return true, nil
}

// musicBrainz waits its turn: one request a second, as MusicBrainz asks.
func (f *Finder) musicBrainz(ctx context.Context, rawURL string, out any) (bool, error) {
	f.mu.Lock()
	if wait := f.MBGap - time.Since(f.lastMB); wait > 0 {
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			f.mu.Unlock()
			return false, ctx.Err()
		}
	}
	f.lastMB = time.Now()
	f.mu.Unlock()
	return f.get(ctx, rawURL, out)
}

// lookUp finds the artist's MusicBrainz id: a confident match on the name,
// or none - a wrong artist's bio is worse than no bio.
func (f *Finder) lookUp(ctx context.Context, name string) (string, error) {
	q := url.Values{"query": {`artist:"` + strings.ReplaceAll(name, `"`, ``) + `"`}, "fmt": {"json"}, "limit": {"5"}}
	var r struct {
		Artists []struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Score int    `json:"score"`
		} `json:"artists"`
	}
	if _, err := f.musicBrainz(ctx, f.MusicBrainz+"/ws/2/artist/?"+q.Encode(), &r); err != nil {
		return "", err
	}
	for _, a := range r.Artists {
		if a.Score >= 90 && strings.EqualFold(a.Name, name) {
			return a.ID, nil
		}
	}
	return "", nil
}

func (f *Finder) similar(ctx context.Context, mbid string) ([]Artist, error) {
	q := url.Values{"artist_mbids": {mbid}, "algorithm": {similarAlgorithm}}
	var r []struct {
		Name string `json:"name"`
		MBID string `json:"artist_mbid"`
	}
	if _, err := f.get(ctx, f.ListenBrainz+"/similar-artists/json?"+q.Encode(), &r); err != nil {
		return nil, err
	}
	var out []Artist
	for _, a := range r {
		if a.MBID != mbid && a.Name != "" {
			out = append(out, Artist{Name: a.Name, MBID: a.MBID})
		}
		if len(out) == maxSimilar {
			break
		}
	}
	return out, nil
}

// bio is the Wikipedia summary MusicBrainz points to, by way of Wikidata.
func (f *Finder) bio(ctx context.Context, mbid string) (string, string, error) {
	var rels struct {
		Relations []struct {
			Type string `json:"type"`
			URL  struct {
				Resource string `json:"resource"`
			} `json:"url"`
		} `json:"relations"`
	}
	if _, err := f.musicBrainz(ctx, f.MusicBrainz+"/ws/2/artist/"+url.PathEscape(mbid)+"?inc=url-rels&fmt=json", &rels); err != nil {
		return "", "", err
	}
	var entity string
	for _, rel := range rels.Relations {
		if rel.Type == "wikidata" {
			entity = rel.URL.Resource[strings.LastIndex(rel.URL.Resource, "/")+1:]
		}
	}
	if entity == "" || !strings.HasPrefix(entity, "Q") {
		return "", "", nil
	}
	var wd struct {
		Entities map[string]struct {
			Sitelinks map[string]struct {
				Title string `json:"title"`
			} `json:"sitelinks"`
		} `json:"entities"`
	}
	if _, err := f.get(ctx, f.Wikidata+"/wiki/Special:EntityData/"+url.PathEscape(entity)+".json", &wd); err != nil {
		return "", "", err
	}
	title := ""
	for _, e := range wd.Entities {
		title = e.Sitelinks["enwiki"].Title
	}
	if title == "" {
		return "", "", nil
	}
	var sum struct {
		Extract     string `json:"extract"`
		ContentURLs struct {
			Desktop struct {
				Page string `json:"page"`
			} `json:"desktop"`
		} `json:"content_urls"`
	}
	found, err := f.get(ctx, f.Wikipedia+"/api/rest_v1/page/summary/"+url.PathEscape(strings.ReplaceAll(title, " ", "_")), &sum)
	if err != nil || !found {
		return "", "", err
	}
	return strings.TrimSpace(sum.Extract), sum.ContentURLs.Desktop.Page, nil
}
