package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/collections"
	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/scrobble"
)

// Scrobbling: each person may connect their own ListenBrainz account, and
// their plays are sent to it from here (internal/scrobble). A play is queued
// in their collections file as it is recorded, then sent in the background;
// what fails to go waits and is tried again after the next play and every
// quarter hour, so plays made while the service was down arrive late rather
// than never. Per person and opt-in by pasting a token: it sends that
// person's own listening to that person's own account.

const (
	scrobbleEvery   = 15 * time.Minute
	scrobbleTimeout = 30 * time.Second
	maxTokenLength  = 200
)

func (s *Server) handleScrobbleStatus(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	sc, connected, err := s.collections.ScrobblerFor(user.ID)
	if err != nil {
		s.collectionsError(w, err)
		return
	}
	answer := map[string]any{"available": s.scrobble != nil, "connected": connected}
	if connected {
		// Never the token.
		answer["service"], answer["userName"], answer["pending"] = sc.Service, sc.UserName, len(sc.Pending)
		if sc.Problem != "" {
			answer["problem"] = sc.Problem
		}
	}
	writeJSON(w, http.StatusOK, answer)
}

func (s *Server) handleScrobbleConnect(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	if s.scrobble == nil {
		writeError(w, http.StatusNotImplemented, "scrobbling is not available")
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "expected a JSON body with token")
		return
	}
	token := strings.TrimSpace(body.Token)
	if token == "" || len(token) > maxTokenLength || strings.ContainsAny(token, " \t\r\n") {
		writeError(w, http.StatusBadRequest, "that does not look like a ListenBrainz token")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	name, err := s.scrobble.Validate(ctx, token)
	if errors.Is(err, scrobble.ErrBadToken) {
		writeError(w, http.StatusBadRequest, "ListenBrainz does not recognize that token")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "ListenBrainz could not be reached just now")
		return
	}
	next := &collections.Scrobbler{Service: "listenbrainz", Token: token, UserName: name}
	// A reconnect after a refused token keeps the plays that queued meanwhile.
	if old, ok, _ := s.collections.ScrobblerFor(user.ID); ok {
		next.Pending = old.Pending
	}
	if err := s.collections.SetScrobbler(user.ID, next); err != nil {
		s.collectionsError(w, err)
		return
	}
	go s.sendScrobbles(user.ID)
	writeJSON(w, http.StatusOK, map[string]any{"connected": true, "service": "listenbrainz", "userName": name})
}

func (s *Server) handleScrobbleDisconnect(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	if err := s.collections.SetScrobbler(user.ID, nil); err != nil {
		s.collectionsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connected": false})
}

// handleNowPlaying tells somebody's service what they just started.
func (s *Server) handleNowPlaying(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var body struct {
		Source string `json:"source"`
		ID     string `json:"id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxCollectionBody)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "expected a JSON body with source and id")
		return
	}
	sc, connected, err := s.collections.ScrobblerFor(user.ID)
	if err != nil || !connected || s.scrobble == nil || sc.Problem != "" ||
		!s.nowPlaying.allow(user.ID, time.Now(), playBurst, playEvery) {
		writeJSON(w, http.StatusOK, map[string]any{"sent": false})
		return
	}
	item, ok := s.resolveItem(w, r, body.Source, body.ID)
	if !ok {
		return
	}
	if item.Kind != media.KindMusic {
		writeJSON(w, http.StatusOK, map[string]any{"sent": false})
		return
	}
	go func() {
		defer func() { _ = recover() }()
		ctx, cancel := context.WithTimeout(context.Background(), scrobbleTimeout)
		defer cancel()
		_ = s.scrobble.NowPlaying(ctx, sc.Token, collections.ListenOf(item, time.Now()))
	}()
	writeJSON(w, http.StatusOK, map[string]any{"sent": true})
}

// sendScrobbles sends what is waiting for one person, one send at a time
// per person. Nothing waits on it.
func (s *Server) sendScrobbles(userID string) {
	if s.scrobble == nil {
		return
	}
	if _, busy := s.scrobbling.LoadOrStore(userID, true); busy {
		return
	}
	defer s.scrobbling.Delete(userID)
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("scrobbling panicked; recovered", "panic", r, "stack", string(debug.Stack()))
		}
	}()
	sc, connected, err := s.collections.ScrobblerFor(userID)
	if err != nil || !connected || len(sc.Pending) == 0 || sc.Problem != "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), scrobbleTimeout)
	defer cancel()
	done, dropped, err := s.scrobble.Submit(ctx, sc.Token, sc.Pending)
	if dropped > 0 {
		s.log.Warn("ListenBrainz refused some plays; they will not be sent", "dropped", dropped)
	}
	if len(done) > 0 {
		if serr := s.collections.Sent(userID, done); serr != nil {
			s.log.Warn("could not take sent listens off the queue", "err", serr)
		}
	}
	if errors.Is(err, scrobble.ErrBadToken) {
		// Revoked or changed on their side: stop, and say so, until they
		// connect again. Their plays keep queueing meanwhile.
		// Only the problem changes: writing back a copy read earlier would
		// lose plays queued since.
		if perr := s.collections.SetScrobblerProblem(userID,
			"ListenBrainz no longer accepts the token. Connect again with a new one."); perr != nil {
			s.log.Warn("could not record the refused token", "err", perr)
		}
		return
	}
	if err != nil {
		s.log.Debug("scrobbles will be tried again", "err", err)
	}
}

// RunScrobbles tries every queue again every quarter hour, for plays made
// while a service was unreachable. Started once with go, so it recovers its
// own panics.
func (s *Server) RunScrobbles(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("scrobble retries panicked; recovered", "panic", r, "stack", string(debug.Stack()))
		}
	}()
	if s.scrobble == nil {
		return
	}
	tick := time.NewTicker(scrobbleEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		for _, u := range s.store.Users() {
			s.sendScrobbles(u.ID)
		}
	}
}
