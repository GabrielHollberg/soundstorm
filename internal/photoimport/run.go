package photoimport

import (
	"archive/zip"
	"crypto/sha256"
	"fmt"
	"io"
	"path"
	"strings"
	"time"
)

// Target is where an import puts photos: the person's own folder, with its
// limit and what is already in it.
type Target interface {
	// IsMedia reports whether a file name is a photo or video the folder keeps.
	IsMedia(name string) bool
	// Existing is an identical file already in the folder, if there is one.
	Existing(size int64, sum [32]byte) (string, bool)
	// Improve gives a kept copy what a duplicate knew that it did not: a
	// better-sourced date, a place. It reports whether anything changed.
	Improve(existing string, m Meta, src DateSource) bool
	// Room refuses a file that would take the person past their photo space.
	Room(size int64) error
	// Save puts a file at rel inside the folder and returns where it went.
	Save(rel string, r io.Reader, size int64, sum [32]byte) (string, error)
	// Sidecar writes data beside a saved file, as "<file>.xmp".
	Sidecar(saved string, data []byte) error
}

// Progress is how an import is going.
type Progress struct {
	Total      int    `json:"total"`      // photos and videos in the download
	Done       int    `json:"done"`       // looked at so far
	Added      int    `json:"added"`      // new, saved
	Duplicates int    `json:"duplicates"` // already in the folder, or twice in the download
	Improved   int    `json:"improved"`   // duplicates that gave the copy kept a better date or a place
	Failed     int    `json:"failed"`
	Source     string `json:"source"` // "google", "apple" or "" for a plain zip
	Problem    string `json:"problem,omitempty"`
}

// ErrNoRoom stops an import when the person's photo space is full.
type ErrNoRoom struct{ Err error }

func (e ErrNoRoom) Error() string { return e.Err.Error() }

// Run imports every photo and video in a zip into t, calling progress as it
// goes and stopping early if stop says so. Albums and their duplicates
// (Takeout copies a photo into every album it is in) become one file each.
func Run(zipPath string, t Target, progress func(Progress), stop func() bool) (Progress, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return Progress{}, fmt.Errorf("not a zip that can be opened: %w", err)
	}
	defer zr.Close()

	// First what the download says about its photos: Takeout's sidecars and
	// iCloud's details, small files read before any photo.
	takeout, icloud, social := NewTakeoutIndex(), NewICloudIndex(), NewJSONIndex()
	var media []*zip.File
	var names []string
	for _, f := range zr.File {
		name := f.Name
		names = append(names, name)
		if f.FileInfo().IsDir() || skipped(name) {
			continue
		}
		lower := strings.ToLower(name)
		switch {
		case strings.HasSuffix(lower, ".json") && f.UncompressedSize64 < 64<<20:
			// A Takeout sidecar (small, one per photo), or a social
			// network's record of its photos (Facebook's, Instagram's,
			// Flickr's), read either way.
			if f.UncompressedSize64 < 1<<20 {
				if rc, err := f.Open(); err == nil {
					if title, m, ok := ParseTakeoutJSON(rc); ok {
						takeout.Add(name, title, m)
						rc.Close()
						continue
					}
					rc.Close()
				}
			}
			if rc, err := f.Open(); err == nil {
				social.Add(rc)
				rc.Close()
			}
		case strings.HasSuffix(lower, ".csv") && strings.Contains(strings.ToLower(path.Base(name)), "photo details"):
			if rc, err := f.Open(); err == nil {
				icloud.AddCSV(rc)
				rc.Close()
			}
		case t.IsMedia(name):
			media = append(media, f)
		}
	}
	p := Progress{Total: len(media), Source: SourceOf(names)}
	switch {
	case p.Source != "":
	case takeout.Len() > 0:
		p.Source = "google"
	case icloud.Len() > 0:
		p.Source = "apple"
	}
	progress(p)

	seen := map[[32]byte]string{} // what this download saved, by content
	for _, f := range media {
		if stop() {
			return p, nil
		}
		p.Done++
		err := one(f, t, takeout, icloud, social, seen, &p)
		if err != nil {
			if room, ok := err.(ErrNoRoom); ok {
				p.Problem = room.Error()
				progress(p)
				return p, room
			}
			p.Failed++
		}
		if p.Done%20 == 0 || p.Done == p.Total {
			progress(p)
		}
	}
	progress(p)
	return p, nil
}

