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
