# Remote access — design

Reaching a SoundStorm install from the internet without Tailscale. This is the
design; it is built in stages, each shipping usable value on its own.

## Goal and non-goals

**Goal.** A household can reach its own server from outside the house at a
`soundstorm.dev` name with a browser-trusted certificate, with as little router
fiddling as the network allows.

**Non-goals, and why.**

- **No media relay, ever.** A relay's cost grows with every film watched; the
  name service exists precisely because DNS and a challenge every couple of
  months do not. A tunnel that carries video is out. That is what Tailscale is
  for, and Tailscale stays the answer for anyone behind carrier-grade NAT or
  who cannot forward a port — remote access here is for a home with a
  forwardable port or public IPv6.
- **No new central data path.** The name service keeps only DNS records and an
  HMAC; remote access adds a *reachability probe* and nothing that a stream
  ever flows through.

## The shape

A home server is behind NAT. Two independent problems:

1. **Ingress** — packets from the internet must reach the box.
2. **A public name + trusted certificate** for the home's public address.

Most of #2 already exists and is indifferent to public vs private:

- **Public-IP discovery is free.** The name service already sees each install's
  public IP as the source address of its calls (`/v1/whoami`, `clientIP`).
- **The certificate is DNS-01**, which proves control of the *name* and does not
  care where the name points — so issuance itself is unchanged for a public
  address. One open point: the remote name is separate from the LAN name (see
  below), so the install needs a certificate for it too. Rather than a second
  certificate — which would double each install's draw on the shared
  soundstorm.dev Let's Encrypt quota, the very pressure point the review
  flagged — the plan is **one certificate carrying both names as SANs**: one
  order, one renewal, two DNS-01 challenge records (one per name). This needs
  the ACME client to handle two authorizations in an order, and the name
  service's challenge endpoint to publish under either label.
- **Dynamic DNS already exists** — the install re-announces on a timer, so a
  changing home IP is a solved problem.

The one thing deliberately in the way is `names.checkAddress`, which refuses a
public address today ("a real certificate on a public address is a phishing
kit"). Remote access lifts that **only** behind the gate below.

## The gate: an SSRF-safe reachability challenge

The name service runs on the public internet. Making it perform an *outbound*
connection is the sharp edge, so the rules are strict:

- **It only ever probes the install's own source IP.** The install does not
  supply a target address to probe; the service uses the source IP of the
  request. So the service can never be pointed at a victim, a metadata endpoint
  (169.254.169.254), or Railway's own internals — the target is, by
  construction, whoever is calling.
- **That source IP must be a public address** (reject private, loopback,
  link-local, CGNAT, multicast, unspecified). If it is not public, remote
  access is not available on this network — use Tailscale.
- **The probe proves it is a real SoundStorm install, not just "something
  answers."** The service fetches a challenge endpoint at `source-IP:port` and
  expects `HMAC(install-token, nonce)`. Only the install holding its own token
  can produce it, so a co-located stranger cannot hijack the name.
- **The port comes from the install** but is only ever combined with its own
  source IP, so it cannot be used to scan or attack a third party.
- **Bounded and limited** — short dial and read timeouts, capped concurrency,
  and its own rate limit, like every other name-service operation.

Only after the probe passes does the service publish the public `A`/`AAAA`
record and permit the certificate. The probe doubles as the best possible UX
signal: "your port is not open yet" is actionable, where a silently dead record
is not.

**Honest residual.** An attacker can still point *their own* random-id name at
*their own* public server and get a valid certificate — but that is true of any
domain plus Let's Encrypt, and `<id>.…soundstorm.dev` is not brandable, so the
phishing value is low. Named here rather than hidden.

## Two names, not one (the hairpin trap)

Keep the existing private `<id>.home.soundstorm.dev` for the LAN, and add a
*separate* public name for remote (label TBD, e.g. `<id>.net.soundstorm.dev`).
Many routers cannot hairpin — loop a LAN client back in through the public IP —
so a single public name would break home access on those routers. The client
already probes reachability before switching to `secureName`, so it can prefer
whichever name it can actually reach.

## Ingress, in stages

### Stage 1 — public naming, manual port-forward

The foundation everything else builds on, and shippable alone.

- `names`: a public-address mode gated by the reachability challenge above;
  publish the public record; keep re-announcing (dynamic DNS).
- `servetls/auto`: an opt-in "remote access" path that announces the public
  address, serves the challenge endpoint, and obtains the cert for the public
  name.
- UI: an explicit, plainly-worded opt-in toggle with a warning that this puts
  the server on the internet; show reachability status; print per-router
  port-forward guidance.
- Installer/compose: nothing required beyond an env flag; the toggle drives it.

### Stage 2 — automatic IPv4 port-forward

Remove the manual step where the router allows it.

- A small NAT traversal client: **NAT-PMP** and **PCP** first (simple UDP to the
  default gateway), then **UPnP-IGD** (SSDP discovery + SOAP) as the widest-
  reaching fallback. Zero third-party dependencies, consistent with the rest of
  the project.
- Opt-in and reversible: the mapping is refreshed on a timer and dropped when
  remote access is turned off. UPnP is powerful, so it is only ever used to open
  *this* port, at the user's explicit request.
- If no method works, fall back to Stage 1's manual instructions with the exact
  port to forward.

**Built: NAT-PMP, PCP and UPnP-IGD** (`internal/portmap`), all pure stdlib and
tested against fakes. The order is PCP, then NAT-PMP, then UPnP. PCP is first
because where it works it is the best protocol, but it carries a client-address
field a strict gateway checks against the packet source — and behind Docker's
own NAT that source is SNATed from `172.20.x` to the host's LAN address, so a
strict router answers ADDRESS_MISMATCH. NAT-PMP has no such field and maps the
right box, so it is the fallback rather than the other way round. UPnP is the
widest-reaching last resort, for routers that speak neither. The name service's
reachability probe, not any protocol's own reply, is the final word on whether
the port actually opened.

UPnP needs three things the other two do not, and each shaped the design. Its
**SSDP discovery is multicast**, which does not cross the Docker bridge, so the
router's device-description URL is discovered on the host by the installer and
passed in as `SOUNDSTORM_UPNP_URL` — like the gateway. The **SOAP** that uses
that URL afterwards is ordinary unicast HTTP, which does reach the router from
the container through Docker's NAT. And AddPortMapping needs the **LAN address
to forward to** (the host's, not the container's `172.20.x`), which is the LAN
address already in `SOUNDSTORM_TLS_HOSTS`. On a host-network or native run the
process can do SSDP itself, so the configured URL is an override, not a
requirement. A router that only grants permanent leases (UPnP error 725) is
retried with a zero lease; the mapping is dropped on teardown regardless.

**The gateway is discovered by the installer, not the container** — the same
division that already has the installer, not the container, find the LAN
address. Inside the container the process's own default route is the Docker
bridge (`172.20.0.1`), not the home router, so a request "to the gateway" from
in there would reach a bridge that does not answer. The installer runs on the
host, discovers the default route's next hop, and writes `SOUNDSTORM_GATEWAY`
into `.env` (best effort; absent just means no automatic forward). A NAT-PMP
request to that address leaves the container, is SNATed to the host's LAN
address by Docker, and reaches the router as if the host sent it — which is
exactly the address the mapping should point at. The mapper opens the port
immediately when remote access is switched on (before the reachability probe
runs) and refreshes it on its own timer, because a router lease is measured in
hours while the certificate loop only wakes every twelve.

Where none of the three could open the port — no gateway, no UPnP URL, and a
router that ignores what it was sent — remote access still works with a
hand-forwarded port; the automatic step just does nothing, and the reachability
probe reports the port closed until it is forwarded by hand.

The next piece is Stage 3 (IPv6), below.

### Stage 3 — IPv6

Often the simplest ingress of all: many ISPs hand out public IPv6, which has no
NAT, so the box already has a globally routable address.

- Publish an `AAAA` record for the box's global IPv6 (the service already knows
  how to write one), verified by the same reachability challenge.
