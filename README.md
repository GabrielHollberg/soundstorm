# SoundStorm

One login and one search box over your whole media library.

`docker compose up`, create an account, and put files in the folders SoundStorm
made for you. Movies, music, audiobooks and ebooks all answer the same search
and play in the same window. You never see an API key, and you never see a
second login.

![One search returning an ebook, a film, music and an audiobook in a single ranked list](docs/shots/2-search.png)

## The idea

Installing self-hosted media servers is a solved problem — Umbrel, CasaOS,
Unraid and a dozen compose stacks all do it. What none of them finish is the
*integration*: you end up with four containers, four admin accounts to create,
four API keys to mint, four web UIs and four search boxes.

SoundStorm is that last mile. It runs the specialist servers, provisions their
credentials itself, and puts one interface on top:

| | |
| --- | --- |
| **Music** | Navidrome — best-in-class tag handling, fast scanner, smart playlists |
| **Films and TV** | Jellyfin — metadata, artwork, season/episode structure |
| **Audiobooks** | Audiobookshelf — author/narrator/series, per-title listening position |
| **Ebooks** | SoundStorm itself — an EPUB describes itself, so no backend is needed |

Using the real servers instead of reimplementing them is the whole trick. When
you search "dune" and get a film back with a real poster and a real synopsis,
that is Jellyfin's metadata work, not SoundStorm's.

## Status

This is a **working vertical slice**, not a finished product. What runs today:

- `docker compose up` brings up all four backends plus SoundStorm
- SoundStorm provisions every one of them on first boot — **zero API keys typed**
- one account, created on first visit, guarding everything
- one search across all four, merged and ranked
- music, films, TV and audiobooks play **inside SoundStorm**
- video a browser cannot decode is **transcoded by Jellyfin on the fly** and
  served as HLS, so HEVC, MKV and DTS play *and seek* like anything else
- **subtitles**, embedded or sidecar, converted to WebVTT and selectable
- ebooks are **read inside SoundStorm**, and remember where you stopped
- no backend publishes a port; SoundStorm is the only door

Not built yet: multi-user and HTTPS. See [docs/roadmap.md](docs/roadmap.md).

## Getting started

```sh
git clone <this repo> && cd soundstorm
pwsh scripts/make-sample-media.ps1   # optional: a tiny synthetic library
docker compose up --build
```

Open <http://localhost:8099> and create your account. That is the entire setup.

SoundStorm creates a `library/` folder on first run and the app's first screen shows
you what goes where:

```
library/
  music/       Talk Talk/Laughing Stock/01 Myrrhman.flac
  movies/      Arrival (2016)/Arrival (2016).mkv
  tv/          Severance (2022)/Season 01/Severance - S01E01.mkv
  audiobooks/  Ursula K. Le Guin/A Wizard of Earthsea/book.m4b
  ebooks/      A Wizard of Earthsea.epub
```

Put a file in the matching folder and it appears in search. Navidrome rescans
every minute; Jellyfin and Audiobookshelf watch for changes; ebooks are picked
up within two minutes. Until a scan catches up the app says so, rather than
pretending the file is not there.

![The folder guide a new install opens on](docs/shots/1-library.png)

`library/ebooks` is a plain folder of `.epub` files. It can also be an existing
Calibre library — SoundStorm reads Calibre's `metadata.opf` sidecars, so a library
you already curate keeps its series, tags and corrected authors, with no
SQLite driver and no Calibre-Web container.

Port 8099 rather than 8080 because 8080 is crowded — a Calibre content server
defaults to it. Override with `SOUNDSTORM_PORT`.

## How it works

```
                            browser
                               │  one origin, one cookie
                       ┌───────▼───────┐
                       │    soundstorm     │  auth · search · player · byte proxy
                       └───────┬───────┘
      ┌──────────────┬─────────┴─────────┬──────────────┐
      ▼              ▼                   ▼              ▼
 Navidrome     Jellyfin        Audiobookshelf   library/ebooks
   :4533         :8096              :80          (a folder)
              films + TV
        — none of the three publishes a port —
```

Jellyfin appears twice: films and series are separate Jellyfin libraries, with
different scrapers and different structure, so they are two SoundStorm sources
sharing one token. Searching a show name finds the show; searching an episode
title finds the episode.

Ebooks have no backend at all. An EPUB carries its own title, author and cover
in a documented format, and needs no transcoding, so SoundStorm reads the folder
directly. That is the line: **SoundStorm can own a media type when it is
self-describing and needs no transcoding.** Video never will be.

Three rules hold it together.

**A dead backend must never take the search down.** Every backend gets its own
deadline and its own error slot. If Jellyfin is restarting you still get your
music, and the response says which source failed and why.

**Normalization happens at the edge.** Each adapter is the only code that knows
its backend's vocabulary. Everything past it speaks `media.Item`.

