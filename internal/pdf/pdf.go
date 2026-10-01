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
	"unicode/utf8"
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

// maxXMP is the largest packet decoded. A real one is a few kilobytes (tens
// with an embedded thumbnail); decoding grows each tiny element into a few
// hundred bytes of structs, so 16MB of empty <Description/>s took 2.6GB.
const maxXMP = 1 << 20

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
	if match == nil || len(match) > maxXMP {
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
	infoRef  = regexp.MustCompile(`/Info\s+(\d+)\s+(\d+)\s+R`)
	infoDate = regexp.MustCompile(`/CreationDate\s*\(D:(\d{4})`)
)

// fromInfoDict reads the trailer's Info dictionary, when it is not compressed.
//
// Only the uncompressed case, because the compressed one needs the whole
// cross-reference machinery this package exists to avoid, and because every
// real PDF measured that hid its Info also had nothing worth finding in it.
//
// The dictionary is found by the trailer's /Info reference rather than by the
// first /Title in the file: in an uncompressed PDF the bookmarks come first,
// and each of them has a /Title too, so a book came out called "Chapter 1".
func fromInfoDict(raw []byte) (Metadata, bool) {
	dict, found := infoObject(raw)
	if !found {
		// No trailer that names one (or the file is cut in the middle and
		// it fell in the gap): the first /Title anywhere is the best guess.
		dict = raw
	}
	var meta Metadata
	if s, ok := dictString(dict, "Title"); ok {
		meta.Title = s
	}
	if a, ok := dictString(dict, "Author"); ok && a != "" {
		meta.Authors = splitAuthors(a)
	}
	if s, ok := dictString(dict, "Subject"); ok {
		meta.Subject = s
	}
	if m := infoDate.FindSubmatch(dict); m != nil {
		meta.Date = string(m[1])
	}
	return meta, meta.Title != ""
}

// infoObject returns the body of the object the trailer names as /Info. found
// is false only when no trailer names one; a reference to an object that is
// not here in plain text (it sits in a compressed object stream) is found but
// empty, which says nothing rather than guessing at a bookmark.
func infoObject(raw []byte) ([]byte, bool) {
	// The last reference wins: an incremental update appends a new trailer,
	// and with it, often, a new Info.
	refs := infoRef.FindAllSubmatch(raw, -1)
	if len(refs) == 0 {
		return nil, false
	}
	ref := refs[len(refs)-1]
	obj := regexp.MustCompile(`(?:^|[^0-9])` + string(ref[1]) + `\s+` + string(ref[2]) + `\s+obj\b`)
	starts := obj.FindAllIndex(raw, -1)
	if len(starts) == 0 {
		return []byte{}, true
	}
	// Likewise the last definition of the object is the current one.
	body := raw[starts[len(starts)-1][1]:]
	if end := bytes.Index(body, []byte("endobj")); end >= 0 {
		body = body[:end]
	}
	return body, true
}

// dictString finds /key in a dictionary and reads the string after it, as a
// literal (...) or a hex <...> string.
func dictString(dict []byte, key string) (string, bool) {
	name := []byte("/" + key)
	for from := 0; ; {
		i := bytes.Index(dict[from:], name)
		if i < 0 {
			return "", false
		}
		at := from + i + len(name)
		from = at
		// /Title must not be the start of /TitleSomething.
		if at < len(dict) && isNameByte(dict[at]) {
			continue
		}
		for at < len(dict) && isSpace(dict[at]) {
			at++
		}
		if at >= len(dict) {
			return "", false
		}
		switch {
		case dict[at] == '(':
			if b, ok := literalString(dict[at+1:]); ok {
				return decodePDFString(b), true
			}
		case dict[at] == '<' && (at+1 >= len(dict) || dict[at+1] != '<'):
			if end := bytes.IndexByte(dict[at+1:], '>'); end >= 0 {
				return pdfText(hexString(dict[at+1 : at+1+end])), true
			}
		}
	}
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f' || c == 0
}

func isNameByte(c byte) bool {
	return !isSpace(c) && !bytes.ContainsRune([]byte("()<>[]{}/%"), rune(c))
}

