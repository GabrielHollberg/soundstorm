package photoimport

import (
	"encoding/binary"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DateSource is where a photo's date came from, from least to most trusted.
// A copy of a photo that arrives later with a better-sourced date (or a place
// where there was none) improves the copy already kept: the same photo is
// often in iCloud with no date and in Google Takeout with one.
type DateSource int

const (
	SourceNone     DateSource = iota // nothing knew; filed under Undated/
	SourceFile                       // the file's own date (a camera card's, a download's)
	SourceName                       // a date in the file name
	SourceDownload                   // Google's or Apple's own record of it
	SourceExif                       // the date inside the photo
)

var sourceNames = map[DateSource]string{SourceFile: "file", SourceName: "name", SourceDownload: "download", SourceExif: "exif"}

// Better is what a duplicate adds to the copy kept: a date from a better
// source, and a place where the kept copy had none. have and haveSrc are the
// kept copy's (haveGPS whether it has a place at all, inside it or beside
// it); inc and incSrc the copy that arrived. changed says whether anything is
// worth writing, dateBetter whether the date itself improved.
func Better(have Meta, haveSrc DateSource, haveGPS bool, inc Meta, incSrc DateSource) (merged Meta, src DateSource, dateBetter, changed bool) {
	merged, src = have, haveSrc
	if incSrc > haveSrc && !inc.Taken.IsZero() {
		merged.Taken, src = inc.Taken, incSrc
		dateBetter, changed = true, true
	}
	if !haveGPS && inc.HasPlace {
		merged.Lat, merged.Lon, merged.HasPlace = inc.Lat, inc.Lon, true
		changed = true
	}
	return merged, src, dateBetter, changed
}

// XMPSidecarFrom is XMPSidecar recording where the date came from, so a later
// copy can tell whether it knows better.
func XMPSidecarFrom(m Meta, src DateSource) []byte {
	b := XMPSidecar(m)
	name := sourceNames[src]
	if name == "" || m.Taken.IsZero() {
		return b
	}
	s := strings.Replace(string(b), `xmlns:photoshop="http://ns.adobe.com/photoshop/1.0/"`,
		`xmlns:photoshop="http://ns.adobe.com/photoshop/1.0/" xmlns:soundstorm="https://soundstorm.dev/ns/photo/1.0/" soundstorm:DateSource="`+name+`"`, 1)
	return []byte(s)
}

var (
	sidecarDate   = regexp.MustCompile(`exif:DateTimeOriginal="([^"]+)"`)
	sidecarLat    = regexp.MustCompile(`exif:GPSLatitude="(\d+),([\d.]+)([NS])"`)
	sidecarLon    = regexp.MustCompile(`exif:GPSLongitude="(\d+),([\d.]+)([EW])"`)
	sidecarSource = regexp.MustCompile(`soundstorm:DateSource="([a-z]+)"`)
)

// ReadSidecar reads back a sidecar SoundStorm wrote: the date and where it
// came from, and the place. One written before sources were recorded is
// taken as a download's, which is what they all were.
func ReadSidecar(data []byte) (m Meta, src DateSource) {
	s := string(data)
	if v := sidecarDate.FindStringSubmatch(s); v != nil {
		if t, err := time.Parse("2006-01-02T15:04:05Z", v[1]); err == nil {
			m.Taken, src = t, SourceDownload
		}
	}
	if v := sidecarSource.FindStringSubmatch(s); v != nil && !m.Taken.IsZero() {
		for k, name := range sourceNames {
			if name == v[1] {
				src = k
			}
		}
	}
	coord := func(v []string, neg string) float64 {
		d, _ := strconv.ParseFloat(v[1], 64)
		min, _ := strconv.ParseFloat(v[2], 64)
		x := d + min/60
		if v[3] == neg {
			x = -x
		}
		return x
	}
	lat, lon := sidecarLat.FindStringSubmatch(s), sidecarLon.FindStringSubmatch(s)
	if lat != nil && lon != nil {
		m.Lat, m.Lon, m.HasPlace = coord(lat, "S"), coord(lon, "W"), true
	}
	return m, src
}

// ExifHasPlace reports whether the EXIF in a photo's first bytes has a GPS
// record.
func ExifHasPlace(head []byte) bool {
	at := strings.Index(string(head), "Exif\x00\x00")
	if at < 0 || len(head) < at+14 {
		return false
	}
	tiff := head[at+6:]
	var bo binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return false
	}
	off := bo.Uint32(tiff[4:])
	if int(off)+2 > len(tiff) {
		return false
	}
	n := int(bo.Uint16(tiff[off:]))
	for i := 0; i < n && i < 512; i++ {
		e := int(off) + 2 + i*12
		if e+12 > len(tiff) {
			return false
		}
		if bo.Uint16(tiff[e:]) == 0x8825 {
			return true
		}
	}
	return false
}
