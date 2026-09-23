package auth

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/state"
)

func newManager(t *testing.T) *Manager {
	t.Helper()
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	return New(store)
}

// A fake clock, so the throttle's waits can be walked through without
// actually waiting.
type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func TestThrottleForgivesTyposAndThenDoubles(t *testing.T) {
	c := &clock{t: time.Unix(1_700_000_000, 0)}
	th := newThrottle()
	th.now = c.now

	for i := 0; i < freeFailures; i++ {
		if w := th.wait("a"); w != 0 {
			t.Fatalf("failure %d already waits %v; typos should be free", i, w)
		}
		th.fail("a")
	}
	want := firstWait
	for i := 0; i < 6; i++ {
		if w := th.wait("a"); w != want {
			t.Fatalf("after %d failures: wait %v, want %v", freeFailures+i, w, want)
		}
		c.advance(want)
		if w := th.wait("a"); w != 0 {
			t.Fatalf("still waiting %v after the wait passed", w)
		}
		th.fail("a")
		want *= 2
	}

	// Other clients are unaffected.
	if w := th.wait("b"); w != 0 {
		t.Errorf("an unrelated client waits %v", w)
	}
}

func TestThrottleIsCappedAndForgets(t *testing.T) {
	c := &clock{t: time.Unix(1_700_000_000, 0)}
	th := newThrottle()
	th.now = c.now

	for i := 0; i < 100; i++ {
		th.fail("a")
	}
	if w := th.wait("a"); w != maxWait {
		t.Errorf("wait after 100 failures = %v, want the cap %v", w, maxWait)
	}

	// A client that goes quiet starts over rather than inheriting its record.
	c.advance(forgetAfter + time.Second)
	th.fail("a")
	if w := th.wait("a"); w != 0 {
		t.Errorf("an hour of quiet did not clear the record: waits %v", w)
	}
}

func TestThrottleClearsOnSuccess(t *testing.T) {
	th := newThrottle()
	for i := 0; i < freeFailures+3; i++ {
		th.fail("a")
	}
	th.succeed("a", "")
	if w := th.wait("a"); w != 0 {
		t.Errorf("a successful sign-in left a wait of %v", w)
	}
}

// Only a wrong password is the client's fault. A server error that counted as
// a strike would lock somebody out for the server's own failure.
func TestThrottleCountsOnlyWrongPasswords(t *testing.T) {
	th := newThrottle()
	boom := errors.New("disk full")
	for i := 0; i < freeFailures*2; i++ {
		if err := th.guarded(context.Background(), "a", "", func() error { return boom }); !errors.Is(err, boom) {
			t.Fatalf("err = %v", err)
		}
	}
	if w := th.wait("a"); w != 0 {
		t.Errorf("server errors were counted as guesses: waits %v", w)
	}
}

// However many ask at once, only a few hashes run - that is what keeps a flood
// of sign-ins from taking every core.
func TestThrottleBoundsConcurrentHashes(t *testing.T) {
	th := newThrottle()
	var running, peak atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = th.guarded(context.Background(), "a", "", func() error {
				n := running.Add(1)
				for {
					p := peak.Load()
					if n <= p || peak.CompareAndSwap(p, n) {
						break
					}
				}
				time.Sleep(20 * time.Millisecond)
				running.Add(-1)
				return nil
			})
		}()
	}
	wg.Wait()
	if p := peak.Load(); p > concurrentHashes {
		t.Errorf("%d hashes ran at once, limit is %d", p, concurrentHashes)
	}
}

func TestSignInThrottlesAndStillWorks(t *testing.T) {
	m := newManager(t)
	if _, err := m.Signup("gabe", "correct horse"); err != nil {
		t.Fatalf("signup: %v", err)
	}
	ctx := context.Background()

	if _, _, _, err := m.SignIn(ctx, "1.2.3.4", "gabe", "correct horse"); err != nil {
		t.Fatalf("sign in: %v", err)
	}
	for i := 0; i < freeFailures; i++ {
		if _, _, _, err := m.SignIn(ctx, "1.2.3.4", "gabe", "nope nope"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: err = %v", i, err)
		}
	}
	_, _, _, err := m.SignIn(ctx, "1.2.3.4", "gabe", "correct horse")
	if _, ok := IsThrottled(err); !ok {
		t.Errorf("sign-in after %d failures was not throttled: %v", freeFailures, err)
	}
	// Somebody else in the house is unaffected.
	if _, _, _, err := m.SignIn(ctx, "5.6.7.8", "gabe", "correct horse"); err != nil {
		t.Errorf("a different client was throttled: %v", err)
	}
}

func TestChangeOwnPassword(t *testing.T) {
	m := newManager(t)
	owner, err := m.Signup("gabe", "correct horse")
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	here, _, _, err := m.Login("gabe", "correct horse")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	elsewhere, _, _, err := m.Login("gabe", "correct horse")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	ctx := context.Background()

	err = m.ChangeOwnPassword(ctx, "c", owner, "wrong guess", "battery staple", here)
	if !errors.Is(err, ErrWrongCurrentPassword) {
		t.Fatalf("wrong current password: err = %v", err)
	}
	// A new password too short to accept is refused before any hash, and
	// without costing the person a strike.
	if err := m.ChangeOwnPassword(ctx, "c", owner, "correct horse", "short", here); err == nil {
		t.Fatal("a short password was accepted")
	}

	if err := m.ChangeOwnPassword(ctx, "c", owner, "correct horse", "battery staple", here); err != nil {
		t.Fatalf("change: %v", err)
	}
	if _, _, _, err := m.Login("gabe", "battery staple"); err != nil {
		t.Errorf("new password does not work: %v", err)
	}
	if _, ok := m.store.SessionUser(here); !ok {
		t.Error("the session that made the change was signed out")
	}
	if _, ok := m.store.SessionUser(elsewhere); ok {
		t.Error("another session survived the change")
	}
}

