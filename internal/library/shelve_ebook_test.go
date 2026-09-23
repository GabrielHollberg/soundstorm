package library

import (
	"archive/zip"
	"bytes"
	"testing"

	"github.com/GabrielHollberg/soundstorm/internal/media"
)

// mkEpub builds a real, minimal EPUB - a zip with a container pointing at an
// OPF - so the shelf rule is exercised through internal/epub exactly as an
// upload would be.
func mkEpub(t *testing.T, title, creator string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name, body string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(body))
	}
	add("mimetype", "application/epub+zip")
	add("META-INF/container.xml", `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`)
	add("OEBPS/content.opf", `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>`+title+`</dc:title><dc:creator>`+creator+`</dc:creator>
  </metadata>
  <manifest><item id="c" href="c.xhtml" media-type="application/xhtml+xml"/></manifest>
  <spine><itemref idref="c"/></spine>
</package>`)
	add("OEBPS/c.xhtml", "<html><body>text</body></html>")
	zw.Close()
	return buf.Bytes()
}

// Somebody else's loose epub lands beside a Calibre library in the same
// Author/Title shape, named from what the book says inside it.
func TestALooseEbookIsShelvedByItsOwnMetadata(t *testing.T) {
	l := newLibrary(t)
	dest := saved(t, l, media.KindEbook, "dune.epub", mkEpub(t, "Dune", "Frank Herbert"))
	if dest != "ebooks/Frank Herbert/Dune/dune.epub" {
		t.Errorf("filed at %q", dest)
	}
}

// A Calibre library is already Author/Title, with curated author folders; the
// name inside the book is often the sort form. The folder wins, and the
// sidecars stay with their book.
func TestACalibreDropKeepsItsShapeAndSidecars(t *testing.T) {
	l := newLibrary(t)
	group := "Frank Herbert/Dune (42)/"
	book := saved(t, l, media.KindEbook, group+"Dune - Frank Herbert.epub", mkEpub(t, "Dune", "Herbert, Frank"))
	opf := saved(t, l, media.KindEbook, group+"metadata.opf", []byte("<package/>"))
	cover := saved(t, l, media.KindEbook, group+"cover.jpg", []byte("\xff\xd8\xff"))

	for name, got := range map[string]string{
		"book":  book,
		"opf":   opf,
		"cover": cover,
	} {
		want := "ebooks/Frank Herbert/Dune (42)/" + map[string]string{
			"book": "Dune - Frank Herbert.epub", "opf": "metadata.opf", "cover": "cover.jpg",
		}[name]
		if got != want {
			t.Errorf("%s filed at %q, want %q", name, got, want)
		}
	}
}

// Folders that only hold a library are discarded; the book's own metadata
// supplies both levels.
func TestAnEbookInsideContainersIsReShelved(t *testing.T) {
	l := newLibrary(t)
	dest := saved(t, l, media.KindEbook, "Downloads/Books/dune.epub", mkEpub(t, "Dune", "Frank Herbert"))
	if dest != "ebooks/Frank Herbert/Dune/dune.epub" {
		t.Errorf("filed at %q", dest)
	}
}

// A PDF that says nothing about itself is named from the file name it was
// dropped with - not from the staging file it arrived in, which is called
// part-123456789.
func TestAnUntaggedPDFIsShelvedByItsDroppedName(t *testing.T) {
	l := newLibrary(t)
	dest := saved(t, l, media.KindEbook, "Some Paper - Jane Doe (2017).pdf", []byte("%PDF-1.4\n%%EOF\n"))
	if dest != "ebooks/Jane Doe/Some Paper/Some Paper - Jane Doe (2017).pdf" {
		t.Errorf("filed at %q", dest)
	}
}

// A container right above a loose file is not an album, and not a book.
func TestAContainerIsNeverTheAlbumOrBook(t *testing.T) {
	l := newLibrary(t)
	dest := saved(t, l, media.KindMusic, "Music/01 Airbag.mp3",
		id3(map[string]string{"TPE1": "Radiohead", "TALB": "OK Computer"}))
	if dest != "music/Radiohead/OK Computer/01 Airbag.mp3" {
		t.Errorf("filed at %q", dest)
	}
}

// A .pdf is a book on the ebook shelf and a companion on the audiobook one.
// Treating it as self-describing everywhere would let an Audible PDF read its
// own metadata and land under a different author from its book.
func TestWhatDescribesItselfDependsOnTheShelf(t *testing.T) {
	for _, c := range []struct {
		kind media.Kind
		name string
		want bool
	}{
		{media.KindEbook, "book.pdf", true},
		{media.KindEbook, "book.epub", true},
		{media.KindEbook, "cover.jpg", false},
		{media.KindEbook, "metadata.opf", false},
		{media.KindAudiobook, "book.pdf", false},
		{media.KindAudiobook, "book.m4b", true},
		{media.KindMusic, "01.flac", true},
	} {
		if got := describesItself(c.kind, c.name); got != c.want {
			t.Errorf("describesItself(%s, %s) = %v, want %v", c.kind, c.name, got, c.want)
		}
	}
}
