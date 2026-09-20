// Package provision gets SoundStorm credentials on each backend without a human
// typing anything.
//
// This is the load-bearing package of the whole product. Installing four media
// servers is a solved problem - Umbrel, CasaOS and Unraid all do it. What none
// of them do is finish the job: you end up with four admin accounts to create,
// four API keys to mint and four logins to remember. Everything in here exists
// to delete that afternoon of setup.
//
// The shape of it: for each backend, either we already have credentials in the
// state file, or we walk its first-run flow ourselves - create an account with
// a generated password, log in as it, point it at the right media folder - and
// write what we get to state. The human sees a progress list, not a form.
//
// Provisioning runs in the background and tolerates a backend that is still
// booting, which is the normal case under `docker compose up`: SoundStorm is
// listening seconds after start, Jellyfin takes the better part of a minute.
package provision

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"syscall"
	"time"

	"github.com/gabehollberg/soundstorm/internal/httpx"
	"github.com/gabehollberg/soundstorm/internal/media"
	"github.com/gabehollberg/soundstorm/internal/source"
	"github.com/gabehollberg/soundstorm/internal/source/audiobookshelf"
	"github.com/gabehollberg/soundstorm/internal/source/jellyfin"
	"github.com/gabehollberg/soundstorm/internal/source/localbooks"
	"github.com/gabehollberg/soundstorm/internal/source/opds"
	"github.com/gabehollberg/soundstorm/internal/source/subsonic"
	"github.com/gabehollberg/soundstorm/internal/state"
)

// Status is where a backend is in its setup.
type Status string

const (
	StatusWaiting      Status = "waiting"      // backend not reachable yet
	StatusProvisioning Status = "provisioning" // walking its first-run flow
	StatusReady        Status = "ready"        // searchable
	StatusFailed       Status = "failed"       // gave up; SoundStorm still serves
)

// accountName is the account SoundStorm creates for itself on every backend. It
// is deliberately recognizable: someone poking at Navidrome later should be
// able to tell which account is the gateway's.
const accountName = "soundstorm"

// giveUpAfter bounds how long we keep retrying a backend that never comes up.
// A backend can be genuinely absent (image failed to pull, wrong URL) and
// SoundStorm must stay useful for the ones that did work.
const giveUpAfter = 10 * time.Minute

// Target is a backend SoundStorm should bring under its wing.
type Target struct {
	ID      string // source id, appears in stream URLs: "navidrome"
	Type    string // "navidrome" or "jellyfin"
	BaseURL string // internal URL, not published: http://navidrome:4533

	// MediaPath is the folder, as the *backend* container sees it, that the
	// backend should serve. Only used by backends that need an API call to
	// register a library; Navidrome takes it as an env var instead.
	MediaPath string

	// TVPath is the series folder, for backends that hold two libraries.
	// Jellyfin is the only one: films and series need separate libraries
	// because it scrapes and models them differently.
	TVPath string
}

// BackendStatus is one backend's setup state, for the UI.
type BackendStatus struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Status Status `json:"status"`
	Detail string `json:"detail,omitempty"`
	Error  string `json:"error,omitempty"`
}

// Manager runs provisioning and reports progress.
type Manager struct {
	store *state.Store
	reg   *source.Registry
	log   *slog.Logger

	targets []Target

	mu       sync.RWMutex
	statuses map[string]*BackendStatus
	order    []string
}

// New builds a Manager for the given targets.
func New(store *state.Store, reg *source.Registry, log *slog.Logger, targets []Target) *Manager {
	m := &Manager{
		store:    store,
		reg:      reg,
		log:      log,
		targets:  targets,
		statuses: make(map[string]*BackendStatus, len(targets)),
	}
	for _, t := range targets {
		m.statuses[t.ID] = &BackendStatus{ID: t.ID, Type: t.Type, Status: StatusWaiting}
		m.order = append(m.order, t.ID)
	}
	return m
}

// Start kicks off provisioning for every target, one goroutine each, and
// returns immediately. SoundStorm serves (and shows setup progress) while this
// runs.
func (m *Manager) Start(ctx context.Context) {
	for _, t := range m.targets {
		go m.run(ctx, t)
	}
}

// Statuses returns per-backend setup state in configuration order.
func (m *Manager) Statuses() []BackendStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]BackendStatus, 0, len(m.order))
	for _, id := range m.order {
		if st, ok := m.statuses[id]; ok {
			out = append(out, *st)
		}
	}
	return out
}

// AllReady reports whether every backend finished setup.
func (m *Manager) AllReady() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, st := range m.statuses {
		if st.Status != StatusReady {
			return false
		}
	}
	return true
}

func (m *Manager) set(id string, status Status, detail, errMsg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.statuses[id]
	if !ok {
		return
	}
	st.Status = status
	st.Detail = detail
	st.Error = errMsg
}

