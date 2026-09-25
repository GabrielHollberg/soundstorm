package subsonic

import (
	"context"
	"encoding/base64"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/source"
)

// Artists and albums follow the folders, not the tags.
//
// Navidrome groups by tags: "Artist feat. Someone" becomes an artist of its
// own, and an album whose tracks disagree about the album artist or the year
// splits in two. SoundStorm already files every upload as Artist/Album/track,
// so the folders are the tidy version of the library - and the owner asked for
// them to be what the app shows. Navidrome's own folder browsing does not help:
// in 0.64 getIndexes and getMusicDirectory answer with its tag-based artists
// and albums (checked: the "folder" ids are album and artist ids).
//
// What every song does carry is its path, relative to the music folder. So the
// artist is the first folder of the path, the album the second, and a disc
// folder below that stays part of its album. Nothing here reads the disk or
// keeps an index: it is a grouping of Navidrome's own song list, cached as
// briefly as the song shelf is.

// A folder id is "f:" and the folder's path, base64url - stable across scans,
// and never mistaken for one of Navidrome's own ids.
const folderPrefix = "f:"

func folderID(path string) string {
	return folderPrefix + base64.RawURLEncoding.EncodeToString([]byte(path))
}

func folderPath(id string) (string, bool) {
	if !strings.HasPrefix(id, folderPrefix) {
		return "", false
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(id, folderPrefix))
	if err != nil || len(b) == 0 {
		return "", false
	}
	return string(b), true
}

// singles is the album for songs sitting straight in an artist's folder.
const singles = "Singles"

// unknownArtist is the artist for a song at the top of the music folder.
const unknownArtist = "Unknown Artist"

type folderAlbum struct {
	path     string // "Artist/Album"
	folder   string // the artist folder's name, which the artist is keyed by
	artist   string // shown: the folder, or the tags' spelling of it
	title    string
	songs    []song
	year     int
	duration int
	art      string
	added    string // newest file's created time, RFC 3339
	played   string // most recent play
	plays    int
}

type folderArtist struct {
	folder string
	name   string
	albums []*folderAlbum
	art    string
}

type folderLibrary struct {
	albums  []*folderAlbum
	artists []*folderArtist
	byAlbum map[string]*folderAlbum
	byName  map[string]*folderArtist
}

// libraryTTL matches the song shelf: scrolling and paging share one fetch.
const libraryTTL = 30 * time.Second

type folderCache struct {
	mu      sync.Mutex
	lib     *folderLibrary
	at      time.Time
	loading chan struct{}
}

func (c *folderCache) clear() {
	c.mu.Lock()
	c.lib = nil
	c.mu.Unlock()
}

