package library

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gabehollberg/soundstorm/internal/media"
)

// Accepting dropped files.
//
// The folders are the interface, so dragging something onto SoundStorm should
// do what dragging it onto the folder would have done - including working out
// which folder that was. Two things make this more than a file copy.
//
// Sidecars have to travel with their media. A film folder holds an .mkv, a
// .srt and a poster; the subtitle is useless in the ebook shelf and invisible
// anywhere but beside its film. So a placement is decided for a whole dropped
// item - a folder and everything under it - rather than file by file, and
// files that cannot name a shelf themselves inherit one.
//
// The path a browser gives us is attacker-controlled. Every segment is
// sanitised and the result is checked to still be inside the folder it claims
// to be in, which is a thing worth doing twice.

const (
	// maxFiles bounds one drop. Past this the browser is the bottleneck anyway,
	// and it stops a dropped root directory from becoming a very large request.
	maxFiles = 5000

	// maxDepth and maxSegment keep a path recognisable as a path. Nothing
	// legitimate nests an album eight levels deep.
	maxDepth   = 8
	maxSegment = 120
)

// companionExtensions ride along with whatever they were dropped beside.
//
// None of them names a shelf on its own: a loose .srt belongs nowhere, but a
// .srt next to a film belongs exactly where the film goes.
var companionExtensions = map[string]bool{
	".srt": true, ".vtt": true, ".ass": true, ".ssa": true, ".sub": true,
	".idx": true, ".sup": true,
	".nfo": true, ".opf": true, ".cue": true, ".m3u": true, ".m3u8": true,
	".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".gif": true,
	".txt": true, ".json": true,
}

// unambiguousAudiobook are audio containers nobody uses for music.
var unambiguousAudiobook = map[string]bool{
	".m4b": true, ".aax": true, ".aaxc": true,
}

var (
	// episodePattern is how television is named, in the two forms people
	// actually use.
	episodePattern = regexp.MustCompile(`(?i)(\bs\d{1,2}[\s._-]*e\d{1,3}\b|\b\d{1,2}x\d{2}\b)`)
	seasonFolder   = regexp.MustCompile(`(?i)^(season|series)[\s._-]*\d+$`)

	// controlChars are stripped from names rather than rejected: a stray
	// character is not worth refusing somebody's film over.
	controlChars = regexp.MustCompile(`[\x00-\x1f\x7f]`)

	// reservedNames cannot be file names on Windows, and the library folder is
	// very often a Windows directory bind-mounted into Linux.
	reservedNames = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[1-9]|lpt[1-9])(\.|$)`)
)

// Placement is where one dropped file will go, or why it will not go anywhere.
type Placement struct {
	// Path is what the client called it, echoed back so a client can match a
	// decision to the file it holds.
	Path string `json:"path"`

	// Kind is the shelf. Empty when nothing will be done with this file.
	Kind media.Kind `json:"kind,omitempty"`

	// Dest is the path relative to the library root, which is also what the
	// UI shows: "movies/Arrival (2016)/Arrival (2016).mkv".
	Dest string `json:"dest,omitempty"`

	// Skipped and Reason explain a file SoundStorm will not take.
	Skipped bool   `json:"skipped,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// Plan decides where a set of dropped paths should go.
//
// forced names a shelf chosen by the person dropping - they dragged onto
// "Audiobooks" rather than onto the window - in which case nothing is guessed
// and only unusable files are skipped. An empty forced means work it out.
func (l *Library) Plan(paths []string, forced media.Kind) []Placement {
	if len(paths) > maxFiles {
		paths = paths[:maxFiles]
	}

	cleaned := make([]string, len(paths))
	groups := map[string][]int{}
	var order []string

	for i, raw := range paths {
		rel, err := cleanRelPath(raw)
		if err != nil {
			continue
		}
		cleaned[i] = rel
		// The group is the top-level thing that was dropped: a folder and
		// everything under it, or a loose file on its own. Deciding one shelf
		// per group is what keeps a subtitle with its film.
		key := rel
		if idx := strings.Index(rel, "/"); idx >= 0 {
			key = rel[:idx]
		}
		if _, seen := groups[key]; !seen {
			order = append(order, key)
		}
		groups[key] = append(groups[key], i)
	}

	out := make([]Placement, len(paths))
	for i := range out {
		out[i] = Placement{
			Path:    paths[i],
			Skipped: true,
			Reason:  "that does not look like a file name",
		}
	}

	for _, key := range order {
		members := groups[key]

		kind := forced
		if kind == "" {
			kind = kindForGroup(cleaned, members)
		}

		for _, i := range members {
			rel := cleaned[i]
			ext := strings.ToLower(path.Ext(rel))

			switch {
			case kind == "":
				out[i] = Placement{
					Path:    paths[i],
					Skipped: true,
					Reason:  "SoundStorm does not know what " + ext + " files are",
				}
			case !knownExtension(ext) && !companionExtensions[ext]:
				// Inside a folder that is going somewhere, an unrecognised file
				// is still skipped: a 4GB .iso next to an album is not part of
				// the album.
				out[i] = Placement{
					Path:    paths[i],
					Skipped: true,
					Reason:  "SoundStorm does not know what " + ext + " files are",
				}
			default:
				out[i] = Placement{
					Path: paths[i],
					Kind: kind,
					Dest: folderName(kind) + "/" + rel,
				}
			}
		}
	}
	return out
}

