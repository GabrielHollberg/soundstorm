// Command soundstorm-caretaker looks after a EmberStorm box: it installs
// updates, and undoes one that does not come up healthy. It runs on the box
// as root, never inside EmberStorm's container (see internal/caretaker).
//
// On the box:
//
//	soundstorm-caretaker serve      the service: the socket and the schedule
//	soundstorm-caretaker status     what is running and what is available
//	soundstorm-caretaker check      look for an update
//	soundstorm-caretaker update     install it now
//
// Where releases are made (the release key never goes on a box):
//
//	soundstorm-caretaker keygen KEYFILE        make the release key pair
//	soundstorm-caretaker manifest -serial N -version V [-notes TEXT] svc=ref@sha256:... ...
//	soundstorm-caretaker sign KEYFILE MANIFEST  write MANIFEST.sig
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/caretaker"
)

const (
	keyFile = "/etc/soundstorm/release.pub"
	socket  = "/run/soundstorm-caretaker/caretaker.sock"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	var err error
	switch os.Args[1] {
	case "serve", "status", "check", "update":
		err = onBox(os.Args[1], log)
	case "keygen":
		err = keygen(os.Args[2:])
	case "manifest":
		err = manifest(os.Args[2:])
	case "sign":
		err = sign(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "soundstorm-caretaker:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: soundstorm-caretaker serve|status|check|update|keygen|manifest|sign")
	os.Exit(2)
}

func onBox(cmd string, log *slog.Logger) error {
	text, err := os.ReadFile(keyFile)
	if err != nil {
		return fmt.Errorf("no release key at %s: %w", keyFile, err)
	}
	keys, err := caretaker.ParsePublicKeys(text)
	if err != nil {
		return err
	}
	cfg := caretaker.Config{Key: keys[0], Backup: keys[1:], ManifestURL: os.Getenv("SOUNDSTORM_RELEASES")}
	// The release the box was built with (box/build.sh): nothing older.
	if n, err := strconv.ParseInt(os.Getenv("SOUNDSTORM_MIN_SERIAL"), 10, 64); err == nil {
		cfg.MinSerial = n
	}
	u := caretaker.New(cfg, log)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	switch cmd {
	case "serve":
		return u.Serve(ctx, socket)
	case "status":
		return printJSON(u.Status())
	case "check":
		m, err := u.Check(ctx)
		if err != nil {
			return err
		}
		if m == nil {
			fmt.Println("Up to date.")
			return nil
		}
		return printJSON(m)
	case "update":
		m, err := u.Check(ctx)
		if err != nil {
			return err
		}
		if m == nil {
			fmt.Println("Up to date.")
			return nil
		}
		err = u.Update(ctx, m)
		fmt.Println(u.Status().Message)
		return err
	}
	return nil
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// keygen writes KEYFILE (the private key, 0600) and KEYFILE.pub. Keep the
// private one off every box and out of the repository, and somewhere it will
// not be lost: without it no box can be updated again.
func keygen(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("keygen KEYFILE")
	}
	if _, err := os.Stat(args[0]); err == nil {
		return fmt.Errorf("%s already exists; not replacing a release key", args[0])
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if err := os.WriteFile(args[0], []byte(base64.StdEncoding.EncodeToString(priv)+"\n"), 0o600); err != nil {
		return err
	}
	return os.WriteFile(args[0]+".pub", []byte(base64.StdEncoding.EncodeToString(pub)+"\n"), 0o644)
}

func manifest(args []string) error {
	fs := flag.NewFlagSet("manifest", flag.ContinueOnError)
	serial := fs.Int64("serial", 0, "release serial, higher than the last")
	version := fs.String("version", "", "version shown to people")
	notes := fs.String("notes", "", "what's new")
	days := fs.Int("days", 90, "days boxes believe it; sign again before then")
	system := fs.String("system", "", "the box's own files: a bundle published beside the manifest")
	packages := fs.String("packages", "", "Debian packages the system files need, comma-separated")
	enable := fs.String("enable", "", "units to switch on, comma-separated")
	restart := fs.String("restart", "", "units to start again, comma-separated")
	if err := fs.Parse(args); err != nil {
		return err
	}
	list := func(s string) []string {
		var out []string
		for _, v := range strings.Split(s, ",") {
			if v = strings.TrimSpace(v); v != "" {
				out = append(out, v)
			}
		}
		return out
	}
	now := time.Now().UTC().Truncate(time.Second)
	m := caretaker.Manifest{Serial: *serial, Version: *version, Notes: *notes,
		Created: now, Expires: now.AddDate(0, 0, *days), Images: map[string]string{}}
	for _, a := range fs.Args() {
		svc, ref, ok := strings.Cut(a, "=")
		if !ok {
			return fmt.Errorf("%q is not service=image@sha256:...", a)
		}
		m.Images[svc] = ref
	}
	if *system != "" {
		data, err := os.ReadFile(*system)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		m.System = &caretaker.System{File: filepath.Base(*system), SHA256: hex.EncodeToString(sum[:]),
			Size: int64(len(data)), Packages: list(*packages), Enable: list(*enable), Restart: list(*restart)}
	}
	if err := m.Validate(); err != nil {
		return err
	}
	return printJSON(m)
}

func sign(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("sign KEYFILE MANIFEST")
	}
	text, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	key, err := caretaker.ParsePrivateKey(text)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(args[1])
	if err != nil {
		return err
	}
	// Refuse to sign what a box would refuse to install.
	if _, err := caretaker.Verify(data, caretaker.Sign(data, key), key.Public().(ed25519.PublicKey)); err != nil {
		return err
	}
	return os.WriteFile(args[1]+".sig", caretaker.Sign(data, key), 0o644)
}
