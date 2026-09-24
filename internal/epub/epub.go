// Package epub reads metadata and resources out of EPUB files.
//
// This is the one place SoundStorm reads a media format directly, and the
// reason is worth stating because it looks like a violation of the rule that
// SoundStorm never owns a library: an EPUB is self-describing. The file
// contains its own title, author, language and cover, in a documented XML
// format, inside a zip. A video file does not - "Dune.2021.mkv" needs a scraper
// and a match against TMDB, which is exactly the work Jellyfin exists to do.
//
// So the line is: SoundStorm can own a media type when it is self-describing
// and needs no transcoding. EPUB qualifies. Video never will. Do not use this
// package as precedent for scanning anything else.
//
// The format, briefly:
//
//	book.epub (a zip)
//	├── mimetype                 "application/epub+zip"
//	├── META-INF/container.xml   points at the OPF
//	└── <dir>/content.opf        Dublin Core metadata, manifest, spine
//
// Calibre writes the same OPF format to a metadata.opf sidecar next to each
// book, so ParseOPF handles both and a Calibre library needs no SQLite reader.
package epub

import (
	"archive/zip"
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"io"
	"mime"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
)

// maxResourceBytes caps how much of any single zip entry we will hold in
// memory. Covers are small; a "cover" that is not is a malformed book.
const maxResourceBytes = 16 << 20

// maxMetadataBytes caps the documents that describe a book (container.xml and
// the OPF). A real OPF is kilobytes; a 16MB one of tiny <item>s is a way to make
// the XML decoder allocate far more than the file weighs.
const maxMetadataBytes = 2 << 20

// maxDirectoryBytes caps how much of a zip archive/zip will parse as its central
// directory. Parsing the directory is what zip.OpenReader spends memory on - a
// file struct per entry, about four times the bytes on disk - and it reads
// headers from the directory's start to the end of the file regardless of how
// many entries the archive claims, so the claimed count cannot be trusted and
// the byte span is what has to be bounded. A real book's directory is tens of
// kilobytes: even a heavily illustrated one has a few thousand entries.
const maxDirectoryBytes = 2 << 20

// Metadata is what an OPF document says about a book.
type Metadata struct {
	Title       string
	Creators    []string
	Language    string
	Date        string
	Identifier  string
	Description string
	Subjects    []string
	Series      string
	SeriesIndex string

	// CoverHref is the cover image's path, already resolved relative to the
	// OPF's own directory. Empty when the book declares no cover.
	CoverHref string
}

// Year extracts a plausible publication year, or 0.
//
// Calibre writes "0101-01-01T00:00:00+00:00" for books with no date, so the
// range check is doing real work: it turns a nonsense year into no year.
func (m Metadata) Year() int {
	if len(m.Date) < 4 {
		return 0
	}
	y, err := strconv.Atoi(m.Date[:4])
	if err != nil || y < 1000 || y > 3000 {
		return 0
	}
	return y
}

// Resource is one file inside the book.
type Resource struct {
	Href      string // path inside the container, relative to the OPF
	MediaType string
}

// Book is an opened EPUB. Close it when done.
type Book struct {
	Meta Metadata

	// Spine is the reading order, for a reader to walk.
	Spine []Resource

	zr     *zip.ReadCloser
	opfDir string
}

// --- OPF ---------------------------------------------------------------------

// Element names below are matched on local name only, deliberately: the Dublin
// Core elements are namespaced, but real books disagree about which prefix and
// which namespace URI they use, and Go's xml package matches local names when
// no namespace is given.
type opfPackage struct {
	Metadata opfMetadata `xml:"metadata"`
	Manifest struct {
		Items []opfItem `xml:"item"`
	} `xml:"manifest"`
	Spine struct {
		ItemRefs []struct {
			IDRef string `xml:"idref,attr"`
		} `xml:"itemref"`
	} `xml:"spine"`
}

type opfMetadata struct {
	Titles       []string  `xml:"title"`
	Creators     []opfText `xml:"creator"`
	Languages    []string  `xml:"language"`
	Dates        []string  `xml:"date"`
	Identifiers  []string  `xml:"identifier"`
	Descriptions []string  `xml:"description"`
	Subjects     []string  `xml:"subject"`
	Metas        []opfMeta `xml:"meta"`
}

