package stream

import (
	"bytes"
	"context"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAwayFromHomeIsTheRemoteNames(t *testing.T) {
	for host, want := range map[string]bool{
		"abc123.net.soundstorm.dev:8099": true,
		"abc123.net.soundstorm.dev":      true,
		"box.tail1234.ts.net":            true,
		"abc123.home.soundstorm.dev":     false,
		"192.168.0.19:8099":              false,
		"localhost:8099":                 false,
	} {
		r := httptest.NewRequest("GET", "/api/stream/navidrome/x", nil)
		r.Host = host
		if got := awayFromHome(r); got != want {
			t.Errorf("%s: away=%v, want %v", host, got, want)
		}
	}
}

func TestPaceRateFollowsTheBitrate(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/stream/navidrome/x?kbps=128", nil)
	if got, want := paceRate(r, "audio/mpeg"), paceAhead*128*1000/8; got != want {
		t.Errorf("converted: %v, want %v", got, want)
	}
	plain := httptest.NewRequest("GET", "/api/stream/navidrome/x", nil)
	if got, want := paceRate(plain, "audio/mp4"), paceAhead*320*1000/8; got != want {
		t.Errorf("original: %v, want %v", got, want)
	}
	if got, want := paceRate(plain, "audio/flac"), paceAhead*1411*1000/8; got != want {
		t.Errorf("lossless: %v, want %v", got, want)
	}
	if got := paceRate(plain, "video/mp4"); got != 0 {
		t.Errorf("video is not paced, got %v", got)
	}
}

func TestPacedCopySendsTheBurstThenHoldsThePace(t *testing.T) {
	data := bytes.Repeat([]byte{1}, 200_000)
	var out bytes.Buffer
	start := time.Now()
	// 100KB at once, then 200KB a second: the other 100KB should take ~0.5s.
	n, err := pacedCopy(context.Background(), &out, bytes.NewReader(data), 100_000, 200_000)
	took := time.Since(start)
	if err != nil || n != int64(len(data)) || out.Len() != len(data) {
		t.Fatalf("copied %d of %d, err %v", n, len(data), err)
	}
	if took < 400*time.Millisecond || took > 2*time.Second {
		t.Errorf("took %v, want about 0.5s", took)
	}
}

func TestPacedCopyStopsWhenTheListenerLeaves(t *testing.T) {
	data := bytes.Repeat([]byte{1}, 1_000_000)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	var out bytes.Buffer
	start := time.Now()
	n, err := pacedCopy(ctx, &out, bytes.NewReader(data), 32<<10, 50_000)
	if err == nil || n >= int64(len(data)) {
		t.Fatalf("should have stopped early: n=%d err=%v", n, err)
	}
	if time.Since(start) > time.Second {
		t.Errorf("took %v to notice the listener had gone", time.Since(start))
	}
}
