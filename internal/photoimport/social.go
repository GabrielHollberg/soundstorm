package photoimport

import (
	"encoding/json"
	"io"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// JSONIndex is what a social network's download says about its photos. The
// photos themselves usually carry nothing - Facebook and Instagram strip
// EXIF - so these files are the only record of when and where.
//
// Rather than one parser per network, it walks any JSON for the two shapes
// they use: Facebook's and Instagram's media objects ("uri", the photo's path
// in the download, beside "creation_timestamp", and, when the photo kept it,
// "taken_timestamp" and a latitude and longitude in its EXIF summary), and
// Flickr's per-photo files ("id" beside "date_taken", and "geo").
type JSONIndex struct {
	byName map[string][]uriMeta // a photo's file name -> where its uri says it is
	byID   map[string]Meta      // a Flickr photo's number
	kept   int                  // records kept, against maxRecords
	read   int64                // JSON bytes decoded, against maxJSONBytes
}

// A download's records are kept in memory while it is sorted, so they are
// bounded: a small zip of repeated records once unpacked to gigabytes (a
// security review). A real account's records are far below these.
const (
	maxRecords   = 2_000_000
	maxJSONBytes = 256 << 20
	maxPerName   = 64 // records of one file name; a real download has a few
)

type uriMeta struct {
	uri string
	m   Meta
	src DateSource
}

func NewJSONIndex() *JSONIndex {
	return &JSONIndex{byName: map[string][]uriMeta{}, byID: map[string]Meta{}}
}

// Len is how many photos the files described.
func (x *JSONIndex) Len() int { return len(x.byName) + len(x.byID) }

// Add reads one JSON file.
func (x *JSONIndex) Add(r io.Reader) {
	if x.read >= maxJSONBytes || x.kept >= maxRecords {
		return
	}
	cr := &countingReader{r: io.LimitReader(r, maxJSONBytes-x.read)}
	var v any
	err := json.NewDecoder(cr).Decode(&v)
	x.read += cr.n
	if err != nil {
		return
	}
	x.walk(v, 0)
}

func (x *JSONIndex) walk(v any, depth int) {
	if depth > 40 {
		return
	}
	switch t := v.(type) {
	case []any:
		for _, e := range t {
			x.walk(e, depth+1)
		}
	case map[string]any:
		if uri, ok := t["uri"].(string); ok && uri != "" {
			if created, ok := t["creation_timestamp"].(float64); ok && created > 0 {
				m, src := Meta{Taken: time.Unix(int64(created), 0).UTC()}, SourceFile // when it was posted
				var taken, lat, lon float64
				findExif(t, &taken, &lat, &lon, 0)
				if taken > 0 {
					m.Taken, src = time.Unix(int64(taken), 0).UTC(), SourceDownload
				}
				if lat != 0 || lon != 0 {
					m.Lat, m.Lon, m.HasPlace = lat, lon, true
				}
				name := strings.ToLower(path.Base(uri))
				if x.kept < maxRecords && len(x.byName[name]) < maxPerName {
					x.byName[name] = append(x.byName[name], uriMeta{uri: strings.ToLower(strings.TrimPrefix(uri, "/")), m: m, src: src})
					x.kept++
				}
			}
		}
		if id, ok := t["id"].(string); ok && id != "" {
			if taken, ok := t["date_taken"].(string); ok {
				if when, err := time.Parse("2006-01-02 15:04:05", taken); err == nil {
					m := Meta{Taken: when}
					if geo, ok := t["geo"].([]any); ok && len(geo) > 0 {
						if g, ok := geo[0].(map[string]any); ok {
							lat, _ := strconv.ParseFloat(str(g["latitude"]), 64)
							lon, _ := strconv.ParseFloat(str(g["longitude"]), 64)
							// Flickr writes them as millionths of a degree.
							if lat > 180 || lat < -180 {
								lat, lon = lat/1e6, lon/1e6
							}
							if lat != 0 || lon != 0 {
								m.Lat, m.Lon, m.HasPlace = lat, lon, true
							}
						}
					}
					if x.kept < maxRecords {
						if _, had := x.byID[id]; !had {
							x.kept++
						}
						x.byID[id] = m
					}
				}
			}
		}
		for _, e := range t {
			x.walk(e, depth+1)
		}
	}
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func str(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	}
	return ""
}

// findExif looks inside a media object for the EXIF summary Facebook keeps:
// "taken_timestamp", "latitude", "longitude".
func findExif(v any, taken, lat, lon *float64, depth int) {
	if depth > 8 {
		return
	}
	switch t := v.(type) {
	case []any:
		for _, e := range t {
			findExif(e, taken, lat, lon, depth+1)
		}
	case map[string]any:
		if f, ok := t["taken_timestamp"].(float64); ok && f > 0 && *taken == 0 {
			*taken = f
		}
		if f, ok := t["latitude"].(float64); ok && *lat == 0 {
			*lat = f
		}
		if f, ok := t["longitude"].(float64); ok && *lon == 0 {
			*lon = f
		}
		for _, e := range t {
			findExif(e, taken, lat, lon, depth+1)
		}
	}
}

// flickrID is the photo's number in a Flickr download's file name:
// "sunset_12345678901_o.jpg".
var flickrID = regexp.MustCompile(`_(\d{6,})(_o)?\.[A-Za-z0-9]+$`)

// Lookup is what the files said about the photo at entry: matched by its
// path, the uri's tail (a download is often unpacked inside a folder of its
// own), or for Flickr by the number in its name.
func (x *JSONIndex) Lookup(entry string) (Meta, DateSource, bool) {
	lower := strings.ToLower(entry)
	for _, c := range x.byName[path.Base(lower)] {
		if strings.HasSuffix(lower, c.uri) || strings.HasSuffix(c.uri, path.Base(lower)) && len(x.byName[path.Base(lower)]) == 1 {
			return c.m, c.src, true
		}
	}
	if m := flickrID.FindStringSubmatch(path.Base(entry)); m != nil {
		if meta, ok := x.byID[m[1]]; ok {
			return meta, SourceDownload, true
		}
	}
	return Meta{}, SourceNone, false
}

// SourceOf names which service a download came from, by its folders.
func SourceOf(names []string) string {
	for _, n := range names {
		l := strings.ToLower(n)
		switch {
		case strings.Contains(l, "google photos/"):
			return "google"
		case strings.Contains(l, "icloud photos/") || strings.Contains(l, "photo details"):
			return "apple"
		case strings.Contains(l, "your_facebook_activity/") || strings.HasPrefix(path.Base(l), "your_posts"):
			return "facebook"
		case strings.Contains(l, "your_instagram_activity/") || strings.HasPrefix(l, "media/posts/"):
			return "instagram"
		case strings.Contains(l, "memories_history.json") || strings.Contains(l, "memories/"):
			return "snapchat"
		case regexp.MustCompile(`(^|/)photo_\d{6,}\.json$`).MatchString(l):
			return "flickr"
		}
	}
	return ""
}

// socialSkipped is what a social download holds that is not the person's
// own photos: chats, which hold everybody else's, and Snapchat's sticker
// layers drawn over a memory.
func socialSkipped(name string) bool {
	l := strings.ToLower(name)
	return strings.Contains(l, "/messages/") || strings.HasPrefix(l, "messages/") ||
		strings.Contains(l, "/inbox/") || strings.Contains(l, "chat_media/") ||
		strings.Contains(path.Base(l), "-overlay.")
}
