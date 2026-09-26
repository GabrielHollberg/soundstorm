package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/GabrielHollberg/soundstorm/internal/media"
)

func TestBookKeyTakesOutEditionNoise(t *testing.T) {
	want := bookKey("Harry Potter and the Sorcerer's Stone")
	for _, title := range []string{
		"Harry Potter and the Sorcerer's Stone, Book 1 [B017V4IM1G]",
		"Harry Potter and the Sorcerer’s Stone (Full-Cast Edition) [B0F14RPXHR]",
		"Harry Potter and the Sorcerers Stone (Unabridged)",
		"HARRY POTTER AND THE SORCERER'S STONE: A Novel",
	} {
		if got := bookKey(title); got != want {
			t.Errorf("bookKey(%q) = %q, want %q", title, got, want)
		}
	}
	if bookKey("The Hobbit") != bookKey("Hobbit") {
		t.Error("a leading article should not matter")
	}
	if bookKey("Dune") == bookKey("Dune Messiah") {
		t.Error("different books must not share a key")
	}
}

func TestMatchBooksPairsEveryEditionOnTitleAndAuthor(t *testing.T) {
	ebooks := []media.Item{
		{ID: "e1", Title: "Harry Potter and the Sorcerer's Stone", Creators: []string{"Rowling, J.K."}},
		{ID: "e2", Title: "Atomic Habits", Creators: []string{"James Clear"}},
		{ID: "e3", Title: "Emma", Creators: []string{"Jane Austen"}},
	}
	audiobooks := []media.Item{
		{ID: "a1", Title: "Harry Potter and the Sorcerer's Stone, Book 1 [B017V4IM1G]", Creators: []string{"J.K. Rowling"}},
		{ID: "a2", Title: "Harry Potter and the Sorcerer’s Stone (Full-Cast Edition)", Creators: []string{"J.K. Rowling"}},
		{ID: "a3", Title: "Atomic Habits [1524779261]", Creators: []string{"James Clear"}},
		// Same title, somebody else's book.
		{ID: "a4", Title: "Emma", Creators: []string{"Somebody Else"}},
	}
	pairs := matchBooks(ebooks, audiobooks)
	got := map[string]bool{}
	for _, p := range pairs {
		got[p.Ebook.ID+"+"+p.Audiobook.ID] = true
	}
	for _, want := range []string{"e1+a1", "e1+a2", "e2+a3"} {
		if !got[want] {
			t.Errorf("missing pair %s (got %v)", want, got)
		}
	}
	if got["e3+a4"] {
		t.Error("an audiobook by a different author was paired on its title alone")
	}
	if len(pairs) != 3 {
		t.Errorf("%d pairs, want 3", len(pairs))
	}
}

// A match the owner marks as not the same book leaves Read & listen, and
// marked right again it comes back.
func TestNotTheSameBookLeavesReadAndListen(t *testing.T) {
	ebooks := stub{id: "ebooks", kind: media.KindEbook, items: []media.Item{
		{ID: "e1", SourceID: "ebooks", Kind: media.KindEbook, Title: "Emma", Creators: []string{"Jane Austen"}},
	}}
	audiobooks := stub{id: "audiobookshelf", kind: media.KindAudiobook, items: []media.Item{
		{ID: "a1", SourceID: "audiobookshelf", Kind: media.KindAudiobook, Title: "Emma [B000]", Creators: []string{"Jane Austen"}},
	}}
	h := newHarness(t, ebooks, audiobooks)
	h.signUp(t)

	count := func() int {
		_, body := h.do(t, http.MethodGet, "/api/books/pairs", "")
		var got struct {
			Pairs []json.RawMessage `json:"pairs"`
		}
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("pairs: %v (%s)", err, body)
		}
		return len(got.Pairs)
	}
	if n := count(); n != 1 {
		t.Fatalf("%d pairs before, want 1", n)
	}
	mark := func(wrong bool) {
		body := `{"ebook":{"sourceId":"ebooks","id":"e1"},"audiobook":{"sourceId":"audiobookshelf","id":"a1"},"wrong":` +
			strconv.FormatBool(wrong) + `}`
		if resp, b := h.do(t, http.MethodPost, "/api/books/pairs/not-same", body); resp.StatusCode != http.StatusOK {
			t.Fatalf("not-same %v: %d %s", wrong, resp.StatusCode, b)
		}
	}
	mark(true)
	if n := count(); n != 0 {
		t.Errorf("%d pairs after marking it wrong, want 0", n)
	}
	mark(false)
	if n := count(); n != 1 {
		t.Errorf("%d pairs after undoing, want 1", n)
	}
}

// Every owner setting answers - an owner route registered but not mounted is
// a 404, which the read-along switch was.
func TestOwnerSettingsAnswer(t *testing.T) {
	h := newHarness(t)
	h.signUp(t)
	for _, path := range []string{"/api/settings/lyrics", "/api/settings/readalong"} {
		resp, body := h.do(t, http.MethodPut, path, `{"enabled":false}`)
		if resp.StatusCode == http.StatusNotFound {
			t.Errorf("%s: 404 %s", path, body)
		}
	}
}

