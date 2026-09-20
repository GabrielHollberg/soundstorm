package epub

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// A Calibre metadata.opf sidecar, trimmed. This is the exact shape a real
// Calibre library writes next to every book, and reading it is what lets atrium
// serve an existing Calibre library without a SQLite driver.
const calibreSidecar = `<?xml version='1.0' encoding='utf-8'?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
    <dc:identifier opf:scheme="calibre" id="calibre_id">1</dc:identifier>
    <dc:title>A Wizard of Earthsea</dc:title>
    <dc:creator opf:file-as="Le Guin, Ursula K." opf:role="aut">Ursula K. Le Guin</dc:creator>
    <dc:contributor opf:file-as="calibre" opf:role="bkp">calibre (9.15.0)</dc:contributor>
    <dc:date>1968-11-01T00:00:00+00:00</dc:date>
    <dc:language>eng</dc:language>
    <dc:subject>Fantasy</dc:subject>
    <dc:subject>Coming of age</dc:subject>
    <meta name="calibre:series" content="Earthsea Cycle"/>
    <meta name="calibre:series_index" content="1"/>
  </metadata>
</package>`

func TestParseOPFReadsCalibreSidecar(t *testing.T) {
	meta, _, err := ParseOPF([]byte(calibreSidecar), "")
	if err != nil {
		t.Fatalf("ParseOPF: %v", err)
	}

	if meta.Title != "A Wizard of Earthsea" {
		t.Errorf("title = %q", meta.Title)
	}
	if len(meta.Creators) != 1 || meta.Creators[0] != "Ursula K. Le Guin" {
		t.Errorf("creators = %v", meta.Creators)
	}
	if meta.Series != "Earthsea Cycle" || meta.SeriesIndex != "1" {
		t.Errorf("series = %q #%q", meta.Series, meta.SeriesIndex)
	}
	if got := meta.Year(); got != 1968 {
		t.Errorf("year = %d, want 1968", got)
	}
	if len(meta.Subjects) != 2 {
		t.Errorf("subjects = %v", meta.Subjects)
	}
}

// Calibre marks its own backup contribution with role="bkp". Listing it as an
// author would put "calibre (9.15.0)" on the result card.
func TestParseOPFKeepsOnlyAuthors(t *testing.T) {
	meta, _, err := ParseOPF([]byte(calibreSidecar), "")
	if err != nil {
		t.Fatalf("ParseOPF: %v", err)
	}
	for _, c := range meta.Creators {
		if c == "calibre (9.15.0)" {
			t.Error("a non-author contributor was listed as a creator")
		}
	}
}

// Calibre writes this for books with no publication date. Reported as-is it
// labels every such book "year 101"; the guard has to turn it into no year.
func TestYearRejectsCalibresPlaceholderDate(t *testing.T) {
	cases := map[string]int{
		"0101-01-01T00:00:00+00:00": 0,
		"1968-11-01T00:00:00+00:00": 1968,
		"2021":                      2021,
		"":                          0,
		"not-a-date":                0,
		"9999-01-01":                0,
	}
	for date, want := range cases {
		if got := (Metadata{Date: date}).Year(); got != want {
			t.Errorf("Year(%q) = %d, want %d", date, got, want)
		}
	}
}

func TestParseOPFFindsCoverBothWays(t *testing.T) {
	epub2 := `<package xmlns="http://www.idpf.org/2007/opf">
	  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
	    <dc:title>Two</dc:title>
	    <meta name="cover" content="cover-img"/>
	  </metadata>
	  <manifest>
	    <item id="cover-img" href="images/front.jpeg" media-type="image/jpeg"/>
	  </manifest>
	</package>`
	meta, _, err := ParseOPF([]byte(epub2), "OEBPS")
	if err != nil {
		t.Fatalf("epub2: %v", err)
	}
	// The href must be resolved against the OPF's own directory, or the zip
	// lookup misses.
	if meta.CoverHref != "OEBPS/images/front.jpeg" {
		t.Errorf("epub2 cover = %q", meta.CoverHref)
	}

	epub3 := `<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
	  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Three</dc:title></metadata>
	  <manifest>
	    <item id="c" href="cover.png" media-type="image/png" properties="cover-image"/>
	  </manifest>
	</package>`
	meta, _, err = ParseOPF([]byte(epub3), "")
	if err != nil {
		t.Fatalf("epub3: %v", err)
	}
	if meta.CoverHref != "cover.png" {
		t.Errorf("epub3 cover = %q", meta.CoverHref)
	}
}

