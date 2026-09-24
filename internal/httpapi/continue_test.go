package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/source"
	"github.com/GabrielHollberg/soundstorm/internal/state"
)

// bookShelf stands in for localbooks: it can describe a book by id.
type bookShelf struct{}

func (bookShelf) ID() string                                                { return "ebooks" }
func (bookShelf) Kind() media.Kind                                          { return media.KindEbook }
func (bookShelf) Health(context.Context) error                              { return nil }
func (bookShelf) Search(context.Context, media.Query) ([]media.Item, error) { return nil, nil }
func (bookShelf) ItemByID(_ context.Context, id string) (media.Item, bool) {
	if id == "deleted" {
		return media.Item{}, false
	}
	return media.Item{ID: id, SourceID: "ebooks", Kind: media.KindEbook, Title: "Book " + id}, true
}

// listening stands in for Audiobookshelf: it remembers per person.
type listening struct{ at time.Time }

func (listening) ID() string                                                { return "audiobookshelf" }
func (listening) Kind() media.Kind                                          { return media.KindAudiobook }
func (listening) Health(context.Context) error                              { return nil }
func (listening) Search(context.Context, media.Query) ([]media.Item, error) { return nil, nil }
func (l listening) InProgress(context.Context, int) ([]source.Started, error) {
	return []source.Started{{
		Item:     media.Item{ID: "a1", SourceID: "audiobookshelf", Kind: media.KindAudiobook, Title: "Audiobook"},
		Fraction: 0.3, At: l.at,
	}}, nil
}

func TestContinueMergesNewestFirstAndLeavesOutTheEnds(t *testing.T) {
	now := time.Now()
	h := newHarness(t, bookShelf{}, listening{at: now.Add(-time.Hour)})
	h.signUp(t)
	me := h.ownID(t)

	set := func(item string, fraction float64, ago time.Duration) {
		t.Helper()
		if err := h.api.store.SetProgress(progressKey(me, "ebooks", item),
			state.Progress{Location: "x", Fraction: fraction, UpdatedAt: now.Add(-ago)}, "", 0); err != nil {
			t.Fatal(err)
		}
	}
	set("recent", 0.5, time.Minute)     // newest: first
	set("older", 0.2, 2*time.Hour)      // after the audiobook
	set("finished", 0.99, time.Second)  // at the end: not something to carry on with
	set("unopened", 0.0, time.Second)   // at the start: same
	set("deleted", 0.4, 30*time.Second) // no longer on the shelf

	resp, body := h.do(t, http.MethodGet, "/api/continue", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("continue: %d %s", resp.StatusCode, body)
	}
	var out struct {
		Items []struct {
			ID       string  `json:"id"`
			Progress float64 `json:"progress"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, it := range out.Items {
		ids = append(ids, it.ID)
	}
	want := []string{"recent", "a1", "older"}
	if len(ids) != len(want) {
		t.Fatalf("row = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("row = %v, want %v", ids, want)
		}
	}
	if out.Items[1].Progress != 0.3 {
		t.Errorf("audiobook progress = %v", out.Items[1].Progress)
	}
}

// Somebody else's reading is not in your row.
func TestContinueIsPerPerson(t *testing.T) {
	h := newHarness(t, bookShelf{})
	h.signUp(t)
	me := h.ownID(t)
	if err := h.api.store.SetProgress(progressKey(me, "ebooks", "mine"),
		state.Progress{Location: "x", Fraction: 0.5, UpdatedAt: time.Now()}, "", 0); err != nil {
		t.Fatal(err)
	}
	h.addMember(t, "sam", "sam-password-1")
	sam := h.asUser(t, "sam", "sam-password-1")

	_, body := sam.do(t, http.MethodGet, "/api/continue", "")
	var out struct{ Items []json.RawMessage }
	_ = json.Unmarshal(body, &out)
	if len(out.Items) != 0 {
		t.Errorf("sam sees %d of the owner's books", len(out.Items))
	}
}
