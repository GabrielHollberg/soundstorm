package library

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Duplicates. An upload used to be refused only when a file of the same name
// was already at its destination, and an iTunes library holds copies under
// other names: "03 Heathens.m4a" and "03 Heathens 1.m4a", the same song
// bought twice. Byte-for-byte they never match - iTunes writes a catalog id,
// a purchase date and its own copy of the artwork into every file - so the
// comparison has to be of the recording, not the file. Measured on a real
// library: of nine such pairs none were identical files, and five had
// identical audio.
//
// The fingerprint is the audio alone - the mdat of an MP4, the frames of an
// MP3 after its ID3 tag, the frames of a FLAC after its metadata blocks. A
// clean and an explicit version differ in their audio, so both are kept; only
// the same recording under a different listing is skipped.
//
// Only against files already in the same destination folder, only for music
// and audiobooks, and only on upload. The same song on an album and on a
// compilation is not a duplicate, and a deluxe edition in its own folder
// keeps every track even where it shares one with the standard. A file
// copied in by hand is never checked, which is also the way to add one
// anyway.
//
// Scoped to audio kinds on purpose - this is where the problem was found and
// measured, and it is the one place a full-file hash is worth its cost.
// Everything else only ever hits the plain "a file of this name already
// exists" check above findDuplicate, which is free. A picture library or a
// documents folder can hold thousands of same-sized, same-extension files,
// and duplicate detection there would mean reading every one of them, in
// full, on every single upload, for a case that in practice does not arise -
// two cameras do not produce byte-identical files under different names the
// way iTunes reliably does. If that ever needs revisiting, it needs its own
// measurement first, the same way this one was.

// DuplicateError says an upload was not added because the same recording, or
// the same file, is already beside where it was going.
type DuplicateError struct {
	Of    string // the name of the file already there
	Audio bool   // compared by recording rather than as a whole file
}

func (e *DuplicateError) Error() string {
	if e.Audio {
		return fmt.Sprintf("the same recording as %q is already here", e.Of)
	}
	return fmt.Sprintf("the same file as %q is already here", e.Of)
}

// audioFormats are the ones compared by their audio alone.
var audioFormats = map[string]bool{".m4a": true, ".m4b": true, ".mp4": true, ".aac": true, ".mp3": true, ".flac": true}

// IsDuplicate reports whether err is a skipped duplicate.
func IsDuplicate(err error) bool {
	var d *DuplicateError
	return errors.As(err, &d)
}

// span is the part of a file its fingerprint covers.
type span struct{ start, length int64 }

// fingerprintSpan finds the part of a file that is the recording, for the
// formats whose tags live inside the file. Anything else - and any file whose
// structure does not parse - is fingerprinted whole, which errs towards
// keeping both copies.
func fingerprintSpan(f *os.File, size int64, ext string) span {
	whole := span{0, size}
	var s span
	var ok bool
	switch strings.ToLower(ext) {
	case ".m4a", ".m4b", ".mp4", ".aac":
		s, ok = mp4Audio(f, size)
	case ".mp3":
		s, ok = mp3Audio(f, size)
	case ".flac":
		s, ok = flacAudio(f, size)
	}
	if !ok || s.length <= 0 {
		return whole
	}
	return s
}

// mp4Audio is the mdat atom's payload: the samples, and nothing iTunes
// writes about them. The atom walk is the same one internal/tags does.
func mp4Audio(f *os.File, size int64) (span, bool) {
	var off int64
	head := make([]byte, 16)
	for off+8 <= size {
		if _, err := f.ReadAt(head[:8], off); err != nil {
			return span{}, false
		}
		atomSize := int64(binary.BigEndian.Uint32(head[:4]))
		kind := string(head[4:8])
		hdr := int64(8)
		switch atomSize {
		case 1:
			if _, err := f.ReadAt(head[8:16], off+8); err != nil {
				return span{}, false
			}
			atomSize = int64(binary.BigEndian.Uint64(head[8:16]))
			hdr = 16
		case 0:
			atomSize = size - off
		}
		if atomSize < hdr || off+atomSize > size {
			return span{}, false
		}
		if kind == "mdat" {
			return span{off + hdr, atomSize - hdr}, true
		}
		off += atomSize
	}
	return span{}, false
}

// mp3Audio is everything between an ID3v2 tag at the front and an ID3v1 tag
// at the back. The v2 size is syncsafe in every version; a footer adds ten.
func mp3Audio(f *os.File, size int64) (span, bool) {
	start, end := int64(0), size
	head := make([]byte, 10)
	if _, err := f.ReadAt(head, 0); err == nil && string(head[:3]) == "ID3" {
		tagSize := int64(head[6]&0x7f)<<21 | int64(head[7]&0x7f)<<14 | int64(head[8]&0x7f)<<7 | int64(head[9]&0x7f)
		start = 10 + tagSize
		if head[5]&0x10 != 0 {
			start += 10
		}
	}
	if size >= 128 {
		tail := make([]byte, 3)
		if _, err := f.ReadAt(tail, size-128); err == nil && string(tail) == "TAG" {
			end = size - 128
		}
	}
	if start >= end {
		return span{}, false
	}
	return span{start, end - start}, true
}

// flacAudio is everything after the metadata blocks - which is where FLAC
// keeps its tags and its cover art.
func flacAudio(f *os.File, size int64) (span, bool) {
	head := make([]byte, 4)
	if _, err := f.ReadAt(head, 0); err != nil || string(head) != "fLaC" {
		return span{}, false
	}
	off := int64(4)
	for off+4 <= size {
		if _, err := f.ReadAt(head, off); err != nil {
			return span{}, false
		}
		last := head[0]&0x80 != 0
		length := int64(head[1])<<16 | int64(head[2])<<8 | int64(head[3])
		off += 4 + length
		if last {
			if off >= size {
				return span{}, false
			}
			return span{off, size - off}, true
		}
	}
	return span{}, false
}

// fingerprint hashes a file's span. ext is the format to read it as, passed
// rather than taken from path because an upload is staged as part-123456789,
// with no extension - read that way, a staged M4A was hashed whole while the
// copy already on the shelf was hashed by its audio, and the two could never
// match. Caught by the first test of the case this exists for.
func fingerprint(path, ext string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", 0, err
	}
	s := fingerprintSpan(f, info.Size(), ext)
	h := sha256.New()
	if _, err := io.Copy(h, io.NewSectionReader(f, s.start, s.length)); err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), s.length, nil
}

// spanLength is a sibling's fingerprint length, without hashing it - so only
// the rare sibling of exactly the same length is ever read in full.
func spanLength(path string) (int64, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		return 0, false
	}
	return fingerprintSpan(f, info.Size(), filepath.Ext(path)).length, true
}

// findDuplicate looks for a file in dir holding the same recording as staged,
// which is to be saved there as name.
func findDuplicate(staged, dir, name string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		// No folder yet means nothing to be a duplicate of.
		return "", nil
	}
	want := strings.ToLower(filepath.Ext(name))
	var sum string
	var length int64 = -1
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		// The same format only: an MP3 and an M4A of one song are two
		// encodings, not two copies.
		if strings.ToLower(filepath.Ext(e.Name())) != want {
			continue
		}
		if length < 0 {
			if sum, length, err = fingerprint(staged, want); err != nil {
				return "", err
			}
		}
		sibling := filepath.Join(dir, e.Name())
		if n, ok := spanLength(sibling); !ok || n != length {
			continue
		}
		other, _, err := fingerprint(sibling, want)
		if err == nil && other == sum {
			return e.Name(), nil
		}
	}
	return "", nil
}
