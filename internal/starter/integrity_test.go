package starter

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"testing"
)

// The bundle is 22MB of binary that nobody looks at, unpacked onto a machine
// nobody is watching, and the failure it had was silent in both directions:
// every file was the right length, opened, and started with the right magic
// number. Nine of the ten ebooks could not be read at all and four mp3s lost
// frame sync about once per 64KB, and the only symptom was a fresh install
// showing one book instead of ten.
//
// What happened was that something read them as text and wrote them back, which
// on Windows drops every carriage return. In a zip that moves the central
// directory out from under its own offsets; in an mp3 it desynchronises the
// frame chain. Both are cheap to check and neither was checked, so these tests
// read every bundled file the way the thing that consumes it will.

func TestBundledEbooksOpen(t *testing.T) {
	found := 0
	forEachBundled(t, ".epub", func(name string, body []byte) {
		found++
		zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
		if err != nil {
			t.Errorf("%s: not a zip: %v", name, err)
			return
		}
		// Opening the entry, not merely finding it in the listing. The damaged
		// copies listed it perfectly well - a zip's central directory survives
		// this - and failed on the local header the offset points at.
		if _, err := readZipEntry(zr, "META-INF/container.xml"); err != nil {
			t.Errorf("%s: cannot read META-INF/container.xml: %v", name, err)
		}
	})
	if found == 0 {
		t.Fatal("no ebooks in the bundle")
	}
}

func TestBundledAudioKeepsFrameSync(t *testing.T) {
	found := 0
	forEachBundled(t, ".mp3", func(name string, body []byte) {
		found++
		frames, err := walkMP3(body)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			return
		}
		// A real encode of a few minutes is thousands of frames. A handful
		// would mean the walk gave up immediately and proved nothing.
		if frames < 100 {
			t.Errorf("%s: only %d frames found, which is not a real recording", name, frames)
		}
	})
	if found == 0 {
		t.Fatal("no audio in the bundle")
	}
}

// And a test for the test. A checker for a silent corruption is worth exactly
// as much as the evidence that it notices the corruption, so this does to a
// bundled file precisely what was done to all of them, and requires the walk to
// object. Without this, walkMP3 returning nil for everything would look like
// a pass.
func TestFrameSyncCheckNoticesStrippedCarriageReturns(t *testing.T) {
	var original []byte
	forEachBundled(t, ".mp3", func(name string, body []byte) {
		if original == nil && len(body) > 1<<20 {
			original = body
		}
	})
	if original == nil {
		t.Skip("no bundled mp3 large enough to damage meaningfully")
	}

	damaged := bytes.ReplaceAll(original, []byte("\r\n"), []byte("\n"))
	if len(damaged) == len(original) {
		t.Fatal("the sample had no CRLF in it, so this proves nothing")
	}

	if _, err := walkMP3(original); err != nil {
		t.Fatalf("the undamaged file did not pass: %v", err)
	}
	if _, err := walkMP3(damaged); err == nil {
		t.Errorf("%d carriage returns were removed and the check did not notice",
			len(original)-len(damaged))
	}
}

// forEachBundled reads every embedded file with the given extension.
//
// From the embedded FS rather than from an unpacked copy: this is asking whether
// what ships is intact, and going through Install would also be testing the
// copy.
func forEachBundled(t *testing.T, ext string, fn func(name string, body []byte)) {
	t.Helper()
	err := fs.WalkDir(bundled, "media", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.EqualFold(path.Ext(p), ext) {
			return err
		}
		body, err := bundled.ReadFile(p)
		if err != nil {
			t.Errorf("%s: %v", p, err)
			return nil
		}
		fn(p, body)
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}

func readZipEntry(zr *zip.Reader, name string) ([]byte, error) {
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		buf := new(bytes.Buffer)
		if _, err := buf.ReadFrom(rc); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}
	return nil, fmt.Errorf("no such entry")
}

// walkMP3 follows the frame chain from end to end and returns how many frames
// it found.
//
// Every frame header states its own length, so a correct file is a chain: the
// bytes immediately after one frame are the next frame's sync word. Remove a
// byte anywhere and every later frame is misaligned, which a decoder survives
// by hunting for the next sync - a click, not an error - and which this notices
// straight away.
func walkMP3(b []byte) (int, error) {
	i := 0
	if len(b) > 10 && string(b[:3]) == "ID3" {
		// Syncsafe: seven bits per byte, so a size can never contain a false
		// sync word.
		size := int(b[6]&0x7f)<<21 | int(b[7]&0x7f)<<14 | int(b[8]&0x7f)<<7 | int(b[9]&0x7f)
		i = 10 + size
		if b[5]&0x10 != 0 {
			i += 10 // a footer, which ID3v2.4 allows
		}
	}
	if i >= len(b) {
		return 0, fmt.Errorf("nothing after the ID3 tag")
	}

	frames := 0
	for {
		// A trailing ID3v1 tag, or LAME's padding, is not a broken chain.
		if i+4 > len(b) || len(b)-i < 4 {
			return frames, nil
		}
		if string(b[i:i+3]) == "TAG" {
			return frames, nil
		}
		n, err := frameLen(b[i:])
		if err != nil {
			return frames, fmt.Errorf("frame %d at byte %d of %d: %w", frames+1, i, len(b), err)
		}
		if i+n > len(b) {
			// The last frame may be cut short by the file ending.
			return frames + 1, nil
		}
		i += n
		frames++
	}
}

var bitrates = [16]int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 0}
var rates = [4]int{44100, 48000, 32000, 0}

// frameLen reads one MPEG-1 Layer III frame header.
func frameLen(b []byte) (int, error) {
	if b[0] != 0xFF || b[1]&0xE0 != 0xE0 {
		return 0, fmt.Errorf("no sync word, found %02x %02x", b[0], b[1])
	}
	if version := b[1] >> 3 & 0x03; version != 3 {
		return 0, fmt.Errorf("not MPEG-1 (version bits %02b)", version)
	}
	if layer := b[1] >> 1 & 0x03; layer != 1 {
		return 0, fmt.Errorf("not layer III (layer bits %02b)", layer)
	}
	bitrate := bitrates[b[2]>>4&0x0f]
	if bitrate == 0 {
		return 0, fmt.Errorf("reserved or free bitrate index %d", b[2]>>4&0x0f)
	}
	rate := rates[b[2]>>2&0x03]
	if rate == 0 {
		return 0, fmt.Errorf("reserved sample rate index %d", b[2]>>2&0x03)
	}
	pad := 0
	if b[2]>>1&0x01 == 1 {
		pad = 1
	}
	return 144*bitrate*1000/rate + pad, nil
}
