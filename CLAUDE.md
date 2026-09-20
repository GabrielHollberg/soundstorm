# SoundStorm — working notes for Claude

Read this first. It carries decisions that the code cannot tell you, including
two reversals of earlier decisions that looked right and were not.

## What this is

A unified front end for a self-hosted media library. One login, one search box,
one player over Navidrome (music), Jellyfin (films and TV), Audiobookshelf
(audiobooks) and a plain folder of EPUBs (ebooks).

The user's words for what they wanted: *"an all-encompassing server that can do
movies, audiobooks, ebooks, music all together... easy for users to install and
then create a login... and put their library into organized folders."*

## The decision that shapes everything

That request sounds like "build a media server". It is not, and the difference
is the whole project.

**Rejected: fork Jellyfin, or build a media server from scratch.** Either means
owning the expensive part — scanning disks, identifying media, storing metadata
and artwork, transcoding. That is years of work to arrive somewhere worse than
what exists. Forking additionally buys a permanent merge tax against a large
C# codebase, in exchange for the ability to change internals that none of the
requirements touch.

**Chosen: own the layer above.** Each backend owns exactly one media type and
one folder. Nothing scans the same folder twice. SoundStorm owns login, search,
playback and the bytes. From the user's seat this is indistinguishable from a
single server — they never learn Jellyfin exists — but Jellyfin still does the
transcoding and Navidrome still does the music scanning.

Navidrome rather than Jellyfin for music specifically: multi-value artist tags,
album-artist vs artist, compilations, ReplayGain, smart playlists, fast scanner.
Jellyfin's music support is a second-class citizen next to its video support,
structurally. Getting Navidrome's music engine *for free* is exactly what the
fork would have made you rebuild by hand.

**If the seams leak, the whole thing is pointless.** Guard this. A result that
links out to a backend's web UI means a second login and a visibly different
app, and at that point you have built a bookmark folder.

## Two reversals from the first design

The original code was an API-only federating gateway. Its instincts were good
and two of them were wrong for this product:

1. **"Never proxy media bytes; results carry absolute upstream URLs."** An
   upstream URL only works if the browser can reach the upstream, which means
   publishing Jellyfin and Navidrome on their own ports, which means their login
   screens are one URL away. You cannot have "one login" and "never touch the
   bytes" at once. SoundStorm is now in the data path. See `internal/stream`.
2. **"Stateless, restartable, no config writes."** Zero-keys provisioning means
   SoundStorm generates credentials, so it must remember them. One login means a
   user and sessions. Both outlive a restart. See `internal/state` — note that
   it holds *only* credentials and the account, never anything about the media.

## What SoundStorm deliberately does not do

Each of these has killed a project like this before.

- **Transcoding.** Jellyfin owns this.
- **Metadata scraping.** Each backend already owns its metadata.
- **Library scanning.** Same.
- **Client apps for TVs.** The graveyard.
- **Rebuilding an app store.** Installation is solved and crowded.

## Three rules the design rests on

1. **A dead backend must never take the search down.** Per-source deadline,
   per-source error slot, always 200, `degraded` says what is missing. Tests in
   `internal/federate` and `internal/httpapi` protect this.
2. **Normalization happens at the edge.** Only an adapter knows its backend's
   vocabulary. Everything past it speaks `media.Item`.
3. **A backend is two halves: search and provisioning.** A backend a human must
   configure by hand defeats the point. Both halves or it is not done.

## Why Jellyfin, specifically

Asked and answered rather than assumed. Plex requires a plex.tv account, so it
cannot be provisioned without a human logging into a cloud service - which is
fatal to the zero-keys claim, not merely inconvenient. Emby went closed-source.
Everything else in that space is too thin to bet a stack on.

The rule in the next section predicts it independently: video is not
self-describing and does need transcoding, so it stays delegated. That is the
same rule that said drop Calibre-Web, so it is cutting both ways.

The honest cost is API churn (Jellyfin 12 broke two documented auth methods
under us) and a 2.5GB image next to Navidrome's 348MB. That trade only clearly
pays off once SoundStorm uses Jellyfin's transcoding, which it still does not.

## One backend, two sources

Films and series are separate Jellyfin libraries - different collection types,
different scrapers, seasons and episodes only for the latter - so Jellyfin is
provisioned once and registered as two sources sharing one token: `jellyfin`
(Movie) and `jellyfin-tv` (Series,Episode).

