package caretaker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Starting over empties every volume and the caches but keeps the media; an
// erase takes the media too. The folders themselves stay - compose binds them
// and backends mount them - and the snapshots taken before updates go, as
// they hold the accounts.
func TestResetEmptiesWhatItSays(t *testing.T) {
	for _, mode := range []ResetMode{ResetStartOver, ResetErase} {
		b := newBox(t)
		srv := filepath.Join(b.dir, "srv")
		b.u.cfg.Volumes = filepath.Join(srv, "volumes")
		b.u.cfg.Cache = filepath.Join(srv, "cache")
		b.u.cfg.Library = filepath.Join(srv, "library")
		write := func(rel string) {
			p := filepath.Join(srv, rel)
			os.MkdirAll(filepath.Dir(p), 0o755)
			os.WriteFile(p, []byte("x"), 0o644)
		}
		write("volumes/soundstorm-state/state.json")
		write("volumes/navidrome-data/navidrome.db")
		write("cache/jellyfin-cache/x/y")
		write("library/music/Artist/Album/01.mp3")
		write("library/README.txt")
		write("volumes-before-7/soundstorm-state/state.json")

		if err := b.u.Reset(context.Background(), mode); err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		gone := func(rel string) bool { _, err := os.Stat(filepath.Join(srv, rel)); return os.IsNotExist(err) }
		there := func(rel string) bool { return !gone(rel) }
		if !gone("volumes/soundstorm-state/state.json") || !gone("volumes/navidrome-data/navidrome.db") || !gone("cache/jellyfin-cache/x") {
			t.Errorf("%s: accounts or caches left", mode)
		}
		if !there("volumes/soundstorm-state") || !there("cache/jellyfin-cache") || !there("library/music") {
			t.Errorf("%s: a volume or shelf folder went", mode)
		}
		if mode == ResetStartOver && !there("library/music/Artist/Album/01.mp3") {
			t.Error("starting over took the media")
		}
		if mode == ResetErase && (there("library/music/Artist") || there("library/README.txt")) {
			t.Error("an erase left media")
		}
		b.mu.Lock()
		ran := strings.Join(b.ran, "; ")
		b.mu.Unlock()
		if !strings.HasPrefix(ran, "up down") || !strings.HasSuffix(ran, "up") || !strings.Contains(ran, "btrfs subvolume delete "+filepath.Join(srv, "volumes-before-7")) ||
			!strings.Contains(ran, "forget.sh "+string(mode)) {
			t.Errorf("%s ran: %s", mode, ran)
		}
	}
	if err := newBox(t).u.Reset(context.Background(), "everything"); err != ErrBadMode {
		t.Errorf("an unknown mode: %v", err)
	}
}

// Presses come in runs: twice is a shutdown, once nothing, five quickly open
// the owner's password for a while.
func TestThePowerButtonCountsRuns(t *testing.T) {
	press := make(chan struct{}, 16)
	runs := make(chan int, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go countRuns(ctx, press, 50*time.Millisecond, func(n int) { runs <- n })
	for i := 0; i < 5; i++ {
		press <- struct{}{}
		time.Sleep(10 * time.Millisecond)
	}
	if n := <-runs; n != 5 {
		t.Fatalf("five quick presses counted as %d", n)
	}
	press <- struct{}{}
	if n := <-runs; n != 1 {
		t.Fatalf("one press counted as %d", n)
	}

	b := newBox(t)
	b.u.Pressed(ctx, 5)
	if open, _ := b.u.buttonOpen(); !open {
		t.Fatal("five presses did not open the owner's password")
	}
	code := b.u.buttonCode()
	if len(code) != 6 {
		t.Fatalf("the code for the box's screen: %q", code)
	}
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	if b.u.claimButton(wrong) {
		t.Fatal("a wrong code was taken")
	}
	if !b.u.claimButton(code) {
		t.Fatal("the right code was refused")
	}
	if open, _ := b.u.buttonOpen(); open || b.u.claimButton(code) {
		t.Fatal("still open after it was used")
	}
	b.u.Pressed(ctx, 5)
	for range maxWrongCodes {
		b.u.claimButton("guess")
	}
	if open, _ := b.u.buttonOpen(); open {
		t.Fatal("guessing did not close the window")
	}
	b.u.Pressed(ctx, 1)
	b.mu.Lock()
	if len(b.ran) != 0 {
		t.Fatalf("one press ran %v", b.ran)
	}
	b.mu.Unlock()
	b.u.Pressed(ctx, 2)
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.ran) == 0 || b.ran[len(b.ran)-1] != "systemctl poweroff" {
		t.Fatalf("two presses ran %v", b.ran)
	}
}

