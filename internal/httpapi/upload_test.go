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
	Kind    string `json:"kind"`
	Dest    string `json:"dest"`
	Skipped bool   `json:"skipped"`
	Reason  string `json:"reason"`
}

func (h *harness) plan(t *testing.T, kind string, paths ...string) []placement {
	t.Helper()
	body, err := json.Marshal(map[string]any{"paths": paths, "kind": kind})
	if err != nil {
		t.Fatal(err)
	}
	resp, out := h.do(t, http.MethodPost, "/api/upload/plan", string(body))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("plan: %d %s", resp.StatusCode, out)
	}
	var decoded struct {
		Files []placement `json:"files"`
	}
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("decode plan: %v", err)
	}
	return decoded.Files
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

	files := h.plan(t, "",
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

// Dropping onto a shelf settles the cases nothing in the file can settle.
func TestDroppingOntoAShelfOverridesTheGuess(t *testing.T) {
	h := newHarness(t)
	h.signUp(t)

	if got := h.plan(t, "", "chapter01.mp3")[0]; got.Kind != "music" {
		t.Errorf("an mp3 dropped on the window went to %q", got.Kind)
	}
	got := h.plan(t, "audiobook", "chapter01.mp3")[0]
	if got.Dest != "audiobooks/chapter01.mp3" {
		t.Errorf("an mp3 dropped on Audiobooks went to %q", got.Dest)
	}

	resp, out := h.upload(t, "audiobook", "chapter01.mp3", "listen")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload: %d %s", resp.StatusCode, out)
	}
	if _, err := os.Stat(filepath.Join(h.libraryRoot(t), "audiobooks", "chapter01.mp3")); err != nil {
		t.Errorf("not where it was asked to go: %v", err)
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
	got := sam.plan(t, "", "Arrival (2016).mkv")[0]
	if !got.Skipped {
		t.Errorf("a film was planned into %q for an account without films", got.Dest)
	}
	if !strings.Contains(got.Reason, "video") {
		t.Errorf("reason = %q", got.Reason)
	}

	// Dropped straight onto the Films shelf: refused outright.
	resp, _ := sam.do(t, http.MethodPost, "/api/upload/plan",
		`{"paths":["Arrival (2016).mkv"],"kind":"video"}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("plan onto a forbidden shelf = %d, want 403", resp.StatusCode)
	}

	// And the upload itself, which is the one that actually moves bytes.
	resp, _ = sam.upload(t, "video", "Arrival (2016).mkv", "a film")
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

	got, err := os.ReadFile(filepath.Join(h.libraryRoot(t), "music", "song.mp3"))
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

	h.plan(t, "", "Arrival (2016).mkv", "song.mp3", "book.epub")

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
