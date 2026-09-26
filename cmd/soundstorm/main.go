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
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/auth"
	"github.com/GabrielHollberg/soundstorm/internal/collections"
	"github.com/GabrielHollberg/soundstorm/internal/httpapi"
	"github.com/GabrielHollberg/soundstorm/internal/library"
	"github.com/GabrielHollberg/soundstorm/internal/lyrics"
	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/portmap"
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

	// Opened here, before the starter library rather than after the backends,
	// because whether the samples have already been unpacked is recorded in it.
	store, err := state.Open(filepath.Join(stateDir, "state.json"))
	if err != nil {
		return err
	}

	// A media server with nothing in it cannot be evaluated, so the first run
	// arrives with a small library of classics already in place. Only folders
	// the user has not put anything in are touched.
	//
	// Once, ever - not on every boot into whatever shelf happens to be empty.
	// It used to be the latter, which meant somebody who deleted the samples on
	// purpose found them back after the next restart, with nothing to explain
	// why. "Only folders the user has not put anything in" reads like it
	// respects a decision and does the opposite of it: an emptied shelf and a
	// never-used one look identical on disk.
	if enabled(env("SOUNDSTORM_STARTER_LIBRARY", "true")) && !store.StarterInstalled() {
		if _, err := starter.Install(lib.Root(), lib.FolderIsEmpty, log); err != nil {
			// Never fatal: a server that will not start because it could not
			// unpack sample media has its priorities backwards.
			log.Warn("could not install the starter library", "err", err)
		} else if err := store.MarkStarterInstalled(); err != nil {
			// Also not fatal, and the cost of getting here is one more unpack
			// next time rather than anything lost.
			log.Warn("could not record that the starter library is installed", "err", err)
		}
		lib.Invalidate()
	}

	// A crash mid-upload leaves bytes in the staging directory that nothing
	// will ever finish, and they are invisible - the directory is hidden and
	// no backend has it mounted.
	lib.ClearStaging()

	// Each person's favourites and playlists, a file each beside the state
	// file. See internal/collections for why they are not in it.
	collectionStore, err := collections.Open(filepath.Join(stateDir, "collections"))
	if err != nil {
		return err
	}

	// Empty the bin of anything deleted more than thirty days ago: now, and
	// once a day after. Recovered, like every goroutine started once and left
	// running - a panic here would take the whole server down with it.
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Error("bin sweep panicked; the bin will be emptied at the next start", "panic", r)
			}
		}()
		for {
			lib.SweepBin(time.Now(), library.BinKeep)
			time.Sleep(24 * time.Hour)
		}
	}()

	targets, err := targetsFromEnv(lib)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return errors.New("no backends configured; set at least one of SOUNDSTORM_NAVIDROME_URL, " +
			"SOUNDSTORM_JELLYFIN_URL, SOUNDSTORM_AUDIOBOOKSHELF_URL")
	}

	// The port the world reaches this install on, which is the published one
	// (SOUNDSTORM_PORT), not the container's internal listen port. It is what
	// the name service probes for remote access and what a remote URL carries.
	publicPort := 8099
	if p, err := strconv.Atoi(env("SOUNDSTORM_PORT", "8099")); err == nil && p > 0 {
		publicPort = p
	}

	// TLS is set up before anything is served, and a bad setting is fatal:
	// quietly falling back to plain HTTP when somebody asked for encryption is
	// the worst available way to be wrong.
	// Remote access is on when the owner has chosen it in the app; before they
	// have, the SOUNDSTORM_REMOTE_ACCESS default stands. Read live so the
	// in-app toggle takes effect without a restart.
	remoteDefault := enabled(os.Getenv("SOUNDSTORM_REMOTE_ACCESS"))
	remoteEnabled := func() bool {
		if on, chosen := store.RemoteAccess(); chosen {
			return on
		}
		return remoteDefault
	}

	// The home router's LAN address, for opening the port automatically when
	// remote access is on. The installer discovers it on the host and writes it
	// here; the container cannot, since its own default route is the Docker
	// bridge. An empty or unparseable value just means no automatic forward.
	var gateway netip.Addr
	if g := strings.TrimSpace(os.Getenv("SOUNDSTORM_GATEWAY")); g != "" {
		if addr, err := netip.ParseAddr(g); err == nil {
			gateway = addr
		} else {
			log.Warn("ignoring SOUNDSTORM_GATEWAY: not an IP address", "value", g)
		}
	}
	// With nothing configured - compose alone, no installer to look it up -
	// guess from the LAN address: nearly every home router is the .1 of its
	// network. Without this, remote access on such an install could never
	// open its own port, and nobody would know why.
	if !gateway.IsValid() {
		for _, h := range splitList(os.Getenv("SOUNDSTORM_TLS_HOSTS")) {
			if lan, err := netip.ParseAddr(h); err == nil {
				if guess := portmap.GuessGateway(lan); guess.IsValid() {
					gateway = guess
					log.Info("no SOUNDSTORM_GATEWAY set; assuming the router is at the usual address", "gateway", guess)
					break
				}
			}
		}
	}

	tlsServer, err := servetls.Load(servetls.Config{
		Mode:     env("SOUNDSTORM_TLS", servetls.ModeOff),
		CertFile: os.Getenv("SOUNDSTORM_TLS_CERT"),
		KeyFile:  os.Getenv("SOUNDSTORM_TLS_KEY"),
		Dir:      filepath.Join(stateDir, "tls"),
		Hosts:    splitList(os.Getenv("SOUNDSTORM_TLS_HOSTS")),
		// Auto mode's two outside services; empty means the real ones.
		NamesURL:      os.Getenv("SOUNDSTORM_NAMES_URL"),
		ACMEDirectory: os.Getenv("SOUNDSTORM_ACME_DIRECTORY"),
		// Remote access: reach this install from outside the house. Opt-in,
		// and only meaningful in auto mode (it needs the name service).
		RemoteEnabled: remoteEnabled,
		Port:          publicPort,
		Gateway:       gateway,
		// UPnP fallback: the installer discovers the router's device-description
		// URL on the host and passes it here, since SSDP cannot cross the Docker
		// bridge from in here. Empty just means UPnP is tried by SSDP or skipped.
		UPnPLocation: strings.TrimSpace(os.Getenv("SOUNDSTORM_UPNP_URL")),
		Log:          log,
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
	// In auto mode, getting the real certificate - in the background too, and
	// for the same reason.
	tlsServer.Start(ctx)

	authManager := auth.New(store)
	// Only meaningful behind a proxy that sets the header and strips any
	// incoming one; any client can send it, so it is off unless asked for.
	authManager.TrustForwardedProto = env("SOUNDSTORM_TRUST_PROXY", "false") == "true"

	var caPEM []byte
	if tlsServer != nil {
		caPEM = tlsServer.CAPEM
	}

	// The first sign-up needs this. The installer writes one into .env and
	// opens the browser with it in the address; without an installer there is
	// none, so one is made up here and printed where the owner will look.
	setupCode := strings.TrimSpace(os.Getenv("SOUNDSTORM_SETUP_CODE"))
	if setupCode == "" {
		setupCode = newSetupCode()
	}
	if store.UserCount() == 0 {
		log.Warn("no account yet: the first sign-up needs this setup code",
			"code", setupCode,
			"or add to the address", "/?setup="+httpapi.NormalizeSetupCode(setupCode))
	}

	// Lyrics found online are kept beside the state, never in the music
	// folders: the library is the user's, and a scanner would index them.
	lyricsFinder, err := lyrics.New(filepath.Join(stateDir, "lyrics"))
	if err != nil {
		log.Warn("online lyrics unavailable", "err", err)
		lyricsFinder = nil
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
		LANHosts:           splitList(os.Getenv("SOUNDSTORM_TLS_HOSTS")),
		PublicName:         tlsServer.PublicName,
		RemoteReachability: tlsServer.ReachabilityAnswer,
		RemoteStatus: func() httpapi.RemoteState {
			method, mapped := tlsServer.RemoteMapping()
			return httpapi.RemoteState{
				Available: tlsServer.SupportsRemote(),
				Enabled:   remoteEnabled(),
				Name:      tlsServer.RemoteName(),
				Port:      tlsServer.RemotePort(),
				Mapped:    mapped,
				Method:    method,
				Upstream:  tlsServer.RemoteUpstream(),
			}
		},
		SetRemoteAccess: func(on bool) error {
			if err := store.SetRemoteAccess(on); err != nil {
				return err
			}
			tlsServer.Refresh() // act on the change now, not at the next check
			return nil
		},
		SetupCode:   setupCode,
		Lyrics:      lyricsFinder,
		Collections: collectionStore,
	})

	srv := &http.Server{
		Addr:              listen,
		Handler:           api.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: it would cut off a film mid-playback. No
		// ReadTimeout either, for the same film: a read deadline that expires
		// while a response is still being written cancels the request's
		// context, and the stream stops with it. Bodies get their own
		// deadline instead - see httpapi.bodyDeadline.
		IdleTimeout:    2 * time.Minute,
		MaxHeaderBytes: 64 << 10,
	}
	if tlsServer != nil {
		srv.TLSConfig = tlsServer.TLSConfig()
	}

	accountState := fmt.Sprintf("%d accounts", store.UserCount())
	if store.UserCount() == 0 {
		accountState = "no accounts yet - the first visit creates the owner"
	}
	scheme := "http"
	if tlsServer.Sniffs() {
		scheme = "http and https"
	} else if tlsServer != nil {
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
		if tlsServer.Sniffs() {
			// Both protocols on one port, told apart by their first byte, so
			// the http:// address everybody already has keeps working beside
			// the real https one.
			var ln net.Listener
			if ln, err = net.Listen("tcp", listen); err == nil {
				err = srv.Serve(tlsServer.Listener(ln))
			}
		} else if tlsServer != nil {
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
	if url := strings.TrimSpace(os.Getenv("SOUNDSTORM_IMMICH_URL")); url != "" {
		targets = append(targets, provision.Target{
			ID:      "immich",
			Type:    "immich",
			BaseURL: url,
			// The folder as Immich's container sees it, which is what its
			// library API wants.
			MediaPath: env("SOUNDSTORM_IMMICH_MEDIA_PATH", "/pictures"),
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
			Kind:      media.KindEbook,
		})
	}
	// Documents are the same kind of folder read the same way - a PDF opens
	// in the browser's own viewer whichever shelf it is on - so the same
	// source serves them, told only which kind it holds.
	if dir := lib.PathFor(media.KindDocument); dir != "" {
		targets = append(targets, provision.Target{
			ID:        "documents",
			Type:      "localbooks",
			MediaPath: dir,
			Kind:      media.KindDocument,
		})
	}
	// Read-along: Storyteller lines an audiobook up with its ebook. Its data
	// folder is mounted read-only here, where the synced books are read from.
	if url := strings.TrimSpace(os.Getenv("SOUNDSTORM_STORYTELLER_URL")); url != "" {
		targets = append(targets, provision.Target{
			ID:               "storyteller",
			Type:             "storyteller",
			BaseURL:          url,
			MediaPath:        env("SOUNDSTORM_STORYTELLER_DATA", "/readalong/data"),
			AudiobooksRemote: env("SOUNDSTORM_STORYTELLER_AUDIOBOOKS", "/audiobooks"),
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

// newSetupCode is 80 random bits in a form somebody can read off a log and
// type: sixteen characters from an alphabet with no 0/o or 1/l to confuse.
func newSetupCode() string {
	const alphabet = "23456789abcdefghijkmnpqrstuvwxyz"
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand does not fail on any supported platform
	}
	var out strings.Builder
	for i, c := range b {
		if i > 0 && i%4 == 0 {
			out.WriteByte('-')
		}
		out.WriteByte(alphabet[int(c)%len(alphabet)])
	}
	return out.String()
}
