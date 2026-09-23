package httpapi

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/source"
)

// counter is a source that records how many times it was asked to scan.
type counter struct {
	stub
	mu    sync.Mutex
	calls int
	err   error
}

func (c *counter) Rescan(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	return c.err
}

func (c *counter) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// waitFor polls until the condition holds or the deadline passes, so the test
// does not depend on a sleep being long enough on a slow machine.
func waitFor(t *testing.T, within time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// The whole point: a file is searchable in seconds rather than after the
// backend's own sweep, which is a minute for music and two for ebooks.
func TestUploadingAsksTheBackendToScan(t *testing.T) {
	music := &counter{stub: stub{id: "navidrome", kind: media.KindMusic}}
	h := newHarness(t, music)
	h.signUp(t)

	resp, body := h.upload(t, "music", "song.flac", "bytes")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload: %d %s", resp.StatusCode, body)
	}
	waitFor(t, 5*time.Second, "a scan to be requested", func() bool { return music.count() >= 1 })
}

// A dropped folder arrives as one upload per file. Asking Navidrome to scan
// thirty times for one album would be worse than waiting.
func TestManyUploadsCauseOneScan(t *testing.T) {
	music := &counter{stub: stub{id: "navidrome", kind: media.KindMusic}}
	h := newHarness(t, music)
	h.signUp(t)

	for i := 0; i < 12; i++ {
		name := "album/track" + string(rune('a'+i)) + ".flac"
		// Distinct contents: twelve identical files in one album folder are
		// eleven duplicates, and are skipped as such.
		if resp, body := h.upload(t, "music", name, "bytes of "+name); resp.StatusCode != http.StatusOK {
			t.Fatalf("upload %s: %d %s", name, resp.StatusCode, body)
		}
	}

	waitFor(t, 5*time.Second, "the scan to be requested", func() bool { return music.count() >= 1 })
	// And nothing more arrives afterwards.
	time.Sleep(rescanDelay)
	if n := music.count(); n != 1 {
		t.Errorf("twelve uploads asked for %d scans, want 1", n)
	}
}

// Only the shelf that received something is disturbed.
func TestOnlyTheAffectedLibraryIsScanned(t *testing.T) {
	music := &counter{stub: stub{id: "navidrome", kind: media.KindMusic}}
	books := &counter{stub: stub{id: "ebooks", kind: media.KindEbook}}
	h := newHarness(t, music, books)
	h.signUp(t)

	if resp, _ := h.upload(t, "ebook", "book.epub", "bytes"); resp.StatusCode != http.StatusOK {
		t.Fatal("upload failed")
	}
	waitFor(t, 5*time.Second, "the ebook scan", func() bool { return books.count() >= 1 })

	if n := music.count(); n != 0 {
		t.Errorf("uploading a book asked the music library to scan %d times", n)
	}
}

// A backend that will not scan is not a reason to have failed an upload that
// already worked - and its own timer will find the file anyway.
func TestAFailedScanDoesNotFailTheUpload(t *testing.T) {
	music := &counter{
		stub: stub{id: "navidrome", kind: media.KindMusic},
		err:  context.DeadlineExceeded,
	}
	h := newHarness(t, music)
	h.signUp(t)

	resp, body := h.upload(t, "music", "song.flac", "bytes")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the upload was failed by a scan that would not start: %d %s", resp.StatusCode, body)
	}
	waitFor(t, 5*time.Second, "the attempt", func() bool { return music.count() >= 1 })
}

// A source that cannot be told to scan must not break the ones that can.
func TestASourceWithoutRescanIsSkipped(t *testing.T) {
	plain := stub{id: "opds", kind: media.KindEbook}
	books := &counter{stub: stub{id: "ebooks", kind: media.KindEbook}}
	h := newHarness(t, plain, books)
	h.signUp(t)

	if resp, _ := h.upload(t, "ebook", "book.epub", "bytes"); resp.StatusCode != http.StatusOK {
		t.Fatal("upload failed")
	}
	waitFor(t, 5*time.Second, "the scan", func() bool { return books.count() >= 1 })
}

// Rescanner is optional, and the compiler should say so.
var _ source.Rescanner = (*counter)(nil)