type opfText struct {
	Value string `xml:",chardata"`
	Role  string `xml:"role,attr"`
}

type opfMeta struct {
	Name     string `xml:"name,attr"`
	Content  string `xml:"content,attr"`
	Property string `xml:"property,attr"`
	Value    string `xml:",chardata"`
}

type opfItem struct {
	ID         string `xml:"id,attr"`
	Href       string `xml:"href,attr"`
	MediaType  string `xml:"media-type,attr"`
	Properties string `xml:"properties,attr"`
}

// ParseOPF reads an OPF document: an EPUB's content.opf, or a Calibre
// metadata.opf sidecar. opfDir is the directory the document sits in, used to
// resolve the cover href; pass "" for a sidecar whose hrefs are already
// relative to the book folder.
func ParseOPF(data []byte, opfDir string) (Metadata, []Resource, error) {
	var pkg opfPackage
	if err := xml.Unmarshal(data, &pkg); err != nil {
		return Metadata{}, nil, fmt.Errorf("parse opf: %w", err)
	}

	md := pkg.Metadata
	meta := Metadata{
		Title:       first(md.Titles),
		Language:    first(md.Languages),
		Date:        first(md.Dates),
		Identifier:  first(md.Identifiers),
		Description: strings.TrimSpace(first(md.Descriptions)),
	}
	for _, c := range md.Creators {
		// role="aut" marks an author; books also list editors, illustrators
		// and translators, which do not belong on a search result card.
		if c.Role != "" && c.Role != "aut" {
			continue
		}
		if v := strings.TrimSpace(c.Value); v != "" {
			meta.Creators = append(meta.Creators, v)
		}
	}
	for _, s := range md.Subjects {
		if v := strings.TrimSpace(s); v != "" {
			meta.Subjects = append(meta.Subjects, v)
		}
	}

	// Series lives in a vendor extension in EPUB2 and a collection in EPUB3.
	var coverID string
	for _, m := range md.Metas {
		switch {
		case m.Name == "calibre:series":
			meta.Series = strings.TrimSpace(m.Content)
		case m.Name == "calibre:series_index":
			meta.SeriesIndex = strings.TrimSpace(m.Content)
		case m.Name == "cover":
			coverID = m.Content
		case m.Property == "belongs-to-collection" && meta.Series == "":
			meta.Series = strings.TrimSpace(m.Value)
		case m.Property == "group-position" && meta.SeriesIndex == "":
			meta.SeriesIndex = strings.TrimSpace(m.Value)
		}
	}

	items := pkg.Manifest.Items
	byID := make(map[string]opfItem, len(items))
	for _, it := range items {
		byID[it.ID] = it
	}

	if href := findCover(items, byID, coverID); href != "" {
		meta.CoverHref = resolve(opfDir, href)
	}

	spine := make([]Resource, 0, len(pkg.Spine.ItemRefs))
	for _, ref := range pkg.Spine.ItemRefs {
		it, ok := byID[ref.IDRef]
		if !ok || it.Href == "" {
			continue
		}
		spine = append(spine, Resource{
			Href:      resolve(opfDir, it.Href),
			MediaType: it.MediaType,
		})
	}

	if meta.Title == "" {
		return meta, spine, fmt.Errorf("opf declares no title")
	}
	return meta, spine, nil
}

// findCover tries the three ways a book can name its cover, newest first.
func findCover(items []opfItem, byID map[string]opfItem, coverID string) string {
	// Every route to a cover requires the item to declare itself an image. The
	// cover is served on this origin by /api/art, so a book that nominated a
	// script or a page as its "cover" would otherwise have it served back with
	// that type - a way for an uploaded book to run code as whoever opens it.
	//
	// EPUB3: an explicit manifest property.
	for _, it := range items {
		if strings.Contains(it.Properties, "cover-image") && it.Href != "" && isImage(it.MediaType) {
			return it.Href
		}
	}
	// EPUB2: <meta name="cover" content="item-id"/>.
	if coverID != "" {
		if it, ok := byID[coverID]; ok && it.Href != "" && isImage(it.MediaType) {
			return it.Href
		}
	}
	// Last resort: an image that calls itself a cover. Plenty of real books
	// declare nothing and rely on the filename.
	for _, it := range items {
		if !isImage(it.MediaType) {
			continue
		}
		if strings.Contains(strings.ToLower(it.ID), "cover") ||
			strings.Contains(strings.ToLower(it.Href), "cover") {
			return it.Href
		}
	}
	return ""
}

