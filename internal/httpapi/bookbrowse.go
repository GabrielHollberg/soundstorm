package httpapi

import (
	"context"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/GabrielHollberg/soundstorm/internal/federate"
	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/source/storyteller"
)

// Books by author and by series, across the ebook and audiobook shelves
// together: an author's page holds their series and their other books,
// whichever shelf each is on. Nothing is stored. Both shelves are listed on
// each request, out of each adapter's shelf cache, and grouped - the same
// shape as Read Along.
//
// Each shelf names people its own way - Calibre often in the sort form
// "Herbert, Frank", Audible as "Frank Herbert" - so authors are grouped on a
// key that reads both alike, and shown in the reading form. Series come from
// the shelves' own metadata: Calibre's series and index, and Audiobookshelf's
// seriesName, which carries the number in it ("Dune #2").

// listBookShelves lists both book shelves, as far as ctx's account may see
// them: through the registry, so a shelf it may not see is never listed.
func (s *Server) listBookShelves(ctx context.Context) (ebooks, audiobooks []media.Item) {
	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	for _, src := range s.reg.All(ctx) {
		kind := src.Kind()
		if kind != media.KindEbook && kind != media.KindAudiobook {
			continue
		}
		if _, synced := src.(*storyteller.Source); synced {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { _ = recover() }() // one shelf, never the request
			items, err := src.Search(ctx, media.Query{Kinds: []media.Kind{kind}, Limit: federate.MaxDepth})
			if err != nil {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if kind == media.KindEbook {
				ebooks = append(ebooks, items...)
			} else {
				audiobooks = append(audiobooks, items...)
			}
		}()
	}
	wg.Wait()
	return ebooks, audiobooks
}

// readingName turns the sort form "Herbert, Frank" into "Frank Herbert". A
// name with no comma, or more than one, is left as it is.
func readingName(name string) string {
	name = strings.TrimSpace(name)
	if strings.Count(name, ",") != 1 {
		return name
	}
	last, first, _ := strings.Cut(name, ",")
	first, last = strings.TrimSpace(first), strings.TrimSpace(last)
	if first == "" || last == "" {
		return name
	}
	return first + " " + last
}

// nameKey is what two spellings of one name share: the reading form,
// lower-case, letters and digits only. "J.K. Rowling" and "Rowling, J. K."
// come out the same.
func nameKey(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(readingName(name)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// authorsOf splits a book's creators into single people. "A & B", "A and B"
// and "A; B" are two. A comma is either a list - Audiobookshelf joins
// several authors with one, "Terry Pratchett, Neil Gaiman" - or the sort form
// of one name, "Herbert, Frank": one word on either side of a single comma
// means the sort form.
func authorsOf(item media.Item) []string {
	var out []string
	add := func(name string) {
		if name = strings.TrimSpace(name); name != "" {
			out = append(out, readingName(name))
		}
	}
	for _, c := range item.Creators {
		for _, part := range authorSplit.Split(c, -1) {
			pieces := strings.Split(part, ",")
			if len(pieces) == 2 && (!strings.Contains(strings.TrimSpace(pieces[0]), " ") ||
				!strings.Contains(strings.TrimSpace(pieces[1]), " ")) {
				add(part)
				continue
			}
			for _, piece := range pieces {
				add(piece)
			}
		}
	}
	return out
}

var authorSplit = regexp.MustCompile(`\s*(?:&|;|\band\b)\s*`)

// seriesNumber reads "Dune #2" (Audiobookshelf) as series "Dune", number 2.
var seriesNumber = regexp.MustCompile(`^(.*?)\s*#\s*([0-9]+(?:\.[0-9]+)?)\s*$`)

// seriesOf says which series a book is in, and where, from whichever field
// its shelf fills in. Only the first series of several.
func seriesOf(item media.Item) (name string, index float64, ok bool) {
	raw := strings.TrimSpace(item.Extra["series"])
	if raw == "" {
		return "", 0, false
	}
	if i := strings.Index(raw, ", "); i > 0 && strings.Contains(raw[i:], "#") {
		raw = raw[:i] // "A #1, B #3": the first
	}
	name = raw
	if m := seriesNumber.FindStringSubmatch(raw); m != nil {
		name = m[1]
		index, _ = strconv.ParseFloat(m[2], 64)
	}
	if v, err := strconv.ParseFloat(item.Extra["seriesIndex"], 64); err == nil {
		index = v
	}
	name = strings.TrimSpace(name)
	return name, index, name != ""
}

type bookGroup struct {
	Key     string       `json:"key"`
	Name    string       `json:"name"`
	Authors []string     `json:"authors,omitempty"`
	Count   int          `json:"count"`
	Cover   *media.Item  `json:"cover,omitempty"`
	Series  []*bookGroup `json:"series,omitempty"`
	Books   []media.Item `json:"books,omitempty"`
	sortKey string
	indexes map[string]float64
}

func (g *bookGroup) add(item media.Item) {
	g.Books = append(g.Books, item)
	g.Count++
	if g.Cover == nil && item.ArtID != "" {
		cover := item
		g.Cover = &cover
	}
}

func matchesWords(words []string, texts ...string) bool {
	all := strings.ToLower(strings.Join(texts, " "))
	for _, w := range words {
		if !strings.Contains(all, w) {
			return false
		}
	}
	return true
}

// handleAuthors lists authors, or with ?key= one author's series and books.
func (s *Server) handleAuthors(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), pairsDeadline)
	defer cancel()
	ebooks, audiobooks := s.listBookShelves(ctx)
	authors := map[string]*bookGroup{}
	for _, item := range append(ebooks, audiobooks...) {
		for _, name := range authorsOf(item) {
			key := nameKey(name)
			if key == "" {
				continue
			}
			a := authors[key]
			if a == nil {
				a = &bookGroup{Key: key, Name: name, sortKey: surnameFirst(name)}
				authors[key] = a
			}
			a.add(item)
		}
	}
	if key := r.URL.Query().Get("key"); key != "" {
		a := authors[key]
		if a == nil {
			writeError(w, http.StatusNotFound, "no such author")
			return
		}
		writeJSON(w, http.StatusOK, authorPage(a))
		return
	}
	words := strings.Fields(strings.ToLower(r.URL.Query().Get("q")))
	list := make([]*bookGroup, 0, len(authors))
	for _, a := range authors {
		if matchesWords(words, a.Name) {
			list = append(list, &bookGroup{Key: a.Key, Name: a.Name, Count: a.Count, Cover: a.Cover, sortKey: a.sortKey})
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].sortKey != list[j].sortKey {
			return list[i].sortKey < list[j].sortKey
		}
		return list[i].Key < list[j].Key
	})
	writeJSON(w, http.StatusOK, map[string]any{"authors": list})
}

