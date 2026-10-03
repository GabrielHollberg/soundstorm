// Package voices makes an audiobook from an ebook: its chapters read aloud by
// Kokoro, an open-source voice that runs on the box (Apache 2.0, a container
// of its own, run unmodified, as every backend is). Nothing leaves the house.
// See "Making an audiobook from an ebook" in CLAUDE.md.
package voices

import (
	"bytes"
	"encoding/xml"
	"html"
	"io"
	"regexp"
	"strings"

	"github.com/GabrielHollberg/soundstorm/internal/epub"
)

// Chapter is one part of a book to read aloud.
type Chapter struct {
	Title string
	Text  string
}

// minWords is the least a spine document must hold to be read: a cover, a
// title page or a copyright line is not worth a file of its own.
const minWords = 40

var space = regexp.MustCompile(`\s+`)

// Chapters reads an EPUB's text in reading order, a chapter per spine
// document worth reading, titled by its first heading.
func Chapters(path string) (title, author string, chapters []Chapter, err error) {
	b, err := epub.Open(path)
	if err != nil {
		return "", "", nil, err
	}
	defer b.Close()
	title, author = b.Meta.Title, ""
	if len(b.Meta.Creators) > 0 {
		author = b.Meta.Creators[0]
	}
	for i, r := range b.Spine {
		if !strings.Contains(r.MediaType, "html") {
			continue
		}
		data, _, err := b.Resource(r.Href)
		if err != nil {
			continue
		}
		heading, docTitle, text := documentText(data)
		if len(strings.Fields(text)) < minWords {
			continue
		}
		// A book that names its chapters only in each page's <title> (the
		// starter book, from Wikisource) - unless, at its start, that is the
		// book's own name, which is a title page's, not a chapter's.
		if heading == "" && docTitle != "" && (len(chapters) > 1 || !strings.EqualFold(docTitle, title)) {
			heading = docTitle
		}
		if heading == "" {
			heading = "Part " + itoa(i+1)
		}
		chapters = append(chapters, Chapter{Title: heading, Text: text})
	}
	return title, author, chapters, nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// blocks end a paragraph: a pause in the reading.
var blocks = map[string]bool{
	"p": true, "div": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"li": true, "blockquote": true, "br": true, "tr": true, "section": true, "dd": true, "dt": true,
}

// skip holds what is never read aloud.
var skip = map[string]bool{"script": true, "style": true, "head": true, "rt": true, "aside": true, "nav": true}

// documentText is a chapter's first heading and its text, a paragraph per
// line. Read with a forgiving tokenizer: books are messy XHTML.
func documentText(data []byte) (heading, docTitle, text string) {
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = false
	d.AutoClose = xml.HTMLAutoClose
	d.Entity = xml.HTMLEntity
	var out strings.Builder
	var para strings.Builder
	var head strings.Builder
	depthSkip := 0
	inHeading := 0
	inTitle := false
	var titleText strings.Builder
	flush := func() {
		p := strings.TrimSpace(space.ReplaceAllString(para.String(), " "))
		para.Reset()
		if p == "" {
			return
		}
		// A paragraph ending without a stop gets one, so the voice pauses.
		if !strings.ContainsAny(p[len(p)-1:], ".!?:;\"'”’)") {
			p += "."
		}
		out.WriteString(p)
		out.WriteString("\n")
	}
	for {
		tok, err := d.Token()
		if err == io.EOF || err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := strings.ToLower(t.Name.Local)
			if name == "title" {
				inTitle = true
			}
			if skip[name] {
				depthSkip++
			}
			if strings.HasPrefix(name, "h") && len(name) == 2 && name[1] >= '1' && name[1] <= '6' {
				inHeading++
			}
			if blocks[name] {
				flush()
			}
		case xml.EndElement:
			name := strings.ToLower(t.Name.Local)
			if name == "title" {
				inTitle = false
			}
			if skip[name] && depthSkip > 0 {
				depthSkip--
			}
			if blocks[name] {
				flush()
			}
			if strings.HasPrefix(name, "h") && len(name) == 2 && name[1] >= '1' && name[1] <= '6' && inHeading > 0 {
				inHeading--
				if head.Len() > 0 && heading == "" {
					heading = strings.TrimSpace(space.ReplaceAllString(head.String(), " "))
				}
				head.Reset()
			}
		case xml.CharData:
			if inTitle {
				titleText.Write(t)
			}
			if depthSkip > 0 {
				continue
			}
			s := html.UnescapeString(string(t))
			para.WriteString(s)
			if inHeading > 0 && heading == "" {
				head.WriteString(s)
			}
		}
	}
	flush()
	if len(heading) > 120 {
		heading = heading[:120]
	}
	docTitle = strings.TrimSpace(space.ReplaceAllString(html.UnescapeString(titleText.String()), " "))
	if len(docTitle) > 120 {
		docTitle = docTitle[:120]
	}
	return heading, docTitle, strings.TrimSpace(out.String())
}

// coverOf is an ebook's cover, for the audiobook's folder.
func coverOf(path string) ([]byte, string) {
	b, err := epub.Open(path)
	if err != nil {
		return nil, ""
	}
	defer b.Close()
	data, ct, err := b.Cover()
	if err != nil {
		return nil, ""
	}
	switch {
	case strings.Contains(ct, "png"):
		return data, ".png"
	case strings.Contains(ct, "jpeg"), strings.Contains(ct, "jpg"):
		return data, ".jpg"
	}
	return nil, ""
}
