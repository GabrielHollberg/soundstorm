package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/auth"
	"github.com/GabrielHollberg/soundstorm/internal/federate"
	"github.com/GabrielHollberg/soundstorm/internal/library"
	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/provision"
	"github.com/GabrielHollberg/soundstorm/internal/source"
	"github.com/GabrielHollberg/soundstorm/internal/state"
)

// stub is a Source that returns canned results and streams from a fake
// upstream.
type stub struct {
	id        string
	kind      media.Kind
	items     []media.Item
	err       error
	streamURL string
}

func (s stub) ID() string       { return s.id }
func (s stub) Kind() media.Kind { return s.kind }
func (s stub) Search(context.Context, media.Query) ([]media.Item, error) {
	return s.items, s.err
}
func (s stub) Health(context.Context) error                                { return s.err }
func (s stub) StreamTarget(context.Context, string) (source.Target, error) { return s.target() }
func (s stub) ArtTarget(context.Context, string) (source.Target, error)    { return s.target() }

func (s stub) target() (source.Target, error) {
	if s.streamURL == "" {
		return source.Target{}, errors.New("no upstream configured")
	}
	return source.Target{URL: s.streamURL}, nil
}

type harness struct {
	srv    *httptest.Server
	client *http.Client
	root   string // the library on disk, for tests that check what landed there

	// api is the server behind srv, for the few tests that need to reach past
	// HTTP - a scan is debounced by two seconds on purpose, and waiting that
	// out in a test buys nothing but flakiness.
	api *Server
}

// libraryRoot is where this harness put its library folders.
func (h *harness) libraryRoot(t *testing.T) string {
	t.Helper()
	if h.root == "" {
		t.Fatal("this harness has no library root")
	}
	return h.root
}

func newHarness(t *testing.T, sources ...source.Source) *harness {
	t.Helper()

	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	reg := source.NewRegistry(sources...)

	// A real library on a temporary directory. Like the store, this was
	// missing until a test finally asked for /api/library and found a nil
	// pointer sitting behind it.
	libRoot := filepath.Join(t.TempDir(), "library")
	lib, err := library.Open(libRoot, "./library", log)
	if err != nil {
		t.Fatalf("library.Open: %v", err)
	}

	api := New(Config{
		Registry: reg,
		// The store was missing here until accounts needed it, which meant
		// every handler that reads state was being exercised against a nil
		// pointer that happened not to be dereferenced yet.
		Store:            store,
		Library:          lib,
		Auth:             auth.New(store),
		Setup:            provision.New(store, reg, log, nil),
		PerSourceTimeout: time.Second,
		Log:              log,
		SetupCode:        testSetupCode,
	})

	srv := httptest.NewServer(api.Routes())
	t.Cleanup(srv.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	return &harness{srv: srv, client: &http.Client{Jar: jar}, root: libRoot, api: api}
}

func (h *harness) do(t *testing.T, method, path, body string) (*http.Response, []byte) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, h.srv.URL+path, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := h.client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, out
}

