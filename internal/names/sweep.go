package names

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Sweeping abandoned installs.
//
// Porkbun allows 2,500 records per domain and every install holds one for
// good, so without this the ceiling is reached by installs nobody runs any
// more. A running install re-announces its address twice a day, which keeps
// its record's last-seen stamp fresh (see Porkbun.Set); a record whose stamp
// is older than forgetAfter belongs to a server that has been off for months,
// and is deleted.
//
// Nothing is lost by it. The registration is not a record - it is an id and
// an HMAC, valid for ever - so the next time that server starts, its first
// announce recreates the record under the same name, and a certificate that
// expired meanwhile renews within a minute. Bookmarks keep working. The only
// thing a swept install ever notices is that its name did not resolve while
// it was switched off, when there was nothing to resolve to.

// Sweeper is a DNS provider that can list and prune what this service wrote.
// Optional: the rehearsal provider cannot, and does not need to.
type Sweeper interface {
	Sweep(ctx context.Context, label string, forgetAfter time.Duration) (SweepResult, error)
}

// SweepResult says what a sweep looked at and did.
type SweepResult struct {
	Installs   int // address records of installs, stamped or not
	Unstamped  int // of those, left alone for want of a date
	Forgotten  int // address records deleted
	Challenges int // stale challenge records deleted
}

// challengeForgetAfter is how long a challenge record may outlive its purpose.
// They are deleted within minutes of being written; one a week old was left by
// an install that crashed mid-renewal.
const challengeForgetAfter = 7 * 24 * time.Hour

// The safety valve. A sweep that would delete more than this share of every
// install at once is far likelier to be a bug - a clock gone wrong, a stamp
// format misread - than a quarter of the world switching off together, and
// the cost of being wrong is everybody's name at once. So it refuses, and says
// so, and a human looks. The floor keeps a young service, where a handful of
// test installs is a large share, able to tidy up.
const (
	maxSweepShare = 0.25
	minSweepFloor = 20
)

// sweepPause spaces deletions out. Porkbun's budget is 20 requests per two
// seconds per key, and the live service shares it.
var sweepPause = 250 * time.Millisecond

// Sweep deletes install records not seen for forgetAfter, and challenge
// records left behind for a week. It only ever considers names shaped exactly
// like the ones this service writes - <id>.<label>.<zone> and the challenge
// beneath it - so a website or mail record on the same domain is invisible to
// it however old.
func (p *Porkbun) Sweep(ctx context.Context, label string, forgetAfter time.Duration) (SweepResult, error) {
	var res SweepResult
	if forgetAfter < 30*24*time.Hour {
		// Shorter than the restamp interval, every healthy install would look
		// abandoned. Refused outright rather than trusted.
		return res, fmt.Errorf("sweep: forgetting after %s would delete healthy installs", forgetAfter)
	}
	all, err := p.call(ctx, "/dns/retrieve/"+p.Domain, nil)
	if err != nil {
		return res, err
	}

	zone := regexp.QuoteMeta(strings.ToLower(label + "." + p.Domain))
	installName := regexp.MustCompile(`^[a-z2-7]{10}\.` + zone + `$`)
	challengeName := regexp.MustCompile(`^_acme-challenge\.[a-z2-7]{10}\.` + zone + `$`)
	now := p.now()

	var forget, challenges []porkbunRecord
	for _, r := range all.Records {
		name := strings.ToLower(strings.TrimSuffix(r.Name, "."))
		switch {
		case (r.Type == "A" || r.Type == "AAAA") && installName.MatchString(name):
			res.Installs++
			seen, ok := lastSeen(r.Notes)
			if !ok {
				// No date: written before stamps existed, or by hand. Its
				// install's next announce will stamp it.
				res.Unstamped++
				continue
			}
			if now.Sub(seen) > forgetAfter {
				forget = append(forget, r)
			}
		case r.Type == "TXT" && challengeName.MatchString(name):
			if seen, ok := lastSeen(r.Notes); ok && now.Sub(seen) > challengeForgetAfter {
				challenges = append(challenges, r)
			}
		}
	}

	if limit := max(minSweepFloor, int(maxSweepShare*float64(res.Installs))); len(forget) > limit {
		return res, fmt.Errorf("sweep refused: %d of %d installs look abandoned, more than the %d a single sweep may remove - check the clock and the stamps before trusting it",
			len(forget), res.Installs, limit)
	}

	for _, r := range append(forget, challenges...) {
		if _, err := p.call(ctx, "/dns/delete/"+p.Domain+"/"+r.ID, nil); err != nil {
			return res, fmt.Errorf("sweep: delete %s: %w", r.Name, err)
		}
		if r.Type == "TXT" {
			res.Challenges++
		} else {
			res.Forgotten++
		}
		select {
		case <-ctx.Done():
			return res, ctx.Err()
		case <-time.After(sweepPause):
		}
	}
	return res, nil
}

// RunSweeper sweeps once a day until ctx ends, if the provider can. The first
// sweep waits a few minutes, so a crash loop cannot turn into a sweep loop.
func (s *Server) RunSweeper(ctx context.Context, forgetAfter time.Duration) {
	s.init()
	sw, ok := s.DNS.(Sweeper)
	if !ok {
		return
	}
	wait := 5 * time.Minute
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = 24 * time.Hour
		res, err := sw.Sweep(ctx, s.Label, forgetAfter)
		if err != nil {
			s.Log.Error("sweep", "err", err, "installs", res.Installs)
			continue
		}
		s.Log.Info("swept", "installs", res.Installs, "unstamped", res.Unstamped,
			"forgotten", res.Forgotten, "challenges", res.Challenges)
	}
}
