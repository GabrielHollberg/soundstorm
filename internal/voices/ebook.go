package voices

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/epub"
)

// Making an ebook from an audiobook: the recording written down by Whisper,
// put into an EPUB 3 a chapter a file, every sentence in a span with an id,
// and the moment each is said kept inside the book itself
// (META-INF/soundstorm-timeline.json) - so the book reads along with its
// recording with no sync step, and the knowledge travels with the file.

// TimelineEntry is where the made book's timeline is kept inside it.
const TimelineEntry = "META-INF/soundstorm-timeline.json"

// Moment is one sentence's place on the audiobook's whole timeline, in the
// shape the reader already follows (storyteller.Moment's).
type Moment struct {
	Start float64 `json:"t"`
	End   float64 `json:"e"`
	Href  string  `json:"h"`
}

// MadeTimeline is what a made book carries about its recording.
type MadeTimeline struct {
	// The audiobook's folder on its shelf, so a pair with another recording
	// of the same title does not follow this one's timings.
	AudiobookFolder string   `json:"audiobookFolder"`
	Timeline        []Moment `json:"timeline"`
}

// BookChapter is where a chapter of the recording starts, on its whole
// timeline.
type BookChapter struct {
	Title string  `json:"title"`
	Start float64 `json:"start"`
}

// paragraph breaks: a pause this long between words, or this many words
// reached at a sentence's end.
const (
	paragraphPause = 1.2
	paragraphWords = 160
)

type sentence struct {
	words      []Word
	paragraphs bool // a new paragraph starts with it
}

