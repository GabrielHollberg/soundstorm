package stream

import (
	"bytes"
	"image"
	"image/color"
	_ "image/gif" // decoders for the covers books carry
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"strconv"

	"github.com/GabrielHollberg/soundstorm/internal/source"
)

// Covers at card size.
//
// Most covers are shown a couple of hundred pixels wide, but they went out at
// full resolution: an iTunes album cover is ~150-200KB, a Calibre cover up to
// 900KB (measured on the real library). Opening the app on a phone with a slow
// connection (measured: 1.1 Mbps) downloaded megabytes of pictures ahead of
// the song somebody had just pressed play on. So covers are card-sized unless
// a bigger one is asked for; Navidrome, Audiobookshelf and Jellyfin resize
// their own, and SoundStorm's own (a book's) are resized here.

const (
	// defaultArtSize is a card's cover: 400px, sharp at 200 CSS pixels on a
	// phone's screen.
	defaultArtSize = 400
	minArtSize     = 64
	maxArtSize     = 2000
	// shrinkMaxInput refuses to decode anything larger - a cover is not.
	shrinkMaxInput = 20 << 20
)

// artSize reads ?size=: a number of pixels (clamped), "full" for the
// original, or card-sized when absent.
func artSize(r *http.Request) int {
	v := r.URL.Query().Get("size")
	if v == "full" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return defaultArtSize
	}
	return min(max(n, minArtSize), maxArtSize)
}

// shrinkLocal returns a smaller copy of a local image target no wider or
// taller than px, or false to serve it as it is: not an image it can read,
// already small enough, or not smaller once re-encoded.
func shrinkLocal(target source.Target, px int) (source.Target, bool) {
	var data []byte
	if target.Bytes != nil {
		data = target.Bytes
	} else {
		f, err := os.Open(target.FilePath)
		if err != nil {
			return target, false
		}
		defer f.Close()
		data, err = io.ReadAll(io.LimitReader(f, shrinkMaxInput+1))
		if err != nil || len(data) > shrinkMaxInput {
			return target, false
		}
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (cfg.Width <= px && cfg.Height <= px) || cfg.Width*cfg.Height > 40_000_000 {
		return target, false
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return target, false
	}
	small := downscale(img, px)
	var out bytes.Buffer
	contentType := "image/jpeg"
	if format == "png" && hasAlpha(small) {
		// Transparency kept, as a JPEG cannot.
		contentType = "image/png"
		err = png.Encode(&out, small)
	} else {
		err = jpeg.Encode(&out, small, &jpeg.Options{Quality: 85})
	}
	if err != nil || out.Len() >= len(data) {
		return target, false
	}
	return source.Target{
		Bytes:       out.Bytes(),
		ContentType: contentType,
		Name:        target.Name,
		ModTime:     target.ModTime,
	}, true
}

// downscale shrinks img to fit in a px square, keeping its shape, by averaging
// a few samples of the source under each new pixel - plenty for a cover, and
// quick without a resizing library.
func downscale(img image.Image, px int) *image.RGBA {
	b := img.Bounds()
	sw, sh := b.Dx(), b.Dy()
	dw, dh := px, px
	if sw >= sh {
		dh = max(1, sh*px/sw)
	} else {
		dw = max(1, sw*px/sh)
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	// Up to 4x4 samples per new pixel, spread across the source area it covers.
	const n = 4
	for y := 0; y < dh; y++ {
		for x := 0; x < dw; x++ {
			var r, g, bl, a uint32
			var count uint32
			for j := 0; j < n; j++ {
				sy := b.Min.Y + ((y*n+j)*sh+sh/(2*n))/(dh*n)
				for i := 0; i < n; i++ {
					sx := b.Min.X + ((x*n+i)*sw+sw/(2*n))/(dw*n)
					cr, cg, cb, ca := img.At(min(sx, b.Max.X-1), min(sy, b.Max.Y-1)).RGBA()
					r += cr
					g += cg
					bl += cb
					a += ca
					count++
				}
			}
			dst.SetRGBA(x, y, color.RGBA{
				R: uint8(r / count >> 8), G: uint8(g / count >> 8),
				B: uint8(bl / count >> 8), A: uint8(a / count >> 8),
			})
		}
	}
	return dst
}

func hasAlpha(img *image.RGBA) bool {
	for i := 3; i < len(img.Pix); i += 4 {
		if img.Pix[i] != 0xff {
			return true
		}
	}
	return false
}
