package httpapi

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func testBox(name string, body ...[]byte) []byte {
	b := bytes.Join(body, nil)
	out := make([]byte, 8, 8+len(b))
	binary.BigEndian.PutUint32(out, uint32(8+len(b)))
	copy(out[4:], name)
	return append(out, b...)
}

// cameraVideo is a tiny MOV as a camera writes one: a movie header with when
// it was filmed and the maker in udta.
func cameraVideo(filmed time.Time, udta ...[]byte) string {
	mvhd := make([]byte, 100)
	binary.BigEndian.PutUint32(mvhd[4:8], uint32(filmed.Sub(time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC))/time.Second))
	return string(append(testBox("moov", testBox("mvhd", mvhd), testBox("udta", udta...)), testBox("mdat", make([]byte, 32))...))
}

// A home video dropped as a film goes to the photos by when it was filmed; a
// film stays a film.
func TestAHomeVideoDroppedAsAFilmGoesToThePhotos(t *testing.T) {
	h := newHarness(t)
	h.signUp(t)
	dest := func(resp *http.Response, out []byte) string {
		t.Helper()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("upload: %d %s", resp.StatusCode, out)
		}
		var body struct{ Dest string }
		_ = json.Unmarshal(out, &body)
		return body.Dest
	}
	filmed := time.Date(2019, 12, 24, 19, 30, 0, 0, time.UTC)

	got := dest(h.upload(t, "video", "Jack's 5th birthday.mov", cameraVideo(filmed, testBox("\xa9mak", []byte("Apple")))))
	if !strings.HasPrefix(got, "pictures/Personal/") || !strings.Contains(got, "/2019/12/") {
		t.Fatalf("a phone's video went to %q, want the photos under 2019/12", got)
	}

	got = dest(h.upload(t, "video", "Dune (2021).mp4", cameraVideo(filmed, testBox("\xa9too", []byte("HandBrake")))))
	if !strings.HasPrefix(got, "movies/") {
		t.Fatalf("a film went to %q, want movies/", got)
	}
}