**Nothing upstream ever reaches the browser.** Results carry no upstream URLs.
SoundStorm fetches media server-side and pipes it through, which is what lets the
backends stay off any published port — and therefore what makes "one login"
true rather than decorative.

That last rule reverses an earlier design decision, deliberately. See the
package comment in `internal/stream` for the cost/benefit.

## Layout

```
cmd/soundstorm/          main, env config, graceful shutdown
internal/media/      Item, Query, Kind — the shared vocabulary
internal/library/    the folder layout: creates it, counts it
internal/source/     the Source interface, Target, Registry
internal/source/*/   one package per backend (subsonic, jellyfin,
                     audiobookshelf, localbooks, opds)
internal/epub/       EPUB metadata and resource reading
internal/provision/  first-boot credential provisioning  ← the load-bearing part
internal/state/      the little that must survive a restart
internal/auth/       single-account login, PBKDF2, sessions
internal/federate/   parallel fan-out, per-source deadlines, merge, rank
internal/stream/     media byte proxy with Range support
internal/httpapi/    handlers
internal/webui/      the embedded UI
```

## Endpoints

| Method | Path | Auth | Purpose |
| --- | --- | --- | --- |
| GET | `/` | — | the UI |
| GET | `/healthz` | — | liveness |
| GET | `/api/session` | — | does an account exist; am I signed in |
| POST | `/api/signup` | — | create the one account (first boot only) |
| POST | `/api/login` / `/api/logout` | — | |
| GET | `/api/setup` | session | per-backend provisioning progress |
| GET | `/api/library` | session | the folder layout and what is in it |
| GET | `/api/playback/{source}/{id...}` | session | how to play an item, and its subtitles |
| GET | `/api/hls/{source}/{path...}` | session | transcoded playlist and segments |
| GET | `/api/subtitle/{source}/{track...}` | session | one subtitle track, as WebVTT |
| GET | `/api/search?q=&kind=&limit=` | session | federated search |
| GET | `/api/stream/{source}/{id...}` | session | media bytes |
| GET | `/api/art/{source}/{id...}` | session | artwork |
| GET | `/api/book/manifest?source=&id=` | session | what is inside a book |
| GET | `/api/book/resource?source=&id=&path=` | session | one file from inside a book |
| GET/PUT | `/api/book/progress?source=&id=` | session | reading position |

The trailing `...` is load-bearing: an OPDS acquisition reference is a path with
slashes in it, and that is the id the adapter needs back.

`kind` is one of `music`, `video` (films), `tv`, `audiobook`, `ebook`, and may
repeat or be comma-separated. Filtering skips non-matching backends entirely.

A search always returns 200. Check `degraded` and the `sources` array.

## Adding a backend

Two halves, because a backend needs both:

1. **Search.** Implement `source.Source` in `internal/source/<name>/`, plus
   `source.Streamer` and `source.ArtProvider` if it serves bytes.
2. **Provisioning.** Add a function in `internal/provision/` that walks the
   backend's first-run flow and returns credentials, then a case in the switch
   in `provision.go` and a target in `targetsFromEnv`.

The second half is the one people skip, and it is the one that matters — a
backend a human has to configure by hand defeats the point of the project.

## Conventions

- **Zero third-party Go dependencies.** Standard library only, including
  password hashing (`crypto/pbkdf2`, stdlib since Go 1.24). There is no
  `go.sum` and the container build downloads nothing.
- **Two vendored browser libraries**, both under
  `internal/webui/assets/vendor/`: foliate-js (MIT) renders EPUB, and hls.js
  (Apache-2.0) plays transcoded video where the browser has no native HLS. Both
  are prebuilt, pinned by content, embedded in the binary and fetched at no
  point during a build, so the properties the zero-dependency rule protects all
  survive. hls.js is 620KB, so it is loaded lazily — only when a video actually
  needs a transcoded stream, never for music, books or direct-play films.
- **No config file.** Everything comes from environment variables set by
  compose, plus state SoundStorm provisions itself. A config file is one more thing
  for a human to edit, and the goal is that a human edits nothing.
- **Fail loudly at startup, degrade gracefully at runtime.**
- `gofmt` clean, `go vet` clean, tests pass.

## Commands

```sh
go test ./...
docker compose up --build
docker compose logs -f soundstorm          # watch provisioning
pwsh scripts/make-sample-media.ps1     # a synthetic library, no downloads
go run ./scripts/mkepub -out b.epub -title T -author A   # one test book
```

## Notes

- **Module path** is `github.com/gabehollberg/soundstorm`. If the repo lives
  elsewhere, fix `go.mod` and run
  `grep -rl gabehollberg/soundstorm . | xargs sed -i 's|gabehollberg/soundstorm|<you>/soundstorm|g'`.
- **No `go.sum`** and that is correct, not an oversight.
- **Serve over TLS or a private network.** Subsonic stream URLs carry
  credentials in the query string — that is the protocol, and although those
  URLs never leave SoundStorm, the session cookie still crosses the wire.
