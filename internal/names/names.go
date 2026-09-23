// Package names gives every SoundStorm install a real name, so it can have a
// real certificate.
//
// The problem is the one internal/servetls describes: browsers only trust a
// certificate somebody on the internet vouches for, and a server at
// 192.168.1.50 has no name on the internet to vouch for. Its own authority
// works, and costs a full-page warning on every device - which reads like
// being hacked to exactly the people SoundStorm is for.
//
// So SoundStorm owns a domain, and hands each install a name under it:
//
//	k3x9m2p7qa.home.soundstorm.dev  ->  192.168.1.50
//
// The install proves to Let's Encrypt that it controls that name with a DNS
// challenge, which needs nothing reachable from the internet - the only thing
// that has to be public is a TXT record, and this service writes it on the
// install's behalf. The certificate's private key never leaves the house.
//
// Three decisions shape it:
//
//   - It never carries media. It answers a registration, an address change,
//     and a challenge every couple of months. That is what keeps it cheap
//     however many installs there are, and what keeps it out of the data path
//     a relay would put it in.
//   - It holds no state. An install's token is an HMAC of its id under one
//     secret, so checking it needs nothing stored, and the DNS provider is the
//     only record of anything. Once a name resolves, it resolves through the
//     provider: this service being down stops registration and renewal, never
//     a lookup.
//   - It only ever points a name at a private address. Otherwise anybody
//     could mint a trusted-looking *.soundstorm.dev name for a phishing page.
//     Public addresses, for reaching an install from outside the house, would
//     need proof the install is really there, and are not offered yet.
package names

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"fmt"
	"net/netip"
	"strings"
)

// idLength is the length of an install's label. Fifty bits of base32: far too
// many to guess, and short enough to read aloud if anybody ever has to.
const idLength = 10

// idAlphabet is lowercase RFC 4648 base32. DNS is case-insensitive, so an
// uppercase id would come back from a resolver in whatever case it liked.
var idAlphabet = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// Registration is what an install keeps after registering: who it is, the
// name it answers to, and the token that proves the first.
type Registration struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Token string `json:"token"`
}

// Credential is the Authorization header value for a registration.
func (r Registration) Credential() string { return "Bearer " + r.ID + "." + r.Token }

// newID returns a fresh install label.
func newID() (string, error) {
	b := make([]byte, 7) // 56 bits, of which the first 50 are kept
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return idAlphabet.EncodeToString(b)[:idLength], nil
}

// validID reports whether s could be an id this service issued. Checked before
// anything else touches it, because it ends up inside a DNS name.
func validID(s string) bool {
	if len(s) != idLength {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '2' && r <= '7') {
			return false
		}
	}
	return true
}

// tokenFor derives an install's token from its id. Nothing is stored: the
// same secret re-derives it on every request, which is what lets this service
// run with no database. Rotating the secret therefore signs every install out
// of renewals at once - which is the only lever there is if it ever leaks.
func tokenFor(secret []byte, id string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("soundstorm-names v1\x00" + id))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// errUnauthorized is any credential that does not check out. One error for
// every reason, so a caller learns nothing about which part was wrong.
var errUnauthorized = errors.New("not a valid registration")

// parseCredential checks an Authorization header and returns the id it
// proves.
func parseCredential(secret []byte, header string) (string, error) {
	rest, ok := strings.CutPrefix(header, "Bearer ")
	if !ok {
		return "", errUnauthorized
	}
	id, token, ok := strings.Cut(strings.TrimSpace(rest), ".")
	if !ok || !validID(id) {
		return "", errUnauthorized
	}
	want := tokenFor(secret, id)
	if subtle.ConstantTimeCompare([]byte(token), []byte(want)) != 1 {
		return "", errUnauthorized
	}
	return id, nil
}

// cgnat is 100.64.0.0/10, shared address space. Not "private" to netip, but it
// is what Tailscale hands out and what some ISPs put a whole home behind, and
// either way nothing on the public internet answers there.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// checkAddress accepts an address a name may point at, and says why not
// otherwise. The rule is "nothing a stranger's browser could reach", because a
// real certificate on a public address is a phishing kit.
func checkAddress(s string) (netip.Addr, error) {
	addr, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil {
		return netip.Addr{}, fmt.Errorf("%q is not an IP address", s)
	}
	addr = addr.Unmap()
	if addr.Zone() != "" {
		return netip.Addr{}, fmt.Errorf("%s has a zone, which DNS cannot carry", addr)
	}
	if addr.IsPrivate() || cgnat.Contains(addr) {
		return addr, nil
	}
	return netip.Addr{}, fmt.Errorf("%s is not a private address; only home-network addresses can be named", addr)
}
