package flac

import (
	"encoding/binary"
	"os"
	"testing"
)

// The clips were made by ffmpeg (Navidrome's own), and the .raw files are
// ffmpeg's decoding of them: every sample must agree.
func TestDecodesWhatFFmpegWrote(t *testing.T) {
	for _, tc := range []struct {
		name     string
		rate, ch int
		bits     int
		width    int  // bytes per sample in the .raw file
		shift    uint // ffmpeg writes 24-bit audio as s32, shifted up
	}{
		{"t16", 44100, 2, 16, 2, 0},
		{"t24", 48000, 1, 24, 4, 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := os.Open("testdata/" + tc.name + ".flac")
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			raw, err := os.ReadFile("testdata/" + tc.name + ".raw")
			if err != nil {
				t.Fatal(err)
			}
			var got []int32
			info, err := Decode(f, func(info Info, chans [][]int32) error {
				for i := range chans[0] {
					for c := range chans {
						got = append(got, chans[c][i])
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if info.SampleRate != tc.rate || info.Channels != tc.ch || info.Bits != tc.bits {
				t.Fatalf("info %+v", info)
			}
			if len(got)*tc.width != len(raw) {
				t.Fatalf("%d samples, want %d", len(got), len(raw)/tc.width)
			}
			for i, v := range got {
				var want int32
				if tc.width == 2 {
					want = int32(int16(binary.LittleEndian.Uint16(raw[i*2:])))
				} else {
					want = int32(binary.LittleEndian.Uint32(raw[i*4:]))
				}
				if v<<tc.shift != want {
					t.Fatalf("sample %d: %d, want %d", i, v<<tc.shift, want)
				}
			}
		})
	}
}

func TestRefusesWhatIsNotFLAC(t *testing.T) {
	f, _ := os.Open("testdata/t16.raw")
	defer f.Close()
	if _, err := Decode(f, func(Info, [][]int32) error { return nil }); err == nil {
		t.Fatal("decoded raw samples as FLAC")
	}
}
