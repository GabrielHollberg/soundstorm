package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/plex"
	"github.com/GabrielHollberg/soundstorm/internal/source"
)

// plexTestShelf is a music shelf that can say where its files are.
type plexTestShelf struct {
	stub
	files []source.SongFile
}

func (s plexTestShelf) SongFiles(context.Context) ([]source.SongFile, error) { return s.files, nil }

// A Plexamp user signs in on Plex's page (here, a stand-in plex.tv that
// answers the PIN once "signed in"), picks a playlist from their server, and
// it comes across matched to the library - never with the Plex token
// reaching the browser.
func TestPlexImportEndToEnd(t *testing.T) {
	const token, serverToken = "account-token-XYZ", "server-token-ABC"
	signedIn := false
	pms := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Plex-Token") != serverToken {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/identity":
			w.Write([]byte(`{"MediaContainer":{"machineIdentifier":"srv1"}}`))
		case "/playlists":
			w.Write([]byte(`{"MediaContainer":{"Metadata":[{"ratingKey":"77","title":"Road Trip","leafCount":3}]}}`))
		case "/playlists/77/items":
			w.Write([]byte(`{"MediaContainer":{"Metadata":[
				{"title":"Airbag","grandparentTitle":"Radiohead","parentTitle":"OK Computer","duration":284000,
				 "Media":[{"Part":[{"file":"/data/Music/Radiohead/OK Computer/01 Airbag.flac"}]}]},
				{"title":"Lithium","grandparentTitle":"Various Artists","originalTitle":"Nirvana","duration":257000,
				 "Media":[{"Part":[{"file":"/data/Music/Comp/07 Lithium.flac"}]}]},
				{"title":"One More Time","grandparentTitle":"Daft Punk","duration":320000,
				 "Media":[{"Part":[{"file":"/data/Music/Daft Punk/Discovery/01 One More Time.flac"}]}]}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer pms.Close()
	plexTV := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/pins":
			w.Write([]byte(`{"id":42,"code":"abcd"}`))
		case r.URL.Path == "/api/v2/pins/42":
			if signedIn {
				w.Write([]byte(`{"id":42,"code":"abcd","authToken":"` + token + `"}`))
			} else {
				w.Write([]byte(`{"id":42,"code":"abcd","authToken":null}`))
			}
		case r.URL.Path == "/api/v2/resources":
			if r.Header.Get("X-Plex-Token") != token {
				http.Error(w, "no", http.StatusUnauthorized)
				return
			}
			json.NewEncoder(w).Encode([]map[string]any{
				{"name": "Player only", "clientIdentifier": "p1", "provides": "player"},
				{"name": "Home server", "clientIdentifier": "srv1", "provides": "server", "owned": true,
					"accessToken": serverToken, "connections": []map[string]any{
						{"uri": "http://jellyfin:8096", "local": true},
						{"uri": pms.URL, "local": true},
					}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer plexTV.Close()

	song := func(id, artist, title string, secs float64, path string) source.SongFile {
		return source.SongFile{Path: path, Item: media.Item{ID: id, SourceID: "navidrome", Kind: media.KindMusic,
			Title: title, Creators: []string{artist}, DurationSeconds: secs}}
	}
	shelf := plexTestShelf{stub: stub{id: "navidrome", kind: media.KindMusic}, files: []source.SongFile{
		song("1", "Radiohead", "Airbag", 284, "Radiohead/OK Computer/01 Airbag.flac"),
		song("2", "Nirvana", "Lithium", 257, "Nirvana/Nevermind/05 Lithium.flac"),
	}}
	h := newHarness(t, shelf)
	h.api.plexOverride = &plex.Client{API: plexTV.URL, AuthPage: "https://auth.example/auth", ClientID: "test",
		AllowHost: func(host string) bool { return host == "127.0.0.1" }}
	h.signUp(t)

	resp, body := h.do(t, http.MethodPost, "/api/plex/signin", `{}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "code=abcd") {
		t.Fatalf("signin = %d %s", resp.StatusCode, body)
	}
	if _, body = h.do(t, http.MethodGet, "/api/plex/status", ""); !strings.Contains(string(body), `"waiting"`) {
		t.Fatalf("before signing in: %s", body)
	}
	signedIn = true
	_, body = h.do(t, http.MethodGet, "/api/plex/status", "")
	if !strings.Contains(string(body), `"ready"`) || !strings.Contains(string(body), "Home server") || strings.Contains(string(body), "Player only") {
		t.Fatalf("after signing in: %s", body)
	}
	if strings.Contains(string(body), token) || strings.Contains(string(body), serverToken) {
		t.Fatal("a Plex token reached the browser")
	}
	_, body = h.do(t, http.MethodGet, "/api/plex/playlists?server=srv1", "")
	if !strings.Contains(string(body), "Road Trip") {
		t.Fatalf("playlists: %s", body)
	}
	resp, body = h.do(t, http.MethodPost, "/api/plex/import", `{"server":"srv1","playlists":[{"id":"77","title":"Road Trip"}]}`)
	var got struct {
		Results []importResult `json:"results"`
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &got) != nil || len(got.Results) != 1 {
		t.Fatalf("import = %d %s", resp.StatusCode, body)
	}
	r := got.Results[0]
	if r.Added != 2 || r.Total != 3 || len(r.Missing) != 1 || r.Missing[0] != "Daft Punk - One More Time" {
		t.Errorf("result = %+v, want Airbag by path and Lithium by its track artist, Daft Punk missing", r)
	}
	_, body = h.do(t, http.MethodGet, "/api/playlists/"+r.ID, "")
	if !strings.Contains(string(body), "Airbag") || !strings.Contains(string(body), "Lithium") {
		t.Errorf("playlist: %s", body)
	}
}

// Only Plex's own names and IP addresses that are not this machine may be
// dialed: a server address is plex.tv's say-so, and a made-up one must not
// reach the compose network or cloud metadata.
func TestPlexServerAddressesAreLimited(t *testing.T) {
	for host, want := range map[string]bool{
		"192-168-1-5.abc123.plex.direct": true,
		"192.168.1.5":                    true,
		"203.0.113.9":                    true,
		"jellyfin":                       false,
		"127.0.0.1":                      false,
		"169.254.169.254":                false,
		"localhost":                      false,
		"::1":                            false,
	} {
		if got := plex.AllowedHost(host); got != want {
			t.Errorf("AllowedHost(%q) = %v, want %v", host, got, want)
		}
	}
}
