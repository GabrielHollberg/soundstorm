# SoundStorm

**One login and one search box over your whole media library.** Films, music,
audiobooks and ebooks, all answering the same search and playing in the same
window. No API keys, no second login, nothing to configure.

## Install

### 🪟&nbsp; Windows

**[⬇ Download SoundStorm-Setup.cmd](https://github.com/GabrielHollberg/soundstorm/releases/latest/download/SoundStorm-Setup.cmd)**, then:

1. **Right-click the downloaded file → Properties**
2. Tick **Unblock** at the bottom → **OK**
3. **Double-click it**

It installs Docker for you if you do not have it, starts it if it is not
running, and leaves a SoundStorm icon on your desktop.

> **Why the Unblock step?** Windows Smart App Control refuses to run *any*
> script downloaded from the web — you get "An Application Control policy has
> blocked this file" with no way to continue. That is about the file extension,
> not about SoundStorm, and unblocking is how Windows expects you to say you
> trust it. Skipping it is the number one reason nothing happens when you
> double-click.

<details>
<summary>Rather paste a command than click through Properties?</summary>

Open PowerShell and paste this. It saves the installer and runs it — two
steps on purpose, because piping a downloaded script straight into PowerShell
is the pattern Windows Defender blocks as malware:

```powershell
irm https://raw.githubusercontent.com/GabrielHollberg/soundstorm/main/install.ps1 -OutFile "$env:TEMP\soundstorm.ps1"
powershell -ExecutionPolicy Bypass -File "$env:TEMP\soundstorm.ps1"
```

</details>

### 🐧&nbsp; Linux &nbsp;·&nbsp; 🍎&nbsp; macOS

```sh
curl -fsSL https://raw.githubusercontent.com/GabrielHollberg/soundstorm/main/install.sh | sh
```

Needs [Docker](https://docs.docker.com/engine/install/) already installed.

---

**Either way**, it downloads about 3GB of media servers, sets them all up, and
opens your browser. Pick a username and password on the first screen and you
are in.

![One search returning an ebook, a film, music and an audiobook in a single ranked list](docs/shots/2-search.png)

<details>
<summary>Other ways to install it</summary>

**By hand, anywhere.** The installer is a convenience, not a requirement — it
downloads one file and runs one command, and so can you:

```sh
mkdir soundstorm && cd soundstorm
curl -fsSL https://raw.githubusercontent.com/GabrielHollberg/soundstorm/main/docker-compose.yml -o docker-compose.yml
docker compose up -d
```

Then open <http://localhost:8099>. To use a different port, put
`SOUNDSTORM_PORT=9000` in a `.env` file beside the compose file.

</details>

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
| **Ebooks** | SoundStorm itself — EPUB and PDF, read in the browser |

Using the real servers instead of reimplementing them is the whole trick. When
you search "dune" and get a film back with a real poster and a real synopsis,
that is Jellyfin's metadata work, not SoundStorm's.

## Status

This is a **working vertical slice**, not a finished product. What runs today:

- one command installs it — published multi-arch images, no build step, no
  repository to clone
- SoundStorm provisions every one of them on first boot — **zero API keys typed**
- **drag and drop**: files and folders dropped on the window are sorted into the
  right library automatically
- **accounts**: the first visit creates the owner, who adds everyone else; each
  person keeps their own place in every book and sees only the libraries they
  are given
- **HTTPS** on request, with a local certificate authority so there is one
  install per device and no warning afterwards
- one search across all four, merged and ranked
- music, films, TV and audiobooks play **inside SoundStorm**
- video a browser cannot decode is **transcoded by Jellyfin on the fly** and
  served as HLS, so HEVC, MKV and DTS play *and seek* like anything else
- **subtitles**, embedded or sidecar, converted to WebVTT and selectable
- ebooks are **read inside SoundStorm**, and remember where you stopped
- no backend publishes a port; SoundStorm is the only door

See [docs/roadmap.md](docs/roadmap.md) for what is not built yet.

## Using it

Everything below is optional. A fresh install already works and already has
something in it to search.

### Where your media goes

**Drag it onto the window.** Anywhere — there is nothing to aim at.
SoundStorm works out what each file is and files it for you: a folder keeps its
structure, subtitles and artwork travel with their film, and anything it cannot
place is listed with the reason rather than dumped somewhere.

![Dragging files onto the window](docs/shots/15-drop.png)

The app opens on your library rather than on a form, so there is nothing to
go looking for. One line under the filters says so, and carries the two
other things worth having to hand: **choose files**, for a phone or anything
else that cannot drag, and the address to give everybody else in the house.

![The screen a new install opens on](docs/shots/1-library.png)

**Or put the files in the folders yourself**, which is the better route for a
whole drive copied over the network. SoundStorm makes a `library/` folder next
to the compose file:

```
library/
  music/       Talk Talk/Laughing Stock/01 Myrrhman.flac
  movies/      Arrival (2016)/Arrival (2016).mkv
  tv/          Severance (2022)/Season 01/Severance - S01E01.mkv
  audiobooks/  Ursula K. Le Guin/A Wizard of Earthsea/book.m4b
  ebooks/      A Wizard of Earthsea.epub, Some Paper - Author (2017).pdf
```

Nothing to import, no library to configure.

**When it genuinely cannot tell, it asks.** An mp3 is a song or a chapter of an
audiobook and nothing in the file says which, so it asks — once for the whole
folder, not once per chapter.

![Asking which library a folder of mp3s belongs in](docs/shots/17-ask.png)

Anything you drop on the window is searchable within a few seconds — SoundStorm
tells whichever server owns that shelf to look, rather than leaving the file
sitting there until its next sweep.

Files you copy into the folders yourself are found on the next sweep instead:
every minute for music, every two for ebooks, and as the watchers notice for
films and audiobooks. Until then the app says "indexing…" rather than
pretending the file is not there.

It arrives with a small library already in place — ten classics from Project
Gutenberg, Bach's Goldberg Variations, and *As a Man Thinketh* as both an ebook
and an audiobook — so there is something to search the moment it starts.
Bundled in the binary rather than downloaded, so the first run works with no
network and nobody's bandwidth but yours is involved. Delete them whenever you
like; they are ordinary files.

`library/ebooks` is a plain folder of `.epub` and `.pdf` files. It can also be
an existing Calibre library — SoundStorm reads Calibre's `metadata.opf`
sidecars, so a library you already curate keeps its series, tags and corrected
authors, with no SQLite driver and no Calibre-Web container.

### Using it from your phone, TV or another computer

It already works — nothing to enable. Use the server computer's address, on the
same port:

```
http://192.168.1.50:8099        <- your number will differ
```

`https://` instead, if you turned on [HTTPS](#turning-on-https) — that is the
one part of the address you cannot guess, so the installer prints the scheme it
actually set rather than leaving you to try both. To find the address again:

| | |
| --- | --- |
| Windows | `ipconfig` — the IPv4 Address of your main adapter |
| macOS | `ipconfig getifaddr en0` |
| Linux | `hostname -I` |

Same account, same library, same everything. Type it once per device and then
**add it to the home screen** — nobody types their media server address twice.

SoundStorm is a progressive web app, so that gives you a real app rather than a
bookmark: its own icon, its own window, and no address bar eating the top of
the screen. **iPhone:** Share → Add to Home Screen. **Android:** Chrome's menu
→ Install app.

> **Android needs a certificate it trusts**, not merely https. SoundStorm's
> own certificate is signed by an authority only this install knows about, so
> Chrome reports `ERR_CERT_AUTHORITY_INVALID`, treats the origin as having a
> certificate error, and refuses to register a service worker there — which
> is what "Install app" depends on. Clicking through the warning lets you
> browse, but does not fix that.
>
> Two ways round it, in order of how pleasant they are:
>
> 1. **[Tailscale](#reaching-it-from-outside-the-house)**, which serves
> SoundStorm on a `ts.net` address with a real Let's Encrypt certificate.
> Installs cleanly, no warning, and works away from home as a bonus.
> 2. **Install SoundStorm's certificate authority on the phone**: open
> `https://<server>:8099/ca.crt`, then Settings → Security → Encryption &
> credentials → Install a certificate → CA certificate. Android will warn
> that the network may be monitored; that warning is about user-installed
> authorities in general, not about this one in particular.
>
> **iPhone adds it to the home screen either way**, because Safari's Add to
> Home Screen does not go through a service worker at all. Nothing else about
> SoundStorm depends on any of this.

Two things worth doing:

- **Give the server a fixed address** in your router (a DHCP reservation), or
  that number will change one day and every bookmark breaks.
- **If nothing loads at all**, the firewall is blocking it. On Windows, set the
  network to **Private** in Settings → Network & Internet, and allow SoundStorm
  through.

> **On macOS and Linux** the installer offers `http://<hostname>.local:8099`
> instead, which survives the address changing. That is not offered on Windows:
> Windows does not reliably advertise its name over mDNS, so the name resolves
> on the server itself and nowhere else — which is a worse thing to be handed
> than a number.

**From outside the house** is a different question, and the answer is not
"forward a port". Run SoundStorm on a [Tailscale](https://tailscale.com)
tailnet instead — nothing is exposed to the internet, and there is no port
forwarding at all:

**Windows:**

```powershell
& "$env:USERPROFILE\SoundStorm\soundstorm.ps1" -Tailscale
```

**Linux / macOS:**

```sh
curl -fsSL https://raw.githubusercontent.com/GabrielHollberg/soundstorm/main/install.sh | sh -s -- --tailscale --auth-key tskey-...
```

It asks for a Tailscale auth key, starts a Tailscale container beside
SoundStorm, and prints the address it lands on — something like
`https://soundstorm.your-tailnet.ts.net`. That address has a **real
certificate**, so unlike the LAN one it shows no browser warning.

Three things it cannot do for you, and there is no way around any of them:

1. **A Tailscale account.** Free for personal use — unlimited devices, up to
   six people — but somebody has to sign up.
2. **An auth key**, generated in their admin console under Settings → Keys.
3. **The Tailscale app on every device** that should reach SoundStorm,
   signed into the same account. A phone without it sees nothing.

SoundStorm works exactly the same with none of this. `--no-tailscale` turns
it off again, and nothing else changes.

### Updating

**Windows:** Start menu → **Update SoundStorm**.

**Everywhere:** run the installer again — the same command you installed with.
It pulls the newer images, restarts, and leaves your library, your accounts and
your settings alone.

<details>
<summary>By hand</summary>

```sh
cd soundstorm
docker compose pull && docker compose up -d
```

</details>

### Backing it up

One file holds your accounts and the passwords SoundStorm invented for
Navidrome, Jellyfin and Audiobookshelf. **Those passwords exist nowhere else.**
Lose that file and the media servers keep running with accounts nobody can
sign in to — and reinstalling does not help, because they are already set up.

From the install folder:

```sh
docker compose run --rm -v "$PWD:/backup" soundstorm backup /backup/soundstorm-backup.json
```

Keep the result somewhere that is not this machine. It is worth as much as the
server: anyone holding it holds every backend password.

To put it back — on a new machine, or after a `docker compose down -v`:

```sh
docker compose run --rm -v "$PWD:/backup" soundstorm restore /backup/soundstorm-backup.json
docker compose up -d
```

Restoring refuses anything that is not a SoundStorm backup, and keeps whatever
it replaced as `state.json.bak`, so restoring the wrong file is undoable too.
Every ordinary write already leaves a `.bak` beside the state, which covers a
bad write but not a deleted volume — that is what this is for.

**Uninstalling saves one automatically** into the install folder before it
removes anything, and leaves it behind when it cleans up.

### Forgotten your password

Signup closes for good once the first account exists, so there is no “register again” to fall back on. From the install folder:

```sh
docker compose down
docker compose run --rm soundstorm reset-password
docker compose up -d
```

It prints a new password for the account and you change it under **Account**
once you are in. With more than one account, add the name:
`reset-password gabe`.

Stopping first is not optional — a running SoundStorm keeps the state in
memory and writes its own copy back, which would quietly undo the reset.
Nothing else is touched: your media, your libraries and everyone else's
accounts all survive, and devices already signed in stay signed in.

### Removing it

**Windows:** Settings → Apps → **SoundStorm** → Uninstall, like any other
program.

**Linux / macOS:**

```sh
curl -fsSL https://raw.githubusercontent.com/GabrielHollberg/soundstorm/main/install.sh | sh -s -- --uninstall
```

Either one stops the servers and deletes their data — accounts, and the
databases Jellyfin and Navidrome built. **Your media is never touched.** The
`library` folder is left exactly where it was, and the uninstaller tells you
where, so you can delete it yourself if you want to.

Docker is left installed, since other things may be using it.

### Other things

```sh
cd soundstorm
docker compose logs -f        # what is it doing
docker compose down           # stop it; nothing is lost
docker compose up -d          # start it again
```

### Giving other people a login

The first account is the owner. From **Account → People** the owner adds
everyone else: a name and a password, and that is the whole ceremony. There is
no open registration and no invite link, deliberately — a server that might be
reachable from outside a house should not let a stranger create an account.

Everybody keeps their own **place in every book**, both for reading and for
listening. The audiobook side of that is real per-person state on the backend,
not a note in a file: SoundStorm quietly gives each person their own
Audiobookshelf account, and removing them takes it away again along with their
sessions and bookmarks.

**Each person sees only the shelves you tick.** Music, Films, TV, Audiobooks,
Ebooks — untick Films for a child account and the tab disappears, the folder
stops being listed, search stops returning films, and the film itself returns
404 if anybody goes looking for the URL. The last one is the part that matters:
hiding results is not a permission.

![Ticking which libraries each person can see](docs/shots/14-libraries.png)

What this is not: per-title or age-rating filtering. The unit is a whole
library, so "no films for the seven-year-old" is answerable and "only these
films" is not.

### Turning on HTTPS

Off by default, because on `localhost` there is nothing on the wire to protect
and a certificate warning is a poor first screen. The moment another machine can
reach it, turn it on by running the installer again with one extra word:

**Windows** — paste this into PowerShell from anywhere:

```powershell
& "$env:USERPROFILE\SoundStorm\soundstorm.ps1" -Https
```

**Linux / macOS:**

```sh
curl -fsSL https://raw.githubusercontent.com/GabrielHollberg/soundstorm/main/install.sh | sh -s -- --https
```

Either one edits one line of your `.env`, restarts, and — this is the part worth
having — prints the `https://` addresses for *this* install, so you are not
guessing at a scheme. `-NoHttps` and `--no-https` put it back.

Behind that flag, SoundStorm runs its own certificate authority. Every device
warns on the first visit, because nothing outside your house can vouch for an
address like `192.168.1.50`. To stop it asking: visit
`https://<server>:8099/ca.crt`, install that file once per device, and there is
no warning again. Nothing to renew.

<details>
<summary>By hand, if you would rather</summary>

Put this in the `.env` file beside your `docker-compose.yml` and run
`docker compose up -d`:

```sh
SOUNDSTORM_TLS=self-signed
```

</details>

The installer records this machine's LAN address in the `.env` file as
`SOUNDSTORM_TLS_HOSTS`, which is the list of addresses the certificate covers
— the server is in a container and cannot work that out for itself. If the
machine's address changes, or you reach it by a name your router hands out,
add it:

```sh
SOUNDSTORM_TLS_HOSTS=192.168.1.50,media.lan
```

then `docker compose up -d`. Hostnames you connect to are also picked up
automatically; only bare IP addresses have to be listed, because browsers send
no name when you type one.

Already have a real certificate? `SOUNDSTORM_TLS=file` with
`SOUNDSTORM_TLS_CERT` and `SOUNDSTORM_TLS_KEY`. Behind a reverse proxy that
terminates TLS for you? Leave TLS off and set `SOUNDSTORM_TRUST_PROXY=true` so
the session cookie is marked Secure.

**Still true:** SoundStorm has not been audited, and putting any self-hosted
server directly on the open internet is a decision worth making deliberately. A
VPN such as [Tailscale](https://tailscale.com) remains the easiest safe answer.

### Windows, in more detail

Windows is a first-class target — this project is developed and tested on it.
Docker Desktop runs the same Linux images there that it runs everywhere else,
and the setup file handles the parts people get stuck on:

| | |
| --- | --- |
| Docker not installed | installs it with `winget`, no website visit |
| Docker installed but not running | starts it and waits for it |
| Port 8099 already taken | quietly uses the next free one |
| Remembering the address | desktop and Start Menu shortcuts |
| Turning the PC on | starts by itself |

What is *not* supported is running the whole stack natively without Docker.
SoundStorm's own binary does run natively on Windows, and on its own it serves
your ebooks; but films, music and audiobooks *are* Jellyfin, Navidrome and
Audiobookshelf, and running those without containers would mean SoundStorm
installing and supervising three third-party servers as Windows processes. That
is a different project, and the one thing SoundStorm is careful not to become.

## For developers

```sh
git clone https://github.com/GabrielHollberg/soundstorm && cd soundstorm
docker compose -f docker-compose.yml -f docker-compose.dev.yml up --build
```

The compose split is deliberate: `docker-compose.yml` names a published image
and nothing else, so it works on its own for somebody who never cloned
anything. `docker-compose.dev.yml` adds `build: .` on top.

```sh
go test ./...
pwsh scripts/make-sample-media.ps1     # a synthetic library, no downloads
pwsh scripts/fetch-test-library.ps1    # ~750MB of real public-domain media
```

On Windows, Smart App Control refuses to run freshly built test binaries, so
`go test` fails on a different handful of packages each time. Run the suite in
a container instead — no Windows binary is executed, and it is what CI does:

```sh
docker run --rm -v "//c/dev/atrium:/src" -w /src golang:1.24-alpine go test ./...
```

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
internal/pdf/        PDF metadata, without parsing PDF structure
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
- **A ~22MB starter library** is embedded in the binary (`internal/starter`) and
  unpacked into empty library folders on first run. Public domain and CC0
  throughout. Set `SOUNDSTORM_STARTER_LIBRARY=false` to skip it.
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

## Notes

- **Module path** is `github.com/GabrielHollberg/soundstorm`. If the repo lives
  elsewhere, fix `go.mod` and run
  `grep -rl GabrielHollberg/soundstorm . | xargs sed -i 's|GabrielHollberg/soundstorm|<you>/soundstorm|g'`.
- **No `go.sum`** and that is correct, not an oversight.
- **Serve over TLS or a private network.** Subsonic stream URLs carry
  credentials in the query string — that is the protocol, and although those
  URLs never leave SoundStorm, the session cookie still crosses the wire.
