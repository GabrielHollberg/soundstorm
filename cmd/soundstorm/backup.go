package main

import (
	"fmt"
	"io"
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
//	(umask 077; docker compose run --rm -T soundstorm backup - > soundstorm-backup.json)
//	docker compose run --rm -T soundstorm restore - < soundstorm-backup.json
//
// A file of "-" is standard output for backup and standard input for restore,
// and on Linux it is the form to use. Written through a bind mount instead, the
// backup is created by the container's own user (uid 10001), mode 0600 - so the
// person who asked for it could not read or copy it without root, which rather
// defeats a file whose whole purpose is to be carried off the machine. Through
// standard output the shell creates it, owned by whoever ran the command, with
// their umask. (On macOS and Windows, Docker Desktop presents a bind-mounted
// file as the host user's, so either form works there.)
//
// The file is worth as much as the server, which is why nothing writes one
// anywhere by default. Inside the media folder would be the obvious place and
// the wrong one - that folder gets synced.

const backupUsage = `SoundStorm backup

  soundstorm backup [file | -]
  soundstorm restore <file | ->

Copies the accounts and backend credentials out of the state volume, and back
in. Without a file, backup writes soundstorm-backup-<date>.json beside the
state. A file of - means standard output for backup and standard input for
restore.

The state volume is the only copy of the passwords SoundStorm created on
Navidrome, Jellyfin and Audiobookshelf. If it is lost, those servers keep
running with accounts nobody can sign in to, and no amount of reinstalling
gets them back.

On Linux, let your shell write the file, so it is yours and private:

  (umask 077; docker compose run --rm -T soundstorm backup - > soundstorm-backup.json)
  docker compose run --rm -T soundstorm restore - < soundstorm-backup.json

Or with somewhere outside the volume mounted (the file then belongs to the
container's user, which on Linux means only root can read it):

  docker compose run --rm -v "$PWD:/backup" soundstorm backup /backup/soundstorm.json

Restoring replaces the current state. Whatever was there is kept as
state.json.bak, so restoring the wrong file is itself undoable.
`

// maxBackupBytes bounds a backup read from standard input. A real state file is
// kilobytes; this is only so a mistaken pipe cannot fill memory.
const maxBackupBytes = 64 << 20

// isTerminal reports whether f is a terminal rather than a pipe or a file.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

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

	// Where the talking goes: standard output normally, but standard error when
	// standard output is the backup itself.
	say := os.Stdout
	var size int64
	if dest == "-" {
		// A terminal is refused: every backend password scrolling past on a
		// screen is exactly what this file must not do, and a pseudo-terminal
		// would turn its newlines into CRLF on the way. Without -T, compose run
		// gives the command one.
		if isTerminal(os.Stdout) {
			fmt.Fprint(os.Stderr, "Standard output is a terminal. Send the backup to a file instead:\n\n"+
				"  (umask 077; docker compose run --rm -T soundstorm backup - > soundstorm-backup.json)\n")
			return 2
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Could not read %s: %v\n", path, err)
			return 1
		}
		if _, err := os.Stdout.Write(raw); err != nil {
			fmt.Fprintf(os.Stderr, "Could not write the backup: %v\n", err)
			return 1
		}
		say, size, dest = os.Stderr, int64(len(raw)), "standard output"
	} else {
		if err := state.CopyTo(path, dest); err != nil {
			fmt.Fprintf(os.Stderr, "Could not write the backup: %v\n", err)
			return 1
		}
		if info, err := os.Stat(dest); err == nil {
			size = info.Size()
		}
	}

	fmt.Fprintln(say)
	fmt.Fprintf(say, "  Backed up to %s (%d bytes)\n", dest, size)
	fmt.Fprintln(say)
	fmt.Fprintf(say, "  %d account(s), credentials for %d backend(s): %s\n",
		summary.Users, len(summary.Backends), strings.Join(summary.Backends, ", "))
	fmt.Fprintln(say)
	// Said plainly, because the mistake this is guarding against is keeping
	// the only copy in the one place that gets deleted.
	fmt.Fprintln(say, "  Keep this somewhere that is not this machine. It is worth as much")
	fmt.Fprintln(say, "  as the server: anyone holding it holds every backend password.")
	fmt.Fprintln(say)
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

	source := args[0]
	var err error
	if source == "-" {
		if isTerminal(os.Stdin) {
			fmt.Fprint(os.Stderr, "Standard input is a terminal. Pipe the backup in:\n\n"+
				"  docker compose run --rm -T soundstorm restore - < soundstorm-backup.json\n")
			return 2
		}
		raw, readErr := io.ReadAll(io.LimitReader(os.Stdin, maxBackupBytes+1))
		switch {
		case readErr != nil:
			err = fmt.Errorf("read standard input: %w", readErr)
		case len(raw) > maxBackupBytes:
			err = fmt.Errorf("standard input is larger than any SoundStorm backup")
		default:
			source = "standard input"
			err = state.RestoreBytes(raw, source, path)
		}
	} else {
		err = state.RestoreFrom(source, path)
	}
	if err != nil {
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
		summary.Users, len(summary.Backends), source)
	fmt.Println()
	fmt.Println("  The previous state is kept as state.json.bak.")
	fmt.Println("  Start SoundStorm again:  docker compose up -d")
	fmt.Println()
	return 0
}