func TestParseOPFRejectsUntitled(t *testing.T) {
	_, _, err := ParseOPF([]byte(`<package><metadata/></package>`), "")
	if err == nil {
		t.Error("an OPF with no title should be an error, not an untitled book")
	}
}

// writeEPUB builds a minimal but structurally valid EPUB on disk.
func writeEPUB(t *testing.T, opfDir string) string {
	t.Helper()

	opfPath := "content.opf"
	if opfDir != "" {
		opfPath = opfDir + "/content.opf"
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name, body string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}

	add("mimetype", "application/epub+zip")
	add("META-INF/container.xml", `<?xml version="1.0"?>
	<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
	  <rootfiles><rootfile full-path="`+opfPath+`" media-type="application/oebps-package+xml"/></rootfiles>
	</container>`)
	add(opfPath, `<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
	  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
	    <dc:title>The Dispossessed</dc:title>
	    <dc:creator>Ursula K. Le Guin</dc:creator>
	    <dc:language>en</dc:language>
	    <dc:date>1974</dc:date>
	  </metadata>
	  <manifest>
	    <item id="c" href="cover.png" media-type="image/png" properties="cover-image"/>
	    <item id="ch1" href="ch1.xhtml" media-type="application/xhtml+xml"/>
	    <item id="ch2" href="a%20space.xhtml" media-type="application/xhtml+xml"/>
	  </manifest>
	  <spine><itemref idref="ch1"/><itemref idref="ch2"/></spine>
	</package>`)

	prefix := ""
	if opfDir != "" {
		prefix = opfDir + "/"
	}
	add(prefix+"cover.png", "PNGDATA")
	add(prefix+"ch1.xhtml", "<html><body>Chapter one</body></html>")
	// A literal space in the entry name, referenced percent-encoded in the OPF.
	add(prefix+"a space.xhtml", "<html><body>Chapter two</body></html>")

	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	path := filepath.Join(t.TempDir(), "book.epub")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write epub: %v", err)
	}
	return path
}

func TestOpenReadsMetadataSpineAndCover(t *testing.T) {
	// Run both with and without a subdirectory: href resolution is the part
	// most likely to be wrong, and a flat EPUB hides the bug.
	for _, dir := range []string{"", "OEBPS"} {
		name := "flat"
		if dir != "" {
			name = "nested"
		}
		t.Run(name, func(t *testing.T) {
			book, err := Open(writeEPUB(t, dir))
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer book.Close()

			if book.Meta.Title != "The Dispossessed" {
				t.Errorf("title = %q", book.Meta.Title)
			}
			if book.Meta.Year() != 1974 {
				t.Errorf("year = %d", book.Meta.Year())
			}
			if len(book.Spine) != 2 {
				t.Fatalf("spine has %d items, want 2", len(book.Spine))
			}

			data, contentType, err := book.Cover()
			if err != nil {
				t.Fatalf("Cover: %v", err)
			}
			if string(data) != "PNGDATA" {
				t.Errorf("cover data = %q", data)
			}
			if contentType != "image/png" {
				t.Errorf("cover type = %q", contentType)
			}

			// The reader fetches spine entries by these paths, so they have to
			// resolve - including the one whose href is percent-encoded but
			// whose zip entry name is not.
			for _, res := range book.Spine {
				if _, _, err := book.Resource(res.Href); err != nil {
					t.Errorf("spine resource %q: %v", res.Href, err)
				}
			}
		})
	}
}

func TestEntriesListsTheContainer(t *testing.T) {
	book, err := Open(writeEPUB(t, "OEBPS"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer book.Close()

	entries := book.Entries()
	byName := map[string]int64{}
	for _, e := range entries {
		byName[e.Name] = e.Size
	}
	for _, want := range []string{"META-INF/container.xml", "OEBPS/content.opf", "OEBPS/ch1.xhtml"} {
		if _, ok := byName[want]; !ok {
			t.Errorf("entries missing %q (got %v)", want, byName)
		}
	}
	if byName["OEBPS/cover.png"] != int64(len("PNGDATA")) {
		t.Errorf("cover size = %d, want %d", byName["OEBPS/cover.png"], len("PNGDATA"))
	}
}

func TestOpenRejectsNonEPUB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not.epub")
	if err := os.WriteFile(path, []byte("this is not a zip"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Error("want an error for a file that is not a zip")
	}
}
