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
  care where the name points — so issuance is unchanged for a public address.
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

### Stage 3 — IPv6

Often the simplest ingress of all: many ISPs hand out public IPv6, which has no
NAT, so the box already has a globally routable address.

- Publish an `AAAA` record for the box's global IPv6 (the service already knows
  how to write one), verified by the same reachability challenge.
- Guide a firewall pinhole where one is needed (PCP can request this on some
  routers).
- The catch: the *visitor's* network must also have IPv6, so this augments the
  IPv4 path rather than replacing it. Prefer it when both ends have it.

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
