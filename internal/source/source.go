// Package source defines the plug point for a backend.
//
// Adding a media server to atrium means implementing Source here and a
// provisioner in internal/provision. Those are the two halves of a backend: how
// to search it, and how to get credentials for it without a human typing any.
package source

import (
	"context"
	"sync"

	"github.com/gabehollberg/atrium/internal/media"
)

// Source is one upstream media server.
//
// Implementations must be safe for concurrent use: federated search calls
// Search on every source at once.
type Source interface {
	// ID is the backend's name ("navidrome", "jellyfin"). It appears in
	// results and in setup status, and it is part of atrium's stream URLs.
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
// It is never given to the browser: atrium fetches it server-side and pipes the
// bytes through, which is what lets the backends stay off any published port.
//
// Headers exist because backends disagree about where a credential goes.
// Subsonic puts it in the query string. Jellyfin 12 accepts it only in an
// Authorization header, having dropped both X-Emby-Token and the api_key query
// parameter that older guides still recommend. The proxy should not have to know
// which, so an adapter hands back both parts and the proxy replays them.
type Target struct {
	URL     string
	Headers map[string]string
}

// Streamer builds an authenticated upstream target for an item's bytes.
type Streamer interface {
	StreamTarget(itemID string) (Target, error)
}

// ArtProvider builds an authenticated upstream target for artwork. artID is the
// opaque handle the adapter put in media.Item.ArtID.
type ArtProvider interface {
	ArtTarget(artID string) (Target, error)
}

// Registry holds the live sources.
//
// Unlike the rest of atrium this is mutable at runtime: sources appear as their
// provisioners finish, which can be a minute or more after boot while a backend
// starts up. Every method is safe for concurrent use.
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