- Guide a firewall pinhole where one is needed (PCP can request this on some
  routers).
- The catch: the *visitor's* network must also have IPv6, so this augments the
  IPv4 path rather than replacing it. Prefer it when both ends have it.

**Built: the dual-stack path, end to end on the software side.** The public
`net` name now carries an `A` and an `AAAA` at once (`handlePublic` no longer
clears the other family), and the install publishes each from a separate call
pinned to that address family (`Client.SetPublicVia`, `autoCert.publishRemote`
trying `tcp4` then `tcp6`). This falls straight out of the SSRF-safe design: the
service publishes the family of the request's *source*, and the only way to make
the source IPv6 is to actually reach the service over IPv6 — which proves the box
has a working global v6, exactly as the reachability probe proves the port is
open. So there is nothing new to trust. A visitor connects on whichever family
they have; a record that later goes dark is covered by the browser trying both
(Happy Eyeballs). The IPv4 call is pinned too, so the port-forward path keeps
publishing `A` regardless of what the default route would have chosen.

**Not built, and each is a deliberate open item, not an oversight:**

- **The container needs IPv6 egress for any of this to activate.** By default a
  Compose bridge network is IPv4-only, so the `tcp6` publish call simply fails
  to dial and nothing is published — the path is dormant, costing nothing, until
  the container has a global v6. Enabling that (`enable_ipv6` on the network, a
  host daemon that does IPv6, and either a routed prefix or NDP proxying) is
  host-dependent and can break `docker compose up` where the host has no v6 — so
  it is the kind of fragile, external-dependency step this project keeps
  **opt-in**, the way Tailscale is. The switch for it is not wired yet.
- **The IPv6 firewall pinhole.** v6 has no NAT, but home routers often run a
  stateful firewall that blocks unsolicited inbound. Opening it is a different
  request from a port map: PCP's `MAP` can do it where the router honours PCP
  over v6, and UPnP has a separate `WANIPv6FirewallControl:AddPinhole`. Neither
  is wired; today a v6 box behind a closed firewall publishes nothing because
  the reachability probe correctly fails, and the owner would forward/allow by
  hand. `internal/portmap` is the place this would go.

Both are verify-against-a-live-network steps with no way to exercise them from
CI or a single dev box, so they are called out here rather than shipped
untested — the same treatment the Tailscale `ts.net` path got.

## Security

Putting a home server on the internet is a real decision. It ships **explicitly
opt-in, with a plain warning**. The review that had to come first is done: the
setup-code signup gate, per-account sign-in throttle, request timeouts,
same-origin/CSRF checks, the reader-XSS fix, and the name-constrained local CA
are all in place, which is what makes exposing the surface defensible. The
name service's new outbound probe is SSRF-safe by construction (above).

## Open questions

- The public name's label (`net`, `remote`, or reuse `home` with a flag).
- Whether the client should auto-detect "I am away from home" and switch names,
  or leave it to the reachability probe it already does.
- How loudly to surface exposure in the UI once remote access is on (a standing
  banner vs. a one-time acknowledgement).
