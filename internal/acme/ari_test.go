package acme

import (
	"context"
	"crypto/x509"
	"encoding/hex"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// RFC 9773's own example: the authority key identifier and the serial, each
// base64url, joined by a dot - the serial with its leading zero byte, as DER
// writes a positive INTEGER whose top bit is set.
func TestCertIDIsTheRFCsExample(t *testing.T) {
	aki, _ := hex.DecodeString("69885B6B87464041E1B37B847BA0AE2CDE01C8D4")
	serial, _ := new(big.Int).SetString("0087654321", 16)
	id, err := CertID(&x509.Certificate{AuthorityKeyId: aki, SerialNumber: serial})
	if err != nil || id != "aYhba4dGQEHhs3uEe6CuLN4ByNQ.AIdlQyE" {
		t.Fatalf("CertID = %q, %v", id, err)
	}
}

// The window is read from the directory's renewalInfo, by the certificate's
// id, with Retry-After for when to ask again; an authority without it says so.
func TestRenewalWindowIsAsked(t *testing.T) {
	var asked string
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/dir", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"newNonce":"` + srv.URL + `/nonce","newAccount":"` + srv.URL + `/acct","newOrder":"` + srv.URL + `/order","renewalInfo":"` + srv.URL + `/ari"}`))
	})
	mux.HandleFunc("/ari/", func(w http.ResponseWriter, r *http.Request) {
		asked = strings.TrimPrefix(r.URL.Path, "/ari/")
		w.Header().Set("Retry-After", "21600")
		w.Write([]byte(`{"suggestedWindow":{"start":"2026-11-01T00:00:00Z","end":"2026-11-03T00:00:00Z"}}`))
	})
	mux.HandleFunc("/plain", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"newNonce":"x","newAccount":"x","newOrder":"x"}`))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	leaf := &x509.Certificate{AuthorityKeyId: []byte{1, 2, 3}, SerialNumber: big.NewInt(0x80)}
	c := &Client{Directory: srv.URL + "/dir"}
	start, end, again, err := c.RenewalWindow(context.Background(), leaf)
	if err != nil {
		t.Fatal(err)
	}
	if asked != "AQID.AIA" || again != 6*time.Hour ||
		!start.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) || !end.Equal(time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("asked %q: %v to %v, again in %v", asked, start, end, again)
	}
	if _, _, _, err := (&Client{Directory: srv.URL + "/plain"}).RenewalWindow(context.Background(), leaf); err != ErrNoRenewalInfo {
		t.Fatalf("an authority without renewal information: %v", err)
	}
}
