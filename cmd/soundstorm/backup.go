package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/state"
)

// Backup and restore, for the failure the app cannot detect and cannot undo.
//
// state.json holds the account and the credentials SoundStorm generated for
// four backends. Those backends cannot be re-provisioned: each already has an
// account SoundStorm created, and the password existed in exactly one file.
// `docker compose down -v`, a wiped volume or a replaced machine leaves four
// working servers that nobody can log into - the provisioners notice and say
// so, and there is nothing they can do about it.
//
//	docker compose run --rm -v "$PWD:/backup" soundstorm backup /backup/soundstorm-backup.json
//	docker compose run --rm -v "$PWD:/backup" soundstorm restore /backup/soundstorm-backup.json
//
// The file is worth as much as the server, which is why nothing writes one
// anywhere by default. Inside the media folder would be the obvious place and
// the wrong one - that folder gets synced.

const backupUsage = `SoundStorm backup

  soundstorm backup [file]
  soundstorm restore <file>

Copies the accounts and backend credentials out of the state volume, and back
in. Without a file, backup writes soundstorm-backup-<date>.json beside the
state.

The state volume is the only copy of the passwords SoundStorm created on
Navidrome, Jellyfin and Audiobookshelf. If it is lost, those servers keep
running with accounts nobody can sign in to, and no amount of reinstalling
gets them back.

Run it with somewhere outside the volume mounted:

  docker compose run --rm -v "$PWD:/backup" soundstorm backup /backup/soundstorm.json

Restoring replaces the current state. Whatever was there is kept as
state.json.bak, so restoring the wrong file is itself undoable.
`

func statePath() string {
	return filepath.Join(env("SOUNDSTORM_STATE_DIR", "/var/lib/soundstorm"), "state.json")
}

// backupState implements the subcommand. Returns an exit code.
func backupState(args []string) int {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Print(backupUsage)
		return 0
	}
	if len(args) > 1 {
		fmt.Fprint(os.Stderr, backupUsage)
		return 2
	}

	path := statePath()
	if _, err := os.Stat(path); err != nil {
		fmt.Fprintf(os.Stderr, "No SoundStorm state at %s\n\n"+
			"Run this inside the container, where the state volume is mounted.\n", path)
		return 1
	}

	dest := ""
	if len(args) == 1 {
		dest = args[0]
	} else {
		dest = filepath.Join(filepath.Dir(path),
			"soundstorm-backup-"+time.Now().Format("2006-01-02")+".json")
	}

	// Read, never opened as a Store. state.Open migrates and saves
	// unconditionally, so backing up through it would rewrite the very file
	// being backed up - and hand its ownership to whoever ran the command,
	// which is how a root-run recovery once left a server unable to read its
	// own state. A backup that modifies its source is not a backup.
	summary, err := state.Inspect(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not read %s: %v\n", path, err)
		return 1
	}
	if err := state.CopyTo(path, dest); err != nil {
		fmt.Fprintf(os.Stderr, "Could not write the backup: %v\n", err)
		return 1
	}

	info, _ := os.Stat(dest)
	size := int64(0)
	if info != nil {
		size = info.Size()
	}
	fmt.Println()
	fmt.Printf("  Backed up to %s (%d bytes)\n", dest, size)
	fmt.Println()
	fmt.Printf("  %d account(s), credentials for %d backend(s): %s\n",
		summary.Users, len(summary.Backends), strings.Join(summary.Backends, ", "))
	fmt.Println()
	// Said plainly, because the mistake this is guarding against is keeping
	// the only copy in the one place that gets deleted.
	fmt.Println("  Keep this somewhere that is not this machine. It is worth as much")
	fmt.Println("  as the server: anyone holding it holds every backend password.")
	fmt.Println()
	return 0
}

// restoreState implements the subcommand. Returns an exit code.
func restoreState(args []string) int {
	if len(args) != 1 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, backupUsage)
		if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
			return 0
		}
		return 2
	}

	path := statePath()

	// Who should own the result. The state file may not exist at all - the
	// whole point of a restore is often that the volume is empty - so fall
	// back to the directory, which Docker sets up for the image's user.
	//
	// Without this, a restore run as root leaves state.json owned by root and
	// SoundStorm crash-loops on "read state: permission denied" at its next
	// start. That has happened here once already, from the reset command.
	owner, ownerErr := os.Stat(path)
	if ownerErr != nil {
		owner, ownerErr = os.Stat(filepath.Dir(path))
	}

	if err := state.RestoreFrom(args[0], path); err != nil {
		fmt.Fprintf(os.Stderr, "Could not restore: %v\n", err)
		return 1
	}

	if ownerErr == nil {
		if err := restoreOwner(path, owner); err != nil {
			fmt.Fprintf(os.Stderr,
				"\n  Warning: restored, but %s could not be given to the user\n"+
					"  SoundStorm runs as: %v\n\n"+
					"  It may fail to start with a permission error.\n", path, err)
		}
	}

	// Read back, so the summary describes what actually landed rather than
	// what the file claimed on the way in. Read, again, not opened.
	summary, err := state.Inspect(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Restored, but the result will not read back: %v\n", err)
		return 1
	}
	fmt.Println()
	fmt.Printf("  Restored %d account(s) and %d backend(s) from %s\n",
		summary.Users, len(summary.Backends), args[0])
	fmt.Println()
	fmt.Println("  The previous state is kept as state.json.bak.")
	fmt.Println("  Start SoundStorm again:  docker compose up -d")
	fmt.Println()
	return 0
}
