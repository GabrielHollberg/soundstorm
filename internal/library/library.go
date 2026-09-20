// Package library owns the folder layout a user actually interacts with.
//
// This is the first thing anyone touches after installing SoundStorm, and it is
// the only part of the product with no UI: you put a file in a folder and it
// appears. That makes the folders themselves the interface, so they are created
// for you, named unambiguously, and described in the app rather than only in a
// README nobody reads.
//
// SoundStorm reads this directory for two narrow purposes - creating it, and
// counting what is in it so the UI can say "1,240 files, none searchable yet,
// Navidrome is still scanning". It does NOT index it. Indexing is the backends'
// job, except for ebooks, which have no backend (see internal/epub for why that
// exception is narrow).
package library

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/media"
)

// countCacheTTL bounds how stale a file count may be. Counting means walking
// the tree, and a large library makes that too expensive to do per request.
const countCacheTTL = 30 * time.Second

// maxCountWalk stops the counter from walking forever on a pathological tree.
// A number this big is already past the point where the exact figure matters.
const maxCountWalk = 200_000

// Folder is one media type's home on disk.
type Folder struct {
	Kind media.Kind `json:"kind"`
	Name string     `json:"name"`

	// Hint is the path as the *user* sees it from where they ran compose,
	// which is the only path worth showing them. SoundStorm's own view is inside
	// a container and means nothing to a human.
	Hint string `json:"hint"`

	// What goes in here, in the user's words rather than ours.
	Description string `json:"description"`
	Example     string `json:"example"`

	// Files counts the media files on disk. Compare with a backend's indexed
	// count to tell "nothing here yet" from "not scanned yet".
	Files int `json:"files"`

	path string
}

// layout is the folder set, in the order the UI should show them.
//
// Named for what people call the thing rather than for how the backends model
// it: "movies" not "video", because nobody has a folder called video, and
// "tv" separate from movies because Jellyfin models series differently and
// because that is how people already arrange their drives.
var layout = []Folder{
	{
		Kind:        media.KindMusic,
		Name:        "music",
		Description: "Albums, singles, anything you listen to as music.",
		Example:     "music/Talk Talk/Laughing Stock/01 Myrrhman.flac",
	},
	{
		Kind:        media.KindVideo,
		Name:        "movies",
		Description: "Films. One folder per film, with the year in the name.",
		Example:     "movies/Arrival (2016)/Arrival (2016).mkv",
	},
	{
		Kind:        media.KindTV,
		Name:        "tv",
		Description: "Series. One folder per show, then one per season.",
		Example:     "tv/Severance (2022)/Season 01/Severance - S01E01.mkv",
	},
	{
		Kind:        media.KindAudiobook,
		Name:        "audiobooks",
		Description: "Audiobooks, filed by author then title.",
		Example:     "audiobooks/Ursula K. Le Guin/A Wizard of Earthsea/book.m4b",
	},
	{
		Kind:        media.KindEbook,
		Name:        "ebooks",
		Description: "EPUB and PDF files. An existing Calibre library works here too.",
		Example:     "ebooks/A Wizard of Earthsea.epub",
	},
}

// mediaExtensions is what counts as a media file per folder. Deliberately
// generous: the point is to tell a user "SoundStorm can see your files", not to
// predict what a backend will accept.
var mediaExtensions = map[media.Kind]map[string]bool{
	media.KindMusic: {
		".mp3": true, ".flac": true, ".m4a": true, ".ogg": true, ".opus": true,
		".wav": true, ".aac": true, ".wma": true, ".aiff": true, ".alac": true,
	},
	media.KindVideo: {
		".mkv": true, ".mp4": true, ".avi": true, ".mov": true, ".m4v": true,
		".wmv": true, ".webm": true, ".mpg": true, ".mpeg": true, ".ts": true,
	},
	media.KindTV: {
		".mkv": true, ".mp4": true, ".avi": true, ".mov": true, ".m4v": true,
		".wmv": true, ".webm": true, ".mpg": true, ".mpeg": true, ".ts": true,
	},
	media.KindAudiobook: {
		".m4b": true, ".mp3": true, ".m4a": true, ".aax": true, ".aaxc": true,
		".ogg": true, ".opus": true, ".flac": true,
	},
	media.KindEbook: {
		".epub": true, ".mobi": true, ".azw3": true, ".pdf": true, ".cbz": true,
	},
}

// Library is the on-disk media root.
type Library struct {
	root string
	hint string
	log  *slog.Logger

	mu        sync.Mutex
	cached    []Folder
	countedAt time.Time
}

// Open prepares the library root, creating any folder that does not exist.
//
// hint is the path as the user sees it (compose passes "./library"); it is used
// only for display.
func Open(root, hint string, log *slog.Logger) (*Library, error) {
	if root == "" {
		return nil, fmt.Errorf("library root is required")
	}
	if hint == "" {
		hint = root
	}

	l := &Library{root: root, hint: hint, log: log}
	if err := l.ensure(); err != nil {
		return nil, err
	}
	return l, nil
}

