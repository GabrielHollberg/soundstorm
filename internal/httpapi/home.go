package httpapi

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/source"
)

// The home page: a row of what arrived lately on each shelf, and the newest
// albums. Every shelf is asked at once and given a few seconds; one that is
// slow or down is left out rather than holding the page up - the same rule
// the search lives by. The rows that are about this person (carry on,
// favourites, recently played) come from the endpoints that already answer
// those.

const (
	homeRowSize  = 12
	homeDeadline = 5 * time.Second
)

// homeKinds is the order the shelf rows appear in, after the music.
var homeKinds = []media.Kind{
	media.KindVideo, media.KindTV, media.KindAudiobook, media.KindEbook,
	media.KindDocument, media.KindPicture,
}

type homeShelf struct {
	Kind  media.Kind   `json:"kind"`
	Items []media.Item `json:"items"`
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), homeDeadline)
	defer cancel()

	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		albums []source.Album
		byKind = map[media.Kind][]media.Item{}
	)
	// Through the registry, so a shelf this account may not see is never asked.
	for _, src := range s.reg.All(r.Context()) {
		if b, ok := src.(source.MusicBrowser); ok {
			wg.Add(1)
			go func(b source.MusicBrowser) {
				defer wg.Done()
				defer func() { _ = recover() }() // one shelf, never the page
				got, err := b.Albums(ctx, source.AlbumsNewest, 0, homeRowSize)
				if err != nil {
					return
				}
				mu.Lock()
				albums = append(albums, got...)
				mu.Unlock()
			}(b)
		}
		if lister, ok := src.(source.RecentLister); ok {
			wg.Add(1)
			go func(kind media.Kind, lister source.RecentLister) {
				defer wg.Done()
				defer func() { _ = recover() }()
				got, err := lister.Recent(ctx, homeRowSize)
				if err != nil || len(got) == 0 {
					return
				}
				mu.Lock()
				byKind[kind] = append(byKind[kind], got...)
				mu.Unlock()
			}(src.Kind(), lister)
		}
	}
	wg.Wait()

	shelves := []homeShelf{}
	for _, kind := range homeKinds {
		if items := byKind[kind]; len(items) > 0 {
			if len(items) > homeRowSize {
				items = items[:homeRowSize]
			}
			shelves = append(shelves, homeShelf{Kind: kind, Items: items})
		}
	}
	if len(albums) > homeRowSize {
		albums = albums[:homeRowSize]
	}
	writeJSON(w, http.StatusOK, map[string]any{"albums": nonNil(albums), "shelves": shelves})
}
