package httpapi

import (
	"context"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/GabrielHollberg/soundstorm/internal/media"
)

// Books somebody has both as an ebook and as an audiobook, so they can read
// along while they listen. Each shelf keeps its own titles - Audible says
// "Harry Potter and the Sorcerer's Stone, Book 1 [B017V4IM1G]", Calibre says
// "Harry Potter and the Sorcerer's Stone" - so they are matched on a title
// with the edition noise taken out, and on the author's surname. Nothing is
// stored: both shelves are listed and matched on each request, the listings
// coming out of each adapter's shelf cache.

const pairsDeadline = 10 * time.Second

type bookPair struct {
	Ebook     media.Item `json:"ebook"`
	Audiobook media.Item `json:"audiobook"`
}

func (s *Server) handleBookPairs(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), pairsDeadline)
	defer cancel()
	pairs := s.listPairs(ctx)
	out := map[string]any{"pairs": pairs, "readalong": false}
	if st, ok := s.readAlong(r.Context()); ok {
		out["readalong"] = true
		statuses := s.pairStatuses(ctx, st, pairs)
		withStatus := make([]map[string]any, len(pairs))
		for i, p := range pairs {
			entry := map[string]any{"ebook": p.Ebook, "audiobook": p.Audiobook}
			if st, ok := statuses[i]; ok {
				entry["sync"] = st
			}
			withStatus[i] = entry
		}
		out["pairs"] = withStatus
	}
	writeJSON(w, http.StatusOK, out)
}

// listPairs lists both shelves, as far as ctx's account may see them, and
// matches them.
func (s *Server) listPairs(ctx context.Context) []bookPair {
	ebooks, audiobooks := s.listBookShelves(ctx)
	wrong := s.store.NotPairs()
	var pairs []bookPair
	have := map[string]bool{}
	for _, p := range matchBooks(ebooks, audiobooks) {
		key := notPairKey(itemRef{p.Ebook.SourceID, p.Ebook.ID}, itemRef{p.Audiobook.SourceID, p.Audiobook.ID})
		if !wrong[key] {
			pairs = append(pairs, p)
			have[key] = true
		}
	}
	// Pairs made by hand, where both books are still on shelves this account
	// can see.
	byRef := map[string]media.Item{}
	for _, it := range append(append([]media.Item(nil), ebooks...), audiobooks...) {
		byRef[it.SourceID+"/"+it.ID] = it
	}
	for _, key := range s.store.ManualPairs() {
		e, a, ok := strings.Cut(key, "|")
		ebook, okE := byRef[e]
		audiobook, okA := byRef[a]
		if !ok || !okE || !okA || have[key] || ebook.Kind != media.KindEbook || audiobook.Kind != media.KindAudiobook {
			continue
		}
		pairs = append(pairs, bookPair{Ebook: ebook, Audiobook: audiobook})
		have[key] = true
	}
	sort.SliceStable(pairs, func(i, j int) bool {
		return bookKey(pairs[i].Ebook.Title) < bookKey(pairs[j].Ebook.Title)
	})
	if pairs == nil {
		pairs = []bookPair{}
	}
	return pairs
}

func notPairKey(ebook, audiobook itemRef) string {
	return ebook.SourceID + "/" + ebook.ID + "|" + audiobook.SourceID + "/" + audiobook.ID
}

// matchBooks pairs every audiobook with the ebooks of the same book. Two
// editions of one audiobook (the regular recording and a full-cast one) each
// pair with the ebook.
func matchBooks(ebooks, audiobooks []media.Item) []bookPair {
	byTitle := map[string][]media.Item{}
	for _, e := range ebooks {
		if k := bookKey(e.Title); k != "" {
			byTitle[k] = append(byTitle[k], e)
		}
	}
	pairs := []bookPair{}
	for _, a := range audiobooks {
		for _, e := range byTitle[bookKey(a.Title)] {
			if sameAuthor(e.Creators, a.Creators) {
				pairs = append(pairs, bookPair{Ebook: e, Audiobook: a})
			}
		}
	}
	sort.SliceStable(pairs, func(i, j int) bool {
		ki, kj := bookKey(pairs[i].Ebook.Title), bookKey(pairs[j].Ebook.Title)
		if ki != kj {
			return ki < kj
		}
		return pairs[i].Audiobook.Title < pairs[j].Audiobook.Title
	})
	return pairs
}

var (
	bracketed   = regexp.MustCompile(`\s*[\[(][^\])]*[\])]`)
	bookNumber  = regexp.MustCompile(`[,:]?\s*\b(book|volume|vol|part)\s+\d+\s*$`)
	editionNote = regexp.MustCompile(`\b(unabridged|abridged)\b`)
)

// bookKey is a title with what differs between editions taken out: anything
// in brackets (an ASIN, "Unabridged", "Full-Cast Edition"), a subtitle after a
// colon, "Book 1" at the end, curly quotes, punctuation, case, and a leading
// article.
func bookKey(title string) string {
	t := strings.ToLower(title)
	t = strings.NewReplacer("’", "'", "‘", "'", "“", `"`, "”", `"`, "&", " and ").Replace(t)
	t = bracketed.ReplaceAllString(t, "")
	if i := strings.Index(t, ":"); i > 0 {
		t = t[:i]
	}
	t = bookNumber.ReplaceAllString(t, "")
	t = editionNote.ReplaceAllString(t, "")
	t = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		if r == '\'' {
			return -1 // "sorcerer's" and "sorcerers" alike
		}
		return ' '
	}, t)
	words := strings.Fields(t)
	if len(words) > 1 && (words[0] == "the" || words[0] == "a" || words[0] == "an") {
		words = words[1:]
	}
	return strings.Join(words, " ")
}

// sameAuthor reports whether two lists of authors share a surname. A side
// with no author at all is taken on the title alone.
func sameAuthor(a, b []string) bool {
	as, bs := surnames(a), surnames(b)
	if len(as) == 0 || len(bs) == 0 {
		return true
	}
	for s := range as {
		if bs[s] {
			return true
		}
	}
	return false
}

// surnames reads "J.K. Rowling" and the sort form "Rowling, J.K." alike.
func surnames(names []string) map[string]bool {
	out := map[string]bool{}
	for _, n := range names {
		for _, one := range strings.Split(n, " & ") {
			var last string
			if i := strings.Index(one, ","); i > 0 {
				last = one[:i]
			} else if f := strings.Fields(one); len(f) > 0 {
				last = f[len(f)-1]
			}
			last = strings.ToLower(strings.TrimFunc(last, func(r rune) bool { return !unicode.IsLetter(r) }))
			if last != "" {
				out[last] = true
			}
		}
	}
	return out
}
