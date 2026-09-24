// Package source defines the plug point for a backend.
//
// Adding a media server to SoundStorm means implementing Source here and a
// provisioner in internal/provision. Those are the two halves of a backend: how
// to search it, and how to get credentials for it without a human typing any.
package source

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/media"
)

// Source is one upstream media server.
//
// Implementations must be safe for concurrent use: federated search calls
// Search on every source at once.
type Source interface {
	// ID is the backend's name ("navidrome", "jellyfin"). It appears in
	// results and in setup status, and it is part of SoundStorm's stream URLs.
	ID() string

	// Kind is what this source serves. A source serves exactly one kind.
	Kind() media.Kind

	// Search returns matches, already normalized. An error here is not fatal
	// to the overall search - the federator records it and carries on.
	Search(ctx context.Context, q media.Query) ([]media.Item, error)

	// Health reports whether the source is reachable and authenticated.
	Health(ctx context.Context) error
}

// Target is an authenticated upstream location for media bytes.
//
// It is never given to the browser: SoundStorm fetches it server-side and pipes
// the bytes through, which is what lets the backends stay off any published
// port.
//
// Headers exist because backends disagree about where a credential goes.
// Subsonic puts it in the query string. Jellyfin 12 accepts it only in an
// Authorization header, having dropped both X-Emby-Token and the api_key query
// parameter that older guides still recommend. The proxy should not have to
// know which, so an adapter hands back both parts and the proxy replays them.
// Exactly one of URL, FilePath or Bytes is set.
type Target struct {
	// URL fetches the bytes from a backend over HTTP.
	URL     string
	Headers map[string]string

	// FilePath serves a file from SoundStorm's own disk. Used by sources that have
	// no backend at all - a folder of ebooks is just a folder.
	FilePath string

	// Bytes serves something SoundStorm produced in memory, such as a cover image
	// extracted from inside an EPUB.
	Bytes []byte

	// ContentType, Name and ModTime describe FilePath and Bytes targets. They
	// are ignored for URL targets, where the upstream response says.
	ContentType string
	Name        string
	ModTime     time.Time

	// OnDone, if set, is called once the client has stopped reading.
	//
	// Some backends start work on our behalf that outlives the request: a
	// Jellyfin transcode is an ffmpeg process that keeps running until it is
	// told otherwise, so every abandoned playback would leave one burning CPU
	// until the server times it out.
	OnDone func()
}

// Streamer builds an authenticated upstream target for an item's bytes.
//
// It takes a context because resolving a target is not always local arithmetic:
// Audiobookshelf addresses audio by a per-file inode that only an API call can
// tell you, so the adapter has to ask upstream before it can answer.
type Streamer interface {
	StreamTarget(ctx context.Context, itemID string) (Target, error)
}

// ArtProvider builds an authenticated upstream target for artwork. artID is the
// opaque handle the adapter put in media.Item.ArtID.
type ArtProvider interface {
	ArtTarget(ctx context.Context, artID string) (Target, error)
}

// BookEntry is one file inside a book container.
type BookEntry struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// OpenBook is a handle on the inside of a book. Close it.
type OpenBook interface {
	Entries() []BookEntry
	Resource(name string) (data []byte, contentType string, err error)
	Close() error
}

// BookOpener is an optional interface for sources that can serve a book's
// internal structure, which is what an in-browser reader needs.
//
// Only a source that holds the file itself can do this. A remote OPDS catalog
// hands over a whole book and nothing smaller, which is exactly why SoundStorm
// reading the folder directly is what made a reader possible at all.
type BookOpener interface {
	OpenBook(ctx context.Context, itemID string) (OpenBook, error)
}

// Playback tells a client how to play an item.
type Playback struct {
	// Mode is "direct" when the bytes can be fetched straight from
	// /api/stream, or "hls" when the client must load a playlist instead.
	Mode string `json:"mode"`

	// Path and Query locate the playlist under /api/hls/{sourceID}/ when Mode
	// is "hls". The path matters: a playlist references its segments
	// relatively, so the URL a client loads it from determines where those
	// segment requests land.
	Path  string     `json:"path,omitempty"`
	Query url.Values `json:"-"`

	// Subtitles are offered whichever mode applies: a direct-played file can
	// still have a sidecar, and a transcode can still carry embedded tracks.
	Subtitles []SubtitleTrack `json:"subtitles,omitempty"`
}

