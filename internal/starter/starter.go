// Package starter ships one item per shelf inside the binary.
//
// A media server with nothing in it cannot be evaluated. Before this, a fresh
// install left you with five empty folders and an instruction to go and find
// media - so the first thing anyone judged was an empty search box, and whether
// the thing actually works was a question for later.
//
// It is bundled rather than downloaded, and that is the whole design decision.
// Fetching on install means every installation hits somebody else's bandwidth:
// Project Gutenberg states outright that automated access to its website will
// get an IP blocked, and the Internet Archive is perpetually stretched. A
// popular project that downloads from donated services on every install is a
// bad citizen and would deserve to be blocked. Bundling also means the first
// run works with no network at all, which matters for the air-gapped installs
// self-hosters actually do.
//
// One of each rather than a spread. It was ten ebooks and four music tracks,
// which demonstrated nothing the first of each did not and made the ebook shelf
// look like somebody else's taste in books. What a first run has to answer is
// "does each kind of media work", and that takes exactly one of each.
//
//	ebook       The Richest Man in Babylon (1926)    Wikisource, public domain
//	audiobook   As a Man Thinketh                    LibriVox, public domain
//	music       Aria, Open Goldberg Variations       CC0 1.0
//
// Free licences are a hard constraint here, not a preference: these files are
// compiled into a published binary. A genuinely famous song is almost certainly
// somebody's copyright, so each of these is as recognisable as a free licence
// allows rather than as recognisable as possible.
//
// No video, and it was tried. Big Buck Bunny was bundled for one commit: CC-BY,
// re-encoded to 25MB, and it direct-played through Jellyfin without a transcode.
// It was still 60% of the bundle on its own - more than everything else combined
// - which is the same arithmetic that kept video out in the first place, and
// three shelves' worth of proof that the thing works is enough. The UI names
// Blender's open movies instead, which is a better use of the space: what
// changes how somebody uses a media server is finding out where free films are.
package starter

import (
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// go:embed rejects file names containing an apostrophe, so the bundled files
// do without one. Nothing is lost: what a reader sees comes from the metadata
// inside each file, not from its name.
//
//go:embed all:media
var bundled embed.FS

// Unpacked reports what was written, for logging and for the UI.
type Unpacked struct {
	Folder string
	Files  int
	Bytes  int64
}

// Install copies the bundled library into empty library folders.
//
// Only empty ones: if there is anything at all in a folder, that is somebody's
// library and it is not ours to add to. This makes the whole thing idempotent
// without tracking state - the second run finds the folders populated, by us or
// by the user, and does nothing either way.
//
// A user who deletes the starter files has said something, so they do not come
// back on the next restart... unless they emptied the folder entirely, which is
// indistinguishable from a fresh install. That is a deliberate trade for not
// keeping a marker file around forever.
func Install(root string, isEmpty func(folder string) bool, log *slog.Logger) ([]Unpacked, error) {
	entries, err := fs.ReadDir(bundled, "media")
	if err != nil {
		return nil, fmt.Errorf("read bundled media: %w", err)
	}

	var installed []Unpacked
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		folder := entry.Name()
		if !isEmpty(folder) {
			continue
		}

		// path.Join, not filepath.Join: an embed.FS is always slash-separated,
		// so building its paths with the OS separator silently finds nothing
		// on Windows. The destination is the opposite case and does want
		// filepath.
		result, err := copyTree(path.Join("media", folder), filepath.Join(root, folder))
		if err != nil {
			// One unwritable folder should not stop the others, and should
			// certainly not stop the server starting.
			log.Warn("could not install starter media", "folder", folder, "err", err)
			continue
		}
		if result.Files > 0 {
			result.Folder = folder
			installed = append(installed, result)
		}
	}

	for _, u := range installed {
		log.Info("added starter media",
			"folder", u.Folder, "files", u.Files, "megabytes", u.Bytes/(1<<20))
	}
	return installed, nil
}

// copyTree writes one embedded folder out to disk.
func copyTree(from, to string) (Unpacked, error) {
	var out Unpacked

	err := fs.WalkDir(bundled, from, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// embed.FS always uses forward slashes; the destination must not.
		rel := strings.TrimPrefix(strings.TrimPrefix(path, from), "/")
		dest := to
		if rel != "" {
			dest = filepath.Join(to, filepath.FromSlash(rel))
		}

		if d.IsDir() {
			return ensureDir(dest)
		}

		data, err := bundled.ReadFile(path)
		if err != nil {
			return err
		}
		if err := ensureDir(filepath.Dir(dest)); err != nil {
			return err
		}
		// 0666 for the same reason the library folders are permissive: these
		// are media files a person on the host is expected to be able to move,
		// rename and delete.
		if err := os.WriteFile(dest, data, 0o666); err != nil {
			return err
		}

		out.Files++
		out.Bytes += int64(len(data))
		return nil
	})

	return out, err
}

// ensureDir makes a directory, and counts one that already exists as success.
//
// os.MkdirAll is documented to do exactly that, and on an ordinary filesystem
// it does. On a Docker Desktop bind mount it can instead return EEXIST for a
// directory that is plainly there - which is how the starter library came to
// skip precisely the two folders compose mounts into the backends, music and
// audiobooks, while succeeding for ebooks, which nothing else mounts.
func ensureDir(path string) error {
	if err := os.MkdirAll(path, 0o777); err == nil {
		return nil
	}
	info, err := os.Stat(path)
	if err == nil && info.IsDir() {
		return nil
	}
	return fmt.Errorf("create %s: %w", path, err)
}

// Attribution describes where one bundled collection came from.
type Attribution struct {
	Folder  string `json:"folder"`
	Title   string `json:"title"`
	Source  string `json:"source"`
	URL     string `json:"url"`
	License string `json:"license"`
}

// Attributions is the credit list, and the more useful half of the idea.
//
// Nobody keeps these files. What changes how somebody uses a media server is
// finding out that LibriVox has thousands of free audiobooks and that Gutenberg
// has seventy thousand books - so the UI shows where this came from, not just
// what it is.
func Attributions() []Attribution {
	return []Attribution{
		{
			Folder:  "ebooks",
			Title:   "The Richest Man in Babylon, by George S. Clason",
			Source:  "Wikisource",
			URL:     "https://en.wikisource.org/wiki/The_Richest_Man_In_Babylon",
			License: "Public domain (published 1926)",
		},
		{
			Folder:  "audiobooks",
			Title:   "As a Man Thinketh, by James Allen",
			Source:  "LibriVox",
			URL:     "https://librivox.org",
			License: "Public domain recording",
		},
		{
			Folder:  "music",
			Title:   "Aria, from the Open Goldberg Variations - Kimiko Ishizaka",
			Source:  "The Open Goldberg Variations",
			URL:     "https://archive.org/details/OpenGoldbergVariations",
			License: "CC0 1.0 Public Domain Dedication",
		},
		{
			// Not bundled: one film outweighs everything above it combined.
			// Big Buck Bunny was, briefly, and measured at 25MB of a 42MB
			// bundle. Named anyway, because pointing somebody at free films is
			// the point.
			Folder:  "movies",
			Title:   "Not included - Blender's open movies are a good first download",
			Source:  "Blender Foundation",
			URL:     "https://studio.blender.org/films",
			License: "CC-BY",
		},
	}
}
