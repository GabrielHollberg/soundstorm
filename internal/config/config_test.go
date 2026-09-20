package config

import (
	"strings"
	"testing"
	"time"
)

const minimal = `{
  // a comment, to prove the file can be annotated
  "listen": ":9000",
  "perSourceTimeout": "3s",
  "sources": [
    {"id": "music", "type": "subsonic", "baseUrl": "https://music.example.com",
     "username": "u", "password": "p"}
  ]
}`

func TestParseMinimal(t *testing.T) {
	cfg, err := Parse([]byte(minimal))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Listen != ":9000" {
		t.Errorf("listen = %q", cfg.Listen)
	}
	if cfg.PerSourceTimeout.Std() != 3*time.Second {
		t.Errorf("perSourceTimeout = %v", cfg.PerSourceTimeout.Std())
	}
	if len(cfg.Sources) != 1 {
		t.Fatalf("want 1 source, got %d", len(cfg.Sources))
	}
}

func TestParseExpandsEnvironment(t *testing.T) {
	t.Setenv("ATRIUM_TEST_PASSWORD", "hunter2")
	src := `{"sources":[{"id":"m","type":"subsonic","baseUrl":"https://x.example.com",
	         "username":"u","password":"${ATRIUM_TEST_PASSWORD}"}]}`

	cfg, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := cfg.Sources[0].Password; got != "hunter2" {
		t.Errorf("password was not expanded from the environment: %q", got)
	}
}

func TestParseAppliesDefaults(t *testing.T) {
	cfg, err := Parse([]byte(`{"sources":[{"id":"m","type":"kiwix","baseUrl":"http://x.example.com"}]}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Listen != ":8080" {
		t.Errorf("default listen = %q, want :8080", cfg.Listen)
	}
	if cfg.PerSourceTimeout.Std() != 5*time.Second {
		t.Errorf("default timeout = %v, want 5s", cfg.PerSourceTimeout.Std())
	}
}

func TestParseRejectsBadConfigs(t *testing.T) {
	cases := map[string]struct{ src, wantErr string }{
		"no sources":    {`{"listen":":8080","sources":[]}`, "no sources"},
		"missing id":    {`{"sources":[{"type":"kiwix","baseUrl":"http://x.example.com"}]}`, "id is required"},
		"missing type":  {`{"sources":[{"id":"a","baseUrl":"http://x.example.com"}]}`, "type is required"},
		"missing url":   {`{"sources":[{"id":"a","type":"kiwix"}]}`, "baseUrl is required"},
		"duplicate id":  {`{"sources":[{"id":"a","type":"kiwix","baseUrl":"http://x.example.com"},{"id":"a","type":"kiwix","baseUrl":"http://y.example.com"}]}`, "duplicate id"},
		"unknown field": {`{"sources":[{"id":"a","type":"kiwix","baseUrl":"http://x.example.com"}],"lissen":":80"}`, "unknown field"},
		"bad duration":  {`{"perSourceTimeout":"5 fortnights","sources":[{"id":"a","type":"kiwix","baseUrl":"http://x.example.com"}]}`, "parse duration"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(tc.src))
			if err == nil {
				t.Fatalf("want an error mentioning %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestBuildRegistryRejectsUnknownType(t *testing.T) {
	cfg, err := Parse([]byte(`{"sources":[{"id":"a","type":"plex","baseUrl":"http://x.example.com"}]}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := cfg.BuildRegistry(); err == nil || !strings.Contains(err.Error(), "unknown type") {
		t.Errorf("want an unknown-type error, got %v", err)
	}
}

func TestBuildRegistryRequiresCredentials(t *testing.T) {
	// A Subsonic source without a password is a configuration error we want
	// at boot, not a mystery empty result later.
	cfg, err := Parse([]byte(`{"sources":[{"id":"m","type":"subsonic","baseUrl":"https://x.example.com","username":"u"}]}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := cfg.BuildRegistry(); err == nil {
		t.Error("want an error when a subsonic source has no password")
	}
}
