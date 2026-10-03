package voices

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/GabrielHollberg/soundstorm/internal/mp4hls"
)

// PieceSeconds is about how long a piece of a recording sent to Whisper is:
// short enough to hold in memory on both sides, long enough to keep the
// requests few. A word cut in two at a piece's edge is the cost.
const PieceSeconds = 600

// maxWhole is the largest file sent whole when it cannot be cut.
const maxWhole = 200 << 20

// EachPiece cuts an audio file into pieces of about PieceSeconds, without
// re-encoding anything: an MP4 (m4b, m4a) as fragments of its own samples
// (mp4hls, the remux long books play by), an MP3 at frame boundaries. Any
// other format is sent whole if it is not too big. f gets each piece's start
// in the file, in seconds, its bytes, and a file name saying its format.
func EachPiece(path string, f func(start float64, data []byte, name string) error) error {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".m4b", ".m4a", ".mp4", ".aac":
		if b, err := mp4hls.Open(path); err == nil {
			return mp4Pieces(b, f)
		}
	case ".mp3":
		return mp3Pieces(path, f)
	}
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if st.Size() > maxWhole {
		return errors.New("this audiobook's format cannot be cut into pieces, and it is too big to send whole")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return f(0, data, "audio"+filepath.Ext(path))
}

func mp4Pieces(b *mp4hls.Book, f func(float64, []byte, string) error) error {
	n := b.Segments()
	for i := 0; i < n; {
		start := b.SegmentStart(i)
		var buf bytes.Buffer
		buf.Write(b.Init())
		j := i
		for j < n && (j == i || b.SegmentStart(j)-start < PieceSeconds) {
			if err := b.WriteSegment(&buf, j); err != nil {
				return err
			}
			j++
		}
		if err := f(start, buf.Bytes(), "piece.mp4"); err != nil {
			return err
		}
		i = j
	}
	return nil
}

// mp3Pieces walks an MP3's frames and sends them in runs; the ID3 tag in
// front is left out, so every piece starts on a frame.
func mp3Pieces(path string, f func(float64, []byte, string) error) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	pos := 0
	if len(data) >= 10 && string(data[:3]) == "ID3" {
		size := int(data[6]&0x7f)<<21 | int(data[7]&0x7f)<<14 | int(data[8]&0x7f)<<7 | int(data[9]&0x7f)
		pos = 10 + size
		if data[5]&0x10 != 0 {
			pos += 10
		}
	}
	t, pieceStart, pieceFrom := 0.0, 0.0, pos
	for pos+4 <= len(data) {
		length, secs := mp3Frame(data[pos:])
		if length == 0 {
			// Not a frame: look for the next sync a byte on.
			pos++
			continue
		}
		if t-pieceStart >= PieceSeconds {
			if err := f(pieceStart, data[pieceFrom:pos], "piece.mp3"); err != nil {
				return err
			}
			pieceStart, pieceFrom = t, pos
		}
		pos += length
		t += secs
	}
	if pos > len(data) {
		pos = len(data)
	}
	if pos > pieceFrom {
		return f(pieceStart, data[pieceFrom:pos], "piece.mp3")
	}
	return nil
}

var (
	mp3Bitrates = [2][3][16]int{
		{ // MPEG-1: layer I, II, III
			{0, 32, 64, 96, 128, 160, 192, 224, 256, 288, 320, 352, 384, 416, 448, 0},
			{0, 32, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 384, 0},
			{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 0},
		},
		{ // MPEG-2 and 2.5
			{0, 32, 48, 56, 64, 80, 96, 112, 128, 144, 160, 176, 192, 224, 256, 0},
			{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0},
			{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0},
		},
	}
	mp3Rates = [4][3]int{{11025, 12000, 8000}, {0, 0, 0}, {22050, 24000, 16000}, {44100, 48000, 32000}}
)

// mp3Frame reads a frame header: the frame's length in bytes and seconds, or
// 0 when this is not one.
func mp3Frame(h []byte) (int, float64) {
	if len(h) < 4 || h[0] != 0xFF || h[1]&0xE0 != 0xE0 {
		return 0, 0
	}
	version := int(h[1]>>3) & 3 // 0: 2.5, 2: 2, 3: 1
	layer := int(h[1]>>1) & 3   // 3: I, 2: II, 1: III
	if version == 1 || layer == 0 {
		return 0, 0
	}
	bi, ri := int(h[2]>>4), int(h[2]>>2)&3
	if bi == 0 || bi == 15 || ri == 3 {
		return 0, 0
	}
	v := 0
	if version != 3 {
		v = 1
	}
	bitrate := mp3Bitrates[v][3-layer][bi] * 1000
	rate := mp3Rates[version][ri]
	pad := int(h[2]>>1) & 1
	var length, samples int
	switch layer {
	case 3: // I
		length, samples = (12*bitrate/rate+pad)*4, 384
	case 2: // II
		length, samples = 144*bitrate/rate+pad, 1152
	default: // III
		if version == 3 {
			length, samples = 144*bitrate/rate+pad, 1152
		} else {
			length, samples = 72*bitrate/rate+pad, 576
		}
	}
	if length < 4 {
		return 0, 0
	}
	return length, float64(samples) / float64(rate)
}
