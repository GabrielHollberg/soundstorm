// Package portmap opens a single inbound port on a home router, so an install
// that has turned remote access on can be reached from the internet without
// anybody editing a port-forward by hand. It speaks PCP and NAT-PMP (RFC 6887
// and 6886) with no third-party dependencies, consistent with the rest of the
// project; UPnP-IGD, the widest-reaching fallback, is a later addition.
//
// It is deliberately narrow. It only ever maps this install's own TCP port, only
// at the owner's explicit request, and it drops the mapping when remote access
// is turned off. A powerful protocol used for exactly one thing.
//
// # Why the gateway is passed in
//
// The obvious design is for this package to find the default gateway itself.
// Inside the container it cannot: the process's own default route is the Docker
// bridge (172.20.0.1), not the home router, so a NAT-PMP request sent "to the
// gateway" would reach a bridge that does not answer. The installer runs on the
// host, where the real gateway is visible, and passes it in - the same division
// that already has the installer, not the container, discover the LAN address
// (see SOUNDSTORM_TLS_HOSTS). A request to that gateway leaves the container,
// is SNATed to the host's LAN address by Docker, and reaches the router as if
// the host sent it - which is exactly the address the mapping should point at.
package portmap

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"sync"
	"time"
)

// Protocol is the transport a mapping forwards. The values are the IANA
// protocol numbers, which is what PCP puts on the wire; NAT-PMP's own 1/2
// opcodes are derived from these (see natpmpOpcode).
type Protocol uint8

const (
	TCP Protocol = 6
	UDP Protocol = 17
)

// gatewayPort is where both PCP and NAT-PMP listen on the router.
const gatewayPort = 5351

// ErrNoGateway is returned when no gateway address is configured, which is the
// normal state on a network where the installer could not discover one (or on
// Docker Desktop, where there is no host gateway the container can reach). It is
// not really an error - it means "this method is unavailable, fall back to the
// manual instructions" - so callers check for it rather than logging it loudly.
var ErrNoGateway = errors.New("portmap: no gateway configured")

// Mapping is a granted port forward. ExternalIP is filled in by PCP, which
// reports the router's WAN address; NAT-PMP leaves it invalid here (its own
// external-address call is separate). nonce carries PCP's mapping nonce so the
// matching delete can name the same mapping.
type Mapping struct {
	Method       string        // "PCP" or "NAT-PMP"
	ExternalPort uint16        // the port the router actually opened
	ExternalIP   netip.Addr    // the WAN address, when the method reports it
	Lifetime     time.Duration // how long the router promised to hold it

	nonce [12]byte
}

// Map asks the router to forward externalPort to this host's internalPort,
// trying PCP first and NAT-PMP second. The returned Mapping records which
// method won and the port actually granted, which the router may choose itself.
//
// A failure is not fatal to remote access: it means the port is not open
// automatically and the owner is shown the manual instructions instead. The
// name service's reachability probe, not this call, is what confirms the world
// can actually reach the port.
func Map(ctx context.Context, gateway netip.Addr, proto Protocol, internalPort, externalPort uint16, lifetime time.Duration) (Mapping, error) {
	if !gateway.IsValid() {
		return Mapping{}, ErrNoGateway
	}
	return mapVia(ctx, netip.AddrPortFrom(gateway, gatewayPort), proto, internalPort, externalPort, lifetime)
}

// mapVia is Map against an explicit server address, so a test can point it at a
// fake gateway on an ephemeral port. The public API always uses port 5351.
func mapVia(ctx context.Context, server netip.AddrPort, proto Protocol, internalPort, externalPort uint16, lifetime time.Duration) (Mapping, error) {
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return Mapping{}, err
	}

	m, pcpErr := pcpMap(ctx, server, proto, internalPort, externalPort, lifetime, nonce)
	if pcpErr == nil {
		return m, nil
	}
	m, pmpErr := natpmpMap(ctx, server, proto, internalPort, externalPort, lifetime)
	if pmpErr == nil {
		return m, nil
	}
	return Mapping{}, fmt.Errorf("no port-mapping protocol worked (pcp: %v; nat-pmp: %v)", pcpErr, pmpErr)
}

// Unmap removes a mapping created by Map, using the same method and nonce so a
// strict PCP gateway recognises it. Best effort: a router that has already
// forgotten the mapping (a reboot, an expired lifetime) is not an error worth
// surfacing, so a delete that the gateway ignores is fine.
func Unmap(ctx context.Context, gateway netip.Addr, m Mapping, proto Protocol, internalPort uint16) error {
	if !gateway.IsValid() {
		return ErrNoGateway
	}
	return unmapVia(ctx, netip.AddrPortFrom(gateway, gatewayPort), m, proto, internalPort)
}

// unmapVia is Unmap against an explicit server address, for the same reason as
// mapVia.
func unmapVia(ctx context.Context, server netip.AddrPort, m Mapping, proto Protocol, internalPort uint16) error {
	switch m.Method {
	case "PCP":
		_, err := pcpMap(ctx, server, proto, internalPort, 0, 0, m.nonce)
		return err
	case "NAT-PMP":
		_, err := natpmpMap(ctx, server, proto, internalPort, 0, 0)
		return err
	default:
		return fmt.Errorf("portmap: cannot remove a mapping made by %q", m.Method)
	}
}