// SubtitleTrack is one selectable subtitle stream.
type SubtitleTrack struct {
	// ID is opaque and specific to the source; hand it back to SubtitleTarget.
	ID string `json:"id"`

	Label string `json:"label"`

	// Language is a BCP-47 tag for the track element's srclang. Backends tend
	// to report ISO 639-2 ("eng"), which browsers do not want.
	Language string `json:"language,omitempty"`

	Forced bool `json:"forced,omitempty"`
}

// Track is one file an item is made of, in the order it should be played.
//
// A LibriVox audiobook is typically one MP3 per chapter - thirty of them for a
// volume of Aesop - and until a client is told about all of them it plays the
// first file and stops, which looks exactly like a broken book.
//
// A track is a whole file rather than an arbitrary span of one, because a file
// is both the unit a backend can address and the unit a browser can be handed
// and told to play. Chapters that live inside a single file are a different
// problem and not solved here: see TrackLister.
type Track struct {
	// ID is what to ask /api/stream for. It is the source's own handle and
	// need not resemble the item's id.
	ID string `json:"id"`

	// Title is what belongs in a chapter list - a chapter name where the
	// backend knows one, something derived from the file where it does not.
	Title string `json:"title"`

	DurationSeconds float64 `json:"durationSeconds,omitempty"`

	// StartSeconds is where this file begins on the whole item's timeline,
	// which is what turns a position into a chapter and an offset. A backend
	// that remembers listening position measures it across the whole book -
	// "two hours in", not "twelve minutes into part four".
	StartSeconds float64 `json:"startSeconds,omitempty"`
}

// Position is how far into an item somebody got.
type Position struct {
	// Seconds is an offset into the whole item, across all of its files.
	Seconds float64 `json:"seconds"`

	// Duration is the item's total length, where the backend knows it.
	Duration float64 `json:"duration,omitempty"`

	// Finished marks a book somebody listened to the end of. It is one-way
	// here: see PositionTracker.
	Finished bool `json:"finished,omitempty"`
}

// PositionTracker is an optional interface for sources that remember how far
// into an item somebody listened.
//
// Position belongs upstream rather than in SoundStorm's own state, and not only
// to avoid a second store: Audiobookshelf keeps it per title and syncs it to
// its own mobile apps, so writing it there means finishing a chapter in the car
// and picking it up in a browser. Keeping our own copy would quietly fork from
// the copy every other client is reading.
//
// Position returns a zero value rather than an error when a backend has never
// heard of the item. Nobody having started a book is not a failure, and "at the
// start" and "never opened" mean the same thing to a player.
type PositionTracker interface {
	Position(ctx context.Context, itemID string) (Position, error)

	// SetPosition records a position. Implementations must treat Finished as
	// one-way - see the Audiobookshelf adapter for what un-finishing costs.
	SetPosition(ctx context.Context, itemID string, pos Position) error
}

// TrackLister is an optional interface for sources whose items are made of more
// than one file.
//
// Only audiobooks have needed it. Music is already indexed a track at a time, a
// film is one file, and a book is a container the reader opens for itself.
//
// It reports files, not chapters, and the two coincide for a per-chapter rip -
// which is what a multi-file audiobook nearly always is. A single m4b carrying
// twenty chapter marks still returns one track: that book plays through
// correctly, it just has no chapter navigation, and adding that means seeking
// within a file rather than switching between them.
type TrackLister interface {
	Tracks(ctx context.Context, itemID string) ([]Track, error)
}

// SubtitleProvider serves a subtitle track as WebVTT.
//
// WebVTT because that is the only thing a browser will accept in a track
// element. Backends store SRT and ASS far more often, so converting is the
// backend's job - and Jellyfin does it on the fly, for embedded and sidecar
// files alike.
type SubtitleProvider interface {
	SubtitleTarget(ctx context.Context, trackID string) (Target, error)
}

// PlaybackModeDirect and PlaybackModeHLS are the two answers.
const (
	PlaybackModeDirect = "direct"
	PlaybackModeHLS    = "hls"
)

