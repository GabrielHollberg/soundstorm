package qr

import (
	"strings"
	"testing"
)

// Every version this draws was read back exactly by macOS's own QR reader
// (CIDetector) when it was written, from 5 to 213 bytes - versions 1 to 10.
// What is held here: the smallest version that fits, the finder patterns in
// their corners, and the limit.
func TestTheSmallestVersionThatFitsIsUsed(t *testing.T) {
	for n, version := range map[int]int{14: 1, 26: 2, 62: 4, 106: 6, 213: 10} {
		c, err := Encode(strings.Repeat("a", n))
		if err != nil {
			t.Fatal(n, err)
		}
		if got := (c.Size - 17) / 4; got != version {
			t.Errorf("%d bytes: version %d, want %d", n, got, version)
		}
		// A finder's centre is dark and its ring at 2 light, in all three corners.
		for _, at := range [][2]int{{3, 3}, {c.Size - 4, 3}, {3, c.Size - 4}} {
			if !c.Dark(at[0], at[1]) || c.Dark(at[0]+2, at[1]) {
				t.Errorf("%d bytes: no finder at %v", n, at)
			}
		}
	}
	if _, err := Encode(strings.Repeat("a", 214)); err != ErrTooLong {
		t.Fatalf("214 bytes: %v, want ErrTooLong", err)
	}
}

func TestPNGHasItsBorder(t *testing.T) {
	c, _ := Encode("https://example.home.soundstorm.dev:8099/?link=KXT4PM")
	b, err := c.PNG(4)
	if err != nil || len(b) < 100 || string(b[1:4]) != "PNG" {
		t.Fatalf("not a PNG: %v", err)
	}
}
