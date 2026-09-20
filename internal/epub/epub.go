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
	"encoding/xml"
	"fmt"
	"io"
	"mime"
	"net/url"
	"path"
	"strconv"
	"strings"
)

// maxResourceBytes caps how much of any single zip entry we will hold in
// memory. Covers are small; a "cover" that is not is a malformed book.
const maxResourceBytes = 16 << 20

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
	// EPUB3: an explicit manifest property.
	for _, it := range items {
		if strings.Contains(it.Properties, "cover-image") && it.Href != "" {
			return it.Href
		}
	}
	// EPUB2: <meta name="cover" content="item-id"/>.
	if coverID != "" {
		if it, ok := byID[coverID]; ok && it.Href != "" {
			return it.Href
		}
	}
	// Last resort: an image that calls itself a cover. Plenty of real books
	// declare nothing and rely on the filename.
	for _, it := range items {
		if !strings.HasPrefix(it.MediaType, "image/") {
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
	zr, err := zip.OpenReader(filePath)
	if err != nil {
		return nil, fmt.Errorf("open epub: %w", err)
	}

	opfPath, err := rootfilePath(zr)
	if err != nil {
		zr.Close()
		return nil, err
	}

	raw, err := readEntry(zr, opfPath)
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
	return b.Resource(b.Meta.CoverHref)
}

// rootfilePath reads META-INF/container.xml to find the OPF.
func rootfilePath(zr *zip.ReadCloser) (string, error) {
	raw, err := readEntry(zr, "META-INF/container.xml")
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

// readEntry pulls one entry out of the zip, tolerating percent-encoded hrefs.
func readEntry(zr *zip.ReadCloser, name string) ([]byte, error) {
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

	data, err := io.ReadAll(io.LimitReader(rc, maxResourceBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", name, err)
	}
	if len(data) > maxResourceBytes {
		return nil, fmt.Errorf("entry %q exceeds %d bytes", name, maxResourceBytes)
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
