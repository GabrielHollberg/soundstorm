package immich

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/media"
)

// fakeImmich answers the handful of endpoints the adapter uses, in the shapes
// Immich 3.2 was seen to use, and records what it was asked.
type fakeImmich struct {
	mu        sync.Mutex
	assets    []asset
	smartDown bool
	calls     []call
}

type call struct {
	Path string
	Body map[string]any
	Key  string
}

func (f *fakeImmich) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	json.Unmarshal(raw, &body)
	f.calls = append(f.calls, call{Path: r.URL.Path, Body: body, Key: r.Header.Get("x-api-key")})

	switch {
	case r.URL.Path == "/api/search/smart" && f.smartDown:
		http.Error(w, `{"message":"machine learning unavailable"}`, http.StatusInternalServerError)
	case r.URL.Path == "/api/search/metadata" || r.URL.Path == "/api/search/smart":
		page, size := int(body["page"].(float64)), int(body["size"].(float64))
		from := min((page-1)*size, len(f.assets))
		to := min(from+size, len(f.assets))
		var next any
		if to < len(f.assets) {
			next = "2"
		}
		json.NewEncoder(w).Encode(map[string]any{"assets": map[string]any{"items": f.assets[from:to], "nextPage": next}})
	case strings.HasPrefix(r.URL.Path, "/api/assets/"):
		id := strings.TrimPrefix(r.URL.Path, "/api/assets/")
		for _, a := range f.assets {
			if a.ID == id {
				json.NewEncoder(w).Encode(a)
				return
			}
		}
		http.NotFound(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeImmich) lastCall() call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[len(f.calls)-1]
}