`source.Source` always said "a server that serves two kinds is configured as two
sources", and that was not actually possible until `jellyfin.Config` grew a
Kind and ItemTypes; the adapter hardcoded `KindVideo`. `provision.buildSources`
now returns a slice for this reason.

## The folders are the interface

Installing SoundStorm creates `library/` with four subfolders, and the app's first
screen is a guide to them rather than an empty search grid. This is deliberate
product surface, not convenience: the folders are the only part of SoundStorm a user
interacts with that has no UI, so they are created for you, named for what
people call the thing ("movies", not "video"), and described in the app. A
README nobody reads is not an interface.

`internal/library` owns this. It reads the directory for exactly two reasons -
creating it, and counting files so the UI can distinguish "you have not added
anything" from "a scan is still running". It does not index. Do not let it grow
into an indexer.

One asymmetry worth knowing: only `localbooks` reports an indexed count, so the
music, movie and audiobook rows show files-on-disk with no comparison. Navidrome
and Jellyfin would each need a count call to fix that.

## The one media type SoundStorm owns, and why that is not a slippery slope

Ebooks have no backend. Calibre-Web was removed: it needed a *database* rather
than a folder, its setup had no API (CSRF form-scraping), and its password could
not be rotated. SoundStorm reads the folder itself.

The justification is narrow and should stay narrow: **an EPUB is
self-describing.** The file contains its own title, author, language and cover
in a documented XML format, and a book needs no transcoding. `Dune.2021.mkv`
contains none of that, which is precisely the work Jellyfin exists to do.

So the line is: SoundStorm can own a media type when it is self-describing and needs
no transcoding. EPUB qualifies. Video never will. Do not cite `internal/epub` as
precedent for scanning anything else.

Existing Calibre libraries still work, with no SQLite driver: Calibre writes a
`metadata.opf` sidecar beside every book in exactly the format an EPUB carries
internally, so one parser reads both.

## The reader

Rendering is foliate-js (MIT), vendored under `internal/webui/assets/vendor/`.
Writing it ourselves was considered and rejected for the same reason as
transcoding: `paginator.js` is 44KB and `epubcfi.js` is 13KB, and those are the
two genuinely hard parts. Reflowable text means a page number is meaningless -
change the font size and "page 47" is different words - so position has to be a
content-anchored locator (an EPUB CFI).

This is the only third-party code in the project. It does not break the
zero-dependency rule's intent: the files are checked in, pinned by content,
embedded via go:embed, and fetched at no point during a build. The Go module
still has no dependencies and no go.sum.

What is ours: SoundStorm unzips server-side (`/api/book/resource`), so no zip
library runs in the browser, and it remembers reading position across devices.

## Verified against live servers

These were checked on a running stack, not inferred. Re-verify if versions move.

- **Jellyfin 12.1.0 rejects `X-Emby-Token` and `?api_key=` with 401.** The only
  form it accepts is `Authorization: MediaBrowser ... Token="..."`. Most docs
  and every older client still show the other two. This is why
  `source.Target` carries headers rather than just a URL.
- **Audiobookshelf 2.36.1 `/login` returns two tokens.** `user.accessToken`
  carries an `exp` one hour out; `user.token` is a legacy JWT with no `exp` at
  all. SoundStorm stores the legacy one on purpose - the other would strand the
  backend an hour after provisioning without a refresh flow. If a release drops
  it, that is where the refresh dance goes.
- **Audiobookshelf addresses audio by inode, not item id.** `/api/items/{id}`
  has to be fetched to learn it, which is why `source.Streamer` takes a context.
- **foliate-js `view.open(book)` renders nothing on its own.** You must call
  `view.init({ lastLocation })` afterwards; that is what paints the first page
  or resumes. Miss it and you get a blank reader with no error anywhere.
- **foliate-view's shadow root is `mode: 'closed'`.** A browser test cannot
  reach inside it - observe rendering through the `relocate` event instead.
- **foliate-js probes for optional files** (`META-INF/encryption.xml`, Apple and
  Kobo display options). 404 is the correct answer; those requests log at debug.
- **Calibre-Web was removed** (see above). `internal/source/opds` and
  `internal/provision/calibreweb.go` remain as an opt-in for a Calibre server
  running elsewhere, via `SOUNDSTORM_CALIBREWEB_URL`.
- **Jellyfin's startup wizard is a plain REST API** (`/Startup/Configuration`,
  `/Startup/User`, `/Startup/RemoteAccess`, `/Startup/Complete`) and stops
  accepting calls once setup completes, which makes driving it safe.