// Negotiator is an optional interface for sources that cannot always hand over
// a file as-is.
//
// Only video needs this. A song is a song, but a film may be in a container or
// codec the browser cannot decode, in which case the backend has to re-encode
// it and the client has to be told to expect a playlist rather than a file.
// Sources that do not implement this are always played directly.
type Negotiator interface {
	Playback(ctx context.Context, itemID string) (Playback, error)
}

// HLSProvider serves the playlists and segments of a transcoded stream.
//
// path arrives relative to the source's own HLS namespace - "{itemID}/main.m3u8",
// "{itemID}/hls1/main/3.ts" - because that is how the playlist refers to them.
type HLSProvider interface {
	HLSTarget(ctx context.Context, path string, query url.Values) (Target, error)
}

// Rescanner is an optional interface for a source that can be told to look at
// its folder now rather than at its next sweep.
//
// Every backend indexes on a timer - Navidrome every minute, the ebook scanner
// every two - so a file that is already on disk is not searchable for up to
// that long. That is a strange thing to explain to somebody who just watched
// the upload finish, and every one of these servers has a "scan now" call, so
// there is no reason to make them wait for it.
//
// Rescan asks and returns; it does not wait for the scan to finish. A scan of
// a large library takes minutes, and the caller only wants the file to start
// being noticed.
type Rescanner interface {
	Rescan(ctx context.Context) error
}

// Starter is an optional interface for sources that must do work before they
// can answer anything - a local library has to read the disk first.
//
// Provisioning calls it before health-checking, so a source only reaches the
// registry once it is genuinely searchable.
type Starter interface {
	Start(ctx context.Context) error
}

// --- who is asking ------------------------------------------------------------

type userKey struct{}

// WithUserID records which SoundStorm account a request belongs to.
//
// Almost nothing needs it. A search returns the same library to everybody, and
// a film is the same bytes whoever asked. It exists for the one thing that
// genuinely differs per person: listening position, which Audiobookshelf keeps
// per account, so two people sharing one account there would overwrite each
// other's place in a book.
//
// It lives here rather than in internal/auth so that an adapter can read it
// without depending on how logging in works.
func WithUserID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, userKey{}, id)
}

type bitRateKey struct{}

// WithMaxBitRate asks a streaming source for audio of at most kbps - a data
// saver for a phone. A source that cannot convert audio ignores it.
func WithMaxBitRate(ctx context.Context, kbps int) context.Context {
	return context.WithValue(ctx, bitRateKey{}, kbps)
}

// MaxBitRate is the ceiling asked for with WithMaxBitRate, or 0 for none.
func MaxBitRate(ctx context.Context) int {
	kbps, _ := ctx.Value(bitRateKey{}).(int)
	return kbps
}

// UserID returns the account a request belongs to, or "" when there is none -
// a background scan or a provisioning call, which belong to nobody.
func UserID(ctx context.Context) string {
	id, _ := ctx.Value(userKey{}).(string)
	return id
}

// --- what a request may see ----------------------------------------------------

// Access is the set of media kinds one request is allowed to reach.
//
// The zero value permits everything, which is what a background scan, a health
// check or a provisioning call gets. Restrictions only ever arrive from an HTTP
// request carrying an account.
type Access struct {
	// kinds is nil for unrestricted access and non-nil otherwise, including
	// when it is empty - an account allowed nothing is a real state, and it
	// must not be mistaken for an account allowed everything.
	kinds map[media.Kind]bool
}

// AccessTo builds a restricted Access. A nil slice means no restriction; an
// empty non-nil slice means nothing at all.
func AccessTo(kinds []media.Kind) Access {
	if kinds == nil {
		return Access{}
	}
	set := make(map[media.Kind]bool, len(kinds))
	for _, k := range kinds {
		set[k] = true
	}
	return Access{kinds: set}
}

// Unrestricted reports whether this Access permits every kind.
func (a Access) Unrestricted() bool { return a.kinds == nil }

// Permits reports whether this request may see the given kind.
func (a Access) Permits(k media.Kind) bool {
	return a.kinds == nil || a.kinds[k]
}

