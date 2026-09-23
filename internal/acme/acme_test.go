package acme

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
)

// The signature has to be R||S at fixed width over "protected.payload" - the
// DER crypto/ecdsa produces by default is rejected by every ACME server, and
// a leading zero byte dropped from R or S fails about one time in 128, which
// is exactly the kind of bug that passes a handful of runs.
func TestJWSVerifiesAsES256(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	c := &Client{Key: key, nonces: nil}

	for i := 0; i < 300; i++ {
		c.nonces = []string{"nonce"}
		c.kid = "https://ca.example/acct/1"
		raw, err := c.sign(context.Background(), "https://ca.example/new-order", map[string]int{"n": i})
		if err != nil {
			t.Fatalf("sign: %v", err)
		}
		var jws struct{ Protected, Payload, Signature string }
		json.Unmarshal(raw, &jws)

		sig, _ := base64.RawURLEncoding.DecodeString(jws.Signature)
		if len(sig) != 64 {
			t.Fatalf("signature is %d bytes, want 64", len(sig))
		}
		digest := sha256.Sum256([]byte(jws.Protected + "." + jws.Payload))
		r, s := new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])
		if !ecdsa.Verify(&key.PublicKey, digest[:], r, s) {
			t.Fatalf("signature %d does not verify", i)
		}
	}
}

// POST-as-GET is an empty payload, not "{}" and not "null": the three mean
// different things to the server, and only the first reads a resource.
func TestPostAsGetHasAnEmptyPayload(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	c := &Client{Key: key, nonces: []string{"n"}, kid: "k"}
	raw, _ := c.sign(context.Background(), "https://ca.example/authz/1", nil)
	if !strings.Contains(string(raw), `"payload":""`) {
		t.Errorf("POST-as-GET payload is not empty: %s", raw)
	}
}
