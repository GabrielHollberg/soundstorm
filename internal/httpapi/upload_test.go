package httpapi

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type placement struct {
	Path    string `json:"path"`
	Group   string `json:"group"`
	Kind    string `json:"kind"`
	Dest    string `json:"dest"`
	Skipped bool   `json:"skipped"`
	Waiting bool   `json:"waiting"`
	Reason  string `json:"reason"`
}

type question struct {
	Group   string   `json:"group"`
	Label   string   `json:"label"`
	Count   int      `json:"count"`
	Options []string `json:"options"`
}

type planResult struct {
	Files     []placement `json:"files"`
	Questions []question  `json:"questions"`
	Accepted  int         `json:"accepted"`
	Waiting   int         `json:"waiting"`
}

func (h *harness) planWith(t *testing.T, choices map[string]string, paths ...string) planResult {
	t.Helper()
	body, err := json.Marshal(map[string]any{"paths": paths, "choices": choices})
	if err != nil {
		t.Fatal(err)
	}
	resp, out := h.do(t, http.MethodPost, "/api/upload/plan", string(body))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("plan: %d %s", resp.StatusCode, out)
	}
	var decoded planResult
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("decode plan: %v", err)
	}
	return decoded
}

func (h *harness) plan(t *testing.T, paths ...string) []placement {
	t.Helper()
	return h.planWith(t, nil, paths...).Files
}

func (h *harness) upload(t *testing.T, kind, path, content string) (*http.Response, []byte) {
	t.Helper()
	q := url.Values{"path": {path}, "kind": {kind}}
	return h.do(t, http.MethodPut, "/api/upload?"+q.Encode(), content)
}

// The whole point: drop a folder and it lands where it belongs, with the
// structure intact, because Jellyfin needs the folder and Navidrome does not
// care either way.
func TestDroppingAFolderPlansAndUploadsIt(t *testing.T) {
	h := newHarness(t)
	h.signUp(t)

	files := h.plan(t,
		"Arrival (2016)/Arrival (2016).mkv",
		"Arrival (2016)/Arrival (2016).en.srt",
		"Arrival (2016)/readme.exe",
	)
	if len(files) != 3 {
		t.Fatalf("planned %d files", len(files))
	}
	if files[0].Dest != "movies/Arrival (2016)/Arrival (2016).mkv" {
		t.Errorf("film -> %q", files[0].Dest)
	}
	if files[1].Dest != "movies/Arrival (2016)/Arrival (2016).en.srt" {
		t.Errorf("subtitle -> %q; it must follow its film", files[1].Dest)
	}
	if !files[2].Skipped {
		t.Errorf("an .exe was going to be accepted as %q", files[2].Dest)
	}

	for _, f := range files[:2] {
		resp, out := h.upload(t, f.Kind, f.Path, "bytes of "+f.Path)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("upload %s: %d %s", f.Path, resp.StatusCode, out)
		}
	}

	root := h.libraryRoot(t)
	for _, rel := range []string{
		"movies/Arrival (2016)/Arrival (2016).mkv",
		"movies/Arrival (2016)/Arrival (2016).en.srt",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s is not on disk: %v", rel, err)
		}
	}
}

// The whole shape of the feature: when it cannot tell, it asks, and the answer
// settles the group.
func TestWhatItCannotSortItAsksAbout(t *testing.T) {
	h := newHarness(t)
	h.signUp(t)

	first := h.planWith(t, nil, "Esopo/one.mp3", "Esopo/two.mp3")
	if len(first.Questions) != 1 {
		t.Fatalf("questions = %+v, want one", first.Questions)
	}
	q := first.Questions[0]
	if q.Label != "Esopo" || q.Count != 2 {
		t.Errorf("question = %+v", q)
	}
	if strings.Join(q.Options, ",") != "music,audiobook" {
		t.Errorf("options = %v", q.Options)
	}
	if first.Accepted != 0 || first.Waiting != 2 {
		t.Errorf("accepted %d, waiting %d", first.Accepted, first.Waiting)
	}
	for _, f := range first.Files {
		if !f.Waiting {
			t.Errorf("%s was not waiting: %+v", f.Path, f)
		}
	}

	answered := h.planWith(t, map[string]string{"Esopo": "audiobook"},
		"Esopo/one.mp3", "Esopo/two.mp3")
	if len(answered.Questions) != 0 {
		t.Fatalf("still asking: %+v", answered.Questions)
	}
	if answered.Files[0].Dest != "audiobooks/Unknown Author/Esopo/one.mp3" {
		t.Errorf("after answering: %q", answered.Files[0].Dest)
	}

	resp, out := h.upload(t, "audiobook", "Esopo/one.mp3", "listen")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload: %d %s", resp.StatusCode, out)
	}
	if _, err := os.Stat(filepath.Join(h.libraryRoot(t), "audiobooks", "Unknown Author", "Esopo", "one.mp3")); err != nil {
		t.Errorf("not where it was asked to go: %v", err)
	}
}

