package httpapi

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/federate"
	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/source/storyteller"
)

// Genres across a tab's shelves: music's, films and TV's, books'. Every
// backend already says what genre a thing is, each its own way - Navidrome a
// song's genre, Jellyfin a list, Audiobookshelf a list whose entries are often
// several joined with commas ("Action & Adventure, Dystopian"), an EPUB its
// subjects - so rather than a call per backend, the shelves are listed (out of
// each adapter's shelf cache) and grouped here, one genre per name whatever
// its spelling of case.

const (
	genresDeadline = 15 * time.Second
	genreItemLimit = 1000
)

type genreOut struct {
	Name  string      `json:"name"`
	Count int         `json:"count"`
	Cover *media.Item `json:"cover,omitempty"`
}

// genreNames splits an item's genre field into single genres.
func genreNames(item media.Item) []string {
	raw := item.Extra["genre"]
	if raw == "" {
		raw = item.Extra["tags"]
	}
	var out []string
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ';' }) {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// shelvesOf lists every shelf of the given kinds this account may see.
func (s *Server) shelvesOf(ctx context.Context, kinds map[media.Kind]bool) []media.Item {
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		all []media.Item
	)
	for _, src := range s.reg.All(ctx) {
		kind := src.Kind()
		if !kinds[kind] {
			continue
		}
		if _, synced := src.(*storyteller.Source); synced {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { _ = recover() }()
			items, err := src.Search(ctx, media.Query{Kinds: []media.Kind{kind}, Limit: federate.MaxDepth})
			if err != nil {
				return
			}
			mu.Lock()
			all = append(all, items...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	return all
}

// handleGenres lists the genres of ?kinds=, most common first, or with
// ?name= what is in one.
func (s *Server) handleGenres(w http.ResponseWriter, r *http.Request) {
	kinds := map[media.Kind]bool{}
	for _, k := range strings.Split(r.URL.Query().Get("kinds"), ",") {
		if k = strings.TrimSpace(k); k != "" {
			kinds[media.Kind(k)] = true
		}
	}
	if len(kinds) == 0 || len(kinds) > 4 {
		writeError(w, http.StatusBadRequest, "expected the kinds of shelf, like kinds=music")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), genresDeadline)
	defer cancel()
	items := s.shelvesOf(ctx, kinds)

	if name := strings.TrimSpace(r.URL.Query().Get("name")); name != "" {
		var in []media.Item
		for _, it := range items {
			for _, g := range genreNames(it) {
				if strings.EqualFold(g, name) {
					in = append(in, it)
					break
				}
			}
		}
		sort.SliceStable(in, func(i, j int) bool { return media.Less(in[i], in[j]) })
		if len(in) > genreItemLimit {
			in = in[:genreItemLimit]
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": nonNil(in)})
		return
	}

	byKey := map[string]*genreOut{}
	for _, it := range items {
		seen := map[string]bool{}
		for _, g := range genreNames(it) {
			key := strings.ToLower(g)
			if seen[key] {
				continue
			}
			seen[key] = true
			out := byKey[key]
			if out == nil {
				out = &genreOut{Name: g}
				byKey[key] = out
			}
			out.Count++
			if out.Cover == nil && it.ArtID != "" {
				cover := it
				out.Cover = &cover
			}
		}
	}
	list := make([]*genreOut, 0, len(byKey))
	for _, g := range byKey {
		list = append(list, g)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Count != list[j].Count {
			return list[i].Count > list[j].Count
		}
		return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name)
	})
	writeJSON(w, http.StatusOK, map[string]any{"genres": list})
}
