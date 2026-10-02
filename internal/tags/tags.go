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
	// Narrator is who reads an audiobook: its own field when the file has
	// one, else the composer, where audiobook stores put the narrator. Used
	// only to tell two editions of a book apart (library/distinct.go).
	Narrator string
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
	// 2.2 had a compression flag here whose scheme was never defined; nobody
	// can read such a tag, so it says nothing.
	if major == 2 && flags&0x40 != 0 {
		return Tags{}, nil
	}

	if _, err := r.Seek(10, io.SeekStart); err != nil {
		return Tags{}, err
	}
	body := make([]byte, size)
	if _, err := io.ReadFull(r, body); err != nil {
		return Tags{}, fmt.Errorf("read tag: %w", err)
	}

	// Unsynchronization rewrites every 0xFF 0x00 pair so that no part of the
	// tag can look like the start of an audio frame to a decoder that does not
	// understand tags. Undo it, or every frame length after the first such
	// pair is wrong and the walk below falls off the end.
	//
	// Only up to 2.3 is it undone across the whole tag. In 2.4 a frame's size
	// counts the bytes as stored, unsynchronized, so the frames are cut out
	// first and each is undone on its own (below).
	unsynced := flags&0x80 != 0
	if unsynced && major < 4 {
		body = undoUnsync(body)
	}
	// An extended header sits between the header and the frames and is not
	// one; skip it by the length it declares. 2.2 has none.
	if major >= 3 && flags&0x40 != 0 && len(body) >= 4 {
		skip := syncsafe(body[:4])
		if major < 4 {
			skip = int(binary.BigEndian.Uint32(body[:4])) + 4
		}
		if skip > 0 && skip < len(body) {
			body = body[skip:]
		}
	}

	if major == 2 {
		return readID3v22(body), nil
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
		format := body[9]
		payload := body[10 : 10+frameSize]
		body = body[10+frameSize:]

		if major >= 4 {
			// Compressed or encrypted: nothing here can read it.
			if format&0x0C != 0 {
				continue
			}
			// The tag's flag means every frame is unsynchronized; a frame
			// may also say so for itself.
			if unsynced || format&0x02 != 0 {
				payload = undoUnsync(payload)
			}
			// A data length indicator: four syncsafe bytes giving the
			// frame's real length, before the text.
			if format&0x01 != 0 {
				if len(payload) < 4 {
					continue
				}
				payload = payload[4:]
			}
		} else if format&0xC0 != 0 {
			continue // 2.3: compressed or encrypted
		}

		switch id {
		case "TPE1":
			t.Artist = decodeID3Text(payload)
		case "TPE2":
			t.AlbumArtist = decodeID3Text(payload)
		case "TALB":
			t.Album = decodeID3Text(payload)
		case "TIT2":
			t.Title = decodeID3Text(payload)
		case "TCOM":
			if t.Narrator == "" {
				t.Narrator = decodeID3Text(payload)
			}
		}
	}
	return t, nil
}

// readID3v22 walks the frames of an ID3v2.2 tag, which is what iTunes wrote
// into MP3s for years: three-letter frame names, three-byte sizes and a
// six-byte header with no flags. The text inside is the same as later
// versions'.
func readID3v22(body []byte) Tags {
	var t Tags
	for len(body) >= 6 {
		id := string(body[0:3])
		if id == "\x00\x00\x00" {
			break
		}
		frameSize := int(body[3])<<16 | int(body[4])<<8 | int(body[5])
		if frameSize <= 0 || frameSize+6 > len(body) {
			break
		}
		payload := body[6 : 6+frameSize]
		body = body[6+frameSize:]
		switch id {
		case "TP1":
			t.Artist = decodeID3Text(payload)
		case "TP2":
			t.AlbumArtist = decodeID3Text(payload)
		case "TAL":
			t.Album = decodeID3Text(payload)
		case "TT2":
			t.Title = decodeID3Text(payload)
		case "TCM":
			t.Narrator = decodeID3Text(payload)
		}
	}
	return t
}

// undoUnsync turns every 0xFF 0x00 back into the 0xFF it stood for.
func undoUnsync(b []byte) []byte {
	return bytes.ReplaceAll(b, []byte{0xFF, 0x00}, []byte{0xFF})
}

// decodeID3Text reads a text frame: one encoding byte, then the string.
//
// A 2.4 frame can hold several values separated by NULs ("A\x00B"). Only the
// first is kept: they are usually several artists, and run together they
// would become one artist called "AB".
func decodeID3Text(b []byte) string {
	if len(b) < 1 {
		return ""
	}
	encoding, text := b[0], b[1:]
	switch encoding {
	case 0: // ISO-8859-1, one byte per rune
		if i := bytes.IndexByte(text, 0); i >= 0 {
			text = text[:i]
		}
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
		if i := bytes.IndexByte(text, 0); i >= 0 {
			text = text[:i]
		}
		return clean(string(text))
	}
}

// decodeUTF16 decodes up to the first NUL code unit, which ends a value.
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
		var u uint16
		if bigEndian {
			u = binary.BigEndian.Uint16(b[i : i+2])
		} else {
			u = binary.LittleEndian.Uint16(b[i : i+2])
		}
		if u == 0 {
			break
		}
		units = append(units, u)
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
	// A real file has a handful of blocks; empty ones by the million kept an
	// upload busy for minutes (a security review).
	for blocks := 0; blocks < 1000; blocks++ {
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
	return Tags{}, nil
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
		case "NARRATOR", "PERFORMER":
			t.Narrator = clean(value)
		case "COMPOSER":
			if t.Narrator == "" {
				t.Narrator = clean(value)
			}
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
	for atoms := 0; offset < end && atoms < 10000; atoms++ {
		if _, err := r.Seek(offset, io.SeekStart); err != nil {
			return nil, err
		}
		if _, err := io.ReadFull(r, header); err != nil {
			return nil, nil // ran out: not an error, just absent
		}
		size := int64(binary.BigEndian.Uint32(header[:4]))
		name := string(header[4:8])
		hdr := int64(8)
		switch size {
		case 0:
			size = end - offset // "to the end of the file"
		case 1:
			// A 64-bit size follows (an mdat over 4GB before moov, which
			// left a big m4b looking untagged - a review).
			big := make([]byte, 8)
			if _, err := io.ReadFull(r, big); err != nil {
				return nil, nil
			}
			size = int64(binary.BigEndian.Uint64(big))
			hdr = 16
		}
		if size < hdr || size > end-offset {
			return nil, nil
		}

		if name == path[0] {
			body := offset + hdr
			// meta is a full atom: four bytes of version and flags before its
			// children. Descending without skipping them lands mid-atom and
			// finds nothing, which is the usual reason an M4A "has no tags".
			if name == "meta" {
				body += 4
			}
			if len(path) == 1 {
				if size-hdr > maxTagBytes {
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
		case "\xa9nrt":
			t.Narrator = value
		case "\xa9wrt":
			if t.Narrator == "" {
				t.Narrator = value
			}
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
