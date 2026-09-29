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
