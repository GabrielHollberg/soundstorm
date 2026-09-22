package tags

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// id3v2 builds a tag the way a real tagger does, so the reader is exercised
// against the layout rather than against a fixture someone wrote to match it.
func id3v2(major byte, encoding byte, frames [][2]string) []byte {
	var body []byte
	for _, f := range frames {
		var payload []byte
		switch encoding {
		case 0, 3:
			payload = append([]byte{encoding}, []byte(f[1])...)
		case 1:
			payload = append([]byte{1, 0xFF, 0xFE}, utf16le(f[1])...)
		}
		size := len(payload)
		body = append(body, []byte(f[0])...)
		if major >= 4 {
			body = append(body, byte(size>>21&0x7F), byte(size>>14&0x7F),
				byte(size>>7&0x7F), byte(size&0x7F))
		} else {
			body = append(body, byte(size>>24), byte(size>>16), byte(size>>8), byte(size))
		}
		body = append(body, 0, 0)
		body = append(body, payload...)
	}
	n := len(body)
	head := []byte{'I', 'D', '3', major, 0, 0,
		byte(n >> 21 & 0x7F), byte(n >> 14 & 0x7F), byte(n >> 7 & 0x7F), byte(n & 0x7F)}
	return append(head, body...)
}

func utf16le(s string) []byte {
	var out []byte
	for _, r := range s {
		out = append(out, byte(r), byte(r>>8))
	}
	return out
}

func TestID3v23AndV24(t *testing.T) {
	for _, major := range []byte{3, 4} {
		got, err := Read(bytes.NewReader(id3v2(major, 3, [][2]string{
			{"TPE1", "Radiohead"},
			{"TALB", "In Rainbows"},
			{"TIT2", "15 Step"},
		})))
		if err != nil {
			t.Fatalf("v2.%d: %v", major, err)
		}
		// The frame size is a plain integer in 2.3 and syncsafe in 2.4, so
		// reading one as the other walks off the end and finds nothing.
		if got.Artist != "Radiohead" || got.Album != "In Rainbows" {
			t.Errorf("v2.%d gave %+v", major, got)
		}
	}
}

// The encoding byte is not decoration: read UTF-16 as UTF-8 and every second
// character is a NUL.
func TestID3TextEncodings(t *testing.T) {
	for _, enc := range []byte{0, 1, 3} {
		got, err := Read(bytes.NewReader(id3v2(3, enc, [][2]string{{"TPE1", "Bjork"}})))
		if err != nil {
			t.Fatalf("encoding %d: %v", enc, err)
		}
		if got.Artist != "Bjork" {
			t.Errorf("encoding %d gave %q", enc, got.Artist)
		}
	}
}

