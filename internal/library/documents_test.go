package library

import (
	"reflect"
	"strings"
	"testing"

	"github.com/GabrielHollberg/soundstorm/internal/media"
)

// A loose PDF could be a novel or a gas bill, and nothing in the file says
// which. Guessing wrong costs somebody moving files; asking costs one click.
func TestALoosePDFIsAskedAbout(t *testing.T) {
	l := newLibrary(t)
	questions := ask(l, "Attention Is All You Need.pdf")
	if len(questions) != 1 {
		t.Fatalf("questions = %+v, want one", questions)
	}
	want := []media.Kind{media.KindEbook, media.KindDocument}
	if !reflect.DeepEqual(questions[0].Options, want) {
		t.Errorf("options = %v, want %v", questions[0].Options, want)
	}
}

// Evidence settles it without a question: the folder it was dropped in, a
// Calibre sidecar, an epub beside it.
func TestAPDFWithEvidenceIsNotAskedAbout(t *testing.T) {
	for _, c := range []struct {
		name  string
		paths []string
		want  media.Kind
	}{
		{"a papers folder", []string{"Papers/attention.pdf"}, media.KindDocument},
		{"a manuals folder, deeper", []string{"Home/Manuals/Kitchen/dishwasher.pdf"}, media.KindDocument},
		{"a books folder", []string{"Books/Dune.pdf"}, media.KindEbook},
		{"a Calibre book", []string{"Frank Herbert/Dune (42)/Dune.pdf", "Frank Herbert/Dune (42)/metadata.opf"}, media.KindEbook},
		{"an epub beside it", []string{"Dune/Dune.pdf", "Dune/Dune.epub"}, media.KindEbook},
	} {
		l := newLibrary(t)
		placements, questions := l.Plan(c.paths, nil)
		if len(questions) != 0 {
			t.Errorf("%s: asked %+v", c.name, questions)
			continue
		}
		if placements[0].Kind != c.want {
			t.Errorf("%s: kind %q, want %q", c.name, placements[0].Kind, c.want)
		}
	}
}

// "Paperback Writer" is not a folder of papers. Folder names are matched whole.
func TestPDFEvidenceMatchesWholeFolderNames(t *testing.T) {
	if got := pdfEvidence("Paperback Writer/score.pdf"); got != "" {
		t.Errorf("a folder merely containing \"paper\" was taken as %q", got)
	}
}

// An audiobook's PDF companion must not decide the audiobook's shelf.
func TestAnAudiobookWithAPDFIsStillAnAudiobook(t *testing.T) {
	l := newLibrary(t)
	placements, questions := l.Plan([]string{
		"The Way of Kings [B003ZWFO7E]/The Way of Kings.pdf",
		"The Way of Kings [B003ZWFO7E]/The Way of Kings.m4b",
	}, nil)
	if len(questions) != 0 {
		t.Fatalf("asked %+v", questions)
	}
	for _, p := range placements {
		if p.Kind != media.KindAudiobook {
			t.Errorf("%s -> %q, want audiobook", p.Path, p.Kind)
		}
	}
}

// Documents are kept in the folders they came in: "Taxes/2024" is how
// somebody finds a statement, and documents have no author to file by.
func TestDocumentsKeepTheirFolders(t *testing.T) {
	l := newLibrary(t)
	placements, _ := l.Plan([]string{"Taxes/2024/statement.pdf"}, nil)
	if placements[0].Dest != "documents/Taxes/2024/statement.pdf" {
		t.Errorf("dest = %q", placements[0].Dest)
	}
	dest := saved(t, l, media.KindDocument, "Taxes/2024/statement.pdf", []byte("%PDF-1.4\n%%EOF\n"))
	if dest != "documents/Taxes/2024/statement.pdf" {
		t.Errorf("saved at %q", dest)
	}
}

// Answering the question places the whole drop on the chosen shelf.
func TestAnsweringDocumentPlacesTheDrop(t *testing.T) {
	l := newLibrary(t)
	placements, questions := l.Plan([]string{"scan-0042.pdf"},
		map[string]media.Kind{"scan-0042.pdf": media.KindDocument})
	if len(questions) != 0 {
		t.Fatalf("still asking: %+v", questions)
	}
	if !strings.HasPrefix(placements[0].Dest, "documents/") {
		t.Errorf("dest = %q", placements[0].Dest)
	}
}
