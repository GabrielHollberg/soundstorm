package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/source/subsonic"
	"github.com/GabrielHollberg/soundstorm/internal/state"
	"github.com/GabrielHollberg/soundstorm/internal/training"
)

// trainLooks learns the looks from the training recordings on this install
// (SOUNDSTORM_TRAINING; httpapi/training.go): it hears each recorded song
// again, fits a model, scores it and today's rule on songs left out, and
// writes the model beside the recordings. Run inside the server's container,
// beside the running server:
//
//	docker exec soundstorm soundstorm train-looks
//
// It reads the state file for Navidrome's credentials and never writes it.
func trainLooks(args []string) int {
	if len(args) > 0 {
		fmt.Fprintln(os.Stderr, "usage: soundstorm train-looks")
		return 2
	}
	stateDir := env("SOUNDSTORM_STATE_DIR", "/var/lib/soundstorm")
	backends, err := state.ReadBackends(filepath.Join(stateDir, "state.json"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "train-looks: read state:", err)
		return 1
	}
	var id string
	var creds state.Backend
	for k, b := range backends {
		if b.Type == "navidrome" {
			id, creds = k, b
			break
		}
	}
	if id == "" {
		fmt.Fprintln(os.Stderr, "train-looks: no music server set up")
		return 1
	}
	music, err := subsonic.New(subsonic.Config{ID: id, BaseURL: creds.BaseURL, Username: creds.Username, Password: creds.Password, Timeout: 30 * time.Second})
	if err != nil {
		fmt.Fprintln(os.Stderr, "train-looks:", err)
		return 1
	}
	dir := filepath.Join(stateDir, "training")
	recs, err := training.LoadRecordings(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "train-looks: nothing recorded yet (", err, ")")
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	fmt.Printf("hearing %d recorded songs\n", len(recs))
	hear := func(r training.Recording) (*training.Heard, error) {
		hctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
		return training.Hear(hctx, music, r.ID, training.Prior(filepath.Join(stateDir, "beats"), r.Source, r.ID))
	}
	if err := training.Run(ctx, dir, recs, hear, func(s string) { fmt.Println(s) }); err != nil {
		fmt.Fprintln(os.Stderr, "train-looks:", err)
		return 1
	}
	return 0
}
