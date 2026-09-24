package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/GabrielHollberg/soundstorm/internal/lyrics"
)

func testLyrics(t *testing.T) *lyrics.Finder {
	t.Helper()
	f, err := lyrics.New(t.TempDir())
	if err != nil {
		t.Fatalf("lyrics.New: %v", err)
	}
	return f
}

// Through the real routes: the owner turns online lyrics on and the session
// reports it; a member is refused.
func TestOwnerCanTurnOnOnlineLyrics(t *testing.T) {
	h := newRemoteHarness(t, &remoteState{})
	h.signUp(t)

	resp, body := h.do(t, http.MethodPut, "/api/settings/lyrics", `{"enabled":true}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /api/settings/lyrics = %d %s", resp.StatusCode, body)
	}
	var session struct {
		OnlineLyrics *bool `json:"onlineLyrics"`
	}
	_, body = h.do(t, http.MethodGet, "/api/session", "")
	if err := json.Unmarshal(body, &session); err != nil || session.OnlineLyrics == nil || !*session.OnlineLyrics {
		t.Errorf("session after turning it on: %s", body)
	}

	h.addMember(t, "sam", samPassword)
	member := h.asUser(t, "sam", samPassword)
	if resp, _ := member.do(t, http.MethodPut, "/api/settings/lyrics", `{"enabled":false}`); resp.StatusCode != http.StatusForbidden {
		t.Errorf("member PUT = %d, want 403", resp.StatusCode)
	}
}
