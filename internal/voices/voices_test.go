package voices

import (
	"bytes"
	"strings"
	"testing"

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
