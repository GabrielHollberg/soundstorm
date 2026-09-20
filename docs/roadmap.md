# Roadmap

The vertical slice works: one login, one search, two backends provisioned with
zero keys, playback in place. What follows is ordered by what would most change
whether this is usable, not by what is most interesting to build.

## 1. Seeking in transcoded video

Transcoding works: a file the browser cannot decode is re-encoded by Jellyfin on
the fly and plays. What it cannot do is seek. Measured in Chrome against an
HEVC/FLAC/MKV file:

- `video.seekable.end(0)` is `0` - the browser considers the stream unseekable
- a 20 second file reports `duration` of 9.9s, growing as it buffers
- seeking to 15s snaps back to 3s

This is inherent to progressive transcoding, not a bug to fix in place: the
server cannot map a byte offset to a timestamp in an encode that has not
happened yet. Direct-played video is unaffected - it is a real file.

Two ways out:

**HLS.** Jellyfin's best-tested transcode path and what its own web client uses.
Segments give the browser a real duration and a real seekable range, so native
controls work normally and adaptive bitrate comes free. Costs one vendored
dependency: hls.js, MIT, ~150KB, ships a prebuilt UMD file so it needs no build
step. The precedent for vendoring is already set by foliate-js.

**Custom controls over progressive.** Keep the current stream, add `startTimeTicks`
to restart the encode at a chosen point, and build a transport bar that tracks
the offset. No new dependency, but it means owning a video scrubber and every
seek costs a transcode restart.

HLS is the better product outcome; the progressive path stays as the fallback
for anything that cannot use it.

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
