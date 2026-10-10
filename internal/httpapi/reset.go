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
	"net/netip"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/auth"
	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/names"
)

// Starting a box over, erasing it, and the way back in when the owner's
// password is forgotten (the owner's design, 2026-10-03). The box's
// caretaker does the wiping and watches the power button (internal/caretaker
// reset.go); EmberStorm decides who may ask, over the caretaker's socket
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
	// One client for every call, and no connection kept open after one: a
	// client made per call left its connection idle in a pool nothing
	// closed, in EmberStorm and in the caretaker alike, on an endpoint
	// anyone at home can ask (the box's blind security review).
	s.caretakerOnce.Do(func() {
		socket := s.caretakerSocket
		s.caretakerClient = &http.Client{
			// Each call's own deadline (caretakerCall): opening a slow USB
			// drive takes longer than the rest.
			Transport: &http.Transport{
				DisableKeepAlives: true,
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, "unix", socket)
				},
			},
		}
	})
	client := s.caretakerClient
	var reader io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	}
	wait := 30 * time.Second
	if path == "/drives/open" {
		wait = 3 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
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

// boxSerial is a box's serial as prepare.sh makes it: EM-XXXX-XXXX.
var boxSerial = regexp.MustCompile(`^EM-[0-9A-HJKMNP-TV-Z]{4}-[0-9A-HJKMNP-TV-Z]{4}$`)

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
	if s.caretakerSocket != "" && s.homeConnection(r) && s.auth.HasAccount() && s.buttonOpen(r.Context()) {
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
	if names.IsAwayName(requestHostname(r)) || !s.homeConnection(r) || !s.buttonOpen(r.Context()) {
		writeError(w, http.StatusForbidden, "Press the power button on the box five times quickly, then try again within 15 minutes.")
		return
	}
	var body struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "expected a JSON body with password and code")
		return
	}
	if err := s.auth.OwnerPasswordProblem(body.Password); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// The code on the box's own screen, or the setup code on its sticker
	// (for a box with no screen plugged in), checked and the window closed
	// in one step by the caretaker: only somebody at the box can read the
	// one, only the owner has the other, and only one ask can have it. Sent
	// as typed - the caretaker sets case, spaces and dashes aside.
	code := strings.TrimSpace(body.Code)
	if len(code) > 64 {
		code = code[:64]
	}
	if status, err := s.caretakerCall(r.Context(), http.MethodPost, "/button/claim", map[string]string{"code": code}, nil); err != nil || status != http.StatusOK {
		writeError(w, http.StatusForbidden, "That is not the setup code on the box's sticker, or the code on its screen.")
		return
	}
	owner, err := s.auth.SetOwnerPassword(body.Password)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.log.Warn("the owner's password was set from the box's button", "owner", owner.Name, "remote", r.RemoteAddr)
	writeJSON(w, http.StatusOK, map[string]any{"owner": owner.Name})
}

// fromHomeNetwork is whether the connection itself comes from the home
// network or the box: a private, loopback or link-local address. The name a
// request asks for (its Host) is the asker's to choose, so it alone said
// nothing - a stranger reaching the box through remote access could name a
// home address and set the owner's password the moment the button was
// pressed. On the box, Docker keeps the real address of a connection from
// the network; through remote access it is a public one.
func fromHomeNetwork(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	a = a.Unmap()
	return a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast()
}

// homeConnection is fromHomeNetwork, and on a box also not relayed from
// inside Docker's own networks. On a box a connection from the home network
// keeps its own address; one from the address of a network this container
// is on was relayed - an IPv6 connection through Docker's proxy (the
// container's networks have no IPv6, so every one arrives from the
// gateway), or Tailscale's sidecar - and may come from anywhere (the box's
// blind security review). Off a box, Docker Desktop gives every connection
// the gateway's address, so there the check stays as it was.
func (s *Server) homeConnection(r *http.Request) bool {
	if !fromHomeNetwork(r) {
		return false
	}
	if s.caretakerSocket == "" {
		return true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	a = a.Unmap()
	if a.IsLoopback() {
		return true
	}
	for _, p := range ownNetworks() {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

// ownNetworks are the networks this process's interfaces are on, loopback
// aside.
var ownNetworks = sync.OnceValue(func() []netip.Prefix {
	var out []netip.Prefix
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok || n.IP.IsLoopback() {
			continue
		}
		if p, err := netip.ParsePrefix(n.String()); err == nil {
			out = append(out, p.Masked())
		}
	}
	return out
})

// GET /api/reset/waiting: an erase or start over waiting for the box's
// power button, if one is (owner), so Settings can say so and cancel it
// whenever the page was opened (the box's blind review).
func (s *Server) handleResetWaiting(w http.ResponseWriter, r *http.Request) {
	var st struct {
		Reset      string    `json:"reset"`
		ResetUntil time.Time `json:"resetUntil"`
	}
	if code, err := s.caretakerCall(r.Context(), http.MethodGet, "/button", nil, &st); err != nil || code != http.StatusOK {
		writeJSON(w, http.StatusOK, map[string]any{"waiting": ""})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"waiting": st.Reset, "until": st.ResetUntil})
}

// GET and PUT /api/box/timezone {zone}: the box's time zone (owner), which
// the page sets to the owner's own, so the night the box keeps for its
// updates and restart is theirs (the box's blind review).
func (s *Server) handleBoxTimeZone(w http.ResponseWriter, r *http.Request) {
	var answer struct {
		Zone string `json:"zone"`
	}
	if r.Method == http.MethodPut {
		var body struct {
			Zone string `json:"zone"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil || len(body.Zone) > 64 {
			writeError(w, http.StatusBadRequest, "expected a time zone")
			return
		}
		code, err := s.caretakerCall(r.Context(), http.MethodPut, "/timezone", map[string]string{"zone": body.Zone}, &answer)
		if err != nil || code != http.StatusOK {
			if code == http.StatusBadRequest {
				writeError(w, http.StatusBadRequest, "That is not a time zone the box knows.")
				return
			}
			writeError(w, http.StatusBadGateway, "The box could not set its time zone just now.")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"zone": answer.Zone})
		return
	}
	if code, err := s.caretakerCall(r.Context(), http.MethodGet, "/timezone", nil, &answer); err != nil || code != http.StatusOK {
		writeError(w, http.StatusBadGateway, "The box could not be asked.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"zone": answer.Zone})
}

// DELETE /api/reset: an erase still waiting for the box's power button is
// taken back (owner).
func (s *Server) handleCancelReset(w http.ResponseWriter, r *http.Request) {
	if status, err := s.caretakerCall(r.Context(), http.MethodPost, "/reset/cancel", nil, nil); err != nil || status != http.StatusOK {
		writeError(w, http.StatusBadGateway, "The box could not be asked. Do not press its power button; the erase is cancelled by itself after ten minutes.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cancelled": true})
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
	// Erasing waits at the caretaker for five presses of the box's power
	// button: said back to the page, which tells the person to press them.
	var answer struct {
		Waiting string `json:"waiting"`
	}
	code, err := s.caretakerCall(r.Context(), http.MethodPost, "/reset", map[string]string{"mode": body.Mode}, &answer)
	if err != nil || code != http.StatusAccepted {
		writeError(w, http.StatusServiceUnavailable, "The box could not start over just now. Try again in a minute.")
		return
	}
	s.log.Warn("the box is being reset", "mode", body.Mode, "by", actor.Name, "waiting", answer.Waiting)
	writeJSON(w, http.StatusAccepted, map[string]any{"mode": body.Mode, "waiting": answer.Waiting})
}
