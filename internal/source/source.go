// Package source defines the plug point for a backend.
//
// Adding a media server to SoundStorm means implementing Source here and a
// provisioner in internal/provision. Those are the two halves of a backend: how
// to search it, and how to get credentials for it without a human typing any.
package source

import (
	"context"
	"net/url"
	"sync"
	"time"

	"github.com/gabehollberg/soundstorm/internal/media"
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

// Starter is an optional interface for sources that must do work before they
// can answer anything - a local library has to read the disk first.
//
// Provisioning calls it before health-checking, so a source only reaches the
// registry once it is genuinely searchable.
type Starter interface {
	Start(ctx context.Context) error
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

// All returns every registered source.
func (r *Registry) All() []Source {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Source, len(r.sources))
	copy(out, r.sources)
	return out
}

// Matching returns the sources whose kind the query is interested in.
func (r *Registry) Matching(q media.Query) []Source {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Source
	for _, s := range r.sources {
		if q.WantsKind(s.Kind()) {
			out = append(out, s)
		}
	}
	return out
}

// ByID returns the source with the given id.
func (r *Registry) ByID(id string) (Source, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, s := range r.sources {
		if s.ID() == id {
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
