package names

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The Android app's Digital Asset Links: what lets a camera open a TV's
// sign-in code in the app. Its package and fingerprint, nothing else.
func TestAssetLinksNameTheApp(t *testing.T) {
	s := &Server{Secret: make([]byte, 32), Zone: "soundstorm.dev", Label: "home", AndroidCerts: []string{"AA:BB"}}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/.well-known/assetlinks.json", nil))
	var got []struct {
		Relation []string `json:"relation"`
		Target   struct {
			Namespace   string   `json:"namespace"`
			PackageName string   `json:"package_name"`
			Fingerprint []string `json:"sha256_cert_fingerprints"`
		} `json:"target"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusOK || len(got) != 1 {
		t.Fatalf("%d %s %v", rec.Code, rec.Body, err)
	}
	if got[0].Target.PackageName != "dev.soundstorm.app" || got[0].Target.Fingerprint[0] != "AA:BB" ||
		got[0].Relation[0] != "delegate_permission/common.handle_all_urls" {
		t.Fatalf("%+v", got[0])
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" && ct != "application/json; charset=utf-8" {
		t.Fatalf("content type %q", ct)
	}
}
