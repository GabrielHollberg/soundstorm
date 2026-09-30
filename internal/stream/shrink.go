package stream

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/gif" // decoders for the covers books carry
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

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
	// shrinkMaxPixels is the largest picture decoded to be shrunk; a larger
	// one is sent as it is. A decoded picture costs up to 8 bytes a pixel
	// (16-bit colour), so this is under 100MB, where 40 million pixels was
	// over 300MB - from a file of a few hundred KB, since a blank picture
	// compresses a thousandfold (a security review: a member could upload
	// such a cover and exhaust the server's memory).
	shrinkMaxPixels = 12_000_000
)

// At most two covers are decoded at once, and a shrunk cover is kept, so a
// page of covers - or many requests for one - is not a decode each.
var (
	shrinkSlots = make(chan struct{}, 2)
	shrunk      = struct {
		sync.Mutex
		m     map[string][]byte
		order []string
	}{m: map[string][]byte{}}
)

const shrunkKeep = 256

func shrunkKey(target source.Target, size int, px int) string {
	return fmt.Sprintf("%s|%s|%d|%d|%d", target.FilePath, target.Name, target.ModTime.UnixNano(), size, px)
}

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
	key := shrunkKey(target, len(data), px)
	shrunk.Lock()
	kept, ok := shrunk.m[key]
	shrunk.Unlock()
	if ok {
		if kept == nil {
			return target, false
		}
		return source.Target{Bytes: kept, ContentType: http.DetectContentType(kept), Name: target.Name, ModTime: target.ModTime}, true
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (cfg.Width <= px && cfg.Height <= px) || cfg.Width*cfg.Height > shrinkMaxPixels {
		keepShrunk(key, nil)
		return target, false
	}
	select {
	case shrinkSlots <- struct{}{}:
	case <-time.After(10 * time.Second):
		return target, false
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	<-shrinkSlots
	if err != nil {
		keepShrunk(key, nil)
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
		keepShrunk(key, nil)
		return target, false
	}
	keepShrunk(key, out.Bytes())
	return source.Target{
		Bytes:       out.Bytes(),
		ContentType: contentType,
		Name:        target.Name,
		ModTime:     target.ModTime,
	}, true
}

// keepShrunk remembers a shrunk cover (nil: send the original), the oldest
// forgotten past shrunkKeep. Covers are tens of KB, so this is a few MB.
func keepShrunk(key string, b []byte) {
	shrunk.Lock()
	defer shrunk.Unlock()
	if _, ok := shrunk.m[key]; !ok {
		shrunk.order = append(shrunk.order, key)
	}
	shrunk.m[key] = b
	for len(shrunk.order) > shrunkKeep {
		delete(shrunk.m, shrunk.order[0])
		shrunk.order = shrunk.order[1:]
	}
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