- **Navidrome's first-run admin form POSTs to `/auth/createAdmin`** and that
  endpoint only works while no user exists. Same safety property.
- All four need anywhere from seconds to a minute after container start, so
  provisioning retries with backoff in the background while SoundStorm serves.

## Naming

The product is **SoundStorm**; every identifier is **soundstorm**. Module path,
binary, container names, the `SOUNDSTORM_` env prefix, the session cookie, the
account created on each backend, the client identity sent to Jellyfin and
Subsonic - all lowercase. Prose, the page title, the wordmark and anything a
person reads use the brand casing. `TestUIShellIsServed` asserts the shell
carries the display form, which is the one place the distinction is checked.

It was called atrium until the rename. Nothing in the repo should say so.

## Testing against real media

`scripts/fetch-test-library.ps1` builds a library from Project Gutenberg,
LibriVox, the Internet Archive etree collection and the Blender open movies -
all public domain or CC, all resumable, about 750MB by default.

It exists because synthetic files cannot find a whole class of bug: every one of
them has exactly the metadata we chose to write. The first run against real
files found three, none of which thirteen generated files could have surfaced.
Run it before believing anything about how this behaves in the wild.

**These are donated services. Do not hammer them.** Project Gutenberg states
plainly that its website "is intended for human users only" and that automated
access "will result in a temporary or permanent block of your IP address"; the
sanctioned routes are the mirrors and /robot/harvest, throttled. The script uses
gutenberg.pglaf.org at two second intervals for that reason, and the mirror
serves byte-identical files. Check the equivalent policy before adding a source,
and never point automated fetches at an origin site that has asked you not to.

## Gotchas

- **Provisioning is not idempotent across a volume reset.** If a backend's
  volume is wiped but SoundStorm's state survives (or vice versa), you get a
  backend with an account whose password nobody holds. The provisioners detect
  this and say so rather than retrying forever. The fix is a human decision.
- **Reconnecting is not provisioning, and conflating them destroys credentials.**
  On restart SoundStorm beats the backends to listening. A failed health check then
  looks like "wrong password" unless you check what kind of failure it was, and
  re-provisioning an already-configured backend can never succeed - so one
  unlucky restart used to brick a backend with its working token still on disk.
  Only 401/403 means the credentials are wrong; 5xx, 429, dial errors and
  timeouts all mean wait. `transient()` in `internal/provision` owns that
  distinction and `provision_test.go` guards it. Jellyfin in particular accepts
  the connection while loading and answers 503.
- **Port 8099, not 8080.** A Calibre content server on this machine already
  holds 8080. `SOUNDSTORM_PORT` overrides.
- **Playback is negotiated, never assumed.** `StreamTarget` POSTs a device
  profile to Jellyfin's `/Items/{id}/PlaybackInfo` and does what it is told.
  Whether a file needs transcoding depends on container, video codec, audio
  codec, profile AND level; Jellyfin knows all five and we know none of them.
- **The device profile errs conservative on purpose.** Claiming a codec we
  cannot decode is the silent failure - Jellyfin hands over the original, the
  video element shows nothing, no error anywhere. Claiming too little only
  costs an unnecessary transcode. So: h264/aac in mp4, VPx/AV1 in webm, and
  everything else transcodes.
- **Transcoding is served as HLS, and the reason is seeking.** A progressive
  transcode has no length until it has finished encoding, so the browser
  reported `seekable.end` of 0, a 20s file showed a duration of 9.9s that grew
  as it buffered, and seeking to 15s snapped back to 3s. Jellyfin's HLS output
  is a VOD playlist listing every segment up front, which gives a real timeline:
  the same file now reports 20.0s, `seekable.end` 20.0, and seeking to 15s
  lands at 16.9s.
- **Jellyfin's playlists reference their children relatively** ("main.m3u8",
  "hls1/main/0.ts"), which is why `/api/hls/{source}/{path...}` mirrors
  Jellyfin's own `/videos/` namespace. Get that mapping right and the browser
  resolves every segment onto SoundStorm by itself; get it wrong and the only
  alternative is rewriting playlists.
- **Jellyfin's direct-play answer is not trusted on its own.** 12.1.0 reports
  `SupportsDirectPlay: true` for an MKV even when the device profile offers only
  mp4, so the container is checked a second time against the same list the
  profile advertises. Believing it reproduces the silent failure exactly.