// BuildEbook makes the EPUB from the words heard, in whole-book time.
func BuildEbook(title, author, folder string, chapters []BookChapter, words []Word, cover []byte, coverExt string) ([]byte, []Moment, error) {
	if len(words) == 0 {
		return nil, nil, fmt.Errorf("nothing was heard in the recording")
	}
	if len(chapters) == 0 || chapters[0].Start > words[0].Start {
		chapters = append([]BookChapter{{Title: "Beginning", Start: 0}}, chapters...)
	}
	// Words into chapters, then sentences, then paragraphs.
	type chapter struct {
		title     string
		sentences []sentence
	}
	var out []chapter
	ci := -1
	var cur *sentence
	inPara := 0
	var last Word
	for i, w := range words {
		for ci+1 < len(chapters) && w.Start >= chapters[ci+1].Start-0.05 {
			ci++
			out = append(out, chapter{title: chapters[ci].Title})
			cur = nil
			inPara = 0
		}
		if ci < 0 {
			ci = 0
			out = append(out, chapter{title: chapters[0].Title})
		}
		ch := &out[len(out)-1]
		if cur == nil {
			newPara := len(ch.sentences) == 0 || (i > 0 && w.Start-last.End >= paragraphPause) || inPara >= paragraphWords
			if newPara {
				inPara = 0
			}
			ch.sentences = append(ch.sentences, sentence{paragraphs: newPara})
			cur = &ch.sentences[len(ch.sentences)-1]
		}
		cur.words = append(cur.words, w)
		inPara++
		last = w
		if endsSentence(w.Text) {
			cur = nil
		}
	}

	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	// The mimetype first and stored, as EPUB requires.
	mt, err := z.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	if err != nil {
		return nil, nil, err
	}
	mt.Write([]byte("application/epub+zip"))
	put := func(name, body string) error {
		w, err := z.Create(name)
		if err != nil {
			return err
		}
		_, err = w.Write([]byte(body))
		return err
	}
	if err := put("META-INF/container.xml", `<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
<rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>
`); err != nil {
		return nil, nil, err
	}

	var moments []Moment
	var manifest, spine, nav strings.Builder
	sid := 0
	for n, ch := range out {
		if len(ch.sentences) == 0 {
			continue
		}
		file := fmt.Sprintf("c%03d.xhtml", n+1)
		var body strings.Builder
		body.WriteString("<h2>" + html.EscapeString(ch.title) + "</h2>\n")
		open := false
		for _, s := range ch.sentences {
			if s.paragraphs {
				if open {
					body.WriteString("</p>\n")
				}
				body.WriteString("<p>")
				open = true
			} else {
				body.WriteString(" ")
			}
			sid++
			id := fmt.Sprintf("s%d", sid)
			texts := make([]string, len(s.words))
			for i, w := range s.words {
				texts[i] = w.Text
			}
			fmt.Fprintf(&body, `<span id="%s">%s</span>`, id, html.EscapeString(strings.Join(texts, " ")))
			moments = append(moments, Moment{
				Start: round3(s.words[0].Start), End: round3(s.words[len(s.words)-1].End),
				Href: "OEBPS/" + file + "#" + id,
			})
		}
		if open {
			body.WriteString("</p>\n")
		}
		page := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml" xml:lang="en" lang="en">
<head><title>` + html.EscapeString(ch.title) + `</title></head>
<body>
` + body.String() + `</body>
</html>
`
		if err := put("OEBPS/"+file, page); err != nil {
			return nil, nil, err
		}
		fmt.Fprintf(&manifest, `<item id="c%d" href="%s" media-type="application/xhtml+xml"/>`+"\n", n+1, file)
		fmt.Fprintf(&spine, `<itemref idref="c%d"/>`+"\n", n+1)
		fmt.Fprintf(&nav, `<li><a href="%s">%s</a></li>`+"\n", file, html.EscapeString(ch.title))
	}
	if err := put("OEBPS/nav.xhtml", `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops" xml:lang="en" lang="en">
<head><title>Contents</title></head>
<body><nav epub:type="toc"><h1>Contents</h1><ol>
`+nav.String()+`</ol></nav></body>
</html>
`); err != nil {
		return nil, nil, err
	}
	coverItem, coverMeta := "", ""
	if len(cover) > 0 {
		mtype := "image/jpeg"
		if coverExt == ".png" {
			mtype = "image/png"
		}
		w, err := z.Create("OEBPS/cover" + coverExt)
		if err != nil {
			return nil, nil, err
		}
		w.Write(cover)
		coverItem = fmt.Sprintf(`<item id="cover" href="cover%s" media-type="%s" properties="cover-image"/>`+"\n", coverExt, mtype)
		coverMeta = `<meta name="cover" content="cover"/>` + "\n"
	}
	madeTitle := title + " (from the audiobook)"
	opf := `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="id">
<metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
<dc:identifier id="id">soundstorm-made-` + fmt.Sprint(time.Now().UnixNano()) + `</dc:identifier>
<dc:title>` + html.EscapeString(madeTitle) + `</dc:title>
<dc:creator>` + html.EscapeString(author) + `</dc:creator>
<dc:language>en</dc:language>
<dc:description>Written down from the audiobook by SoundStorm, without the original's formatting.</dc:description>
<meta property="dcterms:modified">` + time.Now().UTC().Format("2006-01-02T15:04:05Z") + `</meta>
` + coverMeta + `</metadata>
<manifest>
<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
` + coverItem + manifest.String() + `</manifest>
<spine>
` + spine.String() + `</spine>
</package>
`
	if err := put("OEBPS/content.opf", opf); err != nil {
		return nil, nil, err
	}
	tl, _ := json.Marshal(MadeTimeline{AudiobookFolder: folder, Timeline: moments})
	if err := put(TimelineEntry, string(tl)); err != nil {
		return nil, nil, err
	}
	if err := z.Close(); err != nil {
		return nil, nil, err
	}
	return buf.Bytes(), moments, nil
}

func endsSentence(w string) bool {
	w = strings.TrimRight(w, `"'”’)]`)
	return strings.HasSuffix(w, ".") || strings.HasSuffix(w, "!") || strings.HasSuffix(w, "?")
}

func round3(f float64) float64 { return float64(int64(f*1000+0.5)) / 1000 }

// ReadMadeTimeline reads the timeline a made book carries, if it is one -
// through internal/epub, whose guards against crafted archives apply, since
// any ebook on the shelf is asked.
func ReadMadeTimeline(path string) (MadeTimeline, bool) {
	b, err := epub.Open(path)
	if err != nil {
		return MadeTimeline{}, false
	}
	defer b.Close()
	data, _, err := b.Resource(TimelineEntry)
	if err != nil {
		return MadeTimeline{}, false
	}
	var t MadeTimeline
	if json.Unmarshal(data, &t) != nil || len(t.Timeline) == 0 {
		return MadeTimeline{}, false
	}
	return t, true
}
