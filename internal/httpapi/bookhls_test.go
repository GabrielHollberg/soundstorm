package httpapi

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/source"
)

// layoutStub is an audiobook shelf that says where a book's file is.
type layoutStub struct {
	stub
	folder, file string
}

func (l layoutStub) AudioLayout(context.Context, string) (source.AudioLayout, error) {
	return source.AudioLayout{Folder: l.folder, Files: []source.AudioFile{{Name: l.file, DurationSeconds: 40}}}, nil
}

// A long book in one MP4 is offered in pieces: the playback answer names a
// playlist, and the playlist, init segment and a fragment are served from the
// file on the shelf.
func TestALongBookPlaysInPieces(t *testing.T) {
	defer func(n int64) { bookHLSMinIndex = n }(bookHLSMinIndex)
	bookHLSMinIndex = 0 // the test book's index is small
	books := layoutStub{stub: stub{id: "abs", kind: media.KindAudiobook, streamURL: "http://upstream/x"}, folder: "Author/Book", file: "Book.m4b"}
	h := newHarness(t, books)
	h.signUp(t)
	dir := filepath.Join(h.libraryRoot(t), "audiobooks", "Author", "Book")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("../mp4hls/testdata/short.m4b")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Book.m4b"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	_, body := h.do(t, http.MethodGet, "/api/playback/abs/item1", "")
	var answer struct {
		Mode, URL string
	}
	if err := json.Unmarshal(body, &answer); err != nil || answer.Mode != "hls" || !strings.HasSuffix(answer.URL, "/index.m3u8") {
		t.Fatalf("playback answer %s", body)
	}
	base := strings.TrimSuffix(answer.URL, "index.m3u8")

	// The playlist, gzipped when asked for.
	req, _ := http.NewRequest(http.MethodGet, h.srv.URL+answer.URL, nil)
	req.Header.Set("Accept-Encoding", "gzip")
	// Sent past the client, which would undo the gzip itself.
	resp, err := http.DefaultTransport.RoundTrip(withCookies(t, h, req))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("playlist not gzipped: %v", resp.Header)
	}
	zr, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	pl, _ := io.ReadAll(zr)
	if !bytes.HasPrefix(pl, []byte("#EXTM3U")) || !bytes.Contains(pl, []byte("s3.m4s")) {
		t.Fatalf("playlist:\n%s", pl)
	}
	for _, file := range []string{"init.mp4", "s0.m4s", "s3.m4s"} {
		r, b := h.do(t, http.MethodGet, base+file, "")
		if r.StatusCode != http.StatusOK || len(b) == 0 || r.Header.Get("Content-Type") != "audio/mp4" {
			t.Fatalf("%s: %d %q %d bytes", file, r.StatusCode, r.Header.Get("Content-Type"), len(b))
		}
	}
	if r, _ := h.do(t, http.MethodGet, base+"s99999.m4s", ""); r.StatusCode != http.StatusNotFound {
		t.Fatalf("a fragment past the end: %d", r.StatusCode)
	}
	if r, _ := h.do(t, http.MethodGet, base+"..%2f..%2fstate.json", ""); r.StatusCode == http.StatusOK {
		t.Fatal("a path out of the book was served")
	}
}

// withCookies copies the harness's session onto a request sent past its client.
func withCookies(t *testing.T, h *harness, req *http.Request) *http.Request {
	t.Helper()
	for _, c := range h.client.Jar.Cookies(req.URL) {
		req.AddCookie(c)
	}
	return req
}