// With no screen plugged into the box, the sticker's setup code - typed with
// any case, spaces or dashes - does what the screen's code does.
func TestTheStickersCodeOpensTheWindowToo(t *testing.T) {
	b := newBox(t)
	os.WriteFile(filepath.Join(b.dir, ".env"), []byte("SOUNDSTORM_PORT=8099\nSOUNDSTORM_SETUP_CODE=k3x9m2p7qa4d8wze1rty\n"), 0o600)
	if b.u.claimButton("K3X9-M2P7-QA4D-8WZE-1RTY") {
		t.Fatal("the sticker's code worked with no presses")
	}
	b.u.Pressed(context.Background(), 5)
	if b.u.claimButton("k3x9 m2p7 qa4d 8wze 1rtz") {
		t.Fatal("a wrong sticker code was taken")
	}
	if !b.u.claimButton("K3X9-M2P7-QA4D-8WZE-1RTY") {
		t.Fatal("the sticker's code was refused")
	}
	if open, _ := b.u.buttonOpen(); open {
		t.Fatal("still open after the sticker's code")
	}
}

// Erasing asked for through the socket waits for five presses at the box;
// until then nothing is touched, and five presses start it.
func TestErasingWaitsForTheButton(t *testing.T) {
	b := newBox(t)
	srv := filepath.Join(b.dir, "srv")
	b.u.cfg.Volumes = filepath.Join(srv, "volumes")
	b.u.cfg.Cache = filepath.Join(srv, "cache")
	b.u.cfg.Library = filepath.Join(srv, "library")
	song := filepath.Join(srv, "library/music/Artist/01.mp3")
	os.MkdirAll(filepath.Dir(song), 0o755)
	os.WriteFile(song, []byte("x"), 0o644)
	os.MkdirAll(b.u.cfg.Volumes, 0o755)
	os.MkdirAll(b.u.cfg.Cache, 0o755)

	rec := httptest.NewRecorder()
	b.u.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/reset", strings.NewReader(`{"mode":"erase"}`)))
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"waiting":"button"`) {
		t.Fatalf("erase asked: %d %s", rec.Code, rec.Body.String())
	}
	time.Sleep(2500 * time.Millisecond)
	if _, err := os.Stat(song); err != nil {
		t.Fatal("erased with nobody at the box")
	}
	if mode, _ := b.u.resetArmed(); mode != ResetErase {
		t.Fatal("not waiting for the button")
	}
	b.u.Pressed(context.Background(), ResetPresses)
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(song); os.IsNotExist(err) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := os.Stat(song); !os.IsNotExist(err) {
		t.Fatal("five presses did not erase")
	}
	if open, _ := b.u.buttonOpen(); open {
		t.Fatal("the presses that confirmed an erase opened the password too")
	}
}

// A real PC lists more than one device sending the power key; every one is
// watched, a keyboard is not, and an old kernel's list with no key bitmap
// still finds the button by its name.
func TestThePowerButtonsAreFound(t *testing.T) {
	devices := "I: Bus=0019 Vendor=0000 Product=0001\nN: Name=\"Power Button\"\nP: Phys=PNP0C0C/button/input0\n" +
		"H: Handlers=kbd event1 \nB: KEY=10000000000000 0\n\n" +
		"I: Bus=0019 Vendor=0000 Product=0001\nN: Name=\"Power Button\"\nP: Phys=LNXPWRBN/button/input0\n" +
		"H: Handlers=kbd event2 \nB: KEY=10000000000000 0\n\n" +
		"I: Bus=0019\nN: Name=\"Intel HID events\"\nH: Handlers=rfkill kbd event5 \nB: KEY=3f000b00000000 0 10000000000000 0\n\n" +
		"I: Bus=0003\nN: Name=\"USB Keyboard\"\nH: Handlers=sysrq kbd event3 leds\nB: KEY=1000000000007 ff9f207ac14057ff febeffdfffefffff fffffffffffffffe\n"
	got := powerButtons(devices)
	if strings.Join(got, " ") != "/dev/input/event1 /dev/input/event2 /dev/input/event5" {
		t.Fatalf("found %v", got)
	}
	old := "I: Bus=0019\nN: Name=\"Power Button\"\nH: Handlers=kbd event2 \n\nI: Bus=0003\nN: Name=\"Keyboard\"\nH: Handlers=sysrq kbd event3\n"
	if got := powerButtons(old); len(got) != 1 || got[0] != "/dev/input/event2" {
		t.Fatalf("found %v without key bitmaps", got)
	}
}

// The button's code goes to the box's own screen, never to the socket the
// app's container shares, and is gone once the window closes.
func TestTheButtonCodeIsOnlyOnTheBoxsScreen(t *testing.T) {
	b := newBox(t)
	b.u.Pressed(context.Background(), 5)
	data, err := os.ReadFile(filepath.Join(b.u.cfg.StateDir, ButtonCodeFile))
	code := strings.Fields(string(data))
	if err != nil || len(code) != 2 || code[0] != b.u.buttonCode() {
		t.Fatalf("the screen's file: %q %v", data, err)
	}
	rec := httptest.NewRecorder()
	b.u.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/button", nil))
	if strings.Contains(rec.Body.String(), code[0]) || strings.Contains(rec.Body.String(), "code") {
		t.Fatalf("the socket gave the code away: %s", rec.Body.String())
	}
	b.u.claimButton(code[0])
	if _, err := os.Stat(filepath.Join(b.u.cfg.StateDir, ButtonCodeFile)); err == nil {
		t.Fatal("the code stayed on the screen after it was used")
	}
}

// A version reporting more sources than the box ever saw cannot hold every
// update to a number no new version reaches.
func TestAnInflatedHealthIsNotTheBaseline(t *testing.T) {
	b := newBox(t)
	b.u.saveBaseline(9)
	if got := b.u.baseline(); got != 9 {
		t.Fatalf("baseline %d", got)
	}
	b.u.saveBaseline(5000)
	if got := b.u.baseline(); got != maxBaseline {
		t.Fatalf("baseline %d", got)
	}
}

// An erase waiting for the button is said on the box's screen, and taken
// back: the five presses after that open the password, erasing nothing.
func TestAWaitingEraseShowsAndCanBeCancelled(t *testing.T) {
	b := newBox(t)
	srv := filepath.Join(b.dir, "srv")
	b.u.cfg.Volumes = filepath.Join(srv, "volumes")
	b.u.cfg.Cache = filepath.Join(srv, "cache")
	b.u.cfg.Library = filepath.Join(srv, "library")
	song := filepath.Join(srv, "library/music/Artist/01.mp3")
	os.MkdirAll(filepath.Dir(song), 0o755)
	os.WriteFile(song, []byte("x"), 0o644)

	b.u.ArmReset(ResetErase)
	// The five presses a forgotten password takes never erase.
	b.u.Pressed(context.Background(), 5)
	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat(song); err != nil {
		t.Fatal("five presses erased")
	}
	if mode, _ := b.u.resetArmed(); mode != ResetErase {
		t.Fatal("five presses took the waiting erase away")
	}
	b.u.closeButton()
	if _, err := os.Stat(filepath.Join(b.u.cfg.StateDir, EraseFile)); err != nil {
		t.Fatal("the screen is not told an erase is waiting")
	}
	rec := httptest.NewRecorder()
	b.u.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/reset/cancel", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("cancel: %d", rec.Code)
	}
	if _, err := os.Stat(filepath.Join(b.u.cfg.StateDir, EraseFile)); !os.IsNotExist(err) {
		t.Fatal("the screen still says an erase is waiting")
	}
	b.u.Pressed(context.Background(), 5)
	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat(song); err != nil {
		t.Fatal("a cancelled erase erased")
	}
	if open, _ := b.u.buttonOpen(); !open {
		t.Fatal("the presses did not open the password")
	}
}

// A release that has expired since it was found is not installed.
func TestAnExpiredReleaseIsNotInstalled(t *testing.T) {
	b := newBox(t)
	m := release(2)
	m.Expires = time.Now().Add(-time.Minute)
	if err := b.u.Update(context.Background(), m); err == nil {
		t.Fatal("an expired release was installed")
	}
	if b.images() != "" {
		t.Fatal("an expired release changed the images")
	}
}

// Starting over waits for the button too: asked for over the socket alone,
// nothing goes.
func TestStartingOverWaitsForTheButton(t *testing.T) {
	b := newBox(t)
	b.u.cfg.Volumes = filepath.Join(b.dir, "volumes")
	b.u.cfg.Cache = filepath.Join(b.dir, "cache")
	b.u.cfg.Library = filepath.Join(b.dir, "library")
	account := filepath.Join(b.u.cfg.Volumes, "state", "state.json")
	os.MkdirAll(filepath.Dir(account), 0o755)
	os.WriteFile(account, []byte("{}"), 0o644)
	os.MkdirAll(b.u.cfg.Cache, 0o755)
	os.MkdirAll(b.u.cfg.Library, 0o755)

	rec := httptest.NewRecorder()
	b.u.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/reset", strings.NewReader(`{"mode":"start-over"}`)))
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"waiting":"button"`) {
		t.Fatalf("start over asked: %d %s", rec.Code, rec.Body.String())
	}
	time.Sleep(2500 * time.Millisecond)
	if _, err := os.Stat(account); err != nil {
		t.Fatal("started over with nobody at the box")
	}
	b.u.Pressed(context.Background(), ResetPresses)
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(account); os.IsNotExist(err) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("ten presses did not start over")
}
