# SoundStorm for developers

## The idea

Installing self-hosted media servers is a solved problem. What nobody finishes
is the *integration*: four containers, four admin accounts, four API keys, four
web UIs and four search boxes. SoundStorm is that last mile. It runs the
specialist servers, sets up their credentials itself, and puts one interface on
top. The person using it never learns Jellyfin exists.

Using the real servers instead of reimplementing them is the whole trick. When
a search for "dune" returns a film with a real poster and synopsis, that is
Jellyfin's work, not SoundStorm's. SoundStorm owns login, search, playback and
the bytes, and deliberately does **not** own transcoding, metadata scraping or
library scanning.

`CLAUDE.md` in the repository root is the long-form design record: every
decision, and the reason for it, including the ones that were reversed.

## Building and running

```sh
git clone https://github.com/GabrielHollberg/soundstorm && cd soundstorm
docker compose -f docker-compose.yml -f docker-compose.dev.yml up --build
```

`docker-compose.yml` names a published image and nothing else, so it works on
its own for somebody who never cloned anything. `docker-compose.dev.yml` adds
`build: .` on top.

```sh
go test ./...
pwsh scripts/make-sample-media.ps1     # a synthetic library, no downloads
pwsh scripts/fetch-test-library.ps1    # ~750MB of real public-domain media
```

On Windows, Smart App Control refuses to run freshly built test binaries, so
run the suite in a container instead, as CI does:

```sh
docker run --rm -v "//h/dev/soundstorm:/src" -w /src golang:1.27-alpine go test ./...
```

Releases are tags: pushing `vX.Y.Z` runs `.github/workflows/publish.yml`, which
tests, builds multi-arch images (amd64 and arm64) to
`ghcr.io/gabrielhollberg/soundstorm`, and attaches `SoundStorm-Setup.cmd` to the
GitHub release. The README's download link is
`/releases/latest/download/SoundStorm-Setup.cmd`, so it always follows the newest
release.

## How it works

```
                              browser
                                 │  one origin, one cookie
                         ┌───────▼────────┐
                         │   soundstorm   │  auth · search · player · byte proxy
                         └───────┬────────┘
   ┌───────────┬─────────────┬───┴──────────┬────────────┬─────────────┬──────────────┐
   ▼           ▼             ▼              ▼            ▼             ▼              ▼
Navidrome   Jellyfin   Audiobookshelf    Immich     Storyteller   AudioMuse-AI   library/ebooks
 music     films + TV    audiobooks      photos     read-along    moods, radio   library/documents
                                                                                 (folders)
                 — no backend publishes a port; SoundStorm is the only door —
```

Three rules hold it together:

1. **A dead backend must never take the search down.** Every backend gets its
   own deadline and error slot. If Jellyfin is restarting you still get your
   music, and the response says which source failed.
2. **Normalization happens at the edge.** Each adapter is the only code that
   knows its backend's vocabulary. Everything past it speaks `media.Item`.
3. **A backend is two halves: search and provisioning.** A backend a human must
   configure by hand defeats the point.

And one consequence: **nothing upstream reaches the browser.** SoundStorm
fetches media server-side and pipes it through, which is what lets the backends
stay off any published port, and what makes "one login" true.

Ebooks and documents have no backend. An EPUB describes itself and needs no
transcoding, so SoundStorm reads the folder directly. That is the line:
SoundStorm owns a media type only when it is self-describing and needs no
transcoding. Video never will be.

## Layout

```
cmd/soundstorm/        main, env config, subcommands (backup, restore, reset-password, train-looks)
cmd/soundstorm-names/  the name service behind *.soundstorm.dev
internal/media/        Item, Query, Kind - the shared vocabulary
internal/source/       the Source interface, optional interfaces, Registry
internal/source/*/     one package per backend (subsonic, jellyfin, audiobookshelf,
                       immich, storyteller, audiomuse, localbooks, opds)
internal/provision/    first-boot credential provisioning  <- the load-bearing part
internal/federate/     parallel fan-out, per-source deadlines, merge, rank
internal/stream/       media byte proxy with Range support
internal/httpapi/      HTTP handlers
internal/webui/        the embedded UI (vanilla JS, no build step)
internal/library/      the folders: intake, placement, duplicates, the bin
internal/collections/  per-person favorites, playlists, history, listens
internal/state/        credentials, accounts, sessions - what must survive a restart
internal/auth/         login, PBKDF2, sessions, throttling
internal/servetls/     local certificate authority, one port for http and https
internal/acme/         a minimal RFC 8555 client (dns-01)
internal/names/        the name service's logic
internal/portmap/      PCP, NAT-PMP and UPnP port mapping
internal/plex/         importing playlists from Plex
internal/scrobble/     ListenBrainz
internal/discover/     MusicBrainz, ListenBrainz, Wikipedia
internal/lyrics/       LRCLIB
internal/epub/, pdf/, tags/   read just enough metadata, no dependencies
internal/photoimport/  bringing photo downloads in (Takeout, iCloud, social), dates and sidecars
internal/beats/        hearing every song for its beats, on the server
internal/flac/         a FLAC decoder, for the beats
internal/training/     the developer's tool for training the visualizers
internal/httpx/        the hardened HTTP client every adapter uses
internal/starter/      the starter library bundled into the binary
android/               the Android app (phones and TVs); see android/README.md
ios/                   the iPhone and Apple TV apps; see ios/README.md
site/                  the documentation website: how it all works, in depth
```

## Adding a backend

Two halves:

1. **Search.** Implement `source.Source` in `internal/source/<name>/`, plus
   `source.Streamer` and `source.ArtProvider` if it serves bytes, and whichever
   optional interfaces fit (`ItemGetter`, `RecentLister`, `Rescanner`...).
2. **Provisioning.** Add a function in `internal/provision/` that walks the
   backend's first-run flow and returns credentials, and register it.

The second half is the one people skip, and the one that matters.

## Conventions

- **Zero third-party Go dependencies.** Standard library only, including
  password hashing (`crypto/pbkdf2`). There is no `go.sum`.
- **Two vendored browser libraries**, foliate-js (MIT) and hls.js (Apache-2.0),
  checked in under `internal/webui/assets/vendor/`, pinned and embedded, fetched
  at no point during a build.
- **No config file.** Environment variables set by compose, plus the state
  SoundStorm provisions itself.
- **Every new `SOUNDSTORM_` setting must also be added to `docker-compose.yml`**,
  which passes settings by name.
- **Fail loudly at startup, degrade gracefully at runtime.**
- `gofmt` clean, `go vet` clean, tests pass.
- Backend images are pinned where their APIs move (Immich by major version,
  Storyteller and AudioMuse-AI by digest).

## API

The UI talks to SoundStorm's JSON API under `/api/`. The routes are listed in
`Routes()` in `internal/httpapi/httpapi.go`. Every route but sign-up, sign-in,
sign-out, `/api/session`, `/api/remote-reachable`, `/healthz`, `/ca.crt` and
the static files needs a session, owner-only routes are mounted separately,
and cross-site writes are refused. A search always answers 200: check
`degraded` and `sources` for what was missing.

## Notes

- The module path is `github.com/GabrielHollberg/soundstorm`.
- The starter library (one ebook, one audiobook, one song; public domain or CC)
  is embedded in the binary and unpacked once. `scripts/fetch-starter-media.sh`
  records where each file comes from.
