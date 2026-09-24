// Package subsonic adapts a Subsonic-API music server, which for SoundStorm
// means Navidrome.
//
// Navidrome is here rather than letting Jellyfin handle music because it is
// simply better at it: multi-value artist tags, album-artist vs artist,
// compilations, ReplayGain, smart playlists, and a scanner that handles a large
// library without complaint. SoundStorm exists so you can have that without
// also having a second app to log into.
//
// Protocol notes: authentication is the salted-token scheme from Subsonic
// 1.13.0 - send the username, a random salt, and token=md5(password+salt). The
// plaintext password never crosses the wire. The server must store the password
// recoverably for this to work, which Navidrome does on purpose.
package subsonic

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/httpx"
	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/source"
)

const (
	apiVersion = "1.16.1"
	clientName = "soundstorm"
)

// Config configures a Subsonic source.
type Config struct {
	ID       string
	BaseURL  string
	Username string
	Password string
	Timeout  time.Duration
}

// Source is a Subsonic-API music server.
type Source struct {
	id    string
	cfg   Config
	http  *httpx.Client
	shelf media.ShelfCache
}

// New builds a Subsonic source.
func New(cfg Config) (*Source, error) {
	if cfg.Username == "" || cfg.Password == "" {
		return nil, fmt.Errorf("subsonic %q: username and password are required", cfg.ID)
	}
	c, err := httpx.New(cfg.BaseURL, cfg.Timeout)
	if err != nil {
		return nil, fmt.Errorf("subsonic %q: %w", cfg.ID, err)
	}
	return &Source{id: cfg.ID, cfg: cfg, http: c}, nil
}

func (s *Source) ID() string       { return s.id }
func (s *Source) Kind() media.Kind { return media.KindMusic }

// auth returns the per-request authentication parameters.
func (s *Source) auth() (url.Values, error) {
	saltBytes := make([]byte, 8)
	if _, err := rand.Read(saltBytes); err != nil {
		return nil, fmt.Errorf("generate salt: %w", err)
	}
	salt := hex.EncodeToString(saltBytes)
	sum := md5.Sum([]byte(s.cfg.Password + salt))

	return url.Values{
		"u": {s.cfg.Username},
		"t": {hex.EncodeToString(sum[:])},
		"s": {salt},
		"v": {apiVersion},
		"c": {clientName},
		"f": {"json"},
	}, nil
}

// envelope is the outer shape every Subsonic response shares.
type envelope struct {
	Response struct {
		Status  string `json:"status"`
		Version string `json:"version"`
		Error   *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		SearchResult3 struct {
			Song   []song        `json:"song"`
			Album  []albumEntry  `json:"album"`
			Artist []artistEntry `json:"artist"`
		} `json:"searchResult3"`
		Song       *song `json:"song"`
		AlbumList2 struct {
			Album []albumEntry `json:"album"`
		} `json:"albumList2"`
		Album   *albumEntry `json:"album"`
		Artists struct {
			Index []struct {
				Artist []artistEntry `json:"artist"`
			} `json:"index"`
		} `json:"artists"`
		Artist *artistEntry `json:"artist"`
	} `json:"subsonic-response"`
}

type song struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Album    string `json:"album"`
	Artist   string `json:"artist"`
	Year     int    `json:"year"`
	Duration int    `json:"duration"` // seconds
	CoverArt string `json:"coverArt"`
	Suffix   string `json:"suffix"`
	// Path is relative to the music folder - checked against Navidrome
	// 0.64: "Artist/Album/01 - Title.mp3".
	Path string `json:"path"`
}

// check turns a Subsonic envelope into an error when the server reported one.
// Subsonic signals failure with HTTP 200 and an error object, so the status
// code alone is not enough.
func (e *envelope) check() error {
	if err := e.Response.Error; err != nil {
		return fmt.Errorf("subsonic error %d: %s", err.Code, err.Message)
	}
	if e.Response.Status != "ok" {
		return fmt.Errorf("subsonic status %q", e.Response.Status)
	}
	return nil
}

