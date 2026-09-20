# Roadmap

The vertical slice works: one login, one search, two backends provisioned with
zero keys, playback in place. What follows is ordered by what would most change
whether this is usable, not by what is most interesting to build.

## 1. Transcoding fallback for video

Today the player asks Jellyfin for `static=true` — the original file, no
remuxing. That covers h264/aac in mp4 and nothing else. A real library is full
of HEVC, DTS and MKV, which a browser will refuse, and the failure is silent:
the video element just shows nothing.

The fix is Jellyfin's HLS endpoint with a device profile describing what the
browser can decode. Jellyfin does all the work; atrium has to ask correctly and
proxy an HLS manifest plus segments rather than one file.

**This is the first thing to build.** Without it the product is "plays some of
your films", which is worse than no claim at all.

## 2. Audiobooks and ebooks

The two media types in the original ask that are still missing.

- **Audiobookshelf** for audiobooks. Provisioning: it has an init endpoint for
  the first root user, same pattern as the other two.
- **Calibre / OPDS** for ebooks. Note there is already a Calibre content server
  running on this machine on port 8080 — a real library to point at rather than
  a synthetic one.

Ebooks need more than a stream endpoint: an epub is read, not played. Either an
in-browser reader or an honest download button. A download button is a seam, but
a small one, and it beats shipping a bad reader.

## 3. Verify the OPDS adapter before trusting it

The original OPDS adapter had a silent bug — double-encoded search paths meant
any multi-word query returned nothing, never an error. It is fixed in
`internal/httpx` and covered by tests, but the adapter itself was deleted rather
than ported. When it comes back, port the regression test with it.

## 4. Real libraries, real scale

Everything so far has been tested against five synthetic files. Unknowns that
only show up at size:

- search latency with 100k tracks
- whether the merged ranking is still sane when every backend returns 25 hits
- Jellyfin's first scan of a large library blocking provisioning

Do not optimize any of this before pointing it at an actual library.

## 5. Multi-user

Currently one account. Real multi-user means per-user libraries and per-user
play state, which means mapping atrium accounts onto backend accounts — the
provisioner would create a Navidrome and Jellyfin user per atrium user rather
than one shared `atrium` account. That is a real feature, not a slice.

## 6. HTTPS

The session cookie currently crosses the wire in the clear on a LAN. The cookie
is marked `Secure` automatically when served over TLS, so this is mostly a
deployment story: a reverse proxy, or built-in ACME.

## Deliberately not planned

Transcoding *by atrium*, metadata scraping, library scanning, TV client apps,
rebuilding an app store. See CLAUDE.md for why each is a trap.
