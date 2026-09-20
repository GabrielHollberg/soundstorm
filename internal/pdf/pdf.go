// Package pdf reads what metadata a PDF is willing to admit to.
//
// It does not parse PDF structure, and that is a deliberate choice rather than
// a shortcut. Doing it properly means the cross-reference table, then cross
// reference *streams*, then object streams, then compressed object resolution -
// several hundred lines before the first title comes out. Measured against five
// real PDFs from five different producers (pdfTeX, Project Gutenberg, Adobe
// Designer, Ghostscript, PDFsam), that machinery would have returned nothing at
// all: every one of them had an Info dictionary that was either absent or
// literally empty, /Title ().
//
// What two of the five did carry was an XMP packet - Dublin Core metadata, in
// plain XML, sitting uncompressed in the file. Finding that needs a byte scan
// and encoding/xml, and it is the same vocabulary internal/epub already speaks.
//
// So: XMP if it is there, the Info dictionary if it happens to be readable, and
// otherwise the filename, which for a great many PDFs is the only thing anybody
// ever told the file about itself.
package pdf

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
)

// scanWindow is how much of each end of a large file is searched.
//
// XMP lives in a metadata stream that producers put near the front or the back,
// and the Info dictionary is referenced from the trailer at the very end. Eight
// megabytes from each end finds both without reading a scanned atlas into
// memory to learn its title.
const scanWindow = 8 << 20

// readWhole is the size below which the file is simply read entirely.
const readWhole = 16 << 20

// Metadata is what a PDF says about itself.
type Metadata struct {
	Title    string
	Authors  []string
	Subject  string
	Keywords []string
	Date     string

	// Source records where the values came from, which is worth surfacing:
	// "xmp", "info" or "filename".
	Source string
}

// Year extracts a plausible publication year, or 0.
func (m Metadata) Year() int {
	for _, candidate := range yearPattern.FindAllString(m.Date, -1) {
		if y, err := strconv.Atoi(candidate); err == nil && y > 1000 && y < 3000 {
			return y
		}
	}
	return 0
}

var yearPattern = regexp.MustCompile(`\d{4}`)

// Open reads a PDF's metadata, falling back to its filename.
//
// It never returns an error for a PDF that simply has nothing to say; only an
// unreadable file is an error. A book with no metadata is still a book.
func Open(path string) (Metadata, error) {
	raw, err := readEnds(path)
	if err != nil {
		return Metadata{}, err
	}
	if !bytes.HasPrefix(raw, []byte("%PDF-")) {
		return Metadata{}, fmt.Errorf("not a pdf: %s", path)
	}

	if meta, ok := fromXMP(raw); ok {
		meta.Source = "xmp"
		return meta, nil
	}
	if meta, ok := fromInfoDict(raw); ok {
		meta.Source = "info"
		return meta, nil
	}
	return Metadata{Source: "filename"}, nil
}

// readEnds returns the file, or its two ends when it is large.
func readEnds(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() <= readWhole {
		return io.ReadAll(f)
	}

	head := make([]byte, scanWindow)
	if _, err := io.ReadFull(f, head); err != nil {
		return nil, err
	}
	tail := make([]byte, scanWindow)
	if _, err := f.ReadAt(tail, info.Size()-scanWindow); err != nil && err != io.EOF {
		return nil, err
	}
	// A separator so a pattern cannot match across the gap and invent a value
	// out of two unrelated halves of the file.
	return append(append(head, '\n'), tail...), nil
}

// --- XMP ---------------------------------------------------------------------

var xmpPattern = regexp.MustCompile(`(?s)<x:xmpmeta.*?</x:xmpmeta>`)

// xmpPacket is the Dublin Core subset, as XMP nests it.
//
// XMP wraps every value in rdf:Alt or rdf:Seq containing rdf:li, so the text is
// two levels down from the tag. Reading dc:title as a plain string yields an
// empty one, which is a convincing way to conclude a file has no metadata when
// it has plenty.
type xmpPacket struct {
	Description []struct {
		Title    xmpValue `xml:"title"`
		Creator  xmpValue `xml:"creator"`
		Subject  xmpValue `xml:"subject"`
		Desc     xmpValue `xml:"description"`
		Date     xmpValue `xml:"date"`
		CreateAt string   `xml:"CreateDate"`
	} `xml:"RDF>Description"`
}

type xmpValue struct {
	Items []string `xml:"Alt>li"`
	Seq   []string `xml:"Seq>li"`
	Bag   []string `xml:"Bag>li"`
	Plain string   `xml:",chardata"`
}

// all returns whichever container the producer happened to use.
func (v xmpValue) all() []string {
	for _, set := range [][]string{v.Items, v.Seq, v.Bag} {
		if len(set) > 0 {
			return clean(set)
		}
	}
	if s := strings.TrimSpace(v.Plain); s != "" {
		return []string{s}
	}
	return nil
}

func (v xmpValue) first() string {
	if all := v.all(); len(all) > 0 {
		return all[0]
	}
	return ""
}