// literalString returns the still-escaped contents of a literal string whose
// opening parenthesis has been read. Parentheses inside need no escape when
// they balance, so "(Dune (1965))" is all one string.
func literalString(b []byte) ([]byte, bool) {
	depth := 1
	for i := 0; i < len(b); i++ {
		switch b[i] {
		case '\\':
			i++
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return b[:i], true
			}
		}
	}
	return nil, false
}

// hexString decodes <...>: whitespace ignored, an odd last digit taken as
// followed by a 0, as the format says.
func hexString(b []byte) []byte {
	var out []byte
	var hi byte
	half := false
	for _, c := range b {
		var v byte
		switch {
		case c >= '0' && c <= '9':
			v = c - '0'
		case c >= 'a' && c <= 'f':
			v = c - 'a' + 10
		case c >= 'A' && c <= 'F':
			v = c - 'A' + 10
		default:
			continue
		}
		if half {
			out = append(out, hi<<4|v)
		} else {
			hi = v
		}
		half = !half
	}
	if half {
		out = append(out, hi<<4)
	}
	return out
}

// decodePDFString undoes literal-string escaping, then reads the text.
func decodePDFString(in []byte) string {
	var out []byte
	for i := 0; i < len(in); i++ {
		if in[i] != '\\' || i+1 >= len(in) {
			out = append(out, in[i])
			continue
		}
		i++
		switch c := in[i]; c {
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'b', 'f':
			out = append(out, ' ')
		case '\r':
			// A backslash at the end of a line continues the string on the
			// next, and stands for nothing.
			if i+1 < len(in) && in[i+1] == '\n' {
				i++
			}
		case '\n':
		default:
			if c >= '0' && c <= '7' {
				// \ddd, one to three octal digits: how a byte-order mark
				// (\376\377) and anything else unprintable is usually written.
				v := int(c - '0')
				for n := 1; n < 3 && i+1 < len(in) && in[i+1] >= '0' && in[i+1] <= '7'; n++ {
					i++
					v = v*8 + int(in[i]-'0')
				}
				out = append(out, byte(v))
				continue
			}
			out = append(out, c)
		}
	}
	return pdfText(out)
}

// pdfText reads the bytes of a text string: UTF-16BE behind a byte-order mark,
// which is how anything outside Latin-1 gets into a PDF string, UTF-8 behind
// its own mark (PDF 2.0), and otherwise PDFDocEncoding.
func pdfText(b []byte) string {
	switch {
	case len(b) >= 2 && b[0] == 0xFE && b[1] == 0xFF:
		var codes []uint16
		for i := 2; i+1 < len(b); i += 2 {
			codes = append(codes, uint16(b[i])<<8|uint16(b[i+1]))
		}
		return strings.TrimSpace(string(utf16.Decode(codes)))
	case len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF:
		return strings.TrimSpace(strings.ToValidUTF8(string(b[3:]), "FFFD"))
	case utf8.Valid(b):
		// Plain ASCII, or a producer that wrote UTF-8 without saying so -
		// which happens, and read as PDFDocEncoding would turn every accent
		// into two wrong characters.
		return strings.TrimSpace(string(b))
	}
	runes := make([]rune, 0, len(b))
	for _, c := range b {
		if c >= 0x80 && c <= 0xA0 && pdfDocHigh[c-0x80] != 0 {
			runes = append(runes, pdfDocHigh[c-0x80])
		} else {
			runes = append(runes, rune(c)) // the rest is Latin-1
		}
	}
	return strings.TrimSpace(string(runes))
}

// pdfDocHigh is where PDFDocEncoding parts from Latin-1: 0x80 to 0xA0.
var pdfDocHigh = [...]rune{
	0x2022, 0x2020, 0x2021, 0x2026, 0x2014, 0x2013, 0x0192, 0x2044,
	0x2039, 0x203A, 0x2212, 0x2030, 0x201E, 0x201C, 0x201D, 0x2018,
	0x2019, 0x201A, 0x2122, 0xFB01, 0xFB02, 0x0141, 0x0152, 0x0160,
	0x0178, 0x017D, 0x0131, 0x0142, 0x0153, 0x0161, 0x017E, 0xFFFD,
	0x20AC,
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
