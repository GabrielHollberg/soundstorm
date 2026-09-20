// Package provision gets atrium credentials on each backend without a human
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
// booting, which is the normal case under `docker compose up`: atrium is
// listening seconds after start, Jellyfin takes the better part of a minute.
package provision

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/gabehollberg/atrium/internal/httpx"
	"github.com/gabehollberg/atrium/internal/source"
	"github.com/gabehollberg/atrium/internal/source/jellyfin"
	"github.com/gabehollberg/atrium/internal/source/subsonic"
	"github.com/gabehollberg/atrium/internal/state"
)

// Status is where a backend is in its setup.
type Status string

const (
	StatusWaiting      Status = "waiting"      // backend not reachable yet
	StatusProvisioning Status = "provisioning" // walking its first-run flow
	StatusReady        Status = "ready"        // searchable
	StatusFailed       Status = "failed"       // gave up; atrium still serves
)

// accountName is the account atrium creates for itself on every backend. It is
// deliberately recognizable: someone poking at Navidrome later should be able
// to tell which account is the gateway's.
const accountName = "atrium"

// giveUpAfter bounds how long we keep retrying a backend that never comes up.
// A backend can be genuinely absent (image failed to pull, wrong URL) and
// atrium must stay useful for the ones that did work.
const giveUpAfter = 10 * time.Minute

// Target is a backend atrium should bring under its wing.
type Target struct {
	ID      string // source id, appears in stream URLs: "navidrome"
	Type    string // "navidrome" or "jellyfin"
	BaseURL string // internal URL, not published: http://navidrome:4533

	// MediaPath is the folder, as the *backend* container sees it, that the
	// backend should serve. Only used by backends that need an API call to
	// register a library; Navidrome takes it as an env var instead.
	MediaPath string
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
// returns immediately. atrium serves (and shows setup progress) while this runs.
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

	// Already provisioned? Try those credentials first. They are only stale if
	// someone reset the backend's volume without resetting atrium's.
	if creds, ok := m.store.Backend(t.ID); ok {
		if err := m.register(ctx, t, creds); err == nil {
			log.Info("backend ready from stored credentials")
			m.set(t.ID, StatusReady, "using stored credentials", "")
			return
		} else {
			log.Warn("stored credentials rejected, re-provisioning", "err", err)
		}
	}

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
	default:
		return state.Backend{}, fmt.Errorf("unknown backend type %q", t.Type)
	}
}

// register builds the adapter, checks it actually works, and publishes it.
func (m *Manager) register(ctx context.Context, t Target, creds state.Backend) error {
	var (
		s   source.Source
		err error
	)
	switch t.Type {
	case "navidrome":
		s, err = subsonic.New(subsonic.Config{
			ID:       t.ID,
			BaseURL:  t.BaseURL,
			Username: creds.Username,
			Password: creds.Password,
			Timeout:  15 * time.Second,
		})
	case "jellyfin":
		s, err = jellyfin.New(jellyfin.Config{
			ID:      t.ID,
			BaseURL: t.BaseURL,
			Token:   creds.Token,
			UserID:  creds.UserID,
			Timeout: 15 * time.Second,
		})
	default:
		return fmt.Errorf("unknown backend type %q", t.Type)
	}
	if err != nil {
		return err
	}

	// Health-check before publishing, so a source in the registry is a source
	// that answers. Otherwise the first search after boot reports a failure the
	// user can do nothing about.
	hctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := s.Health(hctx); err != nil {
		return fmt.Errorf("health check: %w", err)
	}

	m.reg.Set(s)
	return nil
}

// generatePassword returns a password no human will ever see or type.
func generatePassword() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate password: %w", err)
	}
	return hex.EncodeToString(raw), nil
}
