// Command SoundStorm is a unified front end for a self-hosted media library.
//
// It runs in front of Navidrome (music) and Jellyfin (video), provisions their
// credentials itself so nobody types an API key, and serves one login, one
// search box and one player over all of them. The backends need no published
// port: SoundStorm is the only thing on one.
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

	"github.com/GabrielHollberg/soundstorm/internal/auth"
	"github.com/GabrielHollberg/soundstorm/internal/httpapi"
	"github.com/GabrielHollberg/soundstorm/internal/library"
	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/provision"
	"github.com/GabrielHollberg/soundstorm/internal/servetls"
	"github.com/GabrielHollberg/soundstorm/internal/source"
	"github.com/GabrielHollberg/soundstorm/internal/starter"
	"github.com/GabrielHollberg/soundstorm/internal/state"
)

func main() {
	// Recovery lives wherever the server lives: somebody locked out of their
	// own install, or holding a backup and an empty volume, should not also
	// have to find a second tool. Anything else falls through and starts the
	// server, so an unrecognised argument is not silently swallowed.
	// Any argument at all is a subcommand, because the server itself takes
	// none - it is configured entirely by environment variables.
	//
	// An unknown one is an error, and that is not pedantry. This used to fall
	// through and start the server, so `soundstorm backup` against an older
	// image quietly launched a *second* SoundStorm against the same state
	// volume instead of saying the command did not exist. Two writers on the
	// one file that cannot be regenerated is the worst possible way to answer
	// a typo.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "reset-password":
			os.Exit(resetPassword(os.Args[2:]))
		case "backup":
			os.Exit(backupState(os.Args[2:]))
		case "restore":
			os.Exit(restoreState(os.Args[2:]))
		default:
			fmt.Fprintf(os.Stderr,
				"soundstorm: unknown command %q\n\n"+
					"  soundstorm                 start the server\n"+
					"  soundstorm backup [file]   copy the accounts and credentials out\n"+
					"  soundstorm restore <file>  put them back\n"+
					"  soundstorm reset-password  set a new password for an account\n\n"+
					"The server takes no arguments; everything else is environment variables.\n",
				os.Args[1])
			os.Exit(2)
		}
	}

	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel(env("SOUNDSTORM_LOG_LEVEL", "info")),
	}))

	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	listen := env("SOUNDSTORM_LISTEN", ":8080")
	stateDir := env("SOUNDSTORM_STATE_DIR", "/var/lib/soundstorm")

	perSourceTimeout, err := time.ParseDuration(env("SOUNDSTORM_PER_SOURCE_TIMEOUT", "5s"))
	if err != nil {
		return fmt.Errorf("SOUNDSTORM_PER_SOURCE_TIMEOUT: %w", err)
	}

	// Before anything else: make sure the folders a person is supposed to put
	// media into actually exist. On a fresh install this is what turns "read
	// the README to learn the layout" into "the folders are already there".
	lib, err := library.Open(
		env("SOUNDSTORM_LIBRARY_DIR", "/library"),
		env("SOUNDSTORM_LIBRARY_HINT", "./library"),
		log,
	)
	if err != nil {
		return err
	}

	// A media server with nothing in it cannot be evaluated, so the first run
	// arrives with a small library of classics already in place. Only folders
	// the user has not put anything in are touched.
	if enabled(env("SOUNDSTORM_STARTER_LIBRARY", "true")) {
		if _, err := starter.Install(lib.Root(), lib.FolderIsEmpty, log); err != nil {
			// Never fatal: a server that will not start because it could not
			// unpack sample media has its priorities backwards.
			log.Warn("could not install the starter library", "err", err)
		}
		lib.Invalidate()
	}

	// A crash mid-upload leaves bytes in the staging directory that nothing
	// will ever finish, and they are invisible - the directory is hidden and
	// no backend has it mounted.
	lib.ClearStaging()

	targets, err := targetsFromEnv(lib)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return errors.New("no backends configured; set at least one of SOUNDSTORM_NAVIDROME_URL, " +
			"SOUNDSTORM_JELLYFIN_URL, SOUNDSTORM_AUDIOBOOKSHELF_URL")
	}

	store, err := state.Open(filepath.Join(stateDir, "state.json"))
	if err != nil {
		return err
	}

	// TLS is set up before anything is served, and a bad setting is fatal:
	// quietly falling back to plain HTTP when somebody asked for encryption is
	// the worst available way to be wrong.
	tlsServer, err := servetls.Load(servetls.Config{
		Mode:     env("SOUNDSTORM_TLS", servetls.ModeOff),
		CertFile: os.Getenv("SOUNDSTORM_TLS_CERT"),
		KeyFile:  os.Getenv("SOUNDSTORM_TLS_KEY"),
		Dir:      filepath.Join(stateDir, "tls"),
		Hosts:    splitList(os.Getenv("SOUNDSTORM_TLS_HOSTS")),
		Log:      log,
	})
	if err != nil {
		return err
	}

	registry := source.NewRegistry()
	setup := provision.New(store, registry, log, targets)

	// Shut down cleanly on Ctrl-C or SIGTERM from the container runtime.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Provisioning runs in the background: a backend can take a minute to boot
	// and SoundStorm should be showing setup progress during it, not refusing to
	// start. This is why the registry is populated asynchronously.
	setup.Start(ctx)

	authManager := auth.New(store)
	// Only meaningful behind a proxy that sets the header and strips any
	// incoming one; any client can send it, so it is off unless asked for.
	authManager.TrustForwardedProto = env("SOUNDSTORM_TRUST_PROXY", "false") == "true"

	var caPEM []byte
	if tlsServer != nil {
		caPEM = tlsServer.CAPEM
	}

	api := httpapi.New(httpapi.Config{
		Registry:         registry,
		Store:            store,
		Library:          lib,
		Auth:             authManager,
		Setup:            setup,
		PerSourceTimeout: perSourceTimeout,
		Log:              log,
		CAPEM:            caPEM,
		// The same list the certificate covers, for the same reason: it is
		// the machine's LAN address, and only the installer - which ran on
		// the host - was ever in a position to find it out.
		LANHosts: splitList(os.Getenv("SOUNDSTORM_TLS_HOSTS")),
	})

	srv := &http.Server{
		Addr:              listen,
		Handler:           api.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: it would cut off a film mid-playback.
	}
	if tlsServer != nil {
		srv.TLSConfig = tlsServer.TLSConfig()
	}

	accountState := fmt.Sprintf("%d accounts", store.UserCount())
	if store.UserCount() == 0 {
		accountState = "no accounts yet - the first visit creates the owner"
	}
	scheme := "http"
	if tlsServer != nil {
		scheme = "https"
	}
	log.Info("SoundStorm starting",
		"listen", listen,
		"scheme", scheme,
		"backends", len(targets),
		"library", lib.Root(),
		"state", stateDir,
		"account", accountState,
	)

	errCh := make(chan error, 1)
	go func() {
		var err error
		if tlsServer != nil {
			// Empty paths: the certificate comes from TLSConfig.GetCertificate,
			// which mints one for whatever address the client actually dialled.
			err = srv.ListenAndServeTLS("", "")
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
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

// targetsFromEnv reads which backends to manage. A backend is present if its
// URL is set, so compose decides the stack and SoundStorm adapts.
func targetsFromEnv(lib *library.Library) ([]provision.Target, error) {
	var targets []provision.Target

	if url := strings.TrimSpace(os.Getenv("SOUNDSTORM_NAVIDROME_URL")); url != "" {
		targets = append(targets, provision.Target{
			ID:      "navidrome",
			Type:    "navidrome",
			BaseURL: url,
		})
	}
	if url := strings.TrimSpace(os.Getenv("SOUNDSTORM_JELLYFIN_URL")); url != "" {
		targets = append(targets, provision.Target{
			ID:      "jellyfin",
			Type:    "jellyfin",
			BaseURL: url,
			// The paths as Jellyfin's container sees them, which is what its
			// library API needs - not SoundStorm's view of the same folders.
			MediaPath: env("SOUNDSTORM_JELLYFIN_MEDIA_PATH", "/media/movies"),
			TVPath:    env("SOUNDSTORM_JELLYFIN_TV_PATH", "/media/tv"),
		})
	}
	if url := strings.TrimSpace(os.Getenv("SOUNDSTORM_AUDIOBOOKSHELF_URL")); url != "" {
		targets = append(targets, provision.Target{
			ID:        "audiobookshelf",
			Type:      "audiobookshelf",
			BaseURL:   url,
			MediaPath: env("SOUNDSTORM_AUDIOBOOKSHELF_MEDIA_PATH", "/audiobooks"),
		})
	}
	// Ebooks are served straight off the disk: an EPUB describes itself, so no
	// backend has to stand between SoundStorm and the folder. The path comes from
	// the library layout rather than its own variable - there is one answer to
	// "where do ebooks live" and it should not be configurable into disagreeing
	// with the folder SoundStorm just created.
	if dir := lib.PathFor(media.KindEbook); dir != "" {
		targets = append(targets, provision.Target{
			ID:        "ebooks",
			Type:      "localbooks",
			MediaPath: dir,
		})
	}
	// Escape hatch for an existing Calibre server elsewhere on the network.
	// This one does need credentials typed, which is why it is not the default.
	if url := strings.TrimSpace(os.Getenv("SOUNDSTORM_CALIBREWEB_URL")); url != "" {
		targets = append(targets, provision.Target{
			ID:        "calibreweb",
			Type:      "calibreweb",
			BaseURL:   url,
			MediaPath: env("SOUNDSTORM_CALIBREWEB_MEDIA_PATH", "/books"),
		})
	}
	return targets, nil
}

// splitList reads a comma-separated environment variable.
func splitList(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// enabled reads a switch the way somebody writing one expects it to be read.
//
// This used to be `!= "false"`, so SOUNDSTORM_STARTER_LIBRARY=off turned the
// starter library on - the exact opposite of what was written, silently. "off"
// is a reasonable thing to write, not least because SOUNDSTORM_TLS uses it for
// the same idea, and a setting that quietly means its opposite is worse than
// one that refuses to be understood.
func enabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "false", "off", "no", "0", "":
		return false
	default:
		return true
	}
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
