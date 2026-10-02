package library

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/photoimport"
	"github.com/GabrielHollberg/soundstorm/internal/tags"
)

// A different file whose name is taken, kept beside the one there, is named
// for what makes it different - not "(2)", which reads as a copy (the
// owner's point): a photo by the camera that took it, a film by its picture
// size ("Dune (2021) - 2160p.mkv", the name Jellyfin reads as a second
// version of the same film), a song by its quality or length, an audiobook
// by who reads it; and when nothing tells them apart, by the day it was
// added. Read from the file itself, the little that answers the question.

// Traits are what can tell two files of one name apart.
type Traits struct {
	Camera   string    // a photo's maker and model
	Taken    time.Time // when a photo was taken
	Width    int       // a video's picture, in pixels
	Height   int
	Seconds  float64 // how long a song, book or film runs
	Kbps     int     // a song's bitrate
	Narrator string  // who reads an audiobook
}

// TraitsOf reads the Traits of the file at path, as a file of kind.
func TraitsOf(kind media.Kind, path string) Traits {
	f, err := os.Open(path)
	if err != nil {
		return Traits{}
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Traits{}
	}
	return ReadTraits(kind, f, info.Size())
}

// traitsWindow bounds how much of a file is read for its Traits from either
// end: a phone's EXIF, an MKV's track list and an MP4's moov all sit there.
const traitsWindow = 4 << 20

// ReadTraits reads Traits from r, size bytes long. Anything it cannot read
// stays zero: the name falls back to the day it was added.
func ReadTraits(kind media.Kind, r io.ReaderAt, size int64) (t Traits) {
	defer func() {
		if recover() != nil {
			t = Traits{} // a crafted file names nothing; it does not stop a save
		}
	}()
	head := make([]byte, min(size, traitsWindow))
	n, _ := r.ReadAt(head, 0)
	head = head[:n]
	switch kind {
	case media.KindPicture:
		t.Camera = photoimport.ExifCamera(head)
		t.Taken, _ = photoimport.ExifTaken(head)
	case media.KindVideo, media.KindTV:
		if bytes.HasPrefix(head, []byte{0x1A, 0x45, 0xDF, 0xA3}) {
			t.Width, t.Height, t.Seconds = mkvPicture(head)
		} else {
			t.Width, t.Height, t.Seconds = mp4Picture(r, size)
		}
	case media.KindMusic, media.KindAudiobook:
		t.Seconds = audioSeconds(r, size, head)
		if t.Seconds > 1 {
			t.Kbps = int(math.Round(float64(size) * 8 / t.Seconds / 1000))
		}
		if kind == media.KindAudiobook {
			if tg, err := tags.Read(io.NewSectionReader(r, 0, size)); err == nil {
				t.Narrator = tg.Narrator
			}
		}
	}
	return t
}

// Label is what a new file's name gains beside one already there: the
// first trait that tells them apart, else the day it was added.
func Label(kind media.Kind, inc, have Traits, now time.Time) string {
	switch kind {
	case media.KindPicture:
		if inc.Camera != "" && !strings.EqualFold(inc.Camera, have.Camera) {
			return inc.Camera
		}
		if !inc.Taken.IsZero() && !inc.Taken.Equal(have.Taken) {
			return inc.Taken.Format("2006-01-02 15.04.05")
		}
	case media.KindVideo, media.KindTV:
		if p := resolution(inc); p != "" && p != resolution(have) {
			return p
		}
		if differ(inc.Seconds, have.Seconds, 60) {
			return length(inc.Seconds)
		}
	case media.KindMusic:
		if inc.Kbps > 0 && have.Kbps > 0 && math.Abs(float64(inc.Kbps-have.Kbps)) > float64(have.Kbps)/10 {
			return fmt.Sprintf("%d kbps", inc.Kbps)
		}
		if differ(inc.Seconds, have.Seconds, 2) {
			return length(inc.Seconds)
		}
	case media.KindAudiobook:
		if inc.Narrator != "" && !strings.EqualFold(inc.Narrator, have.Narrator) {
			return "read by " + inc.Narrator
		}
		if differ(inc.Seconds, have.Seconds, 60) {
			return length(inc.Seconds)
		}
	}
	return "added " + now.Format("2006-01-02")
}