// surnameFirst orders "Frank Herbert" under H.
func surnameFirst(name string) string {
	f := strings.Fields(strings.ToLower(name))
	if len(f) < 2 {
		return strings.ToLower(name)
	}
	return f[len(f)-1] + " " + strings.Join(f[:len(f)-1], " ")
}

// authorPage splits an author's books into their series, each in order, and
// the rest by title.
func authorPage(a *bookGroup) *bookGroup {
	page := &bookGroup{Key: a.Key, Name: a.Name, Count: a.Count, Cover: a.Cover}
	series := map[string]*bookGroup{}
	for _, item := range a.Books {
		name, index, ok := seriesOf(item)
		if !ok {
			page.Books = append(page.Books, item)
			continue
		}
		key := nameKey(name)
		g := series[key]
		if g == nil {
			g = &bookGroup{Key: key, Name: name, indexes: map[string]float64{}}
			series[key] = g
			page.Series = append(page.Series, g)
		}
		g.indexes[item.SourceID+"/"+item.ID] = index
		g.add(item)
	}
	for _, g := range page.Series {
		sortSeries(g)
	}
	sort.Slice(page.Series, func(i, j int) bool {
		return strings.ToLower(page.Series[i].Name) < strings.ToLower(page.Series[j].Name)
	})
	sort.SliceStable(page.Books, func(i, j int) bool { return media.Less(page.Books[i], page.Books[j]) })
	return page
}

func sortSeries(g *bookGroup) {
	sort.SliceStable(g.Books, func(i, j int) bool {
		a, b := g.indexes[g.Books[i].SourceID+"/"+g.Books[i].ID], g.indexes[g.Books[j].SourceID+"/"+g.Books[j].ID]
		if a != b {
			return a < b
		}
		return media.Less(g.Books[i], g.Books[j])
	})
}

// handleSeries lists series, or with ?key= one series' books in order.
func (s *Server) handleSeries(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), pairsDeadline)
	defer cancel()
	ebooks, audiobooks := s.listBookShelves(ctx)
	series := map[string]*bookGroup{}
	var order []*bookGroup
	for _, item := range append(ebooks, audiobooks...) {
		name, index, ok := seriesOf(item)
		if !ok {
			continue
		}
		key := nameKey(name)
		g := series[key]
		if g == nil {
			g = &bookGroup{Key: key, Name: name, indexes: map[string]float64{}}
			series[key] = g
			order = append(order, g)
		}
		g.indexes[item.SourceID+"/"+item.ID] = index
		g.add(item)
		for _, a := range authorsOf(item) {
			if !containsFold(g.Authors, a) {
				g.Authors = append(g.Authors, a)
			}
		}
	}
	if key := r.URL.Query().Get("key"); key != "" {
		g := series[key]
		if g == nil {
			writeError(w, http.StatusNotFound, "no such series")
			return
		}
		sortSeries(g)
		writeJSON(w, http.StatusOK, g)
		return
	}
	words := strings.Fields(strings.ToLower(r.URL.Query().Get("q")))
	list := make([]*bookGroup, 0, len(order))
	for _, g := range order {
		if !matchesWords(words, append([]string{g.Name}, g.Authors...)...) {
			continue
		}
		// The cover is the first book's, as the series page starts there.
		sortSeries(g)
		cover := g.Cover
		for i := range g.Books {
			if g.Books[i].ArtID != "" {
				cover = &g.Books[i]
				break
			}
		}
		list = append(list, &bookGroup{Key: g.Key, Name: g.Name, Authors: g.Authors, Count: g.Count, Cover: cover})
	}
	sort.Slice(list, func(i, j int) bool {
		a, b := bookKey(list[i].Name), bookKey(list[j].Name)
		if a != b {
			return a < b
		}
		return list[i].Key < list[j].Key
	})
	writeJSON(w, http.StatusOK, map[string]any{"series": list})
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}
