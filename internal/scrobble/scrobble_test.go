package scrobble

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/collections"
)

func listenOf(title, artist string) collections.Listen {
	return collections.Listen{At: time.Unix(1790000000, 0), SourceID: "nd", ID: title, Title: title, Artist: artist, Seconds: 200}
}

func TestListensAreSentTheWayListenBrainzAsks(t *testing.T) {
	var got []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Token good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/1/validate-token":
			io.WriteString(w, "{\"code\":200,\"valid\":true,\"user_name\":\"gabe\"}")
		case "/1/submit-listens":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			got = append(got, body)
			io.WriteString(w, "{\"status\":\"ok\"}")
		}
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, HTTP: srv.Client(), Version: "abc1234"}
	ctx := context.Background()

	if name, err := c.Validate(ctx, "good"); err != nil || name != "gabe" {
		t.Errorf("validate = %q, %v", name, err)
	}
	if _, err := c.Validate(ctx, "bad"); !errors.Is(err, ErrBadToken) {
		t.Errorf("a refused token = %v", err)
	}
	done, err := c.Submit(ctx, "good", []collections.Listen{listenOf("One", "Band"), listenOf("No artist", ""), listenOf("Two", "Band")})
	if err != nil || len(done) != 3 {
		t.Fatalf("submit = %d done, %v", len(done), err)
	}
	if got[0]["listen_type"] != "import" || len(got[0]["payload"].([]any)) != 2 {
		t.Errorf("batch = %v", got[0])
	}
	first := got[0]["payload"].([]any)[0].(map[string]any)
	meta := first["track_metadata"].(map[string]any)
	info := meta["additional_info"].(map[string]any)
	if first["listened_at"].(float64) != 1790000000 || meta["artist_name"] != "Band" ||
		info["duration_ms"].(float64) != 200000 || info["submission_client"] != "SoundStorm" {
		t.Errorf("listen = %v", first)
	}
	c.Submit(ctx, "good", []collections.Listen{listenOf("Three", "Band")})
	if got[1]["listen_type"] != "single" {
		t.Errorf("one listen went as %v", got[1]["listen_type"])
	}
	if done, err := c.Submit(ctx, "bad", []collections.Listen{listenOf("Four", "Band")}); !errors.Is(err, ErrBadToken) || len(done) != 0 {
		t.Errorf("refused submit = %d done, %v", len(done), err)
	}
}