// Search browses or searches the music library.
//
// The whole matching set is fetched, not the first N, because search3 returns
// songs in an order of its own - neither by title nor by any relevance
// SoundStorm can reproduce - and merged paging needs the first N in
// SoundStorm's order (see media.Less). Measured before this: of the first 50
// songs by title in a 4,413-song library, Navidrome's first 50 held none, so
// every scroll repeated some songs and skipped others. A browse is then
// ordered and cut here; a search is returned whole for the merge to rank.
// Listings are cached briefly so scrolling costs one fetch.
func (s *Source) Search(ctx context.Context, q media.Query) ([]media.Item, error) {
	all, err := s.shelf.GetOrFetch(q.Text, func() ([]media.Item, error) { return s.fetchAll(ctx, q.Text) })
	if err != nil {
		return nil, err
	}
	if q.Text == "" {
		return media.FirstN(all, q.LimitOr(25)), nil
	}
	return all, nil
}

// fetchPage is how many songs one search3 call asks for.
const fetchPage = 1000

// maxSongs bounds one listing. Far past any household's library, and short
// of letting a runaway answer eat the server's memory.
const maxSongs = 200_000

// fetchAll pages through every song matching text.
func (s *Source) fetchAll(ctx context.Context, text string) ([]media.Item, error) {
	var items []media.Item
	for offset := 0; offset < maxSongs; offset += fetchPage {
		page, err := s.fetchPage(ctx, text, offset)
		if err != nil {
			return nil, err
		}
		items = append(items, page...)
		if len(page) < fetchPage {
			break
		}
	}
	return items, nil
}

func (s *Source) fetchPage(ctx context.Context, text string, offset int) ([]media.Item, error) {
	params, err := s.auth()
	if err != nil {
		return nil, err
	}
	params.Set("query", text)
	params.Set("songCount", strconv.Itoa(fetchPage))
	params.Set("songOffset", strconv.Itoa(offset))
	params.Set("albumCount", "0")
	params.Set("artistCount", "0")

	var env envelope
	if err := s.http.JSON(ctx, "/rest/search3.view", params, &env); err != nil {
		return nil, err
	}
	if err := env.check(); err != nil {
		return nil, err
	}

	items := make([]media.Item, 0, len(env.Response.SearchResult3.Song))
	for _, sg := range env.Response.SearchResult3.Song {
		items = append(items, s.songItem(sg))
	}
	return items, nil
}

// songItem is one Navidrome song as SoundStorm shows it.
func (s *Source) songItem(sg song) media.Item {
	item := media.Item{
		ID:              sg.ID,
		SourceID:        s.id,
		Kind:            media.KindMusic,
		Title:           sg.Title,
		Subtitle:        sg.Album,
		Year:            sg.Year,
		ArtID:           sg.CoverArt,
		DurationSeconds: float64(sg.Duration),
		Extra:           map[string]string{},
	}
	if sg.Artist != "" {
		item.Creators = []string{sg.Artist}
	}
	if sg.Album != "" {
		item.Extra["album"] = sg.Album
	}
	if sg.Suffix != "" {
		item.Extra["format"] = sg.Suffix
	}
	return item
}

// StreamTarget builds an authenticated upstream target for a track.
//
// Subsonic carries credentials in the query string, which is how the protocol
// works, so no headers are needed. They never reach the browser: SoundStorm
// fetches them itself and pipes the bytes through, so Navidrome needs no
// published port.
func (s *Source) StreamTarget(_ context.Context, itemID string) (source.Target, error) {
	return s.mediaTarget("/rest/stream.view", itemID)
}

// ArtTarget builds an authenticated upstream target for cover art.
func (s *Source) ArtTarget(_ context.Context, artID string) (source.Target, error) {
	return s.mediaTarget("/rest/getCoverArt.view", artID)
}

func (s *Source) mediaTarget(path, id string) (source.Target, error) {
	if id == "" {
		return source.Target{}, fmt.Errorf("subsonic %q: empty id", s.id)
	}
	params, err := s.auth()
	if err != nil {
		return source.Target{}, err
	}
	params.Set("id", id)
	return source.Target{URL: s.http.URL(path, params)}, nil
}

