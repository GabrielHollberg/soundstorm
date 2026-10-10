package caretaker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A drive plugged in is only listed; it is opened when asked, and nothing
// not plugged in (or not named as a USB partition) is.
func TestADriveIsOpenedOnlyWhenAsked(t *testing.T) {
	b := newBox(t)
	b.u.cfg.DrivesWaiting = filepath.Join(b.dir, "waiting")
	b.u.cfg.DrivesOpen = filepath.Join(b.dir, "open")
	os.MkdirAll(b.u.cfg.DrivesWaiting, 0o700)
	os.MkdirAll(b.u.cfg.DrivesOpen, 0o700)
	os.WriteFile(filepath.Join(b.u.cfg.DrivesWaiting, "sdb1"), []byte("label=SanDisk\ntype=exfat\nsize=32000000000\n"), 0o600)
	var mounted []string
	b.u.run = func(_ context.Context, name string, args ...string) error {
		if len(args) == 2 && args[0] == "mount" {
			mounted = append(mounted, args[1])
			return os.WriteFile(filepath.Join(b.u.cfg.DrivesOpen, args[1]), []byte("/run/soundstorm/drives/SanDisk\n"), 0o600)
		}
		return nil
	}
	h := b.u.Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/drives", nil))
	var listed struct{ Drives []Drive }
	json.Unmarshal(rec.Body.Bytes(), &listed)
	if len(listed.Drives) != 1 || listed.Drives[0].Label != "SanDisk" || listed.Drives[0].Open != "" || len(mounted) != 0 {
		t.Fatalf("listed %+v, mounted %v", listed.Drives, mounted)
	}

	for _, part := range []string{"sdc1", "../etc", "nvme0n1", ""} {
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/drives/open", strings.NewReader(`{"part":"`+part+`"}`)))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%q: %d", part, rec.Code)
		}
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/drives/open", strings.NewReader(`{"part":"sdb1"}`)))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"open":"SanDisk"`) || len(mounted) != 1 {
		t.Fatalf("open: %d %s, mounted %v", rec.Code, rec.Body.String(), mounted)
	}
	if d := b.u.waitingDrives(); len(d) != 1 || d[0].Open != "SanDisk" {
		t.Fatalf("after opening: %+v", d)
	}
}

// The box's time zone: a real one is set through timedatectl and read from
// the system's link; anything else is refused before anything runs.
func TestTheTimeZoneIsTheOwners(t *testing.T) {
	b := newBox(t)
	b.u.cfg.LocalTime = filepath.Join(b.dir, "localtime")
	var ran []string
	b.u.run = func(_ context.Context, name string, args ...string) error {
		ran = append(ran, name+" "+strings.Join(args, " "))
		if name == "timedatectl" {
			os.Remove(b.u.cfg.LocalTime)
			return os.Symlink("/usr/share/zoneinfo/"+args[1], b.u.cfg.LocalTime)
		}
		return nil
	}
	h := b.u.Handler()
	for _, bad := range []string{"../../etc/passwd", "Mars/Olympus", "--help", ""} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/timezone", strings.NewReader(`{"zone":"`+bad+`"}`)))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%q: %d", bad, rec.Code)
		}
	}
	if len(ran) != 0 {
		t.Fatalf("ran %v for bad zones", ran)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/timezone", strings.NewReader(`{"zone":"America/Denver"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("set: %d %s", rec.Code, rec.Body.String())
	}
	if got := b.u.zoneName(); got != "America/Denver" {
		t.Fatalf("zone read back as %q", got)
	}
	if b.u.localZone().String() != "America/Denver" {
		t.Fatal("not used for the night")
	}
}
