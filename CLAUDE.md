# atrium — working notes for Claude

Read this first. It carries decisions made before the code existed, and one
important mismatch between what is on disk and what the project is now for.

## What this is

A federating gateway over self-hosted media servers. One search across music,
audiobooks, ebooks, video and offline article archives, returning one merged,
ranked list where every result has the same shape.

## The pivot (most important thing here)

The code on disk was written as a **personal** gateway over one specific
stack. The project has since been re-aimed at a **product**: an easy option
for people who want an all-encompassing media server.

Nothing in the core needs rewriting for that, but the gaps are real:

| On disk now | Needs to become |
| --- | --- |
| Hand-written JSON config with manually fetched API keys | Services provision and wire their own credentials; a human types nothing |
| No UI, API only | A web UI. An API is not a product. |
| No auth (assumes Tailscale) | Mandatory auth — other people are not behind your tailnet |
| Owner's hostnames as examples | Sensible defaults, discovery |

**Positioning, because it decides scope.** Installation is a solved and
crowded problem: Umbrel, CasaOS, Unraid Community Applications, Runtipi,
Yunohost, Saltbox, linuxserver.io compose stacks. Do not rebuild an app store.
What every one of them leaves undone is *integration* — you finish setup with
five containers, five web UIs, five logins and five search boxes.
"All-encompassing" in that world means all-installed, never all-unified.

atrium is the integration layer. That is the entire differentiator. Guard it.

**Target v1:** one `docker compose up` brings up Jellyfin, Navidrome,
Audiobookshelf, Calibre-Web and atrium, pre-wired, with one search box on top
and zero API keys typed by a human. The zero-keys clause is the hard part and
also the whole value proposition.

## What atrium deliberately does not do

Do not add these. Each one has killed a project like this before.

- **Transcoding.** ffmpeg wrapped badly. Jellyfin owns this.
- **Metadata scraping.** Each backend already owns its metadata.
- **Client apps for TVs.** Roku, Fire TV, Android TV. This is the graveyard.
- **Storing or proxying media bytes.** Results carry absolute upstream URLs;
  the client streams from the source. This keeps atrium small, stateless and
  restartable.

## Three rules the design rests on

1. **A dead source must never take the search down.** Every backend gets its
   own deadline and its own error slot. A search always returns 200; the
   `degraded` flag and the `sources` array say what is missing and why. This is
   load-bearing, not decorative — home-hosted boxes go offline routinely. The
   tests in `internal/federate` and `internal/httpapi` exist to protect it.
2. **Normalization happens at the edge.** Only an adapter knows its backend's
   vocabulary. Everything past it speaks `media.Item`.
3. **Adding a backend is one file.** Implement `source.Source`, add a case to
   the switch in `internal/config`. Nothing else changes.

## Layout

```
cmd/atrium/          main, flags, graceful shutdown
internal/media/      Item, Query, Kind — the shared vocabulary
internal/source/     the Source interface, Registry, and one pkg per backend
internal/federate/   parallel fan-out, per-source deadlines, merge, rank
internal/httpapi/    handlers
internal/httpx/      shared HTTP helper (URL joining, decode, body cap)
internal/jsonc/      comment stripping for the config file
```

## Conventions

- **Zero third-party dependencies.** Standard library only. This was partly
  forced (a blocked module proxy) and partly kept on purpose: the container
  build downloads nothing and there is no supply chain to audit. Think hard
  before adding the first dep.
- **Config is JSON with comments** (`internal/jsonc`), `${VAR}` expanded from
  the environment at load, unknown keys are a hard error so typos fail at boot.
- **Fail loudly at startup, degrade gracefully at runtime.** A bad config kills
  the process. A dead backend does not.
- `gofmt` clean, `go vet` clean, tests pass. Keep it that way.

## Verify before trusting

`internal/source/audiobookshelf` and `internal/source/kiwix` are marked
`VERIFY:` in their source. Their response shapes were written against
documented behaviour, not a live server, and both have moved between upstream
releases. Results from those two are unconfirmed.

To fix a mapping, do not guess — set `"enableProbe": true` and:

```sh
curl 'http://localhost:8080/api/probe/audiobookshelf?q=test' | jq .
```

That is the literal upstream response. Adjust the structs in that one file.

Subsonic and OPDS follow stable published specs and are trusted.

## Commands

```sh
make check   # validate config + probe every source, without serving
make test
make run
docker compose up --build
```

`make check` is the fastest way to find a wrong URL or a bad token.

## Gotchas

- **Module path** is `github.com/gabehollberg/atrium`, guessed from an email
  address. If the repo lives elsewhere, fix `go.mod` and run
  `grep -rl gabehollberg/atrium . | xargs sed -i 's|gabehollberg/atrium|<you>/atrium|g'`.
- **Dev on Windows, deploy to Linux.** `go run ./cmd/atrium` natively; the
  container is for deployment. Do not add a bind-mount dev loop — there is no
  reason for one and it is slow across the Windows filesystem boundary.
- **No `go.sum`** and that is correct, not an oversight. The Dockerfile has no
  `go mod download` step for the same reason.
- **Subsonic stream URLs carry credentials in the query string.** That is the
  protocol, not a bug. It does mean atrium must not be served over plain HTTP
  on an open network.
