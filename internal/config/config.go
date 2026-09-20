// Package config loads atrium's configuration and turns it into live sources.
//
// The file is JSON with comments (see internal/jsonc), so it can be annotated
// in place without a third-party parser. Any ${VAR} in a string value is
// expanded from the environment at load time, which keeps secrets out of the
// file and makes it safe to commit.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gabehollberg/atrium/internal/jsonc"
	"github.com/gabehollberg/atrium/internal/source"
	"github.com/gabehollberg/atrium/internal/source/audiobookshelf"
	"github.com/gabehollberg/atrium/internal/source/jellyfin"
	"github.com/gabehollberg/atrium/internal/source/kiwix"
	"github.com/gabehollberg/atrium/internal/source/opds"
	"github.com/gabehollberg/atrium/internal/source/subsonic"
)

// Duration wraps time.Duration so the config can say "5s" instead of
// 5000000000.
type Duration time.Duration

// UnmarshalJSON accepts either a duration string ("5s") or a number of
// nanoseconds.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var asString string
	if err := json.Unmarshal(b, &asString); err == nil {
		parsed, err := time.ParseDuration(asString)
		if err != nil {
			return fmt.Errorf("parse duration %q: %w", asString, err)
		}
		*d = Duration(parsed)
		return nil
	}
	var asNumber int64
	if err := json.Unmarshal(b, &asNumber); err != nil {
		return fmt.Errorf("duration must be a string like \"5s\" or a number of nanoseconds")
	}
	*d = Duration(asNumber)
	return nil
}

// Std returns the wrapped time.Duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }

// Config is the whole configuration file.
type Config struct {
	Listen           string         `json:"listen"`
	PerSourceTimeout Duration       `json:"perSourceTimeout"`
	EnableProbe      bool           `json:"enableProbe"`
	Sources          []SourceConfig `json:"sources"`
}

// SourceConfig is one backend. Which fields matter depends on Type.
type SourceConfig struct {
	ID      string   `json:"id"`
	Type    string   `json:"type"`
	BaseURL string   `json:"baseUrl"`
	Timeout Duration `json:"timeout"`

	// subsonic, opds
	Username string `json:"username"`
	Password string `json:"password"`

	// jellyfin
	APIKey string `json:"apiKey"`
	UserID string `json:"userId"`

	// audiobookshelf
	Token     string `json:"token"`
	LibraryID string `json:"libraryId"`

	// opds
	SearchPath string `json:"searchPath"`

	// kiwix
	Book string `json:"book"`
}

// Load reads, expands and validates a configuration file.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	return Parse(raw)
}

// Parse handles the bytes of a configuration file. Split out from Load so it
// is testable without touching the filesystem.
func Parse(raw []byte) (*Config, error) {
	// Strip comments first, then expand ${VAR} so a commented-out secret is
	// never expanded.
	expanded := os.ExpandEnv(string(jsonc.Strip(raw)))

	var cfg Config
	dec := json.NewDecoder(strings.NewReader(expanded))
	dec.DisallowUnknownFields() // a typo'd key should fail loudly, not silently
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Listen == "" {
		c.Listen = ":8080"
	}
	if c.PerSourceTimeout <= 0 {
		c.PerSourceTimeout = Duration(5 * time.Second)
	}
}

func (c *Config) validate() error {
	if len(c.Sources) == 0 {
		return fmt.Errorf("config defines no sources")
	}
	seen := make(map[string]bool, len(c.Sources))
	for i, s := range c.Sources {
		switch {
		case s.ID == "":
			return fmt.Errorf("sources[%d]: id is required", i)
		case seen[s.ID]:
			return fmt.Errorf("sources[%d]: duplicate id %q", i, s.ID)
		case s.Type == "":
			return fmt.Errorf("source %q: type is required", s.ID)
		case s.BaseURL == "":
			return fmt.Errorf("source %q: baseUrl is required", s.ID)
		}
		seen[s.ID] = true
	}
	return nil
}

// BuildRegistry turns the configuration into live sources.
//
// A source that fails to build stops startup: better to fail loudly at boot
// than to serve a silently incomplete library.
func (c *Config) BuildRegistry() (*source.Registry, error) {
	reg := source.NewRegistry()
	for _, sc := range c.Sources {
		timeout := sc.Timeout.Std()
		if timeout <= 0 {
			timeout = c.PerSourceTimeout.Std()
		}

		var (
			s   source.Source
			err error
		)
		switch sc.Type {
		case "subsonic", "navidrome":
			s, err = subsonic.New(subsonic.Config{
				ID: sc.ID, BaseURL: sc.BaseURL, Username: sc.Username,
				Password: sc.Password, Timeout: timeout,
			})
		case "jellyfin", "emby":
			s, err = jellyfin.New(jellyfin.Config{
				ID: sc.ID, BaseURL: sc.BaseURL, APIKey: sc.APIKey,
				UserID: sc.UserID, Timeout: timeout,
			})
		case "audiobookshelf", "abs":
			s, err = audiobookshelf.New(audiobookshelf.Config{
				ID: sc.ID, BaseURL: sc.BaseURL, Token: sc.Token,
				LibraryID: sc.LibraryID, Timeout: timeout,
			})
		case "opds", "calibre":
			s, err = opds.New(opds.Config{
				ID: sc.ID, BaseURL: sc.BaseURL, SearchPath: sc.SearchPath,
				Username: sc.Username, Password: sc.Password, Timeout: timeout,
			})
		case "kiwix":
			s, err = kiwix.New(kiwix.Config{
				ID: sc.ID, BaseURL: sc.BaseURL, Book: sc.Book, Timeout: timeout,
			})
		default:
			return nil, fmt.Errorf("source %q: unknown type %q (want subsonic, jellyfin, audiobookshelf, opds or kiwix)", sc.ID, sc.Type)
		}
		if err != nil {
			return nil, err
		}
		reg.Add(s)
	}
	return reg, nil
}
