package httpapi

import (
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/source"
)

// Importing a playlist from another player - Plexamp by way of Plex, iTunes,
// Jellyfin, anything that writes M3U. The file names songs by where they sat
// on that player's disk and, usually, by an #EXTINF line with the artist,
// title and length. Each is found on the music shelf by its path first
// (the same files, moved: the end of the path matches), then by artist and
// title, the length picking between versions. What cannot be found is
// reported, never guessed at.

// maxImportBody bounds an uploaded playlist: a full one (5,000 songs) of long
// paths is well under this.
const maxImportBody = 4 << 20

// maxMissingReported is how many unfound songs the answer names.
const maxMissingReported = 500

// m3uEntry is one song a playlist file names.
type m3uEntry struct {
	Location string  // as written
	Artist   string  // from #EXTINF, when it had one
	Title    string  // likewise
	Seconds  float64 // likewise; 0 when unknown
}

// label is how an unfound entry is reported: its artist and title if the
// file gave them, else its file name.
func (e m3uEntry) label() string {
	switch {
	case e.Artist != "" && e.Title != "":
		return e.Artist + " - " + e.Title
	case e.Title != "":
		return e.Title
	}
	return path.Base(normalizeLocation(e.Location))
}

// parseM3U reads a playlist file: #EXTM3U or plain, any line ending. The
// name comes from #PLAYLIST when the file has one.
func parseM3U(text string) (name string, entries []m3uEntry) {
	text = strings.TrimPrefix(text, string(rune(0xFEFF)))
	var pending m3uEntry
	for _, line := range strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == '\r' }) {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
		case strings.HasPrefix(line, "#EXTINF:"):
			pending = parseExtinf(strings.TrimPrefix(line, "#EXTINF:"))
		case strings.HasPrefix(line, "#PLAYLIST:"):
			name = strings.TrimSpace(strings.TrimPrefix(line, "#PLAYLIST:"))
		case strings.HasPrefix(line, "#"):
		default:
			pending.Location = line
			entries = append(entries, pending)
			pending = m3uEntry{}
		}
	}
	return name, entries
}

// parseExtinf reads "<seconds> [attributes],<Artist> - <Title>".
func parseExtinf(s string) m3uEntry {
	var e m3uEntry
	head, info, ok := strings.Cut(s, ",")
	if !ok {
		return e
	}
	if f := strings.Fields(head); len(f) > 0 {
		if secs, err := strconv.ParseFloat(f[0], 64); err == nil && secs > 0 && !math.IsInf(secs, 0) {
			e.Seconds = secs
		}
	}
	info = strings.TrimSpace(info)
	if artist, title, ok := strings.Cut(info, " - "); ok {
		e.Artist, e.Title = strings.TrimSpace(artist), strings.TrimSpace(title)
	} else {
		e.Title = info
	}
	return e
}

// normalizeLocation turns what a playlist wrote - a file:// URL, a Windows
// path, a relative one - into lowercase "/"-separated segments.
func normalizeLocation(loc string) string {
	if strings.HasPrefix(strings.ToLower(loc), "file:") {
		if u, err := url.Parse(loc); err == nil && u.Path != "" {
			loc = u.Path
		}
	} else if strings.Contains(loc, "%") {
		if d, err := url.PathUnescape(loc); err == nil {
			loc = d
		}
	}
	loc = strings.ReplaceAll(loc, "\\", "/")
	return strings.Trim(strings.ToLower(loc), "/")
}

// songIndex is the music shelf, ready to be matched against.
type songIndex struct {
	bySuffix map[string][]media.Item // the last 1, 2 and 3 path segments
	byTags   map[string][]media.Item // artist and title
	byTitle  map[string][]media.Item // title alone
}

// maxSuffix is how many trailing path segments are compared: artist, album
// and file is as much as two libraries laid out alike have in common.
const maxSuffix = 3

func newSongIndex(songs []source.SongFile) *songIndex {
	ix := &songIndex{bySuffix: map[string][]media.Item{}, byTags: map[string][]media.Item{}, byTitle: map[string][]media.Item{}}
	for _, sf := range songs {
		parts := strings.Split(strings.ToLower(sf.Path), "/")
		for n := 1; n <= maxSuffix && n <= len(parts); n++ {
			key := strings.Join(parts[len(parts)-n:], "/")
			ix.bySuffix[key] = append(ix.bySuffix[key], sf.Item)
		}
		title := matchKey(sf.Item.Title)
		if title == "" {
			continue
		}
		ix.byTitle[title] = append(ix.byTitle[title], sf.Item)
		for _, artist := range sf.Item.Creators {
			key := matchKey(artist) + "|" + title
			ix.byTags[key] = append(ix.byTags[key], sf.Item)
		}
	}
	return ix
}

