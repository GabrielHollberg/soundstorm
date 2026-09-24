package names

import (
	"context"
	cryptorand "crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Server is the name service's HTTP API.
//
//	POST   /v1/register   -> 201 Registration
//	PUT    /v1/address    {"ip": "192.168.1.50"}   point the name at an address
//	PUT    /v1/challenge  {"value": "..."}         publish an ACME DNS challenge
//	DELETE /v1/challenge                           and take it down again
//
// Everything but register needs the registration's credential.
type Server struct {
	// Secret keys every install's token. At least 32 random bytes; changing
	// it invalidates every registration there is.
	Secret []byte

	// Zone is the domain at the provider, and Label the one level under it
	// that every install sits beneath: an install is <id>.<Label>.<Zone>.
	// A label of its own keeps install names out of the way of anything else
	// the domain is used for.
	Zone, Label string

	// PublicLabel is the level for remote-access names, which point at a home's
	// public address rather than its LAN one: an install is
	// <id>.<PublicLabel>.<Zone>. Separate from Label so a device on the LAN and
	// a device away from home resolve different records - many routers cannot
	// loop a LAN client back through the public IP. Defaults to "net".
	PublicLabel string

	DNS DNS

	// probe verifies an install is reachable at a public address before a name
	// is pointed there. Nil uses the real HTTP prober; tests set their own.
	probe func(ctx context.Context, addr netip.Addr, port int, id string) error

	// Nameservers are asked whether a challenge is visible before its PUT
	// returns. Empty skips the wait, which is only right for a test server
	// that answers the moment it is told.
	Nameservers []string

	// ClientIPHeader names a header the hosting proxy sets to the real client
	// address - Railway's proxy, say - used for rate limiting. Empty uses the
	// connection's address. Never set it without such a proxy in front: a
	// client can send any header it likes.
	ClientIPHeader string

	Log *slog.Logger

	once   sync.Once
	limits *limits
}

// Rates. Each is a count per window, and none of them is anywhere near what
// one honest install uses: it registers once, re-announces its address when
// it changes, and asks for a challenge every couple of months.
//
// The challenge limits are the ones that matter. Every certificate issued
// under the zone counts against Let's Encrypt's per-domain weekly allowance,
// and a challenge is how one gets issued, so a global cap is what stops one
// abuser spending everybody's budget. The durable fix is putting the zone on
// the Public Suffix List, after which each install is its own domain to Let's
// Encrypt.
var (
	registerRate        = rate{10, time.Hour}      // per client network
	addressRate         = rate{20, time.Hour}      // per install
	challengeRate       = rate{10, 24 * time.Hour} // per install
	challengeNetRate    = rate{20, 24 * time.Hour} // per client network
	globalChallengeRate = rate{300, 24 * time.Hour}
	publicRate          = rate{20, time.Hour} // per install; each triggers an outbound probe
	clearRate           = rate{30, time.Hour} // per install; each costs registrar calls
)

// The per-network challenge limit is what stops the global one being a way to
// switch renewals off. Registration is free, so a per-install limit alone let a
// few addresses mint enough installs to spend the whole day's global budget, and
// every real install's renewal then waited on tomorrow. Bounded per network as
// well, spending it takes many networks rather than a few addresses. (The Public
// Suffix List remains the durable fix: once each install is its own domain to
// Let's Encrypt, there is no shared budget to protect.)

// reachTimeout bounds the outbound reachability probe: a short dial and read,
// so a slow or black-holed address cannot tie the handler up.
const reachTimeout = 6 * time.Second

// challengeWait bounds how long a challenge PUT waits for the nameservers.
// Porkbun usually serves a new record within a minute or two.
const challengeWait = 4 * time.Minute

func (s *Server) init() {
	s.once.Do(func() {
		s.limits = newLimits()
		if s.Log == nil {
			s.Log = slog.Default()
		}
		if s.PublicLabel == "" {
			s.PublicLabel = "net"
		}
		if s.probe == nil {
			s.probe = s.reachable
		}
	})
}

// Handler returns the API.
func (s *Server) Handler() http.Handler {
	s.init()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	// Which address the rate limits see for the caller, and the two headers
	// it could have come from. Only ever the caller's own request reflected
	// back, so nothing leaks; it exists because whether a host's proxy sets a
	// header honestly cannot be read off its documentation - Railway's
	// contradicted itself - only measured.
	mux.HandleFunc("GET /v1/whoami", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"clientIP":        s.clientIP(r),
			"x-real-ip":       r.Header.Get("X-Real-Ip"),
			"x-forwarded-for": r.Header.Get("X-Forwarded-For"),
		})
	})
	mux.HandleFunc("POST /v1/register", s.handleRegister)
	mux.HandleFunc("PUT /v1/address", s.authed(s.handleAddress))
	mux.HandleFunc("PUT /v1/public", s.authed(s.handlePublic))
	mux.HandleFunc("DELETE /v1/public", s.authed(s.handleClearPublic))
	mux.HandleFunc("PUT /v1/challenge", s.authed(s.handleSetChallenge))
	mux.HandleFunc("DELETE /v1/challenge", s.authed(s.handleClearChallenge))
	return mux
}

