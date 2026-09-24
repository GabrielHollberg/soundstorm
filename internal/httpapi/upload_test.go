package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/media"
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

	// Counted either side rather than against zero. The folders are not empty
	// to begin with - each ships a placeholder, and that placeholder is what
	// keeps a backend willing to notice deletions - so "nothing was written"
	// is a comparison, not a count.
	before := listFiles(h.libraryRoot(t))

	h.plan(t, "Arrival (2016).mkv", "song.flac", "book.epub")

	after := listFiles(h.libraryRoot(t))
	if len(after) != len(before) {
		t.Errorf("planning changed the library from %d files to %d: %v",
			len(before), len(after), after)
	}
}

// listFiles is every file under root, relative, sorted.
func listFiles(root string) []string {
	var out []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rel = path
			}
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(out)
	return out
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

// The scan somebody triggers by hand is, more often than not, the scan right
// after they emptied a folder from their file manager and wondered why their
// deleted films were still listed. That is the moment the placeholder has to be
// back: asked to scan a folder that is genuinely empty, a backend declines to
// remove anything from it - it cannot tell a deletion from an unmounted disk -
// and reports success while changing nothing.
func TestScanningPutsAMissingPlaceholderBackFirst(t *testing.T) {
	h := newHarness(t)
	h.signUp(t)

	placeholder := filepath.Join(h.libraryRoot(t), "movies", "README.txt")
	if err := os.Remove(placeholder); err != nil {
		t.Fatalf("remove: %v", err)
	}

	h.api.rescanNow(media.KindVideo)

	if _, err := os.Stat(placeholder); err != nil {
		t.Errorf("a scan left the folder empty, so a deletion would go unnoticed: %v", err)
	}
}

// A disk failure quotes the container's own absolute path - os.MkdirAll and a
// failed rename both do - and that path has no business in a 400 body. The
// original, path and all, is what gets logged; this is what the browser sees.
func TestFilesystemPathsDoNotReachTheBrowserOnAFailedUpload(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"a bare path error", &fs.PathError{Op: "mkdir", Path: "/library/.uploads", Err: fs.ErrPermission}},
		{"a wrapped path error", fmt.Errorf("prepare upload: %w", &fs.PathError{Op: "open", Path: "/library/music/x", Err: fs.ErrNotExist})},
		{"a link error", &os.LinkError{Op: "rename", Old: "/library/.uploads/part-1", New: "/library/music/a.mp3", Err: fs.ErrExist}},
	}
	for _, c := range cases {
		got := desensitizeFSError(c.err)
		if strings.Contains(got, "/library") {
			t.Errorf("%s: %q still names the library path", c.name, got)
		}
		if got == "" {
			t.Errorf("%s: message was emptied entirely", c.name)
		}
	}
	// An ordinary, already-friendly validation message is left alone.
	plain := errors.New("there is no music library")
	if got := desensitizeFSError(plain); got != plain.Error() {
		t.Errorf("a plain message was altered: %q", got)
	}
}

// startUpload sends an upload whose body is fed by the returned writer, so a test
// can pace it. The response arrives on the returned channel.
func (h *harness) startUpload(t *testing.T, path string) (*io.PipeWriter, <-chan int) {
	t.Helper()
	pr, pw := io.Pipe()
	q := url.Values{"path": {path}, "kind": {"music"}}
	req, err := http.NewRequest(http.MethodPut, h.srv.URL+"/api/upload?"+q.Encode(), pr)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 1)
	go func() {
		resp, err := h.client.Do(req)
		if err != nil {
			done <- -1
			return
		}
		resp.Body.Close()
		done <- resp.StatusCode
	}()
	t.Cleanup(func() { pw.Close() })
	return pw, done
}

// An upload sending a byte at a time is cut off. Each read used to push the
// deadline back however little arrived, so a trickle held a connection, a
// staging file and a file descriptor open for ever.
func TestATrickledUploadIsCutOff(t *testing.T) {
	old := uploadStall
	uploadStall = 300 * time.Millisecond
	defer func() { uploadStall = old }()

	h := newHarness(t)
	h.signUp(t)
	pw, done := h.startUpload(t, "trickle.mp3")
	go func() {
		for i := 0; i < 60; i++ {
			if _, err := pw.Write([]byte{'x'}); err != nil {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		pw.Close()
	}()

	select {
	case code := <-done:
		if code == http.StatusOK {
			t.Error("a trickled upload was accepted")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a trickled upload was still open long after its window closed")
	}
	if _, err := os.Stat(filepath.Join(h.libraryRoot(t), "music", "Unknown Artist", "Unknown Album", "trickle.mp3")); err == nil {
		t.Error("a trickled upload landed in the library")
	}
}

// A slow upload that keeps making real progress is not cut off, however long
// it takes in total.
func TestASlowButSteadyUploadCompletes(t *testing.T) {
	old := uploadStall
	uploadStall = 300 * time.Millisecond
	defer func() { uploadStall = old }()

	h := newHarness(t)
	h.signUp(t)
	pw, done := h.startUpload(t, "steady.mp3")
	go func() {
		chunk := bytes.Repeat([]byte("s"), uploadMinProgress+1024)
		// Longer in total than several windows, but each window sees progress.
		for i := 0; i < 6; i++ {
			if _, err := pw.Write(chunk); err != nil {
				return
			}
			time.Sleep(150 * time.Millisecond)
		}
		pw.Close()
	}()

	select {
	case code := <-done:
		if code != http.StatusOK {
			t.Errorf("a steady upload = %d, want 200", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a steady upload never finished")
	}
}

// One account cannot hold uploads open by the hundred: past the cap, another
// is refused until one finishes.
func TestUploadsPerAccountAreCapped(t *testing.T) {
	h := newHarness(t)
	h.signUp(t)

	var writers []*io.PipeWriter
	for i := 0; i < maxUploadsPerUser; i++ {
		pw, _ := h.startUpload(t, fmt.Sprintf("held-%d.mp3", i))
		writers = append(writers, pw)
	}
	inFlight := func() int {
		h.api.uploadsMu.Lock()
		defer h.api.uploadsMu.Unlock()
		n := 0
		for _, c := range h.api.uploads {
			n += c
		}
		return n
	}
	deadline := time.Now().Add(3 * time.Second)
	for inFlight() < maxUploadsPerUser && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := inFlight(); got != maxUploadsPerUser {
		t.Fatalf("%d uploads in flight, want %d", got, maxUploadsPerUser)
	}

	resp, _ := h.upload(t, "music", "one-too-many.mp3", "x")
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("upload past the cap = %d, want 429", resp.StatusCode)
	}

	// Finishing one frees its slot.
	writers[0].Write([]byte("done"))
	writers[0].Close()
	deadline = time.Now().Add(3 * time.Second)
	for inFlight() >= maxUploadsPerUser && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if resp, out := h.upload(t, "music", "after.mp3", "x"); resp.StatusCode != http.StatusOK {
		t.Errorf("upload after one finished = %d, want 200: %s", resp.StatusCode, out)
	}
}
