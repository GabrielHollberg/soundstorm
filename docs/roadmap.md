# Roadmap

The vertical slice works: one login, one search, two backends provisioned with
zero keys, playback in place. What follows is ordered by what would most change
whether this is usable, not by what is most interesting to build.

## 1. Subtitles

Jellyfin has the subtitle tracks and hls.js can render them, but nothing in
SoundStorm asks for either. For film and TV this is the most visible thing still
missing.

The pieces exist: PlaybackInfo reports the available `MediaStreams` of type
Subtitle, Jellyfin can deliver them as WebVTT on a separate endpoint or burn
them into the transcode, and the full hls.js build (already vendored, rather
than the light one, for exactly this reason) handles them.

## 2. More ebook formats

The reader handles EPUB. foliate-js also ships MOBI, AZW3, FB2 and CBZ readers
that were not vendored, and `internal/epub` only knows EPUB, so the library
scan ignores everything else on disk.

PDF is the awkward one either way: weak embedded metadata, so the filename is
often all there is to go on.

## 3. Reading position is per-account, not per-device

Progress is stored as an EPUB CFI in SoundStorm's state, so it already follows you
between browsers. What it does not do is merge sensibly if two devices read the
same book at once - last writer wins. Fine for one account; revisit with
multi-user.

## 4. Real libraries, real scale

Everything so far has been tested against nine synthetic files. Unknowns that
only show up at size:

- search latency with 100k tracks
- whether the merged ranking is still sane when every backend returns 25 hits
- Jellyfin's first scan of a large library blocking provisioning
- Audiobookshelf's one-extra-request-per-play cost when many people press play
- the ebook scan parses every EPUB on first boot; the mtime cache makes
  rescans cheap but a cold start on a big library is untested

Do not optimize any of this before pointing it at an actual library.

## 5. Multi-user

Currently one account. Real multi-user means per-user libraries and per-user
play state, which means mapping SoundStorm accounts onto backend accounts — the
provisioner would create a Navidrome and Jellyfin user per SoundStorm user rather
than one shared `soundstorm` account. That is a real feature, not a slice.

## 6. HTTPS

The session cookie currently crosses the wire in the clear on a LAN. The cookie
is marked `Secure` automatically when served over TLS, so this is mostly a
deployment story: a reverse proxy, or built-in ACME.

## Deliberately not planned

Transcoding *by SoundStorm*, metadata scraping, library scanning, TV client apps,
rebuilding an app store. See CLAUDE.md for why each is a trap.