- **Subtitle URLs are built, not taken from PlaybackInfo.** 12.1.0 leaves
  `DeliveryUrl` and `DeliveryMethod` empty even when the profile declares
  subtitle support, but `/Videos/{item}/{mediaSource}/Subtitles/{index}/Stream.vtt`
  works for embedded and sidecar tracks alike and converts SRT on the fly.
  Same pattern as HLS: read the info, construct the URL.
- **Only text subtitles are offered.** PGS, VOBSUB and DVB are pictures of
  text; attaching one as a track renders nothing at all, which is worse than
  offering nothing. Burning them in would need a re-encode.
- **No `crossorigin` on the video element.** Every media and subtitle URL is
  same-origin, so the cookie goes anyway; setting the attribute forces CORS
  semantics and the media then fails to load for want of headers nobody needs
  to send. Cost half an hour once.
- **hls.js is loaded lazily and may never load at all.** Recent Chrome plays
  HLS natively, so the fallback went unexercised on the first test run - forcing
  it (stub `canPlayType` to reject mpegurl) is the only way to know it works.
  Do that whenever the video path changes.
- **Transcodes must be stopped explicitly.** A Jellyfin transcode is an ffmpeg
  process that outlives the HTTP request, so `source.Target.OnDone` fires
  `DELETE /Videos/ActiveEncodings`. Verified that endpoint exists: it answers
  204, where a made-up path answers 404.
- **EPUB only.** The library scan ignores every other book format, and the
  reader only has foliate-js's EPUB modules vendored.
- **A Content-Security-Policy on the shell is load-bearing, not hardening.**
  EPUBs can contain scripts, and the reader renders book content in a blob:
  iframe that inherits SoundStorm's origin - without `script-src 'self'`, opening a
  book would run a stranger's JavaScript against the session cookie. `blob:` IS
  allowed in style-src and font-src, or books render unstyled.
- **Audiobooks play their first file only.** Multi-file books need the playback
  session API and a player that understands a track list.
- **PowerShell here-strings carry CRLF into `docker exec bash -c`**, and a
  trailing carriage return makes bash misread the command. `scripts/` passes
  single-line commands for that reason.
- **Library folders are created 0777 on purpose.** They exist to be written
  into by a person on the host whose uid SoundStorm cannot know, and read by four
  backends running as assorted other uids. Being unable to copy files into your
  own media folder is a far worse failure than a permissive mode on a home
  media directory. Revisit if SoundStorm ever grows PUID/PGID support.
- **The library skeleton is committed** (`library/*/README.txt`), so a fresh
  clone already has somewhere to put media; `.gitignore` keeps the structure and
  ignores the contents.
- **`scripts/mkepub` generates test books.** The sample-media script used to
  borrow Calibre's `ebook-convert` from the Calibre-Web container, which no
  longer exists. An EPUB is a zip with two XML files, so producing one needs
  neither Calibre nor a container.
- **The rename invalidated every provisioned volume.** The account SoundStorm
  creates on each backend is named after it, and the state file moved from
  /var/lib/atrium to /var/lib/soundstorm - so an install from before the rename
  finds backends already set up with credentials it does not hold, which is the
  one failure the provisioners cannot recover from on their own. The fix is
  `docker compose down -v` and a fresh provision. Worth remembering if the
  project is ever renamed again.
- **Navidrome's setting is `ND_SCANINTERVAL`, not `ND_SCANSCHEDULE`.** It
  ignores unknown keys silently, so the wrong name looks like it works while no
  periodic scan ever runs and new music appears only on restart. Verified
  against 0.64.0. Check `--help` in the container before trusting any of these
  env names.
- **Dev on Windows, deploy to Linux.** Go lives at `C:\dev\tools\go` (installed
  from the zip, on the user PATH). Docker Desktop must be running.
- **No `go.sum`** and that is correct. Zero third-party dependencies, including
  password hashing — `crypto/pbkdf2` has been stdlib since Go 1.24.
- **Screenshots in `docs/shots/`** were captured with Playwright driving the
  system Chrome (`channel: 'chrome'`, no browser download). Note that Node on
  Windows does not resolve MSYS-style `/c/...` paths — pass `C:\...`.

## Commands

```sh
go test ./...
docker compose up --build
docker compose logs -f soundstorm          # watch provisioning
docker compose down -v                 # reset everything, including credentials
pwsh scripts/make-sample-media.ps1     # synthetic library, no downloads
```

`docker compose logs -f SoundStorm` is the fastest way to see why a backend is not
answering.
