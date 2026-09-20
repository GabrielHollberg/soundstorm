// Package media defines the vocabulary every source is translated into.
//
// The whole point of atrium is that a song from Navidrome, a chapter from
// Audiobookshelf, an epub from Calibre, a film from Jellyfin and a Wikipedia
// article from Kiwix all arrive at the client as the same shape.
package media

import "strings"

// Kind is the broad category of a piece of media. It is deliberately coarse:
// it exists so a client can group results and a user can filter them, not to
// model every distinction a backend makes.
type Kind string

const (
	KindMusic     Kind = "music"
	KindAudiobook Kind = "audiobook"
	KindEbook     Kind = "ebook"
	KindVideo     Kind = "video"
	KindArticle   Kind = "article"
)

// AllKinds is the set of kinds atrium understands.
func AllKinds() []Kind {
	return []Kind{KindMusic, KindAudiobook, KindEbook, KindVideo, KindArticle}
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

// ParseKind converts user input ("Music", " ebook ") into a Kind.
func ParseKind(s string) (Kind, bool) {
	k := Kind(strings.ToLower(strings.TrimSpace(s)))
	return k, k.Valid()
}

// Item is one result, normalized across every backend.
type Item struct {
	// ID is unique within a source, not globally. Pair it with SourceID.
	ID       string `json:"id"`
	SourceID string `json:"sourceId"`
	Kind     Kind   `json:"kind"`

	Title    string   `json:"title"`
	Subtitle string   `json:"subtitle,omitempty"`
	Creators []string `json:"creators,omitempty"` // artist, author, director
	Year     int      `json:"year,omitempty"`

	// CoverURL and OpenURL are absolute URLs pointing at the upstream service.
	// atrium does not proxy media bytes; it tells the client where to go.
	CoverURL string `json:"coverUrl,omitempty"`
	OpenURL  string `json:"openUrl,omitempty"`

	DurationSeconds float64 `json:"durationSeconds,omitempty"`

	// Score is the relevance assigned at federation time, 0..1.
	Score float64 `json:"score"`

	// Extra carries source-specific fields worth surfacing but not worth
	// promoting into the common shape (album, narrator, series, ISBN...).
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
