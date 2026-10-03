package caretaker

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Settings is what the owner chooses, kept in the state folder.
type Settings struct {
	// Auto updates at night; off means the app asks first.
	Auto bool `json:"auto"`
}

func (u *Updater) settings() Settings {
	s := Settings{Auto: true}
	if data, err := os.ReadFile(filepath.Join(u.cfg.StateDir, "settings.json")); err == nil {
		_ = json.Unmarshal(data, &s)
	}
	return s
}

func (u *Updater) saveSettings(s Settings) error {
	if err := os.MkdirAll(u.cfg.StateDir, 0o755); err != nil {
		return err
	}
	data, _ := json.Marshal(s)
	return writeFile(filepath.Join(u.cfg.StateDir, "settings.json"), data)
}

// Handler is the caretaker's door, served on a Unix socket that only
// SoundStorm's container is given. It can ask, never command anything else:
//
//	GET  /status            what is running, what is available
//	POST /check             look for an update now
//	POST /update            install the available update now (Update now)
//	GET  /settings          {"auto": true}
//	PUT  /settings          change it
//
// SoundStorm decides who may press these (the owner); the caretaker trusts
// whoever reaches the socket, which is why nothing else is ever given it.
func (u *Updater) Handler() http.Handler {
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		reply(w, u.Status())
	})
	mux.HandleFunc("POST /check", func(w http.ResponseWriter, r *http.Request) {
		_, _ = u.Check(r.Context())
		reply(w, u.Status())
	})
	mux.HandleFunc("POST /update", func(w http.ResponseWriter, r *http.Request) {
		m := u.Status().Available
		if m == nil {
			http.Error(w, "no update is available", http.StatusConflict)
			return
		}
		if !u.busy.TryLock() {
			http.Error(w, ErrBusy.Error(), http.StatusConflict)
			return
		}
		u.busy.Unlock()
		// It outlives the request: the app reloads while SoundStorm restarts.
		go func() { _ = u.Update(context.Background(), m) }()
		w.WriteHeader(http.StatusAccepted)
		reply(w, u.Status())
	})
	mux.HandleFunc("GET /settings", func(w http.ResponseWriter, r *http.Request) {
		reply(w, u.settings())
	})
	mux.HandleFunc("PUT /settings", func(w http.ResponseWriter, r *http.Request) {
		var s Settings
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&s); err != nil {
			http.Error(w, "bad settings", http.StatusBadRequest)
			return
		}
		if err := u.saveSettings(s); err != nil {
			http.Error(w, "could not save", http.StatusInternalServerError)
			return
		}
		reply(w, s)
	})
	return mux
}

// Serve answers on the socket and checks for updates in the background:
// shortly after start, then every six hours, installing one at night (2 to 5
// in the morning, the box's time) when the owner has left Auto on.
func (u *Updater) Serve(ctx context.Context, socket string) error {
	if err := os.MkdirAll(filepath.Dir(socket), 0o755); err != nil {
		return err
	}
	_ = os.Remove(socket)
	ln, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	// Only root, and the group the socket is shared with, may open it.
	_ = os.Chmod(socket, 0o660)
	srv := &http.Server{Handler: u.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	go u.schedule(ctx)
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (u *Updater) schedule(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			u.log.Error("update schedule stopped", "panic", r)
		}
	}()
	wait := 3 * time.Minute
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = 6 * time.Hour
		m, err := u.Check(ctx)
		if err != nil || m == nil || !u.settings().Auto {
			continue
		}
		// At night: wait for the window, then install.
		if h := time.Now().Hour(); h < 2 || h >= 5 {
			wait = untilHour(time.Now(), 2) + time.Duration(time.Now().UnixNano()%int64(time.Hour))
			continue
		}
		_ = u.Update(ctx, m)
	}
}

// untilHour is how long from now until the next time it is h o'clock.
func untilHour(now time.Time, h int) time.Duration {
	next := time.Date(now.Year(), now.Month(), now.Day(), h, 0, 0, 0, now.Location())
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next.Sub(now)
}
