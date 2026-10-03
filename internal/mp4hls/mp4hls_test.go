package mp4hls

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// testdata/short.m4b is 40 seconds of AAC in an MP4 with its index at the
// end and two chapters, made by ffmpeg as an audiobook store's encoder would.

func TestASegmentHoldsItsSamplesBytes(t *testing.T) {
	b, err := Open("testdata/short.m4b")
	if err != nil {
		t.Fatal(err)
	}
	if b.Segments() < 4 {
		t.Fatalf("40 seconds in %d segments", b.Segments())
	}
	if d := b.Duration(); d < 39.9 || d > 40.2 {
		t.Fatalf("duration %.3f", d)
	}
	// The playlist names every segment, and its lengths add up to the file's.
	pl := string(b.Playlist())
	var sum float64
	for _, line := range strings.Split(pl, "\n") {
		if v, ok := strings.CutPrefix(line, "#EXTINF:"); ok {
			f, _ := strconv.ParseFloat(strings.TrimSuffix(v, ","), 64)
			sum += f
		}
	}
	if sum < b.Duration()-0.01 || sum > b.Duration()+0.01 {
		t.Fatalf("playlist adds to %.3f, file %.3f", sum, b.Duration())
	}
	// Every segment's mdat is exactly its samples, and they cover the file's
	// samples once each.
	file, _ := os.ReadFile("testdata/short.m4b")
	var all []byte
	for i := 0; i < b.Segments(); i++ {
		var seg bytes.Buffer
		if err := b.WriteSegment(&seg, i); err != nil {
			t.Fatal(err)
		}
		data := seg.Bytes()
		moofLen := binary.BigEndian.Uint32(data)
		if string(data[4:8]) != "moof" || string(data[moofLen+4:moofLen+8]) != "mdat" {
			t.Fatalf("segment %d is not moof then mdat", i)
		}
		// The trun's data offset lands on the mdat's first byte.
		if off := binary.BigEndian.Uint32(data[84:]); off != moofLen+8 {
			t.Fatalf("segment %d data offset %d, mdat at %d", i, off, moofLen+8)
		}
		all = append(all, data[moofLen+8:]...)
	}
	var want []byte
	for s := uint32(0); s < b.samples; s++ {
		c := b.chunkOf(s)
		off := b.chunkOffset[c]
		for k := b.chunkFirst[c]; k < s; k++ {
			off += uint64(b.size(k))
		}
		want = append(want, file[off:off+uint64(b.size(s))]...)
	}
	if !bytes.Equal(all, want) {
		t.Fatalf("segments hold %d bytes, the samples are %d", len(all), len(want))
	}
}

func TestLayoutFindsTheIndexWithoutReadingIt(t *testing.T) {
	n, err := Layout("testdata/short.m4b")
	if err != nil || n <= 0 || n > 64<<10 {
		t.Fatalf("index %d, %v", n, err)
	}
}

// TestWriteOut writes the HLS to OUT for a player to check by hand
// (go test -run WriteOut with OUT set; skipped otherwise).
func TestWriteOut(t *testing.T) {
	dir := os.Getenv("OUT")
	if dir == "" {
		t.Skip()
	}
	src := os.Getenv("SRC")
	if src == "" {
		src = "testdata/short.m4b"
	}
	b, err := Open(src)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "index.m3u8"), b.Playlist(), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "init.mp4"), b.Init(), 0o644)
	for i := 0; i < b.Segments(); i++ {
		f, _ := os.Create(filepath.Join(dir, "s"+strconv.Itoa(i)+".m4s"))
		if err := b.WriteSegment(f, i); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
}
