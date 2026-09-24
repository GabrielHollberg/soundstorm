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
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/httpx"
	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/source"
)

const (
	apiVersion = "1.16.1"
	// clientName is how SoundStorm introduces itself to Navidrome, which
	// keeps a "player" record per client name and sets its "report real
	// path" switch once, when the record is created, from
	// ND_SUBSONIC_DEFAULTREPORTREALPATH. Records made under the old name,
	// "soundstorm", predate that setting and keep sending paths made up from
	// the tags - checked on a real install: the same song came back as
	// "2CELLOS/2Cellos/01-06 - The Resistance.m4a" under the old name and
	// "/music/2CELLOS/2Cellos/06 The Resistance.m4a" under a new one. A new
	// name gets every install a record with real paths, without reaching
	// into Navidrome's own admin API.
	clientName = "soundstorm-app"
)

// Config configures a Subsonic source.
type Config struct {
	ID       string
	BaseURL  string
	Username string
	Password string
	Timeout  time.Duration
	// MediaRoot is the music folder as Navidrome sees it, "/music" unless
	// set. Song paths are real paths under it (ND_SUBSONIC_DEFAULTREPORTREALPATH)
	// and are made relative to it before anything uses them.
	MediaRoot string
}

// defaultMediaRoot is where docker-compose.yml mounts the music folder in
// Navidrome's container.
const defaultMediaRoot = "/music"

