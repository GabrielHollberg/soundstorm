// Command atrium is a unified front end for a self-hosted media library.
//
// It runs in front of Navidrome (music) and Jellyfin (video), provisions their
// credentials itself so nobody types an API key, and serves one login, one
// search box and one player over all of them. The backends need no published
// port: atrium is the only thing on one.
//
// It deliberately does not scan libraries, scrape metadata or transcode. Those
// are the things the servers behind it are good at, and reimplementing them is
// how a project like this dies.
//
// Configuration is entirely environment variables, set by docker compose. There
// is no config file on purpose: a file is one more thing to hand-edit, and the
// goal is that a human edits nothing.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gabehollberg/atrium/internal/auth"
	"github.com/gabehollberg/atrium/internal/httpapi"
	"github.com/gabehollberg/atrium/internal/library"
	"github.com/gabehollberg/atrium/internal/media"
	"github.com/gabehollberg/atrium/internal/provision"
	"github.com/gabehollberg/atrium/internal/source"
	"github.com/gabehollberg/atrium/internal/state"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel(env("ATRIUM_LOG_LEVEL", "info")),
	}))

	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	listen := env("ATRIUM_LISTEN", ":8080")
	stateDir := env("ATRIUM_STATE_DIR", "/var/lib/atrium")

	perSourceTimeout, err := time.ParseDuration(env("ATRIUM_PER_SOURCE_TIMEOUT", "5s"))
	if err != nil {
		return fmt.Errorf("ATRIUM_PER_SOURCE_TIMEOUT: %w", err)
	}

	// Before anything else: make sure the folders a person is supposed to put
	// media into actually exist. On a fresh install this is what turns "read
	// the README to learn the layout" into "the folders are already there".
	lib, err := library.Open(
		env("ATRIUM_LIBRARY_DIR", "/library"),
		env("ATRIUM_LIBRARY_HINT", "./library"),
		log,
	)
	if err != nil {
		return err
	}

	targets, err := targetsFromEnv(lib)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return errors.New("no backends configured; set at least one of ATRIUM_NAVIDROME_URL, " +
			"ATRIUM_JELLYFIN_URL, ATRIUM_AUDIOBOOKSHELF_URL")
	}

	store, err := state.Open(filepath.Join(stateDir, "state.json"))
	if err != nil {
		return err
	}

	registry := source.NewRegistry()
	setup := provision.New(store, registry, log, targets)

	// Shut down cleanly on Ctrl-C or SIGTERM from the container runtime.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Provisioning runs in the background: a backend can take a minute to boot
	// and atrium should be showing setup progress during it, not refusing to
	// start. This is why the registry is populated asynchronously.
	setup.Start(ctx)

	api := httpapi.New(httpapi.Config{
		Registry:         registry,
		Store:            store,
		Library:          lib,
		Auth:             auth.New(store),
		Setup:            setup,
		PerSourceTimeout: perSourceTimeout,
		Log:              log,
	})

	srv := &http.Server{
		Addr:              listen,
		Handler:           api.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: it would cut off a film mid-playback.
	}

	accountState := "an account exists"
	if store.User() == nil {
		accountState = "no account yet - first visit creates it"
	}
	log.Info("atrium starting",
		"listen", listen,
		"backends", len(targets),
		"library", lib.Root(),
		"state", stateDir,
		"account", accountState,
	)

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

// targetsFromEnv reads which backends to manage. A backend is present if its URL
// is set, so compose decides the stack and atrium adapts.
func targetsFromEnv(lib *library.Library) ([]provision.Target, error) {
	var targets []provision.Target

	if url := strings.TrimSpace(os.Getenv("ATRIUM_NAVIDROME_URL")); url != "" {
		targets = append(targets, provision.Target{
			ID:      "navidrome",
			Type:    "navidrome",
			BaseURL: url,
		})
	}
	if url := strings.TrimSpace(os.Getenv("ATRIUM_JELLYFIN_URL")); url != "" {
		targets = append(targets, provision.Target{
			ID:      "jellyfin",
			Type:    "jellyfin",
			BaseURL: url,
			// The path as Jellyfin's container sees it, which is what its
			// library API needs - not atrium's view of the same folder.
			MediaPath: env("ATRIUM_JELLYFIN_MEDIA_PATH", "/media/movies"),
		})
	}
	if url := strings.TrimSpace(os.Getenv("ATRIUM_AUDIOBOOKSHELF_URL")); url != "" {
		targets = append(targets, provision.Target{
			ID:        "audiobookshelf",
			Type:      "audiobookshelf",
			BaseURL:   url,
			MediaPath: env("ATRIUM_AUDIOBOOKSHELF_MEDIA_PATH", "/audiobooks"),
		})
	}
	// Ebooks are served straight off the disk: an EPUB describes itself, so no
	// backend has to stand between atrium and the folder. The path comes from
	// the library layout rather than its own variable - there is one answer to
	// "where do ebooks live" and it should not be configurable into disagreeing
	// with the folder atrium just created.
	if dir := lib.PathFor(media.KindEbook); dir != "" {
		targets = append(targets, provision.Target{
			ID:        "ebooks",
			Type:      "localbooks",
			MediaPath: dir,
		})
	}
	// Escape hatch for an existing Calibre server elsewhere on the network.
	// This one does need credentials typed, which is why it is not the default.
	if url := strings.TrimSpace(os.Getenv("ATRIUM_CALIBREWEB_URL")); url != "" {
		targets = append(targets, provision.Target{
			ID:        "calibreweb",
			Type:      "calibreweb",
			BaseURL:   url,
			MediaPath: env("ATRIUM_CALIBREWEB_MEDIA_PATH", "/books"),
		})
	}
	return targets, nil
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func logLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