func TestOnlyTheOwnerManagesAccounts(t *testing.T) {
	m := newManager(t)
	owner, err := m.Signup("gabe", "correct horse")
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	member, err := m.CreateUser(owner, "sam", "a long enough one", state.RoleOwner)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if member.IsOwner() {
		t.Error("a second owner was created")
	}
	if _, err := m.CreateUser(member, "eve", "a long enough one", state.RoleMember); !errors.Is(err, ErrForbidden) {
		t.Errorf("a member created an account: %v", err)
	}
	if err := m.ResetPassword(member, owner.ID, "taken over now", ""); !errors.Is(err, ErrForbidden) {
		t.Errorf("a member reset the owner's password: %v", err)
	}
	if err := m.SetLibraries(owner, owner.ID, []string{}); err == nil {
		t.Error("the owner's own libraries were narrowed")
	}
	if err := m.SetLibraries(owner, member.ID, []string{"not-a-kind"}); err == nil {
		t.Error("an unknown library was accepted")
	}
	if err := m.DeleteUser(owner, owner.ID); err == nil {
		t.Error("the owner removed themselves")
	}
}

// From the internet an attacker has as many addresses as they can borrow,
// and a limit per address is five free guesses from each. The name being
// guessed is counted too, so a spread-out attack slows down all the same.
func TestGuessesFromManyAddressesAreLimitedPerAccount(t *testing.T) {
	th := newThrottle()
	wrong := func() error { return ErrInvalidCredentials }
	for i := 0; i < accountFreeFailures; i++ {
		client := fmt.Sprintf("203.0.113.%d", i)
		if err := th.guarded(context.Background(), client, "Gabe", wrong); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("guess %d: %v, want it checked", i, err)
		}
	}
	// A fresh address, the same name - in another case, which is the same
	// account to the store.
	err := th.guarded(context.Background(), "198.51.100.7", "gabe", func() error {
		t.Fatal("hashed a guess at an account under attack")
		return nil
	})
	if _, ok := IsThrottled(err); !ok {
		t.Fatalf("err = %v, want throttled", err)
	}
	// Somebody else signing in is not held up by it.
	if err := th.guarded(context.Background(), "198.51.100.7", "sam", func() error { return nil }); err != nil {
		t.Errorf("another account was held up: %v", err)
	}

	// Capped well below the address limit, because this one reaches the owner.
	for i := 0; i < 30; i++ {
		th.failFor(fmt.Sprintf("x%d", i), "gabe")
	}
	if w := th.waitFor("fresh", "gabe"); w > accountMaxWait {
		t.Errorf("wait = %v, want at most %v", w, accountMaxWait)
	}
}

// A spray of made-up names must not push the account under attack out of
// the ledger - which is what clearing it whenever it filled up would do.
func TestASprayOfNamesDoesNotForgiveTheAccountUnderAttack(t *testing.T) {
	th := newThrottle()
	for i := 0; i < accountFreeFailures+2; i++ {
		th.failFor(fmt.Sprintf("a%d", i), "gabe")
	}
	for i := 0; i < maxTracked+10; i++ {
		th.failFor("spray", fmt.Sprintf("nobody-%d", i))
	}
	if th.waitFor("fresh", "gabe") == 0 {
		t.Error("the spray flushed the account under attack")
	}
}

// PBKDF2's cost rises with the input's length once it passes SHA-256's
// 64-byte block, so a login guess sent thousands of bytes long costs several
// times what an ordinary attempt does - for one hash the throttle's
// concurrency cap was sized around. checkPassword already refuses anything
// this long when a password is *set*, so nothing genuine is ever this long,
// which is what makes it safe to refuse it before hashing at all.
func TestAnOverlongPasswordIsNeverSetOrHashed(t *testing.T) {
	m := newManager(t)
	huge := strings.Repeat("a", MaxPasswordLength+1)

	if _, err := m.Signup("gabe", huge); err == nil {
		t.Fatal("an over-length password was accepted at signup")
	}

	if _, err := m.Signup("gabe", "correct horse"); err != nil {
		t.Fatalf("signup: %v", err)
	}
	ctx := context.Background()
	_, _, _, err := m.SignIn(ctx, "1.2.3.4", "gabe", huge)
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("err = %v, want ErrInvalidCredentials", err)
	}
	// And refusing it did not count as a throttled failure against the real
	// password's ordinary attempts - it is refused before the throttle's own
	// bookkeeping for a wrong password even applies, since it never reaches
	// a real comparison.
	if _, _, _, err := m.SignIn(ctx, "1.2.3.4", "gabe", "correct horse"); err != nil {
		t.Errorf("the real password stopped working: %v", err)
	}
}

// The same refusal applies to the current-password check on a password
// change, which hashes exactly the same way.
func TestAnOverlongCurrentPasswordIsRefused(t *testing.T) {
	m := newManager(t)
	owner, err := m.Signup("gabe", "correct horse")
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	huge := strings.Repeat("a", MaxPasswordLength+1)
	err = m.ChangeOwnPassword(context.Background(), "1.2.3.4", owner, huge, "a new password here", "")
	if !errors.Is(err, ErrWrongCurrentPassword) {
		t.Fatalf("err = %v, want ErrWrongCurrentPassword", err)
	}
}