// Source is a Subsonic-API music server.
type Source struct {
	id    string
	cfg   Config
	http  *httpx.Client
	shelf media.ShelfCache
	// folders is the song list grouped into artist and album folders; see
	// folders.go.
	folders folderCache
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
	if cfg.MediaRoot == "" {
		cfg.MediaRoot = defaultMediaRoot
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
		Artist      *artistEntry `json:"artist"`
		RandomSongs struct {
			Song []song `json:"song"`
		} `json:"randomSongs"`
		LyricsList struct {
			StructuredLyrics []struct {
				Synced bool `json:"synced"`
				Line   []struct {
					Start *int   `json:"start"`
					Value string `json:"value"`
				} `json:"line"`
			} `json:"structuredLyrics"`
		} `json:"lyricsList"`
		Genres struct {
			Genre []struct {
				Value     string `json:"value"`
				SongCount int    `json:"songCount"`
			} `json:"genre"`
		} `json:"genres"`
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
	Genre    string `json:"genre"`
	// Path is relative to the music folder - checked against Navidrome
	// 0.64: "Artist/Album/01 - Title.mp3". Artists and albums are the
	// folders in it (folders.go).
	Path       string `json:"path"`
	Track      int    `json:"track"`
	DiscNumber int    `json:"discNumber"`
	Created    string `json:"created"` // when Navidrome first saw the file
	PlayCount  int    `json:"playCount"`
	Played     string `json:"played"` // last played
	ArtistID   string `json:"artistId"`
	// AlbumArtist is OpenSubsonic's display form of the album artist tag.
	AlbumArtist string `json:"displayAlbumArtist"`
	// ReplayGain is OpenSubsonic's: how loud the track and its album are,
	// from the file's own tags. Checked against Navidrome 0.64.1, which
	// reports it for a tagged MP3 and leaves it out for an untagged one.
	ReplayGain *struct {
		TrackGain *float64 `json:"trackGain"`
		AlbumGain *float64 `json:"albumGain"`
		TrackPeak *float64 `json:"trackPeak"`
		AlbumPeak *float64 `json:"albumPeak"`
	} `json:"replayGain"`
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
	songs, err := s.fetchSongsPage(ctx, text, offset)
	if err != nil {
		return nil, err
	}
	items := make([]media.Item, 0, len(songs))
	for _, sg := range songs {
		items = append(items, s.songItem(sg))
	}
	return items, nil
}

// fetchAllSongs pages through every song matching text, as Navidrome
// describes them - paths and play counts included, which is what grouping
// them into folders needs.
func (s *Source) fetchAllSongs(ctx context.Context, text string) ([]song, error) {
	var all []song
	for offset := 0; offset < maxSongs; offset += fetchPage {
		page, err := s.fetchSongsPage(ctx, text, offset)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if len(page) < fetchPage {
			break
		}
	}
	return all, nil
}

func (s *Source) fetchSongsPage(ctx context.Context, text string, offset int) ([]song, error) {
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

	return env.Response.SearchResult3.Song, nil
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
	if sg.Genre != "" {
		item.Extra["genre"] = sg.Genre
	}
	// For volume levelling in the player, which is the only thing that
	// reads these.
	if rg := sg.ReplayGain; rg != nil {
		for key, v := range map[string]*float64{
			"trackGain": rg.TrackGain, "albumGain": rg.AlbumGain,
			"trackPeak": rg.TrackPeak, "albumPeak": rg.AlbumPeak,
		} {
			if v != nil && !math.IsNaN(*v) && !math.IsInf(*v, 0) {
				item.Extra[key] = strconv.FormatFloat(*v, 'f', 2, 64)
			}
		}
	}
	return item
}

// StreamTarget builds an authenticated upstream target for a track.
//
// Subsonic carries credentials in the query string, which is how the protocol
// works, so no headers are needed. They never reach the browser: SoundStorm
// fetches them itself and pipes the bytes through, so Navidrome needs no
// published port.
func (s *Source) StreamTarget(ctx context.Context, itemID string) (source.Target, error) {
	t, err := s.mediaTarget("/rest/stream.view", itemID)
	if err != nil {
		return t, err
	}
	if kbps := source.MaxBitRate(ctx); kbps > 0 {
		// A data saver: Navidrome converts to MP3 at no more than kbps - MP3
		// because every browser plays it, where Navidrome's own default,
		// Opus, is patchy on iPhones. A file already that small, in that
		// format, is sent as it is. estimateContentLength gives the converted
		// stream a length, which is what lets a browser show and seek a
		// timeline before the whole song has arrived.
		u, perr := url.Parse(t.URL)
		if perr != nil {
			return source.Target{}, perr
		}
		q := u.Query()
		q.Set("maxBitRate", strconv.Itoa(kbps))
		q.Set("format", "mp3")
		q.Set("estimateContentLength", "true")
		u.RawQuery = q.Encode()
		t.URL = u.String()
	}
	return t, nil
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
	s.folders.clear()
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
	rel, ok := s.realPath(env.Response.Song.Path)
	if !ok {
		// A path Navidrome made up from the tags names a file that may not
		// exist; deleting by it would miss, or worse.
		return nil, fmt.Errorf("subsonic %q: Navidrome is not reporting real file paths", s.id)
	}
	return []string{rel}, nil
}

// realPath turns a song's path into one relative to the music folder, and
// says whether it is a real one. Navidrome reports the file's actual path
// only when told to (ND_SUBSONIC_DEFAULTREPORTREALPATH); otherwise it sends
// one built from the tags - "Artist tag/Album tag/01 - Title.mp3" - which
// looks exactly like a real path whenever tags and folders agree, and names
// no file whenever they do not. A real path starts at Navidrome's music folder.
func (s *Source) realPath(p string) (string, bool) {
	if !strings.HasPrefix(p, "/") {
		return p, false
	}
	rel, err := source.RelativeTo(s.cfg.MediaRoot, p)
	if err != nil {
		return "", false
	}
	return rel, true
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

// tagAlbum is one of Navidrome's own (tag-grouped) albums, for an id saved
// before albums followed folders.
func (s *Source) tagAlbum(ctx context.Context, id string) (source.Album, []media.Item, error) {
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

// tagArtist is one of Navidrome's own (tag-grouped) artists, for an id saved
// before artists followed folders.
func (s *Source) tagArtist(ctx context.Context, id string) (source.Artist, []source.Album, error) {
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

// RandomSongs draws songs at random: from the whole library, one genre, or a
// span of years (zero means no bound). Subsonic caps a draw at 500.
func (s *Source) RandomSongs(ctx context.Context, n int, genre string, fromYear, toYear int) ([]media.Item, error) {
	if n <= 0 || n > 500 {
		n = 500
	}
	env, err := s.call(ctx, "/rest/getRandomSongs.view", func(p url.Values) {
		p.Set("size", strconv.Itoa(n))
		if genre != "" {
			p.Set("genre", genre)
		}
		if fromYear > 0 {
			p.Set("fromYear", strconv.Itoa(fromYear))
		}
		if toYear > 0 {
			p.Set("toYear", strconv.Itoa(toYear))
		}
	})
	if err != nil {
		return nil, err
	}
	out := make([]media.Item, 0, len(env.Response.RandomSongs.Song))
	for _, sg := range env.Response.RandomSongs.Song {
		out = append(out, s.songItem(sg))
	}
	return out, nil
}

// Genres lists the library's genres, with how many songs each has.
func (s *Source) Genres(ctx context.Context) ([]source.Genre, error) {
	env, err := s.call(ctx, "/rest/getGenres.view", nil)
	if err != nil {
		return nil, err
	}
	out := make([]source.Genre, 0, len(env.Response.Genres.Genre))
	for _, g := range env.Response.Genres.Genre {
		if g.Value != "" {
			out = append(out, source.Genre{Name: g.Value, SongCount: g.SongCount})
		}
	}
	return out, nil
}

// Lyrics asks for a song's lyrics through OpenSubsonic's songLyrics extension,
// which Navidrome 0.64.1 lists and answers from a .lrc file beside the song
// (checked) or the file's own tags. Synced lyrics are preferred when there
// is a choice. A song with none answers an empty list, not an error.
func (s *Source) Lyrics(ctx context.Context, songID string) (source.Lyrics, error) {
	env, err := s.call(ctx, "/rest/getLyricsBySongId.view", func(p url.Values) { p.Set("id", songID) })
	if err != nil {
		return source.Lyrics{}, err
	}
	all := env.Response.LyricsList.StructuredLyrics
	if len(all) == 0 {
		return source.Lyrics{Lines: []source.LyricLine{}}, nil
	}
	pick := all[0]
	for _, l := range all {
		if l.Synced {
			pick = l
			break
		}
	}
	out := source.Lyrics{Synced: pick.Synced, Lines: make([]source.LyricLine, 0, len(pick.Line))}
	for _, line := range pick.Line {
		start := -1
		if pick.Synced && line.Start != nil {
			start = *line.Start
		}
		out.Lines = append(out.Lines, source.LyricLine{Start: start, Text: line.Value})
	}
	return out, nil
}