// run brings one backend from "just a URL" to "registered and searchable".
func (m *Manager) run(ctx context.Context, t Target) {
	log := m.log.With("backend", t.ID)

	if creds, ok := m.store.Backend(t.ID); ok {
		m.reconnect(ctx, t, creds, log)
		return
	}
	m.provision(ctx, t, log)
}

// reconnect brings a backend back using credentials we already hold.
//
// The subtlety that cost a debugging session: on a restart, SoundStorm is
// listening seconds after the container starts and Jellyfin is not. A health
// check then fails with "connection refused", which is emphatically NOT the
// same as "this token is wrong" - but treating them alike meant throwing away
// good credentials and falling through to provisioning, which can never succeed
// on a backend that is already set up. One unlucky restart and the backend was
// permanently broken with its working credentials still on disk.
//
// So: a transport failure means wait and try again. Only an answer from the
// backend that rejects us justifies re-provisioning, and that path still exists
// because wiping a backend's volume while keeping SoundStorm's is a real thing
// to do.
func (m *Manager) reconnect(ctx context.Context, t Target, creds state.Backend, log *slog.Logger) {
	deadline := time.Now().Add(giveUpAfter)
	delay := 2 * time.Second

	for attempt := 1; ; attempt++ {
		if ctx.Err() != nil {
			return
		}

		err := m.register(ctx, t, creds)
		if err == nil {
			log.Info("backend ready from stored credentials", "attempts", attempt)
			m.set(t.ID, StatusReady, "using stored credentials", "")
			return
		}

		if !transient(err) {
			// The backend answered and refused us. Either its volume was reset
			// (provisioning will work) or something else is wrong (provisioning
			// will fail with a message naming the problem).
			log.Warn("stored credentials rejected by the backend, re-provisioning", "err", err)
			m.provision(ctx, t, log)
			return
		}

		if time.Now().After(deadline) {
			log.Error("backend never came up", "attempts", attempt, "err", err)
			m.set(t.ID, StatusFailed, "", "not reachable: "+err.Error())
			return
		}

		m.set(t.ID, StatusWaiting, fmt.Sprintf("waiting for %s to start (attempt %d)", t.Type, attempt), "")
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		if delay < 15*time.Second {
			delay += 2 * time.Second
		}
	}
}

// transient reports whether err means "the backend is not up yet" rather than
// "the backend said no". Getting this distinction wrong is what made a restart
// destroy working credentials, and it bites at two separate layers:
//
//   - the transport layer, when nothing is listening yet: connection refused,
//     DNS failure, timeout
//   - the HTTP layer, when something IS listening but is not ready. Jellyfin
//     answers "503 Jellyfin Server is loading. Please try again shortly." for
//     several seconds after its port opens.
//
// Only an actual refusal - 401 or 403 - means the credentials are wrong.
func transient(err error) bool {
	var statusErr *httpx.StatusError
	if errors.As(err, &statusErr) {
		return statusErr.Temporary()
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, syscall.ECONNREFUSED)
}

// provision walks a backend's first-run flow and registers the result.
func (m *Manager) provision(ctx context.Context, t Target, log *slog.Logger) {
	deadline := time.Now().Add(giveUpAfter)
	delay := 2 * time.Second

	for attempt := 1; ; attempt++ {
		if ctx.Err() != nil {
			return
		}

		creds, err := m.provisionOnce(ctx, t, log)
		if err == nil {
			if err = m.store.SetBackend(t.ID, creds); err != nil {
				log.Error("could not persist credentials", "err", err)
				m.set(t.ID, StatusFailed, "", "could not save credentials: "+err.Error())
				return
			}
			if err = m.register(ctx, t, creds); err == nil {
				log.Info("backend provisioned and ready", "attempts", attempt)
				m.set(t.ID, StatusReady, "", "")
				return
			}
			log.Warn("provisioned but not usable yet", "err", err)
		}

		if time.Now().After(deadline) {
			log.Error("giving up on backend", "attempts", attempt, "err", err)
			m.set(t.ID, StatusFailed, "", err.Error())
			return
		}

		m.set(t.ID, StatusWaiting, fmt.Sprintf("waiting for %s (attempt %d)", t.Type, attempt), "")
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		if delay < 15*time.Second {
			delay += 2 * time.Second
		}
	}
}

