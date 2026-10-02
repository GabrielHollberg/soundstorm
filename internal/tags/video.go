package tags

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"strings"
	"time"
)

// Camera says whether a video was filmed by a phone or a camera, from what
// those write inside an MP4 or MOV and a film never carries: the maker and
// model (an iPhone's com.apple.quicktime.make, an Android's
// com.android.version, the ©mak and ©mod a camera writes), where it was
// filmed (©xyz), or a camera maker's own box (GoPro's FIRM, Canon's CNCV,
// Samsung's smta). An encoder's name (©too, which HandBrake and ffmpeg
// write into ripped films) does not count.
//
// It reads only the moov box's small parts, wherever the box sits. It is not
// a video parser and must not become one: the question is "did a device film
// this", which decides whether a dropped video is a home video for Photos or
// a film.
type Camera struct {
	Filmed bool      // a phone or a camera made it
	Taken  time.Time // when, if the file says; zero otherwise
}

// cameraBoxes are boxes inside moov/udta that only a filming device writes.
var cameraBoxes = map[string]bool{
	"\xa9mak": true, "\xa9mod": true, "\xa9xyz": true,
	"FIRM": true, "CNCV": true, "CNTH": true, "CNMN": true, "smta": true,
	"manu": true, "modl": true,
}

// cameraKeys are QuickTime metadata keys (moov/meta/keys) that only a
// filming device writes.
var cameraKeys = []string{
	"com.apple.quicktime.make", "com.apple.quicktime.model",
	"com.apple.quicktime.location.ISO6709",
	"com.android.version", "com.android.capture.fps", "com.android.manufacturer", "com.android.model",
}

// maxMetaBytes bounds a moov/meta box read whole: a phone's is a few KB.
const maxMetaBytes = 1 << 20

// VideoCamera reads Camera from the file at path. Anything it cannot read is
// "not filmed": the video stays where the name put it.
func VideoCamera(path string) Camera {
	f, err := os.Open(path)
	if err != nil {
		return Camera{}
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Camera{}
	}
	return videoCamera(f, info.Size())
}

func videoCamera(r io.ReadSeeker, size int64) Camera {
	var out Camera
	moovStart, moovEnd, ok := findBox(r, "moov", 0, size)
	if !ok {
		return out
	}
	for _, b := range boxes(r, moovStart, moovEnd) {
		switch b.name {
		case "mvhd":
			if t, ok := mvhdCreated(r, b.start, b.end); ok && out.Taken.IsZero() {
				out.Taken = t
			}
		case "udta":
			for _, c := range boxes(r, b.start, b.end) {
				if cameraBoxes[c.name] {
					out.Filmed = true
				}
			}
		case "meta":
			if b.end-b.start > maxMetaBytes {
				continue
			}
			body := make([]byte, b.end-b.start)
			if _, err := r.Seek(b.start, io.SeekStart); err != nil {
				continue
			}
			if _, err := io.ReadFull(r, body); err != nil {
				continue
			}
			for _, k := range cameraKeys {
				if bytes.Contains(body, []byte(k)) {
					out.Filmed = true
				}
			}
			// The phone's own clock, with its time zone, beats mvhd's.
			if t, ok := quicktimeCreationDate(body); ok {
				out.Taken = t
			}
		}
	}
	// A creation time on a video a device did not make is an encoder's, and
	// says nothing about when anything was filmed.
	if !out.Filmed {
		out.Taken = time.Time{}
	}
	return out
}

type box struct {
	name       string
	start, end int64 // the body, after the header
}

// boxes lists the boxes between start and end, at most a few thousand.
func boxes(r io.ReadSeeker, start, end int64) []box {
	var out []box
	head := make([]byte, 16)
	for off := start; off+8 <= end && len(out) < 4096; {
		if _, err := r.Seek(off, io.SeekStart); err != nil {
			break
		}
		if _, err := io.ReadFull(r, head[:8]); err != nil {
			break
		}
		size := int64(binary.BigEndian.Uint32(head[:4]))
		name := string(head[4:8])
		hdr := int64(8)
		switch size {
		case 0:
			size = end - off
		case 1:
			if _, err := io.ReadFull(r, head[8:16]); err != nil {
				return out
			}
			size = int64(binary.BigEndian.Uint64(head[8:16]))
			hdr = 16
		}
		if size < hdr || size > end-off {
			break
		}
		out = append(out, box{name: name, start: off + hdr, end: off + size})
		off += size
	}
	return out
}

func findBox(r io.ReadSeeker, name string, start, end int64) (int64, int64, bool) {
	for _, b := range boxes(r, start, end) {
		if b.name == name {
			return b.start, b.end, true
		}
	}
	return 0, 0, false
}

// mvhdCreated is the movie header's creation time: seconds since 1904, UTC.
func mvhdCreated(r io.ReadSeeker, start, end int64) (time.Time, bool) {
	b := make([]byte, 12)
	if end-start < 12 {
		return time.Time{}, false
	}
	if _, err := r.Seek(start, io.SeekStart); err != nil {
		return time.Time{}, false
	}
	if _, err := io.ReadFull(r, b); err != nil {
		return time.Time{}, false
	}
	var secs int64
	if b[0] == 1 {
		secs = int64(binary.BigEndian.Uint64(b[4:12]))
	} else {
		secs = int64(binary.BigEndian.Uint32(b[4:8]))
	}
	t := time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(secs) * time.Second)
	if t.Year() < 1995 || t.After(time.Now().Add(48*time.Hour)) {
		return time.Time{}, false
	}
	return t, true
}

// quicktimeCreationDate reads com.apple.quicktime.creationdate from a
// QuickTime meta box's body: the keys box names it, and the item list holds
// its value under the key's 1-based index.
func quicktimeCreationDate(meta []byte) (time.Time, bool) {
	r := bytes.NewReader(meta)
	size := int64(len(meta))
	list := boxes(r, 0, size)
	// A meta box written as a full box carries four bytes of version and
	// flags before its children.
	if len(list) == 0 || (list[0].name != "hdlr" && list[0].name != "keys") {
		list = boxes(r, 4, size)
	}
	index := 0
	var ilst *box
	for i := range list {
		b := list[i]
		switch b.name {
		case "keys":
			body := meta[b.start:b.end]
			if len(body) < 8 {
				continue
			}
			count := int(binary.BigEndian.Uint32(body[4:8]))
			off := 8
			for k := 1; k <= count && off+8 <= len(body); k++ {
				n := int(binary.BigEndian.Uint32(body[off : off+4]))
				if n < 8 || off+n > len(body) {
					break
				}
				if string(body[off+8:off+n]) == "com.apple.quicktime.creationdate" {
					index = k
				}
				off += n
			}
		case "ilst":
			ilst = &list[i]
		}
	}
	if index == 0 || ilst == nil {
		return time.Time{}, false
	}
	for _, item := range boxes(r, ilst.start, ilst.end) {
		if int(binary.BigEndian.Uint32([]byte(item.name))) != index {
			continue
		}
		for _, d := range boxes(r, item.start, item.end) {
			if d.name != "data" || d.end-d.start < 8 {
				continue
			}
			value := strings.TrimSpace(string(meta[d.start+8 : d.end]))
			for _, layout := range []string{"2006-01-02T15:04:05-0700", time.RFC3339, "2006-01-02T15:04:05Z0700"} {
				if t, err := time.Parse(layout, value); err == nil {
					return t, true
				}
			}
		}
	}
	return time.Time{}, false
}
