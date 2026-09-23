package library

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/GabrielHollberg/soundstorm/internal/epub"
	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/pdf"
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

	// maxDepth keeps a path recognisable as a path. Nothing legitimate nests an
	// album eight levels deep.
	maxDepth = 8

	// maxSegment bounds one name. It was 120, which is not a sanity bound but a
	// real ceiling that real files hit: an Audible filename is title, subtitle
	// and ASIN in one string, and a drop of 94 audiobooks had its long-named
	// half refused. The evidence was the library itself - the longest name that
	// had ever made it in was 118 characters, and nothing sat above it.
	//
	// 200 leaves room under the 255 that ext4 and NTFS both allow per component,
	// and anything longer is shortened rather than refused. See shortenSegment.
	maxSegment = 200
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
	// Why each rejected path was rejected. Every one of them used to read "that
	// does not look like a file name", which is true of an absolute path and
	// actively misleading about a name three characters too long - somebody with
	// half an audiobook library missing had no way to tell which.
	refused := make([]string, len(paths))
	groups := map[string][]int{}
	var order []string

	for i, raw := range paths {
		rel, err := cleanRelPath(raw)
		if err != nil {
			refused[i] = err.Error()
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
		why := refused[i]
		if why == "" {
			why = "that does not look like a file name"
		}
		out[i] = Placement{
			Path:    paths[i],
			Skipped: true,
			Reason:  why,
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
					Dest: folderName(kind) + "/" + shelvePath(kind, rel, fromFilename(kind, rel)),
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
		hasPDF     bool
		hasOPF     bool
		pdfSays    media.Kind
		photos     int  // images that look like photos, not artwork
		photoPath  bool // a folder name says pictures
	)

	for _, i := range members {
		rel := cleaned[i]
		ext := strings.ToLower(path.Ext(rel))
		if ext == ".opf" {
			// Calibre writes a metadata.opf beside every book it manages.
			hasOPF = true
		}
		// Counted before the companion check, because a .jpg is both: the
		// cover of an album and a photo. Which it is depends on what else was
		// dropped with it, and that is only known once the walk is done.
		if stillImage[ext] && !looksLikeArtwork(rel) {
			photos++
		}
		if mentionsPictures(rel) {
			photoPath = true
		}
		if ext == "" || companionExtensions[ext] {
			continue
		}

		// A PDF is a book or a document and the file does not say which, so
		// it waits for the rest of the group - an audiobook's PDF companion
		// must not send the audiobook to a book shelf.
		if ext == ".pdf" {
			hasPDF = true
			if pdfSays == "" {
				pdfSays = pdfEvidence(rel)
			}
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

	// Photos lead when there is nothing else, or when they plainly outnumber
	// the videos beside them - a camera roll with a few clips in it. A film
	// folder with a poster and some fan art is not that: artwork is not
	// counted as photos, and one or two images next to a video never lead.
	// Audio or a book in the group means the images are its artwork.
	photoLed := photos > 0 && !hasAudio && !hasPDF &&
		(!hasVideo || photoPath || (photos >= 5 && photos > 2*videoFiles))

	switch {
	case photoLed:
		return media.KindPicture, nil
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
	case hasPDF && hasOPF:
		// A Calibre library: every book in it has a metadata.opf beside it.
		return media.KindEbook, nil
	case hasPDF && pdfSays != "":
		return pdfSays, nil
	case hasPDF:
		return "", []media.Kind{media.KindEbook, media.KindDocument}
	}
	return "", nil
}

// Folder names that say what a PDF is. Matched as whole path segments, so a
// book called "Paperback Writer" is not taken for a document by "paper".
var (
	bookFolders = map[string]bool{
		"books": true, "ebooks": true, "e-books": true, "calibre": true,
		"calibre library": true, "novels": true, "textbooks": true,
	}
	documentFolders = map[string]bool{
		"documents": true, "docs": true, "papers": true, "manuals": true,
		"paperwork": true, "statements": true, "receipts": true, "invoices": true,
		"scans": true, "forms": true, "taxes": true, "contracts": true,
		"my documents": true,
	}
)

// pdfEvidence reads what the dropped path says a PDF is, or "" when it says
// nothing - which is when the drop is asked about rather than guessed at.
func pdfEvidence(rel string) media.Kind {
	segments := strings.Split(strings.ToLower(rel), "/")
	for _, s := range segments[:len(segments)-1] {
		s = strings.TrimSpace(s)
		switch {
		case bookFolders[s]:
			return media.KindEbook
		case documentFolders[s]:
			return media.KindDocument
		}
	}
	return ""
}

// stillImage are the extensions a photo arrives as. Several of them double as
// artwork, which is why they are also companions.
var stillImage = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".heic": true, ".heif": true,
	".webp": true, ".gif": true, ".tif": true, ".tiff": true, ".avif": true,
	".dng": true, ".cr2": true, ".cr3": true, ".nef": true, ".arw": true,
	".raf": true, ".orf": true, ".rw2": true,
}

// artworkNames are what media managers call the pictures they keep beside a
// film or an album - Kodi, Jellyfin and every tagger agree on these. Matched
// against the name without its extension, and "-poster" style suffixes too.
var artworkNames = []string{
	"poster", "fanart", "folder", "cover", "backdrop", "banner", "logo",
	"thumb", "landscape", "clearart", "clearlogo", "disc", "discart", "front",
	"back", "keyart", "background",
}

// artworkFolders hold nothing but artwork, however many files are in them.
var artworkFolders = map[string]bool{
	"extrafanart": true, "extrathumbs": true, "artwork": true, ".actors": true,
	"scans": true, "covers": true,
}

// looksLikeArtwork reports whether an image is a poster, a cover or fan art
// rather than a photo, so a film folder with ten pieces of fan art is still a
// film.
func looksLikeArtwork(rel string) bool {
	segments := strings.Split(strings.ToLower(rel), "/")
	for _, dir := range segments[:len(segments)-1] {
		if artworkFolders[dir] {
			return true
		}
	}
	base := segments[len(segments)-1]
	base = strings.TrimSuffix(base, path.Ext(base))
	for _, name := range artworkNames {
		if base == name || strings.HasSuffix(base, "-"+name) {
			return true
		}
	}
	return false
}

// mentionsPictures reports whether a folder in the path says it holds photos.
// DCIM is what every camera and phone names its own folder.
func mentionsPictures(rel string) bool {
	segments := strings.Split(strings.ToLower(rel), "/")
	for _, dir := range segments[:len(segments)-1] {
		switch strings.TrimSpace(dir) {
		case "pictures", "photos", "dcim", "camera", "camera roll", "my pictures":
			return true
		}
	}
	return false
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
		// Shortened rather than refused, and before the reserved-name check so a
		// cut cannot produce one. A name too long to store is a reason to store
		// it under a shorter name, not a reason to refuse somebody's audiobook -
		// and nothing is hidden by it, because the plan shows the destination it
		// will use before anything is copied.
		if len(segment) > maxSegment {
			segment = shortenSegment(segment)
		}
		if reservedNames.MatchString(segment) {
			return "", fmt.Errorf("%q is not a usable file name", segment)
		}
		if segment == "" {
			continue
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

// shortenSegment cuts an over-long name down to fit, keeping its extension.
//
// The extension is what decides the shelf and what every player dispatches on,
// so it survives at the cost of the title. Cut on a rune boundary: a name ending
// in half a UTF-8 character is not a name, and these are full of typographic
// quotes and accents.
func shortenSegment(s string) string {
	ext := path.Ext(s)
	// Past a certain length that full stop was part of the title rather than an
	// extension, and keeping it would waste the budget on nothing.
	if len(ext) > 16 {
		ext = ""
	}
	budget := maxSegment - len(ext)
	if budget <= 0 {
		return ""
	}

	var b strings.Builder
	for _, r := range s[:len(s)-len(ext)] {
		if b.Len()+utf8.RuneLen(r) > budget {
			break
		}
		b.WriteRune(r)
	}
	// A trailing dot or space disappears on Windows, which is the same trap the
	// caller trims for before it gets here.
	return strings.TrimRight(b.String(), ". ") + ext
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
		return "", receiveError(staging, err)
	}
	if err := tmp.Close(); err != nil {
		return "", receiveError(staging, err)
	}

	// Where it goes is decided now rather than before the bytes arrived,
	// because for a loose track the answer is inside the file. Nothing has
	// been written outside the staging directory yet, so a refusal here still
	// leaves the library untouched.
	rel = shelveUnder(kind, rel, tmpName, folder)

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
// Author/Title straight from the path. Ebooks are shelved the same way for the
// person browsing the folder rather than for any backend - they were flat
// until a Calibre library arrived as Author/Title and every other upload
// landed loose beside it. Films and television are already folder shaped by
// the time anybody drops them, and Jellyfin matches on the *name* rather than
// the depth, so imposing a layout there would be inventing one.
var structuredDepth = map[media.Kind]int{
	media.KindMusic:     2, // Artist/Album/track
	media.KindAudiobook: 2, // Author/Title/part
	media.KindEbook:     2, // Author/Title/book, which is also Calibre's own layout
}

// shelveUnder files an upload as Artist/Album or Author/Title.
//
// A file that cannot carry tags inherits its group's folder rather than
// deriving one: an Audible book arrives as an .m4b beside a companion .pdf,
// and reading each one's own tags put them in different places - the m4b
// under "Brandon Sanderson/The Way of Kings [B003ZWFO7E]" and the pdf under
// "Unknown Author/The Way of Kings [B003ZWFO7E]". One book, two authors, and
// the PDF orphaned from the thing it explains. The client sends taggable files
// first so the group's folder exists by the time a companion arrives.
func shelveUnder(kind media.Kind, rel, staged, shelf string) string {
	if _, ok := structuredDepth[kind]; !ok {
		return rel
	}
	if !describesItself(kind, rel) {
		if beside, ok := groupFolder(kind, rel, shelf); ok {
			return beside
		}
	}
	return shelvePath(kind, rel, readMeta(kind, staged, rel))
}

// describesItself reports whether a file can say where it belongs on this
// shelf. Per shelf, not per extension: a .pdf is a book on the ebook shelf and
// a companion on the audiobook one, where letting it read its own metadata
// filed Audible's accompanying PDFs under a different author from their book.
func describesItself(kind media.Kind, rel string) bool {
	if kind == media.KindEbook {
		switch strings.ToLower(path.Ext(rel)) {
		case ".epub", ".pdf":
			return true
		}
		return false
	}
	return taggable(rel)
}

// readMeta reads what a staged file says about itself, in the shape the shelf
// rule wants: who made it and what it is called. Any failure is silence.
//
// For a book, whatever the file does not say is taken from the name it was
// dropped with - "Title - Author (2017).pdf" is the usual shape, and for PDFs
// the filename is often the only real information there is. The staged file
// is called part-123456789, so it is the dropped path that is read, never the
// staged one.
func readMeta(kind media.Kind, staged, rel string) tags.Tags {
	if kind != media.KindEbook {
		return readTags(staged)
	}
	var t tags.Tags
	switch strings.ToLower(path.Ext(rel)) {
	case ".epub":
		if book, err := epub.Open(staged); err == nil {
			t.Title = book.Meta.Title
			if len(book.Meta.Creators) > 0 {
				t.Artist = book.Meta.Creators[0]
			}
			book.Close()
		}
	case ".pdf":
		if m, err := pdf.Open(staged); err == nil && m.Source != "filename" {
			t.Title = m.Title
			if len(m.Authors) > 0 {
				t.Artist = m.Authors[0]
			}
		}
	}
	name := fromFilename(kind, rel)
	if t.Title == "" {
		t.Title = name.Title
	}
	if t.Artist == "" {
		t.Artist = name.Artist
	}
	return t
}

// fromFilename is what a book's file name says about it, for the plan - which
// runs before the bytes arrive - and as readMeta's fallback. Only books: a
// track's file name is "01 Airbag.mp3", which names nothing worth a folder.
func fromFilename(kind media.Kind, rel string) tags.Tags {
	if kind != media.KindEbook {
		return tags.Tags{}
	}
	// Without its extension, whatever it is: FromFilename strips only ".pdf",
	// and an epub's title would otherwise end ".epub".
	base := path.Base(rel)
	m := pdf.FromFilename(strings.TrimSuffix(base, path.Ext(base)))
	t := tags.Tags{Title: m.Title}
	if len(m.Authors) > 0 {
		t.Artist = m.Authors[0]
	}
	return t
}

// taggable reports whether internal/tags can read this file's own metadata.
//
// Deliberately the formats that package handles rather than "is it audio":
// something it cannot read has nothing to say about where it belongs, whatever
// it is.
func taggable(rel string) bool {
	switch strings.ToLower(path.Ext(rel)) {
	case ".mp3", ".m4a", ".m4b", ".mp4", ".flac":
		return true
	}
	return false
}

// dropShape is what the dropped path says about a file: the folder that is
// the album or book, a disc folder inside it if there is one, and the folder
// above it, which may or may not be the artist.
type dropShape struct {
	base  string // the file name
	group string // the album or book folder, "" for a loose file
	disc  string // "CD1", "Disc 2" - kept as a folder inside the group
	above string // the folder above the group, "" if none or a container
}

// discFolder matches the folders a multi-disc rip splits into. Without this a
// CD rip of an audiobook - Title/CD1/01.mp3 - would be filed as a book called
// "CD1".
var discFolder = regexp.MustCompile(`(?i)^(cd|disc|disk)\s*\d+([\s._-]*(of\s*\d+)?)?$`)

// containers are folder names that hold a library rather than name anybody.
// Dropping Libation's "Books" folder put every book under an author called
// "Books", which is how this list started. Compared case-insensitively.
var containers = map[string]bool{
	"books": true, "audiobooks": true, "audio books": true, "audible": true,
	"libation": true, "music": true, "my music": true, "itunes": true,
	"itunes media": true, "media": true, "library": true, "downloads": true,
	"download": true, "albums": true, "artists": true, "mp3": true, "flac": true,
	"new folder": true, "soundstorm media": true,
}

func shapeOf(rel string) dropShape {
	parts := strings.Split(rel, "/")
	s := dropShape{base: parts[len(parts)-1]}
	dirs := parts[:len(parts)-1]
	if n := len(dirs); n >= 2 && discFolder.MatchString(dirs[n-1]) {
		s.disc, dirs = dirs[n-1], dirs[:n-1]
	}
	// A container right above the file - Books/Dune.epub, Music/track.mp3 -
	// is not an album or a book, and a file inside one is as good as loose.
	if n := len(dirs); n >= 1 && !isContainer(dirs[n-1]) {
		s.group = dirs[n-1]
		if n >= 2 && !isContainer(dirs[n-2]) {
			s.above = dirs[n-2]
		}
	}
	return s
}

func isContainer(dir string) bool { return containers[strings.ToLower(strings.TrimSpace(dir))] }

// groupFolder finds where this file's group already landed, so a companion can
// join it instead of guessing.
//
// The group is the album or book folder the drop came in - "The Way of Kings
// [B003ZWFO7E]" - wherever it sat in the drop, and if a taggable file from it
// has already been placed, that folder exists under the shelf beneath whatever
// author the tags named. One match is an answer; none or several is not, and
// the caller falls back to the path.
//
// Directory entries are compared by name rather than matched with
// filepath.Glob, which would be shorter and wrong: every Audible folder is named
// "Title [ASIN]", and to Glob a bracketed run is a character class. "The Way of
// Kings [B003ZWFO7E]" would match a directory called "The Way of Kings B" and
// nothing else.
func groupFolder(kind media.Kind, rel, shelf string) (string, bool) {
	if _, ok := structuredDepth[kind]; !ok {
		return "", false
	}
	s := shapeOf(rel)
	if s.group == "" {
		// A loose file belongs to no group and so has nothing to join.
		return "", false
	}
	entries, err := os.ReadDir(shelf)
	if err != nil {
		return "", false
	}

	var found string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Stat rather than ReadDir per candidate: a shelf can hold hundreds of
		// authors and this runs on every companion upload.
		info, err := os.Stat(filepath.Join(shelf, e.Name(), s.group))
		if err != nil || !info.IsDir() {
			continue
		}
		if found != "" {
			// The same book filed under two authors already. Joining one of
			// them at random would make that permanent.
			return "", false
		}
		found = e.Name()
	}
	if found == "" {
		return "", false
	}
	return joinShelf(found, s.group, s.disc, s.base), true
}

// shelvePath applies the layout rule. Split out so that Plan can run it with
// no tags at all - it has only the path at that point - and Save can run it
// again with the file's own. Where they differ, Save is better informed.
//
// The shelf is always exactly Artist/Album or Author/Title, whatever shape the
// drop had:
//
//   - **The album or book folder keeps the name it was dropped with**, when it
//     had one. "Atomic Habits [1524779261]" is a better folder than the tag's
//     "Atomic Habits (Unabridged)", and the bracketed ASIN keeps two editions
//     apart. Only a loose file takes its album from the tags.
//   - **The artist or author folder comes from the tags.** This reversed an
//     earlier rule that trusted any drop already two folders deep, which filed
//     a drop of Libation's "Books" folder as ninety-four books by an author
//     called "Books". Whatever sits above the book in a drop is at least as
//     often a container as a name; the tags said the right thing for all 112
//     books in that drop.
//   - Everything else in the drop - "Music/Rock/" in front of an artist - is
//     discarded, and a disc folder is kept inside the album.
//
// Music and audiobooks differ in one place. An audiobook's artist tag is its
// author on every part. A track's artist tag is the performer, and differs
// across a compilation or a featured guest, so for music the album artist tag
// comes first, then the folder the album was dropped in, and only then the
// track's artist - or a compilation without an album-artist tag would be
// scattered across a folder per guest.
func shelvePath(kind media.Kind, rel string, t tags.Tags) string {
	if _, ok := structuredDepth[kind]; !ok {
		return rel
	}
	s := shapeOf(rel)

	var who string
	switch kind {
	case media.KindMusic:
		who = firstOf(t.AlbumArtist, s.above, t.Artist)
	case media.KindEbook:
		// The folder above first, like music: a Calibre library's author
		// folders are curated, and the name a book carries inside it is
		// often an author-sort form or a publisher's mistake.
		who = firstOf(s.above, t.Folder())
	default:
		who = firstOf(t.Folder(), s.above)
	}

	album := s.group
	if album == "" {
		album = t.Album
		if kind != media.KindMusic && album == "" {
			// Audiobookshelf reads the album tag as the book, which is what
			// every audiobook ripper writes - but a single-file book often
			// carries only a title.
			album = t.Title
		}
	}

	// Placeholders when nothing is known, rather than leaving the file loose.
	// A file that says nothing about itself is exactly the one that needs
	// somewhere obvious to be found and fixed.
	first, second := "Unknown Artist", "Unknown Album"
	if kind != media.KindMusic {
		first, second = "Unknown Author", "Unknown Title"
	}
	rebuilt, err := cleanRelPath(joinShelf(tagSegment(who, first), tagSegment(album, second), s.disc, s.base))
	if err != nil {
		// A name that cannot survive being a path is not worth failing an
		// upload over; where it was asked to go is safe.
		return rel
	}
	return rebuilt
}

func firstOf(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func joinShelf(who, group, disc, base string) string {
	if disc != "" {
		return who + "/" + group + "/" + disc + "/" + base
	}
	return who + "/" + group + "/" + base
}

// diskFull is how little has to be left before a failed write is reported as a
// full disk rather than as itself. An audiobook is routinely hundreds of
// megabytes, so anything under this is full for the purpose at hand.
const diskFull = 64 << 20

// receiveError explains a failed write, because the raw error does not.
//
// Ninety-one uploads failed with "write /library/.uploads/part-753272949:
// input/output error" while the host's drive sat at exactly 0 bytes free. EIO
// is what Docker Desktop's file sharing reports for a bind mount with no room
// left - it reads like corruption or a permissions problem, and sends somebody
// looking in either of those directions instead of at df.
//
// The free-space figure is the honest part: it is measured rather than inferred
// from an errno, so it says nothing when it cannot tell (see space_other.go).
func receiveError(dir string, err error) error {
	free, known := freeSpace(dir)
	switch {
	case known && free < diskFull:
		return fmt.Errorf("the library disk is full - %s free: %w", humanBytes(free), err)
	case known:
		return fmt.Errorf("receive file: %w (the library disk has %s free)", err, humanBytes(free))
	}
	return fmt.Errorf("receive file: %w", err)
}

// FreeSpace reports the room left in the library, for the UI to warn with
// before a drop starts rather than after it has failed 91 times.
func (l *Library) FreeSpace() (uint64, bool) { return freeSpace(l.root) }

// humanBytes is for a person reading an error message, so it rounds.
func humanBytes(n uint64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
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
