package auth

import (
	"context"
	"errors"
	"path/filepath"
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
	th.succeed("a")
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
		if err := th.guarded(context.Background(), "a", func() error { return boom }); !errors.Is(err, boom) {
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
			_ = th.guarded(context.Background(), "a", func() error {
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