// Rescan asks Navidrome to look at the music folder now.
//
// Verified against 0.64.0: /rest/startScan answers with a scanStatus saying
// scanning is true, and the scan it starts is the quick kind - it looks at
// what changed rather than re-reading every tag, which is what makes it cheap
// enough to fire after an upload.
func (s *Source) Rescan(ctx context.Context) error {
	s.shelf.Clear()
	params, err := s.auth()
	if err != nil {
		return err
	}
	var env envelope
	if err := s.http.JSON(ctx, "/rest/startScan.view", params, &env); err != nil {
		return fmt.Errorf("subsonic %q: start scan: %w", s.id, err)
	}
	return env.check()
}

func (s *Source) Health(ctx context.Context) error {
	params, err := s.auth()
	if err != nil {
		return err
	}
	var env envelope
	if err := s.http.JSON(ctx, "/rest/ping.view", params, &env); err != nil {
		return err
	}
	return env.check()
}

// ItemFiles is the track's file, relative to the music folder, which is how
// Navidrome reports it.
func (s *Source) ItemFiles(ctx context.Context, itemID string) ([]string, error) {
	if itemID == "" {
		return nil, fmt.Errorf("subsonic %q: empty id", s.id)
	}
	params, err := s.auth()
	if err != nil {
		return nil, err
	}
	params.Set("id", itemID)
	var env envelope
	if err := s.http.JSON(ctx, "/rest/getSong.view", params, &env); err != nil {
		return nil, fmt.Errorf("subsonic %q: get song: %w", s.id, err)
	}
	if err := env.check(); err != nil {
		return nil, err
	}
	if env.Response.Song == nil || env.Response.Song.Path == "" {
		return nil, fmt.Errorf("subsonic %q: no file for %q", s.id, itemID)
	}
	return []string{env.Response.Song.Path}, nil
}

// ItemByID describes one song, for a favourite or a playlist entry, which know
// it only by id.
func (s *Source) ItemByID(ctx context.Context, itemID string) (media.Item, bool) {
	if itemID == "" {
		return media.Item{}, false
	}
	params, err := s.auth()
	if err != nil {
		return media.Item{}, false
	}
	params.Set("id", itemID)
	var env envelope
	if err := s.http.JSON(ctx, "/rest/getSong.view", params, &env); err != nil || env.check() != nil {
		return media.Item{}, false
	}
	if env.Response.Song == nil {
		return media.Item{}, false
	}
	return s.songItem(*env.Response.Song), true
}

// albumEntry is an album as Subsonic describes it - in getAlbumList2,
// getAlbum (with its songs) and getArtist (inside an artist).
type albumEntry struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Artist    string `json:"artist"`
	ArtistID  string `json:"artistId"`
	Year      int    `json:"year"`
	SongCount int    `json:"songCount"`
	Duration  int    `json:"duration"`
	CoverArt  string `json:"coverArt"`
	Song      []song `json:"song"`
}

type artistEntry struct {
	ID         string       `json:"id"`
	Name       string       `json:"name"`
	AlbumCount int          `json:"albumCount"`
	CoverArt   string       `json:"coverArt"`
	Album      []albumEntry `json:"album"`
}

func (s *Source) album(a albumEntry) source.Album {
	return source.Album{
		ID: a.ID, SourceID: s.id, Title: a.Name, Artist: a.Artist, ArtistID: a.ArtistID,
		Year: a.Year, SongCount: a.SongCount, DurationSeconds: float64(a.Duration), ArtID: a.CoverArt,
	}
}

func (s *Source) artist(a artistEntry) source.Artist {
	return source.Artist{ID: a.ID, SourceID: s.id, Name: a.Name, AlbumCount: a.AlbumCount, ArtID: a.CoverArt}
}