func newSource(t *testing.T, f *fakeImmich) *Source {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	s, err := New(Config{ID: "immich", BaseURL: srv.URL, APIKey: "k3y", LibraryID: "lib-1", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func ms(n int64) *int64 { return &n }

func sampleAssets() []asset {
	return []asset{
		{ID: "a1", Type: "IMAGE", OriginalFileName: "IMG_0900.heic", LocalDateTime: "2024-07-14T18:02:11.000Z", Width: 4032, Height: 3024},
		{ID: "a2", Type: "VIDEO", OriginalFileName: "IMG_0901.mov", LocalDateTime: "2024-07-14T18:03:00.000Z", Duration: ms(12500)},
		{ID: "a3", Type: "IMAGE", OriginalFileName: "IMG_0100.jpg", LocalDateTime: "2023-01-02T09:00:00.000Z", IsOffline: true},
		{ID: "a4", Type: "IMAGE", OriginalFileName: "IMG_0050.jpg", LocalDateTime: "2022-05-05T10:00:00.000Z"},
	}
}

// Browsing asks for this library, newest first, without offline files - and
// keeps Immich's order through SoundStorm's merge by way of SortKey.
func TestBrowsingIsNewestFirstAndSkipsMissingFiles(t *testing.T) {
	f := &fakeImmich{assets: sampleAssets()}
	s := newSource(t, f)

	items, err := s.Search(context.Background(), media.Query{Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	c := f.lastCall()
	if c.Path != "/api/search/metadata" || c.Body["libraryId"] != "lib-1" || c.Body["order"] != "desc" || c.Body["isOffline"] != false {
		t.Errorf("browse asked %s %v", c.Path, c.Body)
	}
	if c.Key != "k3y" {
		t.Errorf("api key header = %q", c.Key)
	}

	var names []string
	for _, it := range items {
		names = append(names, it.Title)
	}
	if strings.Join(names, ",") != "IMG_0900.heic,IMG_0901.mov,IMG_0050.jpg" {
		t.Errorf("items = %v; want Immich's order without the offline file", names)
	}
	for i := 1; i < len(items); i++ {
		if items[i-1].SortKey >= items[i].SortKey {
			t.Errorf("SortKey does not preserve order at %d: %q then %q", i, items[i-1].SortKey, items[i].SortKey)
		}
	}
	if items[0].Subtitle != "14 Jul 2024" || items[0].Year != 2024 || items[0].Extra["type"] != "image" {
		t.Errorf("first item = %+v", items[0])
	}
	if items[1].Extra["type"] != "video" || items[1].DurationSeconds != 12.5 {
		t.Errorf("clip = %+v", items[1])
	}
}

// Typed text is a search by what is in the picture. Without the machine
// learning container - off on a small machine, or downloading its models on a
// fresh install - it falls back to file names rather than failing.
func TestSearchFallsBackToFileNamesWithoutMachineLearning(t *testing.T) {
	f := &fakeImmich{assets: sampleAssets()}
	s := newSource(t, f)
	if _, err := s.Search(context.Background(), media.Query{Text: "beach at sunset", Limit: 5}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if c := f.lastCall(); c.Path != "/api/search/smart" || c.Body["query"] != "beach at sunset" {
		t.Errorf("text search asked %s %v", c.Path, c.Body)
	}

	f.smartDown = true
	items, err := s.Search(context.Background(), media.Query{Text: "IMG_09", Limit: 5})
	if err != nil {
		t.Fatalf("Search with smart search down: %v", err)
	}
	if c := f.lastCall(); c.Path != "/api/search/metadata" || c.Body["originalFileName"] != "IMG_09" {
		t.Errorf("fallback asked %s %v", c.Path, c.Body)
	}
	if len(items) == 0 {
		t.Error("the fallback returned nothing")
	}
}

// Immich's page is at most 1000; SoundStorm pages to 2000 deep.
func TestDeepBrowsingAsksForSeveralPages(t *testing.T) {
	f := &fakeImmich{}
	for i := 0; i < 1500; i++ {
		f.assets = append(f.assets, asset{ID: "x", Type: "IMAGE", OriginalFileName: "p.jpg"})
	}
	s := newSource(t, f)
	items, err := s.Search(context.Background(), media.Query{Limit: 1500})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1500 {
		t.Errorf("got %d items, want 1500", len(items))
	}
	var sizes []float64
	for _, c := range f.calls {
		sizes = append(sizes, c.Body["size"].(float64))
	}
	if len(sizes) != 2 || sizes[0] != 1000 || sizes[1] != 500 {
		t.Errorf("page sizes asked = %v, want [1000 500]", sizes)
	}
}

// The grid gets the small thumbnail; the viewer asks for the preview, which is
// a JPEG whatever the original was - a browser cannot show HEIC or raw.
func TestArtIsAThumbnailOrAPreview(t *testing.T) {
	s := newSource(t, &fakeImmich{})
	thumb, _ := s.ArtTarget(context.Background(), "a1")
	preview, _ := s.ArtTarget(context.Background(), "a1"+PreviewSuffix)
	if !strings.HasSuffix(thumb.URL, "/api/assets/a1/thumbnail?size=thumbnail") {
		t.Errorf("thumbnail URL = %s", thumb.URL)
	}
	if !strings.HasSuffix(preview.URL, "/api/assets/a1/thumbnail?size=preview") {
		t.Errorf("preview URL = %s", preview.URL)
	}
	if thumb.Headers["x-api-key"] != "k3y" {
		t.Error("art request carries no api key")
	}
}

// A photo downloads as its original; a clip plays as Immich's playable
// rendition, transcoded if the original will not play in a browser.
func TestStreamIsTheOriginalOrAPlayableClip(t *testing.T) {
	s := newSource(t, &fakeImmich{assets: sampleAssets()})
	photo, err := s.StreamTarget(context.Background(), "a1")
	if err != nil || !strings.HasSuffix(photo.URL, "/api/assets/a1/original") {
		t.Errorf("photo stream = %s, %v", photo.URL, err)
	}
	clip, err := s.StreamTarget(context.Background(), "a2")
	if err != nil || !strings.HasSuffix(clip.URL, "/api/assets/a2/video/playback") {
		t.Errorf("clip stream = %s, %v", clip.URL, err)
	}
}
