// Command atrium is a federating gateway over a pile of self-hosted media
// servers: one search across music, audiobooks, ebooks, video and offline
// article archives.
//
// It deliberately does not transcode, scrape metadata or store media. Those
// are solved problems owned by the servers it sits in front of.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gabehollberg/atrium/internal/config"
	"github.com/gabehollberg/atrium/internal/federate"
	"github.com/gabehollberg/atrium/internal/httpapi"
	"github.com/gabehollberg/atrium/internal/source"
)

func main() {
	configPath := flag.String("config", "configs/atrium.yaml", "path to the configuration file")
	checkOnly := flag.Bool("check", false, "validate the config, probe every source, and exit")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if err := run(*configPath, *checkOnly, log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(configPath string, checkOnly bool, log *slog.Logger) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	reg, err := cfg.BuildRegistry()
	if err != nil {
		return err
	}
	log.Info("configuration loaded", "sources", reg.Len(), "listen", cfg.Listen)

	if checkOnly {
		return check(reg, cfg.PerSourceTimeout.Std(), log)
	}

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           httpapi.New(reg, cfg.PerSourceTimeout.Std(), cfg.EnableProbe, log).Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Shut down cleanly on Ctrl-C or SIGTERM from the container runtime.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.Listen)
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

// check probes every source once and reports, so a misconfiguration surfaces
// before you go looking for it in a search result that silently came back short.
func check(reg *source.Registry, timeout time.Duration, log *slog.Logger) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout+5*time.Second)
	defer cancel()

	var failed int
	for _, st := range federate.HealthAll(ctx, reg, timeout) {
		if st.OK {
			log.Info("source ok", "id", st.SourceID, "kind", string(st.Kind), "tookMs", st.TookMS)
			continue
		}
		failed++
		log.Error("source unreachable", "id", st.SourceID, "kind", string(st.Kind), "err", st.Error)
	}
	if failed > 0 {
		return errors.New("one or more sources failed their health check")
	}
	return nil
}
