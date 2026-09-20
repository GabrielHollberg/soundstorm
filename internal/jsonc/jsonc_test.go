package jsonc

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStripLineAndBlockComments(t *testing.T) {
	src := []byte(`{
  // the port to listen on
  "listen": ":8080",
  /* multi
     line */
  "enableProbe": false
}`)
	var out struct {
		Listen      string `json:"listen"`
		EnableProbe bool   `json:"enableProbe"`
	}
	if err := json.Unmarshal(Strip(src), &out); err != nil {
		t.Fatalf("decode after strip: %v", err)
	}
	if out.Listen != ":8080" {
		t.Errorf("listen = %q", out.Listen)
	}
}

// The subtle case: a URL contains // and must survive untouched.
func TestStripLeavesURLsInStringsAlone(t *testing.T) {
	src := []byte(`{"baseUrl": "https://music.soundstorm.dev/opds"}`)
	var out struct {
		BaseURL string `json:"baseUrl"`
	}
	if err := json.Unmarshal(Strip(src), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.BaseURL != "https://music.soundstorm.dev/opds" {
		t.Errorf("a URL inside a string was mangled: %q", out.BaseURL)
	}
}

func TestStripHandlesEscapedQuotes(t *testing.T) {
	src := []byte(`{"password": "a\"b // not a comment"}`)
	var out struct {
		Password string `json:"password"`
	}
	if err := json.Unmarshal(Strip(src), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Password != `a"b // not a comment` {
		t.Errorf("escaped quote handling broke the value: %q", out.Password)
	}
}

func TestStripPreservesLineCount(t *testing.T) {
	src := []byte("{\n// one\n/* two\nthree */\n}")
	if got, want := strings.Count(string(Strip(src)), "\n"), strings.Count(string(src), "\n"); got != want {
		t.Errorf("line count changed: got %d, want %d", got, want)
	}
}
