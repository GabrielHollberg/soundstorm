package provision

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"syscall"
	"testing"

	"github.com/gabehollberg/soundstorm/internal/httpx"
)

// This guards a bug that cost a working stack twice, in two different layers.
//
// On restart SoundStorm comes up before Jellyfin does. The health check fails,
// and if that failure is read as "these credentials are wrong", SoundStorm
// discards good credentials and falls through to provisioning - which can never
// succeed against a backend that is already set up. One unlucky restart and the
// backend is permanently broken with its working token still sitting on disk.
//
// The first fix caught only transport errors. Jellyfin accepts the connection
// while it loads and answers 503, which sailed straight through.
func TestTransientDistinguishesNotReadyFromRefused(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "jellyfin still loading",
			err:  fmt.Errorf("health check: %w", &httpx.StatusError{Status: 503, Body: "Jellyfin Server is loading. Please try again shortly."}),
			want: true,
		},
		{
			name: "bad gateway",
			err:  fmt.Errorf("health check: %w", &httpx.StatusError{Status: http.StatusBadGateway}),
			want: true,
		},
		{
			name: "rate limited",
			err:  fmt.Errorf("health check: %w", &httpx.StatusError{Status: http.StatusTooManyRequests}),
			want: true,
		},
		{
			name: "connection refused",
			err:  fmt.Errorf("health check: %w", &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}),
			want: true,
		},
		{
			name: "host not resolvable yet",
			err:  fmt.Errorf("health check: %w", &net.DNSError{Name: "jellyfin"}),
			want: true,
		},
		{
			name: "deadline exceeded",
			err:  fmt.Errorf("health check: %w", context.DeadlineExceeded),
			want: true,
		},
		{
			// The one case that genuinely means "your credentials are wrong",
			// and the only one that should trigger re-provisioning.
			name: "unauthorised",
			err:  fmt.Errorf("health check: %w", &httpx.StatusError{Status: http.StatusUnauthorized}),
			want: false,
		},
		{
			name: "forbidden",
			err:  fmt.Errorf("health check: %w", &httpx.StatusError{Status: http.StatusForbidden}),
			want: false,
		},
		{
			name: "library missing",
			err:  errors.New("library \"abc\" not found on this server"),
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := transient(tc.err); got != tc.want {
				t.Errorf("transient(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestStatusErrorUnwrapsThroughWrapping(t *testing.T) {
	// register() wraps the health check error, so errors.As has to reach
	// through at least one layer of fmt.Errorf.
	wrapped := fmt.Errorf("start source: %w",
		fmt.Errorf("health check: %w", &httpx.StatusError{Status: 503}))

	var statusErr *httpx.StatusError
	if !errors.As(wrapped, &statusErr) {
		t.Fatal("StatusError did not survive wrapping")
	}
	if statusErr.Status != 503 {
		t.Errorf("status = %d, want 503", statusErr.Status)
	}
	if !statusErr.Temporary() {
		t.Error("503 should be temporary")
	}
}
