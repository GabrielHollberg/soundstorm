//go:build !windows

package main

import (
	"os"
	"syscall"
)

// restoreOwner puts a file's uid and gid back after a write replaced it.
//
// state.Store saves atomically - a fresh temporary file renamed over the
// original - so the file that ends up there belongs to whoever ran the
// command, not to whoever owned it a moment ago. Run as root, which is what a
// bare `docker run` gives you, that leaves state.json owned by root while
// SoundStorm runs as an unprivileged user; the server then crash-loops at
// startup on "read state: permission denied".
//
// Found the hard way, on a live install, while doing exactly that. The
// documented route (`docker compose run --rm soundstorm reset-password`) runs
// as the image's own user and never had the problem - which is precisely why
// it should not be the thing standing between somebody and a working server.
func restoreOwner(path string, before os.FileInfo) error {
	sys, ok := before.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	uid, gid := int(sys.Uid), int(sys.Gid)

	after, err := os.Stat(path)
	if err != nil {
		return err
	}
	// Nothing to do in the ordinary case, where the command was run as the
	// user who already owned it.
	if now, ok := after.Sys().(*syscall.Stat_t); ok && int(now.Uid) == uid && int(now.Gid) == gid {
		return nil
	}
	return os.Chown(path, uid, gid)
}
