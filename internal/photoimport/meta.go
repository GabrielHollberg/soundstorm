// Package photoimport brings a person's photo library in from where it was:
// a Google Takeout download, an iCloud Photos download, or any zip of photos.
// Each photo and video goes into the person's own folder by the year and month
// it was taken (Personal/<name>/<year>/<month>/), the same as a phone's
// backup, with what the download knew about it - when, and where - kept, and
// a photo already there (from the phone, or another download) skipped.
//
// It reads only what answers those two questions: when and where. Like
// internal/tags it is not a metadata library and must not become one; Immich
// reads everything else from the files themselves.
package photoimport

import (
	"bytes"
	"encoding/binary"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Meta is what is known about one photo or video from outside it.
type Meta struct {
	Taken    time.Time
	Lat, Lon float64
	HasPlace bool
}

// --- Google Takeout -----------------------------------------------------------

// takeoutJSON is the part of a Takeout sidecar ("IMG_1234.JPG.json", or since
// 2024 "IMG_1234.JPG.supplemental-metadata.json", often cut short) that says
// when and where. Google keeps these beside the photos, and for many photos
// they are the only record of the date: the file itself may carry none, or
// the date it was uploaded.
type takeoutJSON struct {
	Title          string `json:"title"`
	PhotoTakenTime struct {
		Timestamp string `json:"timestamp"`
	} `json:"photoTakenTime"`
	CreationTime struct {
		Timestamp string `json:"timestamp"`
	} `json:"creationTime"`
	GeoData     takeoutGeo `json:"geoData"`
	GeoDataExif takeoutGeo `json:"geoDataExif"`
}

type takeoutGeo struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

// ParseTakeoutJSON reads one sidecar: the original file name it describes,
// and when and where. ok is false for a JSON file that is not a photo's
// sidecar (an album's metadata.json, say).
func ParseTakeoutJSON(r io.Reader) (title string, m Meta, ok bool) {
	var j takeoutJSON
	if err := json.NewDecoder(io.LimitReader(r, 1<<20)).Decode(&j); err != nil || j.Title == "" {
		return "", Meta{}, false
	}
	ts := j.PhotoTakenTime.Timestamp
	if ts == "" || ts == "0" {
		ts = j.CreationTime.Timestamp
	}
	if n, err := strconv.ParseInt(ts, 10, 64); err == nil && n > 0 {
		m.Taken = time.Unix(n, 0).UTC()
	}
	for _, g := range []takeoutGeo{j.GeoData, j.GeoDataExif} {
		if g.Latitude != 0 || g.Longitude != 0 {
			m.Lat, m.Lon, m.HasPlace = g.Latitude, g.Longitude, true
			break
		}
	}
	return j.Title, m, !m.Taken.IsZero() || m.HasPlace
}

// TakeoutIndex finds a photo's sidecar by its folder and name.
type TakeoutIndex struct {
	byKey   map[string]Meta     // folder/original name, lowercased
	byDir   map[string][]string // folder -> original names, for cut-short names
	byTitle map[string][]Meta   // original name alone, for an album's copy
}

func NewTakeoutIndex() *TakeoutIndex {
	return &TakeoutIndex{byKey: map[string]Meta{}, byDir: map[string][]string{}, byTitle: map[string][]Meta{}}
}

// Add records a sidecar found at entry (its path in the zip).
func (t *TakeoutIndex) Add(entry, title string, m Meta) {
	dir := strings.ToLower(path.Dir(entry))
	key := dir + "/" + strings.ToLower(title)
	if _, seen := t.byKey[key]; !seen {
		t.byDir[dir] = append(t.byDir[dir], strings.ToLower(title))
		t.byTitle[strings.ToLower(title)] = append(t.byTitle[strings.ToLower(title)], m)
	}
	t.byKey[key] = m
}

// Len is how many sidecars were found.
func (t *TakeoutIndex) Len() int { return len(t.byKey) }

var copyNumber = regexp.MustCompile(`\(\d+\)$`)

// editedSuffix is how Takeout names an edited copy, in the languages it is
// most often seen in; the copy's date and place are the original's.
var editedSuffix = []string{"-edited", "-bearbeitet", "-modifié", "-editado", "-modificato", "-bewerkt"}

// Lookup is the sidecar for the media file at entry. Takeout's names do not
// line up with its sidecars' in three ways, each handled: an edited copy
// ("IMG_1-edited.jpg" uses IMG_1.jpg's), a second photo of the same name
// ("IMG_1(1).jpg", whose sidecar is "IMG_1.jpg(1).json" but whose title is
// IMG_1.jpg), and names cut short at 47 characters, where the title in the
// sidecar is the whole original name.
func (t *TakeoutIndex) Lookup(entry string) (Meta, bool) {
	dir := strings.ToLower(path.Dir(entry))
	name := strings.ToLower(path.Base(entry))
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	candidates := []string{name}
	for _, suf := range editedSuffix {
		if s, ok := strings.CutSuffix(stem, suf); ok {
			candidates = append(candidates, s+ext)
		}
	}
	if s := copyNumber.ReplaceAllString(stem, ""); s != stem {
		candidates = append(candidates, s+ext)
	}
	for _, c := range candidates {
		if m, ok := t.byKey[dir+"/"+c]; ok {
			return m, true
		}
	}
	// Cut short: the only original name in the folder that begins with this
	// one's stem and has its extension.
	if len(stem) >= 20 {
		var match string
		for _, title := range t.byDir[dir] {
			if strings.HasPrefix(title, stem) && path.Ext(title) == ext {
				if match != "" {
					return Meta{}, false
				}
				match = title
			}
		}
		if match != "" {
			return t.byKey[dir+"/"+match], true
		}
	}
	// An album's copy of a photo whose sidecar is in another folder (the
	// year's): the same name anywhere in the download, as long as every
	// sidecar of that name agrees on when.
	for _, c := range candidates {
		if ms := t.byTitle[c]; len(ms) > 0 {
			agree := true
			for _, m := range ms[1:] {
				agree = agree && m.Taken.Equal(ms[0].Taken)
			}
			if agree {
				return ms[0], true
			}
		}
	}
	return Meta{}, false
}

// --- iCloud Photos --------------------------------------------------------------

// ICloudIndex is the photos' creation dates from the "Photo Details.csv"
// files in an iCloud download (Apple's "Request a copy of your data"),
// keyed by the photo's file name.
type ICloudIndex struct {
	taken map[string]time.Time
}

func NewICloudIndex() *ICloudIndex { return &ICloudIndex{taken: map[string]time.Time{}} }

// Len is how many photos the details named.
func (c *ICloudIndex) Len() int { return len(c.taken) }

// iCloudDate is how the details write a date: "Monday March 11,2024 8:34 PM
// GMT", in a few spellings over the years.
var iCloudDate = []string{
	"Monday January 2,2006 3:04 PM MST",
	"Monday January 2, 2006 3:04 PM MST",
	"Monday January 2,2006 15:04 MST",
	"January 2,2006 3:04 PM MST",
	time.RFC3339,
}

// AddCSV reads one details file.
func (c *ICloudIndex) AddCSV(r io.Reader) {
	cr := csv.NewReader(io.LimitReader(r, 64<<20))
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	head, err := cr.Read()
	if err != nil {
		return
	}
	nameCol, dateCol := -1, -1
	for i, h := range head {
		switch strings.TrimSpace(strings.TrimPrefix(h, "\ufeff")) {
		case "imgName":
			nameCol = i
		case "originalCreationDate":
			dateCol = i
		}
	}
	if nameCol < 0 || dateCol < 0 {
		return
	}
	for {
		row, err := cr.Read()
		if err == io.EOF {
			return
		}
		if err != nil || len(row) <= max(nameCol, dateCol) {
			continue
		}
		// Apple writes the date unquoted with a comma in it ("March 11,2024"),
		// so it can arrive as two columns: one more than the heading has.
		date := row[dateCol]
		if len(row) > len(head) && dateCol+1 < len(row) {
			date += "," + row[dateCol+1]
		}
		raw := strings.Join(strings.Fields(date), " ")
		for _, layout := range iCloudDate {
			if t, err := time.Parse(layout, raw); err == nil {
				c.taken[strings.ToLower(row[nameCol])] = t.UTC()
				break
			}
		}
	}
}

// Lookup is the creation date for a file name, if the details had it.
func (c *ICloudIndex) Lookup(name string) (time.Time, bool) {
	t, ok := c.taken[strings.ToLower(path.Base(name))]
	return t, ok
}

// --- the file itself ------------------------------------------------------------

// ExifTaken finds when a photo was taken from the EXIF in its first bytes:
// DateTimeOriginal, else DateTime. It looks for the "Exif\0\0" marker rather
// than walking the container, which finds it in a JPEG's APP1 and in a HEIC's
// Exif item alike, as long as it is in the bytes given.
func ExifTaken(head []byte) (time.Time, bool) {
	at := bytes.Index(head, []byte("Exif\x00\x00"))
	if at < 0 {
		return time.Time{}, false
	}
	tiff := head[at+6:]
	if len(tiff) < 8 {
		return time.Time{}, false
	}
	var bo binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return time.Time{}, false
	}
	ifd := func(off uint32) map[uint16][]byte {
		out := map[uint16][]byte{}
		if int(off)+2 > len(tiff) {
			return out
		}
		n := int(bo.Uint16(tiff[off:]))
		for i := 0; i < n && i < 512; i++ {
			e := int(off) + 2 + i*12
			if e+12 > len(tiff) {
				break
			}
			tag, typ, count := bo.Uint16(tiff[e:]), bo.Uint16(tiff[e+2:]), bo.Uint32(tiff[e+4:])
			switch {
			case typ == 2 && count > 4 && count < 64: // a string stored elsewhere
				p := bo.Uint32(tiff[e+8:])
				if int(p)+int(count) <= len(tiff) {
					out[tag] = tiff[p : p+count]
				}
			case typ == 4 || typ == 13: // a pointer to another IFD
				out[tag] = tiff[e+8 : e+12]
			}
		}
		return out
	}
	parse := func(b []byte) (time.Time, bool) {
		s := strings.TrimRight(string(b), "\x00 ")
		t, err := time.Parse("2006:01:02 15:04:05", s)
		if err != nil || t.Year() < 1900 {
			return time.Time{}, false
		}
		return t, true
	}
	zero := ifd(bo.Uint32(tiff[4:]))
	if p, ok := zero[0x8769]; ok {
		if v, ok := ifd(bo.Uint32(p))[0x9003]; ok {
			if t, ok := parse(v); ok {
				return t, true
			}
		}
	}
	if v, ok := zero[0x0132]; ok {
		return parse(v)
	}
	return time.Time{}, false
}