func differ(a, b, by float64) bool { return a > 0 && b > 0 && math.Abs(a-b) > by }

// resolution names a picture size as people do: 2160p, 1080p, 720p...
func resolution(t Traits) string {
	w, h := t.Width, t.Height
	switch {
	case w <= 0 || h <= 0:
		return ""
	case h >= 2000 || w >= 3800:
		return "2160p"
	case h >= 1000 || w >= 1900:
		return "1080p"
	case h >= 700 || w >= 1260:
		return "720p"
	case h >= 560:
		return "576p"
	default:
		return "480p"
	}
}

// length is how long something runs, as a name can carry it: "3m 45s",
// "2h 14m".
func length(s float64) string {
	total := int(math.Round(s))
	if total >= 3600 {
		return fmt.Sprintf("%dh %02dm", total/3600, total%3600/60)
	}
	return fmt.Sprintf("%dm %02ds", total/60, total%60)
}

// labelName makes a label safe in a file name.
func labelName(label string) string {
	label = strings.Map(func(r rune) rune {
		switch {
		case r < 0x20 || strings.ContainsRune(`/\:*?"<>|`, r):
			return '-'
		}
		return r
	}, label)
	label = strings.Trim(strings.TrimSpace(label), ".")
	if runes := []rune(label); len(runes) > 60 {
		label = string(runes[:60])
	}
	return label
}

// DistinctName is dest named for label - "Stem - label.ext" - or, should
// that be taken too, the same with the time; "" when nothing is free.
func DistinctName(dest, label string) string {
	dir, base := filepath.Split(dest)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	label = labelName(label)
	if label == "" {
		label = "added " + time.Now().Format("2006-01-02")
	}
	free := func(name string) bool {
		_, err := os.Lstat(filepath.Join(dir, name))
		return errors.Is(err, os.ErrNotExist)
	}
	if c := stem + " - " + label + ext; free(c) {
		return filepath.Join(dir, c)
	}
	if c := stem + " - " + label + " " + time.Now().Format("15.04.05") + ext; free(c) {
		return filepath.Join(dir, c)
	}
	for i := 2; i < 1000; i++ {
		if c := fmt.Sprintf("%s - %s %d%s", stem, label, i, ext); free(c) {
			return filepath.Join(dir, c)
		}
	}
	return ""
}

// ChosenName is the name somebody typed for the new file, kept to one
// segment and its own extension; "" when it cannot be used.
func ChosenName(dest, typed string) string {
	typed = strings.TrimSpace(typed)
	if typed == "" {
		return ""
	}
	ext := filepath.Ext(dest)
	if !strings.EqualFold(filepath.Ext(typed), ext) {
		typed += ext
	}
	clean, err := cleanRelPath(typed)
	if err != nil || strings.Contains(clean, "/") {
		return ""
	}
	c := filepath.Join(filepath.Dir(dest), clean)
	if _, err := os.Lstat(c); !errors.Is(err, os.ErrNotExist) {
		return ""
	}
	return c
}

// NameFor is where a different file whose name dest is taken goes: the
// name somebody chose when it is free, else one for what makes it
// different from the one there.
func NameFor(kind media.Kind, staged, dest, chosen string) string {
	if c := ChosenName(dest, chosen); c != "" {
		return c
	}
	label := Label(kind, TraitsOf(kind, staged), TraitsOf(kind, dest), time.Now())
	return DistinctName(dest, label)
}

// --- the little each format says --------------------------------------------

