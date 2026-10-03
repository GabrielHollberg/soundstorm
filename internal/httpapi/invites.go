package httpapi

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/qr"
	"github.com/GabrielHollberg/soundstorm/internal/state"
)

// Inviting the family (the owner's asking, 2026-10-03): rather than making
// up a password for each person and telling it to them, the owner makes an
// invitation - a name - and shows its QR code, or sends its link. The person
// scans it, chooses their own password, and is in. An invitation works once,
// for seven days, and the owner can cancel it. Its token (128 random bits)
// is in the code alone; the server keeps a hash (state.Invites).

const inviteLife = 7 * 24 * time.Hour

var inviteToken = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)

// inviteURL is where an invitation's code points: the away-from-home name
// when remote access is on (so it works wherever the person is), else the
// home name, else the address the owner is using.
func (s *Server) inviteURL(r *http.Request, token string) string {
	if s.remoteStatus != nil {
		if st := s.remoteStatus(); st.Enabled && st.Name != "" && st.Port != 0 {
			return "https://" + st.Name + ":" + strconv.Itoa(st.Port) + "/invite/" + token
		}
	}
	return strings.TrimSuffix(s.linkURL(r, "x"), "/link/x") + "/invite/" + token
}

// POST /api/invites {name}: a new invitation, and its link.
func (s *Server) handleNewInvite(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "expected a name")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" || len(name) > 64 {
		writeError(w, http.StatusBadRequest, "Give the person's name.")
		return
	}
	if err := s.nameFree(name); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	token := base64.RawURLEncoding.EncodeToString(raw)
	inv := state.Invite{Name: name, By: actor.Name, Created: time.Now().UTC(), Expires: time.Now().Add(inviteLife).UTC()}
	if err := s.store.AddInvite(state.HashInvite(token), inv); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	url := s.inviteURL(r, token)
	writeJSON(w, http.StatusOK, map[string]any{
		"id": state.HashInvite(token)[:16], "name": name, "url": url, "expires": inv.Expires,
		"qr": "data:image/png;base64," + qrPNG(url),
	})
}

// qrPNG is a QR code of text as base64 PNG, or "".
func qrPNG(text string) string {
	code, err := qr.Encode(text)
	if err != nil {
		return ""
	}
	img, err := code.PNG(8)
	if err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(img)
}

// GET /api/invites: the invitations still waiting (not their links: those
// were shown once, when made).
func (s *Server) handleInvites(w http.ResponseWriter, r *http.Request) {
	out := []map[string]any{}
	for h, inv := range s.store.Invites() {
		out = append(out, map[string]any{"id": h[:16], "name": inv.Name, "expires": inv.Expires})
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["name"].(string) < out[j]["name"].(string) })
	writeJSON(w, http.StatusOK, map[string]any{"invites": out})
}

// DELETE /api/invites/{id}: cancelled.
func (s *Server) handleCancelInvite(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	for h := range s.store.Invites() {
		if len(id) == 16 && strings.HasPrefix(h, id) {
			_ = s.store.DeleteInvite(h)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

// handleInvitePage is the code's address: the page, which asks.
func (s *Server) handleInvitePage(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if !inviteToken.MatchString(token) {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, "/?invite="+token, http.StatusFound)
}

// inviteFor is the invitation a request's token names, limited per address
// so tokens cannot be tried one after another.
func (s *Server) inviteFor(w http.ResponseWriter, r *http.Request) (string, state.Invite, bool) {
	if !s.inviteTries.allow(clientOf(r), time.Now(), 20, 30*time.Second) {
		writeError(w, http.StatusTooManyRequests, "too many tries; wait a minute")
		return "", state.Invite{}, false
	}
	token := r.PathValue("token")
	if !inviteToken.MatchString(token) {
		writeError(w, http.StatusNotFound, "This invitation is not right, or has been used or cancelled.")
		return "", state.Invite{}, false
	}
	hash := state.HashInvite(token)
	inv, ok := s.store.InviteFor(hash)
	if !ok {
		writeError(w, http.StatusNotFound, "This invitation has been used, cancelled, or has run out. Ask for a new one.")
		return "", state.Invite{}, false
	}
	return hash, inv, true
}

// GET /api/invite/{token}: whom it is for, from whom, to which server.
func (s *Server) handleInviteLookup(w http.ResponseWriter, r *http.Request) {
	_, inv, ok := s.inviteFor(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": inv.Name, "by": inv.By, "server": s.store.ServerName()})
}

// POST /api/invite/{token} {username, password, keep}: the account made and
// signed in - no approval, the owner vouched - and the invitation used up.
func (s *Server) handleInviteAccept(w http.ResponseWriter, r *http.Request) {
	hash, inv, ok := s.inviteFor(w, r)
	if !ok {
		return
	}
	creds, err := decodeCredentials(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(creds.Username) == "" {
		creds.Username = inv.Name
	}
	if err := s.nameFree(creds.Username); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	var owner state.User
	for _, u := range s.store.Users() {
		if u.IsOwner() {
			owner = u
		}
	}
	created, err := s.auth.CreateUser(owner, creds.Username, creds.Password, state.RoleMember)
	if err != nil {
		writeError(w, statusFor(err), err.Error())
		return
	}
	_ = s.store.DeleteInvite(hash)
	token, expiry, err := s.auth.SessionFor(created)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "account made; sign in with it")
		return
	}
	s.auth.SetCookie(w, r, token, expiry)
	if err := s.auth.SetDeviceCookie(w, r, created); err != nil {
		s.log.Warn("could not mark this device as trusted", "err", err)
	}
	if creds.Keep {
		s.keepOnDevice(w, r, created)
	}
	s.log.Info("account made from an invitation", "username", created.Name, "invited by", inv.By)
	writeJSON(w, http.StatusOK, map[string]any{"signedIn": true, "user": s.withPicture(publicUser(created), created.ID)})
}
