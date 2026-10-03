package httpapi

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A USB drive plugged into the box: the owner sees it, its files, a piece of
// one, and brings a planned file in - copied by the server, filed where the
// plan said. Nothing outside the drive can be named, and a member sees none.
func TestADriveIsBroughtIn(t *testing.T) {
	drives := t.TempDir()
	testDrivesDir = drives
	t.Cleanup(func() { testDrivesDir = "" })
	pdf := []byte("%PDF-1.4 a tax form")
	write := func(rel string, data []byte) {
		full := filepath.Join(drives, "SanDisk", filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, data, 0o644)
	}
	write("Docs/Taxes/2024.pdf", pdf)
	write(".Trashes/old.pdf", pdf)
	write("System Volume Information/x.pdf", pdf)
	os.WriteFile(filepath.Join(drives, "outside.pdf"), pdf, 0o644)
	os.MkdirAll(filepath.Join(drives, "Empty"), 0o755) // a drive taken out

	h := newHarness(t)
	h.signUp(t)
	_, body := h.do(t, http.MethodGet, "/api/drives", "")
	var list struct{ Drives []struct{ ID string } }
	json.Unmarshal(body, &list)
	if len(list.Drives) != 1 || list.Drives[0].ID != "SanDisk" {
		t.Fatalf("drives: %s", body)
	}
	_, body = h.do(t, http.MethodGet, "/api/drives/SanDisk/files", "")
	var files struct{ Files []struct{ Path string } }
	json.Unmarshal(body, &files)
	if len(files.Files) != 1 || files.Files[0].Path != "Docs/Taxes/2024.pdf" {
		t.Fatalf("files (hidden and system folders left out): %s", body)
	}
	if resp, got := h.do(t, http.MethodGet, "/api/drives/SanDisk/read?path=Docs/Taxes/2024.pdf&from=0&to=4", ""); resp.StatusCode != 200 || string(got) != "%PDF" {
		t.Fatalf("a piece of a file: %d %q", resp.StatusCode, got)
	}
	for _, escape := range []string{"../outside.pdf", "/../outside.pdf", "Docs/../../outside.pdf"} {
		if resp, _ := h.do(t, http.MethodGet, "/api/drives/SanDisk/read?path="+escape+"&from=0&to=4", ""); resp.StatusCode == 200 {
			t.Fatalf("read outside the drive: %s", escape)
		}
	}

	resp, body := h.do(t, http.MethodPost, "/api/drives/SanDisk/import",
		`{"jobs":[{"id":"Docs/Taxes/2024.pdf","name":"2024.pdf","path":"Taxes/2024.pdf","group":"Docs","kind":"document"}]}`)
	if resp.StatusCode != 200 {
		t.Fatalf("import: %d %s", resp.StatusCode, body)
	}
	var st struct {
		Waiting int
		Running bool
		Jobs    []struct{ State, Dest, Error string }
	}
	for deadline := time.Now().Add(10 * time.Second); ; {
		_, body = h.do(t, http.MethodGet, "/api/drives/import", "")
		json.Unmarshal(body, &st)
		if !st.Running && st.Waiting == 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(st.Jobs) != 1 || st.Jobs[0].State != "done" || !strings.HasSuffix(st.Jobs[0].Dest, "2024.pdf") {
		t.Fatalf("after the import: %s", body)
	}
	matches, _ := filepath.Glob(filepath.Join(h.root, "documents", "*", "2024.pdf"))
	if len(matches) != 1 {
		t.Fatalf("the file is not on the shelf: %v", matches)
	}
	if got, _ := os.ReadFile(matches[0]); string(got) != string(pdf) {
		t.Fatal("the copy is not the file")
	}
	if got, _ := os.ReadFile(filepath.Join(drives, "SanDisk", "Docs", "Taxes", "2024.pdf")); string(got) != string(pdf) {
		t.Fatal("the drive's own file changed")
	}

	// A job naming a file off the drive is refused before anything moves.
	if resp, _ := h.do(t, http.MethodPost, "/api/drives/SanDisk/import",
		`{"jobs":[{"id":"../outside.pdf","name":"x.pdf","path":"x.pdf","kind":"document"}]}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a file off the drive: %d", resp.StatusCode)
	}

	// The owner's alone.
	h.do(t, http.MethodPost, "/api/users", `{"username":"sam","password":"violet tractor glacier"}`)
	sam := h.another(t)
	sam.do(t, http.MethodPost, "/api/login", `{"username":"sam","password":"violet tractor glacier"}`)
	for _, p := range []string{"/api/drives", "/api/drives/SanDisk/files", "/api/drives/SanDisk/read?path=Docs/Taxes/2024.pdf&from=0&to=4"} {
		if resp, _ := sam.do(t, http.MethodGet, p, ""); resp.StatusCode == 200 {
			t.Fatalf("a member reached %s", p)
		}
	}
}