// kindForGroup picks one shelf for everything dropped together.
//
// The first file that can name a shelf wins, and files that cannot - subtitles,
// artwork - do not get a vote. A folder of nothing but companions names no
// shelf and is skipped entirely, which is right: a lone subtitle has no home.
func kindForGroup(cleaned []string, members []int) media.Kind {
	for _, i := range members {
		if k, ok := kindForPath(cleaned[i]); ok {
			return k
		}
	}
	return ""
}

// kindForPath decides a shelf from one path.
func kindForPath(rel string) (media.Kind, bool) {
	ext := strings.ToLower(path.Ext(rel))
	if ext == "" || companionExtensions[ext] {
		return "", false
	}

	if mediaExtensions[media.KindEbook][ext] {
		return media.KindEbook, true
	}
	if unambiguousAudiobook[ext] {
		return media.KindAudiobook, true
	}

	if mediaExtensions[media.KindVideo][ext] {
		// Films and episodes share every container, so the name is the only
		// evidence there is. A season folder counts as much as the file name:
		// "Severance/Season 01/pilot.mkv" says television without S01E01 in it.
		for _, segment := range strings.Split(rel, "/") {
			if seasonFolder.MatchString(segment) {
				return media.KindTV, true
			}
		}
		if episodePattern.MatchString(rel) {
			return media.KindTV, true
		}
		return media.KindVideo, true
	}

	if mediaExtensions[media.KindMusic][ext] {
		// An mp3 is a song or a chapter of a book and nothing in the file says
		// which. Music is the common case, so it is the default; a folder that
		// says otherwise is believed, and anybody who disagrees can drop onto
		// the Audiobooks shelf instead of onto the window.
		if mentionsAudiobooks(rel) {
			return media.KindAudiobook, true
		}
		return media.KindMusic, true
	}
	if mediaExtensions[media.KindAudiobook][ext] {
		return media.KindAudiobook, true
	}
	return "", false
}

func mentionsAudiobooks(rel string) bool {
	lower := strings.ToLower(rel)
	return strings.Contains(lower, "audiobook") || strings.Contains(lower, "audio book")
}

// knownExtension reports whether any shelf claims this extension.
func knownExtension(ext string) bool {
	for _, set := range mediaExtensions {
		if set[ext] {
			return true
		}
	}
	return false
}

// folderName is the on-disk folder for a kind, which is not always the kind's
// own name - "movies" rather than "video".
func folderName(kind media.Kind) string {
	for _, f := range layout {
		if f.Kind == kind {
			return f.Name
		}
	}
	return string(kind)
}

// cleanRelPath turns a browser-supplied path into something safe to join onto
// the library root.
//
// Everything here is attacker-controlled. The rules are deliberately strict
// rather than clever: anything that cannot be made into a plain relative path
// of ordinary segments is refused rather than repaired into something
// surprising.
func cleanRelPath(raw string) (string, error) {
	// Browsers use forward slashes even on Windows, but a handcrafted request
	// will not, and a backslash is a separator on the host this may land on.
	raw = strings.ReplaceAll(raw, "\\", "/")
	raw = controlChars.ReplaceAllString(raw, "")

	// A drive letter or a UNC path is not a relative path.
	if strings.HasPrefix(raw, "/") || regexp.MustCompile(`^[A-Za-z]:`).MatchString(raw) {
		return "", fmt.Errorf("absolute paths are not accepted")
	}

	var segments []string
	for _, segment := range strings.Split(raw, "/") {
		segment = strings.TrimSpace(segment)
		if segment == "" || segment == "." {
			continue
		}
		// Dot runs are checked before anything is trimmed. Trimming first
		// turns ".." into "" and then skips it, which quietly repairs
		// "../../etc/passwd.mp3" into "etc/passwd.mp3" and writes it - no
		// escape, but a strange file in somebody's music folder and no hint
		// that a path was rewritten. Refusing is the only honest answer.
		if strings.Trim(segment, ".") == "" {
			return "", fmt.Errorf("paths cannot climb out of the library")
		}
		// Trailing dots and spaces vanish on Windows, which would turn
		// "evil.txt." into "evil.txt" after a check rather than before it.
		segment = strings.TrimRight(segment, ". ")
		if segment == "" {
			continue
		}
		if reservedNames.MatchString(segment) {
			return "", fmt.Errorf("%q is not a usable file name", segment)
		}
		if len(segment) > maxSegment {
			return "", fmt.Errorf("that name is too long")
		}
		segments = append(segments, segment)
	}

	if len(segments) == 0 {
		return "", fmt.Errorf("empty path")
	}
	if len(segments) > maxDepth {
		return "", fmt.Errorf("that is nested too deeply")
	}
	if path.Ext(segments[len(segments)-1]) == "" {
		return "", fmt.Errorf("a file needs an extension for SoundStorm to place it")
	}
	return strings.Join(segments, "/"), nil
}

