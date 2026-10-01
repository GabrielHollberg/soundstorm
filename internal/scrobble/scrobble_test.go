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
	done, _, err := c.Submit(ctx, "good", []collections.Listen{listenOf("One", "Band"), listenOf("No artist", ""), listenOf("Two", "Band")})
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
	if done, _, err := c.Submit(ctx, "bad", []collections.Listen{listenOf("Four", "Band")}); !errors.Is(err, ErrBadToken) || len(done) != 0 {
		t.Errorf("refused submit = %d done, %v", len(done), err)
	}
}

// One listen ListenBrainz will not take must not hold up the rest: it refuses
// the whole request, so the batch is resent a listen at a time and only the
// refused one is dropped. A 5xx, which says nothing about the listens, still
// leaves everything queued. And a listen with no moment is never sent.
func TestARefusedListenDoesNotBlockTheQueue(t *testing.T) {
	var requests int
	serverDown := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if serverDown {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		var body struct {
			Payload []struct {
				ListenedAt    int64 `json:"listened_at"`
				TrackMetadata struct {
					TrackName string `json:"track_name"`
				} `json:"track_metadata"`
			} `json:"payload"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		for _, p := range body.Payload {
			if p.TrackMetadata.TrackName == "Poison" || p.ListenedAt <= 0 {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
		}
		io.WriteString(w, "{\"status\":\"ok\"}")
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	ctx := context.Background()

	undated := listenOf("Undated", "Band")
	undated.At = time.Time{}
	queue := []collections.Listen{listenOf("One", "Band"), listenOf("Poison", "Band"), listenOf("Two", "Band"), undated}
	done, dropped, err := c.Submit(ctx, "good", queue)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if len(done) != 4 || dropped != 1 {
		t.Fatalf("done %d, dropped %d; want all four done and the poisoned one dropped", len(done), dropped)
	}
	// The batch, then One, Poison and Two alone; the undated one never went.
	if requests != 4 {
		t.Errorf("%d requests, want 4", requests)
	}

	serverDown = true
	done, dropped, err = c.Submit(ctx, "good", []collections.Listen{listenOf("Three", "Band"), listenOf("Four", "Band")})
	if err == nil || len(done) != 0 || dropped != 0 {
		t.Errorf("a 502 gave done %d, dropped %d, err %v; want nothing done", len(done), dropped, err)
	}
}
