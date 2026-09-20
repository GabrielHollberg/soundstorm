package pdf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writePDF makes a file that starts like a PDF and contains body verbatim.
// Enough for metadata extraction, which never parses PDF structure.
func writePDF(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "book.pdf")
	content := "%PDF-1.7\n" + body + "\n%%EOF\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// XMP nests every value inside rdf:Alt or rdf:Seq, so reading dc:title as a
// plain string yields an empty one. That mistake made five real PDFs look like
// they had no metadata when two of them had plenty.
func TestXMPValuesAreUnwrappedFromRDF(t *testing.T) {
	path := writePDF(t, `<?xpacket begin="" id="W5M0MpCehiHzreSzNTczkc9d"?>
<x:xmpmeta xmlns:x="adobe:ns:meta/">
 <rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
  <rdf:Description rdf:about="" xmlns:dc="http://purl.org/dc/elements/1.1/">
   <dc:title><rdf:Alt><rdf:li xml:lang="x-default">Form W-9 (Rev. March 2024)</rdf:li></rdf:Alt></dc:title>
   <dc:creator><rdf:Seq><rdf:li>Internal Revenue Service</rdf:li><rdf:li>Second Author</rdf:li></rdf:Seq></dc:creator>
   <dc:description><rdf:Alt><rdf:li>Request for Taxpayer Identification Number</rdf:li></rdf:Alt></dc:description>
   <dc:subject><rdf:Bag><rdf:li>tax</rdf:li><rdf:li>forms</rdf:li></rdf:Bag></dc:subject>
   <dc:date><rdf:Seq><rdf:li>2024-03-01</rdf:li></rdf:Seq></dc:date>
  </rdf:Description>
 </rdf:RDF>
</x:xmpmeta>`)

	meta, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if meta.Source != "xmp" {
		t.Errorf("source = %q, want xmp", meta.Source)
	}
	if meta.Title != "Form W-9 (Rev. March 2024)" {
		t.Errorf("title = %q", meta.Title)
	}
	if len(meta.Authors) != 2 || meta.Authors[0] != "Internal Revenue Service" {
		t.Errorf("authors = %v", meta.Authors)
	}
	if len(meta.Keywords) != 2 {
		t.Errorf("keywords = %v", meta.Keywords)
	}
	if meta.Year() != 2024 {
		t.Errorf("year = %d", meta.Year())
	}
}

// pdfTeX writes /Title () - present, and empty. Treating that as metadata gives
// every LaTeX paper a blank title instead of falling through to its filename.
func TestEmptyMetadataFallsThroughToTheFilename(t *testing.T) {
	path := writePDF(t, "trailer\n<< /Info 1 0 R >>\n1 0 obj << /Title () /Author () >> endobj")

	meta, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if meta.Source != "filename" {
		t.Errorf("source = %q: an empty title was treated as real", meta.Source)
	}
	if meta.Title != "" {
		t.Errorf("title = %q, want nothing", meta.Title)
	}
}

func TestInfoDictionaryIsReadWhenItHasSomethingToSay(t *testing.T) {
	path := writePDF(t, `1 0 obj << /Title (The Iliad) /Author (Homer) /CreationDate (D:19990215120000) >> endobj`)

	meta, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if meta.Source != "info" {
		t.Errorf("source = %q, want info", meta.Source)
	}
	if meta.Title != "The Iliad" {
		t.Errorf("title = %q", meta.Title)
	}
	if len(meta.Authors) != 1 || meta.Authors[0] != "Homer" {
		t.Errorf("authors = %v", meta.Authors)
	}
	if meta.Year() != 1999 {
		t.Errorf("year = %d", meta.Year())
	}
}

// Anything outside Latin-1 reaches a PDF string as UTF-16BE behind a BOM.
func TestUTF16StringsAreDecoded(t *testing.T) {
	utf16Title := "\xfe\xff\x00C\x00a\x00f\x00\xe9"
	path := writePDF(t, "1 0 obj << /Title ("+utf16Title+") >> endobj")

	meta, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if meta.Title != "Café" {
		t.Errorf("title = %q, want Café", meta.Title)
	}
}

// For most PDFs the filename is the only thing anyone ever recorded about the
// file, so this is a primary source rather than a fallback.
func TestFilenameParsing(t *testing.T) {
	cases := []struct {
		name    string
		title   string
		authors string
		year    int
	}{
		{"Attention Is All You Need - Vaswani et al (2017).pdf", "Attention Is All You Need", "Vaswani et al", 2017},
		{"The Iliad - Homer.pdf", "The Iliad", "Homer", 0},
		{"A Dummy Document (2006).pdf", "A Dummy Document", "", 2006},
		{"just_a_file_name.pdf", "just a file name", "", 0},
		{"Paper - Smith, Jones.pdf", "Paper", "Smith, Jones", 0},
		{"1706.03762v7.pdf", "1706.03762v7", "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FromFilename(tc.name)
			if got.Title != tc.title {
				t.Errorf("title = %q, want %q", got.Title, tc.title)
			}
			if joined := strings.Join(got.Authors, ", "); joined != tc.authors {
				t.Errorf("authors = %q, want %q", joined, tc.authors)
			}
			if got.Year() != tc.year {
				t.Errorf("year = %d, want %d", got.Year(), tc.year)
			}
		})
	}
}

// A PDF often knows its title but not its author, and the filename usually has
// both - so the two are merged rather than one replacing the other.
func TestMergeFillsGapsWithoutOverwriting(t *testing.T) {
	embedded := Metadata{Title: "The Real Title", Source: "xmp"}
	merged := embedded.Merge(FromFilename("Filename Title - Some Author (1999).pdf"))

	if merged.Title != "The Real Title" {
		t.Errorf("title = %q: the filename overwrote real metadata", merged.Title)
	}
	if len(merged.Authors) != 1 || merged.Authors[0] != "Some Author" {
		t.Errorf("authors = %v: the gap was not filled", merged.Authors)
	}
	if merged.Year() != 1999 {
		t.Errorf("year = %d", merged.Year())
	}
	if merged.Source != "xmp" {
		t.Errorf("source = %q, want the stronger source retained", merged.Source)
	}
}

func TestOpenRejectsSomethingThatIsNotAPDF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notreally.pdf")
	// Exactly what a failed download looks like, and it turned up for real:
	// an HTML error page saved under a .pdf name.
	if err := os.WriteFile(path, []byte("<html><body>404 Not Found</body></html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Error("an HTML error page should not be accepted as a PDF")
	}
}
