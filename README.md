<p align="center">
  <img src="web/social-preview.png" alt="EmberStorm: your music, films, TV, audiobooks, ebooks and photos. One app, in your own home.">
</p>

<p align="center">
  <a href="https://github.com/GabrielHollberg/emberstorm/releases/latest/download/EmberStorm-Setup.cmd"><strong>⬇ Download for Windows</strong></a>
  &nbsp;·&nbsp;
  <a href="#mac-and-linux">Mac and Linux</a>
  &nbsp;·&nbsp;
  <a href="docs/guide.md">User guide</a>
  &nbsp;·&nbsp;
  <a href="https://github.com/GabrielHollberg/emberstorm/releases">What's new</a>
</p>

<!-- The same screenshots as emberstorm.app (web/shots), from test servers
     with made-up music and free stock photos. -->
<p align="center">
  <img src="web/shots/home.png" width="190" alt="Home on a phone: shuffle all music, new music">
  &nbsp;
  <img src="web/shots/photos.png" width="190" alt="Photos as a timeline of months and days">
  &nbsp;
  <img src="web/shots/now-playing.png" width="190" alt="Now Playing with a storm animation that follows the music">
  &nbsp;
  <img src="web/shots/reader.png" width="190" alt="A book read one line at a time">
</p>

EmberStorm™ turns a computer at home into your own streaming service. Put your
files in its folders, or drag them onto the window, and they appear, sorted,
with covers, ready to play on the phones, tablets, TVs and computers in the
house. There are no subscriptions, no adverts and no accounts with anybody
but yourself, and nothing to set up by hand.

## What you get

**Music**
- A proper music player: gapless playback, crossfade, even volume across songs,
  a sleep timer, and lock-screen and headphone controls.
- Synced lyrics that light up line by line, and a dozen **visualizers** that
  move with the song itself (EmberStorm listens to every track for its beats).
- **Radio** that never runs out: from any song, album or artist, by **mood**
  (Chill, Feel good, Focus, Party...) or by decade and genre. EmberStorm
  listens to your music to learn how it sounds.
- Mixes, favorites and playlists. **Import your playlists from Plex/Plexamp**,
  or from any player that saves M3U files.
