package audiobookshelf

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gabehollberg/soundstorm/internal/media"
	"github.com/gabehollberg/soundstorm/internal/source"
)

// LibriVox catalogue entries carry HTML entities in plain-text fields, and
// Audiobookshelf stores what it is given. Without decoding, a reader sees
// "Las F&aacute;bulas de Esopo" instead of the accent. Seen in real data.
func TestHTMLEntitiesAreDecoded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"book":[{"libraryItem":{"id":"1","media":{"duration":60,
		  "metadata":{"title":"Las F&aacute;bulas de Esopo, Vol. 01",
		              "subtitle":"Cuentos &amp; Fábulas",
		              "authorName":"Esopo &amp; otros"}}}}]}`))
	}))
	defer srv.Close()

	s, err := New(Config{ID: "abs", BaseURL: srv.URL, Token: "t", LibraryID: "lib"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	items, err := s.Search(context.Background(), media.Query{Text: "esopo"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}

	got := items[0]
	if got.Title != "Las Fábulas de Esopo, Vol. 01" {
		t.Errorf("title = %q, entities not decoded", got.Title)
	}
	if got.Subtitle != "Cuentos & Fábulas" {
		t.Errorf("subtitle = %q", got.Subtitle)
	}
	if len(got.Creators) != 1 || got.Creators[0] != "Esopo & otros" {
		t.Errorf("creators = %v", got.Creators)
	}
}

// itemServer answers /api/items/{id} with body, and records every path asked
// for so a test can prove a round trip did or did not happen.
func itemServer(t *testing.T, body string) (*httptest.Server, *[]string) {
	t.Helper()
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &asked
}

func newTestSource(t *testing.T, baseURL string) *Source {
	t.Helper()
	s, err := New(Config{ID: "abs", BaseURL: baseURL, Token: "tok", LibraryID: "lib"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

// The bug this whole feature exists for: a LibriVox book is one MP3 per
// chapter, and a client told about only the first plays two minutes of Aesop
// and goes quiet.
//
// Shapes below are from Audiobookshelf 2.36.1, GET /api/items/{id}, for real
// books in the test library.
func TestTracksListEveryFileInOrder(t *testing.T) {
	// index deliberately out of array order: Audiobookshelf does not promise
	// the array is sorted, and for a 30-part book guessing wrong puts chapter
	// 10 after chapter 1.
	srv, asked := itemServer(t, `{"id":"bk1","media":{"duration":4989,
	  "chapters":[
	    {"id":0,"start":0,"end":2206.5,"title":"Chapters 1 to 3"},
	    {"id":1,"start":2206.5,"end":3710.6,"title":"Chapters 4 to 5"},
	    {"id":2,"start":3710.6,"end":4989.0,"title":"Chapters 6 to 7"}],
	  "audioFiles":[
	    {"index":3,"ino":"333","duration":1278.4,
	     "metadata":{"filename":"ghost_6-7.mp3"},"metaTags":{"tagTitle":"tag three"}},
	    {"index":1,"ino":"111","duration":2206.5,
	     "metadata":{"filename":"ghost_1-3.mp3"},"metaTags":{"tagTitle":"tag one"}},
	    {"index":2,"ino":"222","duration":1504.1,
	     "metadata":{"filename":"ghost_4-5.mp3"},"metaTags":{"tagTitle":"tag two"}}],
	  "metadata":{"title":"The Canterville Ghost"}}}`)

	tracks, err := newTestSource(t, srv.URL).Tracks(context.Background(), "bk1")
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	if len(tracks) != 3 {
		t.Fatalf("want 3 tracks, got %d", len(tracks))
	}

	// One chapter per file, so the chapter names win over the ID3 tags.
	want := []struct {
		id, title string
		duration  float64
	}{
		{"bk1/111", "Chapters 1 to 3", 2206.5},
		{"bk1/222", "Chapters 4 to 5", 1504.1},
		{"bk1/333", "Chapters 6 to 7", 1278.4},
	}
	for i, w := range want {
		if tracks[i].ID != w.id {
			t.Errorf("track %d id = %q, want %q", i, tracks[i].ID, w.id)
		}
		if tracks[i].Title != w.title {
			t.Errorf("track %d title = %q, want %q", i, tracks[i].Title, w.title)
		}
		if tracks[i].DurationSeconds != w.duration {
			t.Errorf("track %d duration = %v, want %v", i, tracks[i].DurationSeconds, w.duration)
		}
	}
	if len(*asked) != 1 {
		t.Errorf("made %d upstream calls for one track list: %v", len(*asked), *asked)
	}
}

// A track id already carries the inode, so playing a chapter list must not cost
// a lookup per chapter. Thirty chapters would be thirty needless round trips.
func TestStreamTargetOfATrackNeedsNoLookup(t *testing.T) {
	srv, asked := itemServer(t, `{"id":"bk1","media":{}}`)

	target, err := newTestSource(t, srv.URL).StreamTarget(context.Background(), "bk1/222")
	if err != nil {
		t.Fatalf("StreamTarget: %v", err)
	}
	if want := srv.URL + "/api/items/bk1/file/222"; target.URL != want {
		t.Errorf("url = %q, want %q", target.URL, want)
	}
	if target.Headers["Authorization"] != "Bearer tok" {
		t.Errorf("headers = %v: upstream would reject this", target.Headers)
	}
	if len(*asked) != 0 {
		t.Errorf("resolved a track id with %d upstream calls: %v", len(*asked), *asked)
	}
}

// A bare item id still has to work: it is the direct-play url on every search
// result, used before any track list has been asked for.
func TestStreamTargetOfABareItemServesTheFirstFile(t *testing.T) {
	srv, _ := itemServer(t, `{"id":"bk1","media":{"audioFiles":[
	  {"index":2,"ino":"222"},{"index":1,"ino":"111"}]}}`)

	target, err := newTestSource(t, srv.URL).StreamTarget(context.Background(), "bk1")
	if err != nil {
		t.Fatalf("StreamTarget: %v", err)
	}
	if want := srv.URL + "/api/items/bk1/file/111"; target.URL != want {
		t.Errorf("url = %q, want %q: not the lowest-index file", target.URL, want)
	}
}

// Titles come from whatever the book actually has. LibriVox puts the chapter
// name in the ID3 title tag - complete with the HTML entities its catalogue
// carries - and some rips have neither tags nor chapters.
func TestTrackTitlesFallBackThroughEverySource(t *testing.T) {
	srv, _ := itemServer(t, `{"id":"bk1","media":{"audioFiles":[
	  {"index":1,"ino":"1","metadata":{"filename":"a.mp3"},
	   "metaTags":{"tagTitle":"001 El &aacute;guila y la zorra"}},
	  {"index":2,"ino":"2","metadata":{"filename":"fabula_01_002_esopo_64kb.mp3"}},
	  {"index":3,"ino":"3","metadata":{"filename":""}}]}}`)

	tracks, err := newTestSource(t, srv.URL).Tracks(context.Background(), "bk1")
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	want := []string{
		"001 El águila y la zorra", // the title tag, entities decoded
		"fabula_01_002_esopo_64kb", // the filename, extension dropped
		"Part 3",                   // nothing at all to go on
	}
	for i, w := range want {
		if tracks[i].Title != w {
			t.Errorf("track %d title = %q, want %q", i, tracks[i].Title, w)
		}
	}
}

// A single-file book is the ordinary case for anything bought rather than
// ripped, and it must not be dressed up as a chapter list of one.
func TestTracksOfASingleFileBookIsOneTrack(t *testing.T) {
	srv, _ := itemServer(t, `{"id":"bk1","media":{"audioFiles":[
	  {"index":1,"ino":"111","duration":610.2,"metadata":{"filename":"thinketh.mp3"}}]}}`)

	tracks, err := newTestSource(t, srv.URL).Tracks(context.Background(), "bk1")
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	if len(tracks) != 1 {
		t.Fatalf("want 1 track, got %d", len(tracks))
	}
}

// A file with no inode cannot be fetched, so offering it as a chapter would
// give somebody a dead entry in the list.
func TestTracksSkipFilesWithNoHandle(t *testing.T) {
	srv, _ := itemServer(t, `{"id":"bk1","media":{"audioFiles":[
	  {"index":1,"ino":"111"},{"index":2,"ino":""},{"index":3,"ino":"333"}]}}`)

	tracks, err := newTestSource(t, srv.URL).Tracks(context.Background(), "bk1")
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	if len(tracks) != 2 {
		t.Fatalf("want 2 tracks, got %d: %+v", len(tracks), tracks)
	}
	if tracks[1].ID != "bk1/333" {
		t.Errorf("track 1 id = %q", tracks[1].ID)
	}
}

// recorder answers /api/me/progress/{id} with status/body, and keeps whatever
// was PATCHed so a test can read what actually went upstream.
type recorder struct {
	srv    *httptest.Server
	sent   []map[string]any
	status int
	body   string
}

func newRecorder(t *testing.T, status int, body string) *recorder {
	t.Helper()
	rec := &recorder{status: status, body: body}
	rec.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			rec.sent = append(rec.sent, payload)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`OK`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(rec.status)
		_, _ = w.Write([]byte(rec.body))
	}))
	t.Cleanup(rec.srv.Close)
	return rec
}

// Nobody having started a book is the normal case, not a failure, and it must
// not look like one to a player deciding where to begin.
func TestPositionOfAnUnstartedBookIsTheStart(t *testing.T) {
	rec := newRecorder(t, http.StatusNotFound, `{"error":"no progress"}`)

	pos, err := newTestSource(t, rec.srv.URL).Position(context.Background(), "bk1")
	if err != nil {
		t.Fatalf("Position: %v", err)
	}
	if pos.Seconds != 0 || pos.Finished {
		t.Errorf("position = %+v, want the start", pos)
	}
}

func TestPositionIsRead(t *testing.T) {
	rec := newRecorder(t, http.StatusOK,
		`{"currentTime":2500.5,"duration":4989.02,"progress":0.5,"isFinished":false}`)

	pos, err := newTestSource(t, rec.srv.URL).Position(context.Background(), "bk1")
	if err != nil {
		t.Fatalf("Position: %v", err)
	}
	if pos.Seconds != 2500.5 || pos.Duration != 4989.02 {
		t.Errorf("position = %+v", pos)
	}
}

// Audiobookshelf 2.36.1 stores a progress record without validating it - a
// PATCH carrying the string "x" as currentTime is accepted with a 200 - so a
// record written by anything else can hold a value that is not a time.
func TestPositionIgnoresAJunkRecord(t *testing.T) {
	rec := newRecorder(t, http.StatusOK, `{"currentTime":"x","duration":4989}`)

	pos, err := newTestSource(t, rec.srv.URL).Position(context.Background(), "bk1")
	if err != nil {
		t.Fatalf("Position: %v", err)
	}
	if pos.Seconds != 0 {
		t.Errorf("seconds = %v, want the start", pos.Seconds)
	}
}

// Audiobookshelf does not derive progress from currentTime: send one without
// the other and its shelf and its "continue listening" row keep showing the
// old percentage while the player resumes in the right place. Verified against
// 2.36.1, where a PATCH of currentTime alone left progress untouched.
func TestSetPositionSendsTheFractionItself(t *testing.T) {
	rec := newRecorder(t, http.StatusOK, `{}`)

	err := newTestSource(t, rec.srv.URL).SetPosition(context.Background(), "bk1",
		source.Position{Seconds: 1200, Duration: 4800})
	if err != nil {
		t.Fatalf("SetPosition: %v", err)
	}
	if len(rec.sent) != 1 {
		t.Fatalf("sent %d requests", len(rec.sent))
	}
	sent := rec.sent[0]
	if sent["currentTime"] != float64(1200) {
		t.Errorf("currentTime = %v", sent["currentTime"])
	}
	if sent["progress"] != 0.25 {
		t.Errorf("progress = %v, want 0.25 computed here", sent["progress"])
	}
}

// isFinished is one-way upstream. Sending false for a book already marked
// finished does not just clear the flag - 2.36.1 resets currentTime and
// progress to zero, which is what "mark as unfinished" means in its own UI. So
// an ordinary save must not carry the flag at all, or scrubbing back after
// reaching the end would throw the position away.
func TestSetPositionNeverUnfinishesABook(t *testing.T) {
	rec := newRecorder(t, http.StatusOK, `{}`)
	s := newTestSource(t, rec.srv.URL)

	if err := s.SetPosition(context.Background(), "bk1",
		source.Position{Seconds: 1200, Duration: 4800}); err != nil {
		t.Fatalf("SetPosition: %v", err)
	}
	if _, present := rec.sent[0]["isFinished"]; present {
		t.Errorf("an ordinary save carried isFinished: %v", rec.sent[0])
	}

	if err := s.SetPosition(context.Background(), "bk1",
		source.Position{Seconds: 4800, Duration: 4800, Finished: true}); err != nil {
		t.Fatalf("SetPosition finished: %v", err)
	}
	if rec.sent[1]["isFinished"] != true {
		t.Errorf("finishing did not say so: %v", rec.sent[1])
	}
}

// A book whose length nobody knows can still have a position, and dividing by
// it would send a NaN into a record Audiobookshelf's own apps read back.
func TestSetPositionWithNoDurationSendsNoFraction(t *testing.T) {
	rec := newRecorder(t, http.StatusOK, `{}`)

	err := newTestSource(t, rec.srv.URL).SetPosition(context.Background(), "bk1",
		source.Position{Seconds: 300})
	if err != nil {
		t.Fatalf("SetPosition: %v", err)
	}
	if _, present := rec.sent[0]["progress"]; present {
		t.Errorf("sent a fraction of an unknown length: %v", rec.sent[0])
	}
}

func TestSetPositionRefusesSomethingThatIsNotATime(t *testing.T) {
	rec := newRecorder(t, http.StatusOK, `{}`)
	s := newTestSource(t, rec.srv.URL)

	for _, seconds := range []float64{math.NaN(), math.Inf(1), -5} {
		if err := s.SetPosition(context.Background(), "bk1",
			source.Position{Seconds: seconds, Duration: 4800}); err == nil {
			t.Errorf("accepted %v as a position", seconds)
		}
	}
	if len(rec.sent) != 0 {
		t.Errorf("sent %d bad records upstream: %v", len(rec.sent), rec.sent)
	}
}

// Position is measured across the whole book, so a track has to say where it
// starts or "two hours in" cannot be turned into a file and an offset.
func TestTracksCarryTheirOffsetIntoTheBook(t *testing.T) {
	srv, _ := itemServer(t, `{"id":"bk1","media":{"audioFiles":[
	  {"index":1,"ino":"111","duration":2206.5},
	  {"index":2,"ino":"222","duration":1504.1},
	  {"index":3,"ino":"333","duration":1278.4}]}}`)

	tracks, err := newTestSource(t, srv.URL).Tracks(context.Background(), "bk1")
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	want := []float64{0, 2206.5, 3710.6}
	for i, w := range want {
		if math.Abs(tracks[i].StartSeconds-w) > 0.001 {
			t.Errorf("track %d starts at %v, want %v", i, tracks[i].StartSeconds, w)
		}
	}
}
