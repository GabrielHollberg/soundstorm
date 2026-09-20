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

## 2. An ebook reader

Ebooks search and rank alongside everything else, but clicking one downloads it
rather than opening it. That is an honest seam and a visible one — the only
result type that leaves atrium.

An epub is a zip of XHTML, so a reader is more tractable than it sounds, but it
needs pagination, a font/theme control and reading-position sync to be worth
having. Reading position in particular is the thing Calibre-Web would otherwise
own, and splitting it across two apps is worse than not having it.

## 3. Rotate the Calibre-Web password

Calibre-Web is provisioned with its published default login (`admin`/`admin123`)
because it has no configuration API and both of its password forms accept the
change, report success, and leave the password untouched. Posting harder is not
safe: the same form carries every permission as a checkbox, so a partial post
can strip the admin role.

It is unreachable except through atrium, so this is not urgent — but it is the
one credential in the stack that is not generated, and that should not stay true
forever. Worth revisiting after a Calibre-Web upgrade.

## 4. Real libraries, real scale

Everything so far has been tested against nine synthetic files. Unknowns that
only show up at size:

- search latency with 100k tracks
- whether the merged ranking is still sane when every backend returns 25 hits
- Jellyfin's first scan of a large library blocking provisioning
- Audiobookshelf's one-extra-request-per-play cost when many people press play

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
