package stream

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"net/http/httptest"
	"testing"

	"github.com/GabrielHollberg/soundstorm/internal/source"
)

func TestArtSizeDefaultsToACard(t *testing.T) {
	for q, want := range map[string]int{
		"": defaultArtSize, "?size=full": 0, "?size=1000": 1000,
		"?size=9": minArtSize, "?size=99999": maxArtSize, "?size=x": defaultArtSize,
	} {
		if got := artSize(httptest.NewRequest("GET", "/api/art/s/a"+q, nil)); got != want {
			t.Errorf("%q: %d, want %d", q, got, want)
		}
	}
}

func TestALargeLocalCoverIsShrunk(t *testing.T) {
	big := image.NewRGBA(image.Rect(0, 0, 1600, 1200))
	for y := 0; y < 1200; y++ {
		for x := 0; x < 1600; x++ {
			big.Set(x, y, color.RGBA{uint8(x), uint8(y), uint8(x ^ y), 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, big, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	small, ok := shrinkLocal(source.Target{Bytes: buf.Bytes(), Name: "cover.jpg"}, 400)
	if !ok {
		t.Fatal("not shrunk")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(small.Bytes))
	if err != nil || cfg.Width != 400 || cfg.Height != 300 {
		t.Fatalf("got %dx%d (%v), want 400x300", cfg.Width, cfg.Height, err)
	}
	if len(small.Bytes) >= buf.Len() || small.ContentType != "image/jpeg" {
		t.Errorf("%d bytes as %s, from %d", len(small.Bytes), small.ContentType, buf.Len())
	}
	// Already small enough: served as it is.
	if _, ok := shrinkLocal(small, 400); ok {
		t.Error("a 400px cover was shrunk again")
	}
	// Not an image: served as it is.
	if _, ok := shrinkLocal(source.Target{Bytes: []byte("not a picture")}, 400); ok {
		t.Error("text was taken for an image")
	}
}

// A JPEG of thousands of scans takes minutes to decode, so one with more
// than a real picture has is sent as it is. Here the markers sit in a
// comment, which a decoder ignores: it would decode quickly, and is refused
// only by the count - the case the count is built for.
func TestAJPEGOfTooManyScansIsNotShrunk(t *testing.T) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 800, 800)), nil); err != nil {
		t.Fatal(err)
	}
	body := bytes.Repeat([]byte{0xFF, 0xDA}, maxJPEGScans+1)
	comment := append([]byte{0xFF, 0xFE, byte((len(body) + 2) >> 8), byte(len(body) + 2)}, body...)
	data := append(append(append([]byte{}, buf.Bytes()[:2]...), comment...), buf.Bytes()[2:]...)
	if _, ok := shrinkLocal(source.Target{Bytes: data, Name: "scans.jpg"}, 400); ok {
		t.Fatal("shrunk a JPEG with more scans than any real one")
	}
	if _, ok := shrinkLocal(source.Target{Bytes: buf.Bytes(), Name: "plain.jpg"}, 400); !ok {
		t.Fatal("an ordinary JPEG was not shrunk")
	}
}