// provisionOnce runs the backend-specific first-run flow.
func (m *Manager) provisionOnce(ctx context.Context, t Target, log *slog.Logger) (state.Backend, error) {
	// A local folder has no host to talk to, so it never gets an HTTP client.
	if t.Type == "localbooks" {
		m.set(t.ID, StatusProvisioning, "reading the ebook folder", "")
		return provisionLocalBooks(ctx, t, log)
	}

	c, err := httpx.New(t.BaseURL, 30*time.Second)
	if err != nil {
		return state.Backend{}, err
	}

	switch t.Type {
	case "navidrome":
		m.set(t.ID, StatusProvisioning, "creating Navidrome account", "")
		return provisionNavidrome(ctx, c, log)
	case "jellyfin":
		m.set(t.ID, StatusProvisioning, "running Jellyfin setup", "")
		return provisionJellyfin(ctx, c, t, log)
	case "audiobookshelf":
		m.set(t.ID, StatusProvisioning, "creating Audiobookshelf account", "")
		return provisionAudiobookshelf(ctx, c, t, log)
	case "calibreweb":
		m.set(t.ID, StatusProvisioning, "configuring Calibre-Web", "")
		return provisionCalibreWeb(ctx, c, t, log)
	default:
		return state.Backend{}, fmt.Errorf("unknown backend type %q", t.Type)
	}
}

// register builds a backend's adapters, checks they work, and publishes them.
//
// One backend can produce more than one source. Jellyfin does: films and series
// are separate Jellyfin libraries with separate scrapers, so they become two
// SoundStorm sources sharing one token - which is what the Source interface
// always described and could not actually do until jellyfin.Config grew a Kind.
func (m *Manager) register(ctx context.Context, t Target, creds state.Backend) error {
	sources, err := m.buildSources(t, creds)
	if err != nil {
		return err
	}

	for _, s := range sources {
		// Some sources have to do work before they can answer anything. A local
		// book library reads the disk here, which on a big library is the
		// slowest step in the whole startup - hence no timeout around it, and
		// the status line saying what is happening.
		if starter, ok := s.(source.Starter); ok {
			if err := starter.Start(ctx); err != nil {
				return fmt.Errorf("start %s: %w", s.ID(), err)
			}
		}

		// Health-check before publishing, so a source in the registry is a
		// source that answers. Otherwise the first search after boot reports a
		// failure the user can do nothing about.
		hctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		err := s.Health(hctx)
		cancel()
		if err != nil {
			return fmt.Errorf("health check %s: %w", s.ID(), err)
		}
	}

	for _, s := range sources {
		m.reg.Set(s)
	}
	return nil
}

// buildSources turns stored credentials into live adapters.
func (m *Manager) buildSources(t Target, creds state.Backend) ([]source.Source, error) {
	switch t.Type {
	case "navidrome":
		s, err := subsonic.New(subsonic.Config{
			ID:       t.ID,
			BaseURL:  t.BaseURL,
			Username: creds.Username,
			Password: creds.Password,
			Timeout:  15 * time.Second,
		})
		return one(s, err)

	case "jellyfin":
		films, err := jellyfin.New(jellyfin.Config{
			ID:        t.ID,
			BaseURL:   t.BaseURL,
			Token:     creds.Token,
			UserID:    creds.UserID,
			Kind:      media.KindVideo,
			ItemTypes: "Movie",
			Timeout:   15 * time.Second,
		})
		if err != nil {
			return nil, err
		}
		if t.TVPath == "" {
			return []source.Source{films}, nil
		}
		// Series and episodes both, so searching a show name finds the show and
		// searching an episode title finds the episode.
		shows, err := jellyfin.New(jellyfin.Config{
			ID:        t.ID + "-tv",
			BaseURL:   t.BaseURL,
			Token:     creds.Token,
			UserID:    creds.UserID,
			Kind:      media.KindTV,
			ItemTypes: "Series,Episode",
			Timeout:   15 * time.Second,
		})
		if err != nil {
			return nil, err
		}
		return []source.Source{films, shows}, nil

	case "audiobookshelf":
		s, err := audiobookshelf.New(audiobookshelf.Config{
			ID:        t.ID,
			BaseURL:   t.BaseURL,
			Token:     creds.Token,
			LibraryID: creds.LibraryID,
			Timeout:   15 * time.Second,
		})
		return one(s, err)

	case "calibreweb":
		s, err := opds.New(opds.Config{
			ID:       t.ID,
			BaseURL:  t.BaseURL,
			Username: creds.Username,
			Password: creds.Password,
			Timeout:  15 * time.Second,
		})
		return one(s, err)

	case "localbooks":
		s, err := localbooks.New(localbooks.Config{
			ID:   t.ID,
			Root: t.MediaPath,
			Log:  m.log,
		})
		return one(s, err)

	default:
		return nil, fmt.Errorf("unknown backend type %q", t.Type)
	}
}

// one wraps the common single-source case.
func one[T source.Source](s T, err error) ([]source.Source, error) {
	if err != nil {
		return nil, err
	}
	return []source.Source{s}, nil
}

// basicAuth builds an HTTP Basic credential.
func basicAuth(username, password string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
}

// generatePassword returns a password no human will ever see or type.
func generatePassword() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate password: %w", err)
	}
	return hex.EncodeToString(raw), nil
}
