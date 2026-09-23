package auth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// The sign-in form is the one endpoint anybody can reach without an account,
// and every request to it costs 600,000 rounds of PBKDF2. That cost is what
// makes guessing slow, and on its own it is also a way to pin every core of a
// Raspberry Pi with a handful of parallel requests. So two limits, which
// answer those two problems separately:
//
//   - A small number of hashes may run at once, whoever asks. That caps the
//     CPU a flood can take no matter how many addresses it comes from.
//   - A client that keeps getting it wrong waits, doubling each time. A person
//     who mistypes three times never notices; a script notices at once.
//
// Clients are told apart by their address. Behind Docker Desktop or the
// Tailscale sidecar everybody can arrive from one address, so a failing script
// there slows everybody's sign-in too. That is why the waits are capped at a
// few minutes rather than being a lockout: slower for a while is an acceptable
// cost of an attack, a household locked out of its own server is not.
const (
	// freeFailures is how many wrong passwords cost nothing extra.
	freeFailures = 5
	// firstWait follows the first failure past the free ones, and doubles.
	firstWait = time.Second
	// maxWait caps the doubling.
	maxWait = 5 * time.Minute
	// forgetAfter clears a client that has stopped failing.
	forgetAfter = time.Hour

	// concurrentHashes is how many sign-in hashes may run at once.
	concurrentHashes = 2
	// queueWait is how long a sign-in waits for a free slot before being
	// told to come back. Long enough to absorb a household arriving at once.
	queueWait = 10 * time.Second

	// maxTracked bounds memory against a flood from many addresses.
	maxTracked = 4096
)

// ThrottledError says a sign-in was refused without being checked, and when
// trying again would be worth it.
type ThrottledError struct {
	RetryAfter time.Duration
}

func (e *ThrottledError) Error() string {
	secs := int(e.RetryAfter.Round(time.Second) / time.Second)
	if secs < 1 {
		secs = 1
	}
	return fmt.Sprintf("too many sign-in attempts; try again in %d seconds", secs)
}

// IsThrottled reports whether err refused a sign-in without checking it.
func IsThrottled(err error) (*ThrottledError, bool) {
	var t *ThrottledError
	ok := errors.As(err, &t)
	return t, ok
}

type strikes struct {
	count int
	last  time.Time
}

type throttle struct {
	mu      sync.Mutex
	clients map[string]strikes
	slots   chan struct{}
	now     func() time.Time
}

func newThrottle() *throttle {
	return &throttle{
		clients: map[string]strikes{},
		slots:   make(chan struct{}, concurrentHashes),
		now:     time.Now,
	}
}

// wait returns how long client must still wait before its next attempt.
func (t *throttle) wait(client string) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.clients[client]
	if !ok || s.count < freeFailures {
		return 0
	}
	penalty := firstWait << (s.count - freeFailures)
	if penalty > maxWait || penalty <= 0 {
		penalty = maxWait
	}
	if left := s.last.Add(penalty).Sub(t.now()); left > 0 {
		return left
	}
	return 0
}

func (t *throttle) fail(client string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	if len(t.clients) >= maxTracked {
		t.pruneLocked(now)
	}
	s := t.clients[client]
	if now.Sub(s.last) > forgetAfter {
		s.count = 0
	}
	s.count++
	s.last = now
	t.clients[client] = s
}

func (t *throttle) succeed(client string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.clients, client)
}

// pruneLocked forgets idle clients, and if that is not enough, everybody.
// Forgetting is the safe direction: it can only make sign-in easier.
func (t *throttle) pruneLocked(now time.Time) {
	for c, s := range t.clients {
		if now.Sub(s.last) > forgetAfter {
			delete(t.clients, c)
		}
	}
	if len(t.clients) >= maxTracked {
		t.clients = map[string]strikes{}
	}
}

// acquire takes a hashing slot, or reports how long to back off.
func (t *throttle) acquire(ctx context.Context) (func(), error) {
	timer := time.NewTimer(queueWait)
	defer timer.Stop()
	select {
	case t.slots <- struct{}{}:
		return func() { <-t.slots }, nil
	case <-timer.C:
		return nil, &ThrottledError{RetryAfter: queueWait}
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// guarded runs check - a password comparison - behind both limits, and
// records the outcome against client. Only ErrInvalidCredentials counts as a
// failure; a server error is not the client's fault.
func (t *throttle) guarded(ctx context.Context, client string, check func() error) error {
	if left := t.wait(client); left > 0 {
		return &ThrottledError{RetryAfter: left}
	}
	release, err := t.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()

	err = check()
	switch {
	case err == nil:
		t.succeed(client)
	case errors.Is(err, ErrInvalidCredentials):
		t.fail(client)
	}
	return err
}