// Kinds returns the permitted kinds in SoundStorm's own order, or every kind
// when unrestricted. Useful for telling a client what it may ask for.
func (a Access) Kinds() []media.Kind {
	if a.kinds == nil {
		return media.AllKinds()
	}
	var out []media.Kind
	for _, k := range media.AllKinds() {
		if a.kinds[k] {
			out = append(out, k)
		}
	}
	return out
}

type accessKey struct{}

// WithAccess records what a request is allowed to reach.
func WithAccess(ctx context.Context, a Access) context.Context {
	return context.WithValue(ctx, accessKey{}, a)
}

// AccessFrom returns a request's permissions, defaulting to unrestricted.
//
// Defaulting open is deliberate and is the reason the Registry's lookups take a
// context at all: internal callers - provisioning, health checks, the library
// counter - have no account and must see everything. Every path that serves a
// person goes through a handler that sets this, and every one of those is
// covered by a test that a restricted account is refused.
func AccessFrom(ctx context.Context) Access {
	a, _ := ctx.Value(accessKey{}).(Access)
	return a
}

// Registry holds the live sources.
//
// Unlike the rest of SoundStorm this is mutable at runtime: sources appear as
// their provisioners finish, which can be a minute or more after boot while a
// backend starts up. Every method is safe for concurrent use.
type Registry struct {
	mu      sync.RWMutex
	sources []Source
}

// NewRegistry builds a registry from the given sources.
func NewRegistry(sources ...Source) *Registry {
	r := &Registry{}
	for _, s := range sources {
		r.Set(s)
	}
	return r
}

// Set adds a source, replacing any existing source with the same ID.
func (r *Registry) Set(s Source) {
	if s == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, existing := range r.sources {
		if existing.ID() == s.ID() {
			r.sources[i] = s
			return
		}
	}
	r.sources = append(r.sources, s)
}

// All returns every source this request may see.
//
// These three take a context for one reason: it carries what the caller is
// allowed to reach. Making the signature demand it is the enforcement - a new
// handler cannot reach a source without passing the request's context, and
// passing the request's context is exactly what applies the restriction.
func (r *Registry) All(ctx context.Context) []Source {
	access := AccessFrom(ctx)
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Source, 0, len(r.sources))
	for _, s := range r.sources {
		if access.Permits(s.Kind()) {
			out = append(out, s)
		}
	}
	return out
}

// Matching returns the sources whose kind the query is interested in and the
// caller is allowed to see.
func (r *Registry) Matching(ctx context.Context, q media.Query) []Source {
	access := AccessFrom(ctx)
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Source
	for _, s := range r.sources {
		if q.WantsKind(s.Kind()) && access.Permits(s.Kind()) {
			out = append(out, s)
		}
	}
	return out
}

// ByID returns the source with the given id, or false when there is no such
// source or the caller may not see it.
//
// The two are deliberately the same answer. Telling somebody that a library
// exists but is not for them is a worse thing to say than nothing.
func (r *Registry) ByID(ctx context.Context, id string) (Source, bool) {
	access := AccessFrom(ctx)
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, s := range r.sources {
		if s.ID() == id {
			if !access.Permits(s.Kind()) {
				return nil, false
			}
			return s, true
		}
	}
	return nil, false
}

// Len reports how many sources are registered.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.sources)
}

// FileLister is an optional interface for a source whose items are files in
// SoundStorm's library, so that the owner can delete them.
//
// ItemFiles names the files and folders an item is made of, relative to the
// shelf's own folder and slash-separated: "Artist/Album/01 Song.mp3", or
// "Author/Title" for an audiobook that is a folder. Normalization at the edge
// again - every backend reports a path differently (Navidrome relative to the
// music folder, Audiobookshelf relative and absolute both, Jellyfin and Immich
// as their own container sees it), and only the adapter knows which.
//
// It only ever reports. Working out an item's companions (a film's subtitles,
// a book's sidecar), and moving anything, is internal/library's job, which is
// also what checks every path stays inside the shelf.
type FileLister interface {
	ItemFiles(ctx context.Context, itemID string) ([]string, error)
}

