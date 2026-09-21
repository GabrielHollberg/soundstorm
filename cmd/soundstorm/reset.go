package main

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"github.com/GabrielHollberg/soundstorm/internal/auth"
	"github.com/GabrielHollberg/soundstorm/internal/state"
)

// Password recovery, for the one failure the app cannot talk somebody through.
//
// Signup closes permanently the moment the first account exists, so a
// forgotten owner password used to mean editing state.json inside a container
// by hand - a file with no documented shape, holding the credentials for all
// four backends, where one slip costs the whole install. That is not a
// recovery path; it is a thing to be talked through over the phone.
//
// It runs from the same binary rather than a second one so that it is always
// present wherever the server is, including inside the published image, with
// no extra thing to download at the exact moment somebody is locked out.
//
//	docker compose run --rm soundstorm reset-password
//
// The server must be stopped first, and the command says so rather than
// assuming: a running SoundStorm holds the state in memory and rewrites the
// whole file on its next change, which would silently undo this.

const resetUsage = `SoundStorm password reset

  soundstorm reset-password [username]

Sets a new password for an account and prints it. With one account on the
server the name can be left out.

Stop SoundStorm first, or it will write its own copy of the state back over
this one:

  docker compose down
  docker compose run --rm soundstorm reset-password
  docker compose up -d
`

// resetPassword implements the subcommand. Returns an exit code.
func resetPassword(args []string) int {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Print(resetUsage)
		return 0
	}
	if len(args) > 1 {
		fmt.Fprint(os.Stderr, resetUsage)
		return 2
	}

	stateDir := env("SOUNDSTORM_STATE_DIR", "/var/lib/soundstorm")
	path := filepath.Join(stateDir, "state.json")
	if _, err := os.Stat(path); err != nil {
		fmt.Fprintf(os.Stderr, "No SoundStorm state at %s\n\n"+
			"Run this inside the container, where the state volume is mounted:\n\n"+
			"  docker compose run --rm soundstorm reset-password\n", path)
		return 1
	}

	// Stat before opening, not after, and that ordering is the whole fix.
	// state.Open rewrites the file even when nothing changed - it migrates and
	// saves unconditionally - so by the time it returns, the ownership this is
	// trying to preserve has already been replaced by whoever is running.
	before, statErr := os.Stat(path)

	store, err := state.Open(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not read %s: %v\n", path, err)
		return 1
	}
	// Put it back straight away: Open has already written once, and an early
	// return below would otherwise leave the file owned by the wrong user
	// without a password having been changed at all.
	if statErr == nil {
		_ = restoreOwner(path, before)
	}

	users := store.Users()
	if len(users) == 0 {
		fmt.Println("There are no accounts on this server yet.")
		fmt.Println("Open SoundStorm in a browser and the first screen will make one.")
		return 0
	}

	var target state.User
	switch {
	case len(args) == 1:
		found, ok := store.UserByName(args[0])
		if !ok {
			fmt.Fprintf(os.Stderr, "No account called %q. There is:\n", args[0])
			for _, u := range users {
				fmt.Fprintf(os.Stderr, "  %s (%s)\n", u.Name, u.Role)
			}
			return 1
		}
		target = found
	case len(users) == 1:
		target = users[0]
	default:
		// Picking one for somebody would be guessing which of their family
		// is locked out.
		fmt.Fprintln(os.Stderr, "There is more than one account. Say which:")
		for _, u := range users {
			fmt.Fprintf(os.Stderr, "  soundstorm reset-password %s   (%s)\n", u.Name, u.Role)
		}
		return 1
	}

	password, err := readablePassword()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not generate a password: %v\n", err)
		return 1
	}

	// The actor is the account itself. Changing your own password is a thing
	// every account may do, so this needs no owner and grants nothing that
	// having the state file did not already grant.
	manager := auth.New(store)
	if err := manager.SetPassword(target, target.ID, password); err != nil {
		fmt.Fprintf(os.Stderr, "Could not set the password: %v\n", err)
		return 1
	}

	if statErr == nil {
		if err := restoreOwner(path, before); err != nil {
			// The password is already changed, so this is a warning rather
			// than a failure - but it is the difference between a server that
			// starts and one that does not, so it says how to fix it.
			fmt.Fprintf(os.Stderr,
				"\n  Warning: the password was changed, but %s could not be\n"+
					"  given back to its previous owner: %v\n\n"+
					"  SoundStorm may now fail to start with a permission error.\n"+
					"  Run this again as the user that owns the file, or hand it\n"+
					"  back by hand.\n",
				path, err)
		}
	}

	fmt.Println()
	fmt.Printf("  Password reset for %s.\n", target.Name)
	fmt.Println()
	fmt.Printf("    username   %s\n", target.Name)
	fmt.Printf("    password   %s\n", password)
	fmt.Println()
	fmt.Println("  Sign in with that, then change it under Account.")
	fmt.Println()
	// Other devices keep working, which is the right default here: somebody
	// recovering their own password has not necessarily been compromised, and
	// signing every TV in the house out would be its own small disaster.
	fmt.Println("  Devices already signed in stay signed in.")
	fmt.Println()
	return 0
}

// readablePassword builds something that can be read off a screen and typed
// into a phone: no l/1/O/0, and grouped so the eye can keep its place.
func readablePassword() (string, error) {
	const alphabet = "abcdefghijkmnpqrstuvwxyz23456789"
	const groups, per = 4, 4

	var b strings.Builder
	for g := 0; g < groups; g++ {
		if g > 0 {
			b.WriteByte('-')
		}
		for i := 0; i < per; i++ {
			n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
			if err != nil {
				return "", err
			}
			b.WriteByte(alphabet[n.Int64()])
		}
	}
	return b.String(), nil
}
