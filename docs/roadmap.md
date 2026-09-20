# Roadmap

Re-ordered for the product target: an easy all-in-one for other people, not a
bespoke gateway for one stack. See CLAUDE.md for the positioning behind this.

## 0. The one-command stack

A `docker compose` bundle that brings up Jellyfin, Navidrome, Audiobookshelf,
Calibre-Web and atrium already wired to each other, with **zero API keys typed
by a human**. Services share secrets through the compose environment or atrium
provisions them on first boot.

This is the whole product. Everything else is support for it. It is also the
only thing Umbrel, CasaOS and Unraid will not do for you — they install the
apps and leave you with five disconnected UIs.

## 1. A web client

One search box, results grouped by kind, a visible banner when `degraded` is
true. Serve it from the binary with `embed` so deployment stays a single
artifact. An API is not a product.

## 2. Auth

Currently none, which was fine behind a tailnet and is not fine for anyone
else. Smallest useful version is a single shared token. Better version proxies
whatever the upstreams already use, so there is one login rather than six.

Mandatory before anyone else runs this.

## 3. Verify the two uncertain adapters

Audiobookshelf and Kiwix are marked `VERIFY:`. Point them at live servers, use
`/api/probe/<id>`, correct the structs. Cheap, and it stops being optional the
moment someone else depends on the results.

## 4. Caching

Every search hits every backend. A short-lived cache keyed on (query, kinds)
makes repeat searches instant and blunts a slow backend. Do this when you
notice the latency, not before.

## 5. Better ranking

`federate.Relevance` is a readable heuristic: exact title match beats prefix
beats substring beats creator match. The upgrade path is a local index scored
with BM25 — but do not build it until you can point at a query it gets wrong.

## Deliberately not planned

Transcoding, metadata scraping, TV client apps, proxying media bytes. See
CLAUDE.md for why each one is a trap.