// RelativeTo turns a path a backend reported, as its own container sees it,
// into one relative to the shelf. It refuses a path outside root rather than
// guessing, so an asset from somewhere the shelf does not own can never be
// named for deletion.
func RelativeTo(root, reported string) (string, error) {
	root = strings.TrimRight(root, "/")
	if root == "" || !strings.HasPrefix(reported, root+"/") {
		return "", fmt.Errorf("%q is not inside %q", reported, root)
	}
	rel := strings.TrimPrefix(reported, root+"/")
	if rel == "" {
		return "", fmt.Errorf("%q is the shelf itself", reported)
	}
	return rel, nil
}

// Started is something one person has begun and not finished, for the
// "Continue" row.
type Started struct {
	Item     media.Item
	Fraction float64   // how far in, 0 to 1
	At       time.Time // when they were last in it
}

// InProgressLister is an optional interface for a source that remembers, per
// person, what they are part way through. The person is the one on ctx (see
// WithUserID), and the answer is newest first.
type InProgressLister interface {
	InProgress(ctx context.Context, limit int) ([]Started, error)
}

// ItemGetter is an optional interface for a source that can describe one item
// by id, for a place that knows an id but not what it looks like - a reading
// position is stored as an id, and the Continue row shows a cover and a title.
type ItemGetter interface {
	ItemByID(ctx context.Context, itemID string) (media.Item, bool)
}

// Album is one album, as a music shelf lists it.
type Album struct {
	ID              string  `json:"id"`
	SourceID        string  `json:"sourceId"`
	Title           string  `json:"title"`
	Artist          string  `json:"artist"`
	ArtistID        string  `json:"artistId,omitempty"`
	Year            int     `json:"year,omitempty"`
	SongCount       int     `json:"songCount"`
	DurationSeconds float64 `json:"durationSeconds,omitempty"`
	ArtID           string  `json:"artId,omitempty"`
}

// Artist is one artist, as a music shelf lists them.
type Artist struct {
	ID         string `json:"id"`
	SourceID   string `json:"sourceId"`
	Name       string `json:"name"`
	AlbumCount int    `json:"albumCount"`
	ArtID      string `json:"artId,omitempty"`
}

// Album orders, for MusicBrowser.Albums.
const (
	AlbumsByName   = "name"
	AlbumsNewest   = "newest"
	AlbumsByArtist = "artist"
	AlbumsRecent   = "recent"
	AlbumsFrequent = "frequent"
	AlbumsRandom   = "random"
)

// MusicBrowser is an optional interface for a music source that knows albums
// and artists, not just songs - which is how anybody actually browses a music
// library. A search box over four thousand song titles is not a music app.
type MusicBrowser interface {
	Albums(ctx context.Context, order string, offset, limit int) ([]Album, error)
	Album(ctx context.Context, id string) (Album, []media.Item, error)
	Artists(ctx context.Context) ([]Artist, error)
	Artist(ctx context.Context, id string) (Artist, []Album, error)
	// SearchMusic finds albums and artists by name.
	SearchMusic(ctx context.Context, text string) ([]Album, []Artist, error)
}

// Genre is one genre in a music library, and how much of it there is.
type Genre struct {
	Name      string `json:"name"`
	SongCount int    `json:"songCount"`
}

// MixSource is an optional interface for a music source that can draw songs at
// random - across the whole library, within a genre, or within a span of years
// - which is what a genre or decade mix is made of.
type MixSource interface {
	RandomSongs(ctx context.Context, n int, genre string, fromYear, toYear int) ([]media.Item, error)
	Genres(ctx context.Context) ([]Genre, error)
}

// LyricLine is one line of a song's words. Start is when it is sung, in
// milliseconds, or -1 when the lyrics carry no timings.
type LyricLine struct {
	Start int    `json:"start"`
	Text  string `json:"text"`
}

// Lyrics are a song's words, synced to the music when the source has
// timings for them.
type Lyrics struct {
	Synced bool        `json:"synced"`
	Lines  []LyricLine `json:"lines"`
}

// LyricsSource is an optional interface for a music source that can give a
// song's lyrics - from a .lrc file beside it, or the file's own tags.
type LyricsSource interface {
	Lyrics(ctx context.Context, songID string) (Lyrics, error)
}