// nameDate is a date (and maybe a time) in a file name: IMG_20191225_090000,
// PXL_20191225_090000123 (milliseconds after the time), Screenshot_2019-12-25-09-00-00, IMG-20191225-WA0001.
var nameDate = regexp.MustCompile(`(?:^|[^0-9])((?:19|20)\d{2})[-_]?(0[1-9]|1[0-2])[-_]?(0[1-9]|[12]\d|3[01])(?:[-_ T]?([01]\d|2[0-3])[-_.:]?([0-5]\d)[-_.:]?([0-5]\d)\d{0,3})?(?:[^0-9]|$)`)

// NameTaken reads when a photo was taken from its file name, as phones and
// cameras name them.
func NameTaken(name string) (time.Time, bool) {
	m := nameDate.FindStringSubmatch(name)
	if m == nil {
		return time.Time{}, false
	}
	n := func(s string) int { v, _ := strconv.Atoi(s); return v }
	t := time.Date(n(m[1]), time.Month(n(m[2])), n(m[3]), n(m[4]), n(m[5]), n(m[6]), 0, time.UTC)
	if t.Day() != n(m[3]) || t.After(time.Now().Add(48*time.Hour)) {
		return time.Time{}, false
	}
	return t, true
}

// --- what Immich reads beside a photo -------------------------------------------

