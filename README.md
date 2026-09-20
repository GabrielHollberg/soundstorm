# atrium

One login and one search box over your whole media library.

`docker compose up`, create an account, drop files into folders. Movies, music,
audiobooks and ebooks all answer the same search and play in the same window.
You never see an API key, and you never see a second login.

![Search results from two different media servers in one list](docs/shots/2-search.png)

## The idea

Installing self-hosted media servers is a solved problem — Umbrel, CasaOS,
Unraid and a dozen compose stacks all do it. What none of them finish is the
*integration*: you end up with four containers, four admin accounts to create,
four API keys to mint, four web UIs and four search boxes.

atrium is that last mile. It runs the specialist servers, provisions their
credentials itself, and puts one interface on top:

| | |
| --- | --- |
| **Music** | Navidrome — best-in-class tag handling, fast scanner, smart playlists |
| **Video** | Jellyfin — metadata, artwork, hardware transcoding |
| **Audiobooks** | Audiobookshelf — *not yet wired up* |
| **Ebooks** | Calibre / OPDS — *not yet wired up* |

Using the real servers instead of reimplementing them is the whole trick. When
you search "dune" and get a film back with a real poster and a real synopsis,
that is Jellyfin's metadata work, not atrium's.

## Status

This is a **working vertical slice**, not a finished product. What runs today:

- `docker compose up` brings up Navidrome, Jellyfin and atrium
- atrium provisions both backends on first boot — **zero API keys typed**
- one account, created on first visit, guarding everything
- one search across both, merged and ranked
- video and audio play **inside atrium**, with seeking
- neither backend publishes a port; atrium is the only door

Not built yet: audiobooks, ebooks, transcoding for formats a browser cannot
play, multi-user, HTTPS. See [docs/roadmap.md](docs/roadmap.md).

## Getting started

```sh
git clone <this repo> && cd atrium
pwsh scripts/make-sample-media.ps1   # optional: a tiny synthetic library
docker compose up --build
```

Open <http://localhost:8099> and create your account. That is the entire setup.

Drop your own files into:

```
media/
  music/     Artist/Album/01 - Track.mp3
  movies/    Film Name (2021)/Film Name (2021).mkv
```

Navidrome rescans every minute. Jellyfin watches for changes.

Port 8099 rather than 8080 because 8080 is crowded — a Calibre content server
defaults to it. Override with `ATRIUM_PORT`.

## How it works

```
                    browser
                       │  one origin, one cookie
                 ┌─────▼─────┐
                 │  atrium   │  auth · search · player · byte proxy
                 └─────┬─────┘
          ┌────────────┴────────────┐
          ▼                         ▼
    Navidrome:4533            Jellyfin:8096
    (no published port)     (no published port)
```

Three rules hold it together.

**A dead backend must never take the search down.** Every backend gets its own
deadline and its own error slot. If Jellyfin is restarting you still get your
music, and the response says which source failed and why.

**Normalization happens at the edge.** Each adapter is the only code that knows
its backend's vocabulary. Everything past it speaks `media.Item`.

**Nothing upstream ever reaches the browser.** Results carry no upstream URLs.
atrium fetches media server-side and pipes it through, which is what lets the
backends stay off any published port — and therefore what makes "one login"
true rather than decorative.

That last rule reverses an earlier design decision, deliberately. See the
package comment in `internal/stream` for the cost/benefit.

## Layout

```
cmd/atrium/          main, env config, graceful shutdown
internal/media/      Item, Query, Kind — the shared vocabulary
internal/source/     the Source interface, Target, Registry
internal/source/*/   one package per backend
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
| GET | `/api/search?q=&kind=&limit=` | session | federated search |
| GET | `/api/stream/{source}/{id}` | session | media bytes |
| GET | `/api/art/{source}/{id}` | session | artwork |

`kind` is one of `music`, `audiobook`, `ebook`, `video`, and may repeat or be
comma-separated. Filtering skips non-matching backends entirely.

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

- **Zero third-party dependencies.** Standard library only, including password
  hashing (`crypto/pbkdf2`, stdlib since Go 1.24). The container build
  downloads nothing and there is no supply chain to audit.
- **No config file.** Everything comes from environment variables set by
  compose, plus state atrium provisions itself. A config file is one more thing
  for a human to edit, and the goal is that a human edits nothing.
- **Fail loudly at startup, degrade gracefully at runtime.**
- `gofmt` clean, `go vet` clean, tests pass.

## Commands

```sh
go test ./...
go run ./cmd/atrium           # needs the backends reachable; compose is easier
docker compose up --build
docker compose logs -f atrium # watch provisioning
```

## Notes

- **Module path** is `github.com/gabehollberg/atrium`. If the repo lives
  elsewhere, fix `go.mod` and run
  `grep -rl gabehollberg/atrium . | xargs sed -i 's|gabehollberg/atrium|<you>/atrium|g'`.
- **No `go.sum`** and that is correct, not an oversight.
- **Serve over TLS or a private network.** Subsonic stream URLs carry
  credentials in the query string — that is the protocol, and although those
  URLs never leave atrium, the session cookie still crosses the wire.