// A question that offers a library somebody does not have is not a question
// they can answer. With one option left there is nothing to ask.
func TestAQuestionOnlyOffersLibrariesYouHave(t *testing.T) {
	h := newHarness(t)
	h.signUp(t)
	id := h.addMember(t, "sam", samPassword)
	h.setLibraries(t, id, `{"libraries":["music","ebook"]}`)
	sam := h.asUser(t, "sam", samPassword)

	got := sam.planWith(t, nil, "Esopo/one.mp3")
	if len(got.Questions) != 0 {
		t.Errorf("asked music-or-audiobook of somebody without audiobooks: %+v", got.Questions)
	}

	// And an answer naming a library they do not have is refused outright.
	resp, _ := sam.do(t, http.MethodPost, "/api/upload/plan",
		`{"paths":["Esopo/one.mp3"],"choices":{"Esopo":"audiobook"}}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

// A restriction that search honours and uploading does not is not a
// restriction. Both the automatic sorter and an explicit shelf have to refuse.
func TestARestrictedAccountCannotUploadToAForbiddenShelf(t *testing.T) {
	h := newHarness(t)
	h.signUp(t)
	id := h.addMember(t, "sam", samPassword)
	h.setLibraries(t, id, `{"libraries":["music"]}`)
	sam := h.asUser(t, "sam", samPassword)

	// Dropped on the window: the film is planned, then refused for them.
	got := sam.plan(t, "Arrival (2016).mkv")[0]
	if !got.Skipped {
		t.Errorf("a film was planned into %q for an account without films", got.Dest)
	}
	if !strings.Contains(got.Reason, "video") {
		t.Errorf("reason = %q", got.Reason)
	}

	// And the upload itself, which is the one that actually moves bytes.
	resp, _ := sam.upload(t, "video", "Arrival (2016).mkv", "a film")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("upload to a forbidden shelf = %d, want 403", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(h.libraryRoot(t), "movies", "Arrival (2016).mkv")); err == nil {
		t.Error("the file was written anyway")
	}

	// Their own shelf still works, or this proves nothing.
	resp, out := sam.upload(t, "music", "song.mp3", "a song")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("their own library was refused: %d %s", resp.StatusCode, out)
	}
}

// Every path here is browser-supplied, which is to say attacker-supplied.
func TestUploadPathsCannotEscapeTheLibrary(t *testing.T) {
	h := newHarness(t)
	h.signUp(t)
	root := h.libraryRoot(t)

	for _, nasty := range []string{
		"../../../etc/passwd.mp3",
		"..\\..\\evil.mp3",
		"/etc/evil.mp3",
		"C:\\evil.mp3",
		"music/../../evil.mp3",
	} {
		resp, _ := h.upload(t, "music", nasty, "payload")
		if resp.StatusCode == http.StatusOK {
			t.Errorf("%q was accepted", nasty)
		}
	}

	var strays []string
	_ = filepath.Walk(filepath.Dir(root), func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasPrefix(p, root) && strings.Contains(string(mustRead(p)), "payload") {
			strays = append(strays, p)
		}
		return nil
	})
	if len(strays) > 0 {
		t.Errorf("files were written outside the library: %v", strays)
	}
}

// Re-dropping an album somebody already added is ordinary, and quietly
// duplicating a hundred tracks is not a thing to do without being asked.
func TestUploadingTheSameFileTwiceIsRefusedNotDuplicated(t *testing.T) {
	h := newHarness(t)
	h.signUp(t)

	if resp, out := h.upload(t, "music", "song.mp3", "original"); resp.StatusCode != http.StatusOK {
		t.Fatalf("first upload: %d %s", resp.StatusCode, out)
	}
	resp, out := h.upload(t, "music", "song.mp3", "replacement")
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("second upload = %d, want 409: %s", resp.StatusCode, out)
	}

	// Music is filed under artist and album now, and "original" carries no
	// tags, so it lands under the placeholders rather than at the top.
	got, err := os.ReadFile(filepath.Join(h.libraryRoot(t),
		"music", "Unknown Artist", "Unknown Album", "song.mp3"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "original" {
		t.Errorf("the original was overwritten with %q", got)
	}
}

// The plan is what lets somebody see the answer before a gigabyte moves, so it
// must not move anything itself.
func TestPlanningWritesNothing(t *testing.T) {
	h := newHarness(t)
	h.signUp(t)

	h.plan(t, "Arrival (2016).mkv", "song.flac", "book.epub")

	var files int
	_ = filepath.Walk(h.libraryRoot(t), func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			files++
		}
		return nil
	})
	if files != 0 {
		t.Errorf("planning wrote %d files", files)
	}
}

func TestUploadingRequiresASignIn(t *testing.T) {
	h := newHarness(t)
	h.signUp(t)
	stranger := &harness{srv: h.srv, client: &http.Client{}, root: h.root}

	resp, _ := stranger.upload(t, "music", "song.mp3", "payload")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
	resp, _ = stranger.do(t, http.MethodPost, "/api/upload/plan", `{"paths":["song.mp3"]}`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("plan status = %d, want 401", resp.StatusCode)
	}
}

func mustRead(path string) []byte {
	b, _ := os.ReadFile(path)
	return b
}