- Download songs to your phone for when you're offline.
- **Your year in music**, a recap of your listening, and optional
  [ListenBrainz](https://listenbrainz.org) scrobbling.

**Films and TV**
- Plays nearly any video file, including ones a browser normally can't: they
  are converted as they play.
- Subtitles, a choice of audio languages, **Continue watching**, and **Up next**
  into the next episode.
- **Picture quality** per device: the original at home, lighter away from home
  or on mobile data, changeable while you watch. Download films for the road,
  choosing the quality and language.

**Books**
- Read ebooks (EPUB, PDF) and listen to audiobooks in the app. Your place is
  kept on every device.
- Audiobooks get a table of contents, a timeline for the chapter you're in,
  thirty-second skips and a speed control.
- **Read along**: when you have a book as both an ebook and an audiobook, the
  pages turn by themselves with the narration - or read it as one line gliding
  by, or a word at a time, with the narrator or at your own speed.
- **Make an audiobook from an ebook** with a natural AI voice, or **an ebook
  from an audiobook** - made on your own computer, nothing sent anywhere.

**Photos**
- A timeline of months and days, like Google Photos: a handle to fly through
  years, pinch to zoom, and videos that play as you scroll past. Browse people
  and places, and search by what's *in* a picture ("beach", "dog"). iPhone HEIC
  photos just work.
- **Everyone has their own photos.** Each person's go in a folder of their own,
  sorted by when they were taken, and only they see them in the app. The owner
  sets how much space each person gets.
- **Send photos and videos to someone in the house.** They choose whether to
  add them, and get a copy of their own.
- **Back up your phone** automatically with the Android app (and the iPhone
  app, once it is out).
- **Bring your photos in from anywhere**: Google Photos, iCloud, Facebook,
  Instagram, Snapchat, Flickr, WhatsApp and Telegram downloads, cloud drives,
  SD cards and old computers. Dates and places are kept, and a photo you
  already have is skipped, so adding the same ones twice does no harm.

**For the whole household**
- Everyone gets their own login, with their own favorites, playlists, history
  and place in every book.
- **Who's listening?** A shared TV or tablet can keep several people: pick
  yourself instead of typing a password, with an optional PIN.
- **Sign a TV in from your phone** by scanning a QR code or typing a short code,
  instead of typing a password with a remote.
- **Use your phone as the TV's remote**: choose the TV, and the music, films and
  photos you pick play there while your phone controls them.
- **Invite family with a QR code**: they scan it and choose their own password.
- Choose which shelves each person can see, so the kids can have no films, for
  example.
- An app for **Android** (phones, Google TV, Android TV, Fire TV), or install
  the web app on any phone with its own icon. Apps for **iPhone** and **Apple
  TV** are in testing and coming to the App Store.
- Secure (HTTPS) automatically, with nothing to set up. Reaching it from outside
  the house is optional and off until you turn it on. Passwords must be strong,
  and you can make every new device wait for approval from one already signed
  in.
- The apps can remember **several servers** (yours and your parents', say) and
  switch between them.

<p align="center">
  <img src="web/shots/tv.png" width="780" alt="EmberStorm playing music on a TV, with a storm animation">
</p>

## What you need

| | |
| --- | --- |
| **Computer** | Windows 10 or 11, a Mac, or Linux, left switched on while you use it |
| **Memory** | 16 GB recommended (it runs several media servers; photo search alone wants several GB) |
| **Disk** | About 20 GB for EmberStorm itself, plus room for your media |
| **Internet** | For the first install (a large download); afterwards it runs at home |

Phones, tablets and other computers need nothing installed: they use EmberStorm
in their web browser, or add it to their home screen as an app. There is also
an Android app (from the [releases page](https://github.com/GabrielHollberg/emberstorm/releases),
the `android-` ones, which also run on Google TV, Android TV and Fire TV). The
iPhone and Apple TV apps are in testing and not in the App Store yet; until
then, an iPhone uses the web app.

## Install

### Windows

1. **[Download EmberStorm-Setup.cmd](https://github.com/GabrielHollberg/emberstorm/releases/latest/download/EmberStorm-Setup.cmd)**
2. **Right-click** the downloaded file → **Properties** → tick **Unblock** at the bottom → **OK**
3. **Double-click** it.

A setup window walks you through the rest. Expect **10 to 30 minutes**, mostly
downloading, and keep the window open until it says it's finished. Along the way:

- **Windows asks for permission** to install Docker (the engine EmberStorm runs
  on) and sometimes Windows Subsystem for Linux. Click **Yes**. The setup
  answers Docker's own first questions for you; if a Docker window appears
  anyway, click **Accept** for its terms and **Skip** for anything else. No
  Docker account is needed.
- **You choose where to keep your library.** Keep the suggested folder, or pick
  one on another drive.
- **"Windows Security Alert"** for Docker Desktop Backend: click **Allow access**.
  If it asks **"Is this your home network?"**, click **Yes** at home, so your
  phone and TV can reach EmberStorm.

When it's done, your browser opens EmberStorm and there's an EmberStorm icon on
your desktop.

<details>
<summary><strong>Why the "Unblock" step?</strong></summary>

Windows blocks *every* script downloaded from the internet ("An Application
Control policy has blocked this file"). That is about the file type, not about
EmberStorm, and ticking **Unblock** is how Windows lets you say you trust it.
Skipping it is the most common reason nothing happens when you double-click.

</details>

<details>
<summary>Rather paste a command? (PowerShell)</summary>

```powershell
irm https://raw.githubusercontent.com/GabrielHollberg/emberstorm/main/install.ps1 -OutFile "$env:TEMP\soundstorm.ps1"
powershell -ExecutionPolicy Bypass -File "$env:TEMP\soundstorm.ps1"
```

</details>

### Mac and Linux

Run this in a terminal:

```sh
curl -fsSL https://raw.githubusercontent.com/GabrielHollberg/emberstorm/main/install.sh | sh
```

It sets up Docker if the computer does not have it (the engine EmberStorm runs
on: plain Docker on Linux, Docker Desktop on a Mac), downloads everything, sets
it all up and prints the address to open. Your computer asks for your password
once, to install Docker. On a Mac, a Docker window may open the first time:
click **Skip**, no Docker account is needed.

<details>
<summary>Already use Docker Compose? Install by hand</summary>

```sh
mkdir soundstorm && cd soundstorm
curl -fsSL https://raw.githubusercontent.com/GabrielHollberg/emberstorm/main/docker-compose.yml -o docker-compose.yml
docker compose up -d
```

Open <http://localhost:8099>. The setup code for the first account is printed in
`docker compose logs soundstorm`. More options are in the [user guide](docs/guide.md#advanced-settings).

</details>

## Getting started

**1. Create your account.** The first screen asks for a username and password
(at least 12 characters, and not a common one); that account is the owner. If it asks for a **setup code**, it's shown at the end
of the setup window and saved in the `.env` file in your EmberStorm folder. The
code makes sure only the person who installed EmberStorm can claim it.

**2. Add your media.** Either way works:

- **Drag files or whole folders onto the EmberStorm window.** EmberStorm works
  out what each file is and files it on the right shelf. If it can't tell (an
  MP3 could be a song or an audiobook chapter), it asks once. On a phone, use
  **Settings → Add media**.
- **Or copy files into the library folders** (the desktop has a shortcut to
  them). This is best for a big collection:

  ```
  library/
    music/        Artist/Album/01 Song.flac
    movies/       Arrival (2016)/Arrival (2016).mkv
    tv/           Severance/Season 01/Severance - S01E01.mkv
    audiobooks/   Author/Book Title/book.m4b
    ebooks/       Author/Book Title/book.epub    (a Calibre library works too)
    documents/    Manuals/Dishwasher.pdf
    pictures/     2024 Holiday/IMG_4031.heic
  ```

New files show up within a minute. A song, a book and an audiobook come
included, so you can try everything straight away. Delete them whenever you
like.

**3. Put it on your phone.** Open **Settings → Use on your phone or TV** on the
computer. It shows the address to type on your phone (your phone must be on the
same Wi-Fi). Then either install the app (Android: the newest `android-` file on
the [releases page](https://github.com/GabrielHollberg/emberstorm/releases)),
which also backs up the phone's photos, or add the web app to your home screen
(on an iPhone, for now, this is the way):
- **iPhone:** Share → **Add to Home Screen**.
- **Android:** Chrome menu → **Install app**. Use the secure address, the one
  ending in `.home.emberstorm.app`.

**4. Add your family.** **Settings → People**: give each person a name and a
password, tick which shelves they may see, and set their photo space if you
like (100 GB each unless you change it).

**5. Bring your photos in.** **Settings → Your photos → Bring your photos in
from anywhere** shows how to download your photos from Google, Apple, Facebook
and the rest; drop the zips on the window and EmberStorm sorts them.

### Coming from Plex or Plexamp?

Your playlists can come with you. On **Music → Playlists**, tap **Import
playlist** → **From Plex or Plexamp**, sign in on Plex's own page, and pick the
playlists you want. Each song is matched to your EmberStorm library, and any
that aren't in it are listed afterwards. EmberStorm never sees your Plex
password and doesn't keep the Plex sign-in once you're done. (Your Plex server
must be switched on, and reachable from the EmberStorm computer.)

### Using it away from home

At home it works straight away. To listen from anywhere:

- **Remote access** (Settings → *Reach it from anywhere*) gives your server its own
  secure web address. EmberStorm opens the port on your router itself where
  the router allows it, and tells you exactly what to do where it doesn't.
- **[Tailscale](https://tailscale.com)** works on any internet connection, puts
  nothing on the internet, and needs the free Tailscale app on each device. On
  Windows, use **Set up Tailscale** in the Start menu.

Details are in the [user guide](docs/guide.md#away-from-home).

## Keeping it running

| | Windows | Mac and Linux |
| --- | --- | --- |
| **Update** | Start menu → **Update EmberStorm** | Run the install command again |
| **Start / stop** | The desktop icon starts it; it also starts with Windows | `docker compose up -d` / `docker compose down` in the install folder |
| **Move to a new computer** | Start menu → **Move EmberStorm to another computer** | `install.sh` with `--export` ([guide](docs/guide.md#moving-to-another-computer)) |
| **Uninstall** | Settings → Apps → EmberStorm → Uninstall | The Mac app: the cloud in the menu bar → Uninstall. Otherwise `install.sh` with `--uninstall` |

Updating keeps your library, accounts and settings. **Uninstalling never deletes
your media**: the `library` folder is left where it is.

**Back it up.** One small file holds every account and the passwords
EmberStorm made for the media servers inside it. See
[Backing up](docs/guide.md#backing-it-up). The uninstaller saves one for you
automatically.

**Forgot your password?** See [Getting back in](docs/guide.md#forgotten-your-password).

## Troubleshooting

**Nothing happens when I double-click the setup file.** Do the **Unblock** step
(right-click → Properties → Unblock), then double-click again.

**Setup says my computer can't run it (virtualization).** Docker needs
virtualization switched on in your computer's BIOS/UEFI settings, usually called
*Intel VT-x*, *AMD-V* or *SVM*. Turn it on, restart, and run the setup again.

**My phone can't open EmberStorm.**
- Make sure the phone is on the same Wi-Fi as the computer, and not a *guest*
  network.
- On Windows, run **Update EmberStorm** from the Start menu while on your home
  network: it fixes the network and firewall settings.
- Give the computer a fixed address in your router (a "DHCP reservation"), so
  the address never changes.

**The secure `.home.emberstorm.app` address doesn't load.** Some routers block
it on purpose. EmberStorm then stays on the plain address, and everything still
works.

**A new file doesn't show up.** Give it a minute, or use **Settings → Check for
new files**. If someone else can't see it, check which shelves they're allowed
under **Settings → People**.

**Something else?** The Windows setup window has **Show log file**; send that
file along with a description when you
[open an issue](https://github.com/GabrielHollberg/emberstorm/issues).

## Privacy: what leaves your house

Your media, searches and passwords stay on your computer, and nothing about
what you watch or play is sent anywhere. EmberStorm sends no usage data, and
switches off the anonymous statistics Navidrome would otherwise send. It
contacts the internet for:

- **Its secure address.** A small name service gives your server its
  `….emberstorm.app` name, and helps it get a free certificate from
  [Let's Encrypt](https://letsencrypt.org). It knows your server's *home
  network* address, and its internet address only if you turn on remote
  access. It never carries your media.
- **Updates**, only when you run them: the programs are downloaded from
  GitHub's and Docker's registries. EmberStorm does not check for updates by
  itself.
- **Things you switch on**, all off by default: finding missing lyrics online
  ([LRCLIB](https://lrclib.net)), artist bios and similar artists
  ([MusicBrainz](https://musicbrainz.org), [ListenBrainz](https://listenbrainz.org),
  Wikipedia), remote access, scrobbling, and importing from Plex.

The media servers it runs also go online on their own, for their usual jobs:
Jellyfin looks up film and show details and posters (from TheMovieDb and
similar), Immich downloads its photo-search models the first time they are used
and checks for its own new versions, the speech recognition behind *Make an
ebook* downloads its model once, and Storyteller (read along) checks its
changelog.

## Built on

EmberStorm is the app on top. The heavy lifting is done by excellent open-source
servers, which EmberStorm installs, sets up and keeps out of your way:

| | | |
| --- | --- | --- |
| Music | [Navidrome](https://www.navidrome.org) | GPL-3.0 |
| Films and TV | [Jellyfin](https://jellyfin.org) | GPL-2.0 |
| Audiobooks | [Audiobookshelf](https://www.audiobookshelf.org) | GPL-3.0 |
| Photos | [Immich](https://immich.app) | AGPL-3.0 |
| Read along | [Storyteller](https://storyteller-platform.dev) | MIT |
| Moods and "sounds like" | [AudioMuse-AI](https://github.com/NeptuneHub/AudioMuse-AI) | AGPL-3.0 |
| Make an audiobook (voice) | [Kokoro-FastAPI](https://github.com/remsky/Kokoro-FastAPI) | Apache-2.0 |
| Make an ebook (speech recognition) | [Whisper ASR Webservice](https://github.com/ahmetoner/whisper-asr-webservice) | MIT |
| Ebook reader | [foliate-js](https://github.com/johnfactotum/foliate-js) | MIT |
| Video player | [hls.js](https://github.com/video-dev/hls.js) | Apache-2.0 |

Each runs unmodified in its own container, under its own license.

## For developers

How it fits together, how to build it and how to add a backend:
[docs/developers.md](docs/developers.md).

**How EmberStorm works, in depth:** the [`site/`](site/index.html) folder is a
small website explaining the whole project, from the overview down to each part
and the decisions behind it. Open `site/index.html` in a browser.

## How it is made

EmberStorm is designed and run by one person, and most of its code is written
with an AI assistant ([Claude](https://www.anthropic.com/claude)), working to
that person's decisions. Each change is checked on a running server before it
ships, the code has had several rounds of security review, and the reasons
behind each design choice are written down in [`site/`](site/index.html) and
[`CLAUDE.md`](CLAUDE.md), so anyone can see why it is built the way it is.

## Support EmberStorm

EmberStorm is free and made by one person. If it's useful to you, you can help
keep it going on [GitHub Sponsors](https://github.com/sponsors/GabrielHollberg).

## License

EmberStorm is free software under the [GNU Affero General Public License,
version 3](LICENSE) (AGPL-3.0): you may use, study, change and share it, and
anyone who offers a changed version to others - including over a network -
must share their changes under the same license.

Versions published before 4 October 2026 were released under the MIT License,
and remain available under it.

The media servers it runs alongside (table above) and the vendored libraries
keep their own licenses.
