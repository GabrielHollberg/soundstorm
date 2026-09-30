package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/GabrielHollberg/soundstorm/internal/media"
)

// A cover of one's own is shown to that person only: another account gets
// neither the list entry nor the picture, and anything that is not a picture
// is refused.
func TestOwnCoversArePerPerson(t *testing.T) {
	h := newHarness(t, stub{id: "navidrome", kind: media.KindMusic})
	h.signUp(t)
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{1}, 64)...)

	put := func(c *harness, key string, body []byte) (*http.Response, []byte) {
		req, _ := http.NewRequest(http.MethodPut, c.srv.URL+"/api/myart?key="+key, bytes.NewReader(body))
		resp, err := c.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var buf bytes.Buffer
		buf.ReadFrom(resp.Body)
		return resp, buf.Bytes()
	}
	if resp, _ := put(h, "art:navidrome/al-1", []byte("<svg onload=alert(1)>")); resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("an SVG was accepted: %d", resp.StatusCode)
	}
	if resp, _ := put(h, "art:elsewhere/al-1", png); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a cover for a shelf that does not exist was accepted: %d", resp.StatusCode)
	}
	resp, body := put(h, "art:navidrome/al-1&key=song:navidrome/s1", png)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("set = %d %s", resp.StatusCode, body)
	}
	var got struct {
		Art map[string]string `json:"art"`
	}
	json.Unmarshal(body, &got)
	url := got.Art["song:navidrome/s1"]
	if url == "" || got.Art["art:navidrome/al-1"] != url {
		t.Fatalf("art = %v, want both keys on one picture", got.Art)
	}
	if resp, pic := h.do(t, http.MethodGet, url, ""); resp.StatusCode != http.StatusOK || !bytes.Equal(pic, png) || resp.Header.Get("Content-Type") != "image/png" {
		t.Errorf("own picture = %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}

	h.addMember(t, "sam", samPassword)
	sam := h.asUser(t, "sam", samPassword)
	if _, body := sam.do(t, http.MethodGet, "/api/myart", ""); strings.Contains(string(body), "al-1") {
		t.Errorf("another person sees the cover: %s", body)
	}
	if resp, _ := sam.do(t, http.MethodGet, url, ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("another person fetched the picture: %d", resp.StatusCode)
	}

	h.do(t, http.MethodDelete, "/api/myart?key=art:navidrome/al-1&key=song:navidrome/s1", "")
	if resp, _ := h.do(t, http.MethodGet, url, ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a picture nothing uses is still served: %d", resp.StatusCode)
	}
}

// A playlist can have a picture of its own, in place of its collage: only the
// person's own playlist, and it goes when the playlist does.
func TestAPlaylistHasAPictureOfItsOwn(t *testing.T) {
	h := newHarness(t, stub{id: "navidrome", kind: media.KindMusic})
	h.signUp(t)
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{2}, 64)...)
	put := func(c *harness, key string) (*http.Response, []byte) {
		req, _ := http.NewRequest(http.MethodPut, c.srv.URL+"/api/myart?key="+key, bytes.NewReader(png))
		resp, err := c.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var buf bytes.Buffer
		buf.ReadFrom(resp.Body)
		return resp, buf.Bytes()
	}
	_, made := h.do(t, http.MethodPost, "/api/playlists", `{"name":"Road trip"}`)
	var list struct {
		ID string `json:"id"`
	}
	json.Unmarshal(made, &list)
	if list.ID == "" {
		t.Fatalf("no playlist: %s", made)
	}
	if resp, _ := put(h, "playlist:0123456789abcdef"); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a picture for a playlist that does not exist was accepted: %d", resp.StatusCode)
	}
	h.addMember(t, "sam", samPassword)
	sam := h.asUser(t, "sam", samPassword)
	if resp, _ := put(sam, "playlist:"+list.ID); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("somebody else's playlist took a picture: %d", resp.StatusCode)
	}
	resp, body := put(h, "playlist:"+list.ID)
	var got struct {
		Art map[string]string `json:"art"`
	}
	json.Unmarshal(body, &got)
	url := got.Art["playlist:"+list.ID]
	if resp.StatusCode != http.StatusOK || url == "" {
		t.Fatalf("set = %d %s", resp.StatusCode, body)
	}
	h.do(t, http.MethodDelete, "/api/playlists/"+list.ID, "")
	if _, body := h.do(t, http.MethodGet, "/api/myart", ""); strings.Contains(string(body), list.ID) {
		t.Errorf("a deleted playlist kept its picture: %s", body)
	}
	if resp, _ := h.do(t, http.MethodGet, url, ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a deleted playlist's picture is still served: %d", resp.StatusCode)
	}
}