// mp4Picture is the largest track's picture size and the movie's length,
// from moov wherever it sits.
func mp4Picture(r io.ReaderAt, size int64) (w, h int, seconds float64) {
	moov := mp4Box(r, 0, size, "moov")
	if moov == nil {
		return 0, 0, 0
	}
	seconds = mvhdSeconds(moov)
	for off := 8; off+8 <= len(moov); {
		n := int(binary.BigEndian.Uint32(moov[off:]))
		if n < 8 || off+n > len(moov) {
			break
		}
		if string(moov[off+4:off+8]) == "trak" {
			trak := moov[off+8 : off+n]
			for t := 0; t+8 <= len(trak); {
				m := int(binary.BigEndian.Uint32(trak[t:]))
				if m < 8 || t+m > len(trak) {
					break
				}
				if string(trak[t+4:t+8]) == "tkhd" && m >= 92 {
					box := trak[t : t+m]
					at := 84 // from the box's start: width at 84 in version 0, 96 in 1
					if box[8] == 1 {
						at = 96
					}
					if at+8 <= len(box) {
						tw := int(binary.BigEndian.Uint32(box[at:]) >> 16)
						th := int(binary.BigEndian.Uint32(box[at+4:]) >> 16)
						if th > h {
							w, h = tw, th
						}
					}
				}
				t += m
			}
		}
		off += n
	}
	return w, h, seconds
}

// mp4Box is the first top-level box of a type, read whole (up to 8MB), or nil.
func mp4Box(r io.ReaderAt, from, size int64, want string) []byte {
	var hdr [16]byte
	for off := from; off+8 <= size; {
		if _, err := r.ReadAt(hdr[:8], off); err != nil {
			return nil
		}
		n := int64(binary.BigEndian.Uint32(hdr[:4]))
		typ := string(hdr[4:8])
		switch n {
		case 0:
			n = size - off
		case 1:
			if _, err := r.ReadAt(hdr[:16], off); err != nil {
				return nil
			}
			n = int64(binary.BigEndian.Uint64(hdr[8:16]))
		}
		if n < 8 || n > size-off {
			return nil
		}
		if typ == want {
			if n > 8<<20 {
				return nil
			}
			b := make([]byte, n)
			if _, err := r.ReadAt(b, off); err != nil {
				return nil
			}
			return b
		}
		off += n
	}
	return nil
}

// mvhdSeconds is a moov's length, from its mvhd.
func mvhdSeconds(moov []byte) float64 {
	i := bytes.Index(moov, []byte("mvhd"))
	if i < 4 || i+28 > len(moov) {
		return 0
	}
	b := moov[i+4:]
	if b[0] == 1 {
		if len(b) < 32 {
			return 0
		}
		scale := binary.BigEndian.Uint32(b[20:])
		dur := binary.BigEndian.Uint64(b[24:])
		if scale == 0 {
			return 0
		}
		return float64(dur) / float64(scale)
	}
	scale := binary.BigEndian.Uint32(b[12:])
	dur := binary.BigEndian.Uint32(b[16:])
	if scale == 0 {
		return 0
	}
	return float64(dur) / float64(scale)
}

// mkvPicture is a Matroska file's picture size and length, from its track
// list and segment info, walking the EBML elements in its first megabytes.
func mkvPicture(b []byte) (w, h int, seconds float64) {
	scale := 1000000.0 // TimecodeScale's default: milliseconds
	var duration float64
	var walk func(b []byte, depth int)
	walk = func(b []byte, depth int) {
		for len(b) > 0 && depth < 8 {
			id, idLen := ebmlID(b)
			if idLen == 0 {
				return
			}
			size, sizeLen, unknown := ebmlSize(b[idLen:])
			if sizeLen == 0 {
				return
			}
			body := b[idLen+sizeLen:]
			if !unknown && size < int64(len(body)) {
				body = body[:size]
			}
			switch id {
			case 0x18538067, 0x1654AE6B, 0xAE, 0xE0, 0x1549A966: // Segment, Tracks, TrackEntry, Video, Info
				walk(body, depth+1)
			case 0x1F43B675: // Cluster: the pictures themselves, nothing more to learn
				return
			case 0xB0:
				w = int(ebmlUint(body))
			case 0xBA:
				h = int(ebmlUint(body))
			case 0x2AD7B1:
				if v := ebmlUint(body); v > 0 {
					scale = float64(v)
				}
			case 0x4489:
				duration = ebmlFloat(body)
			}
			if unknown || size > int64(len(b)-idLen-sizeLen) {
				return
			}
			b = b[idLen+sizeLen+int(size):]
		}
	}
	walk(b, 0)
	if duration > 0 {
		seconds = duration * scale / 1e9
	}
	return w, h, seconds
}

