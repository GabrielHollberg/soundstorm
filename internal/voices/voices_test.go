package voices

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GabrielHollberg/soundstorm/internal/epub"
	"github.com/GabrielHollberg/soundstorm/internal/tags"
)

const starter = "../starter/media/ebooks/George S. Clason/The Richest Man in Babylon/The Richest Man in Babylon - George S. Clason.epub"

// The starter ebook reads as its chapters, in order, titled, with its
// cover page and the like left out.
func TestAnEbookReadsAsItsChapters(t *testing.T) {
	title, author, chapters, err := Chapters(starter)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(title, "Richest Man") || author == "" {
		t.Fatalf("title %q author %q", title, author)
	}
	if len(chapters) < 10 {
		t.Fatalf("%d chapters", len(chapters))
	}
	for _, c := range chapters {
		if c.Title == "" || len(strings.Fields(c.Text)) < minWords || strings.Contains(c.Text, "<") {
			t.Fatalf("chapter %q: %d words, %q", c.Title, len(strings.Fields(c.Text)), c.Text[:min(80, len(c.Text))])
		}
	}
	for _, c := range chapters {
		t.Logf("%-50q %5d words", c.Title, len(strings.Fields(c.Text)))
	}
}

// The tag at the front of each chapter is read back as the audiobook server
// would: the book, its author, the chapter and the AI voice as narrator.
func TestTheChapterTagSaysItIsAnAIVoice(t *testing.T) {
	tag := id3([][2]string{{"TIT2", "Chapter 1 — Gold"}, {"TALB", "The Book"}, {"TPE1", "An Author"}, {"TPE2", "An Author"}, {"TCOM", "AI voice: Heart"}})
	// A frame of silence after it, as an MP3 would have.
	mp3 := append(tag, 0xFF, 0xFB, 0x90, 0x64, 0, 0, 0, 0)
	got, err := tags.Read(bytes.NewReader(append(mp3, make([]byte, 400)...)))
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Chapter 1 — Gold" || got.Album != "The Book" || got.AlbumArtist != "An Author" || got.Narrator != "AI voice: Heart" {
		t.Fatalf("read back %+v", got)
	}
}

func TestAnAudiobookWrittenDownCarriesItsTimeline(t *testing.T) {
	words := []Word{
		{0.0, 0.4, "Chapter"}, {0.4, 0.8, "one."},
		{1.2, 1.5, "The"}, {1.5, 1.9, "keeper"}, {1.9, 2.4, "climbed."},
		{4.0, 4.2, "He"}, {4.2, 4.6, "slept."}, // after a pause: a new paragraph
		{10.0, 10.4, "Chapter"}, {10.4, 10.8, "two."}, {11.0, 11.5, "Morning."},
	}
	chapters := []BookChapter{{"Chapter One", 0}, {"Chapter Two", 10}}
	data, moments, err := BuildEbook("The Lighthouse Keeper", "Ada Test", "Ada Test/The Lighthouse Keeper", chapters, words, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(moments) != 5 {
		t.Fatalf("want a moment a sentence (5), got %d: %+v", len(moments), moments)
	}
	if moments[1].Href != "OEBPS/c001.xhtml#s2" || moments[1].Start != 1.2 || moments[1].End != 2.4 {
		t.Errorf("second sentence: %+v", moments[1])
	}
	if moments[3].Href != "OEBPS/c002.xhtml#s4" || moments[3].Start != 10 {
		t.Errorf("chapter two's first sentence: %+v", moments[3])
	}
	path := filepath.Join(t.TempDir(), "made.epub")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := epub.Open(path)
	if err != nil {
		t.Fatalf("the made book does not open as an EPUB: %v", err)
	}
	if b.Meta.Title != "The Lighthouse Keeper (from the audiobook)" {
		t.Errorf("title %q", b.Meta.Title)
	}
	page, _, err := b.Resource("OEBPS/c001.xhtml")
	b.Close()
	if err != nil || !strings.Contains(string(page), `<span id="s2">The keeper climbed.</span>`) || strings.Count(string(page), "<p>") != 2 {
		t.Errorf("chapter one:\n%s", page)
	}
	tl, ok := ReadMadeTimeline(path)
	if !ok || tl.AudiobookFolder != "Ada Test/The Lighthouse Keeper" || len(tl.Timeline) != 5 {
		t.Errorf("timeline read back: %v %+v", ok, tl)
	}
}

func TestAnMP3IsCutAtItsFrames(t *testing.T) {
	// 128 kbps, 44.1 kHz MPEG-1 layer III: 417 bytes a frame (no padding),
	// 1152 samples. Twenty minutes is about 45,940 frames.
	frame := make([]byte, 417)
	frame[0], frame[1], frame[2] = 0xFF, 0xFB, 0x90
	n := 46000
	data := append([]byte("ID3\x03\x00\x00\x00\x00\x00\x05abcde"), bytes.Repeat(frame, n)...)
	path := filepath.Join(t.TempDir(), "book.mp3")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	var starts []float64
	total := 0
	err := EachPiece(path, func(start float64, piece []byte, name string) error {
		if piece[0] != 0xFF || len(piece)%417 != 0 {
			t.Errorf("piece at %.1f does not start on a frame or ends mid-frame", start)
		}
		starts = append(starts, start)
		total += len(piece)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(starts) != 3 || starts[1] < 599 || starts[1] > 601 || total != n*417 {
		t.Errorf("pieces at %v, %d bytes of %d", starts, total, n*417)
	}
}
