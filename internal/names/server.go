package names

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
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

	DNS DNS

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
	registerRate        = rate{10, time.Hour}      // per client address
	addressRate         = rate{20, time.Hour}      // per install
	challengeRate       = rate{10, 24 * time.Hour} // per install
	globalChallengeRate = rate{300, 24 * time.Hour}
)

// challengeWait bounds how long a challenge PUT waits for the nameservers.
// Porkbun usually serves a new record within a minute or two.
const challengeWait = 4 * time.Minute

func (s *Server) init() {
	s.once.Do(func() {
		s.limits = newLimits()
		if s.Log == nil {
			s.Log = slog.Default()
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
	mux.HandleFunc("PUT /v1/challenge", s.authed(s.handleSetChallenge))
	mux.HandleFunc("DELETE /v1/challenge", s.authed(s.handleClearChallenge))
	return mux
}

// NameFor is the full name an id answers to.
func (s *Server) NameFor(id string) string { return id + "." + s.Label + "." + s.Zone }

func (s *Server) relative(id string) string { return id + "." + s.Label }

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if !s.limits.allow("register:"+s.clientIP(r), registerRate) {
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
	if err := s.DNS.Set(r.Context(), s.relative(id), typ, addr.String()); err != nil {
		s.Log.Error("set address", "id", id, "err", err)
		writeError(w, http.StatusBadGateway, "the DNS provider refused the change")
		return
	}
	// One address, not one of each family: a browser handed both would pick
	// either, and an install moving from v4 to v6 would otherwise leave the old
	// one answering. Best effort - a stale record of the other family is a
	// slower lookup, not a broken one.
	if err := s.DNS.Delete(r.Context(), s.relative(id), other); err != nil {
		s.Log.Warn("clear other family", "id", id, "err", err)
	}
	s.Log.Info("address set", "id", id)
	writeJSON(w, http.StatusOK, map[string]string{"name": s.NameFor(id), "ip": addr.String()})
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
		Value string `json:"value"`
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
	if !s.limits.allow("challenge:*", globalChallengeRate) {
		s.Log.Warn("global challenge limit reached")
		writeError(w, http.StatusTooManyRequests, "the service is issuing too many certificates today; try again tomorrow")
		return
	}

	name := "_acme-challenge." + s.relative(id)
	if err := s.DNS.Set(r.Context(), name, "TXT", body.Value); err != nil {
		s.Log.Error("set challenge", "id", id, "err", err)
		writeError(w, http.StatusBadGateway, "the DNS provider refused the change")
		return
	}
	if len(s.Nameservers) > 0 {
		ctx, cancel := context.WithTimeout(r.Context(), challengeWait)
		defer cancel()
		fqdn := "_acme-challenge." + s.NameFor(id) + "."
		if err := waitForTXT(ctx, s.Nameservers, fqdn, body.Value); err != nil {
			s.Log.Warn("challenge not visible in time", "id", id, "err", err)
			writeError(w, http.StatusGatewayTimeout, "the record was written but is not being served yet; try again in a few minutes")
			return
		}
	}
	s.Log.Info("challenge published", "id", id)
	writeJSON(w, http.StatusOK, map[string]bool{"visible": true})
}

func (s *Server) handleClearChallenge(w http.ResponseWriter, r *http.Request, id string) {
	if err := s.DNS.Delete(r.Context(), "_acme-challenge."+s.relative(id), "TXT"); err != nil {
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
	mu      sync.Mutex
	buckets map[string]*bucket
	now     func() time.Time
}

// maxBuckets bounds memory against a flood of distinct keys.
const maxBuckets = 100_000

func newLimits() *limits { return &limits{buckets: map[string]*bucket{}, now: time.Now} }

func (l *limits) allow(key string, r rate) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b, ok := l.buckets[key]
	if !ok || now.After(b.reset) {
		if len(l.buckets) >= maxBuckets {
			for k, old := range l.buckets {
				if now.After(old.reset) {
					delete(l.buckets, k)
				}
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