// Root returns the library root as SoundStorm sees it.
func (l *Library) Root() string { return l.root }

// PathFor returns the absolute path of one media kind's folder.
func (l *Library) PathFor(kind media.Kind) string {
	for _, f := range layout {
		if f.Kind == kind {
			return filepath.Join(l.root, f.Name)
		}
	}
	return ""
}

// ensure creates the root and its folders if they are missing.
func (l *Library) ensure() error {
	if err := os.MkdirAll(l.root, 0o777); err != nil {
		// Almost always a container started without its library mounted, and
		// "permission denied" on its own sends people looking at file modes
		// rather than at the missing volume.
		return fmt.Errorf("create library root %s: %w "+
			"(if this is a container, mount a writable folder there)", l.root, err)
	}

	var created []string
	for _, f := range layout {
		path := filepath.Join(l.root, f.Name)

		// Already there is the common case, and not only because we made it
		// last time: compose bind-mounts library/music and library/movies into
		// the backends, so Docker creates those directories before this ever
		// runs.
		if info, err := os.Stat(path); err == nil {
			if !info.IsDir() {
				l.log.Warn("library path is not a directory", "path", path)
			}
			continue
		} else if !os.IsNotExist(err) {
			// A stat that failed for any other reason is not evidence the
			// folder is missing, and guessing that it is leads straight to
			// mkdir reporting "file exists".
			l.log.Warn("could not inspect library folder", "path", path, "err", err)
			continue
		}

		if err := os.MkdirAll(path, 0o777); err != nil {
			// Losing one folder must not stop the server. It used to: a
			// fresh install where Docker had created the mount points first
			// crash-looped on "mkdir: file exists", which is a spectacular
			// way to fail at the one job this code has.
			if info, statErr := os.Stat(path); statErr == nil && info.IsDir() {
				continue // somebody else created it; that is a success
			}
			l.log.Warn("could not create library folder", "path", path, "err", err)
			continue
		}
		// MkdirAll applies the process umask, which in a container usually
		// clears group and other write. These folders exist to be written into
		// by a person on the host whose uid we cannot know and whose files the
		// backends must also read, so the permissions are set explicitly
		// afterwards. A home media folder is not a secret; being unable to copy
		// files into your own library is a much worse failure than this is.
		if err := os.Chmod(path, 0o777); err != nil {
			l.log.Warn("could not relax permissions on library folder",
				"path", path, "err", err)
		}
		created = append(created, f.Name)
	}

	if len(created) > 0 {
		l.log.Info("created library folders",
			"root", l.root, "folders", strings.Join(created, ", "))
	}
	return nil
}

// Folders returns the layout with current file counts, cached briefly.
func (l *Library) Folders() []Folder {
	l.mu.Lock()
	defer l.mu.Unlock()

	if time.Since(l.countedAt) < countCacheTTL && l.cached != nil {
		return append([]Folder(nil), l.cached...)
	}

	out := make([]Folder, 0, len(layout))
	for _, f := range layout {
		f.path = filepath.Join(l.root, f.Name)
		f.Hint = l.hint + "/" + f.Name
		f.Files = countMedia(f.path, mediaExtensions[f.Kind])
		out = append(out, f)
	}

	l.cached = out
	l.countedAt = time.Now()
	return append([]Folder(nil), out...)
}

// FolderIsEmpty reports whether one folder has no media in it.
//
// "No media" rather than "no files": the folders ship with a README.txt, and a
// folder containing only that is still one nobody has put anything in.
func (l *Library) FolderIsEmpty(name string) bool {
	for _, f := range l.Folders() {
		if f.Name == name {
			return f.Files == 0
		}
	}
	return false
}

// Invalidate drops the cached counts, for when something has just written to
// the library and the next read should see it.
func (l *Library) Invalidate() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.countedAt = time.Time{}
}

// IsEmpty reports whether every folder is empty, which is the state a fresh
// install is in and the one the UI most needs to explain.
func (l *Library) IsEmpty() bool {
	for _, f := range l.Folders() {
		if f.Files > 0 {
			return false
		}
	}
	return true
}

// countMedia counts files with a matching extension, cheaply.
func countMedia(root string, extensions map[string]bool) int {
	if len(extensions) == 0 {
		return 0
	}
	var count, seen int
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable corner should not zero the count
		}
		if seen++; seen > maxCountWalk {
			return fs.SkipAll
		}
		if d.IsDir() {
			// Skip dot-directories and Calibre's internal bookkeeping.
			if path != root && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		if extensions[strings.ToLower(filepath.Ext(d.Name()))] {
			count++
		}
		return nil
	})
	return count
}
