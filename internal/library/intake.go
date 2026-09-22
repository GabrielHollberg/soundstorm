package library

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/tags"
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
// And some things genuinely cannot be worked out, in which case the answer is
// to ask rather than to guess. An .mp3 is a song or a chapter of an audiobook
// and nothing in the file says which; a folder of six .mkv files with no
// episode numbering is a boxed set or a series. Asking costs one click and
// being wrong costs somebody going and moving files on disk, so the bar for
// guessing is "there is real evidence", not "one of them is more likely".
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

// ambiguousAudio is the short list worth asking about.
//
// Only mp3, and that is the point of the list existing: flac, wav, aiff and
// alac are music in practice, m4a is music because audiobooks in that family
// use m4b, and asking about all of them would turn every album drop into a
// question. Mp3 really is both - every LibriVox recording is one.
var ambiguousAudio = map[string]bool{".mp3": true}

// ambiguousVideoCount is how many video files in one dropped folder stop
// looking like a film.
//
// One or two .mkv files with no episode numbering is a film and perhaps its
// extras. Six is a series somebody named badly, and putting six episodes in
// the film library is worth one question.
const ambiguousVideoCount = 3

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

	// Group is the dropped item this file came in - a folder name, or the file
	// itself. Everything sharing a group shares a shelf, and a question is
	// asked about a group rather than about each of its files.
	Group string `json:"group,omitempty"`

	// Kind is the shelf. Empty when nothing will be done with this file.
	Kind media.Kind `json:"kind,omitempty"`

	// Dest is the path relative to the library root, which is also what the
	// UI shows: "movies/Arrival (2016)/Arrival (2016).mkv".
	Dest string `json:"dest,omitempty"`

	// Skipped and Reason explain a file SoundStorm will not take.
	Skipped bool   `json:"skipped,omitempty"`
	Reason  string `json:"reason,omitempty"`

	// Waiting marks a file whose group has a question outstanding. It is not
	// skipped and not placed; it is waiting for an answer.
	Waiting bool `json:"waiting,omitempty"`
}

// Question is a group SoundStorm will not guess about.
type Question struct {
	Group string `json:"group"`

	// Label is what to put in front of the choice: the folder or file name,
	// which is the thing the person just dragged and will recognise.
	Label string `json:"label"`

	// Count is how many files hang on the answer, so a question can say
	// "these 30 files" rather than asking thirty times.
	Count int `json:"count"`

	Options []media.Kind `json:"options"`
}

// Plan decides where a set of dropped paths should go.
//
// choices answers questions a previous Plan asked, keyed by group. A group
// with no answer and no way to work one out comes back as a Question and its
// files come back waiting.
func (l *Library) Plan(paths []string, choices map[string]media.Kind) ([]Placement, []Question) {
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
		// per group is what keeps a subtitle with its film - and what makes a
		// thirty-chapter audiobook one question instead of thirty.
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

	var questions []Question
	for _, key := range order {
		members := groups[key]

		kind, options := decideGroup(cleaned, members)
		if answer, ok := choices[key]; ok && permitted(answer, options, kind) {
			kind, options = answer, nil
		}

		if len(options) > 1 {
			// Only the files that would actually go somewhere are worth
			// counting in the question; the .exe in the folder is skipped
			// either way.
			var waiting int
			for _, i := range members {
				if usable(cleaned[i]) {
					waiting++
				}
			}
			questions = append(questions, Question{
				Group:   key,
				Label:   key,
				Count:   waiting,
				Options: options,
			})
		}

		for _, i := range members {
			rel := cleaned[i]
			ext := strings.ToLower(path.Ext(rel))

			switch {
			case !usable(rel):
				out[i] = Placement{
					Path:    paths[i],
					Group:   key,
					Skipped: true,
					Reason:  "SoundStorm does not know what " + ext + " files are",
				}
			case len(options) > 1:
				out[i] = Placement{Path: paths[i], Group: key, Waiting: true}
			case kind == "":
				out[i] = Placement{
					Path:    paths[i],
					Group:   key,
					Skipped: true,
					Reason:  "nothing here says which library this belongs in",
				}
			default:
				out[i] = Placement{
					Path:  paths[i],
					Group: key,
					Kind:  kind,
					// Predicted with no tags, because the bytes have not
					// arrived yet. Save runs the same rule with the file's
					// own tags and can only do better - it fills in a real
					// artist where this had to guess at a placeholder.
					Dest: folderName(kind) + "/" + shelvePath(kind, rel, tags.Tags{}),
				}
			}
		}
	}
	return out, questions
}

