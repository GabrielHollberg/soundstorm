package collections

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/media"
)

func played(id string) media.Item {
	return media.Item{ID: id, SourceID: "nd", Kind: media.KindMusic, Title: "Song " + id, Creators: []string{"Band"},
		DurationSeconds: 200, Extra: map[string]string{"album": "LP", "genre": "Rock"}}
}

func TestEveryPlayIsLoggedByYear(t *testing.T) {
	s, _ := Open(t.TempDir())
	_ = s.RecordPlay("u1", played("a"), time.Date(2025, 12, 31, 23, 0, 0, 0, time.UTC))
	_ = s.RecordPlay("u1", played("a"), time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC))
	_ = s.RecordPlay("u1", played("b"), time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC))
	got, err := s.Listens("u1", 2026)
	if err != nil || len(got) != 2 || got[0].ID != "a" || got[1].Title != "Song b" || got[1].Album != "LP" || got[1].Seconds != 200 {
		t.Fatalf("2026 = %+v, %v", got, err)
	}
	if old, _ := s.Listens("u1", 2025); len(old) != 1 {
		t.Errorf("2025 has %d plays", len(old))
	}
	if years := s.ListenYears("u1"); len(years) == 0 || years[0] != 2026 {
		t.Errorf("years = %v", years)
	}
}

func TestADamagedLineIsSkipped(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	_ = s.RecordPlay("u1", played("a"), time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC))
	p := filepath.Join(dir, "listens", "u1-2026.jsonl")
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("{\"at\":\"2026-05-01T10:00:00Z\",\"s\":\"nd\",\"id\":\"b\",\"t\":\"cut sh\n")
	f.Close()
	_ = s.RecordPlay("u1", played("c"), time.Date(2026, 5, 1, 11, 0, 0, 0, time.UTC))
	got, err := s.Listens("u1", 2026)
	if err != nil || len(got) != 2 || got[1].ID != "c" {
		t.Errorf("listens past a half-written line = %+v, %v", got, err)
	}
}

func TestPlaysQueueForAConnectedServiceAndLeaveWhenSent(t *testing.T) {
	s, _ := Open(t.TempDir())
	_ = s.RecordPlay("u1", played("a"), time.Now())
	if sc, ok, _ := s.ScrobblerFor("u1"); ok || len(sc.Pending) != 0 {
		t.Fatal("queued with nothing connected")
	}
	_ = s.SetScrobbler("u1", &Scrobbler{Service: "listenbrainz", Token: "t"})
	_ = s.RecordPlay("u1", played("b"), time.Now())
	_ = s.RecordPlay("u1", played("c"), time.Now())
	sc, _, _ := s.ScrobblerFor("u1")
	if len(sc.Pending) != 2 {
		t.Fatalf("pending = %d", len(sc.Pending))
	}
	_ = s.RecordPlay("u1", played("d"), time.Now()) // arrives while the first two are being sent
	_ = s.Sent("u1", sc.Pending)
	sc, _, _ = s.ScrobblerFor("u1")
	if len(sc.Pending) != 1 || sc.Pending[0].ID != "d" {
		t.Errorf("after sending = %+v", sc.Pending)
	}
}

func TestForgettingSomebodyTakesTheirListens(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	_ = s.RecordPlay("u1", played("a"), time.Now())
	_ = s.Forget("u1")
	if m, _ := filepath.Glob(filepath.Join(dir, "listens", "u1-*")); len(m) != 0 {
		t.Errorf("left %v", m)
	}
}