// skipped is what a download holds that is not a photo of the person's:
// Google's bin, macOS's resource forks.
func skipped(name string) bool {
	lower := strings.ToLower(name)
	return strings.Contains(lower, "__macosx/") || strings.HasPrefix(path.Base(name), "._") ||
		strings.Contains(lower, "/trash/") || strings.Contains(lower, "/bin/") || socialSkipped(name)
}

func one(f *zip.File, t Target, takeout *TakeoutIndex, icloud *ICloudIndex, social *JSONIndex, seen map[[32]byte]string, p *Progress) error {
	size := int64(f.UncompressedSize64)
	// The first pass: what it is (its hash), and when it was taken.
	rc, err := f.Open()
	if err != nil {
		return err
	}
	h := sha256.New()
	head := make([]byte, 0, 4<<20)
	buf := make([]byte, 256<<10)
	for {
		n, rerr := rc.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
			if len(head) < cap(head) {
				head = append(head, buf[:min(n, cap(head)-len(head))]...)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			rc.Close()
			return rerr
		}
	}
	rc.Close()
	var sum [32]byte
	copy(sum[:], h.Sum(nil))

	// When, and how sure: what the download said, else the photo's own
	// EXIF, else a date in its name (IMG_20191225_090000.jpg - phones name
	// files so almost everywhere), else the date the zip gave it if that is
	// not simply when the download was made. With none of those it goes to
	// Undated/ rather than under today, where it would hide among this
	// month's photos.
	meta, fromTakeout := takeout.Lookup(f.Name)
	var socialSrc DateSource
	if !fromTakeout {
		// Facebook, Instagram or Flickr's record, the only one their
		// photos have: they come stripped of what was inside them.
		if m, s, ok := social.Lookup(f.Name); ok {
			meta, socialSrc, fromTakeout = m, s, true
		}
	}
	src := SourceNone
	exifTaken, exifOK := ExifTaken(head)
	switch {
	case exifOK:
		src = SourceExif
		if meta.Taken.IsZero() {
			meta.Taken = exifTaken
		}
	case !meta.Taken.IsZero():
		src = SourceDownload
		if socialSrc != SourceNone {
			src = socialSrc // when it was posted, if that is all it knew
		}
	default:
		if when, ok := icloud.Lookup(f.Name); ok {
			meta.Taken, src = when, SourceDownload
		} else if when, ok := NameTaken(path.Base(f.Name)); ok {
			meta.Taken, src = when, SourceName
		} else if f.Modified.Year() > 1990 && time.Since(f.Modified) > 30*24*time.Hour {
			meta.Taken, src = f.Modified.UTC(), SourceFile
		}
	}

	// Already kept - in the folder, or earlier in this download: not saved
	// again, but what this copy knew improves the one kept.
	existing, dup := seen[sum]
	if !dup {
		existing, dup = t.Existing(size, sum)
	}
	if dup {
		p.Duplicates++
		if existing != "" && t.Improve(existing, meta, src) {
			p.Improved++
		}
		return nil
	}
	if err := t.Room(size); err != nil {
		return ErrNoRoom{err}
	}
	rel := "Undated/" + path.Base(f.Name)
	if !meta.Taken.IsZero() {
		rel = fmt.Sprintf("%04d/%02d/%s", meta.Taken.Year(), int(meta.Taken.Month()), path.Base(f.Name))
	}

	// The second pass: saved.
	rc, err = f.Open()
	if err != nil {
		return err
	}
	saved, err := t.Save(rel, rc, size, sum)
	rc.Close()
	if err != nil {
		return err
	}
	seen[sum] = saved
	p.Added++
	// The date and place written beside it for Immich when they came from the
	// download, or when the photo carries no date of its own: the saved file
	// is new, and Immich would otherwise date it the day it was saved.
	if fromTakeout || (src != SourceExif && !meta.Taken.IsZero()) {
		_ = t.Sidecar(saved, XMPSidecarFrom(meta, src))
	}
	return nil
}