// usable reports whether a file is something SoundStorm would ever store.
func usable(rel string) bool {
	ext := strings.ToLower(path.Ext(rel))
	return knownExtension(ext) || companionExtensions[ext]
}

// permitted checks an answer against what was actually asked, so a client
// cannot send a group to a shelf the question never offered.
func permitted(answer media.Kind, options []media.Kind, current media.Kind) bool {
	if len(options) == 0 {
		// Nothing was asked; an answer is only meaningful if it changes a
		// group that had no shelf of its own.
		return current == ""
	}
	for _, o := range options {
		if o == answer {
			return true
		}
	}
	return false
}

// decideGroup works out one shelf for everything dropped together, or returns
// the choices worth offering when it genuinely cannot.
func decideGroup(cleaned []string, members []int) (media.Kind, []media.Kind) {
	var (
		videoFiles int
		audioFiles int
		hasVideo   bool
		hasAudio   bool
		hasMP3     bool
		episodes   bool
	)

	for _, i := range members {
		rel := cleaned[i]
		ext := strings.ToLower(path.Ext(rel))
		if ext == "" || companionExtensions[ext] {
			continue
		}

		// The decisive ones answer outright and stop the walk.
		if mediaExtensions[media.KindEbook][ext] {
			return media.KindEbook, nil
		}
		if unambiguousAudiobook[ext] {
			return media.KindAudiobook, nil
		}
		if mentionsAudiobooks(rel) && (mediaExtensions[media.KindMusic][ext] ||
			mediaExtensions[media.KindAudiobook][ext]) {
			return media.KindAudiobook, nil
		}

		if mediaExtensions[media.KindVideo][ext] {
			hasVideo = true
			videoFiles++
			if looksLikeEpisode(rel) {
				episodes = true
			}
			continue
		}
		if mediaExtensions[media.KindMusic][ext] || mediaExtensions[media.KindAudiobook][ext] {
			hasAudio = true
			audioFiles++
			if ambiguousAudio[ext] {
				hasMP3 = true
			}
		}
	}

	// A folder of tracks with one video in it is an album with a bonus video,
	// not a film. Any video used to win outright, so a single .mp4 sitting
	// beside eleven .flac files sent the whole album to the film library -
	// tracks and all. The video rides along into the music folder, where it
	// keeps the album it belongs to and Navidrome simply ignores it.
	audioLed := hasAudio && audioFiles > videoFiles

	switch {
	case hasVideo && episodes && !audioLed:
		return media.KindTV, nil
	case audioLed && hasMP3:
		return "", []media.Kind{media.KindMusic, media.KindAudiobook}
	case audioLed:
		return media.KindMusic, nil
	case hasVideo && videoFiles >= ambiguousVideoCount:
		return "", []media.Kind{media.KindVideo, media.KindTV}
	case hasVideo:
		return media.KindVideo, nil
	case hasAudio && hasMP3:
		return "", []media.Kind{media.KindMusic, media.KindAudiobook}
	case hasAudio:
		// flac, wav, m4a and the rest are music in practice.
		return media.KindMusic, nil
	}
	return "", nil
}

