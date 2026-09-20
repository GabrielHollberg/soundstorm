// Package media defines the vocabulary every backend is translated into.
//
// The point of atrium is that a song from Navidrome and a film from Jellyfin
// arrive at the browser as the same shape, from the same origin, behind the
// same login. Nothing downstream of an adapter knows which server answered.
package media

import "strings"

// Kind is the broad category of a piece of media. It is deliberately coarse:
// it exists so the UI can group results and pick a player, not to model every
// distinction a backend makes.
type Kind string

const (
	KindMusic     Kind = "music"
	KindAudiobook Kind = "audiobook"
	KindEbook     Kind = "ebook"
	KindVideo     Kind = "video"
)

// AllKinds is the set of kinds atrium understands.
func AllKinds() []Kind {
	return []Kind{KindMusic, KindAudiobook, KindEbook, KindVideo}
}

// Valid reports whether k is a kind atrium knows about.
func (k Kind) Valid() bool {
	for _, known := range AllKinds() {
		if k == known {
			return true
		}
	}
	return false
}

// ParseKind converts user input ("Music", " video ") into a Kind.
func ParseKind(s string) (Kind, bool) {
	k := Kind(strings.ToLower(strings.TrimSpace(s)))
	return k, k.Valid()
}

// Player is the browser element that can play this kind.
func (k Kind) Player() string {
	switch k {
	case KindMusic, KindAudiobook:
		return "audio"
	case KindVideo:
		return "video"
	default:
		return "none"
	}
}

// Item is one result, normalized across every backend.
//
// Deliberately absent: any URL pointing at an upstream server. The backends
// are not reachable from the browser, so an Item carries the identifiers
// atrium needs to fetch bytes on the client's behalf, and the UI builds
// atrium-relative paths from SourceID/ID/ArtID.
type Item struct {
	// ID is unique within a source, not globally. Pair it with SourceID.
	ID       string `json:"id"`
	SourceID string `json:"sourceId"`
	Kind     Kind   `json:"kind"`

	Title    string   `json:"title"`
	Subtitle string   `json:"subtitle,omitempty"`
	Creators []string `json:"creators,omitempty"` // artist, author, director
	Year     int      `json:"year,omitempty"`

	// ArtID is an opaque, source-specific artwork handle. Empty means the
	// source has no artwork for this item. It is not always equal to ID:
	// Subsonic cover art has its own id space ("al-42" for song "300").
	ArtID string `json:"artId,omitempty"`

	DurationSeconds float64 `json:"durationSeconds,omitempty"`

	// Score is the relevance assigned at federation time, 0..1.
	Score float64 `json:"score"`

	// Extra carries source-specific fields worth showing but not worth
	// promoting into the common shape (album, narrator, series, rating...).
	Extra map[string]string `json:"extra,omitempty"`
}

// Query is a federated search request.
type Query struct {
	Text  string
	Kinds []Kind // empty means "all kinds"
	Limit int    // per-source cap; 0 means the source's own default
}

// WantsKind reports whether this query is interested in kind k.
func (q Query) WantsKind(k Kind) bool {
	if len(q.Kinds) == 0 {
		return true
	}
	for _, want := range q.Kinds {
		if want == k {
			return true
		}
	}
	return false
}

// LimitOr returns the query's limit, or def when unset.
func (q Query) LimitOr(def int) int {
	if q.Limit <= 0 {
		return def
	}
	return q.Limit
}
