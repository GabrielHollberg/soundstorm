package library

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/GabrielHollberg/soundstorm/internal/media"
)

// PersonalDir is the folder inside pictures/ that holds each person's own
// photos: everything a member adds to the picture shelf, and what anyone's
// phone backs up, goes to pictures/Personal/<their name>/. A member sees only
// their own folder; the owner sees the whole pictures folder, theirs included.
const PersonalDir = "Personal"

// PersonalFolder is a person's own photo folder inside pictures/, by their
// account name made safe as a folder name ("AC/DC" becomes "AC-DC").
func PersonalFolder(name string) string {
	return PersonalDir + "/" + tagSegment(name, "Someone")
}

// EnsurePersonalFolder makes a person's photo folder if it is not there yet,
// and returns it relative to pictures/.
func (l *Library) EnsurePersonalFolder(name string) (string, error) {
	rel := PersonalFolder(name)
	return rel, ensureDir(filepath.Join(l.PathFor(media.KindPicture), filepath.FromSlash(rel)))
}

// PersonalPath is where a file in a person's folder goes, relative to
// pictures/: rel inside their folder, cleaned like any other upload path.
func PersonalPath(name, rel string) (string, error) {
	clean, err := cleanRelPath(rel)
	if err != nil {
		return "", err
	}
	return path.Join(PersonalFolder(name), clean), nil
}

// PersonalUsage is how many bytes a person's photo folder holds.
func (l *Library) PersonalUsage(name string) int64 {
	dir := filepath.Join(l.PathFor(media.KindPicture), filepath.FromSlash(PersonalFolder(name)))
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

// PersonalHas reports whether a file of this size is already at rel in a
// person's folder: how a phone's backup knows what not to send again.
func (l *Library) PersonalHas(name, rel string, size int64) bool {
	p, err := PersonalPath(name, rel)
	if err != nil {
		return false
	}
	info, err := os.Stat(filepath.Join(l.PathFor(media.KindPicture), filepath.FromSlash(p)))
	return err == nil && info.Mode().IsRegular() && (size <= 0 || info.Size() == size)
}

// IsPersonalPath reports whether a pictures/-relative path is in someone's
// personal folder.
func IsPersonalPath(rel string) bool {
	return strings.HasPrefix(path.Clean("/"+rel), "/"+PersonalDir+"/")
}
