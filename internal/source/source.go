// Package source defines the plug point for a backend.
//
// Adding a new media server to atrium means implementing Source and
// registering a constructor in internal/config. Nothing else changes.
package source

import (
	"context"

	"github.com/gabehollberg/atrium/internal/media"
)

// Source is one upstream media server.
//
// Implementations must be safe for concurrent use: federated search calls
// Search on every source at once.
type Source interface {
	// ID is the operator-chosen name for this instance ("navidrome",
	// "jellyfin-4k"). It appears in results and in health output.
	ID() string

	// Kind is what this source serves. A source serves exactly one kind;
	// a server that serves two (Jellyfin doing both video and music) is
	// configured as two sources pointing at the same host.
	Kind() media.Kind

	// Search returns matches, already normalized. An error here is not fatal
	// to the overall search - the federator records it and carries on.
	Search(ctx context.Context, q media.Query) ([]media.Item, error)

	// Health reports whether the source is reachable and authenticated.
	Health(ctx context.Context) error
}

// Prober is an optional interface. A source that implements it can return the
// raw upstream response for a query, which is how you fix a field mapping
// against a real server instead of guessing at its schema.
type Prober interface {
	Probe(ctx context.Context, q media.Query) (body []byte, contentType string, err error)
}

// Registry holds the configured sources.
type Registry struct {
	sources []Source
}

// NewRegistry builds a registry from the given sources.
func NewRegistry(sources ...Source) *Registry {
	r := &Registry{}
	for _, s := range sources {
		r.Add(s)
	}
	return r
}

// Add appends a source. Not safe for concurrent use with All or Matching;
// build the registry fully at startup, then treat it as read-only.
func (r *Registry) Add(s Source) {
	if s != nil {
		r.sources = append(r.sources, s)
	}
}

// All returns every registered source.
func (r *Registry) All() []Source {
	out := make([]Source, len(r.sources))
	copy(out, r.sources)
	return out
}

// Matching returns the sources whose kind the query is interested in.
func (r *Registry) Matching(q media.Query) []Source {
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
	for _, s := range r.sources {
		if s.ID() == id {
			return s, true
		}
	}
	return nil, false
}

// Len reports how many sources are registered.
func (r *Registry) Len() int { return len(r.sources) }