// --- reading a file ----------------------------------------------------------

// Open reads a book's metadata and spine. The returned Book holds the zip open;
// Close it.
func Open(filePath string) (*Book, error) {
	// Before archive/zip spends memory on the directory, check its size from the
	// end-of-directory record - a read of at most 64KB. A book is opened on
	// upload, on every scan and on every reader request, so a hostile one must
	// be cheap to refuse every time, not just once.
	if err := checkDirectorySpan(filePath); err != nil {
		return nil, err
	}
	zr, err := zip.OpenReader(filePath)
	if err != nil {
		return nil, fmt.Errorf("open epub: %w", err)
	}

	opfPath, err := rootfilePath(zr)
	if err != nil {
		zr.Close()
		return nil, err
	}

	raw, err := readEntryLimit(zr, opfPath, maxMetadataBytes)
	if err != nil {
		zr.Close()
		return nil, err
	}

	meta, spine, err := ParseOPF(raw, path.Dir(opfPath))
	if err != nil {
		zr.Close()
		return nil, err
	}

	return &Book{Meta: meta, Spine: spine, zr: zr, opfDir: path.Dir(opfPath)}, nil
}

// Close releases the underlying file.
func (b *Book) Close() error { return b.zr.Close() }

// Entry is one file inside the book, as the container stores it.
type Entry struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// Entries lists everything in the container.
//
// A browser-side reader needs this: foliate-js asks for resources by path and
// wants their sizes, and it is cheaper to hand over the whole listing once than
// to answer a stat request per chapter.
func (b *Book) Entries() []Entry {
	out := make([]Entry, 0, len(b.zr.File))
	for _, f := range b.zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		out = append(out, Entry{Name: path.Clean(f.Name), Size: int64(f.UncompressedSize64)})
	}
	return out
}

// Resource returns one file from inside the book, by its container path.
func (b *Book) Resource(href string) ([]byte, string, error) {
	data, err := readEntry(b.zr, href)
	if err != nil {
		return nil, "", err
	}
	return data, contentType(href), nil
}

// Cover returns the cover image bytes and content type.
func (b *Book) Cover() ([]byte, string, error) {
	if b.Meta.CoverHref == "" {
		return nil, "", fmt.Errorf("book declares no cover")
	}
	data, ct, err := b.Resource(b.Meta.CoverHref)
	if err != nil {
		return nil, "", err
	}
	// The declared media type was checked when the cover was chosen; the type it
	// is served with comes from the file name, so check that too. SVG is an
	// image to the manifest but a scriptable document to a browser, so it is
	// not accepted as something served back on this origin.
	if !isImage(ct) || strings.HasPrefix(strings.ToLower(ct), "image/svg") {
		return nil, "", fmt.Errorf("cover %q is not an image (%s)", b.Meta.CoverHref, ct)
	}
	return data, ct, nil
}

// isImage reports whether a media type names an image.
func isImage(mediaType string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(mediaType)), "image/")
}

