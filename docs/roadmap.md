# Roadmap

SoundStorm works end to end: one login and one search over six backends
provisioned with nobody logging in (Navidrome, Jellyfin, Audiobookshelf,
Immich, Storyteller, AudioMuse-AI) plus the ebook and document shelves it reads
itself; apps for the web, Android (and Android TV), iPhone and Apple TV; remote
access; per-person photos with phone backup and imports. What follows is
ordered by what would most change whether it is usable, not by what is most
interesting to build.

## 1. Scale

Measured on a real library - 4,413 songs, 1,663 ebooks, 113 audiobooks - which
found and fixed paging repeats, made-up paths and slow first loads. Still
untested: a 100k-track library, Jellyfin's first scan of a large film library,
and how many viewers one machine can convert video for at once (conversions are
now capped at four play sessions per person).

## 2. More ebook formats

EPUB and PDF are handled. MOBI, AZW3, FB2 and CBZ are not. foliate-js ships
readers for them that were not vendored; the Go side would need metadata for
each.

## 3. Smaller open items

- **Photo albums kept as albums.** Imports file by date; the albums a Google or
  iCloud download knows about are not kept yet.
- **Near-duplicate photos** (an edited or re-compressed copy) are kept twice.
- **iPhone: native audio (stage two) and photo backup.** Android has both.
- **Casting** to a Chromecast needs short-lived per-song URLs.
- **Skipping intros** needs a Jellyfin plugin to know where they are.
- **Last.fm scrobbling** needs an API key registered to the project;
  ListenBrainz works today.
- **The Plex import** has been tested against stand-ins, not a real account.
- **Bitmap subtitles** (PGS, VOBSUB) would mean burning them in, a mandatory
  re-encode; only text subtitles are offered.
- **Signing.** The Android app is signed with a test key (moving to a release
  key means everyone reinstalls once), and the Windows setup file is unsigned,
  which is why it needs the Unblock step.
- **Sessions cannot be listed or revoked one by one.** Changing a password signs
  every other device out.
- **Per-title filtering** ("only these films"): access is per whole shelf; doing
  more means per-person Jellyfin accounts and its parental ratings.

Settled since this roadmap was first written: a backend's setup that fails half
way through now finishes on the next attempt, its passwords kept from the
moment they are made; favorites, positions and history
are kept per person by SoundStorm itself, so per-person Navidrome and Jellyfin
accounts are not needed; reading position is per account and last writer wins,
which suits one person reading one book.

## 4. HTTPS and remote access

Built and live:

- **A trusted certificate on a plain LAN address, with no account.**
  `SOUNDSTORM_TLS=auto` names each install `<id>.home.soundstorm.dev` through
  the name service and gets a Let's Encrypt certificate by DNS challenge. Both
  installers default to it. See `docs/names-service.md`.
- **Remote access without Tailscale.** A switch in Settings (or `--remote`)
  gives the install `<id>.net.soundstorm.dev` on the home's public address,
  on the same certificate, opens the port by PCP, NAT-PMP or UPnP where the
  router allows, checks it can really be reached, and says what to do where it
  cannot (carrier-grade NAT, double NAT). It came after several security
  reviews of everything facing the internet. See `docs/remote-access.md`.
- **Tailscale** as an opt-in profile, for homes behind carrier-grade NAT or for
  anybody who would rather not be on the internet.

Still to do: apply for `soundstorm.dev` on the **Public Suffix List** before
installs number in the hundreds. Until then every install shares the domain's
Let's Encrypt allowance and the name service's daily budgets, and another
install's name is "same site" to browsers.

The rule that keeps it cheap, and must not bend: **the service never carries
media**.

## Deliberately not planned

Transcoding *by SoundStorm*, metadata scraping, library scanning, rebuilding an
app store. See CLAUDE.md for why each is a trap.

TV apps were on this list, and were built anyway, knowingly: Android TV and
Google TV run the Android app with the page's TV mode, and Apple TV has a
native app because tvOS has no web view.
