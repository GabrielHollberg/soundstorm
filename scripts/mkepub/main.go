// Command mkepub writes a small, valid EPUB for testing.
//
// It exists because the sample library used to be built with Calibre's
// ebook-convert, borrowed from the Calibre-Web container - and that container
// is gone. Rather than reintroduce a 500MB image to produce a few kilobytes of
// test data, this writes the format directly: an EPUB is a zip with two XML
// files and some XHTML, which is the same reason atrium can read one without a
// backend.
//
//	go run ./scripts/mkepub -out book.epub -title "Dune" -author "Frank Herbert"
package main

import (
	"archive/zip"
	"flag"
	"fmt"
	"html"
	"os"
	"strings"
)

func main() {
	out := flag.String("out", "book.epub", "path to write")
	title := flag.String("title", "Untitled", "book title")
	author := flag.String("author", "Unknown", "book author")
	year := flag.String("year", "", "publication year, optional")
	chapters := flag.Int("chapters", 3, "how many chapters to generate")
	flag.Parse()

	if err := write(*out, *title, *author, *year, *chapters); err != nil {
		fmt.Fprintln(os.Stderr, "mkepub:", err)
		os.Exit(1)
	}
	fmt.Println(*out)
}

func write(path, title, author, year string, chapters int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	zw := zip.NewWriter(f)

	// The mimetype entry must come first and be stored uncompressed. Readers
	// that sniff the format by reading the first bytes depend on it.
	mimetype, err := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	if err != nil {
		return err
	}
	if _, err := mimetype.Write([]byte("application/epub+zip")); err != nil {
		return err
	}

	add := func(name, body string) error {
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = w.Write([]byte(body))
		return err
	}

	if err := add("META-INF/container.xml", containerXML); err != nil {
		return err
	}

	var manifest, spine, dates strings.Builder
	for i := 1; i <= chapters; i++ {
		id := fmt.Sprintf("ch%d", i)
		fmt.Fprintf(&manifest,
			"\n    <item id=%q href=\"%s.xhtml\" media-type=\"application/xhtml+xml\"/>", id, id)
		fmt.Fprintf(&spine, "\n    <itemref idref=%q/>", id)

		body := fmt.Sprintf(chapterXHTML, i, i, html.EscapeString(title))
		if err := add(id+".xhtml", body); err != nil {
			return err
		}
	}
	if year != "" {
		fmt.Fprintf(&dates, "\n    <dc:date>%s</dc:date>", html.EscapeString(year))
	}

	if err := add("cover.svg", fmt.Sprintf(coverSVG,
		html.EscapeString(title), html.EscapeString(author))); err != nil {
		return err
	}

	opf := fmt.Sprintf(packageOPF,
		html.EscapeString(title),
		html.EscapeString(author),
		dates.String(),
		manifest.String(),
		spine.String(),
	)
	if err := add("content.opf", opf); err != nil {
		return err
	}

	return zw.Close()
}

const containerXML = `<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`

const packageOPF = `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="bookid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>%s</dc:title>
    <dc:creator>%s</dc:creator>
    <dc:language>en</dc:language>
    <dc:identifier id="bookid">urn:atrium:sample</dc:identifier>%s
  </metadata>
  <manifest>
    <item id="cover" href="cover.svg" media-type="image/svg+xml" properties="cover-image"/>%s
  </manifest>
  <spine>%s
  </spine>
</package>`

const chapterXHTML = `<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml">
<head><title>Chapter %d</title></head>
<body>
  <h1>Chapter %d</h1>
  <p>Placeholder text from a synthetic book generated for testing %s.</p>
  <p>It exists so that a reader has something to paginate, and so that a
  library scan has something to find. There is no story here.</p>
</body>
</html>`

const coverSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 600 900">
  <rect width="600" height="900" fill="#1f242d"/>
  <rect x="24" y="24" width="552" height="852" fill="none" stroke="#6aa8ff" stroke-width="3"/>
  <text x="300" y="380" text-anchor="middle" fill="#e6e9ee"
        font-family="Georgia, serif" font-size="46">%s</text>
  <text x="300" y="700" text-anchor="middle" fill="#949ba7"
        font-family="Georgia, serif" font-size="28">%s</text>
</svg>`
