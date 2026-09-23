# Roadmap

The vertical slice works: one login, one search, two backends provisioned with
zero keys, playback in place. What follows is ordered by what would most change
whether this is usable, not by what is most interesting to build.

## 1. Real libraries, real scale

Everything so far has been proven against thirteen synthetic files. That is
enough to show each mechanism works and nothing about whether it holds up.

Unknowns that only appear at size:

- search latency with 100k tracks, and whether the merged ranking is still
  sensible when every backend returns 25 hits
- Jellyfin's first scan of a large library blocking provisioning
- the ebook scan parses every EPUB on first boot; the mtime cache makes
  rescans cheap but a cold start on thousands of books is untested
- how often real files fall outside the conservative direct-play profile, and
  therefore how much transcoding actually happens
- whether one machine can transcode for more than one viewer at a time

This is now the largest gap in the project by a wide margin. Every other item
below is a feature; this one is the question of whether the features work.

## 2. More ebook formats

EPUB and PDF are handled. MOBI, AZW3, FB2 and CBZ are not, and neither the
library scan nor the reader knows about them.

foliate-js already ships readers for all of them - `mobi.js`, `fb2.js`,
`comic-book.js` - and they were simply not vendored. The Go side would need
metadata for each, which for MOBI means parsing the PalmDOC header and for CBZ
means there is nothing to parse and the filename is all there is.

## 3. Reading position is per-account, not per-device

Progress is stored as an EPUB CFI in SoundStorm's state, so it already follows you
between browsers. What it does not do is merge sensibly if two devices read the
same book at once - last writer wins. Fine for one account; revisit with
multi-user.

## 4. Bitmap subtitles

Only text subtitles are offered. PGS, VOBSUB and DVB are pictures of text, and
attaching one as a track renders nothing at all - so they are filtered out
rather than shown and silently broken.

Making them work means burning them into the video, which turns a stream that
might have been direct played into a mandatory re-encode. Jellyfin can do it;
the work is deciding when to ask, since the cost is real and the user is the
only one who knows whether they want those subtitles.

## 5. Multi-user, the rest of it

Accounts exist: an owner, members, per-person reading and listening position,
and a per-person Audiobookshelf account so two people do not overwrite each
other in a book. What is not built:

- **Per-user Navidrome and Jellyfin accounts.** They share one. Nothing
  SoundStorm surfaces from them differs per person today, so this only matters
  when watched state, play counts or favourites reach the UI.
- **Per-title or age-rating filtering.** Access is per whole library, so "no
  films for the seven-year-old" works and "only these films" does not. Doing it
  properly means per-user Jellyfin accounts and its parental ratings, which is
  a second provisioning path for one feature - worth it only once somebody
  actually asks.
- **Sessions cannot be listed or revoked individually.** Changing a password
  signs every other device out, which covers the leaked-password case; there
  is still no "sign out that one phone".

## 6. HTTPS

Built: `SOUNDSTORM_TLS` serves HTTPS from a local certificate authority, and
the Tailscale profile gives a real certificate.

**Built and live: a trusted certificate on a plain LAN address, with no
account.** `SOUNDSTORM_TLS=auto` registers the install with the name service
(`cmd/soundstorm-names`), which names it `<id>.home.soundstorm.dev`, points
that at its LAN address through Porkbun's DNS, and publishes the DNS challenge
Let's Encrypt checks. Rehearsed end to end against Pebble, in CI and in a real
browser. See `docs/names-service.md`. What is left, in order:

1. ~~Deploy the name service at `names.soundstorm.dev` and run one install
   against Let's Encrypt staging.~~ Done 2026-09-23.
2. ~~Then production, and make `auto` what the installer writes.~~ Done
   2026-09-23: staging, then production, on a real install, and both
   installers now default to auto.
3. Apply for `soundstorm.dev` on the Public Suffix List before installs
   number in the hundreds. Until then every install shares the domain's weekly
   Let's Encrypt allowance of about fifty new certificates.
4. Remote access without Tailscale: the same name pointed at the home's public
   address, with the port opened by UPnP where the router allows it. The
   service refuses public addresses today, on purpose - it would first have to
   prove the install is really there, or a trusted certificate on a public
   address is a phishing kit.

The rule that keeps it cheap, and must not bend: **the service never carries
media**. A relay would scale its cost with every film watched; DNS records
and a challenge every couple of months do not.

## Deliberately not planned

Transcoding *by SoundStorm*, metadata scraping, library scanning, TV client apps,
rebuilding an app store. See CLAUDE.md for why each is a trap.
