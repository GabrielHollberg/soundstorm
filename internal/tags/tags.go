// Package tags reads just enough of an audio file to know where to file it.
//
// It is not a tag library and must not become one. The question it answers is
// "which artist and which album", so that a track dropped on its own lands at
// music/Artist/Album/track.flac rather than loose in the top of the shelf.
// Everything else about the file - duration, artwork, genre, replay gain - is
// the backends' job, and they are much better at it.
//
// Three formats, because those are the three a person actually drops: MP3
// carries ID3v2, M4A and MP4 carry iTunes atoms, FLAC carries Vorbis comments.
// Anything else returns nothing and the caller falls back, which is the same
// shape internal/epub and internal/pdf already use.
//
// Deliberately absent: ID3v1. It is 128 bytes at the end of the file with a
// 30-character limit per field, so an album name is usually truncated - and
// every file that has it also has ID3v2 unless it was made before about 1999.
package tags

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf16"
)

// Tags is the part worth reading.
type Tags struct {
	// AlbumArtist is what a folder should be named after when it exists.
	// A compilation has a different artist on every track and one album
	// artist, so filing by Artist would scatter it across a dozen folders.
	AlbumArtist string
	Artist      string
	Album       string
	Title       string
}

// Folder is the artist to file under: album artist when there is one, the
// track artist otherwise, and empty when the file says neither.
func (t Tags) Folder() string {
	if t.AlbumArtist != "" {
		return t.AlbumArtist
	}
	return t.Artist
}

// maxTagBytes caps how much of a file is read looking for tags.
//
// An ID3v2 header declares its own size and a malicious one can declare a very
// large number; MP4 atoms nest. This is generous for real files - artwork
// pushes tags to a megabyte or two - and bounded for everything else.
const maxTagBytes = 8 << 20

var errUnsupported = errors.New("no tags in a format this understands")

// Read identifies the format from its first bytes and reads what it can.
//
// A file with no tags is not an error: it returns a zero Tags, because "this
// track says nothing about itself" is an ordinary thing for the caller to
// handle and not a failure.
func Read(r io.ReadSeeker) (Tags, error) {
	head := make([]byte, 12)
	n, err := io.ReadFull(r, head)
	if err != nil && n < 8 {
		return Tags{}, fmt.Errorf("read header: %w", err)
	}
	head = head[:n]

	switch {
	case bytes.HasPrefix(head, []byte("ID3")):
		return readID3(r, head)
	case bytes.HasPrefix(head, []byte("fLaC")):
		return readFLAC(r)
	case len(head) >= 8 && string(head[4:8]) == "ftyp":
		return readMP4(r)
	}
	return Tags{}, errUnsupported
}

// --- ID3v2, in MP3 ----------------------------------------------------------

func readID3(r io.ReadSeeker, head []byte) (Tags, error) {
	if len(head) < 10 {
		return Tags{}, errUnsupported
	}
	major := head[3]
	flags := head[5]
	size := syncsafe(head[6:10])
	if size <= 0 || size > maxTagBytes {
		return Tags{}, errUnsupported
	}

	if _, err := r.Seek(10, io.SeekStart); err != nil {
		return Tags{}, err
	}
	body := make([]byte, size)
	if _, err := io.ReadFull(r, body); err != nil {
		return Tags{}, fmt.Errorf("read tag: %w", err)
	}

	// Unsynchronisation rewrites every 0xFF 0x00 pair so that no part of the
	// tag can look like the start of an audio frame to a decoder that does not
	// understand tags. Undo it, or every frame length after the first such
	// pair is wrong and the walk below falls off the end.
	if flags&0x80 != 0 {
		body = bytes.ReplaceAll(body, []byte{0xFF, 0x00}, []byte{0xFF})
	}
	// An extended header sits between the header and the frames and is not
	// one; skip it by the length it declares.
	if flags&0x40 != 0 && len(body) >= 4 {
		skip := syncsafe(body[:4])
		if major < 4 {
			skip = int(binary.BigEndian.Uint32(body[:4])) + 4
		}
		if skip > 0 && skip < len(body) {
			body = body[skip:]
		}
	}

	var t Tags
	for len(body) >= 10 {
		id := string(body[0:4])
		// Padding: the tag is zero-filled to its declared size.
		if id == "\x00\x00\x00\x00" {
			break
		}
		var frameSize int
		if major >= 4 {
			frameSize = syncsafe(body[4:8])
		} else {
			frameSize = int(binary.BigEndian.Uint32(body[4:8]))
		}
		if frameSize <= 0 || frameSize+10 > len(body) {
			break
		}
		payload := body[10 : 10+frameSize]

		switch id {
		case "TPE1":
			t.Artist = decodeID3Text(payload)
		case "TPE2":
			t.AlbumArtist = decodeID3Text(payload)
		case "TALB":
			t.Album = decodeID3Text(payload)
		case "TIT2":
			t.Title = decodeID3Text(payload)
		}
		body = body[10+frameSize:]
	}
	return t, nil
}

