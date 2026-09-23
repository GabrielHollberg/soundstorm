package names

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// DNS is the one thing this service needs from a DNS provider: make a name
// hold exactly one value of a type, or none. Names are relative to the zone -
// "k3x9m2p7qa.home", not the full name - because that is how every provider's
// API addresses a record.
//
// Set reports whether it changed anything, so a caller can skip follow-up
// work when the answer was already right - which, for an install announcing
// the address it had yesterday, is nearly always.
type DNS interface {
	Set(ctx context.Context, name, typ, value string) (changed bool, err error)
	Delete(ctx context.Context, name, typ string) error
}

// --- Porkbun -----------------------------------------------------------------

// Porkbun drives Porkbun's DNS API, where soundstorm.dev is registered.
//
// Its API key is account-wide: whoever holds it can change every record of
// every domain that has API access switched on. So switch it on for the one
// domain this serves and nothing else - and the stronger form of the same
// advice is a domain used for nothing but install names, so that a breach of
// this service could never touch a website or mail.
type Porkbun struct {
	Domain       string // the zone, e.g. "soundstorm.dev"
	APIKey       string
	SecretAPIKey string

	// Base is the API root. Porkbun moved it once already, from porkbun.com
	// to api.porkbun.com, so it is a setting rather than a constant.
	Base string
	HTTP *http.Client

	// Now is the clock the last-seen stamps are written with. Nil is
	// time.Now; tests set it.
	Now func() time.Time
}

// porkbunTTL is Porkbun's minimum. Nothing here benefits from a longer one:
// an address change should land as soon as it can.
const porkbunTTL = "600"

type porkbunRecord struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content string `json:"content"`
	Notes   string `json:"notes"`
}

// Every record this service writes carries the date its install was last
// heard from, in Porkbun's notes field - stored with the record, never served
// in DNS. It is the only state the service has, and it lives where the
// records do, so nothing else has to be kept anywhere.
//
// It is what lets abandoned installs be swept: Porkbun allows 2,500 records
// per domain, and without a date there is no telling a server switched off
// for good from one that has not restarted since last week.
const seenPrefix = "soundstorm last-seen "

// restampAfter is how stale a stamp may get before re-announcing the same
// address rewrites it. A running install announces twice a day; rewriting
// every time would turn a free lookup into a paid write, and a stamp a month
// old is plenty precise for a sweep measured in months.
const restampAfter = 30 * 24 * time.Hour

func (p *Porkbun) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *Porkbun) stamp() string { return seenPrefix + p.now().UTC().Format("2006-01-02") }

// lastSeen reads a stamp back. ok is false for a record with none - one
// written before stamps existed, or by hand - which a sweep leaves alone.
func lastSeen(notes string) (time.Time, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(notes), seenPrefix)
	if !ok {
		return time.Time{}, false
	}
	t, err := time.Parse("2006-01-02", rest)
	return t, err == nil
}

type porkbunReply struct {
	Status  string          `json:"status"`
	Message string          `json:"message"`
	Records []porkbunRecord `json:"records"`
}

func (p *Porkbun) call(ctx context.Context, path string, fields map[string]string) (porkbunReply, error) {
	body := map[string]string{"apikey": p.APIKey, "secretapikey": p.SecretAPIKey}
	for k, v := range fields {
		body[k] = v
	}
	raw, _ := json.Marshal(body)

	base := strings.TrimRight(p.Base, "/")
	if base == "" {
		base = "https://api.porkbun.com/api/json/v3"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(raw))
	if err != nil {
		return porkbunReply{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := p.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return porkbunReply{}, fmt.Errorf("porkbun: %w", err)
	}
	defer resp.Body.Close()

	var reply porkbunReply
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err := json.Unmarshal(data, &reply); err != nil {
		return porkbunReply{}, fmt.Errorf("porkbun %s: status %d, unreadable reply", path, resp.StatusCode)
	}
	if reply.Status != "SUCCESS" {
		// The message never contains the key, but it can say which domain
		// lacks API access - which is exactly what somebody setting this up
		// needs to read.
		return reply, fmt.Errorf("porkbun %s: %s", path, reply.Message)
	}
	return reply, nil
}

// Set makes name hold exactly value, stamped as seen today. Porkbun's "edit
// by name and type" changes records that exist and does nothing for ones that
// do not, so it looks first and creates when there is nothing to edit - and
// does nothing at all when the value is already right and the stamp recent,
// which is the common case for an install re-announcing an address that has
// not changed.
//
// changed reports whether the value changed; refreshing a stale stamp on the
// same value is a write, but not a change.
func (p *Porkbun) Set(ctx context.Context, name, typ, value string) (bool, error) {
	path := "/" + p.Domain + "/" + typ + "/" + name
	got, err := p.call(ctx, "/dns/retrieveByNameType"+path, nil)
	if err != nil {
		return false, err
	}
	if len(got.Records) == 1 && got.Records[0].Content == value {
		if seen, ok := lastSeen(got.Records[0].Notes); ok && p.now().Sub(seen) < restampAfter {
			return false, nil
		}
		_, err = p.call(ctx, "/dns/editByNameType"+path, map[string]string{
			"content": value, "ttl": porkbunTTL, "notes": p.stamp(),
		})
		return false, err
	}
	if len(got.Records) == 0 {
		_, err = p.call(ctx, "/dns/create/"+p.Domain, map[string]string{
			"name": name, "type": typ, "content": value, "ttl": porkbunTTL, "notes": p.stamp(),
		})
	} else {
		_, err = p.call(ctx, "/dns/editByNameType"+path, map[string]string{
			"content": value, "ttl": porkbunTTL, "notes": p.stamp(),
		})
	}
	return err == nil, err
}

// Delete removes every record of a type at a name.
func (p *Porkbun) Delete(ctx context.Context, name, typ string) error {
	_, err := p.call(ctx, "/dns/deleteByNameType/"+p.Domain+"/"+typ+"/"+name, nil)
	return err
}

// --- pebble-challtestsrv -------------------------------------------------------

// ChallTestSrv drives pebble-challtestsrv, the mock DNS server that ships
// with Pebble, Let's Encrypt's own test ACME server. It exists so the whole
// chain - register, challenge, certificate - can be exercised on one machine
// with no account anywhere. Never used in production.
type ChallTestSrv struct {
	Domain string // appended to relative names, as a real zone would be
	Base   string // its management API, e.g. http://challtestsrv:8055
	HTTP   *http.Client
}

func (c *ChallTestSrv) post(ctx context.Context, path string, body any) error {
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.Base, "/")+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("challtestsrv %s: status %d", path, resp.StatusCode)
	}
	return nil
}

