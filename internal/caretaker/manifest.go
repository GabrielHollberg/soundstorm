// Package caretaker keeps a EmberStorm box up to date and looks after it. It
// runs on the box itself, as root, beside EmberStorm and never inside it:
// EmberStorm must not hold the Docker socket (see "Installing, updating,
// removing" in CLAUDE.md), so everything that drives Docker or the disks is
// here, behind a narrow door EmberStorm can only ask through.
//
// An update is a manifest: the exact image, by digest, for every service,
// signed with the project's release key. The box takes only a manifest whose
// signature checks against the key built into it and whose serial is higher
// than the one it runs - so neither a stranger's manifest nor an old one of
// ours replayed can move it.
package caretaker

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Manifest is one release of the box's software.
type Manifest struct {
	// Serial orders releases; a box only ever moves to a higher one.
	Serial int64 `json:"serial"`
	// Version is what a person is shown ("2026.10.3").
	Version string    `json:"version"`
	Created time.Time `json:"created"`
	// Expires is when a box stops believing it: an old release, signed and
	// all, cannot be served to a box for ever in place of newer ones (the
	// twelfth security pass). A release is signed again before then.
	Expires time.Time `json:"expires"`
	// Images is each compose service's image, pinned by digest.
	Images map[string]string `json:"images"`
	// Notes is "what's new", in plain words, for the app.
	Notes string `json:"notes,omitempty"`
}

var (
	serviceName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	// A registry reference pinned by digest, and nothing a YAML file or a
	// command line could read as anything else.
	pinnedRef = regexp.MustCompile(`^[a-z0-9][a-z0-9._/:-]{0,200}@sha256:[0-9a-f]{64}$`)
	version   = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+-]{0,40}$`)
)

// Validate refuses a manifest the box could not safely act on, signed or not.
func (m *Manifest) Validate() error {
	if m.Serial <= 0 {
		return errors.New("manifest: no serial")
	}
	if !version.MatchString(m.Version) {
		return errors.New("manifest: bad version")
	}
	if len(m.Images) == 0 {
		return errors.New("manifest: no images")
	}
	if m.Expires.IsZero() || !m.Expires.After(m.Created) {
		return errors.New("manifest: no expiry")
	}
	for svc, ref := range m.Images {
		if !serviceName.MatchString(svc) {
			return fmt.Errorf("manifest: bad service name %q", svc)
		}
		if !pinnedRef.MatchString(ref) {
			return fmt.Errorf("manifest: image for %s is not pinned by digest", svc)
		}
	}
	if len(m.Notes) > 4000 {
		return errors.New("manifest: notes too long")
	}
	return nil
}

// Sign returns the signature for a manifest's exact bytes, as the base64
// text published beside it (manifest.json.sig).
func Sign(data []byte, key ed25519.PrivateKey) []byte {
	return []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(key, data)) + "\n")
}

// Verify checks a manifest's signature and contents. The signature covers the
// bytes as published, so nothing is re-encoded before checking.
func Verify(data, sig []byte, key ed25519.PublicKey) (*Manifest, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || len(raw) != ed25519.SignatureSize {
		return nil, errors.New("manifest: unreadable signature")
	}
	if len(key) != ed25519.PublicKeySize || !ed25519.Verify(key, data, raw) {
		return nil, errors.New("manifest: signature does not match the release key")
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// VerifyAny is Verify against each key in turn: the release key, then any
// backup kept somewhere safe, so losing the one does not leave every box
// unable to update (the box's blind security review).
func VerifyAny(data, sig []byte, keys ...ed25519.PublicKey) (*Manifest, error) {
	err := errors.New("manifest: no release key")
	for _, k := range keys {
		var m *Manifest
		if m, err = Verify(data, sig, k); err == nil {
			return m, nil
		}
	}
	return nil, err
}

// ParsePublicKeys reads the box's release keys: one a line, as keygen writes
// them, the release key first and any backup after; blank lines and lines
// starting with # are notes.
func ParsePublicKeys(text []byte) ([]ed25519.PublicKey, error) {
	var keys []ed25519.PublicKey
	for _, line := range strings.Split(string(text), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, err := ParsePublicKey([]byte(line))
		if err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return nil, errors.New("no release key")
	}
	return keys, nil
}

// ParsePublicKey reads a key as written by keygen: base64 on one line.
func ParsePublicKey(text []byte) (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(text)))
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, errors.New("not an ed25519 public key")
	}
	return ed25519.PublicKey(raw), nil
}

// ParsePrivateKey reads a private key as written by keygen.
func ParsePrivateKey(text []byte) (ed25519.PrivateKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(text)))
	if err != nil || len(raw) != ed25519.PrivateKeySize {
		return nil, errors.New("not an ed25519 private key")
	}
	return ed25519.PrivateKey(raw), nil
}

// ImagesFile is the compose file that points each service at the manifest's
// image (compose.images.yml on the box). Services are sorted so the same
// manifest always writes the same file.
func ImagesFile(m *Manifest) []byte {
	svcs := make([]string, 0, len(m.Images))
	for svc := range m.Images {
		svcs = append(svcs, svc)
	}
	sort.Strings(svcs)
	var b strings.Builder
	fmt.Fprintf(&b, "# Written by the caretaker: EmberStorm %s (release %d).\nservices:\n", m.Version, m.Serial)
	for _, svc := range svcs {
		fmt.Fprintf(&b, "  %s:\n    image: %s\n", svc, m.Images[svc])
	}
	return []byte(b.String())
}
