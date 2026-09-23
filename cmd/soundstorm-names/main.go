// Command soundstorm-names is the name service: it gives every SoundStorm
// install a name under soundstorm.dev, so each one can have a real certificate.
// See internal/names for why it exists and what it will and will not do.
//
// It runs anywhere that can run a container and hold four secrets - Railway is
// where it was first deployed. Configuration is the environment:
//
//	NAMES_SECRET             at least 32 random bytes; keys every install's token
//	NAMES_ZONE               the domain at the provider (default soundstorm.dev)
//	NAMES_LABEL              the level installs sit beneath (default home)
//	PORKBUN_API_KEY          Porkbun credentials, with API access switched on
//	PORKBUN_SECRET_API_KEY   for NAMES_ZONE only
//	NAMES_CLIENT_IP_HEADER   header the hosting proxy puts the client address in
//	NAMES_FORGET_AFTER_DAYS  delete an install's record once it has been silent
//	                         this long (default 180); it comes back, same name,
//	                         the next time that install starts
//	PORT                     where to listen (default 8080; Railway sets it)
//
// For a local rehearsal against Pebble instead of Porkbun and Let's Encrypt:
//
//	NAMES_CHALLTESTSRV       pebble-challtestsrv management URL
//	NAMES_NAMESERVERS        host:port list to wait on (default Porkbun's)
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/names"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("soundstorm-names stopped", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	secret := os.Getenv("NAMES_SECRET")
	if len(secret) < 32 {
		return errors.New("NAMES_SECRET must be at least 32 characters; generate one with: openssl rand -base64 48")
	}
	zone := env("NAMES_ZONE", "soundstorm.dev")

	s := &names.Server{
		Secret:         []byte(secret),
		Zone:           zone,
		Label:          env("NAMES_LABEL", "home"),
		ClientIPHeader: os.Getenv("NAMES_CLIENT_IP_HEADER"),
		Log:            log,
	}

	if test := os.Getenv("NAMES_CHALLTESTSRV"); test != "" {
		s.DNS = &names.ChallTestSrv{Domain: zone, Base: test}
		log.Warn("using pebble-challtestsrv for DNS: a rehearsal, not a real service")
	} else {
		key, secretKey := os.Getenv("PORKBUN_API_KEY"), os.Getenv("PORKBUN_SECRET_API_KEY")
		if key == "" || secretKey == "" {
			return errors.New("PORKBUN_API_KEY and PORKBUN_SECRET_API_KEY are required")
		}
		s.DNS = &names.Porkbun{Domain: zone, APIKey: key, SecretAPIKey: secretKey, Base: os.Getenv("PORKBUN_API_BASE")}
		s.Nameservers = names.PorkbunNameservers
	}
	if ns := os.Getenv("NAMES_NAMESERVERS"); ns != "" {
		s.Nameservers = strings.Split(ns, ",")
	}

	days, err := strconv.Atoi(env("NAMES_FORGET_AFTER_DAYS", "180"))
	if err != nil || days < 30 {
		return fmt.Errorf("NAMES_FORGET_AFTER_DAYS must be a whole number of days, at least 30; got %q",
			os.Getenv("NAMES_FORGET_AFTER_DAYS"))
	}
	go s.RunSweeper(context.Background(), time.Duration(days)*24*time.Hour)

	addr := ":" + env("PORT", "8080")
	log.Info("soundstorm-names listening", "addr", addr, "zone", zone, "label", s.Label)
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// Every body here is capped at 4KB (readJSON), so a generous read
		// timeout costs nothing legitimate.
		ReadTimeout: 15 * time.Second,
		// Long enough for a challenge PUT, which waits for the nameservers.
		WriteTimeout: 6 * time.Minute,
		// Without this an idle keep-alive connection is held open forever:
		// Go falls back to ReadTimeout for idle connections when IdleTimeout
		// is zero, and ReadTimeout was zero here too before the line above.
		// This service is genuinely on the internet already, so a slow drip
		// of connections opened and left idle is a real cost, not a
		// theoretical one.
		IdleTimeout: 60 * time.Second,
	}
	return srv.ListenAndServe()
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