// Maintainer keeps a mapping alive for as long as remote access is on. Router
// mappings expire - a NAT-PMP lease is often two hours - so a one-shot open
// would lapse and the box would quietly fall off the internet; the Maintainer
// re-requests it well before then, and takes it down the moment remote access
// is switched off.
type Maintainer struct {
	Gateway      netip.Addr
	Proto        Protocol
	InternalPort uint16
	ExternalPort uint16
	Lifetime     time.Duration // requested lease; the router may grant less
	Log          *slog.Logger

	// testServer, when valid, overrides Gateway:5351 so a test can drive the
	// loop against a fake gateway on an ephemeral port. Zero in production.
	testServer netip.AddrPort

	opMu sync.Mutex // serialises map/unmap so the loop and EnsureNow never collide

	mu      sync.Mutex
	current *Mapping // the live mapping, or nil when none is held
}

// server is the gateway address to talk to: the test override if set, else the
// real gateway on port 5351.
func (mt *Maintainer) server() netip.AddrPort {
	if mt.testServer.IsValid() {
		return mt.testServer
	}
	return netip.AddrPortFrom(mt.Gateway, gatewayPort)
}

// Current returns the live mapping, or false when none is held. For the UI and
// for tests; the reachability probe is the authoritative "is it reachable".
func (mt *Maintainer) Current() (Mapping, bool) {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	if mt.current == nil {
		return Mapping{}, false
	}
	return *mt.current, true
}

// lease is the mapping lifetime to request, defaulted.
func (mt *Maintainer) lease() time.Duration {
	if mt.Lifetime <= 0 {
		return time.Hour
	}
	return mt.Lifetime
}

// Run holds a mapping open while enabled() is true and drops it when it turns
// false, refreshing before each lease expires. It returns when ctx is done,
// removing any live mapping on the way out so a stopped server does not leave a
// port open on the router.
func (mt *Maintainer) Run(ctx context.Context, enabled func() bool) {
	defer mt.drop(context.WithoutCancel(ctx))

	for {
		wait := mt.step(ctx, enabled)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// EnsureNow opens the mapping immediately, if it is not already held, and
// returns it. It is what the certificate loop calls the instant remote access
// is switched on, so the port is open before the name service is asked to probe
// it - without waiting for the refresh loop's own timer to come round.
func (mt *Maintainer) EnsureNow(ctx context.Context) (Mapping, error) {
	return mt.ensure(ctx)
}

// DropNow removes the mapping immediately. Called when remote access is switched
// off, so the port closes at once rather than at the next refresh tick.
func (mt *Maintainer) DropNow(ctx context.Context) {
	mt.drop(ctx)
}

// step does one iteration of the refresh loop and returns how long to wait
// before the next. Separate so a test can drive it without the timer.
func (mt *Maintainer) step(ctx context.Context, enabled func() bool) time.Duration {
	if !enabled() {
		mt.drop(ctx)
		return mt.lease()
	}
	m, err := mt.ensure(ctx)
	if err != nil {
		// Retry sooner than a full lease - a router that was briefly busy, or a
		// gateway that has only just come up.
		return retryWait(mt.lease())
	}
	// Refresh at half the granted lease, so a missed refresh has a whole second
	// half to recover in before the mapping actually lapses.
	granted := m.Lifetime
	if granted <= 0 {
		granted = mt.lease()
	}
	return granted / 2
}

// ensure opens (or re-opens, refreshing the lease) the mapping and records it.
// The op lock serialises it against the refresh loop and against drop, so a
// toggle-driven EnsureNow and a timer-driven step never map at once.
func (mt *Maintainer) ensure(ctx context.Context) (Mapping, error) {
	mt.opMu.Lock()
	defer mt.opMu.Unlock()

	server := mt.server()
	if !server.IsValid() {
		mt.mu.Lock()
		mt.current = nil
		mt.mu.Unlock()
		return Mapping{}, ErrNoGateway
	}

	opCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	m, err := mapVia(opCtx, server, mt.Proto, mt.InternalPort, mt.ExternalPort, mt.lease())
	if err != nil {
		mt.mu.Lock()
		mt.current = nil
		mt.mu.Unlock()
		if mt.Log != nil {
			mt.Log.Warn("could not open the port automatically; manual forwarding may be needed",
				"port", mt.ExternalPort, "err", err)
		}
		return Mapping{}, err
	}

	mt.mu.Lock()
	first := mt.current == nil
	mt.current = &m
	mt.mu.Unlock()
	if first && mt.Log != nil {
		mt.Log.Info("opened the port on the router", "method", m.Method,
			"externalPort", m.ExternalPort, "lease", m.Lifetime.Round(time.Second))
	}
	return m, nil
}

// drop removes the live mapping if there is one. Safe to call when none is held.
func (mt *Maintainer) drop(ctx context.Context) {
	mt.opMu.Lock()
	defer mt.opMu.Unlock()

	mt.mu.Lock()
	m := mt.current
	mt.current = nil
	mt.mu.Unlock()
	if m == nil {
		return
	}
	server := mt.server()
	if !server.IsValid() {
		return
	}
	dropCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := unmapVia(dropCtx, server, *m, mt.Proto, mt.InternalPort); err != nil && mt.Log != nil {
		mt.Log.Warn("could not remove the port mapping", "err", err)
	} else if mt.Log != nil {
		mt.Log.Info("removed the port mapping", "externalPort", m.ExternalPort)
	}
}

// retryWait bounds the after-failure retry to something short but not a busy
// loop: a tenth of a lease, floored at 30s and capped at five minutes.
func retryWait(lifetime time.Duration) time.Duration {
	w := lifetime / 10
	if w < 30*time.Second {
		w = 30 * time.Second
	}
	if w > 5*time.Minute {
		w = 5 * time.Minute
	}
	return w
}