// NameFor is the full name an id answers to.
func (s *Server) NameFor(id string) string { return id + "." + s.Label + "." + s.Zone }

func (s *Server) relative(id string) string { return id + "." + s.Label }

// PublicNameFor is the full remote-access name an id answers to.
func (s *Server) PublicNameFor(id string) string { return id + "." + s.PublicLabel + "." + s.Zone }

func (s *Server) publicRelative(id string) string { return id + "." + s.PublicLabel }

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if !s.limits.allow("register:"+s.clientNet(r), registerRate) {
		writeError(w, http.StatusTooManyRequests, "too many registrations from this address; try again later")
		return
	}
	id, err := newID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not make an id")
		return
	}
	reg := Registration{ID: id, Name: s.NameFor(id), Token: tokenFor(s.Secret, id)}
	s.Log.Info("registered", "id", id)
	writeJSON(w, http.StatusCreated, reg)
}

func (s *Server) handleAddress(w http.ResponseWriter, r *http.Request, id string) {
	var body struct {
		IP string `json:"ip"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	addr, err := checkAddress(body.IP)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if !s.limits.allow("address:"+id, addressRate) {
		writeError(w, http.StatusTooManyRequests, "too many address changes; try again later")
		return
	}
	typ, other := "A", "AAAA"
	if addr.Is6() {
		typ, other = "AAAA", "A"
	}
	changed, err := s.DNS.Set(r.Context(), s.relative(id), typ, addr.String())
	if err != nil {
		s.Log.Error("set address", "id", id, "err", err)
		writeError(w, http.StatusBadGateway, "the DNS provider refused the change")
		return
	}
	// One address, not one of each family: a browser handed both would pick
	// either, and an install moving from v4 to v6 would otherwise leave the old
	// one answering. Only when something changed: an install re-announces the
	// same address on every start, and a delete that finds nothing would
	// double the provider calls of the common case - Porkbun meters calls per
	// key. Best effort; a stale record of the other family is a slower lookup,
	// not a broken one.
	if changed {
		if err := s.DNS.Delete(r.Context(), s.relative(id), other); err != nil {
			s.Log.Warn("clear other family", "id", id, "err", err)
		}
	}
	s.Log.Info("address set", "id", id)
	writeJSON(w, http.StatusOK, map[string]string{"name": s.NameFor(id), "ip": addr.String()})
}

// handlePublic points an install's remote-access name at its public address,
// for reaching the server from outside the house.
//
// The address is not taken from the request body - it is the request's own
// source address. That is the whole SSRF defence: the service can only ever be
// asked to probe and name whoever is calling, never a victim, a metadata
// endpoint, or the host's own network. The install supplies only the port it
// serves, which is only ever combined with that source address, so it cannot
// be used to reach a third party either. The address must additionally be a
// public one, or remote access is not available on this network (that is what
// Tailscale is for), which also means the probe never touches an internal
// range even if a request somehow arrives from one.
func (s *Server) handlePublic(w http.ResponseWriter, r *http.Request, id string) {
	var body struct {
		Port int `json:"port"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if body.Port < 1 || body.Port > 65535 {
		writeError(w, http.StatusUnprocessableEntity, "port must be between 1 and 65535")
		return
	}
	addr, err := publicAddress(s.clientIP(r))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity,
			"this network has no public address the internet can reach; reach it from away with Tailscale instead")
		return
	}
	if !s.limits.allow("public:"+id, publicRate) {
		writeError(w, http.StatusTooManyRequests, "too many remote-access checks; try again later")
		return
	}

	// Prove the install is actually reachable at that address and port, and is
	// this install. This is what stops a dead or wrong record being published,
	// and gives the caller something it can act on when a port is not open.
	ctx, cancel := context.WithTimeout(r.Context(), reachTimeout)
	defer cancel()
	if err := s.probe(ctx, addr, body.Port, id); err != nil {
		s.Log.Info("remote reachability failed", "id", id, "err", err)
		writeError(w, http.StatusFailedDependency,
			fmt.Sprintf("could not reach this server from the internet at %s port %d; open that port on the router and try again", addr, body.Port))
		return
	}

	typ := "A"
	if addr.Is6() {
		typ = "AAAA"
	}
	// The public name is deliberately dual-stack: unlike the LAN name, it keeps
	// whichever of A and AAAA it already had. An install with a forwarded IPv4
	// port and a directly reachable global IPv6 publishes both, from two calls -
	// one reaching the service over each family - and a visitor connects on the
	// family it has. A record that later goes dark is not a problem either: a
	// browser handed both tries both (Happy Eyeballs) and falls back.
	if _, err := s.DNS.Set(ctx, s.publicRelative(id), typ, addr.String()); err != nil {
		s.Log.Error("set public address", "id", id, "err", err)
		writeError(w, http.StatusBadGateway, "the DNS provider refused the change")
		return
	}
	s.Log.Info("public address set", "id", id, "type", typ)
	writeJSON(w, http.StatusOK, map[string]string{"name": s.PublicNameFor(id), "ip": addr.String()})
}

