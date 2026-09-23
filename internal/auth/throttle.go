package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
//
// The account being guessed at is counted too, whoever is asking. An
// address-only limit is five free guesses per address, which from the
// internet is five per address an attacker can borrow; per account it is a
// guess a minute however many there are. That cost falls on the real owner
// only while somebody is actively guessing their name, is at most a minute,
// and never touches a device that is already signed in.
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

	// accountFreeFailures, accountFirstWait and accountMaxWait are the same
	// three for an account name, whatever address the guesses come from.
	// More free failures than an address gets, because a whole household's
	// typos land here; a shorter cap, because this one reaches the owner.
	accountFreeFailures = 10
	accountFirstWait    = 2 * time.Second
	accountMaxWait      = time.Minute

	// inflightRetry is how long a request refused for concurrency is told to
	// wait. Short: it is not a wrong password, just too many at once.
	inflightRetry = time.Second

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

// ledger counts failures per key and says how long a key must wait.
type ledger struct {
	free       int
	first, max time.Duration
	keys       map[string]strikes
}

type throttle struct {
	mu       sync.Mutex
	clients  *ledger
	accounts *ledger
	inflight map[string]int // guesses being hashed right now, per account
	slots    chan struct{}
	now      func() time.Time
}

func newThrottle() *throttle {
	return &throttle{
		clients:  &ledger{free: freeFailures, first: firstWait, max: maxWait, keys: map[string]strikes{}},
		accounts: &ledger{free: accountFreeFailures, first: accountFirstWait, max: accountMaxWait, keys: map[string]strikes{}},
		inflight: map[string]int{},
		slots:    make(chan struct{}, concurrentHashes),
		now:      time.Now,
	}
}

func (l *ledger) wait(key string, now time.Time) time.Duration {
	s, ok := l.keys[key]
	if !ok || s.count < l.free {
		return 0
	}
	penalty := l.first << (s.count - l.free)
	if penalty > l.max || penalty <= 0 {
		penalty = l.max
	}
	if left := s.last.Add(penalty).Sub(now); left > 0 {
		return left
	}
	return 0
}

func (l *ledger) fail(key string, now time.Time) {
	if len(l.keys) >= maxTracked {
		l.prune(now)
	}
	s := l.keys[key]
	if now.Sub(s.last) > forgetAfter {
		s.count = 0
	}
	s.count++
	s.last = now
	l.keys[key] = s
}

// prune forgets idle keys, then keys that have cost nothing yet, and only
// then everybody. The middle step is what stops a spray of made-up names
// from flushing the one account actually under attack. Forgetting is the
// safe direction: it can only make sign-in easier.
func (l *ledger) prune(now time.Time) {
	for k, s := range l.keys {
		if now.Sub(s.last) > forgetAfter {
			delete(l.keys, k)
		}
	}
	if len(l.keys) >= maxTracked {
		for k, s := range l.keys {
			if s.count < l.free {
				delete(l.keys, k)
			}
		}
	}
	if len(l.keys) >= maxTracked {
		l.keys = map[string]strikes{}
	}
}

// wait returns how long client must still wait before its next attempt.
func (t *throttle) wait(client string) time.Duration {
	return t.waitFor(client, "")
}

// waitFor is the longer of the client's wait and the account's.
func (t *throttle) waitFor(client, account string) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	w := t.clients.wait(client, now)
	if account != "" {
		w = max(w, t.accounts.wait(account, now))
	}
	return w
}

func (t *throttle) fail(client string) { t.failFor(client, "") }

func (t *throttle) failFor(client, account string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	t.clients.fail(client, now)
	if account != "" {
		t.accounts.fail(account, now)
	}
}

func (t *throttle) succeed(client, account string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.clients.keys, client)
	if account != "" {
		delete(t.accounts.keys, account)
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

// guarded runs check - a password comparison - behind every limit, and
// records the outcome against client and account. account is the name being
// signed in as, folded to lower case, whether or not it exists: counting only
// real ones would make the 429 a way to find out who has an account. Only
// ErrInvalidCredentials counts as a failure; a server error is not the
// client's fault.
func (t *throttle) guarded(ctx context.Context, client, account string, check func() error) error {
	account = strings.ToLower(strings.TrimSpace(account))

	// The backoff above only bites once a failure has been recorded, and a
	// failure is only recorded after the hash finishes. So a burst sent all at
	// once all passes the wait check before any of them has failed, and only
	// the global two-slot cap limits it - which is the whole day's guessing
	// budget handed over at a few per second. Reserving the account here, under
	// the same lock as the wait check, is what makes a burst against one name
	// wait for each other: the second and later ones are turned away until the
	// first has failed and left its strike, so the backoff catches up.
	if left, ok := t.reserve(client, account); !ok {
		return &ThrottledError{RetryAfter: left}
	}
	defer t.releaseInflight(account)

	release, err := t.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()

	err = check()
	switch {
	case err == nil:
		t.succeed(client, account)
	case errors.Is(err, ErrInvalidCredentials):
		t.failFor(client, account)
	}
	return err
}

// reserve checks the backoff and, if a named account is being signed in to,
// admits only one guess against it at a time. It returns how long to wait when
// it refuses.
func (t *throttle) reserve(client, account string) (time.Duration, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	if w := t.clients.wait(client, now); w > 0 {
		return w, false
	}
	if account != "" {
		if w := t.accounts.wait(account, now); w > 0 {
			return w, false
		}
		if t.inflight[account] > 0 {
			return inflightRetry, false
		}
		t.inflight[account]++
	}
	return 0, true
}

func (t *throttle) releaseInflight(account string) {
	if account == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if n := t.inflight[account]; n <= 1 {
		delete(t.inflight, account)
	} else {
		t.inflight[account] = n - 1
	}
}