// call makes one authenticated Subsonic request and checks its envelope.
func (s *Source) call(ctx context.Context, path string, set func(url.Values)) (envelope, error) {
	params, err := s.auth()
	if err != nil {
		return envelope{}, err
	}
	if set != nil {
		set(params)
	}
	var env envelope
	if err := s.http.JSON(ctx, path, params, &env); err != nil {
		return envelope{}, fmt.Errorf("subsonic %q: %s: %w", s.id, path, err)
	}
	return env, env.check()
}

var albumListTypes = map[string]string{
	source.AlbumsByName:   "alphabeticalByName",
	source.AlbumsNewest:   "newest",
	source.AlbumsByArtist: "alphabeticalByArtist",
	source.AlbumsRecent:   "recent",
	source.AlbumsFrequent: "frequent",
	source.AlbumsRandom:   "random",
}

// Albums lists albums in one of Subsonic's orders. Paged by the caller; each
// call asks for at most 500, which is the protocol's own ceiling.
func (s *Source) Albums(ctx context.Context, order string, offset, limit int) ([]source.Album, error) {
	kind, ok := albumListTypes[order]
	if !ok {
		kind = albumListTypes[source.AlbumsByName]
	}
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	env, err := s.call(ctx, "/rest/getAlbumList2.view", func(p url.Values) {
		p.Set("type", kind)
		p.Set("size", strconv.Itoa(limit))
		p.Set("offset", strconv.Itoa(max(0, offset)))
	})
	if err != nil {
		return nil, err
	}
	out := make([]source.Album, 0, len(env.Response.AlbumList2.Album))
	for _, a := range env.Response.AlbumList2.Album {
		out = append(out, s.album(a))
	}
	return out, nil
}

// Album is one album and its songs, in track order.
func (s *Source) Album(ctx context.Context, id string) (source.Album, []media.Item, error) {
	env, err := s.call(ctx, "/rest/getAlbum.view", func(p url.Values) { p.Set("id", id) })
	if err != nil {
		return source.Album{}, nil, err
	}
	if env.Response.Album == nil {
		return source.Album{}, nil, fmt.Errorf("subsonic %q: no album %q", s.id, id)
	}
	songs := make([]media.Item, 0, len(env.Response.Album.Song))
	for _, sg := range env.Response.Album.Song {
		songs = append(songs, s.songItem(sg))
	}
	return s.album(*env.Response.Album), songs, nil
}

// Artists is every artist, as Subsonic's index lists them.
func (s *Source) Artists(ctx context.Context) ([]source.Artist, error) {
	env, err := s.call(ctx, "/rest/getArtists.view", nil)
	if err != nil {
		return nil, err
	}
	var out []source.Artist
	for _, index := range env.Response.Artists.Index {
		for _, a := range index.Artist {
			out = append(out, s.artist(a))
		}
	}
	return out, nil
}

// Artist is one artist and their albums.
func (s *Source) Artist(ctx context.Context, id string) (source.Artist, []source.Album, error) {
	env, err := s.call(ctx, "/rest/getArtist.view", func(p url.Values) { p.Set("id", id) })
	if err != nil {
		return source.Artist{}, nil, err
	}
	if env.Response.Artist == nil {
		return source.Artist{}, nil, fmt.Errorf("subsonic %q: no artist %q", s.id, id)
	}
	albums := make([]source.Album, 0, len(env.Response.Artist.Album))
	for _, a := range env.Response.Artist.Album {
		albums = append(albums, s.album(a))
	}
	return s.artist(*env.Response.Artist), albums, nil
}

// SearchMusic finds albums and artists whose names match.
func (s *Source) SearchMusic(ctx context.Context, text string) ([]source.Album, []source.Artist, error) {
	env, err := s.call(ctx, "/rest/search3.view", func(p url.Values) {
		p.Set("query", text)
		p.Set("songCount", "0")
		p.Set("albumCount", "100")
		p.Set("artistCount", "50")
	})
	if err != nil {
		return nil, nil, err
	}
	var albums []source.Album
	for _, a := range env.Response.SearchResult3.Album {
		albums = append(albums, s.album(a))
	}
	var artists []source.Artist
	for _, a := range env.Response.SearchResult3.Artist {
		artists = append(artists, s.artist(a))
	}
	return albums, artists, nil
}