func ebmlID(b []byte) (uint32, int) {
	if len(b) == 0 {
		return 0, 0
	}
	n := 0
	for mask := byte(0x80); n < 4 && b[0]&mask == 0; mask >>= 1 {
		n++
	}
	n++
	if n > 4 || len(b) < n {
		return 0, 0
	}
	var id uint32
	for i := 0; i < n; i++ {
		id = id<<8 | uint32(b[i])
	}
	return id, n
}

func ebmlSize(b []byte) (size int64, n int, unknown bool) {
	if len(b) == 0 {
		return 0, 0, false
	}
	for mask := byte(0x80); n < 8 && b[0]&mask == 0; mask >>= 1 {
		n++
	}
	n++
	if n > 8 || len(b) < n {
		return 0, 0, false
	}
	v := int64(b[0] & (0xFF >> n))
	all := v == int64(0xFF>>n)
	for i := 1; i < n; i++ {
		v = v<<8 | int64(b[i])
		all = all && b[i] == 0xFF
	}
	return v, n, all
}

func ebmlUint(b []byte) uint64 {
	var v uint64
	for i := 0; i < len(b) && i < 8; i++ {
		v = v<<8 | uint64(b[i])
	}
	return v
}

func ebmlFloat(b []byte) float64 {
	switch len(b) {
	case 4:
		return float64(math.Float32frombits(binary.BigEndian.Uint32(b)))
	case 8:
		return math.Float64frombits(binary.BigEndian.Uint64(b))
	}
	return 0
}

// audioSeconds is how long a song or book runs: an M4A's mvhd, a FLAC's
// stream info, an MP3's size at its first frame's bitrate.
func audioSeconds(r io.ReaderAt, size int64, head []byte) float64 {
	switch {
	case len(head) >= 8 && string(head[4:8]) == "ftyp":
		if moov := mp4Box(r, 0, size, "moov"); moov != nil {
			return mvhdSeconds(moov)
		}
	case bytes.HasPrefix(head, []byte("fLaC")) && len(head) >= 26:
		info := head[8:]
		rate := uint64(info[10])<<12 | uint64(info[11])<<4 | uint64(info[12])>>4
		samples := uint64(info[13]&0x0F)<<32 | uint64(binary.BigEndian.Uint32(info[14:]))
		if rate > 0 {
			return float64(samples) / float64(rate)
		}
	default:
		start := 0
		if bytes.HasPrefix(head, []byte("ID3")) && len(head) >= 10 {
			start = 10 + (int(head[6])<<21 | int(head[7])<<14 | int(head[8])<<7 | int(head[9]))
		}
		for i := start; i+4 <= len(head) && i < start+64<<10; i++ {
			if head[i] == 0xFF && head[i+1]&0xE0 == 0xE0 {
				if kbps := mp3Kbps(head[i:]); kbps > 0 {
					return float64(size-int64(i)) * 8 / float64(kbps*1000)
				}
			}
		}
	}
	return 0
}

// mp3Kbps is an MPEG-1 or 2 layer III frame header's bitrate, or 0.
func mp3Kbps(h []byte) int {
	version := (h[1] >> 3) & 3 // 3: MPEG-1, 2: MPEG-2, 0: MPEG-2.5
	layer := (h[1] >> 1) & 3   // 1: layer III
	index := int(h[2] >> 4)
	if layer != 1 || index == 0 || index == 15 || version == 1 {
		return 0
	}
	v1 := []int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320}
	v2 := []int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160}
	if version == 3 {
		return v1[index]
	}
	return v2[index]
}

// TakenName is the file name a different file of size bytes would get at
// dest ("movies/Dune/Dune.mkv"), a name already taken on kind's shelf - its
// Traits read from r, which may hold only the two ends of it (the page
// sends those before the file, to show the name in its question).
func (l *Library) TakenName(kind media.Kind, dest string, r io.ReaderAt, size int64) (string, error) {
	abs, err := l.shelfFile(kind, dest)
	if err != nil {
		return "", err
	}
	label := Label(kind, ReadTraits(kind, r, size), TraitsOf(kind, abs), time.Now())
	name := DistinctName(abs, label)
	if name == "" {
		return "", errors.New("no free name")
	}
	return filepath.Base(name), nil
}