// looksLikeEpisode reports whether a path names television.
func looksLikeEpisode(rel string) bool {
	for _, segment := range strings.Split(rel, "/") {
		if seasonFolder.MatchString(segment) {
			return true
		}
	}
	return episodePattern.MatchString(rel)
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

	// Where it goes is decided now rather than before the bytes arrived,
	// because for a loose track the answer is inside the file. Nothing has
	// been written outside the staging directory yet, so a refusal here still
	// leaves the library untouched.
	rel = shelveUnder(kind, rel, tmpName)

	dest := filepath.Join(folder, filepath.FromSlash(rel))
	// Sanitising should already guarantee this. Checking the result anyway
	// costs nothing and turns a future mistake in cleanRelPath - or in the
	// names that just came out of a stranger's tags - into a refusal rather
	// than a write outside the library.
	if !within(folder, dest) {
		return "", fmt.Errorf("that path does not stay inside the library")
	}
	if _, err := os.Stat(dest); err == nil {
		return "", ErrAlreadyThere
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

// --- shelf layout -----------------------------------------------------------

// structuredDepth is how many folders a shelf expects above the file.
//
// Music and audiobooks are the two the backends read structurally: Navidrome
// groups an album by its folder as well as its tags, and Audiobookshelf reads
// Author/Title straight from the path. Films and television are already folder
// shaped by the time anybody drops them, and Jellyfin matches on the *name*
// rather than the depth, so imposing a layout there would be inventing one.
// Ebooks are a flat folder on purpose.
var structuredDepth = map[media.Kind]int{
	media.KindMusic:     2, // Artist/Album/track
	media.KindAudiobook: 2, // Author/Title/part
}

// shelveUnder gives a loose file the folders its shelf expects.
//
// Dropping a single track used to leave it at the top of music/, which is
// untidy rather than broken - Navidrome reads tags, not paths - but it is also
// exactly the mess the folders exist to prevent, and it compounds one file at
// a time.
//
// Anything already deep enough is left alone: somebody who dropped
// Artist/Album/track.flac has said where it goes more reliably than a tag
// will, and second-guessing that would move files for no reason.
func shelveUnder(kind media.Kind, rel, staged string) string {
	if _, ok := structuredDepth[kind]; !ok {
		return rel
	}
	return shelvePath(kind, rel, readTags(staged))
}

// shelvePath applies the layout rule. Split out so that Plan can run it with
// no tags at all - it has only the path at that point - and Save can run it
// again with the file's own. They agree whenever a file is untagged, and when
// it is not, Save is strictly better informed than the prediction.
func shelvePath(kind media.Kind, rel string, t tags.Tags) string {
	want, ok := structuredDepth[kind]
	if !ok {
		return rel
	}
	depth := strings.Count(rel, "/")
	if depth >= want {
		return rel
	}

	// Only the missing levels are filled in. A drop of "Laughing Stock/01
	// Myrrhman.flac" already names the album, and replacing that with whatever
	// the tags say - or worse, with a placeholder when there are no tags -
	// would throw away the one piece of structure a person actually chose.
	base := rel
	middle := ""
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		base, middle = rel[i+1:], rel[:i]
	}

	artist := t.Folder()
	album := middle
	if album == "" {
		album = t.Album
	}
	if kind == media.KindAudiobook && album == "" {
		// Audiobookshelf reads the album tag as the book and the artist tag as
		// the author, which is what every audiobook ripper writes - but a
		// single-file book often carries only a title.
		album = t.Title
	}

	// Placeholders when nothing is known, rather than leaving the file loose.
	// The rule is that a track always has an artist folder and an album
	// folder, and a file that says nothing about itself is exactly the one
	// that needs somewhere obvious to be found and fixed.
	first, second := "Unknown Artist", "Unknown Album"
	if kind == media.KindAudiobook {
		first, second = "Unknown Author", "Unknown Title"
	}
	rebuilt, err := cleanRelPath(tagSegment(artist, first) + "/" + tagSegment(album, second) + "/" + base)
	if err != nil {
		// A tag that cannot survive being a path is not worth failing an
		// upload over; where it was asked to go is safe.
		return rel
	}
	return rebuilt
}

// readTags opens the staged file and reads what it says about itself. Any
// failure is silence: a file with no tags is ordinary, not an error.
func readTags(path string) tags.Tags {
	f, err := os.Open(path)
	if err != nil {
		return tags.Tags{}
	}
	defer f.Close()
	t, err := tags.Read(f)
	if err != nil {
		return tags.Tags{}
	}
	return t
}

// tagSegment turns a tag into one path segment, or falls back.
//
// Tags come from whoever made the file, so they contain slashes, colons and
// occasionally a newline. Those are replaced rather than refused: an album
// genuinely called "AC/DC Live" should not fail to upload.
func tagSegment(value, fallback string) string {
	value = strings.TrimSpace(strings.Map(func(r rune) rune {
		switch r {
		case '/', rune(92), ':', '*', '?', rune(34), '<', '>', '|':
			return '-'
		}
		if r < 0x20 {
			return -1
		}
		return r
	}, value))
	value = strings.TrimRight(value, ". ")
	if value == "" {
		return fallback
	}
	if len(value) > maxSegment {
		value = strings.TrimSpace(value[:maxSegment])
	}
	return value
}