// signUp creates the account and leaves the harness signed in.
func (h *harness) signUp(t *testing.T) {
	t.Helper()
	resp, body := h.do(t, http.MethodPost, "/api/signup", `{"setupCode":"`+testSetupCode+`","username":"gabe","password":"correct horse"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("signup failed: %d %s", resp.StatusCode, body)
	}
}

// The login has to actually gate something. If media bytes were reachable
// without a session, the backends would be effectively public and the whole
// single-login premise would be theatre.
func TestEverythingInterestingRequiresASession(t *testing.T) {
	h := newHarness(t, stub{id: "music", kind: media.KindMusic})

	for _, path := range []string{
		"/api/search?q=dune",
		"/api/setup",
		"/api/stream/music/1",
		"/api/art/music/1",
	} {
		resp, _ := h.do(t, http.MethodGet, path, "")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("GET %s without a session = %d, want 401", path, resp.StatusCode)
		}
	}
}

func TestSessionReportsWhetherSignupHasHappened(t *testing.T) {
	h := newHarness(t)

	var before struct {
		HasAccount bool `json:"hasAccount"`
		SignedIn   bool `json:"signedIn"`
	}
	_, body := h.do(t, http.MethodGet, "/api/session", "")
	if err := json.Unmarshal(body, &before); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if before.HasAccount || before.SignedIn {
		t.Errorf("fresh install should have no account and no session, got %+v", before)
	}

	h.signUp(t)

	var after struct {
		HasAccount bool `json:"hasAccount"`
		SignedIn   bool `json:"signedIn"`
	}
	_, body = h.do(t, http.MethodGet, "/api/session", "")
	if err := json.Unmarshal(body, &after); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !after.HasAccount || !after.SignedIn {
		t.Errorf("after signup want account and session, got %+v", after)
	}
}

// Signup is a first-boot action. A second one would be an account takeover by
// anyone who finds the port.
func TestSignupIsOnlyAvailableOnce(t *testing.T) {
	h := newHarness(t)
	h.signUp(t)

	resp, _ := h.do(t, http.MethodPost, "/api/signup", `{"setupCode":"`+testSetupCode+`","username":"someone","password":"else entirely"}`)
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("second signup = %d, want 409", resp.StatusCode)
	}
}

func TestSignupRejectsWeakPassword(t *testing.T) {
	h := newHarness(t)
	resp, body := h.do(t, http.MethodPost, "/api/signup", `{"setupCode":"`+testSetupCode+`","username":"gabe","password":"short"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (body %s)", resp.StatusCode, body)
	}
}

func TestLoginAndLogout(t *testing.T) {
	h := newHarness(t, stub{id: "music", kind: media.KindMusic})
	h.signUp(t)

	// Signed in after signup, so search is reachable.
	resp, _ := h.do(t, http.MethodGet, "/api/search?q=x", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("search after signup = %d", resp.StatusCode)
	}

	if resp, _ := h.do(t, http.MethodPost, "/api/logout", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("logout = %d", resp.StatusCode)
	}
	resp, _ = h.do(t, http.MethodGet, "/api/search?q=x", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("search after logout = %d, want 401", resp.StatusCode)
	}

	// Wrong password stays out.
	resp, _ = h.do(t, http.MethodPost, "/api/login", `{"username":"gabe","password":"wrong password"}`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("bad password = %d, want 401", resp.StatusCode)
	}

	// Right password gets back in.
	resp, _ = h.do(t, http.MethodPost, "/api/login", `{"setupCode":"`+testSetupCode+`","username":"gabe","password":"correct horse"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("good password = %d, want 200", resp.StatusCode)
	}
	resp, _ = h.do(t, http.MethodGet, "/api/search?q=x", "")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("search after login = %d", resp.StatusCode)
	}
}

func TestSearchMergesResultsFromEveryBackend(t *testing.T) {
	h := newHarness(t,
		stub{id: "navidrome", kind: media.KindMusic, items: []media.Item{
			{ID: "1", Title: "Dune", Kind: media.KindMusic, SourceID: "navidrome"},
		}},
		stub{id: "jellyfin", kind: media.KindVideo, items: []media.Item{
			{ID: "2", Title: "Dune", Kind: media.KindVideo, SourceID: "jellyfin"},
		}},
	)
	h.signUp(t)

	resp, body := h.do(t, http.MethodGet, "/api/search?q=dune", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}

	var got federate.Result
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Items) != 2 {
		t.Errorf("want 2 merged items, got %d", len(got.Items))
	}
	if got.Degraded {
		t.Error("want Degraded=false when every source succeeds")
	}
	for _, it := range got.Items {
		if it.Score <= 0 {
			t.Errorf("item %q was not scored", it.Title)
		}
	}
}

// The behaviour that matters when one server is off: the search still answers.
func TestSearchStillServesWhenOneBackendIsDown(t *testing.T) {
	h := newHarness(t,
		stub{id: "jellyfin", kind: media.KindVideo, items: []media.Item{
			{ID: "1", Title: "Dune", Kind: media.KindVideo},
		}},
		stub{id: "navidrome", kind: media.KindMusic, err: errors.New("dial tcp: connection refused")},
	)
	h.signUp(t)

	resp, body := h.do(t, http.MethodGet, "/api/search?q=dune", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a degraded search must still be 200, got %d", resp.StatusCode)
	}

	var got federate.Result
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Items) != 1 {
		t.Errorf("want the surviving backend's result, got %d items", len(got.Items))
	}
	if !got.Degraded {
		t.Error("want Degraded=true so the UI can say so")
	}
	// Why it failed is for the log. The real error names upstream addresses,
	// and a transport error once quoted a Subsonic URL with its credential.
	if strings.Contains(string(body), "dial tcp") {
		t.Errorf("the upstream error reached the browser: %s", body)
	}
}

// An empty result must be [] and not null, or the UI's .map() breaks on the
// most ordinary case there is: a search that found nothing.
func TestEmptySearchReturnsAnArray(t *testing.T) {
	h := newHarness(t, stub{id: "music", kind: media.KindMusic})
	h.signUp(t)

	_, body := h.do(t, http.MethodGet, "/api/search?q=nothing", "")
	if !strings.Contains(string(body), `"items": []`) {
		t.Errorf("want an empty array for items, got %s", body)
	}
}

func TestSearchValidatesInput(t *testing.T) {
	h := newHarness(t, stub{id: "music", kind: media.KindMusic})
	h.signUp(t)

	// "/api/search" with no q is deliberately absent from this list. It used
	// to be a 400 and is now a browse - "everything on this shelf" - which is
	// what the UI asks for the moment somebody picks a filter without typing.
	// TestBrowseWithNoQueryParameterAtAll pins the new behaviour down.
	for _, path := range []string{
		"/api/search?q=x&kind=vhs", // unknown kind
		"/api/search?q=x&limit=0",  // out of range
		"/api/search?q=x&limit=999",
		"/api/search?q=x&limit=abc",
	} {
		resp, body := h.do(t, http.MethodGet, path, "")
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("GET %s: status = %d, want 400 (body %s)", path, resp.StatusCode, body)
		}
	}
}

func TestSearchFiltersByKind(t *testing.T) {
	h := newHarness(t,
		stub{id: "navidrome", kind: media.KindMusic, items: []media.Item{{ID: "1", Title: "Dune"}}},
		stub{id: "jellyfin", kind: media.KindVideo, items: []media.Item{{ID: "2", Title: "Dune"}}},
	)
	h.signUp(t)

	_, body := h.do(t, http.MethodGet, "/api/search?q=dune&kind=video", "")
	var got federate.Result
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Sources) != 1 || got.Sources[0].SourceID != "jellyfin" {
		t.Errorf("kind filter did not apply: %+v", got.Sources)
	}
}

// The proxy is what lets the backends stay off any published port, so it has to
// actually move the bytes - and forward Range, or seeking a film breaks.
func TestStreamProxiesUpstreamBytes(t *testing.T) {
	const payload = "0123456789abcdef"
	var sawRange string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawRange = r.Header.Get("Range")
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Header().Set("Accept-Ranges", "bytes")
		http.ServeContent(w, r, "track.mp3", time.Unix(0, 0), strings.NewReader(payload))
	}))
	defer upstream.Close()

	h := newHarness(t, stub{
		id:        "navidrome",
		kind:      media.KindMusic,
		streamURL: upstream.URL + "/rest/stream.view?id=300",
	})
	h.signUp(t)

	resp, body := h.do(t, http.MethodGet, "/api/stream/navidrome/300", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream status = %d", resp.StatusCode)
	}
	if string(body) != payload {
		t.Errorf("proxied body = %q, want %q", body, payload)
	}
	if got := resp.Header.Get("Content-Type"); got != "audio/mpeg" {
		t.Errorf("content type = %q, want audio/mpeg", got)
	}

	// Now a ranged request, which is what a player issues when you seek.
	req, err := http.NewRequest(http.MethodGet, h.srv.URL+"/api/stream/navidrome/300", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Range", "bytes=4-7")
	ranged, err := h.client.Do(req)
	if err != nil {
		t.Fatalf("ranged request: %v", err)
	}
	defer ranged.Body.Close()
	rangedBody, _ := io.ReadAll(ranged.Body)

	if sawRange != "bytes=4-7" {
		t.Errorf("upstream saw Range %q, want bytes=4-7", sawRange)
	}
	if ranged.StatusCode != http.StatusPartialContent {
		t.Errorf("ranged status = %d, want 206", ranged.StatusCode)
	}
	if string(rangedBody) != "4567" {
		t.Errorf("ranged body = %q, want 4567", rangedBody)
	}
	if ranged.Header.Get("Content-Range") == "" {
		t.Error("Content-Range must be forwarded or the player cannot seek")
	}
}

func TestStreamUnknownSource(t *testing.T) {
	h := newHarness(t, stub{id: "navidrome", kind: media.KindMusic})
	h.signUp(t)

	resp, _ := h.do(t, http.MethodGet, "/api/stream/nope/1", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestHealthzNeedsNoSession(t *testing.T) {
	h := newHarness(t, stub{id: "music", kind: media.KindMusic})
	resp, body := h.do(t, http.MethodGet, "/healthz", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got struct {
		Status  string `json:"status"`
		Sources int    `json:"sources"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != "ok" || got.Sources != 1 {
		t.Errorf("unexpected healthz payload: %+v", got)
	}
}

func TestUIShellIsServed(t *testing.T) {
	h := newHarness(t)
	resp, body := h.do(t, http.MethodGet, "/", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	// The brand is displayed, so it is spelled the way a person reads it.
	// Identifiers elsewhere are lowercase; this one is not.
	if !strings.Contains(string(body), "SoundStorm") {
		t.Error("shell does not look like the UI")
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content type = %q", ct)
	}
}

// multiFile is a source whose items are several files each - which is what an
// audiobook ripped one MP3 per chapter looks like.
type multiFile struct {
	stub
	tracks []source.Track
	err    error
}

func (m multiFile) Tracks(context.Context, string) ([]source.Track, error) {
	return m.tracks, m.err
}

// Without this a client is told about one file and plays the first chapter of a
// thirty-part book, which is indistinguishable from the book being broken.
func TestPlaybackListsEveryFileOfAMultiPartBook(t *testing.T) {
	src := multiFile{
		stub: stub{id: "abs", kind: media.KindAudiobook},
		tracks: []source.Track{
			{ID: "bk1/111", Title: "Chapters 1 to 3", DurationSeconds: 2206.5},
			{ID: "bk1/222", Title: "Chapters 4 to 5", DurationSeconds: 1504.1},
		},
	}
	h := newHarness(t, src)
	h.signUp(t)

	resp, body := h.do(t, http.MethodGet, "/api/playback/abs/bk1", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}

	var got struct {
		Mode   string `json:"mode"`
		URL    string `json:"url"`
		Tracks []struct {
			Title           string  `json:"title"`
			URL             string  `json:"url"`
			DurationSeconds float64 `json:"durationSeconds"`
		} `json:"tracks"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Mode != source.PlaybackModeDirect {
		t.Errorf("mode = %q: an audiobook is not transcoded", got.Mode)
	}
	if len(got.Tracks) != 2 {
		t.Fatalf("want 2 tracks, got %d: %s", len(got.Tracks), body)
	}
	if got.Tracks[0].Title != "Chapters 1 to 3" {
		t.Errorf("track 0 title = %q", got.Tracks[0].Title)
	}
	// The slash inside a track id has to survive to the stream route, which
	// matches it with a trailing wildcard.
	if want := "/api/stream/abs/bk1/222"; got.Tracks[1].URL != want {
		t.Errorf("track 1 url = %q, want %q", got.Tracks[1].URL, want)
	}
	if got.Tracks[1].DurationSeconds != 1504.1 {
		t.Errorf("track 1 duration = %v", got.Tracks[1].DurationSeconds)
	}
}

// A song and a bought audiobook are both one file. Sending a chapter list of
// one would put a Chapters button on every track in the library.
func TestPlaybackOmitsATrackListOfOne(t *testing.T) {
	src := multiFile{
		stub:   stub{id: "abs", kind: media.KindAudiobook},
		tracks: []source.Track{{ID: "bk1/111", Title: "As a Man Thinketh"}},
	}
	h := newHarness(t, src)
	h.signUp(t)

	_, body := h.do(t, http.MethodGet, "/api/playback/abs/bk1", "")
	if strings.Contains(string(body), "tracks") {
		t.Errorf("a single-file item was given a chapter list: %s", body)
	}
}

// A backend that will not answer must not take playback down with it: the
// direct url is still correct, and still plays the first file.
func TestPlaybackStillPlaysWhenTheTrackListFails(t *testing.T) {
	src := multiFile{
		stub: stub{id: "abs", kind: media.KindAudiobook},
		err:  errors.New("upstream is having a moment"),
	}
	h := newHarness(t, src)
	h.signUp(t)

	resp, body := h.do(t, http.MethodGet, "/api/playback/abs/bk1", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	var got struct {
		Mode string `json:"mode"`
		URL  string `json:"url"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.URL != "/api/stream/abs/bk1" {
		t.Errorf("url = %q", got.URL)
	}
	if got.Mode != source.PlaybackModeDirect {
		t.Errorf("mode = %q", got.Mode)
	}
}

// resumable is a source that remembers where somebody got to.
type resumable struct {
	stub
	pos     source.Position
	readErr error
	saved   []source.Position
	saveErr error
}

func (r *resumable) Position(context.Context, string) (source.Position, error) {
	return r.pos, r.readErr
}

func (r *resumable) SetPosition(_ context.Context, _ string, pos source.Position) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	r.saved = append(r.saved, pos)
	return nil
}

func TestPlaybackReportsWhereYouLeftOff(t *testing.T) {
	src := &resumable{
		stub: stub{id: "abs", kind: media.KindAudiobook},
		pos:  source.Position{Seconds: 2500.5, Duration: 4989.02},
	}
	h := newHarness(t, src)
	h.signUp(t)

	_, body := h.do(t, http.MethodGet, "/api/playback/abs/bk1", "")
	var got struct {
		Position *struct {
			Seconds  float64 `json:"seconds"`
			Duration float64 `json:"duration"`
		} `json:"position"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Position == nil {
		t.Fatalf("no position reported: %s", body)
	}
	if got.Position.Seconds != 2500.5 || got.Position.Duration != 4989.02 {
		t.Errorf("position = %+v", *got.Position)
	}
}

// The key being present is what tells a client to save at all, so a source
// that cannot remember must not appear to.
func TestPlaybackOmitsPositionWhenNothingRemembersIt(t *testing.T) {
	h := newHarness(t, stub{id: "nd", kind: media.KindMusic})
	h.signUp(t)

	_, body := h.do(t, http.MethodGet, "/api/playback/nd/song", "")
	if strings.Contains(string(body), "position") {
		t.Errorf("a source with no memory advertised one: %s", body)
	}
}

// Failing to read a position must not stop one being written for the rest of
// the session: losing a bookmark is small, refusing to make new ones is not.
func TestPlaybackStillOffersToSaveWhenReadingFails(t *testing.T) {
	src := &resumable{
		stub:    stub{id: "abs", kind: media.KindAudiobook},
		readErr: errors.New("upstream is having a moment"),
	}
	h := newHarness(t, src)
	h.signUp(t)

	resp, body := h.do(t, http.MethodGet, "/api/playback/abs/bk1", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "position") {
		t.Errorf("stopped offering to save after one failed read: %s", body)
	}
}

func TestPositionIsSavedUpstream(t *testing.T) {
	src := &resumable{stub: stub{id: "abs", kind: media.KindAudiobook}}
	h := newHarness(t, src)
	h.signUp(t)

	resp, body := h.do(t, http.MethodPut, "/api/playback/abs/bk1",
		`{"seconds":1200.5,"duration":4989.02}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	if len(src.saved) != 1 {
		t.Fatalf("saved %d positions", len(src.saved))
	}
	if src.saved[0].Seconds != 1200.5 || src.saved[0].Duration != 4989.02 {
		t.Errorf("saved %+v", src.saved[0])
	}
	if src.saved[0].Finished {
		t.Error("an ordinary save was marked finished")
	}
}

// Audiobookshelf stores a progress record without validating it, so anything
// that is not a time has to be stopped here - a JSON number is never NaN, but
// 1e999 decodes to +Inf without complaint.
func TestPositionRefusesSomethingThatIsNotATime(t *testing.T) {
	src := &resumable{stub: stub{id: "abs", kind: media.KindAudiobook}}
	h := newHarness(t, src)
	h.signUp(t)

	for _, body := range []string{
		`{"seconds":1e999}`,
		`{"seconds":-30}`,
		`{"seconds":10,"duration":1e999}`,
	} {
		resp, out := h.do(t, http.MethodPut, "/api/playback/abs/bk1", body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s accepted with %d: %s", body, resp.StatusCode, out)
		}
	}
	if len(src.saved) != 0 {
		t.Errorf("passed %d bad positions upstream: %v", len(src.saved), src.saved)
	}
}

func TestSavingAPositionToASourceThatCannotIsRefusedClearly(t *testing.T) {
	h := newHarness(t, stub{id: "nd", kind: media.KindMusic})
	h.signUp(t)

	resp, _ := h.do(t, http.MethodPut, "/api/playback/nd/song", `{"seconds":30}`)
	if resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501", resp.StatusCode)
	}
}

// testSetupCode is what the harness's server expects, written the way the
// installer prints one.
const testSetupCode = "k7qm-2xfp-9rtd-h4wn"

// Until an account exists, whoever reaches the port first owns the server -
// and once it faces the internet that may not be whoever installed it. So the
// first sign-up needs the code the installer put in the address it opened.
func TestTheFirstSignupNeedsTheSetupCode(t *testing.T) {
	h := newHarness(t)

	_, body := h.do(t, http.MethodGet, "/api/session", "")
	if !strings.Contains(string(body), `"setupCodeRequired": true`) {
		t.Errorf("the page is not told a code is needed: %s", body)
	}

	for _, code := range []string{"", "wrong-code-here-xxxx", "k7qm-2xfp-9rtd-h4w"} {
		resp, _ := h.do(t, http.MethodPost, "/api/signup",
			`{"setupCode":"`+code+`","username":"stranger","password":"correct horse"}`)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("setup code %q: status %d, want 403", code, resp.StatusCode)
		}
	}
	resp, _ := h.do(t, http.MethodGet, "/api/session", "")
	_ = resp
	if h.api.auth.HasAccount() {
		t.Fatal("a refused sign-up created an account")
	}

	// Typed from a log rather than carried in the address: case and dashes
	// are presentation.
	resp, body = h.do(t, http.MethodPost, "/api/signup",
		`{"setupCode":"K7QM 2XFP 9RTD H4WN","username":"gabe","password":"correct horse"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the right code was refused: %d %s", resp.StatusCode, body)
	}
	_, body = h.do(t, http.MethodGet, "/api/session", "")
	if strings.Contains(string(body), "setupCodeRequired") {
		t.Errorf("still asking for a code after the owner exists: %s", body)
	}
}