// Unsynchronisation rewrites every 0xFF 0x00 pair so no part of a tag can look
// like the start of an audio frame. Left undone, every frame length after the
// first such pair is wrong.
func TestID3Unsynchronisation(t *testing.T) {
	raw := id3v2(3, 3, [][2]string{{"TPE1", "A"}, {"TALB", "B"}})
	raw[5] |= 0x80 // claim unsynchronisation
	body := bytes.ReplaceAll(raw[10:], []byte{0xFF}, []byte{0xFF, 0x00})
	n := len(body)
	raw = append(raw[:6], append([]byte{
		byte(n >> 21 & 0x7F), byte(n >> 14 & 0x7F), byte(n >> 7 & 0x7F), byte(n & 0x7F),
	}, body...)...)
	raw[5] |= 0x80

	got, err := Read(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Artist != "A" || got.Album != "B" {
		t.Errorf("got %+v", got)
	}
}

// A compilation has a different artist per track and one album artist. Filing
// by Artist scatters it across a folder per track.
func TestAlbumArtistWinsForFoldering(t *testing.T) {
	got, _ := Read(bytes.NewReader(id3v2(3, 3, [][2]string{
		{"TPE1", "Guest Singer"},
		{"TPE2", "Various Artists"},
	})))
	if got.Folder() != "Various Artists" {
		t.Errorf("Folder() = %q", got.Folder())
	}
}

func flac(comments ...string) []byte {
	var block []byte
	vendor := []byte("test")
	block = binary.LittleEndian.AppendUint32(block, uint32(len(vendor)))
	block = append(block, vendor...)
	block = binary.LittleEndian.AppendUint32(block, uint32(len(comments)))
	for _, c := range comments {
		block = binary.LittleEndian.AppendUint32(block, uint32(len(c)))
		block = append(block, c...)
	}
	out := []byte("fLaC")
	// Block type 4, marked last, then a 24-bit big-endian length.
	out = append(out, 0x84, byte(len(block)>>16), byte(len(block)>>8), byte(len(block)))
	return append(out, block...)
}

func TestFLACVorbisComments(t *testing.T) {
	got, err := Read(bytes.NewReader(flac(
		"ARTIST=Talk Talk", "ALBUM=Laughing Stock", "TITLE=Myrrhman")))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Artist != "Talk Talk" || got.Album != "Laughing Stock" {
		t.Errorf("got %+v", got)
	}
}

// Vorbis repeats the key for a multi-value field, which is exactly why
// Navidrome was chosen. The first is the one to file under.
func TestFLACMultiValueArtistTakesTheFirst(t *testing.T) {
	got, _ := Read(bytes.NewReader(flac("ARTIST=Bowie", "ARTIST=Queen", "ALBUM=Hot Space")))
	if got.Artist != "Bowie" {
		t.Errorf("Artist = %q", got.Artist)
	}
}

func mp4(tags map[string]string) []byte {
	data := func(value string) []byte {
		payload := append(make([]byte, 8), value...) // 4 type + 4 reserved
		out := make([]byte, 4)
		binary.BigEndian.PutUint32(out, uint32(len(payload)+8))
		return append(append(out, "data"...), payload...)
	}
	var ilst []byte
	for name, value := range tags {
		child := data(value)
		out := make([]byte, 4)
		binary.BigEndian.PutUint32(out, uint32(len(child)+8))
		ilst = append(ilst, append(append(out, name...), child...)...)
	}
	box := func(name string, body []byte) []byte {
		out := make([]byte, 4)
		binary.BigEndian.PutUint32(out, uint32(len(body)+8))
		return append(append(out, name...), body...)
	}
	// meta is a full atom: four bytes of version and flags before its children.
	meta := box("meta", append(make([]byte, 4), box("ilst", ilst)...))
	moov := box("moov", box("udta", meta))
	return append(box("ftyp", []byte("M4A isom")), moov...)
}

func TestMP4ITunesAtoms(t *testing.T) {
	got, err := Read(bytes.NewReader(mp4(map[string]string{
		"\xa9ART": "Miles Davis",
		"\xa9alb": "Kind of Blue",
		"\xa9nam": "So What",
	})))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	// meta carries four bytes of version and flags its siblings do not.
	// Descending without skipping them lands mid-atom and finds nothing,
	// which is the usual reason an M4A looks untagged.
	if got.Artist != "Miles Davis" || got.Album != "Kind of Blue" {
		t.Errorf("got %+v", got)
	}
}

// Nothing here may panic or hang on a file that is not what it claims.
func TestRubbishIsNotAnError(t *testing.T) {
	for _, in := range [][]byte{
		{},
		[]byte("ID3"),
		append([]byte("ID3\x03\x00\x00\x7f\x7f\x7f\x7f"), 0xFF),
		[]byte("fLaC\x84\xff\xff\xff"),
		append([]byte("\x00\x00\x00\x08ftyp"), 0x00),
		bytes.Repeat([]byte{0xFF}, 64),
	} {
		if _, err := Read(bytes.NewReader(in)); err != nil {
			continue // refusing is fine; crashing is not
		}
	}
}