// find matches one entry, or answers false.
func (ix *songIndex) find(e m3uEntry) (media.Item, bool) {
	// The same file: the longest matching tail of the path that names one
	// song. A lone file name counts only if no other song shares it.
	if loc := normalizeLocation(e.Location); loc != "" && !strings.Contains(loc, "://") {
		parts := strings.Split(loc, "/")
		for n := min(maxSuffix, len(parts)); n >= 1; n-- {
			hits := ix.bySuffix[strings.Join(parts[len(parts)-n:], "/")]
			// A file name alone ("01 Intro.mp3") is weak evidence: it must
			// agree on length when the playlist gives one.
			if len(hits) == 1 && (n > 1 || e.Seconds == 0 || near(hits[0].DurationSeconds, e.Seconds)) {
				return hits[0], true
			} else if len(hits) > 1 {
				if it, ok := closest(hits, e.Seconds); ok {
					return it, true
				}
			}
		}
	}
	// By its tags: from #EXTINF, or read off the path the way a library is
	// laid out (Artist/Album/01 Title.ext).
	artist, title := e.Artist, e.Title
	if title == "" {
		artist, title = tagsFromPath(normalizeLocationKeepCase(e.Location))
	}
	t := matchKey(title)
	if t == "" {
		return media.Item{}, false
	}
	if artist != "" {
		if hits := ix.byTags[matchKey(artist)+"|"+t]; len(hits) > 0 {
			if it, ok := closest(hits, e.Seconds); ok {
				return it, true
			}
			if e.Seconds == 0 || len(hits) == 1 {
				return hits[0], true
			}
		}
	}
	// A title alone only when it names one song, and its length agrees if
	// the file gave one.
	if hits := ix.byTitle[t]; len(hits) == 1 && (e.Seconds == 0 || near(hits[0].DurationSeconds, e.Seconds)) {
		return hits[0], true
	}
	return media.Item{}, false
}

// closest is the candidate nearest the length the playlist gave, if one is
// near it; with no length given, the first.
func closest(hits []media.Item, seconds float64) (media.Item, bool) {
	if seconds == 0 {
		return hits[0], true
	}
	best, bestGap := media.Item{}, math.Inf(1)
	for _, it := range hits {
		if gap := math.Abs(it.DurationSeconds - seconds); gap < bestGap {
			best, bestGap = it, gap
		}
	}
	return best, near(best.DurationSeconds, seconds)
}

// near is two lengths close enough to be one recording: players round, and
// encoders pad a second or two.
func near(a, b float64) bool {
	return a == 0 || math.Abs(a-b) <= 3
}

func normalizeLocationKeepCase(loc string) string {
	if strings.HasPrefix(strings.ToLower(loc), "file:") {
		if u, err := url.Parse(loc); err == nil && u.Path != "" {
			loc = u.Path
		}
	}
	return strings.Trim(strings.ReplaceAll(loc, "\\", "/"), "/")
}

// trackNumber is a leading "01 ", "1-02 - ", "07. " on a file name.
var trackNumber = regexp.MustCompile(`^\d{1,3}([-.]\d{1,3})?\s*[-.]?\s*`)

// tagsFromPath guesses artist and title from Artist/Album/01 Title.ext, or
// "Artist - Title.ext" on its own.
func tagsFromPath(p string) (artist, title string) {
	parts := strings.Split(p, "/")
	file := parts[len(parts)-1]
	if dot := strings.LastIndex(file, "."); dot > 0 {
		file = file[:dot]
	}
	file = trackNumber.ReplaceAllString(file, "")
	if a, t, ok := strings.Cut(file, " - "); ok {
		return a, t
	}
	if len(parts) >= 3 {
		return parts[len(parts)-3], file
	}
	return "", file
}

// matchKey is a name as compared across libraries: case, punctuation,
// accents' spacing and a leading "The" set aside, and a trailing "(feat. ...)"
// or "[Remastered]" dropped, since players disagree about those.
var trailingBracket = regexp.MustCompile(`\s*[\(\[][^\)\]]*[\)\]]\s*$`)

func matchKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	for {
		t := trailingBracket.ReplaceAllString(s, "")
		if t == s || t == "" {
			break
		}
		s = t
	}
	if i := strings.Index(s, " feat. "); i > 0 {
		s = s[:i]
	}
	s = strings.TrimPrefix(s, "the ")
	s = strings.ReplaceAll(s, "&", "and")
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// handleImportPlaylist makes a playlist from an M3U file's text.
func (s *Server) handleImportPlaylist(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var body struct {
		Name string `json:"name"`
		M3U  string `json:"m3u"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "expected a JSON body with name and m3u")
		return
	}
	fileName, entries := parseM3U(body.M3U)
	if len(entries) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "That file has no songs in it.")
		return
	}
	name := strings.TrimSpace(fileName)
	if name == "" {
		name = strings.TrimSpace(body.Name)
	}
	if rs := []rune(name); len(rs) > 100 {
		name = string(rs[:100])
	}
	if name == "" {
		name = "Imported playlist"
	}

	// Every music shelf this account can see that can say where its files are.
	var songs []source.SongFile
	for _, src := range s.reg.All(r.Context()) {
		lister, ok := src.(source.SongFileLister)
		if !ok || src.Kind() != media.KindMusic {
			continue
		}
		files, err := lister.SongFiles(r.Context())
		if err != nil {
			s.log.Warn("playlist import could not list songs", "source", src.ID(), "err", err)
			continue
		}
		songs = append(songs, files...)
	}
	if len(songs) == 0 {
		writeError(w, http.StatusServiceUnavailable, "The music library is not available right now.")
		return
	}
	ix := newSongIndex(songs)
	var found []media.Item
	missing := []string{}
	for _, e := range entries {
		if it, ok := ix.find(e); ok {
			found = append(found, it)
		} else {
			if len(missing) < maxMissingReported {
				missing = append(missing, e.label())
			}
		}
	}
	if len(found) == 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error": "None of these songs are in your library.", "missing": missing, "total": len(entries)})
		return
	}
	p, err := s.collections.ImportPlaylist(user.ID, name, found)
	if err != nil {
		s.collectionsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"playlist":     playlistSummary(p),
		"total":        len(entries),
		"added":        len(p.Items),
		"missing":      missing,
		"missingCount": len(entries) - len(found),
	})
}
