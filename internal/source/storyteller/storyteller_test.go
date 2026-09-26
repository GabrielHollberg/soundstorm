package storyteller

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/GabrielHollberg/soundstorm/internal/source"
)

func TestClockReadsEverySMILForm(t *testing.T) {
	for in, want := range map[string]float64{
		"2.370s": 2.37, "1500ms": 1.5, "0:01:02.5": 62.5, "01:02.5": 62.5, "1.5min": 90, "1h": 3600,
	} {
		got, ok := clock(in)
		if !ok || got < want-1e-9 || got > want+1e-9 {
			t.Errorf("clock(%q) = %v, %v; want %v", in, got, ok, want)
		}
	}
	if _, ok := clock(""); ok {
		t.Error("an empty clock value was accepted")
	}
}

// A synced book as Storyteller writes it, cut down to two sentences.
func syncedBook(t *testing.T) *zip.Reader {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	add := func(name, body string) {
		f, _ := w.Create(name)
		f.Write([]byte(body))
	}
	add("META-INF/container.xml", `<container><rootfiles><rootfile full-path="OEBPS/content.opf"/></rootfiles></container>`)
	add("OEBPS/content.opf", `<package><manifest>
<item id="c1" href="c1.xhtml" media-type="application/xhtml+xml" media-overlay="c1-mo"/>
<item id="c1-mo" href="MediaOverlays/c1.smil" media-type="application/smil+xml"/>
</manifest><spine><itemref idref="c1"/></spine></package>`)
	add("OEBPS/MediaOverlays/c1.smil", `<smil><body><seq>
<par id="c1-s1"><text src="../c1.xhtml#c1-s1"/><audio src="../Audio/00001-00002.mp4" clipBegin="0.500s" clipEnd="2.370s"/></par>
<par id="c1-s2"><text src="../c1.xhtml#c1-s2"/><audio src="../Audio/00001-00002.mp4" clipBegin="2.370s" clipEnd="5.800s"/></par>
</seq></body></smil>`)
	w.Close()
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return zr
}

func TestClipsReadTheMediaOverlaysInReadingOrder(t *testing.T) {
	clips, err := readClips(syncedBook(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(clips) != 2 {
		t.Fatalf("%d clips, want 2", len(clips))
	}
	c := clips[1]
	if c.Href != "OEBPS/c1.xhtml#c1-s2" || c.File != 1 || c.Piece != 2 || c.Begin != 2.37 || c.End != 5.8 {
		t.Errorf("clip = %+v", c)
	}
}

func TestTimelinePutsChapterPiecesOnTheWholeBook(t *testing.T) {
	// One m4b, three chapters: piece 2 starts at the second chapter.
	one := source.AudioLayout{
		Files:    []source.AudioFile{{Name: "book.m4b", StartSeconds: 0, DurationSeconds: 3000}},
		Chapters: []float64{0, 1000, 2000},
	}
	got, err := Timeline([]Clip{{Href: "a#1", File: 1, Piece: 2, Begin: 2.37, End: 5.8}}, one)
	if err != nil || len(got) != 1 || got[0].Start != 1002.37 {
		t.Fatalf("single file: %v %+v", err, got)
	}

	// Two files, no chapter marks of their own, listed out of name order:
	// Storyteller numbers them by name.
	two := source.AudioLayout{
		Files: []source.AudioFile{
			{Name: "02.mp3", StartSeconds: 600, DurationSeconds: 500},
			{Name: "01.mp3", StartSeconds: 0, DurationSeconds: 600},
		},
		Chapters: []float64{0, 600},
	}
	got, err = Timeline([]Clip{{Href: "b#1", File: 2, Piece: 1, Begin: 10, End: 12}}, two)
	if err != nil || got[0].Start != 610 {
		t.Fatalf("two files: %v %+v", err, got)
	}

	// More pieces than chapters: the two did not cut the same way, so no
	// timeline rather than a wrong one.
	if _, err := Timeline([]Clip{{Href: "c#1", File: 1, Piece: 4}}, one); err == nil {
		t.Error("a piece past the last chapter was placed anyway")
	}
	if _, err := Timeline([]Clip{{Href: "d#1", File: 3, Piece: 1}}, two); err == nil {
		t.Error("a file the audiobook does not have was placed anyway")
	}
}

func TestSyncNextRequeuesWithTheChosenBookFirst(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v2/books" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[
{"uuid":"running","readaloud":{"status":"PROCESSING","queuePosition":0}},
{"uuid":"b2","readaloud":{"status":"QUEUED","queuePosition":1}},
{"uuid":"b4","readaloud":{"status":"QUEUED","queuePosition":2}},
{"uuid":"mc","readaloud":{"status":"QUEUED","queuePosition":3}},
{"uuid":"done","readaloud":{"status":"ALIGNED"}}]`))
			return
		}
		calls = append(calls, r.Method+" "+strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v2/books/"), "/process"))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	s, err := New(Config{ID: "storyteller", BaseURL: srv.URL, Token: "t", DataDir: "/d", AudiobooksRemote: "/audiobooks"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SyncNext(context.Background(), "mc"); err != nil {
		t.Fatal(err)
	}
	want := "DELETE mc,DELETE b2,DELETE b4,POST mc,POST b2,POST b4"
	if got := strings.Join(calls, ","); got != want {
		t.Errorf("calls\n got %s\nwant %s", got, want)
	}
}