// library is the grouped song list, fetched once for everybody asking at the
// same moment and kept for libraryTTL.
func (s *Source) library(ctx context.Context) (*folderLibrary, error) {
	c := &s.folders
	for {
		c.mu.Lock()
		if c.lib != nil && time.Since(c.at) < libraryTTL {
			lib := c.lib
			c.mu.Unlock()
			return lib, nil
		}
		if c.loading != nil {
			wait := c.loading
			c.mu.Unlock()
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		done := make(chan struct{})
		c.loading = done
		c.mu.Unlock()

		songs, err := s.fetchAllSongs(ctx, "")
		if err == nil {
			// Paths relative to the music folder. One that is not real (see
			// realPath) is grouped as it stands, which is by its tags - the
			// best there is until Navidrome reports real paths.
			for i := range songs {
				if rel, ok := s.realPath(songs[i].Path); ok {
					songs[i].Path = rel
				}
			}
		}
		c.mu.Lock()
		c.loading = nil
		if err == nil {
			c.lib = groupByFolder(songs)
			c.at = time.Now()
		}
		lib := c.lib
		c.mu.Unlock()
		close(done)
		if err != nil {
			return nil, err
		}
		return lib, nil
	}
}

// groupByFolder turns songs into folder albums and artists.
func groupByFolder(songs []song) *folderLibrary {
	lib := &folderLibrary{byAlbum: map[string]*folderAlbum{}, byName: map[string]*folderArtist{}}
	for _, sg := range songs {
		parts := strings.Split(strings.Trim(strings.ReplaceAll(sg.Path, "\\", "/"), "/"), "/")
		if inBundle(parts) {
			continue
		}
		artist, album := unknownArtist, singles
		switch {
		case len(parts) >= 3:
			artist, album = parts[0], parts[1]
		case len(parts) == 2:
			artist = parts[0]
		}
		key := artist + "/" + album
		a := lib.byAlbum[key]
		if a == nil {
			a = &folderAlbum{path: key, folder: artist, artist: artist, title: album}
			lib.byAlbum[key] = a
			lib.albums = append(lib.albums, a)
		}
		a.songs = append(a.songs, sg)
	}
	for _, a := range lib.albums {
		sort.SliceStable(a.songs, func(i, j int) bool { return songBefore(a.songs[i], a.songs[j]) })
		arts, years := map[string]int{}, map[int]int{}
		for _, sg := range a.songs {
			a.duration += sg.Duration
			a.plays += sg.PlayCount
			if sg.CoverArt != "" {
				arts[sg.CoverArt]++
			}
			if sg.Year > 0 {
				years[sg.Year]++
			}
			if sg.Created > a.added {
				a.added = sg.Created
			}
			if sg.Played > a.played {
				a.played = sg.Played
			}
		}
		a.art = mostCommon(arts)
		a.year = mostCommonInt(years)
		if a.title != singles {
			titles := map[string]int{}
			for _, sg := range a.songs {
				titles[sg.Album]++
			}
			a.title = spelling(a.title, titles)
		}

		ar := lib.byName[a.folder]
		if ar == nil {
			ar = &folderArtist{folder: a.folder, name: a.folder}
			lib.byName[a.folder] = ar
			lib.artists = append(lib.artists, ar)
		}
		ar.albums = append(ar.albums, a)
	}
	// An artist is the folder, spelled as the tags spell it when the two are
	// the same name: a folder cannot be called "AC/DC" or end in a dot, so
	// "AC-DC" shows as AC/DC and "Fun" as Fun. - while a folder the tags
	// genuinely disagree with keeps its own name.
	for _, ar := range lib.artists {
		names := map[string]int{}
		for _, a := range ar.albums {
			for _, sg := range a.songs {
				names[sg.AlbumArtist]++
				names[sg.Artist]++
			}
		}
		ar.name = spelling(ar.folder, names)
		for _, a := range ar.albums {
			a.artist = ar.name
		}
	}
	for _, ar := range lib.artists {
		sort.SliceStable(ar.albums, func(i, j int) bool {
			x, y := ar.albums[i], ar.albums[j]
			if x.year != y.year {
				return x.year < y.year
			}
			return sortKey(x.title) < sortKey(y.title)
		})
		// The artist's picture is the cover of their newest album that has
		// one. Navidrome's own artist image is a placeholder silhouette unless
		// it has been set up to fetch photos from the internet, which it is
		// not, so an album cover is the only real picture there is.
		ar.art = ""
		for i := len(ar.albums) - 1; i >= 0; i-- {
			if ar.albums[i].art != "" {
				ar.art = ar.albums[i].art
				break
			}
		}
	}
	sort.SliceStable(lib.artists, func(i, j int) bool { return sortKey(lib.artists[i].name) < sortKey(lib.artists[j].name) })
	return lib
}

// songBefore is album order: the sub-folder (a disc), then disc and track
// numbers, then the file name, which a rip numbers anyway.
func songBefore(x, y song) bool {
	dx, dy := dirOf(x.Path), dirOf(y.Path)
	if dx != dy {
		return dx < dy
	}
	if x.DiscNumber != y.DiscNumber {
		return x.DiscNumber < y.DiscNumber
	}
	if x.Track != y.Track {
		return x.Track < y.Track
	}
	return strings.ToLower(x.Path) < strings.ToLower(y.Path)
}

func dirOf(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return strings.ToLower(p[:i])
	}
	return ""
}

// sortKey orders names the way a record shop does: case aside, and without a
// leading "The" or "A".
func sortKey(name string) string {
	k := strings.ToLower(strings.TrimSpace(name))
	for _, article := range []string{"the ", "a ", "an "} {
		if strings.HasPrefix(k, article) && len(k) > len(article) {
			return k[len(article):]
		}
	}
	return k
}

func mostCommon(counts map[string]int) string {
	best, n := "", 0
	for k, c := range counts {
		if c > n || (c == n && k < best) {
			best, n = k, c
		}
	}
	return best
}

func mostCommonInt(counts map[int]int) int {
	best, n := 0, 0
	for k, c := range counts {
		if c > n || (c == n && k > best) {
			best, n = k, c
		}
	}
	return best
}

// inBundle is whether a path runs through an iTunes LP or iTunes Extras
// bundle: a digital booklet's own jingle is not a song on an album, and the
// bundle is not an artist.
func inBundle(parts []string) bool {
	for _, p := range parts[:max(0, len(parts)-1)] {
		l := strings.ToLower(p)
		if strings.HasSuffix(l, ".itlp") || strings.HasSuffix(l, ".ite") {
			return true
		}
	}
	return false
}

// spelling is the folder's name as the tags write it: the most common tag
// value that is the same name once case and punctuation are set aside, or
// the folder's own name when none is.
func spelling(folder string, tags map[string]int) string {
	want := loose(folder)
	best, n := folder, 0
	for tag, count := range tags {
		if tag != "" && loose(tag) == want && (count > n || (count == n && tag < best)) {
			best, n = tag, count
		}
	}
	return best
}