// XMPSidecar is a sidecar for Immich to read the date and place from
// ("IMG_1234.JPG.xmp" beside it): the photo itself is never changed, and
// Immich prefers what a sidecar says. Written for a Takeout photo whose
// download knew its date or place, which the file may not.
func XMPSidecar(m Meta) []byte {
	var attrs []string
	if !m.Taken.IsZero() {
		attrs = append(attrs, fmt.Sprintf(`exif:DateTimeOriginal="%s" photoshop:DateCreated="%s"`,
			m.Taken.Format("2006-01-02T15:04:05Z"), m.Taken.Format("2006-01-02T15:04:05Z")))
	}
	if m.HasPlace {
		attrs = append(attrs, fmt.Sprintf(`exif:GPSLatitude="%s" exif:GPSLongitude="%s"`,
			xmpCoord(m.Lat, "N", "S"), xmpCoord(m.Lon, "E", "W")))
	}
	return []byte(`<?xpacket begin="` + "\ufeff" + `" id="W5M0MpCehiHzreSzNTczkc9d"?>
<x:xmpmeta xmlns:x="adobe:ns:meta/">
 <rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
  <rdf:Description rdf:about="" xmlns:exif="http://ns.adobe.com/exif/1.0/" xmlns:photoshop="http://ns.adobe.com/photoshop/1.0/"
   ` + strings.Join(attrs, "\n   ") + `/>
 </rdf:RDF>
</x:xmpmeta>
<?xpacket end="w"?>
`)
}

// xmpCoord writes a coordinate as XMP has it: degrees, minutes and a letter,
// "51,30.12345N".
func xmpCoord(v float64, pos, neg string) string {
	letter := pos
	if v < 0 {
		letter, v = neg, -v
	}
	deg := math.Floor(v)
	return fmt.Sprintf("%d,%.6f%s", int(deg), (v-deg)*60, letter)
}
