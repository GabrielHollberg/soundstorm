package tags

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"
)

func mp4box(name string, body ...[]byte) []byte {
	b := bytes.Join(body, nil)
	out := make([]byte, 8, 8+len(b))
	binary.BigEndian.PutUint32(out, uint32(8+len(b)))
	copy(out[4:], name)
	return append(out, b...)
}

func mvhd(t time.Time) []byte {
	b := make([]byte, 100)
	secs := t.Sub(time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)) / time.Second
	binary.BigEndian.PutUint32(b[4:8], uint32(secs))
	return mp4box("mvhd", b)
}

// iPhoneMeta is a QuickTime meta box with the make and the creation date.
func iPhoneMeta(date string) []byte {
	key := func(name string) []byte {
		b := make([]byte, 8)
		binary.BigEndian.PutUint32(b, uint32(8+len(name)))
		copy(b[4:], "mdta")
		return append(b, name...)
	}
	keysBody := make([]byte, 8)
	binary.BigEndian.PutUint32(keysBody[4:], 2)
	keysBody = append(keysBody, key("com.apple.quicktime.make")...)
	keysBody = append(keysBody, key("com.apple.quicktime.creationdate")...)
	item := func(index int, value string) []byte {
		data := mp4box("data", make([]byte, 8), []byte(value))
		name := make([]byte, 4)
		binary.BigEndian.PutUint32(name, uint32(index))
		return mp4box(string(name), data)
	}
	return mp4box("meta",
		mp4box("hdlr", make([]byte, 24)),
		mp4box("keys", keysBody),
		mp4box("ilst", item(1, "Apple"), item(2, date)))
}

func TestPhoneVideosAreToldFromFilms(t *testing.T) {
	filmed := time.Date(2019, 12, 24, 19, 30, 0, 0, time.UTC)
	mdat := mp4box("mdat", make([]byte, 64))

	// An iPhone's: its own clock, with its zone, wins over mvhd's.
	iphone := append(mp4box("ftyp", []byte("qt  ")), mdat...)
	iphone = append(iphone, mp4box("moov", mvhd(filmed), iPhoneMeta("2019-12-24T13:30:00-0600"))...)
	got := videoCamera(bytes.NewReader(iphone), int64(len(iphone)))
	if !got.Filmed || got.Taken.Format("2006-01-02 15:04 -0700") != "2019-12-24 13:30 -0600" {
		t.Fatalf("iPhone video: %+v", got)
	}

	// A camera's, moov first: ©mak in udta, the date from mvhd.
	camera := append(mp4box("moov", mvhd(filmed), mp4box("udta", mp4box("\xa9mak", []byte("Canon")))), mdat...)
	got = videoCamera(bytes.NewReader(camera), int64(len(camera)))
	if !got.Filmed || !got.Taken.Equal(filmed) {
		t.Fatalf("camera video: %+v", got)
	}

	// A ripped film: an encoder's name and a creation time, which say nothing.
	film := append(mp4box("moov", mvhd(filmed), mp4box("udta", mp4box("\xa9too", []byte("HandBrake 1.6")), mp4box("\xa9nam", []byte("Dune")))), mdat...)
	if got := videoCamera(bytes.NewReader(film), int64(len(film))); got.Filmed || !got.Taken.IsZero() {
		t.Fatalf("a film taken for a home video: %+v", got)
	}

	// Not an MP4 at all.
	if got := videoCamera(bytes.NewReader([]byte("\x1a\x45\xdf\xa3 matroska")), 13); got.Filmed {
		t.Fatalf("an mkv: %+v", got)
	}
}