// loose is a name with only its letters and digits, in lower case.
func loose(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (s *Source) folderAlbum(a *folderAlbum) source.Album {
	return source.Album{
		ID: folderID(a.path), SourceID: s.id, Title: a.title, Artist: a.artist,
		ArtistID: folderID(a.folder), Year: a.year, SongCount: len(a.songs),
		DurationSeconds: float64(a.duration), ArtID: a.art,
	}
}

func (s *Source) folderArtist(ar *folderArtist) source.Artist {
	return source.Artist{ID: folderID(ar.folder), SourceID: s.id, Name: ar.name, AlbumCount: len(ar.albums), ArtID: ar.art}
}

// Albums lists the album folders in one of the browsing orders.
func (s *Source) Albums(ctx context.Context, order string, offset, limit int) ([]source.Album, error) {
	lib, err := s.library(ctx)
	if err != nil {
		return nil, err
	}
	list := append([]*folderAlbum(nil), lib.albums...)
	byName := func(i, j int) bool {
		if ki, kj := sortKey(list[i].title), sortKey(list[j].title); ki != kj {
			return ki < kj
		}
		return sortKey(list[i].artist) < sortKey(list[j].artist)
	}
	switch order {
	case source.AlbumsNewest:
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].added != list[j].added {
				return list[i].added > list[j].added
			}
			return byName(i, j)
		})
	case source.AlbumsByArtist:
		sort.SliceStable(list, func(i, j int) bool {
			if ki, kj := sortKey(list[i].artist), sortKey(list[j].artist); ki != kj {
				return ki < kj
			}
			if list[i].year != list[j].year {
				return list[i].year < list[j].year
			}
			return byName(i, j)
		})
	case source.AlbumsRecent:
		list = filterAlbums(list, func(a *folderAlbum) bool { return a.played != "" })
		sort.SliceStable(list, func(i, j int) bool { return list[i].played > list[j].played })
	case source.AlbumsFrequent:
		list = filterAlbums(list, func(a *folderAlbum) bool { return a.plays > 0 })
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].plays != list[j].plays {
				return list[i].plays > list[j].plays
			}
			return byName(i, j)
		})
	case source.AlbumsRandom:
		rand.Shuffle(len(list), func(i, j int) { list[i], list[j] = list[j], list[i] })
	default:
		sort.SliceStable(list, byName)
	}
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	offset = max(0, offset)
	if offset >= len(list) {
		return []source.Album{}, nil
	}
	list = list[offset:min(len(list), offset+limit)]
	out := make([]source.Album, 0, len(list))
	for _, a := range list {
		out = append(out, s.folderAlbum(a))
	}
	return out, nil
}

func filterAlbums(list []*folderAlbum, keep func(*folderAlbum) bool) []*folderAlbum {
	out := list[:0]
	for _, a := range list {
		if keep(a) {
			out = append(out, a)
		}
	}
	return out
}

// Album is one album folder and its songs, in order.
func (s *Source) Album(ctx context.Context, id string) (source.Album, []media.Item, error) {
	path, ok := folderPath(id)
	if !ok {
		return s.tagAlbum(ctx, id) // an id saved before albums followed folders
	}
	lib, err := s.library(ctx)
	if err != nil {
		return source.Album{}, nil, err
	}
	a := lib.byAlbum[path]
	if a == nil {
		return source.Album{}, nil, fmt.Errorf("subsonic %q: no album %q", s.id, path)
	}
	songs := make([]media.Item, 0, len(a.songs))
	for _, sg := range a.songs {
		songs = append(songs, s.songItem(sg))
	}
	return s.folderAlbum(a), songs, nil
}

// Artists is every artist folder, in name order.
func (s *Source) Artists(ctx context.Context) ([]source.Artist, error) {
	lib, err := s.library(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]source.Artist, 0, len(lib.artists))
	for _, ar := range lib.artists {
		out = append(out, s.folderArtist(ar))
	}
	return out, nil
}

// Artist is one artist folder and its albums, oldest first.
func (s *Source) Artist(ctx context.Context, id string) (source.Artist, []source.Album, error) {
	name, ok := folderPath(id)
	if !ok {
		return s.tagArtist(ctx, id)
	}
	lib, err := s.library(ctx)
	if err != nil {
		return source.Artist{}, nil, err
	}
	ar := lib.byName[name]
	if ar == nil {
		return source.Artist{}, nil, fmt.Errorf("subsonic %q: no artist %q", s.id, name)
	}
	albums := make([]source.Album, 0, len(ar.albums))
	for _, a := range ar.albums {
		albums = append(albums, s.folderAlbum(a))
	}
	return s.folderArtist(ar), albums, nil
}

// SearchMusic finds album and artist folders whose names hold every word typed.
func (s *Source) SearchMusic(ctx context.Context, text string) ([]source.Album, []source.Artist, error) {
	lib, err := s.library(ctx)
	if err != nil {
		return nil, nil, err
	}
	words := strings.Fields(strings.ToLower(text))
	match := func(hay string) bool {
		hay = strings.ToLower(hay)
		for _, w := range words {
			if !strings.Contains(hay, w) {
				return false
			}
		}
		return true
	}
	var albums []source.Album
	for _, a := range lib.albums {
		if match(a.title + " " + a.artist) {
			albums = append(albums, s.folderAlbum(a))
		}
	}
	sort.SliceStable(albums, func(i, j int) bool { return sortKey(albums[i].Title) < sortKey(albums[j].Title) })
	var artists []source.Artist
	for _, ar := range lib.artists {
		if match(ar.name) {
			artists = append(artists, s.folderArtist(ar))
		}
	}
	return albums, artists, nil
}
