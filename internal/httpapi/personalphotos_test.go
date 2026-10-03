package httpapi

import (
	"encoding/binary"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/library"
	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/state"
)

// Only the folders SoundStorm sorts photos into count as already kept: a
// photo in a folder somebody arranged themselves does not stop a copy being
// filed by date.
func TestOnlyDatedFoldersCountAsKept(t *testing.T) {
	personal := filepath.Join("lib", "pictures", "Personal", "bob")
	cases := map[string]bool{
		"2019/07/IMG_1.jpg":        true,
		"Undated/IMG_2.jpg":        true,
		"Vacation/IMG_1.jpg":       false,
		"2019/IMG_1.jpg":           false,
		"Family/2019/07/IMG_1.jpg": false,
		"IMG_3.jpg":                false,
	}
	for rel, want := range cases {
		if got := managedPhoto(personal, filepath.Join(personal, filepath.FromSlash(rel))); got != want {
			t.Errorf("%s: %v, want %v", rel, got, want)
		}
	}
}

// A different photo whose name is taken that month is kept beside it under
// a name for what makes it different - its camera - and a photo sent again
// is found under that name by its size, so a phone's backup does not file
// it twice.
func TestATakenPhotoNameSaysWhatDiffers(t *testing.T) {
	lib, err := library.Open(t.TempDir(), "./library", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{library: lib}
	rel, err := lib.EnsurePersonalFolder("bob")
	if err != nil {
		t.Fatal(err)
	}
	month := filepath.Join(lib.PathFor(media.KindPicture), filepath.FromSlash(rel), "2024", "05")
	if err := os.MkdirAll(month, 0o777); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(month, "IMG_0001.jpg"), exifJPEG("Google", "Pixel 8"), 0o666)
	staged := filepath.Join(t.TempDir(), "part-1")
	os.WriteFile(staged, exifJPEG("Apple", "iPhone 15 Pro"), 0o666)
	got := s.personalDistinct("bob", "2024/05/IMG_0001.jpg", staged)
	if got != "2024/05/IMG_0001 - iPhone 15 Pro.jpg" {
		t.Fatalf("named for its camera: %q", got)
	}
	os.WriteFile(filepath.Join(month, "IMG_0001 - iPhone 15 Pro.jpg"), exifJPEG("Apple", "iPhone 15 Pro"), 0o666)
	size := int64(len(exifJPEG("Apple", "iPhone 15 Pro")))
	if !s.personalSentBefore("bob", "2024/05/IMG_0001.jpg", size) {
		t.Fatal("the iPhone's photo, sent again, should be found")
	}
	if s.personalSentBefore("bob", "2024/05/IMG_0001.jpg", 12345) {
		t.Fatal("a photo of another size has not been sent")
	}
	if got := s.personalDistinct("bob", "2024/05/IMG_0002.jpg", staged); got != "2024/05/IMG_0002.jpg" {
		t.Fatalf("a free name stays: %q", got)
	}
}

// exifJPEG is the start of a JPEG whose EXIF names a camera.
func exifJPEG(maker, model string) []byte {
	le := binary.LittleEndian
	strs := []string{maker + "\x00", model + "\x00"}
	tiff := []byte("II*\x00\x08\x00\x00\x00")
	tiff = le.AppendUint16(tiff, 2)
	off := uint32(8 + 2 + 2*12 + 4)
	for i, tag := range []uint16{0x010F, 0x0110} {
		tiff = le.AppendUint16(tiff, tag)
		tiff = le.AppendUint16(tiff, 2)
		tiff = le.AppendUint32(tiff, uint32(len(strs[i])))
		tiff = le.AppendUint32(tiff, off)
		off += uint32(len(strs[i]))
	}
	tiff = le.AppendUint32(tiff, 0)
	for _, s := range strs {
		tiff = append(tiff, s...)
	}
	out := []byte{0xFF, 0xD8, 0xFF, 0xE1, 0, 0}
	out = append(out, "Exif\x00\x00"...)
	return append(out, tiff...)
}

// The file's own date dates a video (on a camera's card it is when the clip
// was filmed) but not a still: a picture with no date inside it or in its
// name was saved from a chat, an email or the web, and its file date is when
// it was saved - so it goes to Undated rather than the wrong month.
func TestAFileDateDatesOnlyVideos(t *testing.T) {
	h := newHarness(t)
	h.signUp(t)
	u := state.User{Name: "gabe"}
	dir := t.TempDir()
	saved := time.Date(2015, 8, 9, 10, 0, 0, 0, time.UTC).UnixMilli()

	still := filepath.Join(dir, "still")
	os.WriteFile(still, []byte("\x89PNG\r\n\x1a\n not a dated picture"), 0o644)
	pl, err := h.api.datedPhoto(u, still, "funny.png", saved)
	if err != nil || !strings.HasSuffix(pl.rel, "Undated/funny.png") {
		t.Fatalf("a still with only a file date went to %v (%v)", pl, err)
	}

	clip := filepath.Join(dir, "clip")
	os.WriteFile(clip, []byte("not a readable video"), 0o644)
	pl, err = h.api.datedPhoto(u, clip, "00012.MTS", saved)
	if err != nil || !strings.HasSuffix(pl.rel, "2015/08/00012.MTS") {
		t.Fatalf("a clip with only a file date went to %v (%v)", pl, err)
	}
}

// A picture backed up from a phone with no date inside it (a screenshot, one
// saved from a message) gets the date the phone gave written beside it, and
// on the file: Immich dated such pictures by the file, the day of the backup
// (the owner's report: years-old pictures showing as yesterday).
func TestABackedUpPictureKeepsThePhonesDate(t *testing.T) {
	h := newHarness(t)
	h.signUp(t)
	taken := time.Date(2019, 3, 14, 15, 9, 26, 0, time.UTC)
	png := "\x89PNG\r\n\x1a\n a picture with no date inside"
	req, _ := http.NewRequest(http.MethodPut, h.srv.URL+"/api/photos/backup?name=Screenshot.png&taken="+strconv.FormatInt(taken.UnixMilli(), 10), strings.NewReader(png))
	resp, err := h.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("backup: %d %s", resp.StatusCode, body)
	}
	full := filepath.Join(h.libraryRoot(t), "pictures", "Personal", "gabe", "2019", "03", "Screenshot.png")
	st, err := os.Stat(full)
	if err != nil {
		t.Fatalf("not filed by its date: %v (%s)", err, body)
	}
	if !st.ModTime().Equal(taken) {
		t.Errorf("file date %v, want %v", st.ModTime(), taken)
	}
	side, err := os.ReadFile(full + ".xmp")
	if err != nil || !strings.Contains(string(side), "2019-03-14T15:09:26") {
		t.Errorf("no date beside it: %v\n%s", err, side)
	}
}
