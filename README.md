# atrium

One search across every media server you run.

atrium sits in front of Navidrome, Audiobookshelf, Calibre, Jellyfin and
kiwix-serve and makes them answer a single query. It returns one merged,
ranked list where a song, an audiobook, an epub, a film and a Wikipedia
article all have the same shape.

## What it deliberately does not do

It does not transcode, scrape metadata, manage libraries or store media.
Those are solved problems owned by the servers it sits in front of, and
solving them again badly is how this kind of project dies. atrium is the part
that does not exist yet: the federating layer above them.

## Why

Self-hosting a media library means running several servers, because each one
is best at exactly one thing. The cost is that finding something means
remembering which server owns it and searching there. atrium removes that
step without replacing any of the servers.

## Design

```
                         ┌──────────┐
   GET /api/search?q= ──▶│  atrium  │
                         └────┬─────┘
             fan out, in parallel, each with its own deadline
        ┌────────────┬────────┼─────────┬────────────┐
        ▼            ▼        ▼         ▼            ▼
   Navidrome   Audiobookshelf  Calibre  Jellyfin   kiwix-serve
   (Subsonic)    (REST)        (OPDS)   (REST)     (OpenSearch)
        │            │            │        │            │
        └────────────┴────────┬───┴────────┴────────────┘
                     normalize ▼ merge, rank
                      one list of media.Item
```

Three rules hold the design together.

**A dead source must never take the search down.** Every backend gets its own
deadline and its own error slot. If the music server is unreachable you still
get your books, and the response says which source failed and why. This is
not a theoretical concern: a box on a home connection goes away when a router
is replaced, an IP changes, or the internet is on a schedule.

**Normalization happens at the edge.** Each adapter is the only code that
knows its backend's vocabulary. Everything past the adapter speaks
`media.Item`. Adding a media server means writing one file.

**atrium does not proxy media bytes.** Results carry absolute URLs pointing at
the upstream server. The client streams from the source directly, so atrium
stays a small, stateless, low-traffic service you can restart without anyone
noticing.

## Endpoints

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/healthz` | Liveness. Makes no upstream calls. |
| GET | `/api/sources` | Per-source health, with timings. |
| GET | `/api/search?q=&kind=&limit=` | Federated search. |
| GET | `/api/probe/{id}?q=` | A source's raw upstream response. Off by default. |

`kind` is one of `music`, `audiobook`, `ebook`, `video`, `article`, and may
repeat or be comma-separated. Filtering by kind skips non-matching backends
entirely rather than querying and discarding.

A search always returns 200. Check `degraded` and the `sources` array to see
whether the answer is complete.

```json
{
  "items": [
    { "id": "300", "sourceId": "navidrome", "kind": "music",
      "title": "Sleep Walk", "creators": ["Santo & Johnny"],
      "year": 1959, "durationSeconds": 141, "score": 1,
      "openUrl": "https://music.example.com/rest/stream.view?id=300&..." }
  ],
  "sources": [
    { "sourceId": "navidrome", "kind": "music", "ok": true, "count": 1, "tookMs": 12 },
    { "sourceId": "kiwix", "kind": "article", "ok": false,
      "error": "dial tcp: connection refused", "tookMs": 0 }
  ],
  "degraded": true,
  "tookMs": 13
}
```

## Getting started

```sh
cp configs/atrium.example.json configs/atrium.json
cp .env.example .env
# edit both, then:
make check     # validate config and probe every source, without serving
make run
```

`make check` is the fastest way to find a wrong URL, a bad token or a library
id you guessed. It reports each source individually and exits non-zero if any
fail.

With Docker:

```sh
docker compose up --build
```

## Configuration

The config file is JSON with comments, so it can be annotated in place. Any
`${VAR}` is expanded from the environment at load time, which keeps secrets
out of the file. Unknown keys are a hard error, so a typo fails at boot
instead of being silently ignored.

See `configs/atrium.example.json` for every field, commented.

## Adding a source

Implement `source.Source` in `internal/source/<name>/`:

```go
type Source interface {
    ID() string
    Kind() media.Kind
    Search(ctx context.Context, q media.Query) ([]media.Item, error)
    Health(ctx context.Context) error
}
```

Then add a case to the switch in `internal/config/config.go`. Nothing else
changes. Implementing the optional `source.Prober` also gets you a
`/api/probe/<id>` endpoint for free.

## Correcting a field mapping

The Subsonic and OPDS adapters follow published, stable specs. The
Audiobookshelf and Kiwix adapters target response shapes that have moved
between releases, and are marked `VERIFY:` in their source.

If a source returns results with missing titles or covers, do not guess:

```sh
# set "enableProbe": true in the config first
curl 'http://localhost:8080/api/probe/audiobookshelf?q=test' | jq .
```

That is the literal upstream response. Adjust the structs in that adapter to
match. Only that one file needs to change.

## Testing

```sh
make test
```

The suite covers the behaviour that matters rather than the plumbing: that a
failed source degrades instead of erroring, that a hung source is cut off at
its deadline, that kind filtering skips backends, that relevance ordering is
sane, that the Subsonic adapter never puts a plaintext password on the wire,
and that a malformed config fails loudly.

## Notes

- **Module path.** `go.mod` says `github.com/gabehollberg/atrium`. If your
  repo lives elsewhere, change it there and run
  `grep -rl gabehollberg/atrium . | xargs sed -i 's|gabehollberg/atrium|<you>/atrium|g'`.
- **No dependencies.** The standard library only. The container downloads
  nothing at build time and there is no supply chain to audit.
- **Exposure.** Subsonic stream URLs carry credentials in the query string;
  that is how the protocol works. Serve atrium over TLS or a private network
  (Tailscale), not on the open internet.
