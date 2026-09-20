package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gabehollberg/atrium/internal/federate"
	"github.com/gabehollberg/atrium/internal/media"
	"github.com/gabehollberg/atrium/internal/source"
)

type stub struct {
	id    string
	kind  media.Kind
	items []media.Item
	err   error
	raw   string
}

func (s stub) ID() string       { return s.id }
func (s stub) Kind() media.Kind { return s.kind }
func (s stub) Search(context.Context, media.Query) ([]media.Item, error) {
	return s.items, s.err
}
func (s stub) Health(context.Context) error { return s.err }
func (s stub) Probe(context.Context, media.Query) ([]byte, string, error) {
	return []byte(s.raw), "application/json", nil
}

func testServer(t *testing.T, enableProbe bool, sources ...source.Source) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	api := New(source.NewRegistry(sources...), time.Second, enableProbe, log)
	srv := httptest.NewServer(api.Routes())
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, srv *httptest.Server, path string) (*http.Response, []byte) {
	t.Helper()
	resp, err := srv.Client().Get(srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, body
}

func TestSearchReturnsMergedResults(t *testing.T) {
	srv := testServer(t, false,
		stub{id: "books", kind: media.KindEbook, items: []media.Item{{ID: "1", Title: "Dune", Kind: media.KindEbook, SourceID: "books"}}},
		stub{id: "video", kind: media.KindVideo, items: []media.Item{{ID: "2", Title: "Dune", Kind: media.KindVideo, SourceID: "video"}}},
	)

	resp, body := get(t, srv, "/api/search?q=dune")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}

	var got federate.Result
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Items) != 2 {
		t.Errorf("want 2 merged items, got %d", len(got.Items))
	}
	if got.Degraded {
		t.Error("want Degraded=false when every source succeeds")
	}
	for _, it := range got.Items {
		if it.Score <= 0 {
			t.Errorf("item %q was not scored", it.Title)
		}
	}
}

// The behaviour that matters when the server at dad's house is offline.
func TestSearchStillServesWhenOneSourceIsDown(t *testing.T) {
	srv := testServer(t, false,
		stub{id: "books", kind: media.KindEbook, items: []media.Item{{ID: "1", Title: "Dune", Kind: media.KindEbook}}},
		stub{id: "music", kind: media.KindMusic, err: errors.New("dial tcp: connection refused")},
	)

	resp, body := get(t, srv, "/api/search?q=dune")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a degraded search must still be 200, got %d", resp.StatusCode)
	}

	var got federate.Result
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Items) != 1 {
		t.Errorf("want the surviving source's result, got %d items", len(got.Items))
	}
	if !got.Degraded {
		t.Error("want Degraded=true so the client can say so")
	}
}

func TestSearchFiltersByKind(t *testing.T) {
	srv := testServer(t, false,
		stub{id: "books", kind: media.KindEbook, items: []media.Item{{ID: "1", Title: "Dune"}}},
		stub{id: "music", kind: media.KindMusic, items: []media.Item{{ID: "2", Title: "Dune"}}},
	)

	_, body := get(t, srv, "/api/search?q=dune&kind=ebook")
	var got federate.Result
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Sources) != 1 || got.Sources[0].SourceID != "books" {
		t.Errorf("kind filter did not apply: %+v", got.Sources)
	}
}

func TestSearchValidatesInput(t *testing.T) {
	srv := testServer(t, false, stub{id: "books", kind: media.KindEbook})

	for _, path := range []string{
		"/api/search",              // no q
		"/api/search?q=x&kind=vhs", // unknown kind
		"/api/search?q=x&limit=0",  // out of range
		"/api/search?q=x&limit=999",
		"/api/search?q=x&limit=abc",
	} {
		resp, body := get(t, srv, path)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("GET %s: status = %d, want 400 (body %s)", path, resp.StatusCode, body)
		}
	}
}

func TestSourcesReportsPerSourceHealth(t *testing.T) {
	srv := testServer(t, false,
		stub{id: "books", kind: media.KindEbook},
		stub{id: "music", kind: media.KindMusic, err: errors.New("unreachable")},
	)

	resp, body := get(t, srv, "/api/sources")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	var got struct {
		AllOK   bool                    `json:"allOk"`
		Sources []federate.SourceStatus `json:"sources"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.AllOK {
		t.Error("allOk should be false when a source is down")
	}
	if len(got.Sources) != 2 {
		t.Fatalf("want 2 statuses, got %d", len(got.Sources))
	}
}

func TestProbeIsOffByDefault(t *testing.T) {
	srv := testServer(t, false, stub{id: "books", kind: media.KindEbook, raw: `{"hello":"world"}`})
	resp, _ := get(t, srv, "/api/probe/books?q=x")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when probing is disabled", resp.StatusCode)
	}
}

func TestProbeReturnsRawUpstreamBody(t *testing.T) {
	srv := testServer(t, true, stub{id: "books", kind: media.KindEbook, raw: `{"hello":"world"}`})

	resp, body := get(t, srv, "/api/probe/books?q=x")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if string(body) != `{"hello":"world"}` {
		t.Errorf("body = %s", body)
	}
}

func TestProbeUnknownSource(t *testing.T) {
	srv := testServer(t, true, stub{id: "books", kind: media.KindEbook})
	resp, _ := get(t, srv, "/api/probe/nope?q=x")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestHealthz(t *testing.T) {
	srv := testServer(t, false, stub{id: "books", kind: media.KindEbook})
	resp, body := get(t, srv, "/healthz")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got struct {
		Status  string `json:"status"`
		Sources int    `json:"sources"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != "ok" || got.Sources != 1 {
		t.Errorf("unexpected healthz payload: %+v", got)
	}
}
