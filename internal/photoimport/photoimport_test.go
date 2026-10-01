package photoimport

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// memTarget is a person's folder in memory.
type memTarget struct {
	files    map[string][]byte
	sidecars map[string]string
	bySum    map[[32]byte]string
	improved map[string]Meta
	limit    int64
	// IsMediaPNG counts PNGs as photos, as the real folder does.
	IsMediaPNG bool
	used       int64
}

func newMem() *memTarget {
	return &memTarget{files: map[string][]byte{}, sidecars: map[string]string{}, bySum: map[[32]byte]string{}, improved: map[string]Meta{}, limit: -1}
}

func (m *memTarget) IsMedia(name string) bool {
	n := strings.ToLower(name)
	return strings.HasSuffix(n, ".jpg") || strings.HasSuffix(n, ".heic") || strings.HasSuffix(n, ".mov") || strings.HasSuffix(n, ".mp4") ||
		(m.IsMediaPNG && strings.HasSuffix(n, ".png"))
}
func (m *memTarget) Existing(size int64, sum [32]byte) (string, bool) {
	p, ok := m.bySum[sum]
	return p, ok
}

// Improve records what a duplicate offered, when it betters what was kept
// (the kept copy's source read from its sidecar, as the server does).
func (m *memTarget) Improve(existing string, inc Meta, src DateSource) bool {
	have, haveSrc := ReadSidecar([]byte(m.sidecars[existing]))
	merged, newSrc, _, changed := Better(have, haveSrc, have.HasPlace, inc, src)
	if changed {
		m.improved[existing] = merged
		m.sidecars[existing] = string(XMPSidecarFrom(merged, newSrc))
	}
	return changed
}
func (m *memTarget) Room(size int64) error {
	if m.limit >= 0 && m.used+size > m.limit {
		return io.ErrShortBuffer
	}
	return nil
}
func (m *memTarget) Save(rel string, r io.Reader, size int64, sum [32]byte) (string, error) {
	b, _ := io.ReadAll(r)
	m.files[rel] = b
	m.bySum[sum] = rel
	m.used += int64(len(b))
	return rel, nil
}
func (m *memTarget) Sidecar(saved string, data []byte) error {
	m.sidecars[saved] = string(data)
	return nil
}

func writeZip(t *testing.T, files map[string]string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "download.zip")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(body))
	}
	zw.Close()
	f.Close()
	return p
}

