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

// The debounce coalesces one burst into one scan; on its own it does nothing
// against a stream of separate triggers spaced apart on purpose - a script
// hitting the manual rescan button, or two uploads a few seconds apart. Each
// real scan is a whole backend walking its library, and every signed-in
// member can ask for one, so it must not be unboundedly repeatable.
// Both tests below set minRescanInterval well above rescanDelay (a fixed 2s
// constant), on purpose: only then does the floor ever extend a timer past
// its ordinary debounce delay, which is the only case there is anything to
// prove. A floor shorter than rescanDelay is already spent by the time
// anything in these tests could observe it.
func TestRepeatedTriggersAreLimitedToOnceEveryFloor(t *testing.T) {
	old := minRescanInterval
	minRescanInterval = 4 * time.Second
	defer func() { minRescanInterval = old }()

	music := &counter{stub: stub{id: "navidrome", kind: media.KindMusic}}
	h := newHarness(t, music)
	h.signUp(t)

	// The first scan, promptly - through the ordinary debounce, not the floor.
	if resp, _ := h.upload(t, "music", "a.flac", "a"); resp.StatusCode != http.StatusOK {
		t.Fatal("upload failed")
	}
	waitFor(t, 3*time.Second, "the first scan", func() bool { return music.count() >= 1 })

	// A trigger shortly after lands well inside the floor's window, so it
	// must wait for the floor rather than firing after the ordinary debounce.
	time.Sleep(200 * time.Millisecond)
	if resp, _ := h.upload(t, "music", "b.flac", "b"); resp.StatusCode != http.StatusOK {
		t.Fatal("upload failed")
	}
	// Long after the ordinary debounce would have fired, but well short of
	// the floor: still just the one scan.
	time.Sleep(rescanDelay + 500*time.Millisecond)
	if n := music.count(); n != 1 {
		t.Fatalf("a second scan fired before the floor: %d", n)
	}
	waitFor(t, 4*time.Second, "the second scan, once the floor passes", func() bool { return music.count() >= 2 })
}

// A trigger that arrives while a floor-extended timer is already pending must
// not reset it to the ordinary, shorter debounce delay - it has to recompute
// the same floor, or a rapid run of triggers could keep resetting a real scan
// back to two seconds away forever.
func TestAPendingRescanCannotBeShortenedBelowTheFloor(t *testing.T) {
	old := minRescanInterval
	minRescanInterval = 6 * time.Second
	defer func() { minRescanInterval = old }()

	music := &counter{stub: stub{id: "navidrome", kind: media.KindMusic}}
	h := newHarness(t, music)
	h.signUp(t)

	if resp, _ := h.upload(t, "music", "a.flac", "a"); resp.StatusCode != http.StatusOK {
		t.Fatal("upload failed")
	}
	waitFor(t, 3*time.Second, "the first scan", func() bool { return music.count() >= 1 })

	// Starts a floor-extended timer, targeting roughly six seconds from now.
	time.Sleep(300 * time.Millisecond)
	if resp, _ := h.upload(t, "music", "b.flac", "b"); resp.StatusCode != http.StatusOK {
		t.Fatal("upload failed")
	}
	// Lands while that timer is still pending. A version that reset it to
	// the bare rescanDelay would fire around 2.6s after the first scan; the
	// correct one keeps aiming for roughly six.
	time.Sleep(300 * time.Millisecond)
	if resp, _ := h.upload(t, "music", "c.flac", "c"); resp.StatusCode != http.StatusOK {
		t.Fatal("upload failed")
	}

	time.Sleep(3 * time.Second) // well past the buggy target, well short of the real one
	if n := music.count(); n != 1 {
		t.Fatalf("the floor was shortened by a later trigger: %d scans", n)
	}
	waitFor(t, 5*time.Second, "the second scan, once the floor passes", func() bool { return music.count() >= 2 })
}
