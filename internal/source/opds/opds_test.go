package opds

import (
	"context"
	"testing"
)

// A download or cover reference is the id a client sends, fetched with the
// server's admin credential attached. It must stay inside the catalog: a
// member cannot use it to reach the server's admin pages, or to add a query of
// their own to a page it does reach.
func TestTargetsStayInsideTheCatalog(t *testing.T) {
	s, err := New(Config{ID: "calibre", BaseURL: "http://calibre:8083", Username: "admin", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{
		"dl:opds/download/12/epub/",
		"dl:opds/download/12/pdf",
	} {
		if _, err := s.StreamTarget(context.Background(), id); err != nil {
			t.Errorf("%q was refused: %v", id, err)
		}
	}
	if _, err := s.ArtTarget(context.Background(), "art:opds/cover/12"); err != nil {
		t.Errorf("a cover was refused: %v", err)
	}

	for _, id := range []string{
		"dl:admin/view",
		"dl:admin/user/1",
		"dl:opds/download/12/epub?x=y",
		"dl:opds/download/12/epub?",
		"dl:opds/download/12/epub#frag",
		"dl:http://evil/opds/download/1",
		"dl://evil/opds/download/1",
		"dl:me",
	} {
		if _, err := s.StreamTarget(context.Background(), id); err == nil {
			t.Errorf("%q was accepted; it is outside the catalog", id)
		}
	}
	if _, err := s.ArtTarget(context.Background(), "art:admin/logfile"); err == nil {
		t.Error("an art id outside the catalog was accepted")
	}
}