// rootfilePath reads META-INF/container.xml to find the OPF.
func rootfilePath(zr *zip.ReadCloser) (string, error) {
	raw, err := readEntryLimit(zr, "META-INF/container.xml", maxMetadataBytes)
	if err != nil {
		return "", fmt.Errorf("not an epub (no META-INF/container.xml): %w", err)
	}
	var container struct {
		Rootfiles []struct {
			FullPath string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if err := xml.Unmarshal(raw, &container); err != nil {
		return "", fmt.Errorf("parse container.xml: %w", err)
	}
	for _, rf := range container.Rootfiles {
		if rf.FullPath != "" {
			return path.Clean(rf.FullPath), nil
		}
	}
	return "", fmt.Errorf("container.xml names no rootfile")
}

// checkDirectorySpan refuses a zip whose central directory, as archive/zip would
// parse it, spans more than maxDirectoryBytes. It finds the end-of-directory
// record the way archive/zip does (the last signature whose comment length fits)
// and bounds the region from the earliest place archive/zip could start reading
// the directory to the end of the file. A zip64 archive is refused outright: its
// fields only saturate when an archive is far larger than any book.
func checkDirectorySpan(filePath string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open epub: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("open epub: %w", err)
	}
	size := info.Size()

	const endLen = 22
	tail := int64(endLen + 0xFFFF)
	if tail > size {
		tail = size
	}
	buf := make([]byte, tail)
	if _, err := f.ReadAt(buf, size-tail); err != nil && err != io.EOF {
		return fmt.Errorf("open epub: %w", err)
	}

	for i := len(buf) - endLen; i >= 0; i-- {
		if binary.LittleEndian.Uint32(buf[i:]) != 0x06054b50 {
			continue
		}
		commentLen := int(binary.LittleEndian.Uint16(buf[i+20:]))
		if i+endLen+commentLen > len(buf) {
			continue
		}
		records := binary.LittleEndian.Uint16(buf[i+10:])
		dirSize := int64(binary.LittleEndian.Uint32(buf[i+12:]))
		dirOffset := int64(binary.LittleEndian.Uint32(buf[i+16:]))
		if records == 0xFFFF || dirSize == 0xFFFFFFFF || dirOffset == 0xFFFFFFFF {
			return fmt.Errorf("not an epub: zip64 archives are far larger than any book")
		}
		// archive/zip reads the directory from dirOffset, or from the end record
		// minus the directory's size when the archive has data prepended. Bound
		// the larger of the two spans it could parse.
		endPos := size - tail + int64(i)
		start := dirOffset
		if alt := endPos - dirSize; alt >= 0 && alt < start {
			start = alt
		}
		if start < 0 || size-start > maxDirectoryBytes {
			return fmt.Errorf("not an epub: its directory is larger than any book's")
		}
		return nil
	}
	// No end record: not a zip at all. archive/zip will say so.
	return nil
}

// ReadSidecar reads a Calibre metadata.opf sidecar, refusing one larger than any
// real book's metadata. It sits in a folder uploads can write to and is read on
// every scan, so an unbounded read would let one oversized file cost that much
// memory every couple of minutes.
func ReadSidecar(filePath string) ([]byte, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxMetadataBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxMetadataBytes {
		return nil, fmt.Errorf("sidecar exceeds %d bytes", maxMetadataBytes)
	}
	return data, nil
}

// readEntry pulls one entry out of the zip, tolerating percent-encoded hrefs.
func readEntry(zr *zip.ReadCloser, name string) ([]byte, error) {
	return readEntryLimit(zr, name, maxResourceBytes)
}

// readEntryLimit is readEntry with its own size cap.
func readEntryLimit(zr *zip.ReadCloser, name string, limit int) ([]byte, error) {
	name = path.Clean(strings.TrimPrefix(name, "/"))

	f := lookup(zr, name)
	if f == nil {
		// OPF hrefs may be percent-encoded ("My%20Book.xhtml") while zip entry
		// names are literal. Try the decoded form before giving up.
		if decoded, err := url.PathUnescape(name); err == nil && decoded != name {
			f = lookup(zr, path.Clean(decoded))
		}
	}
	if f == nil {
		return nil, fmt.Errorf("no entry %q in epub", name)
	}

	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("open %q: %w", name, err)
	}
	defer rc.Close()

	data, err := io.ReadAll(io.LimitReader(rc, int64(limit)+1))
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", name, err)
	}
	if len(data) > limit {
		return nil, fmt.Errorf("entry %q exceeds %d bytes", name, limit)
	}
	return data, nil
}

func lookup(zr *zip.ReadCloser, name string) *zip.File {
	for _, f := range zr.File {
		if path.Clean(f.Name) == name {
			return f
		}
	}
	// Some producers disagree about case on the container path.
	for _, f := range zr.File {
		if strings.EqualFold(path.Clean(f.Name), name) {
			return f
		}
	}
	return nil
}

// resolve turns an href relative to the OPF into a container path.
func resolve(opfDir, href string) string {
	href = strings.TrimSpace(href)
	if href == "" {
		return ""
	}
	// Strip any fragment; a cover href should not have one, but spine items do.
	if i := strings.IndexByte(href, '#'); i >= 0 {
		href = href[:i]
	}
	if opfDir == "" || opfDir == "." {
		return path.Clean(href)
	}
	return path.Clean(path.Join(opfDir, href))
}

func contentType(href string) string {
	if ct := mime.TypeByExtension(path.Ext(href)); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

func first(values []string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
