package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/GabrielHollberg/soundstorm/internal/media"
)

// fileShelf is a music source that knows where its items' files are - the
// shape every real adapter has, reduced to a map.
type fileShelf struct {
	files map[string][]string
}

func (f *fileShelf) ID() string                                                { return "navidrome" }
func (f *fileShelf) Kind() media.Kind                                          { return media.KindMusic }
func (f *fileShelf) Health(context.Context) error                              { return nil }
func (f *fileShelf) Search(context.Context, media.Query) ([]media.Item, error) { return nil, nil }
func (f *fileShelf) ItemFiles(_ context.Context, id string) ([]string, error) {
	if files, ok := f.files[id]; ok {
		return files, nil
	}
	return nil, fmt.Errorf("no item %q", id)
}

func putFile(t *testing.T, root, rel string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("audio"), 0o666); err != nil {
		t.Fatal(err)
	}
}

const oneSong = `{"items":[{"source":"navidrome","id":"s1","title":"Aria"}]}`

// Only the owner deletes: every other account shares these shelves with the
// rest of the house.
func TestOnlyTheOwnerCanDelete(t *testing.T) {
	shelf := &fileShelf{files: map[string][]string{"s1": {"Artist/Album/01 Aria.mp3"}}}
	h := newHarness(t, shelf)
	h.signUp(t)
	putFile(t, h.root, "music/Artist/Album/01 Aria.mp3")
	h.addMember(t, "sam", "sam-password-1")
	member := h.asUser(t, "sam", "sam-password-1")

	for _, path := range []string{"/api/delete/preview", "/api/delete", "/api/delete/undo"} {
		resp, _ := member.do(t, http.MethodPost, path, oneSong)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("a member's %s = %d, want 403", path, resp.StatusCode)
		}
	}
	if _, err := os.Stat(filepath.Join(h.root, "music/Artist/Album/01 Aria.mp3")); err != nil {
		t.Error("a member's request deleted the file")
	}
}

func TestPreviewDeleteAndUndo(t *testing.T) {
	shelf := &fileShelf{files: map[string][]string{"s1": {"Artist/Album/01 Aria.mp3"}}}
	h := newHarness(t, shelf)
	h.signUp(t)
	putFile(t, h.root, "music/Artist/Album/01 Aria.mp3")
	putFile(t, h.root, "music/Artist/Album/cover.jpg")
	song := filepath.Join(h.root, "music/Artist/Album/01 Aria.mp3")

	resp, body := h.do(t, http.MethodPost, "/api/delete/preview", oneSong)
	var preview struct{ Items, Files int }
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &preview) != nil {
		t.Fatalf("preview: %d %s", resp.StatusCode, body)
	}
	// The album's only track, so its cover goes with it.
	if preview.Items != 1 || preview.Files != 2 {
		t.Errorf("preview = %+v, want 1 item and 2 files", preview)
	}
	if _, err := os.Stat(song); err != nil {
		t.Fatal("a preview moved something")
	}

	resp, body = h.do(t, http.MethodPost, "/api/delete", oneSong)
	var done struct{ Entry string }
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &done) != nil || done.Entry == "" {
		t.Fatalf("delete: %d %s", resp.StatusCode, body)
	}
	if _, err := os.Stat(song); !os.IsNotExist(err) {
		t.Error("the song is still on the shelf")
	}

	resp, body = h.do(t, http.MethodPost, "/api/delete/undo", `{"entry":"`+done.Entry+`"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("undo: %d %s", resp.StatusCode, body)
	}
	if _, err := os.Stat(song); err != nil {
		t.Error("undo did not put the song back")
	}
}

// A shelf that names a path outside itself is refused, and nothing moves.
func TestDeleteRefusesAPathOutsideTheShelf(t *testing.T) {
	shelf := &fileShelf{files: map[string][]string{"s1": {"../ebooks/book.epub"}}}
	h := newHarness(t, shelf)
	h.signUp(t)
	putFile(t, h.root, "ebooks/book.epub")

	resp, _ := h.do(t, http.MethodPost, "/api/delete", oneSong)
	if resp.StatusCode == http.StatusOK {
		t.Fatal("a path outside the shelf was accepted")
	}
	if _, err := os.Stat(filepath.Join(h.root, "ebooks/book.epub")); err != nil {
		t.Error("a file outside the shelf was moved")
	}
}