// handleClearPublic removes an install's remote-access records, for when the
// owner turns remote access off.
func (s *Server) handleClearPublic(w http.ResponseWriter, r *http.Request, id string) {
	// Limited like every other write: each call spends registrar requests from a
	// budget every install shares, so an unlimited one is a way to exhaust it.
	if !s.limits.allow("clear:"+id, clearRate) {
		writeError(w, http.StatusTooManyRequests, "too many changes for this install; try again later")
		return
	}
	for _, typ := range []string{"A", "AAAA"} {
		if err := s.DNS.Delete(r.Context(), s.publicRelative(id), typ); err != nil {
			s.Log.Warn("clear public address", "id", id, "type", typ, "err", err)
		}
	}
	s.Log.Info("public address cleared", "id", id)
	w.WriteHeader(http.StatusNoContent)
}

// reachable is the default probe: fetch the install's reachability endpoint at
// its own address and port over plain HTTP, and check it answers with the
// challenge only this install can compute.
func (s *Server) reachable(ctx context.Context, addr netip.Addr, port int, id string) error {
	nonce, err := newNonce()
	if err != nil {
		return err
	}
	want := Reachability(tokenFor(s.Secret, id), nonce)

	u := "http://" + net.JoinHostPort(addr.String(), strconv.Itoa(port)) + ReachablePath + "?nonce=" + nonce
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	client := &http.Client{
		Timeout: reachTimeout,
		// Never follow a redirect: the address is a validated public IP, and a
		// redirect is the one way the thing answering could send this probe
		// somewhere else. Returning the 3xx as-is makes it a failed check.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("could not connect: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("answered %d, not 200", resp.StatusCode)
	}
	got, err := io.ReadAll(io.LimitReader(resp.Body, 512))
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(string(got))), []byte(want)) != 1 {
		return fmt.Errorf("the server there is not this install")
	}
	return nil
}

