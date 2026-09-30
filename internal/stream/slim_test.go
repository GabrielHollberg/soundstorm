package stream

import (
	"bytes"
	"encoding/binary"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/source"
)

func mp4box(typ string, parts ...[]byte) []byte {
	body := bytes.Join(parts, nil)
	b := make([]byte, 8, 8+len(body))
	binary.BigEndian.PutUint32(b, uint32(8+len(body)))
	copy(b[4:], typ)
	return append(b, body...)
}

// testM4A is shaped like an iTunes song: ftyp, moov (a track whose chunk
// offsets point into mdat, then udta holding a large "picture"), padding,
// then the audio.
func testM4A() (file []byte, offsets []int64) {
	ftyp := mp4box("ftyp", []byte("M4A \x00\x00\x00\x00M4A mp42isom"))
	picture := bytes.Repeat([]byte{0xAB}, 60<<10)
	udta := mp4box("udta", mp4box("meta", make([]byte, 4), mp4box("ilst", mp4box("covr", picture))))
	free := mp4box("free", make([]byte, 30<<10))
	audio := make([]byte, 200<<10)
	for i := range audio {
		audio[i] = byte(i*7 + i/251)
	}
	build := func(offs []uint32) []byte {
		stco := make([]byte, 8+4*len(offs))
		binary.BigEndian.PutUint32(stco[4:], uint32(len(offs)))
		for i, o := range offs {
			binary.BigEndian.PutUint32(stco[8+4*i:], o)
		}
		trak := mp4box("trak", mp4box("mdia", mp4box("minf", mp4box("stbl", mp4box("stco", stco)))))
		mvhd := make([]byte, 100) // version 0: timescale 1000, 10 seconds
		binary.BigEndian.PutUint32(mvhd[12:], 1000)
		binary.BigEndian.PutUint32(mvhd[16:], 10000)
		moov := mp4box("moov", mp4box("mvhd", mvhd), trak, udta)
		return bytes.Join([][]byte{ftyp, moov, free, mp4box("mdat", audio)}, nil)
	}
	// Build once to learn where mdat's payload starts, then point into it.
	probe := build([]uint32{0, 0, 0})
	payload := int64(len(probe) - len(audio))
	offsets = []int64{payload, payload + 70000, payload + 150000}
	return build([]uint32{uint32(offsets[0]), uint32(offsets[1]), uint32(offsets[2])}), offsets
}

func upstream(t *testing.T, contentType string, data []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("ETag", `"abc"`)
		http.ServeContent(w, r, "", time.Unix(1700000000, 0), bytes.NewReader(data))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func slimGet(t *testing.T, p *Proxy, url, method, rng string) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	r := httptest.NewRequest(method, "/api/stream/navidrome/x", nil)
	if rng != "" {
		r.Header.Set("Range", rng)
	}
	w := httptest.NewRecorder()
	ok := p.serveSlim(w, r, source.Target{URL: url}, "navidrome/x", "test")
	return w, ok
}

func TestAnM4AIsSentWithoutItsPicture(t *testing.T) {
	orig, offsets := testM4A()
	srv := upstream(t, "audio/mp4", orig)
	p := New(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	w, ok := slimGet(t, p, srv.URL, http.MethodGet, "")
	if !ok || w.Code != http.StatusOK {
		t.Fatalf("served slim %v, status %d", ok, w.Code)
	}
	slim := w.Body.Bytes()
	if bytes.Contains(slim, []byte("covr")) || bytes.Contains(slim, []byte("free")) {
		t.Error("the picture or the padding was sent")
	}
	// The bitrate, for pacing: the audio's bytes over mvhd's ten seconds.
	if l := p.slim.get("navidrome/x"); l.kbps < 150 || l.kbps > 180 {
		t.Errorf("bitrate %.0f kbps, want about 164", l.kbps)
	}
	if saved := len(orig) - len(slim); saved < 90<<10 {
		t.Errorf("only %d bytes smaller", saved)
	}
	// The chunk offsets in the slim file point at the same audio bytes.
	at := bytes.Index(slim, []byte("stco"))
	if at < 0 {
		t.Fatal("no stco")
	}
	for i, want := range offsets {
		got := int64(binary.BigEndian.Uint32(slim[at+12+4*i:]))
		if !bytes.Equal(slim[got:got+64], orig[want:want+64]) {
			t.Errorf("chunk %d: offset %d does not point at the audio from %d", i, got, want)
		}
	}
	// Ranges are slices of the whole: across the header, and within the audio.
	for _, c := range []struct {
		rng        string
		start, end int
	}{{"bytes=100-5000", 100, 5000}, {"bytes=1000-", 1000, len(slim) - 1}, {"bytes=-4096", len(slim) - 4096, len(slim) - 1}} {
		w, _ := slimGet(t, p, srv.URL, http.MethodGet, c.rng)
		if w.Code != http.StatusPartialContent || !bytes.Equal(w.Body.Bytes(), slim[c.start:c.end+1]) {
			t.Errorf("%s: status %d, %d bytes, not the matching slice", c.rng, w.Code, w.Body.Len())
		}
	}
	if w, _ := slimGet(t, p, srv.URL, http.MethodGet, "bytes=99999999-"); w.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Errorf("past the end: status %d", w.Code)
	}
	if w, _ := slimGet(t, p, srv.URL, http.MethodHead, ""); w.Header().Get("Content-Length") != strconv.Itoa(len(slim)) || w.Body.Len() != 0 {
		t.Errorf("HEAD: length %q, body %d", w.Header().Get("Content-Length"), w.Body.Len())
	}
}