func fromXMP(raw []byte) (Metadata, bool) {
	match := xmpPattern.Find(raw)
	if match == nil {
		return Metadata{}, false
	}

	var packet xmpPacket
	if err := xml.Unmarshal(match, &packet); err != nil {
		return Metadata{}, false
	}

	var meta Metadata
	for _, d := range packet.Description {
		if meta.Title == "" {
			meta.Title = d.Title.first()
		}
		if len(meta.Authors) == 0 {
			meta.Authors = d.Creator.all()
		}
		if meta.Subject == "" {
			meta.Subject = d.Desc.first()
		}
		if len(meta.Keywords) == 0 {
			meta.Keywords = d.Subject.all()
		}
		if meta.Date == "" {
			meta.Date = firstNonEmpty(d.Date.first(), d.CreateAt)
		}
	}

	// An XMP packet with no title is no more use than none at all, and plenty
	// of producers write an empty one.
	if meta.Title == "" {
		return Metadata{}, false
	}
	return meta, true
}

// --- Info dictionary ---------------------------------------------------------

var (
	infoTitle   = regexp.MustCompile(`/Title\s*\(((?:[^()\\]|\\.)*)\)`)
	infoAuthor  = regexp.MustCompile(`/Author\s*\(((?:[^()\\]|\\.)*)\)`)
	infoSubject = regexp.MustCompile(`/Subject\s*\(((?:[^()\\]|\\.)*)\)`)
	infoDate    = regexp.MustCompile(`/CreationDate\s*\(D:(\d{4})`)
)

// fromInfoDict reads the trailer's Info dictionary, when it is not compressed.
//
// Only the uncompressed case, because the compressed one needs the whole
// cross-reference machinery this package exists to avoid, and because every
// real PDF measured that hid its Info also had nothing worth finding in it.
func fromInfoDict(raw []byte) (Metadata, bool) {
	var meta Metadata
	if m := infoTitle.FindSubmatch(raw); m != nil {
		meta.Title = decodePDFString(m[1])
	}
	if m := infoAuthor.FindSubmatch(raw); m != nil {
		if a := decodePDFString(m[1]); a != "" {
			meta.Authors = splitAuthors(a)
		}
	}
	if m := infoSubject.FindSubmatch(raw); m != nil {
		meta.Subject = decodePDFString(m[1])
	}
	if m := infoDate.FindSubmatch(raw); m != nil {
		meta.Date = string(m[1])
	}
	return meta, meta.Title != ""
}

// decodePDFString undoes PDF string escaping, and UTF-16 when marked.
func decodePDFString(in []byte) string {
	var out []byte
	for i := 0; i < len(in); i++ {
		if in[i] != '\\' || i+1 >= len(in) {
			out = append(out, in[i])
			continue
		}
		i++
		switch in[i] {
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'b', 'f':
			out = append(out, ' ')
		default:
			out = append(out, in[i])
		}
	}

	// A byte-order mark means the rest is UTF-16BE, which is how anything
	// outside Latin-1 gets into a PDF string.
	if len(out) >= 2 && out[0] == 0xFE && out[1] == 0xFF {
		var codes []uint16
		for i := 2; i+1 < len(out); i += 2 {
			codes = append(codes, uint16(out[i])<<8|uint16(out[i+1]))
		}
		return strings.TrimSpace(string(utf16.Decode(codes)))
	}
	return strings.TrimSpace(string(out))
}

// --- filenames ---------------------------------------------------------------

var (
	trailingYear = regexp.MustCompile(`[\s_\-]*[\(\[]?((?:19|20)\d{2})[\)\]]?\s*$`)
	messySpaces  = regexp.MustCompile(`[\s_]+`)
)

// FromFilename makes the best guess a filename allows.
//
// For PDFs this is the primary source rather than a fallback: most of them were
// never told anything about themselves, and whoever saved the file put the only
// real information into its name.
//
// Understood shapes, in order: "Title - Author", "Title (2019)", "Title".
func FromFilename(name string) Metadata {
	base := strings.TrimSuffix(name, ".pdf")
	base = strings.TrimSuffix(base, ".PDF")
	base = messySpaces.ReplaceAllString(base, " ")
	base = strings.TrimSpace(base)

	meta := Metadata{Source: "filename"}

	if m := trailingYear.FindStringSubmatch(base); m != nil {
		meta.Date = m[1]
		base = strings.TrimSpace(trailingYear.ReplaceAllString(base, ""))
	}

	// " - " is the convention people actually use, and the one our own
	// bundled library writes.
	if title, author, found := strings.Cut(base, " - "); found {
		title, author = strings.TrimSpace(title), strings.TrimSpace(author)
		if title != "" && author != "" {
			meta.Title = title
			meta.Authors = splitAuthors(author)
			return meta
		}
	}

	meta.Title = base
	return meta
}

// Merge fills gaps in m from the fallback, so a PDF that knows its title but
// not its author can still take the author from its filename.
func (m Metadata) Merge(fallback Metadata) Metadata {
	if m.Title == "" {
		m.Title = fallback.Title
		if m.Source == "" {
			m.Source = fallback.Source
		}
	}
	if len(m.Authors) == 0 {
		m.Authors = fallback.Authors
	}
	if m.Date == "" {
		m.Date = fallback.Date
	}
	return m
}

func splitAuthors(in string) []string {
	parts := strings.FieldsFunc(in, func(r rune) bool { return r == ',' || r == ';' })
	var out []string
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 && strings.TrimSpace(in) != "" {
		return []string{strings.TrimSpace(in)}
	}
	return out
}

func clean(values []string) []string {
	var out []string
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