// newNonce is a fresh random challenge nonce.
func newNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := cryptorand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// validChallenge accepts exactly what an ACME DNS-01 value is - the unpadded
// base64url of a SHA-256, 43 characters - and nothing else, so this can only
// ever be used to publish an ACME challenge.
func validChallenge(v string) bool {
	if len(v) != 43 {
		return false
	}
	for _, r := range v {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func (s *Server) handleSetChallenge(w http.ResponseWriter, r *http.Request, id string) {
	var body struct {
		Value  string `json:"value"`
		Public bool   `json:"public"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if !validChallenge(body.Value) {
		writeError(w, http.StatusUnprocessableEntity, "that is not an ACME DNS challenge value")
		return
	}
	if !s.limits.allow("challenge:"+id, challengeRate) {
		writeError(w, http.StatusTooManyRequests, "too many certificate requests for this install today")
		return
	}
	if !s.limits.allow("challenge-net:"+s.clientNet(r), challengeNetRate) {
		writeError(w, http.StatusTooManyRequests, "too many certificate requests from this network today")
		return
	}
	if !s.limits.allow("challenge:*", globalChallengeRate) {
		s.Log.Warn("global challenge limit reached")
		writeError(w, http.StatusTooManyRequests, "the service is issuing too many certificates today; try again tomorrow")
		return
	}

	// Which of the install's two names this challenge is for. The id is fixed
	// by the credential, so public only picks the label - it cannot name
	// another install's record.
	rel, full := s.relative(id), s.NameFor(id)
	if body.Public {
		rel, full = s.publicRelative(id), s.PublicNameFor(id)
	}

	if _, err := s.DNS.Set(r.Context(), "_acme-challenge."+rel, "TXT", body.Value); err != nil {
		s.Log.Error("set challenge", "id", id, "err", err)
		writeError(w, http.StatusBadGateway, "the DNS provider refused the change")
		return
	}
	if len(s.Nameservers) > 0 {
		ctx, cancel := context.WithTimeout(r.Context(), challengeWait)
		defer cancel()
		fqdn := "_acme-challenge." + full + "."
		if err := waitForTXT(ctx, s.Nameservers, fqdn, body.Value); err != nil {
			s.Log.Warn("challenge not visible in time", "id", id, "err", err)
			writeError(w, http.StatusGatewayTimeout, "the record was written but is not being served yet; try again in a few minutes")
			return
		}
	}
	s.Log.Info("challenge published", "id", id, "public", body.Public)
	writeJSON(w, http.StatusOK, map[string]bool{"visible": true})
}

func (s *Server) handleClearChallenge(w http.ResponseWriter, r *http.Request, id string) {
	if !s.limits.allow("clear:"+id, clearRate) {
		writeError(w, http.StatusTooManyRequests, "too many changes for this install; try again later")
		return
	}
	rel := s.relative(id)
	if r.URL.Query().Get("public") != "" {
		rel = s.publicRelative(id)
	}
	if err := s.DNS.Delete(r.Context(), "_acme-challenge."+rel, "TXT"); err != nil {
		s.Log.Warn("clear challenge", "id", id, "err", err)
		writeError(w, http.StatusBadGateway, "the DNS provider refused the change")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) authed(next func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := parseCredential(s.Secret, r.Header.Get("Authorization"))
		if err != nil {
			writeError(w, http.StatusUnauthorized, err.Error())
			return
		}
		next(w, r, id)
	}
}

func (s *Server) clientIP(r *http.Request) string {
	if s.ClientIPHeader != "" {
		if v := strings.TrimSpace(r.Header.Get(s.ClientIPHeader)); v != "" {
			// X-Forwarded-For style lists: the proxy appends, so the last
			// entry is the one it saw, and the only one it vouches for.
			if i := strings.LastIndex(v, ","); i >= 0 {
				v = strings.TrimSpace(v[i+1:])
			}
			return v
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// clientNet is the caller's address for rate limiting: an IPv4 address as it is,
// and an IPv6 address by its /64. A single home or server is handed a whole /64,
// so keying IPv6 on the full address gave anybody reaching the service over v6
// a fresh limit for every address in it - effectively none at all.
func (s *Server) clientNet(r *http.Request) string {
	ip := s.clientIP(r)
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	addr = addr.Unmap()
	if addr.Is6() {
		if p, err := addr.Prefix(64); err == nil {
			return p.String()
		}
	}
	return addr.String()
}

// --- limits --------------------------------------------------------------------

type rate struct {
	n      int
	window time.Duration
}

type bucket struct {
	count int
	reset time.Time
}

// limits is a fixed-window counter per key, in memory. It forgets on restart,
// which errs towards letting people in; the global challenge cap is the only
// limit whose loss costs anything, and a restart cannot be triggered from
// outside.
type limits struct {
	mu        sync.Mutex
	buckets   map[string]*bucket
	now       func() time.Time
	nextPrune time.Time
}

// maxBuckets bounds memory against a flood of distinct keys.
const maxBuckets = 100_000

// pruneEvery bounds how often a full table is scanned for expired entries, so a
// flood of new keys against a full table cannot make every request walk it.
const pruneEvery = time.Second

func newLimits() *limits { return &limits{buckets: map[string]*bucket{}, now: time.Now} }

func (l *limits) allow(key string, r rate) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b, ok := l.buckets[key]
	if !ok || now.After(b.reset) {
		if !ok && len(l.buckets) >= maxBuckets {
			if now.After(l.nextPrune) {
				for k, old := range l.buckets {
					if now.After(old.reset) {
						delete(l.buckets, k)
					}
				}
				l.nextPrune = now.Add(pruneEvery)
			}
			// Still full: refuse a new key rather than grow without bound. Keys
			// already in the table - the global challenge count, an install
			// that is already counted - carry on as before.
			if len(l.buckets) >= maxBuckets {
				return false
			}
		}
		b = &bucket{reset: now.Add(r.window)}
		l.buckets[key] = b
	}
	if b.count >= r.n {
		return false
	}
	b.count++
	return true
}

// --- JSON ----------------------------------------------------------------------

func readJSON(w http.ResponseWriter, r *http.Request, into any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	if err := dec.Decode(into); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "request too large")
		} else {
			writeError(w, http.StatusBadRequest, "expected a JSON body")
		}
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