func (c *ChallTestSrv) fqdn(name string) string { return name + "." + c.Domain + "." }

// Set always reports a change: challtestsrv cannot be asked what it holds.
func (c *ChallTestSrv) Set(ctx context.Context, name, typ, value string) (bool, error) {
	host := c.fqdn(name)
	var err error
	switch typ {
	case "TXT":
		if err = c.post(ctx, "/clear-txt", map[string]string{"host": host}); err == nil {
			err = c.post(ctx, "/set-txt", map[string]string{"host": host, "value": value})
		}
	case "A":
		err = c.post(ctx, "/add-a", map[string]any{"host": host, "addresses": []string{value}})
	case "AAAA":
		err = c.post(ctx, "/add-aaaa", map[string]any{"host": host, "addresses": []string{value}})
	default:
		err = fmt.Errorf("challtestsrv: no support for %s records", typ)
	}
	return err == nil, err
}

func (c *ChallTestSrv) Delete(ctx context.Context, name, typ string) error {
	host := c.fqdn(name)
	switch typ {
	case "TXT":
		return c.post(ctx, "/clear-txt", map[string]string{"host": host})
	case "A":
		return c.post(ctx, "/clear-a", map[string]string{"host": host})
	case "AAAA":
		return c.post(ctx, "/clear-aaaa", map[string]string{"host": host})
	}
	return fmt.Errorf("challtestsrv: no support for %s records", typ)
}

// --- visibility ----------------------------------------------------------------

// PorkbunNameservers are where soundstorm.dev's records are served from.
// Asking them directly is what "is it visible yet" means: Let's Encrypt asks
// the authoritative servers too, not a cache.
var PorkbunNameservers = []string{
	"curitiba.ns.porkbun.com:53",
	"fortaleza.ns.porkbun.com:53",
	"maceio.ns.porkbun.com:53",
	"salvador.ns.porkbun.com:53",
}

// waitForTXT polls the given nameservers until every one of them serves value
// at fqdn, or ctx ends.
//
// This is the step that decides whether a challenge passes. A provider's API
// answering "done" means the record is in its database, not that its
// nameservers are serving it yet, and asking Let's Encrypt to look before they
// are is a failed validation - which counts against a rate limit. All of them,
// not any one, because Let's Encrypt validates from several places at once and
// cannot be told which server to ask.
func waitForTXT(ctx context.Context, servers []string, fqdn, value string) error {
	pending := append([]string(nil), servers...)
	for {
		var still []string
		for _, server := range pending {
			if !serves(ctx, server, fqdn, value) {
				still = append(still, server)
			}
		}
		if len(still) == 0 {
			return nil
		}
		pending = still
		select {
		case <-ctx.Done():
			return fmt.Errorf("the challenge record is not visible on %s yet: %w", strings.Join(pending, ", "), ctx.Err())
		case <-time.After(3 * time.Second):
		}
	}
}

func serves(ctx context.Context, server, fqdn, value string) bool {
	r := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, server)
		},
	}
	lookup, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	txts, err := r.LookupTXT(lookup, fqdn)
	if err != nil {
		return false
	}
	for _, t := range txts {
		if t == value {
			return true
		}
	}
	return false
}
