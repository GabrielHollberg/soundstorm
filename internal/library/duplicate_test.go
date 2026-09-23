package library

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/GabrielHollberg/soundstorm/internal/media"
)

// m4a builds an MP4 whose metadata and audio can be varied separately: a
// moov atom carrying tags (and, in life, artwork) and an mdat carrying the
// samples - the two halves iTunes changes and does not change between two
// purchases of one song.
func m4a(tags, audio string) []byte {
	atom := func(kind string, payload []byte) []byte {
		b := make([]byte, 8, 8+len(payload))
		binary.BigEndian.PutUint32(b, uint32(8+len(payload)))
		copy(b[4:], kind)
		return append(b, payload...)
	}
	var out []byte
	out = append(out, atom("ftyp", []byte("M4A \x00\x00\x00\x00"))...)
	out = append(out, atom("moov", []byte("udta:"+tags))...)
	out = append(out, atom("mdat", []byte(audio))...)
	return out
}

// mp3 wraps the same frames in different ID3v2 tags.
func mp3(tags map[string]string, frames string) []byte {
	return append(id3(tags)[:len(id3(tags))-4], []byte(frames)...)
}

// flac puts different metadata blocks - where FLAC keeps tags and cover art -
// in front of the same frames.
func flac(comment, frames string) []byte {
	var b bytes.Buffer
	b.WriteString("fLaC")
	block := []byte(comment)
	b.Write([]byte{0x80 | 4, byte(len(block) >> 16), byte(len(block) >> 8), byte(len(block))})
	b.Write(block)
	b.WriteString(frames)
	return b.Bytes()
}

func expectDuplicate(t *testing.T, l *Library, kind media.Kind, rel string, content []byte, of string) {
	t.Helper()
	_, err := l.Save(kind, rel, bytes.NewReader(content))
	d, ok := err.(*DuplicateError)
	if !ok {
		t.Fatalf("%s: err = %v, want a duplicate of %s", rel, err, of)
	}
	if d.Of != of {
		t.Errorf("%s: duplicate of %q, want %q", rel, d.Of, of)
	}
}

// The case that started it: "03 Heathens 1.m4a" beside "03 Heathens.m4a",
// the same song bought twice. Different catalog ids, purchase dates and
// artwork; identical audio.
func TestTheSameRecordingUnderAnotherNameIsSkipped(t *testing.T) {
	l := newLibrary(t)
	const audio = "the samples of Heathens, identical in both purchases"

	saved(t, l, media.KindMusic, "Suicide Squad/03 Heathens.m4a", m4a("cnID=431242bf purd=2025", audio))
	expectDuplicate(t, l, media.KindMusic, "Suicide Squad/03 Heathens 1.m4a",
		m4a("cnID=43126ecf purd=2024 covr=deluxe-art", audio), "03 Heathens.m4a")

	tags := map[string]string{"TPE1": "Radiohead", "TALB": "In Rainbows"}
	saved(t, l, media.KindMusic, "In Rainbows/01.mp3", mp3(tags, "\xff\xfbframes"))
	retagged := map[string]string{"TPE1": "Radiohead", "TALB": "In Rainbows", "COMM": "bought twice"}
	expectDuplicate(t, l, media.KindMusic, "In Rainbows/01 copy.mp3", mp3(retagged, "\xff\xfbframes"), "01.mp3")

	saved(t, l, media.KindMusic, "Laughing Stock/01.flac", flac("TITLE=Myrrhman", "flac frames"))
	expectDuplicate(t, l, media.KindMusic, "Laughing Stock/01 (2).flac", flac("TITLE=Myrrhman COVER=png", "flac frames"), "01.flac")
}

// Everything that sounds different is kept: a clean and an explicit version,
// a remaster, a radio edit. The check is of identical audio, nothing looser.
func TestDifferentAudioIsKept(t *testing.T) {
	l := newLibrary(t)
	saved(t, l, media.KindMusic, "Album/06 Notice Me.m4a", m4a("explicit", "the explicit samples"))
	saved(t, l, media.KindMusic, "Album/06 Notice Me 1.m4a", m4a("explicit", "the clean samples"))
}

// The same song in another folder is not a duplicate - an album and a
// compilation both have it, and a deluxe edition in its own folder must stay
// whole - and neither is another format of it.
func TestOtherFoldersAndOtherFormatsAreKept(t *testing.T) {
	l := newLibrary(t)
	const audio = "See You Again"
	saved(t, l, media.KindMusic, "Furious 7/07 See You Again.m4a", m4a("7 of 16", audio))
	saved(t, l, media.KindMusic, "Furious 7 (Deluxe)/07 See You Again.m4a", m4a("7 of 22", audio))
	saved(t, l, media.KindMusic, "Furious 7/07 See You Again.m4b", m4a("7 of 16", audio))
}

// Duplicate detection is scoped to music and audiobooks - the two kinds it
// was measured against - and nowhere else, because a picture or documents
// folder can hold thousands of same-sized files and hashing every one of
// them on every upload has a cost with no real-world case behind it. A
// second copy of a photo under another name is kept; only the same *name*
// is ever refused, by the plain check that runs before this one.
func TestDuplicateDetectionIsScopedToAudio(t *testing.T) {
	l := newLibrary(t)
	saved(t, l, media.KindPicture, "Holiday/IMG_0001.jpg", []byte("\xff\xd8\xffphoto"))
	saved(t, l, media.KindPicture, "Holiday/IMG_0001 (1).jpg", []byte("\xff\xd8\xffphoto"))
	saved(t, l, media.KindDocument, "Taxes/2024.pdf", []byte("%PDF-1.4 form"))
	saved(t, l, media.KindDocument, "Taxes/2024 (1).pdf", []byte("%PDF-1.4 form"))

	// The name check still applies: the exact same name is still refused.
	if _, err := l.Save(media.KindPicture, "Holiday/IMG_0001.jpg", bytes.NewReader([]byte("different bytes entirely"))); !errors.Is(err, ErrAlreadyThere) {
		t.Errorf("err = %v, want ErrAlreadyThere", err)
	}
}

// A file that claims a format it does not have is compared whole rather than
// guessed at, which errs towards keeping both.
func TestAnUnparseableFileIsComparedWhole(t *testing.T) {
	l := newLibrary(t)
	saved(t, l, media.KindMusic, "Album/a.m4a", []byte("not really an mp4 at all"))
	saved(t, l, media.KindMusic, "Album/b.m4a", []byte("not really an mp4 either"))
}
