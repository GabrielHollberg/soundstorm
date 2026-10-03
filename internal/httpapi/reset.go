package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/auth"
	"github.com/GabrielHollberg/soundstorm/internal/media"
)

// Starting a box over, erasing it, and the way back in when the owner's
// password is forgotten (the owner's design, 2026-10-03). The box's
// caretaker does the wiping and watches the power button (internal/caretaker
// reset.go); SoundStorm decides who may ask, over the caretaker's socket
// (SOUNDSTORM_CARETAKER, on a box only):
//
//   - Start over (owner, password, "START OVER" typed): every account, list
//     and setting goes, the media stays.
//   - Erase everything (owner, password, "ERASE" typed): the media too.
//   - Forgot the password, or lost the sticker: the power button pressed five
//     times, and for fifteen minutes the owner's password can be set from
//     the home network without the old one. Nothing is erased.

// caretakerCall asks the caretaker something over its socket.
func (s *Server) caretakerCall(ctx context.Context, method, path string, body, out any) (int, error) {
	if s.caretakerSocket == "" {
		return 0, errNoCaretaker
	}
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", s.caretakerSocket)
		}},
	}
	var reader io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://caretaker"+path, reader)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if out != nil {
		_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(out)
	}
	return resp.StatusCode, nil
}

var errNoCaretaker = errors.New("this install has no caretaker")

// buttonOpen reports whether the box's power button was pressed five times
// within the last fifteen minutes.
func (s *Server) buttonOpen(ctx context.Context) bool {
	var st struct {
		Open bool `json:"open"`
	}
	code, err := s.caretakerCall(ctx, http.MethodGet, "/button", nil, &st)
	return err == nil && code == http.StatusOK && st.Open
}

// GET /api/reset/button: whether the owner's password may be set now, and
// whose it is. Open: it is asked from the sign-in screen.
func (s *Server) handleResetButton(w http.ResponseWriter, r *http.Request) {
	// box: there is a button to press at all, for the sign-in screen's hint.
	out := map[string]any{"open": false, "box": s.caretakerSocket != ""}
	if s.caretakerSocket != "" && s.auth.HasAccount() && s.buttonOpen(r.Context()) {
		for _, u := range s.store.Users() {
			if u.IsOwner() {
				out["open"], out["owner"] = true, u.Name
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// POST /api/reset/owner-password {password}: the owner's new password, with
// no old one, while the button's window is open - once, and only from the
// home network: never through the away-from-home name.
func (s *Server) handleResetOwnerPassword(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(strings.ToLower(requestHostname(r)), ".net.soundstorm.dev") || !s.buttonOpen(r.Context()) {
		writeError(w, http.StatusForbidden, "Press the power button on the box five times quickly, then try again within 15 minutes.")
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "expected a JSON body with password")
		return
	}
	owner, err := s.auth.SetOwnerPassword(body.Password)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	_, _ = s.caretakerCall(r.Context(), http.MethodPost, "/button/used", nil, nil)
	s.log.Warn("the owner's password was set from the box's button", "owner", owner.Name, "remote", r.RemoteAddr)
	writeJSON(w, http.StatusOK, map[string]any{"owner": owner.Name})
}

// GET /api/reset/summary: what an erase would delete, to say before asking.
func (s *Server) handleResetSummary(w http.ResponseWriter, r *http.Request) {
	counts := map[string]int{}
	var bytes int64
	for _, kind := range []media.Kind{media.KindMusic, media.KindVideo, media.KindTV, media.KindAudiobook, media.KindEbook, media.KindDocument, media.KindPicture} {
		dir := s.library.PathFor(kind)
		if dir == "" {
			continue
		}
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || r.Context().Err() != nil {
				return nil
			}
			if d.Type().IsRegular() && !strings.HasPrefix(d.Name(), ".") && d.Name() != "README.txt" {
				counts[string(kind)]++
				if info, err := d.Info(); err == nil {
					bytes += info.Size()
				}
			}
			return nil
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": counts, "bytes": bytes, "people": len(s.store.Users())})
}

// POST /api/reset {mode, password, confirm}: start the box over, or erase
// it. The owner's alone, with their password and the word typed.
func (s *Server) handleReset(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var body struct {
		Mode     string `json:"mode"`
		Password string `json:"password"`
		Confirm  string `json:"confirm"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "expected mode, password and confirm")
		return
	}
	word := map[string]string{"start-over": "START OVER", "erase": "ERASE"}[body.Mode]
	if word == "" {
		writeError(w, http.StatusBadRequest, "mode is start-over or erase")
		return
	}
	if strings.ToUpper(strings.Join(strings.Fields(body.Confirm), " ")) != word {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Type %s to confirm.", word))
		return
	}
	if err := s.auth.VerifyOwnPassword(r.Context(), clientOf(r), actor, body.Password); err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			writeError(w, http.StatusForbidden, "That password is not right.")
			return
		}
		writeError(w, http.StatusTooManyRequests, err.Error())
		return
	}
	code, err := s.caretakerCall(r.Context(), http.MethodPost, "/reset", map[string]string{"mode": body.Mode}, nil)
	if err != nil || code != http.StatusAccepted {
		writeError(w, http.StatusServiceUnavailable, "The box could not start over just now. Try again in a minute.")
		return
	}
	s.log.Warn("the box is being reset", "mode", body.Mode, "by", actor.Name)
	writeJSON(w, http.StatusAccepted, map[string]any{"mode": body.Mode})
}