func run(t *testing.T, zipPath string, target Target) Progress {
	t.Helper()
	p, err := Run(zipPath, target, func(Progress) {}, func() bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// A Takeout download: each photo filed by the date its sidecar knew, Takeout's
// ways of naming a sidecar each found, an album's copy of a photo counted
// once, and the date and place written beside it for Immich.
func TestTakeout(t *testing.T) {
	long := "PXL_20190704_123456789_PORTRAIT_SUPER_LONG_NAME_X.jpg" // cut short in the zip
	short := long[:len(long)-len("_X.jpg")] + ".jpg"
	z := writeZip(t, map[string]string{
		"Takeout/Google Photos/Photos from 2019/IMG_1.jpg":              "one",
		"Takeout/Google Photos/Photos from 2019/IMG_1.jpg.json":         `{"title":"IMG_1.jpg","photoTakenTime":{"timestamp":"1562245200"},"geoData":{"latitude":51.5,"longitude":-0.12}}`,
		"Takeout/Google Photos/Photos from 2019/IMG_1-edited.jpg":       "one, edited",
		"Takeout/Google Photos/Photos from 2019/IMG_2(1).jpg":           "two again",
		"Takeout/Google Photos/Photos from 2019/IMG_2.jpg(1).json":      `{"title":"IMG_2.jpg","photoTakenTime":{"timestamp":"1577836800"}}`,
		"Takeout/Google Photos/Photos from 2019/" + short:               "long",
		"Takeout/Google Photos/Photos from 2019/" + long[:40] + ".json": `{"title":"` + long + `","photoTakenTime":{"timestamp":"1546300800"}}`,
		"Takeout/Google Photos/Holiday/IMG_1.jpg":                       "one", // the album's copy
		"Takeout/Google Photos/Holiday/metadata.json":                   `{"title":"Holiday"}`,
		"Takeout/archive_browser.html":                                  "<html>",
	})
	m := newMem()
	p := run(t, z, m)
	if p.Source != "google" || p.Total != 5 || p.Added != 4 || p.Duplicates != 1 {
		t.Errorf("progress = %+v", p)
	}
	for _, want := range []string{"2019/07/IMG_1.jpg", "2019/07/IMG_1-edited.jpg", "2020/01/IMG_2(1).jpg", "2019/01/" + short} {
		if _, ok := m.files[want]; !ok {
			t.Errorf("missing %s; have %v", want, keys(m.files))
		}
	}
	side := m.sidecars["2019/07/IMG_1.jpg"]
	if !strings.Contains(side, `exif:DateTimeOriginal="2019-07-04T13:00:00Z"`) || !strings.Contains(side, `exif:GPSLatitude="51,30.000000N"`) || !strings.Contains(side, `exif:GPSLongitude="0,7.200000W"`) {
		t.Errorf("sidecar = %s", side)
	}
}

// An iCloud download: the dates from Apple's details, and a Live Photo's
// still and its clip kept together.
func TestICloud(t *testing.T) {
	z := writeZip(t, map[string]string{
		"iCloud Photos/Photos/IMG_0001.HEIC":     "still",
		"iCloud Photos/Photos/IMG_0001.MOV":      "clip",
		"iCloud Photos/Photos/IMG_0002.JPG":      "other",
		"iCloud Photos/Photos/Photo Details.csv": "imgName,fileChecksum,favorite,hidden,deleted,originalCreationDate,viewCount,importDate\nIMG_0001.HEIC,x,no,no,no,Monday March 11,2024 8:34 PM GMT,1,\nIMG_0001.MOV,y,no,no,no,Monday March 11,2024 8:34 PM GMT,1,\nIMG_0002.JPG,z,no,no,no,Friday December 31,2021 11:59 PM GMT,0,\n",
	})
	m := newMem()
	p := run(t, z, m)
	if p.Source != "apple" || p.Added != 3 {
		t.Errorf("progress = %+v", p)
	}
	for _, want := range []string{"2024/03/IMG_0001.HEIC", "2024/03/IMG_0001.MOV", "2021/12/IMG_0002.JPG"} {
		if _, ok := m.files[want]; !ok {
			t.Errorf("missing %s; have %v", want, keys(m.files))
		}
	}
	// These carry no date of their own (no EXIF), so Apple's is written
	// beside each for Immich, or it would date them the day they were saved.
	if !strings.Contains(m.sidecars["2021/12/IMG_0002.JPG"], `exif:DateTimeOriginal="2021-12-31T23:59:00Z"`) {
		t.Errorf("sidecar = %q", m.sidecars["2021/12/IMG_0002.JPG"])
	}
}

// A full photo space stops the import, saying why.
func TestStopsAtTheLimit(t *testing.T) {
	z := writeZip(t, map[string]string{"a/1.jpg": "12345", "a/2.jpg": "67890"})
	m := newMem()
	m.limit = 7
	_, err := Run(z, m, func(Progress) {}, func() bool { return false })
	if _, ok := err.(ErrNoRoom); !ok || len(m.files) != 1 {
		t.Errorf("err = %v, files = %v", err, keys(m.files))
	}
}

// The date a photo was taken, from EXIF in its first bytes.
func TestExifTaken(t *testing.T) {
	// A TIFF with IFD0 pointing at an Exif IFD holding DateTimeOriginal.
	var b bytes.Buffer
	b.WriteString("Exif\x00\x00")
	tiff := make([]byte, 0, 128)
	le := binary.LittleEndian
	tiff = append(tiff, 'I', 'I', 42, 0)
	tiff = le.AppendUint32(tiff, 8)
	// IFD0: one entry, ExifIFD pointer -> 26
	tiff = le.AppendUint16(tiff, 1)
	tiff = le.AppendUint16(tiff, 0x8769)
	tiff = le.AppendUint16(tiff, 4)
	tiff = le.AppendUint32(tiff, 1)
	tiff = le.AppendUint32(tiff, 26)
	tiff = le.AppendUint32(tiff, 0)
	// Exif IFD at 26: DateTimeOriginal, 20 bytes at 44
	tiff = le.AppendUint16(tiff, 1)
	tiff = le.AppendUint16(tiff, 0x9003)
	tiff = le.AppendUint16(tiff, 2)
	tiff = le.AppendUint32(tiff, 20)
	tiff = le.AppendUint32(tiff, 44)
	tiff = le.AppendUint32(tiff, 0)
	tiff = append(tiff, []byte("2018:08:15 09:30:00\x00")...)
	b.Write(tiff)
	head := append([]byte("\xff\xd8\xff\xe1\x00\x00"), b.Bytes()...)
	got, ok := ExifTaken(head)
	if !ok || !got.Equal(time.Date(2018, 8, 15, 9, 30, 0, 0, time.UTC)) {
		t.Errorf("ExifTaken = %v, %v", got, ok)
	}
	if _, ok := ExifTaken([]byte("no exif here")); ok {
		t.Error("found a date in nothing")
	}
}

func keys(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// A date in a file name, as phones and cameras write them.
func TestNameTaken(t *testing.T) {
	cases := map[string]string{
		"IMG_20191225_090000.jpg":            "2019-12-25T09:00:00Z",
		"PXL_20210704_183015123.jpg":         "2021-07-04T18:30:15Z",
		"Screenshot_2019-12-25-09-00-00.png": "2019-12-25T09:00:00Z",
		"IMG-20191225-WA0001.jpg":            "2019-12-25T00:00:00Z",
		"20180101_120000.mp4":                "2018-01-01T12:00:00Z",
	}
	for name, want := range cases {
		got, ok := NameTaken(name)
		if !ok || got.Format(time.RFC3339) != want {
			t.Errorf("NameTaken(%q) = %v, %v; want %s", name, got, ok, want)
		}
	}
	for _, name := range []string{"IMG_0001.JPG", "photo.jpg", "IMG_20191345_000000.jpg", "DSC01234.JPG"} {
		if got, ok := NameTaken(name); ok {
			t.Errorf("NameTaken(%q) = %v, want no date", name, got)
		}
	}
}

// The case that asked for this: a photo from iCloud with no date anywhere,
// then the same photo in a Takeout download that knew its date and place.
// The second is not saved again; it gives the first its date and place.
func TestADuplicateImprovesTheCopyKept(t *testing.T) {
	m := newMem()
	run(t, writeZip(t, map[string]string{"iCloud Photos/Photos/IMG_5555.JPG": "same bytes"}), m)
	if _, ok := m.files["Undated/IMG_5555.JPG"]; !ok {
		t.Fatalf("first copy not in Undated: %v", keys(m.files))
	}
	p := run(t, writeZip(t, map[string]string{
		"Takeout/Google Photos/Photos from 2018/IMG_5555.JPG":      "same bytes",
		"Takeout/Google Photos/Photos from 2018/IMG_5555.JPG.json": `{"title":"IMG_5555.JPG","photoTakenTime":{"timestamp":"1530000000"},"geoData":{"latitude":40.7,"longitude":-74.0}}`,
	}), m)
	if p.Duplicates != 1 || p.Improved != 1 || p.Added != 0 {
		t.Errorf("progress = %+v", p)
	}
	got := m.improved["Undated/IMG_5555.JPG"]
	if got.Taken.Unix() != 1530000000 || !got.HasPlace {
		t.Errorf("improved = %+v", got)
	}
	// And a worse copy improves nothing: the same photo dated only by its
	// file, after Google's record.
	if _, _, _, changed := Better(got, SourceDownload, true, Meta{Taken: time.Now()}, SourceFile); changed {
		t.Error("a file date replaced Google's")
	}
	// The source survives a round trip through the sidecar.
	if m2, src := ReadSidecar(XMPSidecarFrom(got, SourceName)); src != SourceName || m2.Taken.Unix() != 1530000000 {
		t.Errorf("read back %v from %v", src, m2)
	}
}

// Facebook and Instagram strip what was inside their photos; their own
// records date them, matched by where the record says each photo is - even
// with the download unpacked inside a folder of its own - and chats, which
// hold everybody else's photos, are left out.
func TestFacebookAndInstagram(t *testing.T) {
	z := writeZip(t, map[string]string{
		"facebook-gabriel/your_facebook_activity/posts/media/Mobile_uploads/111_222_n.jpg": "fb one",
		"facebook-gabriel/your_facebook_activity/posts/media/Mobile_uploads/333_444_n.jpg": "fb two",
		"facebook-gabriel/your_facebook_activity/posts/your_posts__check_ins__photos_and_videos_1.json": `[{"attachments":[{"data":[
			{"media":{"uri":"your_facebook_activity/posts/media/Mobile_uploads/111_222_n.jpg","creation_timestamp":1600000000,
				"media_metadata":{"photo_metadata":{"exif_data":[{"taken_timestamp":1500000000,"latitude":48.85,"longitude":2.35}]}}}},
			{"media":{"uri":"your_facebook_activity/posts/media/Mobile_uploads/333_444_n.jpg","creation_timestamp":1400000000}}]}]}]`,
		"facebook-gabriel/your_facebook_activity/messages/inbox/friend_1/photos/555_n.jpg": "a friend's photo",
	})
	m := newMem()
	p := run(t, z, m)
	if p.Source != "facebook" || p.Added != 2 {
		t.Errorf("progress = %+v, files %v", p, keys(m.files))
	}
	// Taken in July 2017 (the record kept it), and the other only posted.
	if _, ok := m.files["2017/07/111_222_n.jpg"]; !ok {
		t.Errorf("missing the dated one: %v", keys(m.files))
	}
	if _, ok := m.files["2014/05/333_444_n.jpg"]; !ok {
		t.Errorf("missing the posted one: %v", keys(m.files))
	}
	if !strings.Contains(m.sidecars["2017/07/111_222_n.jpg"], "GPSLatitude") || !strings.Contains(m.sidecars["2014/05/333_444_n.jpg"], `DateSource="file"`) {
		t.Errorf("sidecars = %v", m.sidecars)
	}

	z = writeZip(t, map[string]string{
		"your_instagram_activity/media/posts_1.json": `[{"media":[{"uri":"media/posts/201906/17890_n.jpg","creation_timestamp":1560000000}]}]`,
		"media/posts/201906/17890_n.jpg":             "ig",
	})
	m = newMem()
	p = run(t, z, m)
	if p.Source != "instagram" {
		t.Errorf("source = %q", p.Source)
	}
	if _, ok := m.files["2019/06/17890_n.jpg"]; !ok {
		t.Errorf("instagram: %v", keys(m.files))
	}
}

// Flickr: a record per photo, matched by the number in the photo's name.
func TestFlickr(t *testing.T) {
	z := writeZip(t, map[string]string{
		"data-download-1/photo_52011112222.json":   `{"id":"52011112222","name":"Sunset","date_taken":"2015-06-01 19:30:00","geo":[{"latitude":"51500000","longitude":"-120000"}]}`,
		"data-download-1/sunset_52011112222_o.jpg": "flickr",
	})
	m := newMem()
	p := run(t, z, m)
	if p.Source != "flickr" {
		t.Errorf("source = %q", p.Source)
	}
	side := m.sidecars["2015/06/sunset_52011112222_o.jpg"]
	if !strings.Contains(side, `DateTimeOriginal="2015-06-01T19:30:00Z"`) || !strings.Contains(side, `GPSLatitude="51,30.000000N"`) {
		t.Errorf("files %v sidecar %q", keys(m.files), side)
	}
}

// Snapchat and Telegram: the date in the name; a memory's sticker layer and
// Snapchat's chat media left out.
func TestSnapchatAndTelegram(t *testing.T) {
	z := writeZip(t, map[string]string{
		"memories/2023-05-01_ABCDEF-main.jpg":                    "snap",
		"memories/2023-05-01_ABCDEF-overlay.png":                 "sticker",
		"chat_media/2023-05-02_x.jpg":                            "chat",
		"json/memories_history.json":                             `{"Saved Media":[]}`,
		"ChatExport_2020/photos/photo_1@25-12-2019_09-00-00.jpg": "telegram",
	})
	m := newMem()
	m.IsMediaPNG = true
	p := run(t, z, m)
	if p.Source != "snapchat" || p.Added != 2 {
		t.Errorf("progress = %+v files %v", p, keys(m.files))
	}
	for _, want := range []string{"2023/05/2023-05-01_ABCDEF-main.jpg", "2019/12/photo_1@25-12-2019_09-00-00.jpg"} {
		if _, ok := m.files[want]; !ok {
			t.Errorf("missing %s: %v", want, keys(m.files))
		}
	}
}
