package provision

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/state"
)

// provisionLocalBooks is the trivial case, and worth keeping in this package
// anyway.
//
// A folder of ebooks has no account to create and no key to mint, so there is
// nothing to provision. But it does have a slow first step - walking the disk
// and parsing every book - and the retry, status and registration machinery
// here is exactly what should own that. The scan itself happens in
// source.Starter, which register() calls before the health check.
func provisionLocalBooks(_ context.Context, t Target, log *slog.Logger) (state.Backend, error) {
	info, err := os.Stat(t.MediaPath)
	if err != nil {
		return state.Backend{}, fmt.Errorf("%s folder %s: %w", t.ID, t.MediaPath, err)
	}
	if !info.IsDir() {
		return state.Backend{}, fmt.Errorf("%s folder %s is not a directory", t.ID, t.MediaPath)
	}

	log.Info("library folder ready", "path", t.MediaPath)
	return state.Backend{
		Type:          "localbooks",
		BaseURL:       "file://" + t.MediaPath,
		ProvisionedAt: time.Now().UTC(),
	}, nil
}
