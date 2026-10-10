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
// EmberStorm's container is given. It can ask, never command anything else:
//
//	GET  /status            what is running, what is available
//	POST /check             look for an update now
//	POST /update            install the available update now (Update now)
//	GET  /settings          {"auto": true}
//	PUT  /settings          change it
//	POST /reset             {"mode": "start-over"|"erase"} (reset.go); erase
//	                        waits for five presses of the button at the box
//	GET  /button            {"open": true, "until": ...} after five presses,
//	                        "erase": until when an erase waits for them
//	POST /button/claim      {"code"}: the code on the box's screen, or the
//	                        sticker's setup code, closing the window
//	POST /button/used       closed again
//
// EmberStorm decides who may press these (the owner); the caretaker trusts
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
		// Once a minute at most, whoever asks.
		if last := u.Status().LastCheck; last == nil || time.Since(*last) > time.Minute {
			_, _ = u.Check(r.Context())
		}
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
		// It outlives the request: the app reloads while EmberStorm restarts.
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
	mux.HandleFunc("POST /reset", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Mode ResetMode `json:"mode"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil ||
			(body.Mode != ResetStartOver && body.Mode != ResetErase) {
			http.Error(w, ErrBadMode.Error(), http.StatusBadRequest)
			return
		}
		if !u.busy.TryLock() {
			http.Error(w, ErrBusy.Error(), http.StatusConflict)
			return
		}
		u.busy.Unlock()
		// Erasing and starting over wait for somebody at the box: ten
		// presses of its power button within EraseWait start it (Pressed).
		// Asked for through this socket alone - which the app's container
		// shares - nothing that cannot be undone happens (the owner's
		// choice, 2026-10-10; start over too after the box's blind review).
		u.ArmReset(body.Mode)
		w.WriteHeader(http.StatusAccepted)
		reply(w, map[string]any{"mode": body.Mode, "waiting": "button", "presses": ResetPresses, "until": time.Now().Add(EraseWait)})
	})
	mux.HandleFunc("POST /reset/cancel", func(w http.ResponseWriter, r *http.Request) {
		u.CancelReset()
		reply(w, map[string]any{"waiting": false})
	})
	// USB drives plugged in, opened only when the owner says so in the app
	// (drives.go). The app's container decides who may ask.
	mux.HandleFunc("GET /drives", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{"drives": u.waitingDrives()})
	})
	mux.HandleFunc("POST /drives/open", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Part string `json:"part"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil {
			http.Error(w, "expected a part", http.StatusBadRequest)
			return
		}
		open, err := u.openDrive(r.Context(), body.Part)
		if errors.Is(err, errNoDrive) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		reply(w, map[string]any{"open": open})
	})
	mux.HandleFunc("GET /button", func(w http.ResponseWriter, r *http.Request) {
		open, until := u.buttonOpen()
		// Never the code: that is for the box's own screen (ButtonCodeFile),
		// or anything able to reach this socket - the app's container -
		// could claim the window before the person at the box (the
		// thirteenth security pass).
		out := map[string]any{"open": open}
		if open {
			out["until"] = until
		}
		if mode, by := u.resetArmed(); mode != "" {
			out["reset"], out["resetUntil"] = mode, by
		}
		reply(w, out)
	})
	mux.HandleFunc("POST /button/claim", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Code string `json:"code"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil || !u.claimButton(body.Code) {
			w.WriteHeader(http.StatusForbidden)
			reply(w, map[string]any{"claimed": false})
			return
		}
		reply(w, map[string]any{"claimed": true})
	})
	mux.HandleFunc("POST /button/used", func(w http.ResponseWriter, r *http.Request) {
		u.closeButton()
		reply(w, map[string]any{"open": false})
	})
	return mux
}

// soundstormGID is the group of EmberStorm's container user (its Dockerfile),
// which the socket is shared with.
const soundstormGID = 10001

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
	// Only root, and the group the socket is shared with - EmberStorm's
	// container user's (10001) - may open it.
	_ = os.Chmod(socket, 0o660)
	_ = os.Chown(socket, 0, soundstormGID)
	// Idle connections closed: the app's container could otherwise hold the
	// root daemon's descriptors open (the box's blind security review).
	srv := &http.Server{Handler: u.Handler(), ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout: time.Minute, IdleTimeout: 30 * time.Second}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	go func() {
		u.recoverPending(ctx)
		u.schedule(ctx)
	}()
	go u.watchButton(ctx)
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
		// Off, the owner is asked first - for a month: a release still not
		// installed then is installed anyway, so updates cannot be put off
		// for good, by the owner or by anything able to reach this socket
		// (the twelfth security pass).
		if err != nil || m == nil || (!u.settings().Auto && time.Since(m.Created) < maxPutOff) {
			continue
		}
		// At night: wait for the window, then install.
		if h := time.Now().Hour(); h < 2 || h >= 5 {
			wait = untilHour(time.Now(), 2) + time.Duration(time.Now().UnixNano()%int64(time.Hour))
			continue
		}
		// Only once EmberStorm has started: the caretaker no longer waits for
		// it at boot (the power button must work from the start), and a box's
		// first start - its programs loading - is no time to change them.
		if err := u.run(ctx, "systemctl", "is-active", "--quiet", "soundstorm.service"); err != nil {
			wait = 10 * time.Minute
			continue
		}
		_ = u.Update(ctx, m)
	}
}

// maxPutOff is how long an update can wait for the owner with Auto off.
const maxPutOff = 30 * 24 * time.Hour

// untilHour is how long from now until the next time it is h o'clock.
func untilHour(now time.Time, h int) time.Duration {
	next := time.Date(now.Year(), now.Month(), now.Day(), h, 0, 0, 0, now.Location())
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next.Sub(now)
}