// decodeID3Text reads a text frame: one encoding byte, then the string.
func decodeID3Text(b []byte) string {
	if len(b) < 1 {
		return ""
	}
	encoding, text := b[0], b[1:]
	switch encoding {
	case 0: // ISO-8859-1, one byte per rune
		runes := make([]rune, 0, len(text))
		for _, c := range text {
			runes = append(runes, rune(c))
		}
		return clean(string(runes))
	case 1: // UTF-16 with a byte order mark
		return clean(decodeUTF16(text, true))
	case 2: // UTF-16BE, no mark
		return clean(decodeUTF16(text, false))
	default: // 3, and anything unknown, is UTF-8 in practice
		return clean(string(text))
	}
}

func decodeUTF16(b []byte, expectBOM bool) string {
	bigEndian := true
	if expectBOM && len(b) >= 2 {
		switch {
		case b[0] == 0xFF && b[1] == 0xFE:
			bigEndian, b = false, b[2:]
		case b[0] == 0xFE && b[1] == 0xFF:
			bigEndian, b = true, b[2:]
		}
	}
	units := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		if bigEndian {
			units = append(units, binary.BigEndian.Uint16(b[i:i+2]))
		} else {
			units = append(units, binary.LittleEndian.Uint16(b[i:i+2]))
		}
	}
	return string(utf16.Decode(units))
}

// syncsafe reads the seven-bits-per-byte integer ID3 uses for sizes, so that
// no length can contain a 0xFF byte and be mistaken for audio.
func syncsafe(b []byte) int {
	if len(b) < 4 {
		return 0
	}
	return int(b[0]&0x7F)<<21 | int(b[1]&0x7F)<<14 | int(b[2]&0x7F)<<7 | int(b[3]&0x7F)
}

// --- Vorbis comments, in FLAC -----------------------------------------------

func readFLAC(r io.ReadSeeker) (Tags, error) {
	if _, err := r.Seek(4, io.SeekStart); err != nil {
		return Tags{}, err
	}
	header := make([]byte, 4)
	for {
		if _, err := io.ReadFull(r, header); err != nil {
			return Tags{}, nil
		}
		last := header[0]&0x80 != 0
		blockType := header[0] & 0x7F
		length := int(header[1])<<16 | int(header[2])<<8 | int(header[3])

		if blockType == 4 { // VORBIS_COMMENT
			if length <= 0 || length > maxTagBytes {
				return Tags{}, nil
			}
			block := make([]byte, length)
			if _, err := io.ReadFull(r, block); err != nil {
				return Tags{}, nil
			}
			return parseVorbis(block), nil
		}
		if last {
			return Tags{}, nil
		}
		if _, err := r.Seek(int64(length), io.SeekCurrent); err != nil {
			return Tags{}, nil
		}
	}
}