// Books the matching misses can be paired by hand, and unpaired.
func TestPairingByHand(t *testing.T) {
	ebooks := stub{id: "ebooks", kind: media.KindEbook, items: []media.Item{
		{ID: "e1", SourceID: "ebooks", Kind: media.KindEbook, Title: "The Hobbit", Creators: []string{"J.R.R. Tolkien"}},
	}}
	audiobooks := stub{id: "audiobookshelf", kind: media.KindAudiobook, items: []media.Item{
		{ID: "a1", SourceID: "audiobookshelf", Kind: media.KindAudiobook, Title: "There and Back Again", Creators: []string{"Tolkien"}},
	}}
	h := newHarness(t, ebooks, audiobooks)
	h.signUp(t)
	count := func() int {
		_, body := h.do(t, http.MethodGet, "/api/books/pairs", "")
		var got struct {
			Pairs []json.RawMessage `json:"pairs"`
		}
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("pairs: %v (%s)", err, body)
		}
		return len(got.Pairs)
	}
	if n := count(); n != 0 {
		t.Fatalf("%d pairs before, want 0 - these titles should not match", n)
	}
	pair := func(on bool) {
		body := `{"ebook":{"sourceId":"ebooks","id":"e1"},"audiobook":{"sourceId":"audiobookshelf","id":"a1"},"paired":` +
			strconv.FormatBool(on) + `}`
		if resp, b := h.do(t, http.MethodPost, "/api/books/pairs/by-hand", body); resp.StatusCode != http.StatusOK {
			t.Fatalf("by-hand %v: %d %s", on, resp.StatusCode, b)
		}
	}
	pair(true)
	if n := count(); n != 1 {
		t.Errorf("%d pairs after pairing by hand, want 1", n)
	}
	pair(false)
	if n := count(); n != 0 {
		t.Errorf("%d pairs after unpairing, want 0", n)
	}
	// Backwards is refused.
	if resp, _ := h.do(t, http.MethodPost, "/api/books/pairs/by-hand",
		`{"ebook":{"sourceId":"audiobookshelf","id":"a1"},"audiobook":{"sourceId":"ebooks","id":"e1"},"paired":true}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a backwards pair answered %d", resp.StatusCode)
	}
}

// Authors read the sort form and the reading form as one person, across
// both shelves, and an author's page splits series (in order) from the rest.
func TestAuthorsAndSeries(t *testing.T) {
	ebooks := stub{id: "ebooks", kind: media.KindEbook, items: []media.Item{
		{ID: "e1", SourceID: "ebooks", Kind: media.KindEbook, Title: "Dune", Creators: []string{"Herbert, Frank"},
			Extra: map[string]string{"series": "Dune", "seriesIndex": "1"}},
		{ID: "e2", SourceID: "ebooks", Kind: media.KindEbook, Title: "The Dosadi Experiment", Creators: []string{"Frank Herbert"}},
	}}
	audiobooks := stub{id: "audiobookshelf", kind: media.KindAudiobook, items: []media.Item{
		{ID: "a1", SourceID: "audiobookshelf", Kind: media.KindAudiobook, Title: "Dune Messiah", Creators: []string{"Frank Herbert"},
			Extra: map[string]string{"series": "Dune #2"}},
		{ID: "a2", SourceID: "audiobookshelf", Kind: media.KindAudiobook, Title: "Good Omens", Creators: []string{"Terry Pratchett, Neil Gaiman"}},
	}}
	h := newHarness(t, ebooks, audiobooks)
	h.signUp(t)
	var list struct {
		Authors []struct {
			Key, Name string
			Count     int
		}
	}
	_, body := h.do(t, http.MethodGet, "/api/books/authors", "")
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("authors: %v (%s)", err, body)
	}
	names := map[string]int{}
	for _, a := range list.Authors {
		names[a.Name] = a.Count
	}
	if names["Frank Herbert"] != 3 || names["Terry Pratchett"] != 1 || names["Neil Gaiman"] != 1 || len(names) != 3 {
		t.Fatalf("authors = %v, want Frank Herbert 3, Terry Pratchett 1, Neil Gaiman 1", names)
	}
	var page struct {
		Series []struct {
			Name  string
			Books []struct{ ID string }
		}
		Books []struct{ ID string }
	}
	_, body = h.do(t, http.MethodGet, "/api/books/authors?key=frankherbert", "")
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("author: %v (%s)", err, body)
	}
	if len(page.Series) != 1 || page.Series[0].Name != "Dune" || len(page.Series[0].Books) != 2 ||
		page.Series[0].Books[0].ID != "e1" || page.Series[0].Books[1].ID != "a1" {
		t.Errorf("series = %+v, want Dune: e1 then a1", page.Series)
	}
	if len(page.Books) != 1 || page.Books[0].ID != "e2" {
		t.Errorf("other books = %+v, want e2", page.Books)
	}
	var series struct {
		Series []struct {
			Name  string
			Count int
		}
	}
	_, body = h.do(t, http.MethodGet, "/api/books/series", "")
	if err := json.Unmarshal(body, &series); err != nil || len(series.Series) != 1 || series.Series[0].Count != 2 {
		t.Errorf("series list = %s", body)
	}
}