// ErrAlreadyThere is returned when the destination exists.
//
// Refusing rather than renaming to "(2)": dropping the same album twice is a
// mistake far more often than it is a request for a second copy, and silently
// duplicating a hundred tracks is not a thing to do without being asked.
var ErrAlreadyThere = fmt.Errorf("already in your library")

// Save writes one uploaded file into its shelf.
//
// The bytes land in a staging directory first and are renamed into place only
// once they are all there. Navidrome and Audiobookshelf watch these folders,
// and a half-written file is exactly the kind of thing a scanner indexes as a
// corrupt track - the staging directory is inside the library root so the
// rename is on one filesystem and therefore atomic, and it is a dot-directory
// at the top level, which no backend has mounted.
func (l *Library) Save(kind media.Kind, rel string, r io.Reader) (string, error) {
	rel, err := cleanRelPath(rel)
	if err != nil {
		return "", err
	}
	folder := l.PathFor(kind)
	if folder == "" {
		return "", fmt.Errorf("there is no %s library", kind)
	}

	dest := filepath.Join(folder, filepath.FromSlash(rel))
	// Sanitising should already guarantee this. Checking the result anyway
	// costs nothing and turns a future mistake in cleanRelPath into a refusal
	// rather than a write outside the library.
	if !within(folder, dest) {
		return "", fmt.Errorf("that path does not stay inside the library")
	}
	if _, err := os.Stat(dest); err == nil {
		return "", ErrAlreadyThere
	}

	staging := filepath.Join(l.root, ".uploads")
	if err := os.MkdirAll(staging, 0o777); err != nil {
		return "", fmt.Errorf("prepare upload: %w", err)
	}
	tmp, err := os.CreateTemp(staging, "part-*")
	if err != nil {
		return "", fmt.Errorf("prepare upload: %w", err)
	}
	tmpName := tmp.Name()
	// Nothing below leaves a stray part file behind, including the paths that
	// return early.
	defer func() {
		tmp.Close()
		os.Remove(tmpName)
	}()

	if _, err := io.Copy(tmp, r); err != nil {
		return "", fmt.Errorf("receive file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("receive file: %w", err)
	}

	if err := ensureDir(filepath.Dir(dest)); err != nil {
		return "", err
	}
	// 0666 for the same reason the folders are permissive: a person on the
	// host has to be able to move and delete their own media.
	if err := os.Chmod(tmpName, 0o666); err != nil {
		return "", fmt.Errorf("set permissions: %w", err)
	}
	if err := os.Rename(tmpName, dest); err != nil {
		return "", fmt.Errorf("put the file in place: %w", err)
	}

	l.Invalidate()
	return folderName(kind) + "/" + rel, nil
}

// within reports whether child is inside parent.
func within(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// ensureDir creates a directory, counting one that already exists as success.
//
// The same Docker Desktop bind-mount quirk internal/starter documents: MkdirAll
// can return EEXIST for a directory that is plainly there.
func ensureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o777); err == nil {
		return nil
	}
	info, err := os.Stat(dir)
	if err == nil && info.IsDir() {
		return nil
	}
	return fmt.Errorf("create %s: %w", dir, err)
}

// ClearStaging removes part files left behind by an interrupted upload.
//
// Called at startup. A crash mid-upload leaves bytes in the staging directory
// that nothing will ever finish, and they are invisible to the user because the
// directory is hidden and unmounted.
func (l *Library) ClearStaging() {
	staging := filepath.Join(l.root, ".uploads")
	entries, err := os.ReadDir(staging)
	if err != nil {
		return
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "part-") {
			_ = os.Remove(filepath.Join(staging, e.Name()))
		}
	}
}