// parseVorbis reads the little-endian length-prefixed KEY=value list.
func parseVorbis(b []byte) Tags {
	read := func(n int) ([]byte, bool) {
		if len(b) < n {
			return nil, false
		}
		out := b[:n]
		b = b[n:]
		return out, true
	}
	length := func() (int, bool) {
		raw, ok := read(4)
		if !ok {
			return 0, false
		}
		return int(binary.LittleEndian.Uint32(raw)), true
	}

	vendorLen, ok := length()
	if !ok || vendorLen < 0 {
		return Tags{}
	}
	if _, ok := read(vendorLen); !ok {
		return Tags{}
	}
	count, ok := length()
	if !ok {
		return Tags{}
	}

	var t Tags
	for i := 0; i < count; i++ {
		n, ok := length()
		if !ok || n < 0 {
			break
		}
		raw, ok := read(n)
		if !ok {
			break
		}
		key, value, found := strings.Cut(string(raw), "=")
		if !found {
			continue
		}
		switch strings.ToUpper(key) {
		case "ALBUMARTIST", "ALBUM ARTIST":
			t.AlbumArtist = clean(value)
		case "ARTIST":
			if t.Artist == "" { // first wins; multi-value tags repeat the key
				t.Artist = clean(value)
			}
		case "ALBUM":
			t.Album = clean(value)
		case "TITLE":
			t.Title = clean(value)
		}
	}
	return t
}

// --- iTunes atoms, in M4A and MP4 -------------------------------------------

// readMP4 walks moov > udta > meta > ilst, which is where iTunes writes tags.
func readMP4(r io.ReadSeeker) (Tags, error) {
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return Tags{}, err
	}
	ilst, err := findAtom(r, []string{"moov", "udta", "meta", "ilst"}, 0, 1<<62)
	if err != nil || ilst == nil {
		return Tags{}, nil
	}
	return parseILST(ilst), nil
}

// findAtom descends a path of atom types, returning the body of the last one.
func findAtom(r io.ReadSeeker, path []string, start, end int64) ([]byte, error) {
	if len(path) == 0 {
		return nil, nil
	}
	offset := start
	header := make([]byte, 8)
	for offset < end {
		if _, err := r.Seek(offset, io.SeekStart); err != nil {
			return nil, err
		}
		if _, err := io.ReadFull(r, header); err != nil {
			return nil, nil // ran out: not an error, just absent
		}
		size := int64(binary.BigEndian.Uint32(header[:4]))
		name := string(header[4:8])
		if size == 0 {
			size = end - offset // "to the end of the file"
		}
		if size < 8 || offset+size > end {
			return nil, nil
		}

		if name == path[0] {
			body := offset + 8
			// meta is a full atom: four bytes of version and flags before its
			// children. Descending without skipping them lands mid-atom and
			// finds nothing, which is the usual reason an M4A "has no tags".
			if name == "meta" {
				body += 4
			}
			if len(path) == 1 {
				if size-8 > maxTagBytes {
					return nil, nil
				}
				if _, err := r.Seek(body, io.SeekStart); err != nil {
					return nil, err
				}
				out := make([]byte, offset+size-body)
				if _, err := io.ReadFull(r, out); err != nil {
					return nil, nil
				}
				return out, nil
			}
			return findAtom(r, path[1:], body, offset+size)
		}
		offset += size
	}
	return nil, nil
}

// parseILST reads the tag atoms. Each holds a "data" atom with the value.
func parseILST(b []byte) Tags {
	var t Tags
	for len(b) >= 8 {
		size := int(binary.BigEndian.Uint32(b[:4]))
		name := string(b[4:8])
		if size < 8 || size > len(b) {
			break
		}
		value := dataAtom(b[8:size])
		switch name {
		case "\xa9ART":
			t.Artist = value
		case "aART":
			t.AlbumArtist = value
		case "\xa9alb":
			t.Album = value
		case "\xa9nam":
			t.Title = value
		}
		b = b[size:]
	}
	return t
}

// dataAtom pulls the text out of the "data" child: size, "data", four bytes of
// type, four reserved, then the value.
func dataAtom(b []byte) string {
	for len(b) >= 8 {
		size := int(binary.BigEndian.Uint32(b[:4]))
		if size < 8 || size > len(b) {
			return ""
		}
		if string(b[4:8]) == "data" && size >= 16 {
			return clean(string(b[16:size]))
		}
		b = b[size:]
	}
	return ""
}

// clean trims whitespace and drops the trailing NULs that pad fixed-width
// writers, which otherwise become part of a directory name.
func clean(s string) string {
	return strings.TrimSpace(strings.TrimRight(s, "\x00"))
}