func TestAnMP3IsSentFromItsFirstFrame(t *testing.T) {
	tagBody := 40 << 10
	tag := []byte{'I', 'D', '3', 3, 0, 0,
		byte(tagBody >> 21 & 0x7f), byte(tagBody >> 14 & 0x7f), byte(tagBody >> 7 & 0x7f), byte(tagBody & 0x7f)}
	tag = append(tag, bytes.Repeat([]byte{0x11}, tagBody)...)
	frames := bytes.Repeat([]byte{0xFF, 0xFB, 0x90, 0x64}, 50<<10)
	srv := upstream(t, "audio/mpeg", append(tag, frames...))
	p := New(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	w, ok := slimGet(t, p, srv.URL, http.MethodGet, "")
	if !ok || !bytes.Equal(w.Body.Bytes(), frames) {
		t.Fatalf("served slim %v, %d bytes; want the frames alone (%d)", ok, w.Body.Len(), len(frames))
	}
	if l := p.slim.get("navidrome/x"); l.kbps != 128 {
		t.Errorf("bitrate %v, want the frame's 128", l.kbps)
	}
}

func TestOtherFilesAreSentAsTheyAre(t *testing.T) {
	orig, _ := testM4A()
	p := New(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, ok := slimGet(t, p, upstream(t, "video/mp4", orig).URL, http.MethodGet, ""); ok {
		t.Error("a video was slimmed")
	}
	// A small tag is not worth it.
	small := append([]byte{'I', 'D', '3', 3, 0, 0, 0, 0, 1, 0}, make([]byte, 128)...)
	small = append(small, bytes.Repeat([]byte{0xFF, 0xFB, 0x90, 0x64}, 1000)...)
	p2 := New(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, ok := slimGet(t, p2, upstream(t, "audio/mpeg", small).URL, http.MethodGet, ""); ok {
		t.Error("an MP3 with a tiny tag was slimmed")
	}
}

// A crafted song file is refused, not a panic: an MP3 whose tag size points
// a few bytes short of the end with another tag after it, and an MP4 box
// with a 64-bit size near the maximum.
func TestCraftedSongsDoNotPanic(t *testing.T) {
	p := New(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	body := 40 << 10
	tag := []byte{'I', 'D', '3', 3, 0, 0, byte(body >> 21 & 0x7f), byte(body >> 14 & 0x7f), byte(body >> 7 & 0x7f), byte(body & 0x7f)}
	mp3 := append(tag, make([]byte, body)...)
	mp3 = append(mp3, 'I', 'D', '3', 3, 0) // a second tag, cut short
	if _, ok := slimGet(t, p, upstream(t, "audio/mpeg", mp3).URL, http.MethodGet, ""); ok {
		t.Error("a cut-short MP3 tag was slimmed")
	}
	huge := []byte{0, 0, 0, 1, 'f', 'r', 'e', 'e', 0x7f, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xf0}
	mp4 := append(huge, make([]byte, 70<<10)...)
	p2 := New(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, ok := slimGet(t, p2, upstream(t, "audio/mp4", mp4).URL, http.MethodGet, ""); ok {
		t.Error("an MP4 with an impossible box size was slimmed")
	}
}
