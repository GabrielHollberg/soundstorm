# SoundStorm — working notes for Claude

Read this first. It carries decisions that the code cannot tell you, including
two reversals of earlier decisions that looked right and were not.

## What this is

A unified front end for a self-hosted media library. One login, one search box,
one player over Navidrome (music), Jellyfin (films and TV), Audiobookshelf
(audiobooks), Immich (pictures) and plain folders of EPUBs and PDFs (ebooks
and documents).

The user's words for what they wanted: *"an all-encompassing server that can do
movies, audiobooks, ebooks, music all together... easy for users to install and
then create a login... and put their library into organized folders."*

## History rewritten, 2026-10-09: the Mac re-clones

The repository's history was rewritten twice on 2026-10-09 (personal emails;
then the owner's own titles, places, home addresses and the business notes,
which now live in the private repo `GabrielHollberg/emberstorm-private`).
**The Mac: re-clone** (rename the old folder, clone, never push from the
old one), and read the private repo's notes for anything business.

## How this is developed: two machines, one main

Two Claude Code sessions work on this repository, one per machine. Each has
its own memory, which the other never sees, so what both must know is here.

- **The Windows PC** (`H:\dev\soundstorm`) runs the live server, and is where
  the server, the web app (`internal/webui/assets`), the Android app
  (`android/`) and any Android TV work happen. Only it deploys:
  `docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d --build soundstorm`,
  then **`sh scripts/after-deploy.sh`**, then push. The check (2026-10-04,
  after two deploys in a day broke the app for the owner - a script error as
  the page loaded, and an empty Photos tab): the live server healthy and its
  page loading without a script error, then a throwaway test server
  (`scripts/smoke-stack.sh`: its own generated music, film, photos and the
  starter book, an account of its own, 127.0.0.1:8296, its data in Docker
  volumes) given the new build and walked by a browser at phone, TV and
  computer size - a song plays and Now Playing opens, Photos shows as it
  opens and a photo and a video open, a film plays, a book opens, Settings
  opens. About a minute; the first run makes the test server (a few
  minutes). Proved against both of that day's bugs, put back on purpose: each
  failed the check. A screen it does not walk is not covered - add a step
  when one is built.
- **The Mac** is for the iPhone app (`ios/`) and any Apple TV work - Xcode
  runs nowhere else. It tests against the live server at the home address or
  its away-from-home name. It cannot deploy the server.
- **Android builds** come from `android/` on main (`gradlew assembleDebug`),
  with `versionCode` raised each time so a phone installs over the last. They
  are published as GitHub pre-releases named `android-<version>`, never marked
  Latest, since Latest is the server's installer.
  **Nothing updates an installed copy by itself**: the owner installs each
  release on their phone, and the projector (Google TV) only gets one
  by adb over the network - its address and the steps are in the owner's
  private notes (this PC's VPN off). It
  sat on 0.7 until 0.26 (2026-10-02), so push each release to it too.

**main is the only long-lived branch.** The apps are folders on it. They used
to live on `android-app` and `ios-app`, and that is how a bug shipped: the
web changes the Android app needed (the safe-area variables, Change server)
were made on its branch, the server serves main, and the installed app's
title sat under the camera until they were brought over. Both branches were
merged into main on 2026-09-30. Do not start per-app branches again; if a
short one is needed for a risky change, merge it the same day.

**The image build must pass, and is checked twice** (2026-10-10: GitHub's
publish failed on every push from 8 to 10 October at gofmt - four unformatted
files - and nobody saw, so every new install, the test box's too, got the 8
October EmberStorm; the live server, built here, hid it). Now: the commit hook
(`scripts/private-check.sh staged`, already installed on both machines) refuses
staged Go files gofmt would change - with Go's gofmt where it is installed, else
in Docker (this PC has no Go); and `scripts/after-deploy.sh` says whether
GitHub's last image build passed, a failure failing the check. Its live health
step now waits up to 90s for every backend to reconnect: "8 sources, expected
9" on every deploy was a false alarm that taught everyone to read past it.

**Every session starts with `git pull` and ends with a commit and a push.**
The owner switches machines by telling the session they are leaving; that
session commits and pushes everything, and the next one pulls before touching
anything. Never leave work uncommitted on one machine.
**On "switching", pull as well as push** (the owner's asking): commit and push
what is left, `git pull`, and say in a line both what went up and what came in
from the other machine, or that nothing did.

**The shared web files are where the two collide**: `app.js`, `style.css`,
`index.html`. Web and server changes are made on the PC by default. When the
iPhone app needs one, the Mac may make it - pull first, keep it small, push at
once - and must tell the owner it reaches phones only once the PC deploys.
Web changes for the phone apps always go to main, never into an app's folder.

**Working with the owner** (kept here so both machines know):

- They dictate. An odd word may be mis-transcribed: restate the reading
  before building when a request is ambiguous.
- One real check per change, then commit. Do not run long comparison or
  mutation runs; mention what was not verified. For phone fixes they would
  rather deploy and test on their own phone than wait on a long simulation.
- Replies in plain words, about what they will see.
- Ask before experiments on the live server, before writing to their real
  backends (Immich and the rest) for a test, and before deleting media. Their
  media may be read for analysis, never changed or copied.
- **Nothing private goes anywhere public - a strict rule** (the owner's,
  2026-10-09, after two history rewrites to take things out). Public means
  everything in this repository - code, comments, tests, notes, README, docs,
  `site/`, `web/` (the website) - and commit messages, release notes, issue
  and pull request text, and anything else posted on GitHub or the web.
  Private means:
  - the owner's personal email addresses, and their personal accounts;
  - their server's real install code, chosen web address and public address;
  - their family's names;
  - where they live: town, county, state, time zone named as a place (say "a
    time zone west of UTC"), photo places from their library;
  - their home network: this PC's address, the projector's, the routers';
  - titles from their own library (films, shows, songs, books) and what they
    rip or download - use made-up titles, keeping sizes and lengths;
  - the business: the company, its state and number, prices, costs and
    margins, suppliers and orders, the trademark search and lawyer,
    certificate-limit requests, bank, cards and anything financial.
  Those go in the private repo `GabrielHollberg/emberstorm-private` (clone
  it beside this one), never here. Use made-up stand-ins in public text
  (`k3x9m2p7qa`, `yourname.home.emberstorm.app`, 192.168.0.50). **A commit is
  refused** that adds any of the private repo's `private-words.txt`
  (`scripts/private-check.sh`, installed as this clone's pre-commit and
  commit-msg hooks by `sh scripts/private-check.sh install`; each machine
  installs it once). Never get past it with `--no-verify`: if it stops a
  commit, take the private thing out, or ask the owner.
- Commits carry GitHub's private address, never a personal email: on each
  machine `git config user.email "96497567+GabrielHollberg@users.noreply.github.com"`
  in this repo (done on the PC 2026-10-08; **the Mac: do it before your next
  push**). The owner's GitHub refuses pushes that expose their email.
- Never print credentials. Commits end with the Co-Authored-By line the
  session is given; never put a model name in a commit.

## For the PC: a public demo server (written 2026-10-10, from the Mac)

The owner wants TestFlight public (a public link for the iPhone and Apple TV
apps). Apple reviews the first build for external testing, and its reviewer
must sign in and use the app - they cannot set up a server. **The owner's
choice: a separate demo server on this PC**, which also lets anybody try
EmberStorm before installing. Built here, as only the PC runs servers:

- **A second install, apart from the live one in every way**: its own folder,
  compose project (`SOUNDSTORM_PROJECT`, as the move rehearsals use; the
  compose file pins `container_name`, so an override renaming every container,
  as `scripts/smoke-stack.sh` does), volumes, library, port (not 8099) and so
  its own install id and `*.net.emberstorm.app` name, reachable from the
  internet (remote access, its own router port). Never the owner's library or
  backends; memory capped so it cannot starve the live server.
- **Only media anyone may show**: the starter library, generated music,
  public-domain or CC films (Blender's), CC0 photos, public-domain books - as
  the website's screenshots were made.
- **A demo mode in the server** (`SOUNDSTORM_DEMO=true`, say): the demo
  account a member who can look at and play everything but changes nothing
  that outlasts the day - no uploads, deleting, moving, password or PIN
  changes, invitations, photo backup, making audiobooks, scrobbling tokens -
  each refused with "This is a demo". And the whole install put back to its
  starting state every night (its volumes from a snapshot), so whatever
  anyone does is gone by morning.
- **Its account and address in the private repo only** (they go to Apple in
  the review notes, never here). Tell the Mac when it is up: the Mac makes the
  external test group, fills in Apple's forms (the privacy page, `web/privacy.html`,
  drafted - the owner approves it before the website is published), and
  submits the iPhone and Apple TV builds for beta review.

## Catching the Mac and Linux up (written 2026-10-10, from the PC)

A whole day on the Windows setup and the web app (see "Installing, updating,
removing" and the welcome under "Nothing found, said plainly"). One list of
what is left for the other platforms, so nothing is lost in the long notes
below; tick items off here.

**Nothing to do: the web changes reach the iPhone app by themselves** (it
shows the page): the welcome one step at a time with the address first, the
"drop files on any EmberStorm screen" wording, Add your music no longer ticked
by the starter library. The offer to install EmberStorm as an app is never
shown inside the phone apps (`window.soundstormApp`).

**For the Mac: the iPhone and Apple TV apps** - open items gathered from the
sections below, newest first:
1. **The audiobook's place, saved by the app** (2026-10-09, "An audiobook's
   place, wherever it was listened to"): take `soundstormApp.place` in
   `pageScript` and PUT `{seconds, duration, finished}` every ten seconds while
   playing and on pause, end and stop - Android 0.54 does. Without it an hour
   listened with the screen off is an hour back on the next device.
2. **Photo backup checks only what is new** (2026-10-09, "And each run checks
   only what is new"): the folder marker (`GET /api/photos/backup/marker`),
   assets confirmed kept with their `modificationDate`, a full check daily or
   when the marker moves - as Android 0.53.
3. **The bug review's list** (2026-10-09, "For the Mac, from this review
   (iPhone and Apple TV)"): the TV's `moveServer` copying the
   `__Host-soundstorm_session` cookie too; backup's export error cleared after
   a run gets through; `FileUploads` sending each job to its own server; Up next
   not saving the next episode as finished; a book's place not saved as 0 before
   its resume lands; `VolumeKeys` off when the page goes; the background task
   scheduled again; leftover backup copies and the restore race.
4. Older and still open: a Share extension (as Android's Share > EmberStorm);
   the iPhone's `confirm()` while something is presented.
*(All four done on the Mac, 2026-10-10.)* **1:** `soundstormApp.place` in
`pageScript`; `NativeAudio` keeps the place (`setPlace`, files matched by path)
and PUTs `{seconds, duration, finished}` every ten seconds while playing and at
once when paused, ended or stopped, only from a settled player, one save at a
time; a new file forgets it until the page tells it again. **2:** the marker,
`backup-confirmed.txt` (key, `modificationDate` stamp; scope server+account),
a full check daily, when the marker moves, with none, or after one cut short;
only unconfirmed assets are looked up at all. The marker is read once the
run's files are through with iOS (`markerAfterSent`), not at the run's end, or
every sending run would see its own uploads as a change. **3:** the TV's
`moveServer` renames `soundstorm_*` to `__Host-soundstorm_*` onto https (and
never overwrites a `__Host-` one already there); a photo the library will not
export is passed over with its own message (`ExportFailed`), and "could not
reach the server" clears once a file arrives; `FileUploads` jobs carry their
own `server`, and hand-overs run one at a time; Up next empties the player
before the next episode loads; the TV holds a book's clock while a jump
lands (`settling`); `VolumeKeys` off on `didCommit` and a killed page; the
background task is always scheduled again; an upload finishing while the
sessions are read back is not taken back as waiting (`finished`). **4:** a
**Share extension** (`SoundStormShare`, `dev.soundstorm.app.share`, app group
`group.dev.soundstorm.app`): what is shared is copied into the group's
`Shared/inbox/<id>/` (`.ready` last); the app hands every ready share to
`__soundstormShared` on opening and as each page loads, retried for a minute
until signed in (`handShared`), moving it to `taken/` (put back if the page
refuses); a week later anything left goes, unless an upload still waits on it
(`SharedInbox.sweep`). An extension may not open its app, so the sheet says to
open EmberStorm. `confirm()` while another dialog is up or closing waits for
it (six seconds at most) rather than answering Cancel. Checked: both apps
build; a share placed in the group folder came up in the page's review on
opening ("Shared Book.epub -> Ebooks"). Not checked: the extension itself from
a real Share sheet, the place saved with a phone's screen off, the marker
against a server with photos (the Mac's test server has none).

**Mac and Linux installer (`install.sh`) against today's Windows setup** -
what Windows does that `install.sh` does not yet:
1. **Mark an install as installed** (`SOUNDSTORM_INSTALLED=1` in `.env` once
   EmberStorm has started and answered). Today a compose file means "update",
   so a first install stopped during the download is an update next time and
   skips the sleep question and the room check.
2. **Shortcuts with the cloud.** None are made: a Mac gets no app to click, a
   Linux desktop no launcher. Mac: an EmberStorm app in ~/Applications that
   starts Docker Desktop if needed and opens the address (the `-Launch` of the
   Windows setup), with the cloud as its icon (an .icns drawn by
   `scripts/make-icons.py`); Linux: `~/.local/share/applications/emberstorm.desktop`
   and the icon. Then the finish says how to keep it (Keep in Dock; add to
   favourites) as Windows says Pin to taskbar.
3. **`?here=server`** on the address it opens (`open_browser`) and on the new
   app's, so the server's own computer gets no "install as an app" offer.
4. **Uninstalling leaves only what the person keeps**: asked first, as Windows
   asks - keep a copy of the accounts (ticked), remove Docker (unticked; the
   Mac's Docker Desktop has its own uninstall in the app, Linux's Docker is the
   system's - leave it, say so), put back the sleep setting it changed (Mac
   `pmset -c sleep` and `autorestart`, recorded before changing them in a file
   like Windows' `changes.json`; Linux the GNOME setting and logind's lid); the
   app or launcher removed; an install folder left empty removed.
5. **Docker's data with the library** (Windows: the library's internal drive,
   never a removable one). Linux: `data-root` in `/etc/docker/daemon.json`
   before Docker first starts, when the setup installs Docker and the library
   is on another internal disk; the room check on that disk. Mac: Docker
   Desktop's disk can only be moved in its settings, which macOS guards (the
   privacy prompt seen 2026-10-08) - leave it, and check room in the home
   folder as now.
6. Not needed there: the window freezing, WSL and its welcome, Docker's desktop
   icon, the Settings, Apps entry.
**The Mac setup app is begun (`mac/`, 2026-10-10, the owner's asking: "is the
Mac installation up to par with Windows?" - it was not, Terminal being the
biggest gap).** A SwiftUI app, `EmberStorm.app` (`dev.soundstorm.mac`): opened
the first time, it asks where the media goes and whether to keep the Mac
awake, then runs `install.sh` (main's, at its commit) with no terminal,
showing steps, a bar and a percentage from what the script prints, and ends
on the setup code, the phone address and Open EmberStorm (`?here=server`);
opened after, it starts Docker and EmberStorm and opens it, with Update and
Uninstall - the Windows window and desktop icon in one. It moves itself to
Applications. **`install.sh` gained three hooks for it, all off unless the app
sets them** (so the PC's Terminal behaviour is unchanged): `as_root` uses
`sudo -A` when `SUDO_ASKPASS` is set and there is no terminal (the app's
askpass is a macOS password window); `keep_awake`'s default is
`EMBERSTORM_KEEP_AWAKE`; and `EMBERSTORM_RESULT` names a file the script
writes its URL, setup code, secure and LAN addresses and library to at the
end. **Keep them when changing `install.sh`**, and the `==> ` step names the
app reads (`Installer.read`). The icon is an Icon Composer document
(`write_mac_icon` in make-icons.py; macOS 26 plated older icons in grey).
Checked: it builds, its pages drawn (`-snapshot`), the icon rendered by
`ictool` and by macOS. **Not yet:** a whole install through it (this Mac had
25 GB free), the password window, a Developer ID certificate (only the account
holder can make one) and notarizing, so it cannot be handed out yet.
*(Then, 2026-10-10: the Developer ID certificate made, the app signed and
notarized, and the owner's first real run through it found: the password asked
after Docker's download (now asked and checked on the questions page); Docker
Desktop's window opening (its welcome settings written before its first start,
and its window hidden while the app works); "would like to administer your
computer" from the crontab (a LaunchAgent on a Mac now, `ADDRESS_AGENT`); a
registry pause stopping the download (`pull` tried again after 30, 60, 120s);
the details blank from Docker's thousands of lines; and **20GB was too little
room**: the images are near 19GB, so `check_room` asks 30GB less what is
already downloaded. **For the PC:** the Windows setup's `Test-DownloadRoom`
asks 20GB too. **And the window sat at 83% for an hour after EmberStorm was
up**: it redrew for every line Docker printed (tens of thousands, each a
change it watched), fell behind, and was still working through them long
after the setup had ended. The lines are no longer watched, the details are
put up four times a second, and progress changes only when it moves - a
pretend setup of 100,000 lines (`-page run -script`, debug) now finishes in
seconds.)*
**And opened with no account yet, the app now puts the setup code in the
address** (`Install.hasOwner`, `/healthz`'s `setUp`), as the Windows icon does:
the owner's first open after the window froze asked for a code. **For the
PC:** the sign-up form's help says to open `.env` "with Notepad" - on a Mac
it is a hidden file (Finder: Cmd-Shift-. in the EmberStorm folder, or
TextEdit's open dialog the same way); worth a Mac line there.
**The second run (Docker removed first):** Docker Desktop's window still
opened - it is a second app, the Electron dashboard
(`com.electron.dockerdesktop`), which the hiding never named; now both are
hidden. And macOS asked whether Docker may find devices on the local network
(Docker's own question, macOS 15 and later; Allow, or EmberStorm cannot open
the router's port). Nothing can answer that for the person, and it comes only when
Docker first starts, so the welcome says it will, and while Docker is being
installed the window says to stay nearby and click Allow, then that nothing
else asks. Not fixed: whether the
welcome settings were written (the folder is macOS-guarded; a settings file
left by the last Docker skips the write).

**Who does what:** the PC writes these in `install.sh` and tests the Linux half
in containers or the box's VM; the Mac then runs the whole install on a Mac -
never done end to end yet - and fixes what only a Mac shows (pmset, the app,
Docker Desktop's first start).

**And the website and apps on a Mac**: Safari on a Mac offers no install
question to a page; it has File > Add to Dock. The install offer could say so
there as it says Share > Add to Home Screen on an iPhone (the PC, in app.js).

## For the Mac, now (2026-10-08, from the PC)

*(All done on the Mac, 2026-10-08: the git email is GitHub's private address;
origin is github.com/GabrielHollberg/emberstorm; TestFlight builds of the
iPhone and Apple TV apps with the rename and emberstorm.app addresses went up
after 1406f4a. The App Store Connect listing is **EmberStorm Media** (2026-10-08:
"EmberStorm" alone was taken on the App Store; the name under the icon stays
EmberStorm). **The bundle id stays `dev.soundstorm.app`** - the owner's
decision, knowing a new id means a new listing, testers starting fresh and
Android reinstalling once; it is never seen, and after the first public
release it could not change anyway.)*

## Waiting on the Mac (written 2026-10-02, after two days on the PC)

From 2026-10-01 to 10-02 the PC worked on the server, the web app and
Android (0.19 to 0.31). Everything web reaches the iPhone app already, since
it shows the same page. What only the Mac can do, most useful first - each
is described in full in its own section below; tick them off here:

**iPhone app**
1. ~~**Background uploads**~~ - *done 2026-10-02, the owner's choice of the
   app picking*: an iPhone's web view keeps its own picker's files to itself,
   so in the app Add media asks the app (`soundstormApp.pickFiles`): Photos and
   videos (PHPicker, the originals) or Files, copied into the app's storage
   (`FileUploads.swift`) and handed to the page as `AppFile`s - name, size,
   date, and pieces read through the app (`readFile`, `blobOf` where a real
   Blob is needed: the copy sample, a different file's ends, a zip's tail, an
   import's piece). The page plans and asks as ever, then `uploads('add')`
   with each file's `id`; the app sends them in a background URLSession (two,
   Wi-Fi only or not; only while charging waits for the plug), with the
   Android app's status, so the page's progress, chip and Stop are unchanged.
   Checked by `testAddMediaIsSentByTheApp` (`-pickTest`, debug only: a
   generated photo, since Photos runs outside the app): planned, checked
   against what is there, sent by the app, "Added", filed by date. Not
   checked: Photos and Files themselves, and big videos.
2. ~~**Photo backup when locked**~~ - *done 2026-10-02*: files are handed to
   iOS ahead (200 or 1GB waiting), counted as they arrive, see the note.
3. **A TV's QR code opening the app** - *half done 2026-10-02*: the app takes
   `soundstorm://link?server=&code=` (CFBundleURLTypes; known servers only,
   `TVLink` in `AppLinks.swift`; checked by `testATVCodeLinkAsksToSignInTheTV`)
   and the page offers "Open in the SoundStorm app" on an iPhone too. The app
   also already handles the QR's own `https://<name>/link/<code>` when it
   arrives (`onContinueUserActivity`). **Left, for the PC then the Mac:**
   Universal Links - the names service serves
   `/.well-known/apple-app-site-association` at `home.soundstorm.dev` and
   `net.soundstorm.dev` (as `assetlinks.json`):
   `{"applinks":{"details":[{"appIDs":["LZA2K5LLDS.dev.soundstorm.app"],"components":[{"/":"/link/*"}]}]}}`
   - the team id is LZA2K5LLDS. Then the Mac adds the Associated Domains
   entitlement `applinks:*.home.soundstorm.dev` and `applinks:*.net.soundstorm.dev`.
   *(Done 2026-10-03: the names service serves it (`handleAppleLinks`,
   `/link/*` and `/invite/*`, live, and in Apple's cache), the app claims both
   names (`SoundStorm.entitlements`) and opens an invitation in the app too
   (`TVLink.invite`, a new server allowed). net.soundstorm.dev now points at
   the names service too, and the iPhone build carrying it is on TestFlight.
   Uploads use an App Store Connect key with the Admin role: the first key
   could not remake the store profile once the capability was added.)*
4. ~~**Scan the TV's code in the app**~~ - *done 2026-10-02*: VisionKit's
   scanner (`CodeScanner`), the code as Android reads it. Not checkable in the
   simulator (no camera): a real iPhone is the test.
5. ~~**Volume buttons turning the TV**~~ - *done 2026-10-02* (`VolumeKeys`):
   iOS gives no way to take the buttons, so the phone's own volume is watched
   (a hidden `MPVolumeView`), each press told to the page and the volume put
   back in the middle; the phone's level is restored when it ends. Not
   checkable in the simulator.

**For the PC, newest (2026-10-03):** see "Setting up a new box" under
"Selling it on a box" - the setup code made in advance and on the sticker,
and Android's Set it up (`soundstorm.local` and port 80 are in the box's
files now). And "Bringing media in from a USB drive": the box's half
(`usb.sh` and its udev rule) wants a run in the VM with a USB disk.

6. **No bounce at a page's ends** (2026-10-03): the page now sets
   `overscroll-behavior-y: none` on html and body, and Android 0.36 turned
   its web view's stretch off - pulling down at the top moved the tab bar.
   iOS 16+ should honour the CSS; to be sure, and for older iOS, set
   `webView.scrollView.bounces = false` in `WebViewController` (lists that
   scroll inside the page keep their own scrolling either way). Check by
   pulling down at the top of Home: the header and tabs stay put.

7. **No scrollbars, and the loading screen holds still** (2026-10-03): the
   page hides every scrollbar it draws, but a web view draws its own at the
   edge - Android 0.38 turns it off (`isVerticalScrollBarEnabled`). The
   iPhone wants `webView.scrollView.showsVerticalScrollIndicator = false` (and
   horizontal), and `bounces = false` from item 6 so the loading screen does
   not rubber-band. The loading screen itself is fixed to the screen now, with
   nothing under it scrolling (it was 100vh plus padding over a taller page,
   so it could be dragged up and down).

8. **Black icons, and the icon as the loading screen** (2026-10-03, the
   owner's asking): every icon's background is black now (`PAPER` in
   `make-icons.py`; Android's `icon_background`, 0.39), so the opening screen
   shows no lighter square, and the page's loading screen is the cloud alone,
   white on black, 96px wide in the middle - where Android's opening screen
   draws it, so one runs into the other. The iPhone's `icon-1024.png` and the
   Apple TV's layers are redrawn on main: the next build carries them. Give the
   iPhone's launch screen a black background too, if it is not already.
   *(Done on the Mac, 2026-10-03: `UILaunchScreen` is `LaunchBlack` with
   `LaunchMark`, the page's cloud white at 96 points; the iPhone's web view
   has no bounce and no scroll indicators - items 6 and 7.)*

9. **The opening screen waits for the app** (2026-10-03, the owner: it went
   icon, blank, icon). Android 0.40 keeps the system's opening screen up
   (`holdOpeningScreen`, a pre-draw hold) until the page says it is ready -
   the page posts `{type: 'ready'}` once its loading screen is hidden or
   shows a message instead of the icon (a watcher on `#boot`, so no path can
   forget) - or the app's own connect or failure screen shows, or eight
   seconds pass (the owner's limit), when the page's own loading screen, the
   same icon in the same place, carries on. Checked on the emulator: the
   system icon, then the page's in the same spot, then sign-in; no blank.
   **For the Mac:** the iPhone app wants the same - keep its launch image (a
   native view of the icon on black over the web view) until the page posts
   `ready`, eight seconds at most; the message already comes.
   *(Done on the Mac, 2026-10-03: `holdOpeningScreen` in
   `WebViewController`, let go on `ready`, the failure screen or eight
   seconds. Checked in the simulator: the cloud on black from the first
   frame, held, then the app. **And found on the way:** every backup message
   from the page - its status, every few seconds - started a backup run,
   each a check of every photo with the server; most of the first full
   backup's 885 checks. From the page a run now starts only when the last did
   not get through or ten minutes have passed (`pageAsked`): one check in a
   minute where there was one every four seconds.)*

**Apple TV**
1. ~~**Controlled from a phone**~~ - *done 2026-10-02* (`Remote.swift`,
   `RemoteControlled`): hello as "Apple TV", the long poll, every command,
   state every two seconds, the take-over question. Checked against a local
   server with curl as the phones: listed as Living room TV, play (a queue of two)
   reported, next moved to the second, stop reported idle; Alex sending
   something asked on the TV, no answer in 15 seconds was a yes, and the TV
   switched to Alex with his song. Not checked: a real song playing (no
   music server here) and a real phone's remote screen.
2. ~~**"Use your phone instead"**~~ - *done 2026-10-02*: on the PIN or
   password step, opens the sign-in screen on the phone's code
   (`AppModel.signInWithPhone`, the book saved first).
3. ~~**Videos and Live photos**~~ - *done 2026-10-02*: the two categories, and
   in the viewer a LIVE badge and OK playing the moving part over the still
   (`api.liveURL`, `<id>%40live` as the page's), muted while music plays.
   Not tried: no Immich on the Mac to give a Live Photo.
4. ~~The open bugs from both reviews~~ - *done 2026-10-02* (Apple TV and
   iPhone backup), except the iPhone's `.net` fallback and `confirm()` while
   something is presented.
5. **Read Along turning the page mid-sentence** (2026-10-03, the owner tested
   on the Apple TV: it still turns only once the next sentence begins). The
   page's reader now does it (`followWithinSentence` in reader.js): within a
   sentence over two seconds, the voice's place is the same share of the
   sentence's letters as of its time (`t` to `e`), and the page goes to the
   one holding that letter, aimed 1s ahead of the voice (the owner's choice after 0.8s and 1.2s; aimed at the
   voice it was still a little late). The TV's `Reader.swift` turns to a sentence's
   start only; it wants the same, on the lit range's own text.
   *(Done 2026-10-03: `followWithin` in `Reader.swift`, the same rule on the
   sentence's letters as laid out in pages. Built, not seen: no test book
   has one sentence across a page.)*
6. ~~**The Google TV app's look**~~ - *done 2026-10-03, the owner's asking
   ("as close a clone as possible", the animations left as they are):*
   `Theme.swift`, `Shell.swift`, `Detail.swift`, `WebMenu.swift`. The side
   bar, header and pills, cards with the white outline and 1.05, Home's
   quick buttons and New rows with See all, tabs and pills without a shelf
   left out, the gate screens as the page's card, Settings, album, artist,
   playlist and show pages, search across every shelf, and the page's own
   menu on a held OK in Now Playing and the reader. Left as Apple's: the
   text boxes and search keyboard (tvOS draws them), and the "Allow this
   sign-in?" and take-over questions, which must show over a film or Now
   Playing. Not tried with a real remote.

## Selling it on a box (an idea being tried, 2026-10-02)

The owner is weighing selling SoundStorm preinstalled on a small computer,
for people leaving the cloud: plug it into power and the router, scan the QR
code on the sticker, create an account - as easy as an Alexa. Nothing is
decided past the first test unit.

- **The test unit:** a small mini PC with 16GB of memory (Immich alone
  wants 6-8GB, so 12GB was turned down), a 64GB eMMC for the system, an
  internal NVMe drive for the data, no Wi-Fi (it goes on a cable, which is the
  setup path anyway). **Drives must be TLC**: QLC drives are slow on big
  copies. Which parts, what they cost and what a box sells for are in the
  owner's private notes, not here.
- **The system image is begun: `box/`** (2026-10-02, see its README). Debian
  13's cloud image with Docker, built by `box/build.sh` in the Debian WSL
  distro (it has KVM; Docker Desktop's VM does not, and Hyper-V is off, so
  no reboot of the live server was needed), booted by `box/run-vm.sh` as a
  pretend ME Mini - UEFI, the image as the eMMC, a blank NVMe data drive.
  The data drive is formatted btrfs only when blank (never USB) and holds the
  library, every data volume and the caches (`compose.box.yml`); `.env` gets
  the setup code once and the box's address every start. Checked in the VM:
  the drive prepared, the stack up with all 9 sources at
  `localhost:8399`. Two things it met: Docker makes any missing folder a
  backend mounts as root 0755 before SoundStorm runs, so SoundStorm could not
  write the shelves and the starter song and audiobook were lost - the box
  makes the seven shelves 0777 itself. **`install.sh` had a worse form of
  it on Linux**: it made the shelves as whoever ran it, 0755, and SoundStorm
  (uid 10001) could add nothing to any of them - uploads, the starter library
  - which Docker Desktop's ownership mapping hid on Windows and Mac. Shown in
  the VM's native Docker (10001 refused a 0755 folder of uid 1000, allowed at
  0777); the installer now opens the library and its shelves 0777 (not
  their contents). The installer itself was not run end to end. And 3GB was
  too little for a first start's every backend setting up at once (4.5GB
  did; the box has 16GB). **The images ship in the disk** (fetched by
  skopeo at build under names of the box's own, since a digest-pinned name
  does not survive save/load; loaded on first boot): a fresh VM with no
  internet at all had every source up in 2.5 minutes. 12GB of images, a 6GB
  compressed disk, 14GB used of the 64GB eMMC.
- **The caretaker's updates work** (`soundstorm-caretaker`, see
  `box/README.md`): signed manifests of digest-pinned images, a higher serial
  only, images downloaded before anything stops, the volumes snapshotted,
  and everything put back if SoundStorm is not healthy within ten minutes.
  Checked in the VM against a test channel with a development key: a good
  release installed in 30s; a broken one (SoundStorm's image swapped for
  another) was undone after its ten minutes - the snapshot put back as the
  volumes, the old images running, all 9 sources again. **Old versions are
  removed after an update** (2026-10-10, found asking whether C: and D:
  matter on the box - they do not, as the data drive holds every data
  folder): nothing took an update's old images away, so the 64GB eMMC would
  fill over a year or two of updates. Once an update is in and healthy,
  images of the box's own repositories that neither the running release, the
  one before it nor a container uses are removed (`pruneImages`; none when
  Docker cannot say what is in use). Checked: a pretend box through three
  updates (`TestOldVersionsAreRemovedAfterAnUpdate`), and Docker's own output
  on this PC matching what the code reads. Not run in the VM. **The real release
  key is not made yet**: whoever holds it can update every box, and losing it
  means no box can be updated again - it must live off this repository, and
  be backed up, before the first box ships. Not built: Settings > Updates in
  the app (the caretaker's socket mounted into SoundStorm), the caretaker
  updating itself.
- **Software it would still need:** writing the image to a real box; a
  script that turns a new unit into a finished one with its own name, setup
  code and QR sticker; the caretaker's drive health, factory reset and
  shutdown; the USB backup; Add storage; Jellyfin's conversions on the Intel
  graphics chip; heavy background jobs taking turns at night in 16GB.
- **Two sizes, 1TB and 2TB**, one internal drive each.
- **Protection, the owner's call:** one internal drive rather than a mirror,
  to keep the price down, and **a USB backup drive as an optional extra**
  (any drive the
  customer plugs in will do): offered at setup, backed up to every night
  automatically once plugged in, the app reminding plainly whenever it is
  missing or a backup has not run, and saying "not protected" while there is
  no backup at all. **USB first; cloud later** - it means accounts, billing
  and terms of service, so it waits until customers ask, but the backup is
  built with where it goes as a setting so cloud is mostly that work then.
  Encrypted cloud backup (and box-to-box) stays an opt-in extra,
  like everything else that sends anything out of the house; its key stays
  with the customer (a recovery code), never with the seller. Backups are
  the caretaker's job, likely with restic, never SoundStorm's; restoring is a
  choice at setup ("New box, or restore from backup?").
- **The license is AGPL-3.0** (decided 2026-10-04): open source, but anyone
  offering a changed SoundStorm to others, over a network included, must
  share their changes. Versions before that day stay MIT for good. Settings,
  Account links to the source, as the AGPL's network clause expects. Nothing
  that runs on somebody's own box is ever paywalled.
- **The product is called EmberStorm** (renamed 2026-10-07; the reasons are
  in the owner's private notes). **Only what people see changed**: the name
  and wordmark text, the website, apps' display names, docs, the names
  service's zone and the owner's devices re-pointed; every internal
  "soundstorm" (env names, containers, cookies, backend accounts, app ids)
  stays - renaming those broke every install the last time (atrium). Keep
  soundstorm.dev a year after, to send old addresses on.
  **Renamed, 2026-10-07.** Done: every visible "SoundStorm" in the web app,
  server messages and pages, installers, website (web/, now for
  emberstorm.app), docs, README and the Android app's text and TV banner. Kept
  as they were, so existing things are found again: the Windows install
  folder when one exists (`%USERPROFILE%\SoundStorm`, else EmberStorm), its
  uninstall registry key and firewall rule (old-named shortcuts are removed
  on update), the local CA's name, Navidrome's `SoundStorm listening`
  transcoding, AudioMuse's cron name, the backends' account names, and every
  lowercase id. The setup file is `EmberStorm-Setup.cmd` (renamed 2026-10-08;
  only that name is on the release now - old download links
  need not keep working, the owner's call, 2026-10-10). **The repository is github.com/GabrielHollberg/emberstorm** (renamed
  2026-10-08; GitHub forwards the old name - raw files, the API and release
  downloads checked - so installers and setup files already out keep working.
  Never make a new repository called soundstorm: it would end the forwarding).
  Kept: the image `ghcr.io/gabrielhollberg/soundstorm`, which every install
  pulls, and the Go module path. A
  saved server name with SoundStorm in it shows EmberStorm. **The addresses
  moved to emberstorm.app** (live 2026-10-08: the owner's server became
  k3x9m2p7qa.home.emberstorm.app with a new certificate a minute after the
  names service was set to the new zone - Railway stages variables until
  deployed; the website is live there; Android 0.45 released; .app for the servers too,
  the owner's choice the same day - people see and bookmark them): the names service
  already answers an install's name on every announce, and a server now takes
  a different name for its own id as its new one (`names.Client.Announce`,
  servetls step) and makes a new certificate; its default service is
  names.emberstorm.app; the away-name checks, the web app, the service
  worker's offline rule, the website and Android 0.45 (built, not released:
  wait for the DNS, as App Links verify at install) accept both zones
  (`names.Zones`, `ServerAddress.ZONES`). **The owner's to do first:**
  Porkbun - API access on for emberstorm.app; its DNS: `names`, `home`,
  `net` and `*` as CNAMEs to the Railway service, the GitHub Pages apex
  records and `www`; Railway - custom domains names/home/net/`*`.emberstorm.app
  on the names service, and `NAMES_ZONE=emberstorm.app`, `NAMES_SITE_ORIGINS=https://emberstorm.app,https://www.emberstorm.app`
  (the code's defaults stay soundstorm.dev until then); GitHub Pages custom
  domain emberstorm.app, then the website workflow; later soundstorm.dev and
  emberstorm.dev (a spare now) forwarded to emberstorm.app, its names left resolving. Then the PC deploys
  the server (its name moves), releases Android 0.45, and the owner's devices
  are re-pointed. Let's Encrypt's per-domain allowance starts afresh on
  emberstorm.app (the override asked for was soundstorm.dev's: if they answer,
  ask for it on emberstorm.app; otherwise ask again near 40 new boxes a week). A higher Let's Encrypt allowance for emberstorm.app was asked for (the private notes). The website's Home and Photos shots got the app's real EmberStorm header
  laid over the old, and `scripts/smoke/social-preview.js` draws
  web/social-preview.png from the shots on the test server (re-upload it in
  the repo's Settings, Social preview, by hand). **For the Mac:** the iPhone and Apple TV apps' visible name
  (display name, connect screens, strings), and the domain checks in
  `ServerAddress.swift`, `AppLinks.swift`, `WebViewController.swift`,
  `ConnectViewController.swift`, `PhotoBackup.swift` and `ServerDiscovery`
  accepting `*.home|net.emberstorm.app` as well as soundstorm.dev, the
  entitlements adding `applinks:*.home.emberstorm.app`,
  `applinks:*.net.emberstorm.app`, `applinks:names.emberstorm.app`; the
  bundle id and targets stay.
  *(Done on the Mac, 2026-10-08: both apps show EmberStorm - their names,
  connect screens, messages and permission texts; the page still sees
  `SoundStormApp/1`. `ServerAddress.zones` (emberstorm.app first,
  soundstorm.dev still taken), `installName`, `twin` and `inZones` replace
  every soundstorm.dev check; a typed code tries both zones; the entitlements
  claim the emberstorm.app names. **And a device saved under soundstorm.dev
  moves itself**: servers stopped answering their old names (checked: the
  owner's no longer does), so when the saved address fails, the same name on
  emberstorm.app is tried and, answering, saved in its place with the
  sign-in's cookies copied (`inNewestZone`; the iPhone's `showFailure`, the
  TV's `refreshSession`). Checked in the simulator: opened at
  k3x9m2p7qa.net.soundstorm.dev:8099, the app saved the emberstorm.app name
  and showed "Sign in to Gabriel's EmberStorm"; the iPhone's connect and
  wrong-address tests pass. The App Store Connect listing's name is the
  owner's to change there.)*

### Setting up a new box - decided with the owner, 2026-10-03

**For the PC: what the box's setup needs, none of it built yet.** The aim is
"some rando buys it, plugs it in" with no address ever typed. The order a
buyer goes through:

1. **Plug in** power and the router's cable (no Wi-Fi on this box).
2. **With a phone (most people):** get the app (a QR on the sticker or card),
   open it - it finds the box on the network (built: iPhone and Apple TV
   `ServerDiscovery`; Android 0.3x) and, as the box has no owner yet
   (`/healthz` `setUp: false`), says **"We found your new SoundStorm" - Set it
   up** (built on iPhone: scan or type the setup code, then the page's sign-up
   asks only a name and password). The server then calls itself
   "<owner>'s SoundStorm" (built, `state.ServerName`).
3. **With only a laptop:** the sticker says **"On a computer, open
   soundstorm.local"** - the web sign-up, with the setup code from the sticker.
4. **TVs and the family's phones** find "<owner>'s SoundStorm" by name and
   sign in (TVs from a phone, built).

**The models ship in the box image** (2026-10-07, the owner's asking, after
"can it be set up without internet?" - it can, and these were the gaps):
Immich's photo-search and face models (786MB), Whisper's base.en for Make an
ebook (141MB) and Storyteller's whisper.cpp and base.en for read-along syncing
(173MB). `box/models.sh` copies them out of a working install with Docker
(the build itself runs none), `build.sh` puts them in
`/var/lib/soundstorm-models`, and `storage.sh` lays each into its empty cache
folder at every boot - first boot, and after Start over empties the caches.
Whisper's volume is mounted at `/root/.cache/whisper` on the box
(`compose.box.yml`, `!override`; Debian 13's Compose is 2.26): mounted over
the whole `/root/.cache`, a folder already holding the model would stop
Docker copying in the program's own virtualenv, which lives there too.
Checked: the export from this PC; then, each from an empty cache filled from
it as the box fills it, with **no network at all**, Immich's model answered a
text search (a 512-number vector) and Whisper started and transcribed. Not
tried: Storyteller's (a whole sync), and a whole box built and booted with
them.

What the PC builds for it:

- **The setup code made when the box is prepared, and printed on the
  sticker** - today `soundstorm` writes one into `.env` at first start, which
  no sticker can know. As a QR code too: the iPhone app's scanner takes the
  code alone or any address carrying `?setup=` (`ConnectViewController.setupCode`).
- **The sticker is just three things:** the setup code (printed and QR), a QR
  to get the app, and "On a computer, open soundstorm.local". **No
  pre-printed home address** - it was considered (a box's id can be made in
  advance, as the name service holds no state) and set aside: the app never
  needs it, it fails until the box is up, some routers refuse such names, and
  the name service allows 30 new ids a day per network. Revisit only if
  `soundstorm.local` proves not enough.
- **`soundstorm.local` on the box:** the box's own system announces it over
  mDNS (avahi-daemon on the host, hostname `soundstorm`; a second box on one
  network becomes `soundstorm-2.local` by itself). This is the Bonjour that
  does not work for Docker installs - the box owns its host, so it does here.
  Macs, Windows 10 and later, Linux and iPhones resolve `.local`; Windows
  before 10 and networks blocking multicast do not (rare at home; the
  router's device list is the fallback).
- **The box answers on port 80 as well as 8099**, so `soundstorm.local` needs
  no port (compose.box.yml: publish 80 to SoundStorm's port too). After
  sign-up the page moves itself to the secure home name as it already does.
- **Android's Set it up:** the Android app's search should read `/healthz`'s
  `name` and `setUp` as the iPhone's does - show servers by name, and a box
  with no owner as "We found your new SoundStorm - Set it up" with the code
  scanned (the app has Google's scanner) or typed, opening the page with
  `?setup=`. *(Done in Android 0.48, 2026-10-08: `ServerDiscovery.Found`
  carries `/healthz`'s `name` and `setUp`; the connect screen shows a box with
  no owner as "We found your new EmberStorm" - **Set it up** (a code typed or
  Google's scanner on the sticker, the code alone or `?setup=` in an address,
  `askSetupCode`) and opens the page with `?setup=`; a TV says to set it up
  from a phone first; a server with an owner shows by its name, "Sign in".
  Checked on the emulator with a stand-in answering `setUp: false` beside the
  live server: both shown, the code asked, the box's page opened. Not seen: the
  page's sign-up with the code from the address on a real box, and the
  scanner on a real sticker.)*
- **The box's own screen** (2026-10-08, the owner asking about somebody with
  no smartphone): a monitor plugged into the box shows, in big letters, how to
  reach EmberStorm - before it has an owner, the address to open on any
  computer or tablet at home (soundstorm.local and its number), the setup code
  and a QR code of both; after, its name, the secure address, and the
  five-presses password hint (`screen.sh`, `soundstorm-screen.service` on
  tty1 in place of the login prompt; `qrencode` and Terminus fonts in the
  image, smaller letters on a monitor under 32 rows). Setting up on the box
  itself (a browser on its screen) was offered and set aside: a whole
  graphical system for the rare buyer with no phone, tablet or computer.
  Checked: both screens drawn in a container (a stand-in new box, and the
  live server read only), the QR code readable. Not seen: on a real monitor
  or in a booted box image.
- Later, from the same walkthrough (not yet decided to build): "still
  getting ready" when a box is found mid-start, "is your phone on the home
  Wi-Fi?" when nothing is found, a welcome after the first sign-in (add media,
  away from home, family, TV), and inviting family by QR.

### Bringing media in from a USB drive (2026-10-03)

Most buyers' films and music are on a drive or a laptop, and a browser over
Wi-Fi is no way to move a terabyte. On the box, a USB drive plugged in is
opened **read-only** by the box's own system (`usb.sh`, from a udev rule and
`soundstorm-usb@.service`, which goes when the drive does) as a folder under
`/run/soundstorm/drives` named after its label; SoundStorm sees it as
`/drives` (`compose.box.yml`, `rslave` so a drive plugged in later appears,
`SOUNDSTORM_DRIVES_DIR`). Nothing on a drive is written: ext journals are not
replayed (`noload`), xfs and btrfs likewise; vfat, exFAT, NTFS (ntfs3),
ext2-4, xfs, btrfs and HFS+ are opened, anything else is not. `SSDATA` and
`SSBACKUP` (kept for the backup drive) are never opened.

SoundStorm (`httpapi/drives.go`, the owner's alone: a drive may hold
anybody's files) lists drives, a drive's files (hidden and system folders
left out, links not followed, 200,000 at most), and pieces of a file (8MB,
never outside the drive). **The page plans as for a drop** - `DriveFile`
reads its pieces from the server as `AppFile` reads through the iPhone app -
so the same review, questions and taken names apply, and then hands the plan
back: `POST /api/drives/{id}/import`, and the server copies each file through
the same filing as an upload (`addFile`, split out of `handleUpload` for it:
a member's photos, dated photo folders, home videos among films, duplicates).
One import at a time, carrying on with the page closed; the page follows it
with the Android app's upload status (`DRIVE_SENDER` beside `APP_SENDER` in
`followAppUploads`), Stop included, and ends with "You can unplug the drive."
The owner's page looks every ten seconds, and a new drive is a question,
**"A drive was plugged in: SanDisk (1.2 TB). What should SoundStorm do with
it?"** (`askAboutDrive`): **Bring in what is on it**, **Use it for backups**,
**Nothing** - the likely one first: a drive with media on it (`hasMedia`, one
file some shelf keeps, looked for among its first 20,000) to bring in, an
empty one for backups, which is then the only other choice. Backups are the
box helper's and not built, so that choice shows "(coming soon)" and cannot
be taken yet; once a drive is chosen for backups it is marked and known again
(`backups` in `/api/drives`, false for now), never asked about nor offered for
import, and nothing on it is erased - backups go in a folder beside what is
there. Settings, Library has a USB drives card (only on an install with
drives, `available`). Checked:
`TestADriveIsBroughtIn` (listing, a piece, a copy where the plan said with
the drive untouched, paths off the drive refused, a member refused), and in
Chrome with a folder copied in as a drive: the message, the review (the MP3
asked music or audiobook, as for any drop), Add 3 files, each on its shelf.
*(The box half checked in the VM, 2026-10-08: an exFAT stick hotplugged while running was opened read-only under /run/soundstorm/drives, seen at /drives in the container through the rslave bind, listed, imported (an EPUB and an MP3 onto their shelves), and gone on unplugging; an ext4 drive opened with noload. `run-vm.sh` has a USB controller and a monitor socket for plugging and the power button. Not tried: NTFS, vfat, xfs, btrfs, HFS+, a drive pulled mid-import.)* Before that: **Not checked: the box half** - the udev rule, `usb.sh` and the bind's
propagation need the VM with a USB disk attached (`run-vm.sh`; QEMU's
`-device usb-storage`) or a real box: **for the PC**. Also on the box now:
port 80 published beside SoundStorm's own, so `http://soundstorm.local`
needs no port (avahi already announces the hostname); the page's move to
the secure name then goes to its https on port 80, which SoundStorm answers.
A drive's top folder named "Music" deciding its MP3s was suggested and
declined, with the audiobook rule above: names ask, they do not decide.

### Starting over, erasing, and the way back in (2026-10-03)

Three situations, the owner's design, none of them erasing by accident:

- **A forgotten owner password, or a lost sticker:** the box's power button
  pressed five times quickly opens, for fifteen minutes, setting the owner's
  password without the old one, from the sign-in screen on any device on the
  home network ("The power button on the box was pressed. Choose a new
  password for Gabriel") - once, never through the away-from-home name, with
  today's password rules, and signing the owner out everywhere. Nothing is
  erased. On a box the sign-in screen says how ("press the power button on
  the box five times quickly"). Pressed once, the button shuts the box down
  properly; held, the hardware still switches it off.
- **Start over** (Settings, Account; the owner's password and START OVER
  typed): every account, sign-in, list and setting, and what the backends
  learnt, go - the data volumes and caches emptied, their folders kept, the
  snapshots from before updates deleted; the media stays, and the box sets
  itself up again (the setup code in `.env`, the sticker's, still works) and
  reads it all back in.
- **Erase everything** (ERASE typed): that and the library's every file, the
  shelves kept empty - for a box sold or given away. What goes is said first
  ("1 account and 1.4 TB of media - 3,200 songs, 140 films...").

The caretaker does it (`internal/caretaker/reset.go`: `Reset`, the button
watch reading the power button's input device - logind told to leave the key
alone, `logind.conf.d/soundstorm-button.conf` - and `POST /reset`,
`GET /button`, `POST /button/used` on its socket, now `chown`ed to group
10001). SoundStorm reaches it at last: `compose.box.yml` binds
`/run/soundstorm-caretaker` and sets `SOUNDSTORM_CARETAKER` and `group_add`;
`httpapi/reset.go` decides who may ask. Checked: the caretaker's tests (a
reset of each kind against folders, the runs of presses, finding the button
in `/proc/bus/input/devices`), SoundStorm's against a stand-in caretaker
(the button's window, once; the word and the password), and in Chrome with a
stand-in caretaker: the hint, five presses, the new password, signing in with
it, and the erase card down to "Erasing the box". **Not checked: on the box**
- the wipe for real, the power button on the test unit (does it report
`KEY_POWER` through "Power Button"? does logind let go?), the socket's group
inside the container: **for the PC**, in the VM and then on the test unit. *(Checked in the VM, 2026-10-08, on
the 2 October image brought up to date: the socket is root:10001 and seen
in the container (the folder bound, so a restarted caretaker's socket shows
too); Start over emptied volumes and caches with folders and owners kept and
the media kept, and the box set itself up again with the same code; Erase
emptied the shelves, kept, and the starter library came back; the caretaker
found QEMU's "Power Button", logind left it alone, five quick presses
opened the password window and the new password signed in, one press shut
down properly. **Found and fixed:** the built-in models came back only at the
next boot after a reset - `Reset` now runs `storage.sh` (`Config.Prepare`)
before starting the stack; udev warned the rule file was executable (a
Windows checkout) - `build.sh` makes it and the logind and tmpfiles confs
644. With only SoundStorm, Navidrome and Audiobookshelf running, for memory.
Not checked: the whole backend set, a fresh image built end to end, the
the test unit's own button.)*

### The box reviewed blind, and the power button (2026-10-10)

Four reviewers who saw only the code (security; bugs and reliability; the
buyer's experience; making real units and real hardware) went over `box/`,
the caretaker and the box's server side before the first real unit. Their
findings, checked against the code, are being worked through in two groups -
before the first unit, then before selling (the list is in "Catching the Mac
and Linux up"'s neighbour below, kept up as it goes).

**The owner's decisions from it:** the password reset takes the **sticker's
setup code** as well as the screen's code (most buyers have no monitor);
**two quick presses** shut the box down properly and one does nothing (a PC's
button reports only that it went down, never how long - so "hold to switch
off" cannot be told apart; held four seconds the hardware still forces it
off); **Debian's security updates** for the box's own system, at night;
**Erase waits for five presses at the box** after it is confirmed in the app.

**Built:** the caretaker reads every input device sending the power key
(`powerButtons`, by the device's key bitmap: a real PC has the ACPI button,
the fixed button and sometimes Intel's HID device, and reports a press on one
of them - the VM's single device hid it), presses closer than 250ms counted
once (one press reported twice), devices looked for again every half minute;
`claimButton` takes the screen's code or the setup code from the stack's
`.env`, case, spaces and dashes aside; `POST /reset` with erase arms it
(`ArmErase`, ten minutes) and five presses then erase in place of opening the
password; the page says to press the button and follows the box going away
and coming back. The caretaker no longer waits for EmberStorm or needs
Docker at boot, so the button works through a first start's long image load
and when the data drive failed; a night update waits until
`soundstorm.service` is active. The sign-in screen says the sticker's code is
needed; the box's screen says two presses switch it off. Checked: the
caretaker's tests (`TestThePowerButtonsAreFound` on a real-PC device list,
`TestTheStickersCodeOpensTheWindowToo`, `TestErasingWaitsForTheButton`) and
the server's. Not checked: the test unit's own button.

**A missing data drive never makes a box look new** (the gravest finding:
a drive dead, loose or late at boot left the box starting afresh on its eMMC -
"set up your new EmberStorm", the family's accounts and media seemingly
gone, and the sticker's code able to claim it). Once fstab holds the data
drive's UUID, storage.sh waits up to 90 seconds for it and otherwise stops
(so Docker, which needs it, never starts and makes nothing on the eMMC);
every storage failure leaves its reason in `/run/soundstorm/storage-problem`,
which the box's screen shows ("EmberStorm cannot start... Nothing has been
erased") with how to switch it off; a box with no data drive at all says so
there too (`no-data-drive`, read by nothing before). A new drive is made
without discarding it first (`mkfs.btrfs -K`) and the unit has no start
timeout, so a big drive is not cut off part way every boot. The screen also
takes its address from the route out (never Docker's 10.231.x networks, shown
with a QR code when the cable was out), the name avahi really announces
(`soundstorm-2.local` for a second box), and strips control characters from
the server's text. Checked: the screen's two storage states drawn in a
container, recognising a box that has had a drive. Not checked: in the VM
with the drive taken away.

**Updates and resets survive a power cut** (`pending.go`). Each is written
down (`pending.json` in the caretaker's state, with the images file from
before as `images.previous.yml`) before anything stops, and taken away when
it is over; at the next start the caretaker finishes it before anything
else. An update whose new images were written is kept if EmberStorm comes up
healthy on it (held to the sources last seen, or any with none recorded),
else put back - the old images, and the volumes from the snapshot, including
when the cut came mid-rollback with the volumes already set aside. A reset
is simply done again from the start, as somebody asked for it. A snapshot
never goes onto a folder already there (btrfs would put the copy inside it,
and a rollback would have put back a folder holding the volumes): a name of
its own then. Every step has a time limit - a download 30 minutes, stopping
5, starting 15 - where a hung one held the box "updating" for good, with no
reset or next update able to begin. Checked: the caretaker's tests (an update
cut short kept, one undone after a second cut in its rollback, a reset done
again, the snapshot's name). Not checked: a real power cut in the VM.

**The box keeps up with its network, and its build has what real hardware
needs.** `address.sh` (`soundstorm-address.timer`, each minute) starts
EmberStorm again on the box's new address when the router hands it another -
it was written only at start, so every phone's saved name pointed at the old
one until the box was switched off and on - never during an update or reset
(`pending.json`), and under `up.sh`'s lock, which now keeps the caretaker and
it from starting and stopping the stack at once. It also keeps avahi to the
port the home network is on (`allow-interfaces`), as it announced
soundstorm.local with Docker's 10.231.x addresses too. Looked at in Debian's
base image rather than guessed: it already has the full kernel (not the cloud
one, which lacks eMMC and Intel graphics drivers), `MODULES=most`, Secure
Boot's shim with the `EFI/BOOT` fallback, netplan bringing up any `en*`/`eth*`
port by DHCP, the clock synced (timesyncd) and security updates on. Added:
`non-free-firmware` with Intel's microcode and graphics firmware and
Realtek's (a network chip on many mini PCs); waiting for any one port, not
all (two ports and one cable held start-up two minutes); Debian's security
updates at 00:30-01:30 (the owner's choice: at night, before the caretaker's
2-5 window), restarting at 05:30 only when one needs it (a kernel);
Ctrl-Alt-Del on a plugged-in keyboard does nothing. And `prepare.sh` no
longer swallows a failure to read `.env`, which would have written it again
with one setting and lost the box's secrets. Checked: the address check in a
Debian container (address changed and EmberStorm started, nothing the second
time, nothing during an update, avahi's setting), `set_env` refusing an
unreadable `.env`, `build.sh`'s syntax. Not checked: an image built with them
(below, all of it seen in the VM). The box's clock
is UTC, so those hours are UTC until the owner's time zone is set (a
before-selling item).

**The USB stick that installs the box** (`box/installer.sh`, the owner's
question "is this the best way?" answered yes, 2026-10-10: the eMMC is
soldered in, so a box can only be given its system by starting it from
something else - and the same stick is the factory's tool and the way a box
is put right). Made after `build.sh`, from the same Debian base: the box's
system as a raw image cut at its last partition, compressed (zstd), with its
SHA-256 and size, and `installer/rootfs/.../install.sh` run at start-up
(`emberstorm-install.service`, on the screen). It finds the eMMC (the one
`mmcblkN`, never its boot or rpmb parts, never the stick; `INSTALL_TARGET`
names another, the VM's `vda`), says what it will do and waits 20 seconds
(switching off stops it), writes every block (dd, direct), reads it all back
against the SHA-256, then **gives the copy disk IDs of its own** (`sgdisk -e
-G`, a new ext4 UUID, fstab and both grub.cfgs rewritten) - the base names
its root by PARTUUID and grub finds it by UUID, so every box and the stick
would have shared them, and a box started with the stick in could have
started the stick's system - adds an EmberStorm boot entry (the firmware's
EFI/BOOT fallback finds it anyway), and switches off. The storage drive is
kept, for putting a box right; a stick made with `FACTORY=1` wipes it
(`wipefs`) so the box starts new. A failure says so on the screen and leaves
the box on. Written to a stick of 16GB or more with balenaEtcher or Rufus.
`run-vm.sh installer` starts the VM from the stick onto a blank built-in
drive; a plain start then boots what it wrote. **Checked in the VM, end to
end**, with a quick build (`NO_IMAGES=1`): installed and switched off in
about two minutes; the written drive had new IDs throughout; it started from
its own drive, grew it to 64GB, formatted the blank storage drive, and every
service ran - waiting for any one port, the nightly security updates timed
for 00:30-01:30, Ctrl-Alt-Del masked, Intel's graphics firmware present -
and the address check set avahi to the one port (it had been ordered after
EmberStorm's first start, which a oneshot waits out: no longer). Not
checked: a real box's eMMC and firmware, Secure Boot on (OVMF here has it
off), a whole build with the images, `FACTORY=1`. **Left for putting a box
right:** `.env` lives on the eMMC, so a reinstalled box has a new setup code
that no longer matches its sticker - the per-unit provisioning (before
selling) should keep the setup code where a reinstall finds it.

**A blind security review of the box (2026-10-10, the owner's asking)**: four
reviewers shown only the code - the caretaker; the box's own scripts, units and
what the container may touch; the build, releases and the USB stick; the
server's box side (reset, the button's password, USB drives). Each finding
checked against the code. Fixed:

- **A USB stick given the storage drive's UUID could still be mounted in its
  place** (the fstab line mounts by UUID, and the label check came after), and
  with the real drive missing the box went on afresh on the eMMC. Now only an
  internal device carrying the UUID is mounted (`mount_dev`, by device),
  waited for up to 90 seconds; whatever mounted it, a drive that is not
  internal stops the box with its message. The drive is mounted `nosuid,nodev`
  (and remounted so on boxes with the old fstab line): the backends write there
  as root, and a file they leave stays a file.
- **An erase asked for in the app could not be taken back, and nothing at the
  box said it was waiting** - the owner's five presses to reset a forgotten
  password would then erase the box. The box's screen now says "ERASING THIS
  BOX WAS ASKED FOR IN THE APP" while it waits (`erase-waiting`, root only,
  as the button's code is), and the page's waiting screen has **Cancel
  erasing** (`DELETE /api/reset`, the caretaker's `POST /reset/cancel`).
- **"At home" was any connection Docker relayed**: on a box an IPv6
  connection reaches the container through Docker's proxy from the network's
  gateway, a private address, and so does anything through Tailscale's sidecar
  - so from the internet the button's window could be watched and claimed with
  the sticker's code. On a box a connection from one of the container's own
  networks is not home (`homeConnection`, `ownNetworks`); off a box (Docker
  Desktop, where every connection is the gateway) it stays as it was.
- **Every call to the caretaker left a connection open** in EmberStorm and in
  the root caretaker, on an endpoint anyone at home can ask: one client per
  server, no keep-alive, and the caretaker's socket closes idle ones.
- A release found before its expiry and installed after it is refused; the
  screen keeps only printable ASCII from the server's text (a UTF-8 C1 control
  was still a console escape) and a keyboard cannot freeze it (Ctrl-Z stopped
  it for good); an erase never writes the setup code empty; every unit no
  longer starts from the base image's saved random seed.

Checked: the caretaker's and server's tests (`TestAWaitingEraseShowsAndCanBeCancelled`,
`TestAnExpiredReleaseIsNotInstalled`, `TestARelayedConnectionIsNotFromHomeOnABox`),
and in the VM the drive mounted from its internal disk `nosuid,nodev` after a
restart. Not checked: a stick carrying the drive's UUID, a real IPv6
connection, the erase notice on a monitor.

**Then, with the owner's go-ahead: ten presses confirm an erase or a start
over** (`ResetPresses`), and five still open the password - the two no longer
share a gesture. Start over waits for the button as Erase does (`ArmReset`,
either mode; a hacked app container could otherwise wipe every account and the
update snapshots), the screen says which is waiting, the page's waiting screen
cancels either, and Settings says both wait for ten presses. Checked: the
caretaker's tests (`TestStartingOverWaitsForTheButton`; five presses with an
erase waiting open the password and erase nothing). Not seen: on a box.

**And three more from that list, the same day:**
- **EmberStorm's own image is never ":latest" in a box or a release**
  (`box/app-image.sh`, used by `build.sh` and `release.sh`): the one GitHub's
  workflow built from a commit on main (`APP_COMMIT`, default HEAD), found by
  the tag every build now gets (`sha-<commit>`, `type=sha` in publish.yml),
  pinned by digest, and checked against the provenance GitHub signs for each
  build (`actions/attest-build-provenance`, `gh attestation verify ...
  --signer-workflow .../publish.yml`) - so an image pushed with a stolen token
  cannot pass. A release, and a PRODUCTION build, refuse without that check
  (it needs `gh`, signed in once, where they run); a development build says
  it was not checked, and with no build for its commit yet uses ":latest".
- **The models are checked file by file** against `box/models.sha256`, the
  list last reviewed and committed (`box/models-list.sh`: every file's
  SHA-256 and every link, made the same in Alpine and Debian). `models.sh`
  writes the list again; the build refuses models that differ (a PRODUCTION
  build refuses with no list) and any setuid, setgid or device file. The first
  list is the 7 October models' 90 files, so it catches a change from now on,
  not one before.
- **A backup release key**: the box's `release.pub` may hold several keys,
  the release key first (`ParsePublicKeys`, `VerifyAny`, `Config.Backup`), so
  one kept in a safe can sign if the release key is lost; and a PRODUCTION
  build takes only a `box/release.pub` committed in the project and unchanged.
  **Still the owner's:** making the keys - on a hardware key or a machine that
  is never online, with a backup kept apart - then committing both public
  halves as `box/release.pub`.
Checked: GitHub built and signed the first commit with it; the lookup in WSL
found its `sha-<commit>` image and pinned the digest `:latest` then had,
refused a made-up commit, and `gh attestation verify` passed that digest and
refused an image GitHub never built for the project (a backend's);
`TestTheBackupKeySignsToo`; the model list made in Alpine matched
Debian's byte for byte, a changed whisper.cpp and a setuid file were refused.

**And three after those** (the owner's "move on to the next items"):
- **The start-up menu is locked** (`lock-grub.sh`, run once by the build and
  removed): the normal entry starts with no password (`--unrestricted`), but
  editing it, GRUB's command line, the advanced entries and the firmware entry
  need the password of a user nobody knows - 32 random bytes made in the build
  and never kept; recovery mode is gone. Putting a box right is the stick's
  job. A BIOS password and USB boot off (which would stop the stick too) and
  disk encryption stay the owner's to decide.
- **USB drives wait for the owner** (`usb.sh seen`, the caretaker's
  `drives.go`, `POST /api/drives/open`): plugged in, a drive is only noticed -
  its label, filesystem and size from blkid, nothing mounted - and opened,
  read-only as before, only once the owner taps **Bring in what is on it** in
  the app, which asks the caretaker. So a crafted stick is not read by the
  kernel's filesystem code unless the owner says so. The app's question and
  Settings' USB card show a drive not opened yet by its label and size; whether
  it holds media is known only once open, so Bring in comes first.
- **A repair stick waits for Enter** before writing (a factory stick, or one
  made with `INSTALL_AUTO=1` for the VM, counts down 20 seconds as before).
  Signing the stick's checksum waits for the real release key.
Checked in the VM on a fresh build: at the menu, `e` asked for a username and
password and refused empty ones, and the box started by itself; the built
grub.cfg had the lock and no recovery entries; a FAT stick plugged in was
noticed and not mounted, listed by the caretaker, opened read-only
(`ro,nosuid,nodev,noexec`) when asked, and cleared when pulled out; the
caretaker's test (`TestADriveIsOpenedOnlyWhenAsked`). Not seen: the page's
question for a drive not yet open (the test server has no caretaker), and the
stick's Enter on a real box.

**A second blind review of the box (2026-10-10, the owner's asking:
security, bugs and recommendations)**: five reviewers - security of the
software side and of the box side, bugs of each, and one on readiness to make
and sell units - none shown these notes; each finding checked against the
code. Fixed:
- **Updates and resets**: the caretaker stopping (a switch-off, a restart)
  while an update waited for health is left for the next start, where it was
  rolled back on a cancelled context half done; a rollback puts the old
  images back before the volumes, and recovery keeps an update only with its
  volumes and snapshot in place; a release that rolled back is not retried
  every night (`rolled-back`); no update starts while a reset waits for the
  button, and ten presses during one wait for it (they were lost); a failed
  stop starts the stack again; erase and every successful update delete the
  `-failed-*` copies of the accounts a rollback could leave; a check no
  longer overwrites an update's or reset's status.
- **The button**: wrong codes pause guessing a minute every five (50 close
  the window) - five closing it let anybody at home keep the owner out; two
  devices' reports of one press are told apart at 80ms, not 250 (quick presses
  of ten were lost).
- **USB and Settings**: one drive opened at a time, with three minutes; a
  read of a drive's file only for a plain file (a fifo held it open);
  whether a drive holds media kept five minutes; Settings' reset card shows
  an erase or start over waiting for the button, with Cancel, whenever opened.
- **The app image's provenance must name its very commit on main**
  (`--source-digest`, `--source-ref`): before, any build of the workflow
  passed, an older or a branch's relabelled. Checked: the right commit passed,
  another refused.
- **The box**: images that fail to load stop EmberStorm with a message on the
  screen (it retried for ever to download names only the box has); the data
  drive's fstab mount is waited for (two mounts could stack); internal means
  a SATA or NVMe disk not behind USB or Thunderbolt (an SD card or an NVMe
  enclosure passed); the screen reads the stack's port; the address check
  asks before the lock and follows a new router; **the start-up menu's lock
  survives a grub update** (10_linux diverted with dpkg and filtered, where an
  update replacing the edited file would have asked every start for a password
  nobody knows - checked by reinstalling grub-common in the VM); the backends'
  database passwords and Storyteller's key are made per box on its first start
  (every box shared the compose defaults; a database already made keeps its
  own); a USB network adapter is never the box's network
  (`05-emberstorm-no-usb-network.network`); the log on the eMMC is capped
  (200MB, a month); compressed swap in memory (zram, a quarter); a monthly
  scrub of the data drive.
- **The stick**: it has disk IDs of its own (it shared them with the copy it
  writes, so after an install cut short it could start that copy); the table
  is read again before the IDs are; old EmberStorm boot entries go before a
  new one; a factory stick wipes SATA storage drives too.
Checked: the caretaker's and server's tests (a shutdown mid-update left for
the next start, a rolled-back release not offered again, ten presses waiting
for a busy box and leaving no account copies, the code pause); in the VM, a
fresh image and stick installed and started - one mount `nosuid,nodev`, zram
on, the journal capped, the three secrets made, the menu open after
`apt-get install --reinstall grub-common`. Not checked: images failing to
load, a USB network adapter, the stick after a cut install.

**Then two from that list, the owner's "do 4 and 2":**
- **The box keeps its owner's time zone** (caretaker `timezone.go`, `GET`/`PUT
  /timezone`; the server's `/api/box/timezone`, owner only; Settings, Devices,
  **Time zone**). A box came up in UTC, so its 2-5 update window, Debian's
  security updates and their 05:30 restart fell in a US family's evening. The
  first time the owner opens Settings on a box still in UTC, the page sets the
  box to the browser's time zone; never by itself after that (an owner opening
  the app on holiday does not move the box), but the card offers "Use this
  device's" when they differ. The caretaker sets it with timedatectl - only a
  name tz knows (Go's own zone data built in) - and reads it afresh for the
  night's window; systemd's timers and the restart follow the system zone.
- **Each box has an identity of its own, kept on its storage drive**
  (`prepare.sh`: `/srv/soundstorm/identity/box.env`, root's alone): the setup
  code, a serial (`EM-XXXX-XXXX`, letters and digits nobody misreads) and the
  three backend secrets made per box. `.env` on the system disk is written from
  it at every start, so a repair with the stick keeps the sticker's code and
  the databases' passwords - **the per-box database passwords added earlier
  the same day would otherwise have been lost by a repair**, breaking the
  photo and moods databases. Seeded from the factory stick's
  `/etc/soundstorm/identity.env`, else a box's existing `.env` (a box from
  before keeps its code and the compose default its database was made with),
  else made new. **A factory stick makes the identity** and shows it at the
  end - serial, setup code and a QR code - for two minutes, and keeps it on
  the stick in `units.csv`; `box/sticker.sh` makes each unit's 90 x 50 mm
  sticker (SVG) from that list: the serial, the setup code, a QR to set up
  (`http://soundstorm.local/?setup=CODE`), a QR to get the app, and "On a
  computer, open soundstorm.local". units.csv belongs with the factory's
  private records, never this repository. Settings, About shows the serial to
  the owner (`boxUnit`, for support).
Checked: the caretaker's test (`TestTheTimeZoneIsTheOwners`: bad names
refused before anything runs, a real one set and read back); the identity's
rules in a Debian container - a new box, a repair keeping the code and the
database password, the factory's, a box from before, and a second run
changing nothing; a sample sticker drawn, and both its QR codes decoded to the
right addresses. Then in the VM, end to end: a factory stick installed, the
box started with the stick's code and serial on its storage drive and in
`.env`, the time zone set to America/Chicago through the caretaker (a path
refused), and Debian's update timer then at 01:05 Chicago time. **Found
there:** the factory wipe took the VM's CD drive for a storage drive (it is
on SATA too) and stopped; it takes disks only now. Not seen: the page's
time-zone card, a real sticker printer.
**The setup code lives only on the label under the box** (the owner's
design: one QR code on each surface - the outside of the box and a card
inside open a page of instructions, the same for every box; the label under
the device carries that box's own code). So the owner sees it in Settings,
About, and beside Start over, which needs it again (`boxSetupCode` in the
session, owner on a box only), and `units.csv` is the support record by
serial. Labels chosen: 2 x 2 inch matte white waterproof film for the
device, 2.5 x 2.5 inch rounded-square matte cards for the box, printed on a
mono laser through Tray 1. `box/sticker.sh` prints them: `labels units.csv`
(Avery 64510, 12 a sheet, 3 x 4 from 0.625", rows 2.5833" apart), `cards`
(Avery 35703, 9 a sheet, 3 x 3 from 0.25"/0.875", 2.75" and 3.375" apart -
both measured from Avery's own template PDFs) and `--test` for a plain-paper
alignment sheet; each an HTML page sized to Letter with no margins, printed
from Chrome at 100%. The card's QR code opens `web/start.html`
(emberstorm.app/start: plug in, get the app or open soundstorm.local, the
code under the box). Checked: Chrome's PDF of each is Letter, 14 boxes made
two label sheets, and all 12 labels and 9 cards decoded (zbarimg at 300 dpi)
to their own addresses. Not checked: a real sheet through the printer -
35703 is 247 g/m2 card, heavier than the LaserJet's tray is rated for.
**The owner's LaserJet squeezes a page about 1.1% across and not down**
(the card test's right column at 5 11/16" for 5 3/4", its bottom row true):
print with `MEASURED_X=5.6875` for cards and `MEASURED_X=5.8111` for labels.
**The ledger: every box made, in the private repo** (the owner's asking: what
support looks up and labels are reprinted from). A factory stick keeps its
list on a FAT partition of its own, `EMBERSTORM` (installer.sh adds it;
Windows opens it as a drive, where it hides the EFI one and cannot read
ext4), a row per box: serial, setup code, when, the stick, the maker's name
and serial number, the network port's MAC, the built-in drive's size and the
storage drives - an install that cannot write its row stops. `box/units-add.sh
/mnt/e/units.csv` adds a stick's rows to `emberstorm-private/units/units.csv`
(0600; a serial seen before is passed over, one with another code stops
everything), and `sticker.sh reprint EM-XXXX-XXXX` prints a box's label again
from it. Customers and orders stay out of it (personal information: the shop
system, joined by the serial). Checked: units-add's new, repeated, old-stick
and clashing lists; reprint and a missing serial; in the VM a factory install
with `KEEP_STICK=1` (run-vm.sh keeps what a run writes to the stick) left its
row on partition 2. Not checked: Windows opening the partition on a real
stick.

**From the readiness review, not built** (the owner's to weigh, biggest
first): **the box's own system cannot be updated** - a release carries only
images, so a fix to the caretaker, the compose files or a script needs the
stick at the customer's (a signed bundle of the box's files, or A/B system
partitions); **a factory test** (drive health, network, the button, every
service up); **memory** - no limits on fifteen services,
to be measured on the unit; **drive health** (SMART, eMMC wear) shown in the
app; **updates in the app** (version, what failed, Auto); **staged rollout and
reports back** (every box updates the same night, nothing reports); **a
support bundle**; **backups** (a USB drive nightly, "not protected" said
plainly); **a real wipe** for resale (blkdiscard or nvme format); **licence
notices and a source offer** for the images, firmware and models shipped.

**Left for the owner** (from the same review): **the release key** is a plain file on the build machine (sign offline
or with a hardware key, before the first box);
**somebody holding the box** can boot another system from USB, and nothing
is encrypted; the stick's checksum is not signed (it waits for the key); Debian's base image is checked against
its checksums over HTTPS but not their signature; a compromised version can
make later updates roll back by breaking backends (the health baseline); the
button's window can be closed by anyone at home with five wrong codes;
`/api/reset/button` says it is a box to anyone; a USB device sending the power
key counts as the button; Thunderbolt or eSATA disks pass as internal.

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
   user and sessions. Both outlive a restart. See `internal/state` — which holds
   credentials, accounts, and one flag recording that the starter library has
   unpacked. Never anything about the media itself; that is the line that
   matters, and it is the reason there is no index in there to go stale.

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
under us) and a 2.5GB image next to Navidrome's 348MB. That trade pays off
because SoundStorm uses Jellyfin's transcoding: video a browser cannot decode is
transcoded on the fly and served as HLS (see Gotchas).

## Television, show by show

Browsing the TV shelf lists shows only (`IncludeItemTypes=Series` when there
is no search text); a search still finds an episode by name. A show opens its
page (`GET /api/tv/show`, `source.ShowBrowser` over Jellyfin's
`/Shows/{id}/Episodes`): the episodes season by season, each with this
person's place from `state.Progress` (a bar part-watched, a tick past 93%),
and one button for what to watch now - carry on, the one after the last
finished, or the first. An episode Jellyfin found nothing about is named
after its file; the title part is shown. When an episode ends, Up next
counts eight seconds into the next (from the page's list, else
`GET /api/tv/next`), Play now and Cancel beside it. Before this a TV item
fell through `play()` to the audio player - episodes played as sound only.

**Film quality is per device** (Playback on this device, `?vq=` on
`/api/playback`, `httpapi/filmquality.go`). Films were capped at 20 Mbit,
under a Blu-ray rip's ~40, so Jellyfin re-encoded every frame (libx264
veryfast, crf 23 - seen in its log for the owner's Long Road North rip, an
H.264 1080p picture any browser plays) and sent stereo. Now: **smart** (the
default, and what a client that does not ask gets - the Apple TV included) is
the original at home and 20 Mbit from an away-from-home name; **always
original**; **standard** (20 Mbit); **data saver** (4 Mbit, 720p, stereo). Under
the cap Jellyfin copies the picture untouched (its stream copy, on by default),
far less work than re-encoding; sound a browser cannot play (TrueHD, DTS) is
still converted, to AAC with up to 5.1 channels. Downloads take standard: an
original Blu-ray picture would be tens of gigabytes on a device - unless
chosen: **downloading a film asks** (`renderDownloadMenu`) Standard (20 Mbps,
"up to N GB" from its length), Data saver (720p, about 4.3 Mbps with sound)
or Original, then which audio track when the file has more than one (the main
one asks nothing extra, so a film kept as it is stays as it is). Download all
for a shelf keeps Standard. Blu-ray subtitles are pictures (PGS) and are not
offered.
**The player has a Quality picker too** (beside Subtitles and Audio, the
owner's design): it starts at the device's setting, and a change plays on from
the same moment at the same audio track and holds for the sitting - across
episodes that Up next rolls into - until the player is closed
(`state.videoQuality`, cleared in `closeVideo`). Hidden for a downloaded film.
**The Audio picker says what a track is** (`audioLabel`): language, format and
channels - "English · Dolby TrueHD Atmos 7.1" - with the file's own name for it
only when that adds something (a commentary's), since a Blu-ray names its
tracks "Surround 7.1", "Surround 5.1" and "Stereo" and the picker was a list of
lookalikes. A track converted to fewer channels than it has says so, "(plays
as 5.1)": the owner asked whether it was lying to them about 7.1.
**Each play is its own conversion** (`playSessionId` from PlaybackInfo in the
HLS query). Jellyfin names a conversion's files after the file, the device and
the play session - not the quality or audio track - and SoundStorm is always
one device, so every play of a film shared one conversion: the first check of
film quality found the projector served the old 20 Mbit pieces, no new ffmpeg
run at all, and another language could have got the old track's pieces.

A file with more than one audio language gets an Audio picker beside
Subtitles. Choosing one asks `/api/playback?audio=<stream index>`: a browser
plays only a file's first audio track, so another language is always Jellyfin
streaming HLS with `AudioStreamIndex`, and it plays on from the same moment.
Checked end to end on a throwaway Jellyfin 12.1.0 with a generated show:
seasons, Up next into season 2, and English to Spanish. Skipping intros is
not built: Jellyfin knows where they are only with a plugin.

**Episodes named and put in order** (2026-10-09, the owner's asking: a
ripped disc of a show came in as "A1_t05", "B1_t00", "C1_t01"..., and
Jellyfin, which matches an episode by "S01E01" in its name, showed the codes
and found nothing). `library/episodes.go`: a show's videos are looked at
together (`SuggestEpisodes`) - one already named "S01E03" or "1x03" keeps it;
one much shorter than the rest (under 40% of the middle one) is an extra, for
the show's `extras/` folder; the longest, when it is as long as the others
together (within 8%, those already numbered beside it counted), is a play-all
and not kept; the rest are episodes in rip order (the folder, then the
ripper's title number, then the name, numbers as numbers), numbered on from
the show's last episode (S01E01 for a new show) and named
"Show/Season 01/Show S01E01.mkv". Lengths when known (Jellyfin's), else
sizes. **At upload** a TV drop with no numbered video is numbered in the plan
(`numberDrop`, after `discTitles`): the review shows the new names, a
play-all is left out with its reason, subtitles follow their video, and a
second disc carries on from the first. **Afterwards**, a show's page and its
hold menu have **Number the episodes** (owner only; `httpapi/episodes.go`,
`GET`/`POST /api/tv/numbering`): every file with its length, what it is
(Episode, Extra, Play all, Don't keep), its season and episode, the new name,
arrows to reorder (the episodes renumber in order from the start at the
top), two alike refused, and Apply (`ApplyEpisodes`): targets worked out
again on the server, every file inside the show's folder, moved aside first
so two can swap, companions with them, play-alls to the bin, emptied folders
gone, the TV shelf rescanned. Checked: the library's tests on the Avatar
disc's real sizes and lengths (episodes 1-4, the play-all, the extra; a
second disc on from E05; a swap; a clash refused; a file outside refused),
the upload plan through the test server, and in Chrome on a fake ripped disc
there: the suggestion right, C2 moved below C3, Apply, the files renamed (the
swap confirmed by size), the play-all in the bin, Jellyfin then listing
S1 E1-E4. A file the server cannot rename (a folder only root may change, on
Linux) fails with "permission denied". Not done: the owner's own show
(their files: renaming waits for their go-ahead).
**Then dragging, and the episode guide** (the same day, the owner's asking):
each row has a handle to drag it by (the others make way, the sheet scrolls
at its edges, letting go renumbers; the arrows stay for a keyboard), and
**Check against the episode guide** asks TVmaze - free, no key or account -
for the show's episodes (`internal/episodeguide`, `GET /api/tv/guide`, owner
only, limited): by the IMDb or TheTVDB number Jellyfin found
(`source.ProviderIDer`), else by name, which the sheet says may be another
show. Asked only on the tap - it tells TVmaze which show this is - and kept
a day. Each episode row then shows the episode its number is ("The Tide Turns"), or
that the guide has no such episode, and a file far off the guide's length
(over 1.6 times: two episodes in one; under 0.4: an extra?) - loosely, as
a guide's length is often the slot (30 minutes for a 23-minute cartoon);
most files off says it may be another show of the name (a remake). Nothing
is renamed by the guide. Jellyfin's own database episode lists were not
used: it lists episodes only for files it has. Checked on the test server
with a fake ripped show: the first row dragged below the second swapped
their numbers, and the guide named the four - and showed Jellyfin had taken
the show for the 2024 remake, its hour-long episodes against 23-minute
files (the owner's own show too: Find the right show first).

## One backend, two sources

Films and series are separate Jellyfin libraries - different collection types,
different scrapers, seasons and episodes only for the latter - so Jellyfin is
provisioned once and registered as two sources sharing one token: `jellyfin`
(Movie) and `jellyfin-tv` (Series,Episode).

`source.Source` always said "a server that serves two kinds is configured as two
sources", and that was not actually possible until `jellyfin.Config` grew a
Kind and ItemTypes; the adapter hardcoded `KindVideo`. `provision.buildSources`
now returns a slice for this reason.

## The shelf layout is enforced, not hoped for

Music is always `Artist/Album/track` and audiobooks are always `Author/Title/part`.
A loose track used to land at the top of `music/`, which is untidy rather than
broken - Navidrome reads tags, not paths - but it is exactly the mess the
folders exist to prevent, and it accumulates one file at a time.

`internal/tags` reads just enough to know where to file something: album
artist, artist, album, title, from ID3v2 in MP3, iTunes atoms in M4A/MP4, and
Vorbis comments in FLAC. It is not a tag library and must not become one.
Everything else about a file - duration, artwork, replay gain - belongs to the
backends, which are much better at it. Same shape as `internal/epub` and
`internal/pdf`: read the little that answers one question, no dependencies.

Three things about it that are easy to get wrong and were verified against a
real file rather than reasoned about:

- **ID3 frame sizes are plain integers in 2.3 and syncsafe in 2.4.** Read one
  as the other and the walk falls off the end of the first frame.
- **Unsynchronization** rewrites every `0xFF 0x00` pair so no part of a tag can
  look like an audio frame. Left undone, every length after the first such pair
  is wrong. The first real file tested had the flag set.
- **MP4's `meta` is a full atom**: four bytes of version and flags before its
  children, which its siblings do not have. Descending without skipping them
  lands mid-atom and finds nothing, which is the usual reason an M4A looks
  untagged.

**The shelf is always exactly two levels, whatever shape the drop had - and
that was a reversal.** The first rule filled in only the missing levels and
left anything two folders deep alone, on the reasoning that a folder somebody
chose beats a tag. Then a drop of Libation's `Books` folder -
`Books/<Title> [ASIN]/<file>.m4b` - filed ninety-four audiobooks under an
author called "Books". Whatever sits above an album or book in a drop is at
least as often a container as a name; the tags named the author correctly for
all 112 books in that drop.

So now:

- **The album or book folder keeps its dropped name** when it had one.
  `Small Steps [1000000001]` beats the tag's `Small Steps (Unabridged)`,
  and the ASIN keeps two editions apart. Only a loose file takes its album
  from the tags. That half of the old reasoning was right.
- **The artist or author comes from the tags.** For audiobooks the artist tag
  is the author on every part, so it wins. For music it is the performer,
  which varies across a compilation, so the order is album artist, then the
  folder the album was dropped in, then the track artist - otherwise a
  compilation without an album-artist tag scatters into a folder per guest.
- **Without tags, the folder above is used unless it is a container** -
  `Books`, `Music`, `Downloads` and so on, a short list in `intake.go` - in
  which case it is `Unknown Author`, somewhere obvious to be found and fixed.
- **Disc folders are kept inside the album** (`CD1`, `Disc 2`), or every CD
  rip of an audiobook would become a book called "CD1".
- Everything else in front of the artist - `Music/Rock/` - is discarded.

**Plan and Save share the rule**, and that matters: Plan runs it with empty
tags because the bytes have not arrived, Save runs it again with the file's
own. They agree for an untagged file, and where they differ Save is better
informed than the prediction - never worse. With the author now coming from
tags, they differ more often: the Libation drop is planned under
`Unknown Author` and saved under the real one. So the drop panel replaces its
planned destination with the one the upload returns, rather than leaving the
prediction on screen next to a tick.

The 112 books already filed under `Books/` were moved by the same rule -
whole book folders, companions included - and Audiobookshelf took them as
moves ("0 Added"), so listening positions survived.

Films and television are not restructured. Jellyfin matches on the *name*, not
the depth, and anything dropped there is already folder-shaped; imposing a
layout would be inventing one.

**Films' names are tidied, though** (2026-10-07, the owner's report: some
films had a frame of the video for a cover). Jellyfin finds a film's poster by
its name, and a disc ripper's names - "The Quiet Meridian_t00.mkv", "The Lord
of the Rings- The Second Crossing (EXT.) PT. 1_t01.mkv" - left half the owner's
films unrecognised. As a film is filed (`library/filmnames.go`, `tidyFilm`,
from both `shelvePath` and `shelveUnder`, so plan and save agree): a ripper's
`_t00` comes off, "PT. 1", "Part 2", "CD1", "Disc 2" at the end become
Jellyfin's " - part1", "Rings- The" gets its space back, and a loose film goes
in a folder of its own name, which is what lets two parts or two versions show
as one film. Films dropped in a folder keep it; companions follow by the same
rule; television is left alone. No year is added (it is not known before the
lookup): "Find the right film" is there for what still does not match.
**A disc's other titles are not the film** (2026-10-09, the owner's report:
"the server thinks all the extras are duplicates"): MakeMKV names every title
alike, so taking `_t00` off each made all 31 of a Blu-ray "The Meridian
Supremacy.mkv" - the first to arrive (a 292MB extra) took the name and the
film and the rest were refused. The page now sends each file's size with the
plan (`sizes`), and where a drop holds several titles of one film
(`library.discTitles`, `PlanSized`) the largest is the film, one half its
size or more a second version beside it ("Name - t12.mkv"), and the rest go
to "Name/extras/Name - t01.mkv", which Jellyfin shows as the film's extras;
a title's subtitles follow it. The upload carries the planned name
(`Placement.Upload`, the page's `item.upload || item.path`), as the server,
seeing one file at a time, cannot tell film from extra; `tidyFilm` leaves it
as it is. `TestADiscsTitlesAreNotAllTheFilm`. **And Firefox 157.0.1** (the
Microsoft Store's, updated 2026-10-07) drops large uploads to the server
part way - hundreds of MB then the connection reset, "connection lost" on
the page, for a 253MB file too - where Chrome and an earlier Firefox (155,
tested) send them; not the server's. Sending big files in pieces would get
round any such browser fault - **and now does** (the same day, the owner's
asking): a file over 64MB goes in 8MB pieces (`httpapi/pieces.go`: POST
`/api/upload/pieces` starts it, PUT `.../{id}?offset=` sends a piece, GET says
how far it got, DELETE stops it; the page's `uploadInPieces`), a failed piece
sent again after 2, 4 ... 30 seconds for about five minutes, and once all are
in the file is filed by `addFile` as any upload is, handed a `library.Staged`
so it is moved into place, not copied again. Unfinished uploads live in
memory (eight an account, forgotten after six idle hours, swept at a restart);
the Android and iPhone apps' own background uploads are unchanged. Checked:
`TestAFileSentInPiecesIsFiled` and `TestAnUploadInPiecesIsItsOwners`, and the
page's own code in Firefox 155 sending 150MB to the test server with its first
piece cut on purpose - 20 requests, the same SHA-256 on the server. Not tried:
Firefox 157 itself, and a whole Blu-ray. **And an extra dropped after its film**
(a drop of its own, which the plan cannot see with the film) goes beside it,
by the size of what holds the film's name - much smaller to `extras/`, about
as large a second version, exactly its size left for the copy check -
in the plan and on arrival (`titleBeside`);
`TestAnExtraAfterItsFilmGoesBesideIt`. **Then named for what they are** (the same day, the owner's asking: "t00"
meant nothing): as each numbered title arrives (`labelTitle`, after the bytes
are in, since the plan knows only sizes), a second version is named by what
differs from the film - its picture size, else its length ("Name - 2h 24m.mkv",
the label Jellyfin shows for the version) - and an extra by its length
("extras/Name - Extra (12 min).mkv", "Extra 2 (12 min)" when two match). A
subtitle follows its title whichever arrives first (`titleRenames`, in memory
while the server runs); a title nothing can be read from keeps its number.
`TestADiscsTitlesAreNamedForWhatTheyAre`.
**Find the right film** (a film's hold menu, owner only; `filmmatch.go`,
`source.FilmMatcher`, `jellyfin/identify.go`): Jellyfin's own Identify - `POST
/Items/RemoteSearch/Movie` by name and year (TheMovieDb and the Open Movie
Database, the same film from both offered once), then `POST
/Items/RemoteSearch/Apply/{id}?ReplaceAllImages=true` with the answer exactly
as found: the answers stay on the server, kept per search's words, and the
page picks one by its place (two searches can be on their way at once - the
first test applied the wrong list). Posters come through the server
(`/api/films/poster`, only `image.tmdb.org/t/p/` at w342 and
`m.media-amazon.com/images/`, no redirects). The file is never touched. A
film's cover address carries its picture's version now (`<id>_<tag>`, as
Navidrome's do), or the week-long browser cache kept the old frame. **A film
in parts plays as one** (`jellyfin/parts.go`): Jellyfin makes the first part
the film and hides the rest from every lookup but `GET
/Videos/{id}/AdditionalParts`; the playback answer lists `parts` (id and
length, the film's own first), a later part is playable once its film has been
asked about (`partsOf`), deleting or moving takes every part, and the player
plays them in turn - its place counted across the whole film, starting in the
part the saved place is in (`playVideo` options `parts`, `part`, `at`;
`startWatching`'s `offset` and `total`). The timeline is the part's. Checked:
the names' tests; on the test server, Find the right film turned the pretend
film into Big Buck Bunny (2008) with a new cover; a two-part film made there
started at its saved place in part 1, went on into part 2 by itself, and saved
1:18 for 38 seconds into part 2. **The owner's eight files were renamed by
hand, with their years** (asked for, places lost knowingly): "The Meridian
Identity (2002)/The Quiet Meridian (2002).mkv" and "... - extra audio
track.mkv" (two copies of the film, 33GB each, the second with one more
English track - shown as one film with two versions), and each Lord of the
Rings as "The Long Road North - The Second Crossing (2002)/... - part1.mkv" and
"part2.mkv" ("(EXT.)" left out). All four then matched, with posters. Not
checked on the owner's screens: their films playing across the parts.
**Find the right show, and Choose a poster** (2026-10-09, the owner's
asking): a TV show's hold menu has Find the right show - the same lookup, the
television source already asking Jellyfin's `RemoteSearch/Series` - and films
and shows alike have **Choose a poster** (owner only): the posters the online
databases have for a title already identified (`source.PosterChooser`,
Jellyfin's `GET /Items/{id}/RemoteImages?type=Primary`, every language,
those a browser can be shown through the server only, 60 at most) in a grid
with each one's language, and a tap has Jellyfin fetch it as the poster
(`POST /Items/{id}/RemoteImages/Download`). The list stays on the server and
the page picks by place, as with matches (`posterChoices`). Checked on the
test server: twelve posters for Big Buck Bunny from TheMovieDb, each shown
through the server, two chosen in turn, the cover's version tag changing
each time; `TestAPosterIsChosen`. Not seen: the grid on a phone, a real show.
**And placed by hand** (the same day, the owner's asking): covers on the
shelf are square, so a tapped poster opens in the same square frame as any
cover (`squarePicture`: drag, pinch or scroll, twist), fetched larger for it
(`/api/films/poster?size=large`, TheMovieDb's w780), and Use makes the framing
the cover for everyone (`PUT .../poster`, as Choose a picture instead does).
Checked on the test server in Chrome: menu, poster, frame zoomed and dragged,
Use, the cover's tag changed, no page error. `ChoosePoster` on the server is
no longer used by the page (Jellyfin's whole poster, framed at the middle).
**And an episode's picture** (the same day, the owner's asking): an episode
row on a show's page has a hold (or right-click) menu now (`attachHoldMenu`;
the lift that ends a hold does not play it), and for the owner **Choose a
picture**: the databases' stills for that episode, in two columns, or Choose a
picture instead (the owner's own or a photo of theirs on the server), placed
in a **16:9 frame** - `squarePicture(source, aspect)` takes any shape now, its
covering and limits worked out for a rectangle turned (`reachU`/`reachV`),
1280px across for a wide one - and set for everyone (`setPosterFor`, the same
`PUT .../poster`; the show's page refreshes). Checked on the test server with
a generated one-episode show: hold, Choose a picture (none found), one of the
server's photos in a 780x439 frame, Use, the episode's picture changed; the
square frame checked again after. Episodes of a show identified wrongly have
no stills (the owner's show, set to a remake of the same name: none for its
episodes).
**And anybody can give a film or show a cover of their own** (the owner's
asking, the same day: choosing the right film is the owner's, as it changes
it for everyone, but a picture of one's own is anybody's, as for music):
Change cover in a film's or show's hold menu (`renderFilmCoverMenu`), kept as
covers of one's own are (`collections/art.go`) under `art:<source>/<film
id>` - keyed by the film itself, so it works for a film with no picture at
all and is not undone by a poster found later (`artPath`; `artKeyFor` drops
Jellyfin's `_<version>` as it drops Navidrome's). Checked on the test server
as a member made for it: Change cover in her menu and no Find the right film,
her picture shown to her, the owner still seeing the poster.
**And Find the right film can take the owner's own picture** ("Choose a
picture instead", the owner's asking): picked and squared as any cover, then
`PUT /api/films/{source}/{id}/poster` (owner, 4MB, JPEG, PNG or WebP by its
bytes) makes it the film's poster for everyone - Jellyfin's `POST
/Items/{id}/Images/Primary` with the picture base64-encoded as the body
(`source.PosterSetter`; `httpx.Request.Raw` for a body neither JSON nor a
form). Somebody's own cover still wins for them. Checked on the test server:
a picture uploaded, the film's cover address changed, and the cover served
was that picture.

**Ebooks are Author/Title too, and used to be flat.** Flat made sense while an
epub describes itself and nothing reads the folders - until a Calibre library
arrived as `Author/Title (id)/` with its `metadata.opf` and `cover.jpg`, and
every other upload landed loose beside it. Calibre's shape is the one worth
matching: it is the same as the other shelves, and flattening it would part
1,664 books from the sidecars that carry their curated metadata and covers.

The rule is the music one, not the audiobook one: **the folder above the book
first**, because a Calibre author folder is curated and the name inside an
epub is often the sort form ("Herbert, Frank"); then the book's own metadata;
then the file name, which for a PDF is often all there is ("Title - Author
(2017).pdf", parsed by `internal/pdf`). The plan uses the file name, since it
runs before the bytes arrive. The staged file is called `part-123456789`, so
it is always the *dropped* name that is parsed - read the staged one and every
untagged PDF would be filed under a title of digits.

**What describes itself is per shelf, not per extension.** A `.pdf` is a book
on the ebook shelf and a companion on the audiobook one; counted as
self-describing everywhere, an Audible PDF would read its own metadata and
land under a different author from its m4b - the bug the companion rule
exists to prevent. The client's upload order follows the same split.

A container folder directly above a loose file - `Books/Dune.epub`,
`Music/track.mp3` - is not taken for the book or album either. That gap was in
the first version of the two-level rule and was caught writing this one.

Tags are somebody else's text, so they go through `cleanRelPath` like any
other path, and the separators in them are replaced rather than refused - an
album really is called "AC/DC Live". `../../etc/passwd` as an album name
becomes the single harmless segment `..-..-etc-passwd`.

## Dropping files in

Dragging onto the window does what dragging into the folder would have done,
including working out which folder that was. `internal/library/intake.go` owns
the decision; `internal/httpapi` owns the two endpoints.

**Two steps, not one multipart request.** `POST /api/upload/plan` takes a list
of paths and answers where each would go; `PUT /api/upload?path=&kind=` takes
one file as the whole request body. The split buys three things a streamed
multipart upload cannot have: the UI can say "14 files, 2 skipped, all going to
Films" before a gigabyte moves, the grouping can see the whole list, and the
body being nothing but the file means no parser between the socket and the disk.

**Placement is per dropped item, not per file.** A film folder holds an .mkv, a
.srt and a poster; the subtitle is useless in the ebook shelf and invisible
anywhere but beside its film. So the first file in a group that can name a shelf
decides for all of them, and companions - subtitles, artwork, .nfo, .opf - get
no vote and inherit the answer. A folder of nothing but companions names no
shelf and is skipped, which is right: a lone .srt has no home.

**A drop that mixes shelves is split where its subfolders disagree**
(`splitGroup`, 2026-10-01). One shelf per dropped folder sent a folder of
everything - music, a film, a show, an audiobook, an ebook, phone photos -
wholly to Audiobooks, because one subfolder was called that and the first
clear file decides; and an epub beside its m4b went with whichever came
first (both found by asking how a giant folder would go). Now a folder's
subfolders and loose files are each decided (`vote`), and where two clearly
name different shelves it is split into them, each split again the same way
down to eight levels; where they agree, or only one says anything, it stays
whole and is decided as before. Not a vote: a question, files naming no
shelf (subtitles, a lone PDF that would be asked about), a loose video beside
other things (an album's bonus video), and pictures unless a folder of them
plainly is photos (named so, camera names, or 50 or more) - so an album with
its scans, a film with its subtitles and poster, and an audiobook with its
booklet stay whole (`split_test.go`). A split part's group is its folder
("Everything/Phone"), which is also what a question names.

**Nothing moves until it has been seen** (the owner's asking, 2026-10-01).
Once the questions are answered the drop panel shows a line per part of the
drop - "Everything/Movies -> Films - 3 files" - each with **Change**, and an
**Add** button (`reviewPlan`); every file is behind Show every file. Change
sends the part's group back as a choice like a question's answer, which the
plan now takes for any part (`permitted`), skipping the files the new shelf
does not keep (`shelfTakes`: "that library does not keep .epub files").
After uploading, the same lines say where each part really landed
(`renderLanded`, from the destinations the uploads return - a home video
moved to the photos shows there).

**The sheet, and stopping** (2026-10-02, the owner's asking): the drop panel
is a sheet over any page (`#intake`, fixed; Settings used to hide it, and it
sat unseen at the top of a scrolled page), a part per row with Change at its
right. While files go up, Close reads **Hide** - they carry on, and a chip
says "Adding 12 of 40..." until tapped (`intakeChip`) - and **Stop** lets the
file going up go (the request aborted; the server keeps nothing of it) and
sends no more; what arrived stays, and the summary counts the rest as not
sent. Checked at phone size with three files on a slowed upload.

**Share > SoundStorm adds to the library** (Android 0.33, `Shared.kt`, the
owner's asking): the app is in the system's Share sheet for audio, video,
pictures, EPUB, PDF and zips. What is shared is copied into the app's own
storage (a share only lends a file while the app is open, and it goes up in
the background after), then handed to the page as AppFiles - the same
`window.__soundstormPicked` the iPhone app's picker uses, through
`window.__soundstormShared`, which waits until somebody is signed in - so it
gets the same review as Add media, the page reading the little it needs
through `soundstormApp.readFile`, and Add hands it to the background sender.
A copy is deleted once sent, or after a week. Names come from the provider's
display name, asked for by column - asked for everything, the media store
answered nothing and the file was named "24" - with an extension from its
type when a name has none. Checked on the emulator from the Files app's
Share: SoundStorm in the sheet, the review ("-> Ebooks"), added in the
background. **For the Mac:** the iPhone wants a Share extension doing the
same (Universal Links aside, see the list at the top).

**In the Android app the app sends them, in the background** (0.30,
`Uploads.kt`, the owner's asking: files stopped when the app was left). The
file picker is now the documents picker (`ACTION_OPEN_DOCUMENT`), whose
files the app may keep reading (a persistable permission), and the app
remembers each picked file by name and size. At Add, the page hands the app
the plan (`soundstormApp.uploads('add')`: name, size, path, kind, taken);
every file matched, a WorkManager job sends them one at a time to the page's
own server with the web view's cookie, in the foreground with a notification
("Adding 3 of 12", a Stop), and the page shows its progress from the app's
status (`followAppUploads`) - and picks it up again when reopened
(`resumeAppUploads`). Anything not matched is sent by the page as before. A
dropped connection or a busy server retries the file from its start; signed
out stops with a reason. **Only on Wi-Fi** and **Only while charging** sit
in Add media (both off by default: files added are wanted now), and files
waiting say what they wait for. Checked on the emulator: three films added
while waiting to charge, the app swiped away (its process ended), the
charger plugged in - all three arrived without the app open.

**A taken name is decided before anything is sent** (2026-10-02, the owner's
design, after Windows' copy dialog). Before the review, the page asks
`POST /api/upload/check` which planned files would land on a name already
used and what is there (`library.CheckDest`), and compares a **Sample** -
SHA-256 over the length and the first, middle and last megabyte, the same in
Go (`library.Sample`) and the page (`fileSample`, with a SHA-256 of its own
for plain http, where browsers have no crypto.subtle). An exact copy is
skipped there and never sent ("already in your library"); a photo counts as
one when it matches any file of its size in the person's dated folders. A
different file with the name is asked about once for all - **Keep both**
(the default), **Skip**, or **Replace** (the owner only, as deleting is:
the old one to the bin) - with **Choose for each file** opening a choice per
file (`conflictChoice`).

**Kept both, the new one is named for what makes it different - never
"(2)"**, which reads as "a copy of" (the owner's point: it is a separate
file). `library/distinct.go` reads the little that tells two files of one
name apart (`Traits`) and names the new one "Name - <label>": a **photo** by
its camera (EXIF maker and model, "IMG_0001 - iPhone 15 Pro.jpg"), else when
it was taken; a **film or episode** by its picture size ("Dune (2021) -
2160p.mkv", Jellyfin's own naming for a second version of a film, so it
shows as one film with a version to choose), else its length; a **song** by
its bitrate, else its length; an **audiobook** by its narrator ("read by
...", the narrator tag or the composer, where audiobook stores put it), else
its length; and anything else, or nothing telling them apart, by the day it
was added ("added 2026-10-02"). Under Choose for each file the suggested
name is in a box to change (`POST /api/upload/describe`, worked out from
the file's first and last megabyte sent ahead; the upload carries the name
as `?as=`, used when free). Photos name themselves on arrival the same way
(`personalDistinct`), and a phone's backup finds a photo it sent before
under any "Name - ..." of that size (`personalSentBefore`). Read correctly
on real files: a Blu-ray rip 1920x1080 and 1h45m, songs' lengths and
bitrates, the Patient Mind m4b's 10h6m (it names no narrator). The upload carries the
choice (`?conflict=keep|replace`, `SaveWith`/`SaveOptions`), and the save
still refuses an exact copy whatever was chosen; the app's background
uploads pass it on (0.31). Names planned before the bytes arrive can differ
from where a song lands by its tags, so the save keeps its own check.
Checked in Chrome: an exact copy not sent, a different file kept as (2),
then replaced with the old one in the bin.

**Move to, in any item's menu** (owner only, as deleting is;
`POST /api/move`, `library.MoveItems`): for something filed in the wrong
shelf after all. The item's files are found as deleting finds them, companions
and folder included; every one is checked against the new shelf first, so a
move it would refuse (a film to Music) moves nothing; each file is filed by
the new shelf's own rule (tags for music and audiobooks, the book's own for
ebooks, the owner's dated folder for photos) and moved, never over a file
already there; emptied folders go; both shelves are told to look. Checked in
Chrome on a throwaway server: a PDF moved from Documents to Ebooks landed
under Unknown Author/Taxes, a move to Music was refused.

**Everything on one screen** (2026-10-02, the owner's design; it used to ask
one folder at a time, then show the review). The plan's questions and its
sorted parts are shown together (`reviewPlan`, `askedBox`): **Needs your
choice** first, alike questions together - "3 folders of MP3s - music or
audiobooks?" - with a choice for all of them, **Choose each** opening a line
per folder with its own choice and its files to look at, and "Mixed" when
they differ; an answer stays there to change until Add (the plan is made
again with it, `review.asked` keeping the question on screen). **Ready** lists
what SoundStorm sorted itself, each part with Change, which can also leave it
out (**Don't add**); **Not adding** lists what was left out, each to add back.
The names-already-taken question sits on the same screen. Add waits until
every question is answered. Checked in Chrome with a mixed folder: three MP3
folders set to audiobooks, then one changed to music and one left out, a
folder of videos to TV - exactly those were sent.

**And a single file can go its own way** (the owner's safety net, for the
rare file that does not belong with its folder): every part's **Files** list
gives each file a choice - the same shelf as the rest of the folder (the usual, named in the list as "Same as the rest (Music)"), another shelf, or
Don't add. A file given another shelf (`library.FileChoice(path)` as the
plan's choice key) leaves its part with its companions - a film's subtitles,
a book's PDF, by name in the same folder - and shows as its own line, whose
Change offers **Back with its folder**. Checked in Chrome: a concert video
in an album folder sent to Films took its subtitles, the album stayed
music, back with its folder rejoined it, and a song left out was not sent.

**When it cannot tell, it asks.** One drop zone, no shelf targets: guessing
wrong costs somebody moving files on disk, so the bar for guessing is "there is
real evidence", not "one of them is more likely". A question is asked per
*group*, so a thirty-chapter audiobook is one decision, and the answer goes back
to `/api/upload/plan` which re-plans - the destination shown is always the one
the server will use rather than something worked out twice.

The list of things worth asking about is deliberately short, because a question
on every album drop would be worse than the occasional wrong guess:

- **mp3, and only mp3.** flac, wav, aiff and alac are music in practice; m4a is
  music because audiobooks in that family use m4b. Mp3 really is both - every
  LibriVox recording is one.
- **Three or more videos in one folder with no episode numbering.** One or two
  unnumbered .mkv files is a film and its extras; six is a series somebody named
  badly, and six episodes in the film library is worth a question.

Everything else stays decisive: `.epub` is a book, `.m4b` is an audiobook,
and `S01E01`, `1x02` or a `Season 01` folder is television. **A name saying
"audiobook" raises the question, never answers it** (the owner's call,
2026-10-03): it used to send every audio file in the drop to Audiobooks
unasked, FLAC and all, and "Audiobook soundtracks" or a narrator's album is
music. Now any audio in such a drop asks, as an MP3 always does. The rule
behind it: a file's own type may decide; a name may only ask. A question only offers libraries the account actually
has, and with one option left it stops being a question.

**Nothing appears at its destination until all of it is there.** Navidrome and
Audiobookshelf watch these folders and a half-written file is exactly what a
scanner indexes as a corrupt track. Bytes land in `<library>/.uploads` and are
renamed into place, which is atomic because it is the same filesystem, and
invisible to the backends because a top-level dot-directory is not mounted into
any of them. `ClearStaging` sweeps it at boot for the crash case.

**The same recording under another name is skipped.** An upload was only ever
refused when its destination name existed, and an iTunes library keeps
repurchases side by side: `03 Paper Lanterns.m4a` and `03 Paper Lanterns 1.m4a`. Measured
on a real library before building anything: of nine such pairs **none** were
identical files - iTunes writes a catalog id, a purchase date and its own copy
of the artwork into each - and five had identical audio. The other four
differed in the audio itself, two by enough to be different versions.

So audio is fingerprinted by the audio alone (`duplicate.go`): the `mdat` of an
MP4, the frames of an MP3 between its ID3 tags, the frames of a FLAC after its
metadata blocks. Everything else is compared whole, as is any file whose
structure does not parse, which errs toward keeping both. A clean and an
explicit version differ in their audio and are both kept; what is skipped is
one recording sold under two listings. On the real files, the five same-audio
pairs differed only by a Parental Advisory badge on one cover, or a Deluxe
Edition cover - checked by rendering the embedded artwork side by side.

The scope is deliberate and narrow: **only the destination folder**, only the
same format, and only uploads. The same song on an album and a compilation is
not a duplicate, and a deluxe edition in its own folder keeps every track even
where it shares one with the standard. Hand-copying a file into the folder is
never checked, which is also how somebody adds one anyway. Only siblings with
exactly the same audio length are ever hashed.

The staged upload is `part-123456789`, so the format comes from the dropped
name, never the staged path - the first test of the case this exists for
caught a staged M4A being hashed whole against an audio-only hash of the copy
on the shelf. Same trap as PDFs above.

A skip comes back as a 409 like "already in your library", and the drop panel
shows both as *skipped*, not failed: the server declining on purpose is not an
error, and the summary line counts them with the plan's skips. Verified by
uploading the real `Paper Lanterns` pair (skipped, naming the original) and the real
`Turn Around` pair (kept) through `Save`.

**Every path is attacker-supplied.** One trap here cost a real bug, caught by
its own test: `TrimRight(segment, ". ")` ran *before* the `..` check, so ".."
became "" and was skipped - which quietly turned `../../etc/passwd.mp3` into
`etc/passwd.mp3` and wrote it. No escape, but a strange file in somebody's music
folder and no sign a path had been rewritten. Dot runs are now refused before
anything is trimmed. `Save` also re-checks the joined path is still inside the
folder, which is a thing worth doing twice.

Uploading follows the same permission as reading: you can add to a shelf you can
see. A restriction search honors and uploading does not is not a restriction,
so the plan refuses forbidden kinds and so does the upload.

On the browser side, `walkEntry` must call `readEntries` **until it returns an
empty batch**. It hands back at most a hundred at a time, and reading once
silently truncates a large folder - the classic way to lose half an album. There
is a test for it that builds a fake entry tree, because a synthetic DataTransfer
gets no filesystem entries and no automated drag can produce real ones.

## The Continue row

What this person is part way through, newest first, above the library on
Everything and on the audiobook, ebook and documents shelves. It is hidden
while searching or selecting.

**Two places know, and neither is new.** A book's position is SoundStorm's own
(`state.Progress`, keyed `userID/sourceID/itemID`). An audiobook's belongs to
Audiobookshelf, per person, through the per-member account `TokenFor` already
makes. So a book started in its phone app is in the row. `source.InProgressLister`
is the optional interface for a backend that remembers.
`source.ItemGetter` turns a stored book id back into a card. Both go through
the registry, so a shelf an account may no longer see drops out of the row.

Audiobookshelf's half is two calls, checked against 2.36.1.
`/api/me/items-in-progress` lists the books, in the search shape, with
`progressLastUpdate`. `/api/me`'s `mediaProgress` has the fraction. The
adapter leaves out finished books, ones hidden from continue listening in
Audiobookshelf's own UI, missing ones, and podcast episodes.

A position under half a percent or over 98.5% is "not started" or
"finished", not something to carry on with; for a film or an episode the top
is 93%, because the credits are the end.

**Film and TV positions are SoundStorm's, not Jellyfin's, and that is the
"revisit when watched-state reaches the UI" moment in "Accounts".** The house
shares one Jellyfin account, so a position kept there would be everybody's:
one person's half-watched film in another's row. Per-member Jellyfin accounts
would fix that at the cost of a second provisioning path, and buy nothing,
since nobody ever sees Jellyfin's own resume. So a video's place is kept like
a book's, in `state.Progress`: the location is the time (`t=1234.5`), so the
state format did not change. The player saves every 30s while playing, on
pause and on close, and on open jumps back (not from the first ten seconds,
and not into the credits). `progressTarget` accepts a Jellyfin id through
`HasItem`, the cached ownership check every Jellyfin target already makes, so
a made-up id still cannot grow the state file. `ItemByID` turns the id back
into a card.

Verified against a real Jellyfin in a throwaway stack that SoundStorm
provisioned itself, with a generated two-minute film. Played, stopped at one
minute and closed, the film was in Continue at 51%, and reopening it resumed
at 61s. The same stack confirmed Jellyfin's side of deleting, which had been
untested: the preview named exactly the film's file, byte for byte.

## Music, toward Plexamp

Asked for as "competitive with Plexamp". The first phase is what a music app is
judged on in its first minute:

- **Lock screen, headphones, car (Media Session).** Title, artist, album and
  cover, and the buttons: next and previous step through the queue or an
  audiobook's chapters. Previous more than 3s in restarts the song. With
  neither, the lock screen gets 15s skips.
- **Albums and artists.** `source.MusicBrowser`, which Navidrome answers from
  getAlbumList2, getAlbum, getArtists, getArtist and search3, behind
  `/api/music`, access-checked like everything else.
- **Now Playing and a real queue.** Every song is in a queue. Play next and
  Add to queue turn one song into a queue. Shuffle rearranges only what is
  still to come and restores the order when turned off. Repeat is all or one.
  On a phone the dock is a mini-player, since the browser's controls are too
  small for a thumb. It is now a floating card that is Now Playing in
  miniature everywhere - the blurred cover behind, the same icons, progress
  along its bottom edge - and the browser's own `<audio controls>` is gone,
  because its white pill clashed with everything. A computer gets previous,
  play, next, a seek bar and volume on the card; a phone gets play and next,
  swipe up for Now Playing and swipe down to stop. A tap anywhere on the card
  opens Now Playing, not only on the cover and title as it first did - except
  on its buttons, sliders and chapter list. Starting music by hand
  opens Now Playing; dragging it down closes it.

  **On a touch screen, songs change by swiping, not buttons.** Previous and
  next are gone from Now Playing and the mini-player wherever the pointer is
  coarse (a computer keeps them - a mouse cannot swipe). Swiping the big
  cover sideways moves it with the neighboring covers riding beside it and
  plays the one that slides in; with the lyrics or queue in the middle the
  title moves instead. The mini-player slides out and back in. A swipe back
  is always the song before - the previous cover is what slid in - not a
  restart of this one; the lock screen's previous keeps the 3-second rule.
  A song played on its own from Songs has no queue, so there is nothing to
  swipe to, as there was nothing for the old next button either.

  **The cover that slides in is the one that stays.** Reported as skipping
  being glitchy and the art slow to appear. The neighbors' covers used to be
  asked for only when the finger started moving, and at the end the main
  cover was hidden until the new song's picture had been asked for again and
  drawn - up to a second of waiting. Now the covers either side are fetched
  and decoded as soon as a song starts (`npSwipe.prime`, from
  `renderNowPlaying`), and on commit the main cover takes the slid-in
  picture behind it (already loaded), comes back under it, the song changes,
  and only then does the slid-in one step aside. Checked frame by frame with
  250ms of latency on every request: the next cover was drawn before the
  swipe, slid in drawn, and the handoff showed no blank or wrong frame.
  Covers are small (40-190KB on the real library, measured), so size was
  never the cost.
- **Now Playing has one layout, the lyrics one.** Title at the top, the
  middle for the lyrics, controls at the bottom; there is no lyrics button,
  because lyrics always show when a song has them. A computer keeps the big
  cover on the left; a phone puts a small one beside the title. The top-right
  button is Up next, which takes the lyrics' place.

  **A song keeps the lyrics layout whether it has lyrics or not, and while
  they load - a reversal.** A song without lyrics used to get a layout of
  its own (the cover alone), and so did every song until its lyrics had
  arrived, so each song change jumped once they came, and a song without
  them jumped back: reported as things glitching when lyrics load. Now
  everything sits exactly where it would with lyrics; the space is empty
  while they load (so a song that has them never flashes "none") and says
  "No lyrics for this song" quietly when there are none. Only an audiobook
  keeps the cover-alone layout, as it never has lyrics. Measured on phone
  and computer, a song with lyrics, one without, loading and loaded: cover,
  title, controls, timeline and the lyrics box identical to the pixel. The
  blurred backdrop fades from one cover to the next once the new one is
  decoded (two layers taking turns), rather than swapping, or vanishing for a
  song without a cover.
  **Then a phone lost the strip and the "no lyrics" line, at the owner's
  asking:** a song is always the big lyrics, the title and artist centred at
  the top with no small cover beside them (`lyrics-on`), and a song without
  lyrics leaves the middle empty, the title where it would be. The cover
  itself no longer shows on a phone for a song, so the looks drawn where the
  cover is (disc, record, Orb and the other cover visualizers) have nowhere
  to draw there; the full-screen looks still run behind the lyrics. So the
  six cover visualizers became full screen too, centred on the screen: one
  Visualizers group of twelve in the Looks sheet, all on `#np-stage`, the
  six first made for the cover drawn at 0.85 of the screen's shorter side
  (`EX_COVER_VIZ`) so they fill it, the rest at 0.55 as before. And the line
  being sung sits at the middle of the screen, where they are centred, not
  of the lyrics box, which starts under the title - measured on all six,
  the line's centre within a pixel of the screen's. **A tap no longer
  changes the look** - the Looks button is the only way (a tap by the title
  was changing it by accident). **Then Lyrics became a look of its own**
  (`lyrics`, in the server's `CoverStyles` too, and the default for an
  account that never chose): on a phone the title is always at the top and
  under it is exactly one thing - the lyrics, the cover centred on the
  screen as a square, disc or record (`min(84vw, 52vh)`), or a visualizer
  with no lyrics over it, the owner's choice among the ways offered. The
  lyrics box stays in place, invisible, under the other looks, so nothing
  moves. A computer treats Lyrics as the cover beside the lyrics, as before.
  **And a double tap on a touch screen moves to the next look** (a single
  tap did at first, and changed looks by accident), in the sheet's
  order and round again, while a hold still brings up the buttons - not on
  the title (the owner had asked for a tap there to do nothing), Up next,
  the sheet or a menu, and not at the end of a hold or swipe. So a lyric
  line is no longer tapped to jump there on a touch screen; the timeline in
  the hold does that.

  **An audiobook went back to the plain player**, at the owner's asking -
  the looks were built for music and the screen is shared. `np-music` on
  Now Playing is what every hide-until-held rule keys on, so a book keeps
  its "Now Playing" label, its close, stop, play and speed buttons and its
  timeline showing; `coverStyle()` answers `square` for a book whatever the
  account chose, so no visualizer or record; the Looks button is hidden and
  a tap changes nothing. **Skipping was weird in a book** - there is no next
  song, and swiping or next jumped a whole chapter - so a book's previous
  and next are thirty seconds back and on (`bookSkip`, across files; the
  mini-player's too, and on a phone its forward shows, `dock-book`), the
  swipe finds no neighbours, and the lock screen gets seek buttons instead
  of next and previous. **And a book has a table of contents at last:** a
  Chapters pill beside the speed opens a sheet of the book's chapters with
  where each starts, the current one marked, a tap going there. They come
  from Audiobookshelf's own chapter list (`source.ChapterLister`, sent as
  `chapters` in `/api/playback`), which names the marks inside a single
  m4b as well as one file per chapter - so a single-file book, which never
  had any navigation, has it now; without a chapter list the files stand in.
  Checked with a stand-in book in the preview (no Audiobookshelf there):
  Chapter Two landed at 1:00, +30 at 1:32, -30 back at 1:03.
  **A book's timeline is its chapter's**, at the owner's asking - across a
  ten-hour book the slightest drag moves a chapter: the draggable one runs
  from the chapter's start to the next one's, and never seeks past it
  (`bookSpan`, `goToBook`, across files). Under it a thin bar shows the
  whole book's progress and cannot be dragged, with "2 of 12 - its title"
  and the time left in the book. A book of one chapter keeps the one
  timeline. Checked on the stand-in book: in Chapter Two the timeline read
  0:02 of 1:00, and its middle landed at 1:30 in the book.
  **The mini-player follows the chapter too** - the line along its edge,
  and a computer's seek bar and times - and **a swipe left, on Now Playing
  or the mini-player, goes to the next chapter** (`nextChapterStart`), the
  same cover sliding in and the chapter's name in a message; **a swipe right
  goes to the chapter before** (`prevChapterStart`) - it sprang back at first,
  and the owner asked for it. More than five seconds into a chapter it goes
  back to that chapter's start, as "previous" restarts a song; nearer the
  start, to the chapter before. With nowhere to go it springs back. The
  mini-player's second line, and the lock screen's title, is the chapter -
  "3 of 12 - its title" - from the book's chapter list, kept up as it plays
  on (`updateChapterCaption`). **Then the book screen went the music
  screen's way too**, at the owner's asking: no "Now Playing" words, and on
  a touch screen its buttons - close, Chapters (a list icon in the hold),
  speed, stop, the thirty-second jumps - invisible until a hold brings them
  up in their places; play stays, and so do the chapter timeline and the
  book's progress, which are what a book is watched for. Checked on the stand-in book: left on
  Now Playing 0:02 to 1:01, right stayed put, left on the mini-player to
  2:01.

  **Hiding the cover had quietly switched off the song analysis.** The
  owner reported the animations feeling like plain tempo again, and they
  were: `keepTime` - which asks for the tempo and has the song heard - ran
  only from `renderCoverDeco`, after its early return for a cover not on
  screen, and a phone's Now Playing stopped showing the cover. It is asked
  for from `applyCoverStyle` now, whenever a visualizer shows. Checked with
  the cover hidden: the click track's 120 beats arrived, the first at 0.23s.
  What follows is how it was:
  On a phone the default is the big cover with a strip of the few lines around
  the one being sung under the centered title; tapping the strip grows it into
  the full lyrics (a tap there never seeks), and tapping the small cover at
  the top shrinks it back.
- **The player asks for the whole screen on a phone**, hiding the status and
  navigation bars while it is open. It is `requestFullscreen` on the
  document, made from the tap that opened the player; Android grants it,
  iPhone Safari offers full screen only to video and ignores it. A web page
  has no other way to hide either bar.

  Then made permanent: the manifest says `display: fullscreen`, so the
  installed app launches with neither bar; in a browser tab every tap asks
  again while the page is not full screen, since a page may only ask from a
  tap and the back gesture or a video's own full screen can drop it.

  **The installed app still shows a black strip over the camera until the
  first tap**, confirmed on a real phone. `display: fullscreen` hides the bars
  but Android letterboxes the camera cutout in black; the page may draw into
  it (viewport-fit=cover) only once the page itself has requested full
  screen, and that needs a tap. So the installed app asks on the first tap
  too, and the strip goes then. Nothing a page does at load can remove it.

  The owner did not like the strip, so the manifest went back to
  `standalone`: the app opens with the status bar showing in its own color,
  as any app does. Full screen is now a small button beside Account, on
  Android only, rather than any tap: asked for, never assumed. It hides both
  bars together - Chrome gives a page no way to hide the navigation bar and
  keep the status bar.

  **Then removed, at the owner's request.** The app opens and stays like any
  installed app, status and navigation bars showing; nothing asks for full
  screen any more. The manifest stays `standalone`.
- **A pull down from the top edge is Android's.** Now Playing closes on a
  drag down, and reaching for the notifications closed it every time. A
  downward drag that starts in the top 56px (or the cutout plus 32) is left
  to the system; the title just below it still pulls Now Playing away, and a
  sideways swipe from the edge still changes song.
- **The camera cutout, in the Android app.** The app draws into the cutout,
  and its web view reports no safe area for it, so it sets `--safe-top` and
  its siblings on the page itself. `style.css` defines those from
  `env(safe-area-inset-*)` and uses them everywhere, never `env()` directly.
  That change was first made only on the `android-app` branch, whose web
  files the server never serves, so the installed app's title sat under the
  camera until it was brought to main. So was the Change server button. The
  rule: web changes for the phone apps go to main; the app branches carry
  only the apps.
- **The status bar takes Now Playing's color.** An installed app's status
  bar is the page's theme color, which a page may change while it runs, so
  in Now Playing it is the top of the cover averaged and put through the
  backdrop's own filter (saturate 1.4, brightness 0.45), and the app's dark
  again on close. Android's navigation bar at the bottom is Chrome's: no page
  can color it, and an installed app cannot draw under the status bar until
  Chrome ships that (in progress, 2026). An APK was considered for this and
  declined: a Trusted Web Activity runs in Chrome and has the same bars, and
  a WebView app loses the lock-screen controls and background playback.
- **Holding anywhere on Now Playing shows its options as icons over the
  cover, only while the finger stays down** (`showHoldIcons`), at the
  owner's asking - where the buttons sat before they went: info, sleep timer
  and favorite along the top, shuffle and repeat at the sides with **Add to
  playlist** (a plain plus: Up next's list icon was too like it) between
  them, download, Up next and songs like this along the bottom. Slide onto
  one and it grows and is named where the title was; let go on it to use
  it; let go anywhere else and they go. The icon a thumb came down on is
  lit at once and chosen on letting go without moving - a reversal: it
  used to need moving off and back on, so the icon under a still thumb was
  never chosen, and the owner put a thumb on play, held, lifted, and
  nothing played. Info, sleep and
  add to playlist open their menu page. With the cover hidden they appear
  in a square in the middle. The veil behind them takes the cover's own size
  about its centre, never its bounding box: a spinning record is rotated,
  and the box around a turned square is up to 1.4 times as wide - it drew a
  dark ring around the disc (measured: 404px box, 300px disc, at 63 degrees).
  The veil is the only dark: no shadow behind each icon (the owner's call).
  The one under the finger gets a white glow about 140px across, fading
  softly - a thumb covers about 45px, and a glow the icon's size hid under it -
  coming in over about a third of a second and going a little slower. Its
  name shows left-aligned on the title's line, clear of a right thumb, and
  says what letting go will do, not how things are ("Turn shuffle off" while
  it is on; repeat reads Repeat all, Repeat this song, Turn repeat off, in
  its cycle); an option that is on
  (shuffle, repeat, a sleep timer, downloaded) is the accent's colour, so it
  never looks like the finger's glow; a favorite's heart is red. **On a touch
  screen the rest of the buttons are invisible too** - close, Looks, stop,
  play (and previous and next, and an audiobook's speed, wherever they show)
  - at the owner's asking, so nothing sits on the screen but the music and
  the timeline. They keep their places (`visibility: hidden`, so they take
  no taps either) and appear there during a hold (`#np-hold-extra`,
  `holdButtonList`), chosen like the nine; letting go on one clicks the real
  button, past the rule that swallows the lift's click. A mouse keeps them
  all showing. **Except play, which came back** at the owner's asking: it shows
  all the time, a tap on it plays or pauses and never changes the look, and
  it is no longer among the buttons a hold brings up. **And it is not a triangle
  any more** (`playOrb`): a triangle read as boring, so music's play button
  is a small living orb - four glowing wavy layers in the cover's colours
  turning against each other, on a canvas larger than the button so its
  glow spills round it - that keeps time with the analysed song: it swells
  on each beat (more on a bar's first), the kick ripples its edges, the
  loudness sets how much it moves. Paused, it stops where it is and dims;
  the movement is the playing. Its own small loop, so it moves on Lyrics and
  the covers too - which is why the song is now heard for any look, not only
  a visualizer. An audiobook keeps its plain white play button. Then made
  quieter and more tied to loudness, asked for as less distracting from the
  main animation: the loudness, eased over a fraction of a second so it
  breathes rather than flickers, sets its size, brightness (overall alpha
  0.35 to 0.75), how wavy its edges are and how fast it turns; the white
  heart and halo are a third of what they were. **Then less cloudy, more
  magical:** the filled layers read as haze, so each is now a line - a wide
  faint stroke under a fine bright one, like neon, a breath of colour inside
  - with sixteen sparkles circling it, each twinkling on its own, flaring
  and drifting out on the beat (the brightest with a small cross of light),
  and a small bright heart. **The full-screen animation keeps clear of
  it** (`viz.keepClearOfPlay`): after each frame a circle round the orb is
  cut out of both stage canvases, out to 1.1 of the button's width but
  solid only to 0.22 and eased the whole way from there (a solid circle with
  a short edge read as a hole cut in the animation; 2.3 and then 1.45 were
  too big), so the rain, stars or waves fade before they cross it. There is
  no play mark inside the orb; it was tried and taken out again. Then the "Now Playing" words went and the timeline too, shown only
  while holding: sliding onto it (a band 36px either side) moves the
  position under the finger, the caption saying "Play from 2:09", and
  letting go there seeks. The sleep timer's countdown still shows. Nothing else answers the finger while they
  show - the swipe stands down. Right-click still opens the full menu, and
  the mini-player's hold opens the menu (below). Before the icons, a hold
  opened that menu, which is what the rest of this entry describes:
  **holding anywhere on Now Playing opened its menu** (and right-click) - the cover, the background, the title, the
  lyrics (a held line does not jump there) - but not what already answers a
  touch: play, the header's buttons, the timeline, Up next, a menu
  (`NP_HOLD_SKIP`). Every hold menu swallows the lift that ends it, moved or
  not, so the lift touches nothing underneath. **The mini-player's hold opens
  the same menu** (not on its buttons), and its own swipes stand down once
  the menu is up; its Up next opens Now Playing on the queue. **A sideways
  swipe anywhere on Now Playing changes song** - it always listened to the
  whole screen, but refused to start on lyrics that had scrolled (so a drag
  there could scroll them) and on Up next, and lyrics scroll as a song plays:
  now those refuse only an up-and-down drag, which stays theirs. It was the
  cover alone first,
  at the owner's asking - the card buttons, shuffle, repeat,
  the sleep timer, Up next and songs like this had crowded onto it. The
  player's options come first (`playerMenuItems`): **Songs like this**,
  Shuffle and Repeat with their state beside them (changing one keeps the
  menu open), the **Sleep timer** as a page of its own, and Up next; then
  the song's own - favorite, download, playlist, info, delete - without
  play next and add to queue, as the song is already playing. An audiobook
  gets only the sleep timer. A tap still turns the cover into a record and a
  swipe still changes song; the lift at the end of a hold is neither, and
  must not reach the page as a click outside the menu - it lands on the
  dimmed page over the cover - so the outside-click rule skips it
  (`state.heldAt`), the trap the card hold met first. The sleep timer's
  countdown still shows under "Now Playing". **Songs like this** keeps the
  song playing and makes what follows its song radio - sounds-alike once
  the analysis has heard it - carrying on without end, and says so in a
  message ("Similar songs added to the queue", or "...replacing your
  queue"). Messages raised from Now Playing sit above it; they used to show
  beneath it, unseen. The header keeps only the arrow, which puts Now
  Playing away with the music going on, and an **X** top right, which stops
  the music and closes the mini-player too (`stopAudio`). On a phone the
  small cover by the title returns from the lyrics or Up next. The cover
  morph's `view-transition-name` is on `#np-cover` alone: the swipe's side
  covers share the class, and two elements with one name aborted every
  transition ("invalid state"). The play controls sit above the timeline,
  at the owner's asking, in every view (the panel and strip views order
  them 3 and 4). Up next's rows carry each song's cover, and stack from the
  top rather than spreading down the list.
- **Every button showing, for driving** (2026-10-05, the owner's asking):
  a button among the ones a hold brings up (`#np-easy-btn`, four squares,
  "Show all the buttons") turns on `np-easy` on Now Playing, kept on the
  device (`soundstorm-np-easy`, `applyNpEasy`): **what a hold shows simply
  stays**, in its own places, taking a plain tap - the bar's buttons, the
  timeline, and the nine options over the cover (`renderEasyLayer`, the hold
  layer drawn by `placeHoldLayer` as buttons); the same button, then lit,
  hides them again. Nothing new is added - a first version put the options in
  a row of their own and brought back previous and next, and the owner wanted
  only the old buttons shown. A hold elsewhere still works. Only on a touch
  screen, never a TV or with a mouse. Checked at phone size: turned on by a
  hold, the nine options stayed with the bar and timeline, a tap turned
  shuffle on, and the button hid them all. **A swipe still changes song** while they show:
  the swipe stood down whenever the hold layer was up, which is right only
  during a real hold (`hold-icons`) - checked left and right, from empty
  screen and across the square of options. **And a hold works the same while they
  show** (the owner's asking): from anywhere, the buttons included
  (`NP_HOLD_SKIP_EASY`), sliding over them names each and letting go uses
  it; a quick tap still presses one. The hold reuses the buttons on screen
  rather than drawing its own: replacing the button a finger came down on
  cut the touch off (its moves and lift went to the removed button, never
  reaching the page), and the hold never ended. Checked: held on shuffle and
  let go on repeat ("Repeat all"), held on the top bar and let go on the
  heart, a tap on shuffle, and the hidden hold unchanged.
- **Four looks for the cover, a tap moving to the next** (`coverStyle` on
  the account; `coverSpin` from before still reads as "spin"): the cover, a
  spinning disc, a **record** and **moving with the music**. The record and the
  moving cover are a layer (`#np-deco`) laid exactly over the cover image,
  which stays in place (hidden, for the record), because the swipe, the hold
  icons and the cover morph all work on that image; the swipe moves the layer
  with it. The record is grooves turning at 4s a turn, the cover as the
  label, and the title, artist and album round it on an SVG textPath, whole
  repeats only (a first version cut "HARBORLIG" off where the ring closed);
  the light on it stays still. **Moving with the music keeps the song's tempo,
  not its beats.** Hearing the beats means routing the music through Web
  Audio, which is what stops it when an iPhone locks (see Leveling), so the
  pulse, the rings and a glow in the cover's colour (`--np-glow`, averaged
  alongside the status bar's) run at the tempo AudioMuse-AI measured
  (`GET /api/music/sound`), folded into 70-150 bpm, bigger for a song more
  energetic than most of the library (energy as a rank, as moods are), and
  lined up with the song's position. A song not yet heard moves slowly.

  **It is a visualizer in place of the cover** (`viz` in app.js; named
  "Visualizer" when tapped to). A first version kept the cover and put
  bouncing bars round it; the owner found it dull and asked for something
  wild, like Plexamp, without the art. Now the cover image is hidden (kept,
  for the swipe and hold) and two canvases 180% its size draw: a nebula and
  light rays in the cover's colours (`coverPalette`, the strongest hues of its
  16x16 thumbnail, brightened); an orb of six layers whose edges are sums of
  travelling waves, added together so they glow white where they overlap,
  swelling on the beat and spiking on 2 and 4; a flashing core; rippling rings;
  a shock ring on each bar's first beat; and, on a second canvas that fades
  rather than clears, 240 particles in a vortex, thrown outward on every beat
  and sprung back, drawn as streaks from their last position (dots one frame
  apart looked like beads). Everything comes from `currentTime` each frame, so
  it is lined up after a seek unasked; it settles to still on pause and stops
  drawing off screen. 59fps in Chrome on the preview; draws in WebKit's
  iPhone 13 profile. It sits behind the (hidden) cover inside the wrap, which
  is its own layer (`isolation`), so the title always draws on top.

  **It follows the song itself, not only its tempo** (`listenTo`,
  `hearSong`). Reported as the timing seeming slightly off, with loud and
  quiet asked to count: the tempo alone put the first beat at 0:00, and a
  tempo slightly wrong drifted. Now a copy of the song - the gapless blob
  already in memory, a download, or else a 96 kbps stream - is decoded on an
  OfflineAudioContext, which plays nothing, so playback stays on the audio
  element and the iPhone lock screen is untouched. At 11025 a second
  (22050 or 44100 where an older Safari refuses low rates), 23ms frames give
  loudness (energy over half a second: one slice between hits is near silence
  in a loud chorus too), a low band (kick) and a high band (snare, hats) from
  two one-pole filters, and onsets. The tempo is the onsets' autocorrelation,
  leaning on AudioMuse-AI's tempo when there is one; the beats come from
  Ellis's dynamic-programming tracker, so a wandering tempo is followed; the
  bar's first beat is where the kick lands hardest. Measured on a generated
  click track (120 bpm, first beat at 0.23s, a quiet section): all 120 beats,
  mean error 13ms (max 24ms, one frame), loudness 0.02 in the quiet part
  against 0.87 in the loud, about a second to analyse. The visualizer then
  takes its beat from the list, its hits from the bands (scaled by loudness,
  since an onset in dB is as big in a soft passage as a loud one), and its
  size, speed and brightness from loudness. Until the analysis arrives, the
  tempo alone keeps time. Not compensated: Bluetooth output latency, which a
  page cannot measure without an AudioContext of its own.

  **Downloaded songs are heard ahead of time, and offline** (`saveHeard`,
  `loadHeard`, `prepareDownloads`). Asked for so offline was not the
  tempo-only version. What is heard in a downloaded song - the beats, the
  bar's first beat, loudness and the two bands as bytes, and AudioMuse's tempo
  and energy - is kept beside it in the downloads cache under a `/__heard/`
  address nothing fetches: about 45KB for a four-minute song. It is made when
  the song is downloaded (while online, so the tempo comes too), and a sweep a
  little after opening hears downloads from before this, one at a time,
  resting between songs and waiting while the app is hidden. Playing reads the
  kept copy first, and `keepTime` takes the kept tempo rather than asking the
  server; a downloaded song's audio is read from the device rather than
  fetched. Removing the download removes it; signing out clears it. Checked:
  kept 0.3s after a download finished; then offline with nothing in memory,
  the downloaded click track played from a blob and the visualizer had its
  beats (120, mean error 11ms) within 0.3s of pressing play.

  **Six visualizers, not one** (`VIZ_STYLES`, `VIZ_SCENES`): the orb
  ("Orb", still `pulse`), then Spectrum (a mirrored equalizer, bass in the
  middle on the kick, highs at the edges on the snare, falling peak caps, a
  reflection), Warp (stars streaking past faster when loud and on kicks, a
  turning hexagon tunnel flashing on each bar), Waves (twisting ribbons, each
  the space between two travelling waves), Kaleidoscope (shapes in a wedge
  mirrored ten ways, snapping round on each bar) and Fireworks (a burst on
  every beat, bigger on a bar's first, crackle on the snare, gravity and
  trails). One frame loop reads the music once - beat, phase, new beat,
  kick, snare, loudness - and hands it to the scene; each keeps its own state,
  fresh when chosen. `cover-viz` on Now Playing marks any of them. Nine looks
  is a lot of tapping, so Now Playing's menu has **Cover look** to jump to
  one. All ran at 58fps in Chrome on the preview; Fireworks draws nothing in
  headless WebKit only because that cannot play audio, and it bursts on beats
  while playing.

  **Flow is full screen, and experimental** (`flowScene`, `#np-stage`): the
  only look not drawn where the cover is. Its two canvases fill Now Playing
  behind everything (the title, lyrics and controls come later in the page,
  so they draw on top), centred on the screen and scaled to its shorter side:
  520 particles in a current - a swirl round the middle mixed with a field of
  waves that tightens as it gets louder - trailing light, a pulse out from the
  middle on each beat and a shock wave across the screen on each bar's first.
  The current was first too tight (trails zigzagged like electricity) and the
  trails faded too slowly (a grey haze of traces that never reached nothing).
  **Holding on a visualizer** draws no dark square over the animation: each
  icon gets a small dark glow of its own (`.np-hold-layer.viz`).

  **Fifteen looks in three groups, picked in Now Playing** (`COVER_GROUPS`):
  Covers (cover, disc, record), Visualizers where the cover is (Orb,
  Spectrum, Warp, Waves, Kaleidoscope, Fireworks) and Full screen (Flow,
  Storm, Synthwave, Galaxy, Aurora, Lava - `FULL_SCENES`, drawn on
  `#np-stage` like Flow, `cover-full` on Now Playing). A **Looks** button in
  Now Playing's top bar opens a sheet of all of them, grouped, the current one
  ticked; it stays open while somebody tries them, each applying at once
  behind it, and goes with Done or a tap elsewhere. The menu's Cover look opens
  the same sheet. A tap on the cover now goes round cover, disc, record and
  the last visualizer used (kept per device), rather than all fifteen. Storm
  is rain plus a branching bolt and a flash on a loud bar's first beat;
  Synthwave a neon grid racing under a striped sun that pulses on the kick;
  Galaxy a spiral whose inner stars turn faster, arms winding tighter when
  loud; Aurora curtains with hanging folds; Lava soft blobs rising, bubbles on
  the snare. All ran at 58-59fps in Chrome on the preview.

  **What stands out, not every beat.** Everything used to react hard to
  every beat, and a kick on every beat of a whole song pumped relentlessly
  (asked for as getting rid of "the most common consistent overwhelming tempo
  beats"). Now each beat's kick is compared with the typical kick of the last
  two bars (`kickPeaks`), and a surge in loudness against the last four
  seconds: a steady beat settles into a soft pulse, and an accent, a fill or a
  drop after a quiet part hits hard (`novelty`). Big moments - bursts, pushes,
  pulses - fire on a beat that stands out (over 0.4) or a bar's first, not all
  four. The comparison starts afresh after a seek (judged only after four
  beats), or the first beats after a jump looked like a drop. Measured on the
  click track: a steady loud part averaged 0.08 (peaks 0.42); the jump from
  quiet to loud hit 0.91. Without the song's analysis, only a soft pulse,
  a little more on each bar. **Storm** was made calmer on the same request:
  rain over the whole screen (spawned left of it for the wind) and slower,
  lightning at most every eight seconds, on a standout moment in a loud part,
  and only a third of those - once in sixteen loud seconds when measured.
  **Lightning is on the first beat of each bar in the song's loud parts** -
  its loudest 30% by the half-second loudness - the first of a loud stretch
  a double strike (Storm) or the finale (Fireworks); the Orb's ring, Flow's
  wave, Galaxy's ripple and Warp's surge go with it. It got there through
  four readings of the owner's asks: bass drops, every measure after a drop,
  then runs of booms - the "boom boom boom" at 0:37 in Night Drive. Booms were a
  sub-bass (under 100 Hz) jump of 10dB out of a lull, three or more under
  0.45s apart; tuned on that file they found 0:37 exactly, but the owner
  reported the other strikes off the beat and the same booms at 2:17
  missed, and the hit list showed why: 10-20dB jumps every quarter second
  through the whole song, the booms no different by any measure tried. So
  it went back to the beat grid, which is what an eye checks lightning
  against. Those booms are syncopated, off the grid, so no beat rule strikes
  on them.

  **And the grid was wrong for fast songs, twice over.** The tempo search
  tried whole-frame lags (23ms): Night Drive's 170 bpm falls between two, the
  error adds up over every beat, and 112 won. Lags now go in tenths of a
  frame, 70-180 bpm. Separately, two calls to `keepTime` for one song (the
  song starting and the look applying) raced: the second read the tempo
  lookup as "not known" while it was in flight and started hearing the song
  with no tempo to lean on, and the hearing is cached per song, so the
  later call's tempo never counted. In-flight lookups are now shared
  (`soundAsking`). With both, Night Drive's beats sit at 0.714s (84, felt at
  half of 170) - strong bass hits land on the beat or the half beat, 50 of
  71 - and saved analyses are version 5, so downloads are heard again.
  **Timing is set by eye, per device.** Then reported from a phone's own
  speaker as the strikes landing late, the first around the last boom. The
  code was on time against the player's clock - the same analysis from the
  96 kbps copy the app hears found the booms within 30ms, and with the CPU
  slowed 6x frames stayed at 17ms and strikes at 37.49, 37.92 and 38.24s - so
  the gap is between that clock and the device's sound, which nothing a page
  can ask reports. The Looks sheet has a **Timing** row (Later, Sooner, a
  tenth of a second a step, -0.5 to +1s, kept in localStorage as
  `soundstorm-viz-lead`); the visualizer reads the clock plus it, and the
  sheet stays open so it can be judged against the music. Checked: 0.3s
  sooner moved the strikes to 37.19, 37.61 and 37.93s.
  **Smooth means no garbage.** Reported as Storm skipping every few
  seconds: frames were steady at 17ms on a computer, but the heap dropped by
  over a megabyte four times a second - a new colour string and a stroke per
  raindrop, 420 a frame - and on a phone that is a collection pause every
  few seconds. `vizColor` now hands out colour strings from a cache (the
  alpha rounded to one of 64 steps, which the eye cannot tell), Storm's rain
  and the Orb's particles are drawn in six batches (three colours, two
  weights), Waves keeps its edges in buffers instead of 900 small arrays a
  frame, and splashes and bolts age in place. Measured as heap drops over
  15s with the CPU slowed 4x: Storm 57 to 6, Flow 62 to 1, Warp 44 to 6,
  Waves 42 to 9, Aurora 34 to 7, Orb 47 to about 19 (its gradients; drawing
  its rays from three shared gradients measured worse, and was reverted).
  Storm's rain speed also eases towards what the music asks rather than
  jumping on every kick, the other half of the skip.
  **The Looks sheet** lost its Done button (cut off on a phone): a choice
  closes it, names only (three to a row fit), no scrolling on an iPhone 13,
  and Now Playing's swipe and hold stand down inside it - a scroll in it
  could drag Now Playing closed.

  **No reduced-motion exception for these looks, and that was the iPhone
  bug.** Reported as the spinning and moving covers doing nothing on an
  iPhone 13 while working on Android. In Playwright's WebKit with an
  iPhone 13 profile all of them animated, and with Reduce Motion on all of
  them stopped dead - a common iPhone setting, rarely on on Android. These
  looks move only because somebody tapped for them, so they move regardless.
- **Covers of one's own** (`collections/art.go`, `/api/myart`): Change cover in
  a song's or an album's menu, for the song alone or the whole album. The
  picture is cropped square and shrunk to 1000px on the device, and kept per
  person under the state dir's `art/<account>/`, named by its content, so one
  picture for an album is stored once. Keys name what it replaces:
  `song:<source>/<id>`, or `art:<source>/<art id>` with Navidrome's version
  suffix (`al-<id>_<hash>`) dropped, so an edited file keeps the choice. A song
  on an album shares the album's cover id, so a song's own cover has to be
  keyed by the song, and the app decides (`artPath`, `artUrl`,
  `withOverride`): the song's, then the album's, then the library's. An album
  choice covers its songs' own cover ids too (a song with embedded art has
  one), and drops single-song choices on that album. An overridden address
  carries the original after a `#` (never sent), which is how `repaintCovers`
  re-points every image on the page without knowing whose card it is. Only
  JPEG, PNG and WebP by their bytes (never SVG), 4MB, 5,000 covers and 1,000
  pictures a person; served only to their owner, `nosniff`, sandboxed;
  unused pictures are deleted; removing a person removes theirs. **A picture
can come from the server's photos** (2026-10-05, the owner's asking): every
place a picture is chosen - a song's or album's cover, a playlist's, one's
own - first asks **From your photos** (first, the owner's asking) or **From this phone**
(`pickCoverImage`; only with Photos, never offline or on a TV): your photos
is a grid of the person's own photos, newest first, and a tap takes its
large preview (a JPEG even for a HEIC), cropped square as a picked file is
(`squarePicture`). That needed `PhotosOfType("photo")`, which the adapter
had never answered - so the TV's Photos pill, which lists stills that way
(a TV has no timeline), had been failing too; it answers now. **And it is placed by hand** (the
owner's asking, the same day): whichever way it came, the picture opens in a
square frame (`squarePicture`) - dragged to move, pinched, scrolled or the
slider to zoom (1-4 times the size that just fills the frame), never leaving
a gap - and Use keeps what is inside it, up to 1000px; Cancel on a photo
from the server goes back to the photos. Checked on the test server: zoomed
2.5 times, a 600px photo gave the 240px inside the frame. **And turned** (the owner's asking,
2026-10-05): two fingers twisting turn it freely, about the point between
them, snapping straight within 4 degrees, and a Rotate button beside the
zoom turns it a quarter to the right (the way with a mouse). Turned, it
still always fills the frame - the scale that just covers grows with the
angle, and the offset is held so the frame's corners stay inside the turned
picture - and Use saves it turned as shown (`paint`, the same drawing for
the frame and the file). Checked: a quarter turn put the top of a test
picture on the right, and a 30-degree twist saved a tilted square with no
empty corner. **The photos to pick from are a plain grid of squares** (`pickServerPhoto`).
A deck of cards - overlapping squares spreading open around the middle of
the screen - was built through five versions and set aside at the owner's
asking ("too big of a hassle"). The grid sits inside a scroller rather than
being it (`.pick-photos-tiles`): a grid of fixed height squeezed its rows to
40px and the squares overlapped.
**And it has the timeline's handle** (the owner's asking): the photos
are grouped by month under headings (`/api/photos/months` and `/month`,
`type=photo`), each loaded as it comes near, and `tlScrubber` - the same
handle, years and month bubble - takes a box that scrolls
(`opts.scroller`) as well as the page, with its own fetching on letting go
(`opts.land`). Checked with a pretend five years of months: halfway down
showed and landed on July 2024 in the picker and in the Photos timeline
alike, the foot on the last month, loaded, and a tap opened the crop.
**The handle looks cooler** (the owner's asking, 2026-10-05; the picker's and
the timeline's alike): a glowing pill in the accent with an arrow up and an
arrow down, growing and glowing brighter while held, over a faint track down
the edge; the month beside the finger is frosted glass with an accent edge
and a point at the handle; the years are smaller frosted labels. Not in
  `soundstorm backup`, which carries the collection files but not pictures.
- **Up next is rearranged by holding a song and dragging it** (the cards'
  450ms hold and 10px slop): it lifts, the rest slide aside to show where it
  will land, the list scrolls near its edges, and letting go moves it
  (`queueMove`). Moving before the hold completes is a scroll and a quick
  tap still plays the song; the lift at the end is swallowed, not taken for
  a tap. A non-passive touchmove stops the browser scrolling once a song is
  up, and Now Playing does not rebuild the list mid-drag. Checked with touch
  events: the playing song carried on, the order changed as dragged.
- **The title and the artist line are one line each**, and scroll sideways
  when too long - a two-second pause at the start of each pass, then a
  seamless loop onto a repeat of the text (Web Animations, since a keyframe
  offset cannot come from a CSS variable). They used to wrap, and on a phone
  the column under the cover is pinned to the bottom, so a two- or
  three-line title spilled upward over the cover (measured: 45-86px on short
  screens). One line each fixes long titles everywhere. Very short screens
  (iPhone SE, 360x640) still overlap by a fixed 19-36px whatever the title;
  shrinking the cover to fit was built and then set aside at the owner's
  call, as not needed yet.
- **A tap turns the cover into a record**: it rounds into a disc that turns
  (20s a turn) while the music plays and stops where it is on pause; another
  tap squares it. Kept on the account (`prefs.coverSpin`), so it stays as
  left, on every device. The turn is the `rotate` property and the swipe
  slides with the `translate` property, never `transform`: a browser applies
  translate, then rotate, then transform, so a slide by transform went off at
  the record's angle - reported as covers moving off "in a weird direction".
  Measured after: a swipe at 72 degrees moved the cover 150px sideways and
  0px up or down. A new song's record starts from the top (the animation
  restarts), as the cover that slid in did, instead of jumping to the old
  one's angle. The end of a swipe is not taken for a tap. Still under
  reduced motion.
- **Cover to lyrics is a view transition**: the big cover and the small one
  share a `view-transition-name`, only one ever on screen, so the browser
  morphs one into the other. Browsers without them just switch.
  Only the art, the title and the lyrics take part. The page's own
  before-and-after pictures are not cross-faded (that dimmed the whole screen
  for an instant, read as a flicker), the background keeps drifting straight
  through, and the progress bar and controls are laid out identically in both
  views - same gap, full width - so they do not move at all. A check measures
  the bar, progress bar and controls in both views and requires them equal.
- **No cover is the cloud**: `no-cover.svg`, drawn by `scripts/make-icons.py`,
  stands in for a song or album without art in cards, Now Playing and the
  mini-player. Artists keep their initials; a face is not album art.
- **Gapless, measured.** The next song is fetched whole into a Blob while the
  current one plays. Gaps were measured by polling `currentTime` every 4ms;
  `timeupdate` fires only every ~250ms, and the first measurement, taken on
  it, reported ~270ms before and after, which was the event's floor rather
  than the gap. Real numbers: 20-23ms before and 11-16ms after on a fast
  network, 180-195ms before and 15-17ms after on a throttled one. The win is
  on a phone.
- **Leveling from ReplayGain**, which Navidrome 0.64.1 passes through as
  OpenSubsonic `replayGain` (checked). Album gain when an album plays in
  order, track gain otherwise. A -6dB pre-amp lets a quiet track come *up*,
  capped by its peak. It is set through `audio.volume`, **not Web Audio**:
  routing a phone's music through Web Audio is what stops it when an iPhone
  locks, and iOS ignoring a page's volume is the cheaper loss. The user's own
  slider is kept and leveled under.

Not attempted, and why: **sonic analysis** (Plexamp's "sonically similar", its
DJs) is ML over every track's audio, the expensive layer this project does not
own. **CarPlay and Android Auto** need a native app, the graveyard; Media
Session reaches a car over Bluetooth.

Two traps from building it:
- A class named `dock-open` on the dock's cover button collided with
  `body.dock-open`, the page-level class set while the player shows. Its
  `background: none` made the whole page transparent.
- The Now Playing bar is a `<header>`, so the app header's rule gave it a
  background until overridden.

Checked in Chrome against a real Navidrome with a generated library: two
artists, three albums with covers and ReplayGain tags of 0, -5 and +8 dB.
Volumes came out at exactly the computed 0.50, 0.28 and 1.00.

## TV apps

Asked for after the phone apps, for Google TV, Android TV, Fire TV and Apple
TV, with "everything, TV-sized" first. This is the graveyard the top of this
file warns about, chosen knowingly; the shape below keeps it to one page and
one app per platform family.

**Google TV, Android TV and Fire TV are the Android app** (`android/`), not a
second app. The manifest adds `LEANBACK_LAUNCHER`, a banner
(`drawable-xhdpi/tv_banner.png`, drawn by `scripts/make-icons.py`), and marks
leanback and a touchscreen as not required, so phones install it as before.
On a TV (`UiModeManager` television, or the leanback feature) the web view's
user agent gains `SoundStormTV/1`, and the page does the rest. Checked on the
Google TV emulator (Android 16): the app is in the TV's row of apps.

**The page's TV mode** (`TV` near the top of app.js, `tvRemote` at its end,
`html.tv` in style.css; `?tv=1` turns it on in a browser, `?tv=0` off). A TV
has no touch and no mouse:
- **The arrows move a focus** between whatever can be pressed, choosing the
  nearest in that direction (the gap along the arrow plus three times the
  offset to the side). Only the top layer is reachable: a menu over Now
  Playing, Now Playing over the library. Into the tabs from the page lands on
  the current tab. The first press lands on the page's first button, never
  the search box or the tabs. Closing a menu or Now Playing puts the focus
  back where it was, at once.
- **OK presses on letting go**, so that held for 450ms it opens the menu a
  hold or right-click opens instead. Timed, not counted in key repeats:
  remotes differ in whether a held button repeats. The remote's menu button
  does the same.
- **A text box reached with the arrows is only highlighted** (read-only until
  OK). Focused for real it brought up the TV's keyboard at once, which then
  took the arrows - the first emulator run could not leave the search box.
- **A film is the whole screen, with no browser controls**, whose small
  buttons (full screen, more options) the projector's remote could not
  reach. It opens with the picture focused: OK pauses and plays, left and
  right skip ten seconds, Back closes (no close button). The name, the
  subtitle and audio pickers and a slim timeline (`tv-video-bar`) show along
  the foot when a key is pressed or it is paused, and fade after four
  seconds (`tv-bare`, not while paused or a picker has the focus); down
  reaches the pickers, up from a picker returns to the film (the film fills
  the screen, so spatial navigation never finds it "above" anything, and the
  picker could not be left); on a picker the arrows move on and OK opens its
  list; the page behind a film never scrolls. Checked with a generated film
  at TV size, and on the projector by the owner. Now Playing
  opens on play. The remote's play/pause key works either way.
- **The TV's web view reports `pointer: coarse`**, like a phone, so every
  touch rule applied - Now Playing hid its buttons until a hold a remote
  cannot do. `touchScreen()` (JS) and `html:not(.tv)` (CSS) keep the touch
  behaviour off a TV.
- A white ring and a slightly grown cover mark the focus; rows that scroll
  sideways get padding for them (they clip, and the ring was cut off on the
  projector); margins keep clear of overscan. Books were hidden at first and
  came back at the owner's asking; in a book, left and right turn the page
  (reader.js hears them) and up and down reach its buttons.
- **Now Playing on the projector was super laggy and did not fit.** The
  visualizers draw at 0.6 of a CSS pixel on a TV (0.6 dpr, against up to 2
  elsewhere) and at 30 frames a second; the play orb at 1. The 540-pixel
  screen had the column 634 tall, the timeline off the bottom: on a TV the
  lyrics box shrinks (24vh) and the gaps tighten.
  Smoother, and still wanted smoother: the drawing is JavaScript on the TV's
  own processor, phone-class or less on a projector, so on a TV the looks
  also draw half the particles, rain and stars (`VIZ_DENSITY`). With an
  animation on screen Now Playing is laid out for a TV: no "Now Playing"
  words, the title centred at the top, the controls and timeline centred at
  the bottom, and it never scrolls - moving the focus inside Now Playing or a
  film does not call scrollIntoView (it slid up on reaching the timeline).
  **The buttons fade after four seconds without the remote** (`tv-idle`),
  leaving the music, its animation and the title; the next press only brings
  them back (media keys still act), so nobody skips a song by pressing to see
  them. The clear circle round the play orb is not cut while they are faded.
  **Long titles ran off the screen**: centring shrank the title's box to the
  text, so `fitNpLine` never saw it overflow; the box is full width on a TV
  and the title scrolls sideways as it does on a phone.
  **Opening Looks froze the app** until it was restarted. The fade's watcher
  observes Now Playing's classes and woke the buttons by removing `tv-idle`,
  and removing a class that is not there still rewrites the attribute - a
  change the watcher saw and answered by waking again, for ever. A class is
  now added or removed only when it would change; any MutationObserver that
  writes to what it watches must do the same.
  **No play, previous or next buttons on a TV** (the owner's asking, the
  orb too): while the buttons are faded (they stay faded) or none of Now
  Playing's buttons has the focus - it opens with none - OK plays and
  pauses and left and right skip (previous and next song, an audiobook's
  thirty seconds) - but **while the timeline shows, left and right move
  ten seconds** through the song instead, and so they do on the timeline
  itself; only faded do they change song. From nothing in focus, faded or
  not, down goes straight to the timeline and up to the arrow that puts Now
  Playing away to the mini player, the buttons fading back in - so the ten
  seconds is, in practice, the timeline highlighted. Up from the timeline is that arrow (not
  Looks, the nearest), down from the top buttons the timeline.
- **An audiobook on a TV**: the title at the top of the column beside the
  cover and the timelines at its foot, level with the cover's top and
  bottom (they sat together in the middle). With the buttons faded, left
  and right go by chapters - the next, or back to this one's start (the one
  before near its start), as a swipe does; a book without a chapter list
  keeps its thirty seconds.
- **The chapter list on a TV** opens with the highlight on the chapter
  playing; up and down move one at a time, the list scrolling when the next
  is off it, nothing past the first or last; left or right anywhere closes
  it and gives the highlight back to the Chapters button.
  The speed menu does the same (opening on the speed chosen), and both keep
  room for the ring at their ends.
- **Audiobooks play quieter** (`bookGainDb`, Playback on this device, 8dB
  unless changed): a book is mastered loud and a film keeps its dialogue
  quiet, so at one volume a book was far louder (the owner's report). Per
  device, applied as a level under the listener's own volume, as songs'
  ReplayGain is.
- **No view screen scrolls** - Now Playing, a film, a photo, a book - the
  owner's rule, after the projector's audiobook screen was found scrolled
  120px down, its bar off the top. `overflow: hidden` was not enough: a
  browser still scrolls such a box to bring a focused element into view,
  and a remote moves the focus constantly; `.np-inner` was even
  `overflow-y: auto`. Now `overflow: clip !important` on all of them (it
  cannot be scrolled at all), and a scroll listener puts any back for a web
  view without clip. Checked with 300-420px of extra content forced in and
  the focus moved to its foot: nothing moved, on TV and phone sizes. Up
  next, the chapters and the lyrics scroll themselves, as before.
- **No mini player on a TV.** A remote reached it only by scrolling to the
  foot of a page, and it covered the bottom of every one. Its place is the
  side bar's first entry while something plays (`tv-np-tab`): the song's
  cover, "Playing", OK opening Now Playing - left then up from anywhere.
  Play and pause are the remote's own key or OK in Now Playing.
- **Photos no longer stop the music**, on every device (a clip, being a film,
  still does), as a book never did. On a TV, down over a photo or a book
  pauses and resumes the music, with a message saying which; left and right
  step photos and turn pages, their arrow buttons gone.
- **Rings lost under the pinned bars and off the screen edge.** The header
  and the pill row stay pinned and paint over what reaches into them: the
  pill rows (styled by id, `#music-tabs`/`#subtabs`, which outranks a class
  rule) get room above rather than being pulled up, and every grid a little
  room above its first row. A side tab fills the bar edge to edge, so its
  ring is drawn inside it. The remote's own previous and
  next keys skip anywhere.

Checked on the emulator with real key presses: moving through Home, a held
OK opening a card's menu with the focus in it, Back closing the menu, OK
playing a song and opening Now Playing, the arrows among its buttons, Back
closing it with the music going on. Films were not played on the emulator
(the preview has no Jellyfin).

**Set up by a home address, the app sat on the loading spinner.** Installed
on the owner's projector (Google TV, Android 14) and opened at
`http://192.168.0.50:8099`, the page checked it could reach the install's
secure name and moved there - and the app took the move for a link out of
SoundStorm and refused it. Now a main-frame move to https on one of the
install's own names (`*.home.soundstorm.dev`, `*.net.soundstorm.dev`), on the
same port, is kept as the server's address and the page reopened there,
since the page's script and messages are allowed for one origin. Checked on
the emulator opened at the host's address: it landed on the secure name's
sign-in page. Installing on the projector went over the network with adb;
this PC's VPN had to be off to reach it, as it is on another subnet
(192.168.68.x, a Google Wifi router behind the main one).

**Music stayed stopped after an alarm** (Android). The web view pauses on
losing the sound to an alarm or a call and never plays again, and the
service then dropped out of the foreground like any paused player, so the
app could be ended - "turned off". The page tells the app when a pause did
not come from its own pause() (PageScript patches it; an ended song is not
one): the service stays in the foreground and every two seconds asks
whether an alarm, a ringtone, a call or an assistant is sounding
(AudioManager mode and active playback usages). Once one has been and is
over, it sends the page "play". None within ten seconds - headphones pulled
out, another music app - and the music stays paused; thirty minutes at
most. Not yet tried against a real alarm.

**With the screen off, the music stopped after one song** (Android). At a
song's end the page is paused for the moment the next song takes to arrive;
the service left the foreground on that pause, and the next song, starting
with the screen off, had to come back into it from the background - which
Android 12 and later refuse (ForegroundServiceStartNotAllowedException),
and the uncaught refusal took the app down. A pause now keeps the
foreground for twenty seconds before letting go (`detach`), and the
foreground call is guarded.

**Then the Android app's songs went native** (0.9, the owner's call after
the alarm and screen-off bugs, which both came from a page doing the
playing). Songs from the server are played by Media3's ExoPlayer in a media
session service (`AudioService`), which Android treats as a music player:
it keeps the foreground, pauses for an alarm or a call and plays on after,
pauses when headphones come out, and gives the lock screen, notification
and cars their controls. The page still decides everything. PageScript gives
the page's `<audio id="audio-player">` a stand-in on the element itself,
installed the moment it is parsed: setting src to a song on the server,
play, pause, seeking, volume and speed become "audio" messages
(`NativeAudio`), and the player's state comes back every half second and on
every change as the element's own events (playing, pause, waiting, seeked,
timeupdate from the page's own clock between reports, ended). The looks,
lyrics and timeline keep reading currentTime. Songs carry the web view's
cookies (`ResolvingDataSource`), covers too (the bitmap loader). **The next
song is handed over ahead** (`queueNext`, from `preloadNext`, 30 seconds
before the end), so the player moves into it by itself - gapless, never
stopping between songs; it tells the page the song ended, the page sets the
next as always and finds it already playing. Next and previous from the
lock screen go to the page, which holds the queue (a ForwardingPlayer).
Downloads are blob: addresses the native player cannot read, so they still
play in the page, and so does crossfade's second element: crossfade is off
in the app. **The animations heard each song only twenty seconds in**:
the gapless copy in memory was what the song was heard from at once, and
without it a song waited to be well buffered, then fetched its 96 kbps copy
(Navidrome's log showed each about 22s after the song began). So the song
queued next is heard while this one plays out (`hearAhead`, from
`preloadNext`), and `listenTo` finds it done. And after the player moved
into the next song by itself, the stand-in kept "playing" from the last one,
so no "playing" came and its time updates stopped - which also stopped the
song after that being queued; the stand-in starts afresh on "ended" (0.10).
**A song that had played to its end would not play again** (0.11): a
finished ExoPlayer plays nothing until it is sent back to the start, and
the same song asked for again was taken for the one already loaded and left
finished. Now "play" and "load" of the same song on a finished player start
it over, and the stand-in no longer counts a finished song as "already in
it".
**Music stopped while somebody was in another app** (0.12): Android
reclaims a background web view's memory by ending its page, and the app,
rebuilding the page, tore the old one down - which told the native player
to stop. A page Android ends is now dropped without touching the music, and
made again when the app is looked at. And since a page that is gone cannot
hand the player the next song, the page now hands over the next ten
(`nativeUpcoming`, `queueUpcoming`), each with its title, artist, album and
cover for the lock screen, a few seconds into every song rather than 30s
from its end; the player carries on through them by itself. **And the page
made again takes the music over** (0.13): the page keeps its queue on the
device as each song starts (`saveNativeQueue`, up to 220 songs around the
one playing); a new page asks the player what it is doing (`askState`, the
player's "state"), and the stand-in keeps the player's reports even before
the page has handed it a song (`nativeState`). If the song playing is in the
kept queue, the page plays it through the usual path, which the stand-in
takes as the song already in the player: nothing loaded again, the same
moment, playing or paused (`adoptPaused`), the queue around it.
**And a song started by hand was heard minutes in, away from home.** Both
waits were the pace (`pace.go`): hearing waited for twenty seconds in hand,
which at 1.5 times the bitrate after an eight-second burst takes about 24s,
and then the 96 kbps copy was paced too - a four-minute song's copy took
over two minutes. Now the copy asks `listen=1`, which is never paced, and
hearing starts once the song is playing; both only on a link not found slow
(`slowLink`), which keeps the old waits. And what was heard in any song is
kept on the device (`soundstorm-heard-v1`, the last 500, about 45KB each,
oldest dropped; cleared with the downloads on signing out), so a song
played before follows its beats from the first second. The Looks sheet
says which: "Following this song's beats", or tempo only and why.

**Then the server hears every song, once, ahead of time** (`internal/beats`,
`GET /api/music/beats`), asked for as having it all before anything plays.
A phone hearing each song itself meant downloading every song twice and
the first seconds of a song hand-started with the tempo only; now the app
asks the server first (`serverHeard`, about 11KB a song) and only hears a
song itself when the server has not. `internal/beats` is `hearSong` step
for step, the same number types where they change a result, including a
quirk: the chain's end search starts at a fractional frame and so always
ends at the last frame. `scripts/beats-parity.js` runs the real `hearSong`
from app.js under Node on the Go test's samples: 120 beats each, 0ms apart.
On the preview through Navidrome, the server and the phone's method on the
same original file agreed on all 120 beats to the millisecond; the phone's
96 kbps copy runs one frame (23ms) late - a re-encoded MP3 streamed without
its gapless header keeps its encoder delay - so the server's is the truer.

How a song reaches it: Navidrome converts it to mono FLAC and
`internal/flac` decodes it (a decoder of our own, the standard library has
none; checked sample for sample against ffmpeg's decoding, 16- and 24-bit,
all stereo modes). **Navidrome's own "flac audio" will not do**: checked, a
lossy song asked for as FLAC comes back as Opus - it will not turn lossy
into lossless. A transcoding under a name of SoundStorm's (`sslisten`,
`subsonic/listen.go`) is converted as asked, and Navidrome takes a new
transcoding only with `ND_ENABLETRANSCODINGCONFIG` on (405 without), which
compose now sets. That lets Navidrome's admin set a command Navidrome runs;
the only admin is SoundStorm's own account, which can already delete media
through SoundStorm, so it adds little. `PrepareListening` adds or corrects
the transcoding, and remembers a refusal for ten minutes; refused, the
phones hear songs themselves as before. It asks for 11025 a second and
Navidrome sent 44100 (mono honored): the rate is read from the file, and
both rates are paths the phone's code already has.

A background pass works through the library three minutes after start,
two minutes after a music scan, and every six hours, one song at a time
with a rest between; a song asked for before the pass reaches it is heard
then (one at a time, others wait ten seconds, then the phone hears it).
Results are files under the state dir's `beats/`, a cache that can always be
made again, never the music folders. A song heard before AudioMuse knew its
tempo is heard again once it does (the search leans on it); a song too short,
over 30 minutes or undecodable is kept as none and looked at again in a
month; anything else (the network, Navidrome) is tried again next pass.
Navidrome caches each conversion (about 2.6MB for four minutes) in its
100MB transcoding cache, so the first pass churns that cache once.

**Analysis is a look of its own** (`analysisScene`), asked for by the
owner to see what the analysis gives: a timeline moving past the moment
playing (a third of the way across; two seconds heard, four to come), under
the beat of the bar (four dots), the bar and the tempo. Its lanes are the
beats (each bar's first bold and numbered, a bolt on the lightning moments -
those known ahead from the loudness, and those the looks struck as it
played), loudness with the loud-part line (the loudest 30%), the kick and
the snare and hats as the analysis found them, and "stood out", the one
reading judged live. A song not yet analysed says so.

**The hit lanes were scaled to each song, so every song was full of hits**
(version 6). Reported as the hi-hat lane firing through Open Water,
which has no hi-hats. Measured: 94 kicks and about 50 high hits a minute
in it, against 82 and 60 in Night Drive - each song's sharpest half of moments
counted as hits, whatever the song. Now both are on a fixed scale: a bass
jump of 4dB starts to count and 10dB is a full hit (42 a minute against
29, the click track still exactly its 120); the highs are a new band above
7kHz (four one-pole high-passes), 9dB to 21dB (17 against 35). They are
named honestly in the Analysis look - Bass hits, and Sharp highs (drums,
"s" sounds, strums): a singer's "s" lives with the hi-hats, and telling
them apart needs source separation, the ML this project does not own. A
steeper bass filter was tried and helped nothing (it rang on the click
track), and the bass count may be right: that song has a bass line.
Where the beats fall and which starts the bar are untouched - they still
use the old bands and in-song scaling - so only the two lanes the looks
react to changed. Hearing is at 44100 now on both sides (11025 cannot hold
7kHz; Navidrome was already sending 44100, and the transcoding says so).
**For the Mac:** the Apple TV's `SongAnalysis` must follow (version 6).
*(Done on the Mac, 2026-09-30, by asking the server: see below.)*

**Lightning is every strong sharp high now**, at the owner's asking: a
sharp high rising past 80% on the fixed scale (`STRIKE_AT`), counted once
as it rises, however many there are - a limit of one every two seconds was
offered and turned down. The first after six quiet seconds is still the
double strike and Fireworks' finale. It replaced the first beat of each bar
in the loudest 30%, which followed the grid rather than anything heard.
Every frame since the last drawn one is checked, so a hit one frame long is
not missed on a TV at 30 a second. Checked on the click track: 16 strikes
for 16 sharp highs in eight seconds, each within a frame. The Analysis look
draws a bolt and a line through the sharp highs at each, by the same rule.
**For the Mac:** the Apple TV's `VizEngine` drops follow the old rule.
*(Done on the Mac, 2026-09-30: `strikesAt` and the drop values, below.)*

**Then most of Night Drive's loud hits did not strike** (version 7): its every
other beat is a loud sharp hit, and they struck, or showed, seemingly at
random. Measured: from the frame before, those hits rose 11-18dB, and
lightning needed 18.6 - a quarter struck; and a hit whose rise fell across
a frame boundary read as two half jumps, so alike hits differed. The rise
is now from the quietest of the three frames before (13.5-20dB for the
same hits), on a scale of 6-16dB, so lightning is 14dB: 72% of those hits.
It is a lot of lightning - Night Drive about 74 strikes a minute, Change My
Mind about 48 (it has big high jumps on a third of its beats; percussion
of some kind). Requiring the whole sound to jump as well was tried, to
favour a snare over an "s": it dropped Night Drive's snares, which sit inside a
dense mix, and hardly touched the other. **For the Mac:** version 7. *(Done: the TV takes the server's hearing.)*

**And a hit must be heard** (version 8): lightning went off on things the
owner could hardly hear, since a rise from near silence is as big as one in
a chorus. A sharp high counts in full within 10dB of the song's loud highs
(its 95th percentile above 7kHz), fading out over the 4dB below. Measured:
all of Night Drive's loud hits kept, 74 to 68 strikes a minute; Open Water
48 to 34. **For the Mac:** version 8. *(Done: the TV takes the server's hearing.)*

**And lightning is on the beat** (`strikesAt`): asked to look afresh at
why Night Drive's hits on 2 and 4 are right for lightning and little else is.
Every strike in its first twelve seconds, taken apart by pitch band, level
and timing: the good ones land within a few milliseconds of a beat; nearly
all the rest fall 100-350ms between beats (fills, the vocal's "th" and
"s"). So a strong sharp high strikes only within 70ms of a beat found (a
little margin over 50ms, where beat and hit sit a frame or two apart; on
Night Drive 70ms measured 35 strikes a minute against 34). Over the song: 68 strikes a minute to 34, the hits on 2 and 4 kept (72% to
71%), strikes anywhere else 38 a minute to 4; the first twelve seconds keep
exactly the five on 2 and 4 and two right on beat 1. A loud top as well was
tried and changed nothing more. It reads the beats found, so no hearing
changed. **For the Mac:** the same rule. *(Done: `Heard.strikesAt`.)*

**Then 60%, on and between beats, from the owner's taps** (`soundstorm
train-looks rules`, which scores versions of the rule against them): over
ten songs (2,270 taps) it matched best of those tried - overall 48% against
40% for 80% on the beat alone, which had been set by eye; 60% on the beat
alone was 43%. It is better on most songs and worse on Night Drive and lanterns,
whose taps keep to the beat; the owner chose it knowing that. 60% is a sharp
high rising about 12dB rather than 14. **For the Mac:** the same rule. *(Done: `Heard.strikesAt`.)*

**The size now comes from loudness and bass, not the hit** (the owner asked
for fewer big bolts and more lit clouds and cloud bolts): measured over 300 of
their songs, the sharp high tops out on nearly every strike, so sizing by it
made 79% of strikes big close bolts. Now a strike is a close bolt when the
song is at 85% loudness or more with a bass hit (30%+) in the same moment, a
bolt far off or across the clouds from 82% loudness, and otherwise the clouds
lit alone (`dropLoud`, `dropBass`): 12%, 37% and 51% over those songs.
**Storm's lightning is sized by how hard the hit was**, at the owner's
asking for cooler lightning. The strike rule hands the looks `dropPower`,
the peak of the sharp high over the four frames from where it crossed (the
analysis is the whole song, so it can look ahead). Past 60%: under about
70% is sheet lightning, the clouds lit from inside with no bolt; to about
85% a thin bolt far off, ending in the sky; above that a thick close bolt
to the ground with branches, a flash over the screen, a glow where it lands
and a burst of spray and splashes. Every bolt starts at the very top of the
screen. Each strike is one flash fading over about 0.4s (`stormLight`) - a
flicker of two to four return strokes, as real lightning does, was tried and
the owner preferred the single flash - and a faint image of the channel
lingers a second after.
**The bolt's shape was rebuilt to be lightning's** (the owner asked whether it
was the best shape; it was an even wiggle with long straight branches that
crossed in a web): jagged at every scale - a few big kinks, then each stretch
broken by midpoint displacement (`stormJag`), so it zigzags up close as it does
from afar - with few branches, short, angled 20-55 degrees down, forking once
more, gathered near the top and shorter the lower they start, and on a close
bolt one strong branch. Each branch thins and fades to its tip. **Not every
bolt falls straight** (the owner's asking): half crawl across the sky
just under the clouds, never reaching the ground, lighting the clouds all
along the way (four lights rolling with it) and drawn over the clouds (under
them, a TV's wide clouds hid them); a quarter lean, landing well to one side,
their branches mostly leaning the same way; the rest come down near straight.
**A crawler spreads rather than aims** (`stormSpider`; the owner: ground
strikes perfect, cloud ones too much one line going somewhere, and should
branch more): from a point under the clouds two or three arms wander off in
different directions, each turning a little at every kink, with branches all
along them on both sides, up as well as down, forking twice more
(`stormBranchAt`). The ground strikes' shape is unchanged.
The bolt thins towards its end, branches dim faster than the main channel
(one faint glow round it: the two bands it had read as a ghost of the bolt,
none looked bare, and the owner chose between),
and the glow, the clouds' light and the flash take a touch of the cover's
colour. The rain brightens and gets a white streak during a flash.
**The clouds are a deck over the whole top of the screen, there all the
time** (the owner's asking; it was a few dark heaps): five rows of lumpy puffs,
the first along the top edge (with four starting above it the very top showed
through dark), over a band the clouds' own slate at the top
(`stormPuffShape`, blobs bumped along the top, drawn into padded canvases so no
edge is cut square), some a shade lighter, the lower ones bigger and drifting
faster. Each strike lights them its own way (`stormLights`): a close bolt a wide
patch where it leaves the clouds and one or two beside it, a far one less,
sheet lightning one to three patches anywhere, each a moment after the last so
the light rolls across. A lit puff is the same shape drawn pale over the dark,
by nearness and its own thinness, under a broad soft glow. **Lit puffs are
painted over, not added**: four or five overlap in places, and added together
they burned the deck white; painted, they build only to the lit colour, the
shapes still showing. **A bolt comes out of a cloud**: the upper rows of the
deck are drawn a second time over the bolts and the rain, so the bolt's top
is hidden in a cloud lit by it rather than starting at the screen's edge
(the owner's asking), and the rain falls from under the cloud. **The top edge
is solid**: the band is opaque there, and a cap is drawn again over the rain
and bolts, since on a phone the backdrop and rain still showed through at the
very top - checked against a red backdrop, no pixel in the top 8% less than
solid.
**The rain has four upgrades** (five were asked for at once): depth (each drop a
`z`: far ones thin, dim, slow, short and landing higher up, near ones thick,
bright, fast and long; nine batches, three depths by three colours); gusts
(every 10-25 seconds, and sometimes with a strong strike, the wind picks up
for two to four seconds, mostly with it, now and then against); heavier with
the music (`st.heavy`, eased: each drop has a place `k`, and in a quiet part
only the drops under the heaviness fall - a drizzle, then a downpour, more
drops and not only faster ones); and mist along the ground where the rain
lands, thicker in a downpour and lit by the flash. Drops on the glass (big
ones landing on the screen and sliding down) were the fifth, and taken out
at the owner's asking. A drop starts where the
wind of the moment will carry it across the screen by the time it lands, so
no corner goes dry, gusts included (the bottom left of a tall phone was dry
when the margin was a share of the width). Clouds and glows are one soft sprite drawn
once per colour and stretched (`stormSprite`), not gradients per frame.
57fps in Chrome at phone and TV size. **For the Mac:** the same, from
`dropPower`. *(Done on the Mac, 2026-09-30: `Storm.swift` is this Storm,
sized from `dropLoud` and `dropBass` as the page now sizes it.)*

**Every other look got its own strike, and more to it** (2026-10-05, the
owner's asking: Storm left exactly as it is, the rest made as good - some
were too plain). Each answers the moments Storm's lightning falls on
(`m.drop`) with something of its own, sized by Storm's own rule
(`strikeSize`: small under 0.3, middling to 0.7, big above - about half,
three in eight and one in eight): the **Orb** throws prominences, loops of
plasma arching off its edge (`plasmaArc`), the big ones a corona of streaks
and the vortex flung out, plasma crawling on its edge between; **Spectrum**
fires beams up from its bars and the caps fly off as sparks, a big strike
every bar (a soft ragged row, not a wall of white) and a line sweeping the
floor, embers rising off tall bars; **Warp** jumps - stars stretched, a
shock wave in the cover's three colours, white on a big one - with nebula
clouds rushing past; **Waves** sends surges rolling along the ribbons
throwing spray, glints on the crests; **Kaleidoscope** shatters, shards out
of every wedge and a prism ring, a big one spinning it hard, with a web
between its shapes and a ring of facets; **Fireworks** a crackle, a peony
whose sparks split, or a chrysanthemum with a ring and glitter, the smoke of
each hanging lit; **Flow** veins of light racing along its own current, the
particles flashing white; **Synthwave** a pulse racing down the grid, a sun
flare across the horizon and a shooting star, the sun lying on the floor in
soft streaks; **Galaxy** novae with a cross of light, jets from the core on
a big one, haze along the arms; **Aurora** a flare running along the
curtains, a corona of rays on a big one, mountains along the foot and
shooting stars; **Lava** an eruption of glowing drops from a heat at the
foot, a blob kicked up (a new one on a big strike). The Orb became a scene
like the others (`VIZ_SCENES.pulse`, its state its own). Glows are one
sprite per colour stretched (`vizGlow`, `vizStreak`), colours made once per
frame or per spark, never per spark per frame. Checked by driving each look
through a small, middling and big strike on a pretend 120 bpm song and
looking at each moment, with the processor slowed four times: 95% of frames
drew in under 3ms in every look; and on the real Now Playing, every look ran
with no error. Not seen: on a phone or the projector, with real songs.
**No circles going out from the middle, in any look** (2026-10-07, the
owner: "I don't like those on really any of them"): Flow's pulse on each
beat, the Orb's shock ring on a strike (its flash stays), Galaxy's ripples,
Warp's shock waves and Kaleidoscope's prism rings went, and so did the
Kaleidoscope's ten-pointed ring of facets round it all. Storm was not
touched; Warp's tunnel (hexagons), Synthwave's grid lines and Fireworks'
ring-shaped bursts are not circles growing out, and stay. Checked by drawing
the five through three strikes each.

**The Analysis look can be dragged** (`analysisScrub`), the owner's asking:
the timeline follows the finger - left for later, right for earlier, at the
look's own scale of six seconds across - "Play from 1:23" shows over the
playhead, and letting go plays from there. A touch that starts on the
timeline is the look's: Now Playing's swipe (changes song) and hold (the
buttons) listen on the whole screen, so capture-phase listeners on the
document stop the touch and pointer events before they arrive. A tap there
still reaches the page as a click, so a double tap still changes the look;
holding for the buttons works everywhere but the timeline. While dragged,
the frame reads `viz.scrubAt` for the moment and keeps drawing when paused.
Checked with real touch drags (CDP) at iPhone size: 3 seconds on and back,
same song, Now Playing still open.

## Training the looks (the developer's own install only)

Asked for as tapping where lightning should be and letting a model learn
when it is right - then widened to every look: they all read the same few
signals each frame from `viz.frame` (big moments, beat pulses, bass hits,
sharp highs, intensity), so a trained signal improves every look using it.
**It is a tool for SoundStorm's developer, not a feature**: the owner
decided nobody else - owners included - should ever be able to do it, and
that what is learnt ships in SoundStorm for every install.

- `SOUNDSTORM_TRAINING=true` in one install's `.env` (this PC's; compose
  passes it, default false). Without it the routes answer 404 and the
  session never says `training`.
- In the Looks sheet, "Training (only on this server)": **Tap big moments**
  (each touch a moment, at the player's time plus Timing) or **Slide
  intensity** (a held finger's height, ten times a second; a gap means
  nothing was said). Now Playing's own gestures stand down while it records
  (capture-phase listeners, as the Analysis drag uses); buttons still work.
  A bar at the foot counts, undoes and clears. The Analysis look draws the
  taps in cyan and the intensity as a cyan line over the loudness.
- Kept per song under the state dir's `training/`, a file each, with what
  the song is (from the library, never the request) and the device's
  Timing (`lead`), to separate the device's lateness from the hand's.
- **The training script** is `soundstorm train-looks` (`internal/training`),
  run inside the server's container beside the running server:
  `docker exec soundstorm soundstorm train-looks`. It reads Navidrome's
  credentials read-only (`state.ReadBackends`; `state.Open` would rewrite
  the file), hears each recorded song again through the listening
  transcoding with the tempo prior the server's cache used (so the beats are
  the server's), and keeps the raw readings (`beats.Analyzer.Frames`).
  Candidates are frames where something rises (a local peak of the band
  rises); each gets 15 measurements (`FeatureNames`: rises and levels per
  band, loudness, distance from the beat, beat 1-4, between beats, how much
  it stands out) - none of today's thresholds. Taps are lined up by the
  song's median lateness (strongest candidate in the 400ms before a tap),
  then to the strongest candidate within 80ms; only the tapped span counts,
  and candidates near a target are left out of training. The model is a
  logistic regression (scaled features, big moments weighted up, a little
  decay), its threshold the best F1 on what it was fitted to. Scored
  leave-one-song-out against today's rule (strikes within 70ms of a moment
  meant), with what it learnt listed; intensity is a ridge fit of the slid
  level on loudness, bass, sharp highs, hits a second and tempo against
  loudness alone. The model is written to `training/model-big-moments.json`;
  building it in, and the raw-sound and borrowed-ears models, come once
  there are recordings to compare them on. The test on generated click
  tracks tapped on each bar's first beat: the model caught 98% and learnt
  beat 1 (+1.6) over beats 2-4; today's rule 13%.

**The sound went out and the song played on** (the owner's report,
2026-10-01, the Android app, fully silent, back on the next song). Nothing
in the code explained it, so Android 0.20 carries a playback log
(`PlayerLog`: the player's volume, audio focus and suppression, the audio
track opening, closing and failing, outputs coming and going, the page's
commands, last 600) and, on the developer's install only (the training
switch), **Sound cut out? Send a report** in Playback on this device, which
keeps it with the page's view under the state dir's `diagnostics/`
(`POST /api/diagnostics/playback`) - read it with
`docker exec soundstorm ls /var/lib/soundstorm/diagnostics`. One guess is
guarded meanwhile: the page sends its volume again whenever the player
reports a different one. Waiting on a report to find the real cause.

**Playing on another device from your phone, and controlling it**
(`players.go`, the owner's design, 2026-10-02, worked out in conversation):
every open page is a player - it says hello (`/api/players/hello`, an id kept
in localStorage, a name set in Settings > On this device, "TV" on a TV),
reports what it plays every few seconds (`/state`), and long-polls for
commands (`/next`, 25 s); phones and TVs only ever talk to the server, so no
pairing or same Wi-Fi. Any item's menu has **Play on...** and Now Playing's
**Play on another device** (which moves what is playing, from the same
moment for a song; a book's and a film's place go through the server's
saved position); then a remote sheet (`#rc`, polling the device's state:
play/pause, -10/+10, previous/next, seek, volume, **Play here**, Stop) and a
chip to reopen it. The rule that keeps accounts apart, the owner's: **a
device acts as one person, and only that person's phones control it** - so
a phone or computer only by its own person, and a **TV**, being shared, by
anyone in the house, but only by taking it over: sending something to it
switches it to the sender first (a one-time code, `/api/players/switch`,
open, 128 bits, used once; the TV signs in as them, keeps them on the device
and reloads past "Who's listening?" - `soundstorm-switched` - with what was
sent waiting behind the switch, never delivered with it). A TV that is free
(not playing for three minutes) switches at once; one in use asks on screen
("Sam wants to play something. Let him?" `#player-ask`) - no answer in
15 seconds is a yes, a No holds that person off 5 minutes, a second 30. What
another person's TV plays is never shown to someone who has not taken it
over. Two traps met building it: commands after a switch were taken by the
page that was leaving (it polled once more before the reload) - the server
now delivers up to a switch and no further, and the page stops listening
once it switches; and `remote-toggle` was already remote access's switch -
the remote sheet's ids are `rc-*`. Checked in Chrome as a TV (Alex) and a
phone (Sam): asked, allowed, switched, and Sam's photo open on the TV;
the remote showing it. **Then reported as finicky, the volume glitching, the controls off a
phone's screen.** The remote redrew from the device's last report every
1.5 s, so a tap flipped back until the next one (up to 2 s): now what a tap
does shows at once and is held for 3 s against reports still on their way
(`holdRemote`), the device reports straight after each command, and play
and pause are sent as such, not as a toggle. The volume was sent only when
the finger lifted and the next report pulled the slider back: now sent as
it slides (every 200 ms) and left alone for 2.5 s after a touch. The sheet
is capped at the screen's height with a smaller cover; checked at 360x640
with everything showing, the TV's level following the slider to 0.3.
**And a Play on button on the playing screens** (the owner's asking):
Now Playing's top bar has `#np-cast` (a cast icon), which a phone keeps
invisible until a hold like the rest of that bar, so it shows and is chosen
with them (`holdButtonList`); the film player has a visible "Play on..."
beside its pickers (`#video-cast`), which saves the film's place first and
closes it here once sent. Neither on a TV, offline, or for a downloaded film.
**Then Now Playing itself became the remote, for music** (the owner's
idea: "why not use the same Now Playing screen as the remote"). While a
phone controls a TV's music, the page's audio element stands in for the TV
(`remoteLayer`, `RA`, `enterMirror`), the way PageScript's stand-in hands
songs to Android's native player - and on top of it, delegating to whatever
the element was before when the mirror is off: setting a song tells the TV to
play it with the queue (unless it already is), play, pause and seeking are
sent, and the TV's reports (polled each second) come back as the element's
own playing, pause, timeupdate and ended - "ended" when the TV has moved to
the next song in the queue by itself, which the page then follows without
sending anything. So lyrics, looks and visualizers (the server's analysis),
Up next, swipes, the hold buttons and the lock screen all act on the TV, and
the phone streams nothing. "On Living room TV" sits at Now Playing's top
(`#np-where`), a tap opening the compact remote for the volume, Play here
(the queue carries on on the phone from the TV's moment) and Stop. While
mirroring nothing is counted, scrobbled, preloaded or crossfaded here: the TV
does that. X or anything else that stops this phone's audio pauses the TV and
lets it go. `RA` is declared near the top of app.js, as crossfade's setting
reads it while the page loads. Music only - an audiobook's clock spans its
files; films and photos keep the compact remote. Checked with a TV pretending
to play (two browsers): Now Playing opened with the label, its clock followed,
pause, seek, play and previous reached the TV, and the TV moving on by itself
was followed with no command sent. Not checked: inside the Android app, where
the layer sits over PageScript's stand-in.
**And the look goes across** (2026-10-03, the owner's report: changing the
look on a phone controlling a TV left the TV as it was): a look chosen while
mirroring is sent to the TV as `control` `look` (`tellLook`), and the current
one when the phone starts controlling it; the TV applies it (it is the same
person's, so the account's choice was already saved by the phone). Checked
with two browsers: the look sent, the other screen on Storm. **For the Mac:**
the Apple TV's `RemoteControlled` should obey `look` too. *(Done on the Mac, 2026-10-03: shown, not saved, as
on the page - `AppModel.showLook`. Built; not tried with a phone.)*
**Then audiobooks, and the phone's volume buttons** (2026-10-02, the owner's
asking). A book sent to a TV gets the same treatment: the element stands for
the file of the book the TV is in, so the book's own player - chapters, the
thirty-second jumps, the chapter timeline, swipes - works unchanged; a place
is sent as the time in the whole book (what the TV's `goToBook` takes), the
phone follows the TV into another file without telling it (`raPoll`), a
book's speed is sent (`control` `rate`; the TV reports its `rate` so the
clock runs at it), and the phone saves no place while mirroring - the TV
does. A book sent from its card does not seek until the TV has said where it
picked up (`RA.known`), or the phone's own resume would pull it back. In the
Android app (0.27) the volume buttons turn the TV's volume while the phone
controls it - mirrored or the compact remote - 5% a press, with the level
shown at the top (`__soundstormVolumeKey`, `remoteVolume`,
`MainActivity.dispatchKeyEvent`); only while the app is in front, and never
in a browser, which cannot hear the buttons. Checked with a TV pretending to
play a three-file book: skips arrived as 104 and 134 seconds (120 first, the
next file's start), the phone followed the TV back into the first file,
1.5x went across, and two presses took the TV to 90%. Not checked: on a real
phone and TV.
**Then one switch instead of Play on** (2026-10-02, the owner's design:
"between using my phone's screen for my phone and using it for the TV ...
there's a lot of weird scenarios"). A device button in the header
(`#ctl-btn`, `CONTROL`, `chooseDevice`) opens a picker of this phone and the
person's open devices (every TV). Choosing a TV takes it (`claim`, a command
that plays nothing; on somebody else's TV it asks or switches as "play"
does) and from then on what is played goes there: music and books by the
mirror (`playAudio` attaches it, `controlAttach`), a film to the TV with the
remote sheet open (`playVideo`), a photo shown on both (`showPhoto`, and
closing it closes the TV's). What is playing here when the TV is chosen moves
there from the same moment; something paused here is left. **Browsing stays on
the phone** - the owner's choice when asked, as the TV is often being watched.
Choosing the phone again leaves the TV playing (`dropMirror`: cleared here, no
place saved, nothing sent), and a chip says "Playing on TV", a tap choosing it
again; whatever the TV plays then - or starts by its own remote - is picked up
on the phone (`raAdopt`, `watchControl`), next and previous going to the TV's
own queue when the phone does not hold it. The choice is kept on the device
(`soundstorm-control`) while that TV stays this person's. The picker has the
TV's volume and Stop. The old Play on menu entries are gone; Now Playing's
cast button, its menu entry and the film player's Play on open the picker.
Checked with a TV pretending to play: chose the TV, a queue played there and
next moved it, back to the phone with the TV playing on and the chip showing,
the chip brought the mirror back, and a film went to the TV with the remote.
Not checked: on a real phone and TV.
**"They said yes and nothing happened"** (2026-10-03, the owner taking an
Apple TV from somebody): once the answer was yes the phone sent its music at
once, before the TV had signed in as it, and the server took that for a new
request to take over somebody else's TV - a second question, the phone's
202 ignored, the music lost. Now anything a person sends while a switch to
them is waiting (`switchingToLocked`) waits behind the switch.
`TestWhatIsSentAsATVSwitchesWaitsForIt` fails without it.
**Photos on the TV follow the phone's swipes** (2026-10-03, the owner's
asking; it already sent each photo): a step carries `step` (the TV says
"Playing from your phone" only for the first) and the photos either side
(`near`, which the TV fetches ahead, so a swipe lands on a picture it
already has), and the phone's LIVE plays the Live Photo's moving part on the
TV too (`control` `live`). **For the Mac:** the Apple TV's `RemoteControlled`
could take `near` (fetch ahead) and `live`; it already shows each photo sent. *(Done
on the Mac, 2026-10-03: a photo sent while the viewer is open steps it in
place (`AppModel.phonePhoto`) with the ones either side fetched ahead, the
first says "Playing from your phone", and `live` plays the moving part.
Checked with commands sent by hand: IMG_0002 then a step to IMG_0003, the
viewer staying open. The pictures were not seen: no photo backend here.)*
**Then a video sent from the phone would not play on the Apple TV, and each
swipe closed and reopened its viewer** (the owner's report, 2026-10-03). The
phone's viewer now plays videos among the photos, so stepping onto one sent
a clip, which the TV took for a film: it closed the photo viewer and opened
its film player in the same moment, and tvOS shows one full-screen view at a
time, so the film never appeared. Now a clip plays inside the TV's photo
viewer, in place, as on the phone (`playClip`, the music paused for it; OK
plays and pauses it), its own Photos grid steps through clips too, and a film
sent while Now Playing or a photo is up waits for that to go first. Checked
on the stand-in: the clip played in the viewer. Not checked with a phone
sending: the swipes need the TestFlight build of 2026-10-03 17:21 or later.
Clips are not in the swipe (the viewer walks photos only; a clip opens as a
film, sent to the TV with the remote).
**A TV switched off was still "controlled"** (2026-10-03, the owner's
report): the server lists a player for 90 seconds after its last word, and
the phone gave up only once it left the list. Each player now says how long
it has been quiet (`quiet` in `/api/players`), and the phone takes over 30
seconds as switched off (`CONTROL_QUIET`: a TV reports every few seconds, its
long poll waits 25) - "TV seems to be off. Playing on this phone again."
**And the chip at the foot of the screen went** (the owner: two things said
what was controlled); the device button in the header says it. Checked in
Chrome as a phone and a TV page: the TV closed, the phone let go 22 seconds
later. Made on the Mac (small, pushed at once); reaches phones once the PC
deploys.
**Two phones cannot control each other** (2026-10-04, the owner: controlling
one phone from another and the other back at the same time made "weird stuff"
happen - each sent the other's music back). A command to another device names
the sending device (`X-Soundstorm-Player`, added by `api()` for
`/api/players/.../command`); the server remembers who last controlled each
device (`controlLoopLocked`, half an hour), and when B sends to A while A
controls B, A is sent `released` and lets go (mirror dropped, film remote
closed, a message saying why): the newer choice wins.
`TestTwoPhonesDoNotControlEachOther`; checked with two browsers as phones.

**Built on the PC, 2026-10-03 (`openFilmRemote`, `FR` near the top of app.js,
as `closeVideo` reads it):** the phone's film overlay in `vr-on` mode - no
video element playing, the film's art blurred behind play, -10/+10, the
timeline and "Stop on the TV", "On <TV>" opening the device picker - and the
Subtitles, Audio and Quality pickers sending `control` `subtitle`, `audio`,
`quality`, which the TV does as its own pickers would (audio and quality play
again from the same moment, the subtitles kept on). The TV's report carries
`subtitle`, `audio` and `quality`, which the phone's pickers show; it polls
each second, follows the TV into the next episode, and closes when the TV
stops showing a film (after eight seconds' grace for it to start). The phone's
volume buttons turn the TV's (`PLAYER.target`). Checked with a throwaway
Jellyfin and a generated film with two languages and a subtitle, a browser as
the TV and one as the phone: playing on the TV, pause, a jump to 1:00,
subtitles on, Spanish, the phone closing with the TV playing on, nothing
streamed to the phone. **Not built:** the lock screen on the phone acting on
the TV during a film. **Not checked:** Up next on a real show.
**For the PC: a film on a TV is controlled from the phone's own film
player** (decided with the owner 2026-10-03: "everything remotely controlled
should be done naturally as it is navigated on the phone itself"; music and
books already are, through Now Playing's mirror; photos step with the phone's
swipes; a film still opens the compact remote sheet). Built as the music
mirror is (`remoteLayer`, `RA`), on the video element:
- **Playing a film or an episode with a TV chosen** (`playVideo` while
  `CONTROL.target`) sends it to the TV as now, and opens the phone's ordinary
  full-screen film player - not the `#rc` sheet - with no picture: the
  film's poster or still blurred behind the controls, and "On Living room
  TV" at the top (as Now Playing's `#np-where`; a tap opens the device
  picker for volume, Play here and Stop). Nothing is streamed to the phone.
- **The phone's controls act on the TV**: play/pause, the timeline (a drag
  sends `seek` on letting go), the ten-second skips, and the Subtitles,
  Audio and Quality pickers - new `control` actions `subtitle` (a track
  index, or off), `audio` (a stream index) and `quality` (the `vq` value),
  each of which the TV does as its own picker would (another audio language
  or quality plays on from the same moment, as on the TV itself).
- **What the TV does comes back** from its reports (`playerState`, polled
  each second while the player shows): the time, playing or paused, and the
  item - so Up next counting into the next episode, or the TV moving on by
  itself, shows on the phone without a command. The TV's report should add
  the subtitle, audio and quality in use and the tracks it has, for the
  pickers to show the TV's own.
- **Leaving the player keeps the TV playing** (as Now Playing's arrow does;
  the device button in the header still says what is controlled); a Stop in
  the player stops the TV and closes it. Back on the phone is leaving.
- **The lock screen and the phone's volume buttons** act on the TV while the
  player shows, as for music (`remoteVolume`, the Android and iPhone apps'
  volume keys).
- **Play here** carries on on the phone from the TV's moment (the film's
  place goes through the server's saved position, as now).
- The `#rc` sheet stays only for what has no screen of its own on the phone
  (or goes, if nothing is left).
**For the Mac, after it:** the Apple TV's `RemoteControlled` obeys
`subtitle`, `audio` and `quality`, and reports what is in use (its player
already does play, pause, seek and skip). *(Done on the Mac, 2026-10-03:
`RemoteControlled` takes the three (subtitle an index or empty, audio a
stream index, quality smart/original/standard/saver - `VideoSession.
chooseQuality` plays on from the same moment in the same language) and
reports `subtitle`, `audio` and `quality`. Checked against the stand-in
server given the players' routes: a film sent, then subtitles on, the second
language and data saver, each reported back, the film playing on.)*
Not built: typing on the TV from the phone; the Apple
TV's half (for the Mac: it would say hello, poll and obey the same
commands).

**Back on Android 16 needed the new API, on phones too.** An app built for
API 36 no longer gets `onBackPressed`: the system closed the app on Back from
inside a menu or Now Playing. `MainActivity` registers an
`OnBackInvokedCallback` (API 33+) that does what `onBackPressed` did.

**Apple TV is a separate native app, on the Mac, after the iPhone app.**
tvOS has no web view, so the page cannot be wrapped as on phones and on
Android TV. The plan to start from: SwiftUI on tvOS, talking to the same
`/api` the page uses (search, home, music, playback, streams, art), with
AVPlayer for films (Jellyfin's HLS through `/api/hls` plays natively) and
music. A native player also brings the system's own Now Playing and remote
controls. Keep it to watching, listening, photos and books, as on Android TV.
**The owner has decided: a real Apple TV app, not AirPlay** (2026-09-30).
The web page's TV mode cannot be reused (no web view on tvOS); what carries
over is every behaviour settled above for Android TV - build to it rather
than working it out again: OK plays and pauses in Now Playing, left and
right skip (ten seconds while the timeline shows), the buttons fade after
four seconds and the next press only brings them back, no play, previous or
next buttons, the playing song's cover first in the side bar in place of a
mini player, lyrics only on the Lyrics look, down over a photo or a book
pauses the music, photos and books keep the music playing. tvOS's own focus
engine and AVPlayer give the highlight and the remote's media keys; the
visualizers would be redrawn natively (Metal or SpriteKit), the largest
part.

## Home, and five tabs instead of ten chips

The shelves were a sideways row of ten chips, most off screen on a phone.
Now five tabs - Home, Music (mixes, playlists, songs, albums and artists), Watch
(films, TV), Books (audiobooks, ebooks, documents), Photos - along the bottom
of a phone and down the side of a computer. The chips are still in the page,
hidden, and a tab presses one, so every search path is unchanged. A shelf the
account may not see, or with no files, is left out; an empty tab is hidden.

Home is a front page, not "everything" alphabetically: Continue, then strips
of new music (the newest album folders), favorites, recently played, and
`GET /api/home`'s newest items per shelf. That asks every source at once
through `source.RecentLister`, five seconds each, and leaves out a shelf
that fails, the search's rule. Each backend orders by its own record of
arrival - Jellyfin DateCreated (a series by DateLastContentAdded, so a new
season brings it forward), Audiobookshelf `sort=addedAt&desc=1` (checked on
a real library), Immich's own newest-first, and for the folders SoundStorm
reads itself the file's modification time. Typing on Home searches
everything.

**Home opens with one-tap music**, above everything else, for an account
with music (`homeQuickPlay`), asked for as "shuffle all music, right when you
get on": **Shuffle all** across the top - a radio station (`mode:
"shuffle"`) and a true random shuffle, as the owner asked: each batch drawn
with equal odds from the songs not played yet this session, with none of the
other stations' leaning away from recent plays or spacing of artists and
albums; endless, and never repeating until the library has been through
(the app sends the last 1,500 queued), where the Shuffle everything mix stops
at a hundred - then Favorite songs (shuffled, only with
some), Music radio (Library radio) and New music. Every name says music
(Shuffle all music), since Home holds every shelf and "Shuffle all" alone
did not say what it would play. They are a row of slim pills (38px,
the icon beside the name), Shuffle all in the accent - big tiles read as
clunky (the owner, 2026-10-03); on a phone two columns, all in sight, never
swiped to (Shuffle all across the top when there are three).

**The pills can be put in any order**: hold one (450ms, as a card) and slide
it; the others move aside as it passes their middles (a FLIP animation), and
the row scrolls near its ends. Kept on the account, in the person's collections file (`prefs`,
bounded), behind `GET`/`PATCH /api/prefs` - it was per device first, and an
order saved that way is carried over once and the device's copy dropped.
Swiping between pills, and the shelf a tab opens on, follow the order, since
both read it from the DOM and `tabShelves`. The click that ends a hold is
swallowed, and while a pill is up touchmove is prevented so the row cannot
scroll away under it.

**Settings has pills too, and its own search.** Its cards are grouped -
Library, Playback, Devices, People, Account - by a `data-cat` on each, in
the same `#subtabs` row the shelves use, so holding and sliding reorders
them (kept as the `settings` pill row) and swiping steps between them: the
swipe's page list includes `#account`. The ghost takes only pages actually
on screen (`getClientRects`), because under Settings the lists are hidden
by the stylesheet, not their class, and moving their cards into the ghost
would lose them. A pill shows only when one of its cards is visible to
this account, followed by an observer since the owner's cards appear
late. While Settings is open the search box searches settings - every
card holding all the words typed, in its text or its `data-words`, across
every pill - and the library's query and placeholder come back on leaving.
**Then tidied, and folded** (2026-10-04, the owner: "messy and
unorganized"). Regrouped in index.html's order - Library: Add media, Your
photos, USB drives, Downloads, Making books; Playback: Playback on this
device, Lyrics, Music discovery, Read-along, Scrobbling; Devices: Use on your
phone or TV, Sign in a TV, Reach it from anywhere, Server name; People;
Account: On this device, Change password, Start over, About, Sign out. Every
card folds to its title and a line saying what is in it (`data-hint`), and a
tap opens it (`foldSettingsCards`, the card's `h2` made a button, everything
else hidden while `folded`); what is open stays open while the app is
(`state.settingsOpen`), a search opens every card that matches, a pill of
one card shows it open, and the welcome's buttons open their card
(`openSettingsCard`). Sign out has no heading and always shows.
**Support SoundStorm** (2026-10-04, Account, before About): a line and a
**Sponsor on GitHub** link to `github.com/sponsors/GabrielHollberg` (the
owner's Sponsors profile; `.github/FUNDING.yml` puts the button on the repo).
**Never in the iPhone or Apple TV apps** - Apple allows tips to a developer
only through its in-app purchase, and a link out can get the app rejected -
nor on a TV (`showSupport`: hidden when `soundstormApp` is there on an
iPhone or iPad). The Android app is installed directly, not from Google
Play, so it shows there; look again if it ever goes on the Play Store.
Folded like every card, with no reminders anywhere.

**The + at the end of a row of categories** lists the ones put away; one
tapped comes back and opens. Hold a category and drag it onto the + to put
it away (never the last one; the row holds still over the +, or scrolling
near the edge slid it from under the finger). Kept on the account as
`prefs.hiddenPills` per row. A row missing there has the defaults, which
put Genres away - so an empty list is stored as an empty list, never null,
or "I added Genres back" would read as "defaults" and hide it again.

**Genres** (Music, Watch, Books) are grouped by the server from the shelves
it already lists and caches (`GET /api/genres?kinds=`), not asked of each
backend: every item carries its genre (`Extra["genre"]`, now also from
Jellyfin and Audiobookshelf; an EPUB's subjects as `tags`), and
Audiobookshelf's entries are often several joined with commas ("Action &
Adventure, Dystopian") - split, and one genre per name whatever its case.

**Black at both ends, to meet Android's bars.** The theme color (the
status bar) is #000, and so are the header and the pills under it - one
solid band, no blur, no line between them - and the phone's tab bar, with no
top line, which runs into the black navigation bar. The pills stick a pixel
under the header, and `--header-h` is the header's real fractional height
(`getBoundingClientRect`): `offsetHeight` rounds, and on a phone's
fractional pixel ratio that left a sliver where the page scrolled through,
reported as a gap above the pills.

A page wider than the phone made the phone zoom out and dropped the tab bar
off the bottom: the album sort sat inside the music row. The row is now
pills that scroll inside their own strip, and `body { overflow-x: clip }`
stops anything else doing it (clip, so the sticky header still works).

A sideways swipe steps between the pills wherever a row of them picks the
page - Music, Books, Watch - anywhere below the header, including the empty
space under a short list. It behaves as pages of one strip, edge to edge:
the moment the gesture locks sideways toward a neighbor, the page is
copied into a ghost laid where it was and the neighbor's pill is pressed,
so its page loads while the finger is still down and rides beside the
ghost. A third of the screen or a flick commits; otherwise the ghost springs
back, the original pill is pressed again behind it, and the scroll position
is put back. touchmove is not passive: once locked sideways it prevents the
scroll. Not on an album, artist or playlist page (the swipe would throw it
away), and not from something that scrolls sideways itself.
`overscroll-behavior-x: none` while pills show, or Chrome also takes a
rightward swipe as Back - the first test run landed on about:blank.

Three things made it glitch on a phone, all fixed. The ghost was a deep
clone, whose lazy images drew blank for a moment: now a list's own nodes
are moved into it (the next render replaces them anyway), and only the
fixed parts with ids of their own - the count, Select, the Continue row -
are copied, because moving those deleted them with the ghost. It scrolled
to the top as the swipe began, so the pills jumped into view mid-drag: the
pills are now pinned under the header (`--header-h`, measured), the next
page is drawn down by the current scroll with a transform, and the real
scroll to the top happens in the frame the ghost goes. And moving nodes let
Chrome's scroll anchoring re-aim the page by 40px, so `overflow-anchor` is
off for the length of a swipe. Positions are written once a frame, not per
touch event.

The photo viewer does the same with two side images holding the previews
either side, 16px apart; on commit the neighbor slides to the middle and
the main image, hidden, swaps to it once decoded.

**Still choppy on a phone, after the ghost content came in**, so four more.
On lift the slide starts at once, the ghost content standing in, where it
used to wait for the next page's data - a stall exactly as long as the
server took. Pressing the next pill waits a frame, so its work (clearing,
ghost content, the request) does not hold up the first frame of movement.
The shimmer pauses while a swipe moves (`html.swiping`), since a moving
background is a repaint every frame, and covers decode asynchronously.
Measured with the CPU slowed 4x and 250ms on every request: frames over
34ms per swipe went from 1-2 to none, the worst from 34ms to 19ms. The
emulator never showed the choppiness itself; a phone is the real check.

**Rough on an iPhone, fine on Android**, which two things explain. Safari
decides within a few pixels whether a drag is a scroll and then ignores
being told otherwise, so under pills the body is `touch-action: pan-y
pinch-zoom`: sideways drags are the app's from the first pixel (strips and
the pills are scroll containers of their own and still pan). And the ghost
was as tall as the list, which an iPhone rasterizes up front; it is now the
screen's size (`position: fixed`, clipped). That took the list's height out
of the page, so the page shrank, the scroll was pulled back and the next
page drew lower - the body's height is held for the length of the swipe.
No iPhone was available to check on; the emulator only confirms the layout.

**The lit pill sits in the middle of its row.** Spacers half the row wide at
each end let the first and last pills get there. A tap glides the row
(`centerPill`); a swipe carries it with the finger, the next pill reaching
the middle as the page arrives, and eases it the rest of the way (or back)
with the slide. During a swipe the pills are only drawn shifted, by a
transform, and the row really scrolls once, in the frame they are put back:
scrolling it every frame made the phone lay the row out every frame, which
is what brought the choppiness back. The row's buttons have no layers of
their own at all (neither made at the swipe nor by the stylesheet): on
Android a layer starting off the side of the row is drawn only once in view,
and the +, last in the row, popped in after the finger lifted. The + has no
transition on its position either, and transforms come off with transitions held off for a frame. And
`#subtabs` is no longer rebuilt
when only the lit pill changes - a rebuild mid-swipe snapped it back.

**And `#app { overflow-x: clip }`, or the tab bar jumps.** A page mid-swipe
hangs off the right of the screen, which made the page wider than the phone:
a real Android phone zoomed out and the bottom tab bar glitched, the same
failure as the album sort once was. Desktop emulation hid it in screenshots;
`visualViewport.width` narrower than `innerWidth` mid-swipe is what shows it.

## Music, phase 3: data saver, sleep timer, playlists, crossfade

- **Streaming quality is per device** (localStorage, Account > Playback on
  this device), because a phone on mobile data and the computer on the
  Wi-Fi want different things. `?kbps=` on `/api/stream` - only 96, 128,
  192, 256 or 320 are honored - becomes Navidrome's `maxBitRate` with
  `format=mp3` (its default, Opus, is patchy on iPhones) and
  `estimateContentLength=true`, which gives the converted stream a length and
  Range support: checked, a request from byte 100,000 answered 206. Downloads
  use `streamPath`, never `playPath`, so they keep the original.
- **A slow link falls back by itself.** Reported from the Android app away
  from home: songs sat at 0:00 for a minute or more. Measured through a
  playback report in a test build: the link from the house's upload to the
  phone ran at about 0.3 Mbps (256KB took 6.9s, 2.9s to the first byte), and
  every iTunes M4A carries ~600KB before its first note - ~200KB of index and
  art and ~350KB of padding - so 15-20 seconds of header alone, and the beat
  analysis was downloading a second copy of the song at the same moment. So a
  song that cannot play six seconds after starting is restarted at 128 kbps
  MP3 (a quarter of the data, no header to wait for), and so is every song for
  the rest of the session (`slowLink`, `startStream`), with a message saying
  why. And a song is only heard for the visualizer once it has 20 seconds
  buffered ahead (`playbackSettled`), never while it is still starting.
  Checked in Chrome throttled to ~40 kbps: the original was asked for at 1.8s,
  the 128 kbps copy at 7.8s, and no analysis download began meanwhile.
- **And the fallback alone was not enough: the pipe was full.** The same
  report showed the 128 kbps copy's first byte arriving 67 seconds after
  play, though the server converts a whole song in about 4s and was idle,
  and then the rest arriving in seconds. The server handed every song to the
  network in full the moment it was asked; over the slow link those
  megabytes queue in the connection (HTTP/2 lets a browser take several MB
  per stream before it pushes back; the router and Docker Desktop's port
  forwarding buffer more), and every request after them - the fallback, the
  next song after a skip, the covers - waits behind bytes nobody plays. So
  audio to a device that reached the server by an away-from-home name
  (`*.net.soundstorm.dev`, `*.ts.net`) is paced (`internal/stream/pace.go`):
  the first 1MB at once, enough to start any song, then 2.5 times its
  bitrate (the ?kbps= asked for, else 320 kbps compressed or CD rate for
  lossless). At home nothing is paced, nor are films (Jellyfin's HLS pieces
  are asked for one at a time). The Android test build's own timing probe
  had been fetching a second whole copy of each converted song, which made
  it worse, and came out.
- **Then known before the first song.** With pacing the fallback took 20s,
  not 67: starting at full quality sends the 1MB burst, which the link then
  has to drain before the quieter copy arrives. So on an away-from-home
  address the app times 128KB from `/api/probe` once a session as it opens
  (incompressible bytes, `no-store`); under 1.2 Mbps songs start at 128 kbps
  from the first one. A slow address is remembered on the device for six
  hours (`soundstorm.slowlink`), and the six-second fallback remembers it too.
  At home none of it runs. Checked in Chrome on a mapped `*.ts.net` name
  throttled to ~0.3 Mbps: slow on opening, the first song asked for at 128
  kbps.
  **And it goes back** (2026-10-01, the owner's asking): while slow, the link
  is timed again every fifteen minutes with the same 128KB, in the
  background, while the page is looked at; over 3 Mbps (ten times what full
  quality needs) the next song is at full quality and the slow note is
  forgotten. Nothing stalls to find out, and a playing song is never changed.
  Checked in Chrome with Playwright's clock: slow and 128 kbps at the start,
  still slow at ten minutes, full quality at sixteen - the fake clock times
  the probe too, so the threshold itself was not tried on a slow link.
  **And it says so, and notices sooner** (2026-10-07, the owner's asking: to
  know it is full quality once on the Wi-Fi, and why it sounds worse on a
  bad signal). As a song starts at a different quality than the last, a
  message: "Weak connection - playing at a lower quality for now" or "Good
  connection - back to full quality" (`noteStreamQuality`; only changes the
  connection made, never a quality chosen in Settings; a song fetched ahead
  counts as full, as nothing is fetched ahead on a slow link). The link is
  timed again four seconds after the phone changes network, comes back
  online or the app is looked at again, at most once a minute
  (`recheckLink`), not only every fifteen minutes. And a song at full
  quality stuck for eight seconds mid-way carries on at the lower quality
  from where it was (in `audioRecover`), rather than waiting out the twenty
  seconds for a retry. Checked in Chrome with the network slowed to 9KB/s:
  the weak message once as the song fell back, then with the network
  restored and an `online` event, the good message as the next song
  started. Not checked: the mid-song drop itself (the slowed song fell back
  at its start), and on a phone actually walking onto its Wi-Fi.
- **Why Chrome was instant and the app was not: caches and covers.** On a
  1.1 Mbps phone link (measured), Chrome had played the songs and covers
  before and kept them. The fresh app had nothing, and opening it asked for
  dozens of full-size covers (~150-200KB each, iTunes art) ahead of the
  song. So covers are now **card-sized unless asked** (`?size=`, default
  400px; `full` for the original, see `internal/stream/shrink.go`).
  Navidrome, Audiobookshelf and Jellyfin resize their own through
  `source.ArtSize`, and SoundStorm resizes a book's cover with the standard
  library. Only Now Playing's big cover and its swipe neighbours (800) and
  the lock screen (600) ask for more (`bigArt`). Card covers load at
  `fetchPriority = 'low'`, behind the song. Covers are kept a week by
  default, though Navidrome's own `public, immutable` wins.

  **Songs were kept too, for a day, and that was a mistake.** With audio
  cacheable, a song that ended by itself waited 20 seconds for the next.
  The preload was fetching the next song as the player asked for it, and
  Chromium lets one request at a time write a URL to its cache; the other
  waits, up to a 20 second timeout. The phone's two requests reached the
  server exactly 20.0s apart. Audio is `no-store` now (`audioNoStore`), and
  the preload fetches with `cache: 'no-store'`. Gapless keeps the next song
  in memory, and downloads live in the Cache API, so nothing needed the HTTP
  cache.
  **Always original** in Playback on this device is a promise: no probe,
  no fallback. **Original, lower on a slow connection** (`smart`) is the
  default.
- **And the real cause: the picture inside the song.** Even so, a song took
  15-40 seconds to start away from home. The phone's own report showed 39s
  from tap to sound, with 75 seconds of music downloaded before the first
  note. Measured in Chrome at 0.6 Mbps on a real iTunes M4A:
  - as it is: 34s;
  - the same song with the tags and artwork taken out of its header, the
    audio byte for byte the same: 2.5s;
  - a plain generated song: 1.7s.

  The browser's media engine treats an embedded picture as a second stream
  and reads about 2.3MB on before playing. An MP3 with a large picture took
  75s; FLAC was not affected. Caching made no difference either way, and at
  home those megabytes take a fraction of a second, which is why it never
  showed. The files are left exactly as they are, since the pictures are
  most songs' only cover and other players read them. Instead an original
  stream is sent **slim** (`internal/stream/slim.go`): a header built in
  memory, then the original's audio fetched from Navidrome by byte range.
  For an M4A the header is its `moov` without `udta`/`meta`, the padding
  after it dropped, and every stco/co64 offset shifted to match. For an MP3
  it starts at the first frame, the ID3 tags skipped. Any range of the slim
  file maps onto the original, so seeking works, and a layout is kept per
  song for an hour. Only when `moov` comes before the audio and the saving
  is over 16KB. Fragmented files, converted streams and anything that is
  not audio are sent as they are. On the real song all 397 chunk offsets
  point at the same bytes, 525KB smaller. Through the preview at 0.6 Mbps
  the generated M4A and MP3 started in 1.1s (were 28s and 75s), their full
  length intact, and a jump to 2:30 played in 1.7s.
- **Then skips waited on the pace.** With the picture gone, the phone's
  report showed the first song playing in 3.4s, but skips took 13.7s and
  30.7s. Each new song started within a second of its first byte; the wait
  was for that byte. The pace was 2.5 times a guessed 320 kbps, about 0.8
  Mbps, and the phone got about 0.6 Mbps. Every second played added to a
  backlog in the network that a skip had to drain: 13s after 7s of play,
  30s after 40s. Now the pace is 1.5 times the song's own bitrate, read
  from the file by the slim path: mvhd length for an M4A, the first frame
  for a constant-bitrate MP3, else a guess. It follows a burst of the header
  plus eight seconds (`paceFor`). Measured through the preview on an away
  name: 322KB, then about 0.33 Mbps, where it was 1.1MB then 0.77.
- **Loading shows.** A ring turns round the play button, in Now Playing (round
  the orb) and on the mini-player, while the song waits on the network. It
  appears on `waiting`, or on `play` before there is data. It clears on
  playing, pause, ended, error or emptied, and shows only after 350ms, so a
  quick start never flickers it.
- **Sleep timer** in Now Playing; "end of this song" stops in the `ended`
  handler instead of advancing - a chapter, for an audiobook. **It never stopped with the screen off**, which is when a sleep timer
  is used: the eight-second fade was stepped by animation frames, a phone
  with its screen off draws none, and the fade never got past its first
  step - reported as the timer not working. It is stepped by a timer now (a
  page playing sound keeps its timers); checked with animation frames
  switched off entirely, the music paused on time. **And in the phone apps
  the player keeps the timer itself** (2026-10-07, the owner's report: a
  half-hour timer ran on for hours with the phone's screen off, and stopped
  the moment it was woken). A phone sleeps a page whose screen is off,
  timers and all, while the app's own player plays on; so `setSleep` hands
  the moment to the app (`soundstormApp.sleepAt(ms since 1970, 0 for none)`),
  and Android's `NativeAudio` fades the music over eight seconds and pauses
  at it on its own (0.42), the page's timer still running while awake.
  "After this song" needed nothing: the songs after it are already taken back
  from the player. Built and published; not tried on a phone with its screen
  off. **For the Mac:** the iPhone app wants the same - `sleepAt` in
  `WebViewController.pageScript`'s `soundstormApp`, posting an "audio"
  message, and `NativeAudio` pausing (a fade if it can) at that moment by a
  timer of its own, which runs while background audio keeps the app awake;
  0 cancels. *(Done on the Mac, 2026-10-07: `soundstormApp.sleepAt` in the
  page script, `setSleep`/`sleepNow` in `NativeAudio` - an eight-second fade,
  then paused and the volume put back. Checked in the simulator: a timer four
  seconds away faded on time and the song paused eight seconds later. Not
  tried with a real phone's screen off.)* **Custom time** is a menu
  page of hours and minutes, typed or stepped by 5 and 15, remembered on the
  device; the countdown shows hours once there are any.
- **A fade is not the listener moving the volume.** The leveling code reads
  every volumechange as the listener's choice, so the sleep timer's fade left
  their volume at zero afterwards. `audio.fading` now guards it; crossfade
  uses the same flag.
- **Crossfade uses a second, hidden audio element for the fade-in only.**
  Everything else - lyrics, lock screen, queue, leveling - listens to the one
  `audio-player`, so when the song ends the main element takes the next song
  over from where the hidden one reached (`takeCrossfade`, usually a blob in
  memory) and the hidden one stops. Not within an album playing in order, on
  repeat-one, before a sleep-timer stop, or where volume cannot be set (the
  probe sets 0.5 and reads it back; an iPhone reads 1). Measured on a real
  Navidrome: the handover landed at 5.3s, at the song's own level.
- **Playlists open like albums**: rows with a drag handle (pointer events, so
  finger and mouse alike; arrow keys on the handle), remove with Undo (the
  add endpoint answers the new count, so the song goes back at count - 1 and
  is moved into place), rename in place, delete on a second tap.
- **Casting is not built yet**: a Chromecast fetches the media itself,
  without the session cookie, so it needs short-lived per-song URLs kept out
  of the access log - and nothing here has a cast device to prove it on.

## Artists and albums are the folders

Asked for by the owner: Navidrome groups by tags, and on a real 4,408-song
library that made **1,467 albums out of 551 album folders** - an album whose
tracks disagree about the year or the album artist splits, and "Artist feat.
Someone" becomes an artist. SoundStorm files every upload as Artist/Album/
track, so the folders are the tidy version, and the music browser now shows
them (`internal/source/subsonic/folders.go`): the first folder of a song's
path is the artist, the second the album, a disc folder below stays in its
album, and a song loose in an artist folder is one of their Singles. It is a
grouping of Navidrome's own song list, cached like the song shelf - nothing
reads the disk or keeps an index. Ids are `f:` plus the folder path; an old
Navidrome id still resolves through its tag-based album or artist.

The names are the folders', spelled as the tags spell them when they are the
same name with case and punctuation set aside: a folder cannot be "AC/DC" or
end in a dot, so `AC-DC` shows as AC/DC and `Fun` as Fun. A folder the tags
genuinely disagree with ("Tidewater" against "Tidewater (Deluxe)") keeps its
own. iTunes LP and Extras bundles (`.itlp`, `.ite`) are not artists.

An artist's picture is the cover of their newest album that has one. It
was Navidrome's artist image (`ar-<id>`), which without an internet agent
set up is a placeholder silhouette, so every artist looked the same.

**Navidrome's paths were made up, and that was a latent bug.** Unless told
otherwise it reports a path built from the tags - `Two Strings/Two Strings/01-06 -
The Long Game.m4a` for a file really called `06 The Long Game.m4a` - which
looks real exactly when tags and folders agree, the case every test library
had. Deleting a song used that path, so it could miss the file. Two things
fix it, both checked on the real install:

- `ND_SUBSONIC_DEFAULTREPORTREALPATH: "true"` in compose. Real paths are
  absolute in Navidrome's container (`/music/...`) and are made relative to
  `subsonic.Config.MediaRoot`; a path that is not real groups by tags, as
  before, and deletion refuses it rather than guessing.
- **The setting is read once per client, when Navidrome first meets it.** Its
  record for the old client name kept sending made-up paths after the
  setting was on; a new name got real ones straight away. So SoundStorm now
  introduces itself as `soundstorm-app`, which every install meets fresh.
  Navidrome's getIndexes/getMusicDirectory were no help: in 0.64 they answer
  with tag-based artists and albums under folder-shaped names.

## Radio

Asked for as "blow Plexamp out of the water". Plexamp's radio rests on
analysing every track's audio, the ML layer this project does not own, so
these stations rest on what is already here: the whole music shelf, this
person's plays and favorites, and - with music discovery on - which artists
people play together. What Plexamp does not do, and these do:

- **A tuner.** Build a station by hand: how familiar (never played ...
  favorites, a weight peaking at the chosen familiarity), which decades, which
  genres. Kept per device in localStorage.
- **Stations:** Library radio (everything, leaning to what you love), Deep
  cuts (unplayed songs by the artists you play most), Time travel (the
  library year by year, carrying on where the last batch stopped), Discovery
  radio (artists like your favorites that you rarely play; discovery only),
  and radio from a song, an album or an artist - the artist page's mix is now
  Artist radio. Song radio starts with the song.
- **Sequenced, not shuffled.** Weighted draw (Efraimidis-Spirakis), no artist
  more than their share of a batch, then ordered so no artist repeats within
  three songs and no album plays twice in a row. The share cap is what makes
  the spread possible: a draw where one artist has half the songs cannot be
  spread, whatever the order.
- **Endless.** `POST /api/music/radio` answers a batch of 25; the app asks for
  the next while five songs remain, sending the queued ids (up to 1,500) so
  nothing repeats. Once everything is excluded a station starts over rather
  than stopping. Starting any other queue ends the station.
- Songs played in the last six hours are drawn a tenth as often.

Similar artists come from the discovery cache only, except for an artist
station's own seed, which is looked up while the person waits. The catalog
(`GET`) draws a first taste of each station for its collage.

## Moods, and the backend that listens

Asked for as "we're still missing mood". Measured first, on the real library
(4,413 songs): 42% are tagged just "Pop", none carry a tempo or mood tag, and
MusicBrainz's tags name moods for famous albums only (OK Computer:
melancholic, lonely; Nevermind: grunge and nothing else). Mood from metadata
would have been a guess from "Pop". Hearing mood is machine learning over the
audio - the layer this project does not own - so it is a backend, the
Storyteller pattern: **AudioMuse-AI** (AGPL-3.0, run unmodified, three
containers: web, worker, Postgres), pinned by digest to 3.6.3 because it
releases weekly. The owner chose this over tags-only, knowing the cost.

Checked live against 3.6.3 before building, in a throwaway stack:

- **Provisioned with nobody logging in.** `/api/setup` answers without a
  login until the first save and never after (a second unauthenticated save
  is a 401; a made-up token is refused) - Jellyfin's startup wizard again.
  SoundStorm generates its API token and admin password, and gives it a
  **non-admin Navidrome account of its own** (`audiomuse`, made through
  Navidrome's native `/api/user`; it sees every song and is refused a scan),
  reset rather than refused if AudioMuse's volumes were wiped and Navidrome's
  were not. It reads songs through Navidrome, so it mounts no folder.
- **Nothing leaves the house**: its models ship in the image (1.9GB, nothing
  downloaded), and its third-party lyrics lookup is switched off at setup.
  Lyrics transcription is off too - hours of CPU for nothing shown.
- **Song ids are Navidrome's own**, so `/api/sync` (the whole analysis,
  500 a page, `include_embeddings=false`) joins to SoundStorm's songs with no
  matching. Six classifiers (danceable, aggressive, happy, party, relaxed,
  sad), energy, tempo, key, top style tags.
- **The classifiers answer in a narrow band** - a solo piano Aria scored 0.58
  to 0.63 on all six - so the raw numbers mean little and their order means a
  lot. Every feature becomes its rank in *this* library (ties share one), and
  a mood is an average of ranks (`moods.go`): Chill is relaxed, calm, not
  aggressive; Focus leans on the instrumental tag. A station takes songs
  scoring over 0.6, weighted by how far over.
- **Timing, measured:** about 9 seconds a song on the development machine,
  so the real library's first listen is roughly 11 hours in the background;
  worker memory peaked around 700MB. A nightly listen is scheduled through its
  `/api/cron`, and SoundStorm's "look for new files" hook starts one three
  minutes after a music scan (debounced), once Navidrome has indexed the
  arrivals.

What it gives Radio: a **Moods** row (Chill, Feel good, Melancholy, Intense,
Party, Focus), mood chips and an **Energy** slider in the station builder
(off until moved), and song, album and artist radio built from what
**sounds like** the seed (`/api/similar_tracks`, a few seeds interleaved),
falling back to similar artists and genres when a song has not been heard
yet. The page says how far the listening has got while it is under way.
Registered as a music source that finds nothing, so access applies.

Verified end to end on the preview: the preview SoundStorm provisioned a
fresh AudioMuse-AI on its first attempt, the 14 test songs were heard, all six
mood stations appeared, and song radio came back "Songs that sound like it".
Its song-path (`/api/find_path`) and text-to-sound search (`/api/clap/search`,
"rainy day piano") are there for later.

## Scrobbling, and the year in music

**Every play is now logged with its moment** (`collections/listens.go`):
one append-only file per person per year, a line per play carrying what the
song was, so recording costs one small write and a year is one file. History
only ever kept a count per song, which says what but not when. A damaged
last line (a crash mid-write) is skipped. Removing a person removes their
listens. Not in `soundstorm backup` (which carries the collections files);
a move carries the whole state folder, these included.

**Scrobbling is ListenBrainz, per person**, because it needs no app key:
each person pastes their own user token (Settings > Scrobbling), checked
with `validate-token` (which answers 200 `valid:false` for a bad one -
checked live). Plays are sent from the server, so every device counts and
the token never reaches a browser; the API never returns it. A play is
queued in the person's collections file as `RecordPlay` records it, sent in
the background, and taken off by song and moment once sent, so plays queued
meanwhile survive; what fails waits (1,000 at most) and is retried after the
next play and every quarter hour. A 401 stops sending and says "connect
again", keeping the queue for the reconnect. "single" for one listen,
"import" for a batch, a thousand a request; songs without an artist are
never sent. The player also sends "playing now". **Last.fm is not built**:
it needs an API key and secret registered to the project, which the owner
has to create.

**The year in music** (`GET /api/recap`) is a story of slides from Mixes'
banner, tapped through (left third back), for this year so far, any year
with listens, or all time. It is minutes, plays, top artists, songs (25
listed, five shown, all playable) and albums, genres, a month chart,
the busiest weekday, a listener kind from the peak hour, the longest streak,
artists new that year (against earlier listens and History's first plays)
and the first song. With the sound analysis, it adds how the year sounded:
the share of plays whose strongest mood was each. The app sends its time zone
offset (`tz`, minutes east), since the server runs in UTC and a 10pm play
belongs to its own evening; a local year reads the UTC files either side.
All time comes from History's counts, so it says something from the first
day; the year starts counting the day this shipped, and says so.

**A long audiobook in one file plays in pieces** (`internal/mp4hls`,
`httpapi/bookhls.go`, 2026-10-03). Reported as read-along books taking for
ever to start; measured from the server's log, away from home: the page was
ready in a second, and the sound took 33 seconds - the player fetching the
book's index before a word. Every Audible .m4b keeps its index (moov) at the
end, and a long one's is big: 33.5MB for the 52.7-hour Long Voyage Home,
3-6MB for the others. So a file whose index is 2MB or more is offered as HLS
made from the file itself: the playback answer says `mode: hls` with
`/api/bookhls/<source>/<item>/<track>/index.m3u8` (a multi-file book, each
such track's `url`, the file's own kept as `file`), and the server reads the
index (0.1s on the 52-hour book) and serves an init segment and ten-second
fragments - a moof of the samples' sizes and durations and an mdat of the
original bytes, byte for byte: a remux like slim.go, nothing transcoded.
Only the sound track is kept (the chapter text track and cover are not
needed), the edit list dropped (AAC priming, ~50ms). The playlist (0.5MB for
the 52-hour book) is sent gzipped. Three books are kept open, their index in
memory. Downloads keep the file (`t.file || t.url`), and a downloaded book
plays its file. The page plays the pieces with the browser's own HLS where
there is one, else hls.js (`setAudioSource`); Android 0.35 adds Media3's HLS
module for its native player. Checked: ffmpeg read the pieces of a generated
book with exactly the original's audio (identical ADTS); the routes through
`Routes()` (`TestALongBookPlaysInPieces`); and Chrome playing the real
52-hour book from the pieces - sound in 0.7s (was 33s), a jump to hour 50 in
0.6s, with Chrome's own HLS and with hls.js. Not checked: the Android app's
and the Apple apps' players on a real book. **For the Mac:** the iPhone's
native player and the Apple TV are handed the playlist as the book's url (a
multi-file book's tracks too); AVPlayer plays HLS, but check the cookie goes
with every fragment (`AVURLAssetHTTPCookiesKey`) and that a book's clock and
seeking behave as with a file. *(Checked on the Mac, 2026-10-03: AVPlayer, the
engine both apps use, played `mp4hls`'s pieces of a generated 5-minute m4b
(index at the end) through a server refusing anything without the cookie -
ready in under a second, 300s long, a jump to 250s playing on, the cookie on
every request. Nothing needed changing in either app. Not tried: a real
Audible book through the real server.)*

**Playback recovers from a dropped connection** (`audioRecover`, 2026-10-04,
the owner: an audiobook stopped, loaded a while, and would not play until the
book was closed and opened again - it was a deploy restarting the server
mid-book, and the same happens on any drop). A failure (the element's
`error`, which the Android and iPhone apps' native players also fire, or a
fatal hls.js error) or loading past twenty seconds starts the same file again
from the moment reached (`startAt` with the last good time), after 2s, 4s,
8s ... at most 30s apart, for about five minutes, and at once on the
browser's `online`. The address gets `retry=<n>` so a native player loads it
afresh. A message at the second try, "Playing again" once it does; after it
gives up, play tries again. Checked with a song whose file was refused for a
while: two refused tries, then on from 3.9s.

**A resumed book started from the beginning** (2026-10-04, the owner: a
read-along sent to the projector, the sound back at the start). The jump to
the saved place (`startAt`'s `pendingSeek`, on `loadedmetadata`) had two
holes: it capped the place at the length less a second even when the length
was not known yet (0:00 then), and it listened only after handing the file
over - the Android app's player says it has a file at once when it is
already in it, so that jump never came. Now it listens first and caps only
by a known length, and a book that should carry on more than a minute in,
found playing in its first 15 seconds within 30 seconds of starting, is
moved there again (twice at most; `audio.resumeTo`) - otherwise the regular
saves write the start back as its place and the restart sticks. Not on the
phone while it controls a TV (the TV's own page does it). Checked in Chrome:
starts at 25s and then at 10s in the same file landed there. **Not checked:
the projector itself**, nor which hole it fell into - its player keeps no
system log; the owner's next read-along on it is the test.

## An audiobook's place, wherever it was listened to (2026-10-09)

The owner's report: a book sometimes started over, or from an older spot,
moving between devices. The place is Audiobookshelf's, per person (see "The
Continue row"), saved by the page every ten seconds, on pause, on close and
at the end; every way into a book resumes from it. Three fixes:

- **The phone apps save it themselves.** In the Android and iPhone apps the
  app's own player plays a book with the screen off, while the phone sleeps
  the page - and the page did the saving, so an hour listened with the
  screen off was an hour back on the next device. The page now tells the app
  where the book's place is saved and where each of its files begins on the
  book's timeline (`tellNativePlace`, `soundstormApp.place`; null for a song,
  a downloaded book or a book on a TV), and Android's `NativeAudio` sends it
  every ten seconds while playing and once whenever it stops, ends, is
  stopped or the player closes (`savePlace`; `finished` at the end of the
  last file). Android 0.54. **For the Mac:** the iPhone's `NativeAudio`
  wants the same - take `soundstormApp.place` in `pageScript` (an "audio"
  message, cmd `place`, with `path`, `duration` and `files` of `url` and
  `offset`), match the playing item's URL path to a file, and PUT
  `{seconds, duration, finished}` to the server's `path` with the session
  cookies every ten seconds while playing and on pause, end and stop; it
  runs while background audio keeps the app awake.
- **The newest listening wins.** A downloaded book listened to offline kept
  its place on the device as "not sent", and that always won on the next
  open - even when the book had been listened to further elsewhere since,
  which pulled the place back. The place now carries when Audiobookshelf
  last recorded it (`source.Position.UpdatedAt`, its `lastUpdate`), and the
  device's own is used, and sent, only when it is newer; otherwise the
  server's is taken and the device's dropped.
- **Ebooks had the same old-copy-wins:** the reader keeps a place on every
  device for every book, and one that never reached the server always won
  there; now only when newer than the server's (`loadProgress` in reader.js,
  against `/api/book/progress`'s `updatedAt`). Films and episodes keep no
  device copy (the server's alone) and stop with the screen, so neither
  problem applies. **And a film's place is kept on the device too** (the
  owner's asking, the same day: a downloaded film watched offline lost it):
  `keepLocalWatch` beside every save, one that never reached the server sent
  on the `online` event (`sendUnsentWatches`, unless the server's is newer)
  and used on opening only when newer than the server's. Checked on the test
  server (`scripts/smoke/offlinewatch.js`): watched to 0:31 offline, kept on
  the device unsent, on the server as 0:31 once back online.
- **Play from the beginning** in an audiobook's hold menu: the one way into a
  book that does not resume (`audio.fromStart`).

Checked: the adapter reads `lastUpdate` (`TestPositionIsRead`), the page
loads with no error (the after-deploy check), the app builds. Not tried:
playing a book with the phone's screen off and opening it on another device,
and an offline download coming back online - the test server has no
audiobook server, and the owner's books are theirs to try it on.

## Read Along keeps the screen on

While a book follows its audiobook, nobody touches the phone, so it used to
sleep mid-chapter. The reader holds a screen wake lock
(`navigator.wakeLock`) from the moment it starts following until it stops,
asking again when the page becomes visible, since the browser drops the
lock whenever the page is hidden. It needs a secure context: over plain
http, or in a browser without Wake Lock, the screen sleeps as before.

**Keep the screen on** is a setting in Playback on this device: off, while
Now Playing is open (to watch a visualizer, or a phone on a stand), or always
while the app is on screen. Per device, in localStorage, like the rest of that
card. It holds a screen wake lock while the choice applies, asks again when
the page comes back (the browser drops the lock whenever it is hidden), and
lets go the moment it no longer applies - Now Playing is watched for class
changes. Only on a secure address, where the card says so otherwise; the
reader's read-along lock is separate. Checked in the preview: held with Now
Playing open, released on closing it, held everywhere in "always", released
when turned off.

## Music, phase 2: mixes, lyrics, downloads

- **Listening history is SoundStorm's, per person**, in the collections file:
  count, first and last play, capped at 5,000 songs. A play counts at half
  the song or four minutes. It feeds Most played, Recently played and
  Rediscover. Library mixes use `source.MixSource` (getRandomSongs,
  getGenres): shuffle, recently added, genres with 5+ songs, decades. Artist
  mix is the artist's songs with genre-mates woven in, since there is no
  "sounds like" without audio analysis.
- **Lyrics** come through OpenSubsonic `songLyrics` (getLyricsBySongId),
  checked on Navidrome 0.64.1 with a `.lrc` beside a song: synced, starts in
  ms. Hover styles only apply with `(hover: hover)`: a tap leaves `:hover`
  stuck and grayed out the current line.
- **Lyrics from LRCLIB** (`internal/lyrics`) fill in songs that have none, and
  are the first thing SoundStorm sends to an outside service about what
  somebody plays - so it is an owner setting, **off by default**, in
  `state.json` as `onlineLyrics`. LRCLIB because it needs no key and no
  account and has synced lyrics; Musixmatch needs a paid license to show whole
  lyrics and Genius's API has none. Local lyrics always win. It is asked only
  on play, never in bulk (LRCGET exists for that), and matched on artist,
  title, album and **duration** - duration is what picks the studio take over a
  live one with different timings. Every answer, "none" included, is cached as
  a file under the state dir's `lyrics/` for good (none re-asks after 30 days);
  never in the music folders, which are the user's and scanned. Checked live:
  a real song came back synced, 56 lines.
- **Music discovery** (`internal/discover`) is the second thing that sends
  anything out of the house, so it is an owner setting, off by default,
  like online lyrics (`onlineDiscovery`). MusicBrainz names the artist (a
  confident match only: another artist's bio is worse than none), ListenBrainz
  gives the artists people play in the same sessions, and Wikipedia - found
  through MusicBrainz's Wikidata link - the bio. No key or account for any of
  them, which is why not Last.fm. MusicBrainz's one request a second and
  User-Agent are kept to; every answer, none included, is kept on disk
  under the state dir for months. Only artists in the library are ever
  offered. It gives an artist's page an About and Similar artists in your
  library, the artist mix artists like them (genres only without it), and
  "More like" mixes around the three most played artists - from what is
  already known, the rest looked up in the background, so the mixes page
  never waits on the network. Checked live: Radiohead came back with 50
  similar artists and its Wikipedia summary in 2.5s, then 54us from disk.
- **Downloads** live in the Cache API (`soundstorm-offline-v1`), with an index
  in localStorage. Downloaded songs always play from the device. Sign-out
  clears them.
- **Books download too**: audiobooks, ebooks, documents, and Read Along
  pairs (both halves, plus a synced book's text - without its copy of the
  audio - and its timeline). All in the same cache as songs, keyed by the
  address the app would ask the server for, so one index and one Remove
  serve everything. The reader falls back to that cache when a fetch fails;
  a downloaded audiobook always plays from the device via a map from its
  kept file addresses to blob URLs (`audio.urlMap`, applied in `startAt`).
  Listening and reading positions are also kept on the device, marked
  unsynced until a save reaches the server, and an unsynced one wins on the
  next open. The reader is several modules, so the worker serves any
  `/static/` file network-first and the kept shell lists foliate's modules;
  without that the first offline test found `soundstormReader` undefined.
  Films, episodes and photos followed, at the owner's asking despite the
  size. A film the browser can play is kept as it is; one it cannot is kept
  as the HLS stream the app would play - the variant playlist and every
  piece, plus hls.min.js - and played offline through hls.js from a
  playlist rewritten to blob: addresses (the browser's own HLS will not
  follow those). Only playlists go in the index; a removal reads the kept
  playlist to find the pieces, or the index would outgrow localStorage.
  The playlist is cached last, so a stopped download is not taken for a
  whole one. A series card has no file and no episode list here, so
  episodes download one by one or by the TV shelf's Download all. Photos
  keep the preview the viewer shows and the thumbnail. Tested on a
  throwaway Jellyfin with a generated MP4 (direct) and MKV (HLS): both
  downloaded, both played offline from blobs, and removing the MKV took its
  pieces. Tested fully offline on the
  mapped test name: a downloaded ebook opened at its place and a downloaded
  track played from a blob.
- **Offline, the app stays itself and shows only downloads.** Opened offline
  or losing the connection while open (the `offline` event, or a search
  answering offline), it keeps its tabs; each shelf lists what is on the
  device (Home and Music as the Downloads list, the rest as cards with their
  covers from the cache), search looks through that, and Settings and the
  list buttons step aside. The `online` event brings it back (a reload if it
  was opened offline and never signed in). The separate offline screen is
  gone, and offline looks like online: Home is the same strips, one per
  downloaded kind, and Music keeps its pills - Songs, Albums (downloaded
  albums and artists) and Playlists (playlists, favorites, shelves) - with
  a group opening as an album page. Music's Downloads pill went; managing
  downloads is a card in Settings. Everything downloaded wears a small badge
  on its cover (and beside a song in an album's list), repainted by
  `markDownloads` whenever downloads change. The badge is a plain green
  tick, the one mark still drawn on a cover. Downloading is from the hold
  menu (an album's too, see below); a song in an album's list keeps its
  gray arrow. Hidden offline and where there is no Cache API.
  **Download all** for a whole shelf (all songs, favorites, each book,
  film and photo shelf) lives in Settings' Downloads card, one button per
  shelf the account has: a button on every shelf page got in the way. An
  artist page keeps its own, as an album keeps Download. Cards no longer
  carry a kind label ("audiobook", "music"): each shelf is its own page.
  **Remove download** in the menu takes an item off the device whichever
  download brought it - alone, in an album or playlist (it leaves the
  group), or half of a Read Along book (the pair goes whole). And downloads come first even online: the reader reads a downloaded
  book from the cache before asking the server, as songs, audiobooks, films
  and photos already did.
- **Opening offline bends sw.js rule 3, on one condition only.** A page load
  may be answered from `soundstorm-offline-shell-v1` only on
  `*.soundstorm.dev` names, only when the network failed, and only once
  something was downloaded. Rule 3 exists for self-signed certificates changing
  under a cached page, and a trusted, self-renewing certificate cannot do
  that. IP addresses, localhost and self-signed keep rule 3 exactly. The
  guard test now asserts that shape. Offline mode asks for no password: the
  files are already on the device. The shell used to be kept only once something
  was downloaded, so opening the installed app with no connection and no
  downloads was Chrome's bare ERR_FAILED (the worker answered
  `Response.error()`), reported from a real phone. Now it is kept every time
  the app loads on a soundstorm.dev name - each address is its own origin
  with its own copy - and the boot screen says "You are offline" when the
  device is, and what works without a connection. Tested on a mapped
  `test.soundstorm.dev` over self-signed TLS (Chrome forces HTTPS on all of
  .dev, so plain http cannot be used for this): signed in, no downloads,
  network off, reopened - the app's own offline screen.

**A server that answers nothing no longer leaves a black screen**
(2026-10-09, the owner's report: the server's cable out, the Android app went
from its opening screen to black and never opened). A server switched off
says no at once; one unplugged - or a home address seen from elsewhere -
often says nothing, and every request waited minutes: the service worker's
page load before falling back to the kept copy, the page's own files, and
the page's first `/api/session`. Now, with a kept copy to fall back on, an
answer not begun within six seconds counts as none (`fetchOrGiveUp` in
sw.js; only the start of the answer is timed, so a slow link is unaffected,
and with nothing kept a request waits as before), the next half minute's take
their kept copies at once (`silentUntil`), and the page's first session check
gives up after five seconds when there are downloads (`boot`, after `await
null` - the downloads are read further down the file). Checked with
`scripts/smoke/offlineopen.js`: a TLS stand-in at test.home.emberstorm.app
in front of the test server, signed in, a song downloaded, then every
connection taken and never answered - the app opened on the downloaded song
in 11 seconds (20 before `silentUntil`). Not tried on a phone; the iPhone app
has no service worker (WKWebView), so offline there is still its own
"can't reach" screen.

## Favorites and playlists

Per person, and kept by SoundStorm (`internal/collections`) rather than by
Navidrome, which has both built in. The house shares one Navidrome account, so
they would be everybody's at once: the same reasoning as film positions, and
the "revisit when favorites reach the UI" moment from "Accounts".

**A file per person, not state.json.** That file is rewritten whole on every
sign-in and session change, under the lock every request takes, and a
household's playlists in it would slow all of those. Here one change writes
one small file, to a temporary name and renamed over. Limits: 5,000
favorites, 200 playlists, 5,000 songs in each, names up to 100 characters.
Removing a person removes their file.

Each entry is a snapshot of the item as its backend described it
(`source.ItemGetter`, now on every adapter). It is never what the browser
sent, and it means a list of 500 songs is one file read, not 500 backend
calls. Playing still goes by id, so a deleted song fails to play rather than
playing something else. Lists are filtered through the registry each time they
are shown, so a favorite on a shelf an account has since lost is hidden. It
is not a way back in.

**Nothing over a cover but the downloaded tick.** For a while every cover
carried buttons - a heart, info, add to queue, play next, download - and
then they went, at the owner's asking: they cluttered the art, and every
one of them was already in the menu a hold opens (right-click, or the
desktop's hover "..."). An **album card** has a hold menu of its own for
the same reason - play, shuffle, album radio, download - since its
download arrow went too. Covers also lost their length, the pages their
counts ("25 songs") and Albums its sort (A to Z; New music's See all still
lists newest first): the length is under **Info** in the hold menu, with
everything else the backend said, and a menu page that grows is placed
again so none of it is off screen.

**A playlist holds each song once, and shows its songs A to Z unless told
otherwise.** Adding one already there is a 409 ("already in this
playlist"); adding several skips those and says how many. Each playlist
has its own **Sort** (on its page, beside Delete): A to Z (the default,
stored as nothing), Artist, Recently added, or Custom order - the order the
songs were put in and dragged to, which `Items` always keeps whatever the
sort, so choosing Custom again brings a hand-made order back. The server
returns songs in the sorted order (`Playlist.Ordered`), each with its
position in `Items` for removing and moving, and they play in that order.
Drag handles only show in Custom order. **A playlist's songs have no x any more**
(2026-10-04, the owner's asking): a hold on one opens its menu - **Remove
from playlist** (Undo in the message), **Select** and the rest - and while
selecting a tap ticks more and the selection's menu has Remove from playlist
too (several at once, Undo putting each back in its place). The rows are
`.item`s in a `.selectable` list, so they hold and select as cards do
(`state.openPlaylist`, `removeFromOpenPlaylist`). **Add to playlist adds to several at
once** (2026-10-06, the owner's asking): the playlists are a list to tick,
as many as wanted, then one "Add to 3 playlists" (`renderPlaylistPicker`, for
one song and for a selection alike); for one song, a playlist it is already
in says "Already in it" and is not offered (`GET /api/playlists?has=`), and a
playlist made there is ticked. Checked: a song in Chill ticked into Road trip
and Gym, added to both with one tap. The **Playlists page** is cards,
A to Z, like the mixes: a collage of up to four covers, name and count,
"New playlist" first; tapping opens one, a hold offers Play, Shuffle and
Delete. (A shuffle button sat on each cover, as a mix card has its play
button, and went at the owner's asking, 2026-10-05; the playlist's own page
keeps Play and Shuffle.) It was a plain list of names with Play buttons. New playlist
and Import are two small buttons above the cards: they were cards of their
own, the size of a playlist, at the start of the grid, and got in the way.

**A playlist can have a picture of its own**, in place of the collage of its
songs: Change picture in its hold menu, or holding the cover on its page. It
is kept like a song's own cover (`collections/art.go`), under the key
`playlist:<id>`. That key is accepted only for a playlist of the person's own,
and deleting the playlist deletes the picture. Use the song covers puts the
collage back.

**Playlists come in from other players as M3U** - asked for as bringing
somebody's Plexamp playlists across. Plexamp keeps them on the Plex server,
which has no export button, so any M3U will do: Plex's API or a tool writes
one, as do iTunes, Jellyfin and most players. **Import playlist** on the
Playlists page takes one or more files (`POST /api/playlists/import`, the
text in the body; the app decodes UTF-8 strictly, else Windows-1252, for old
iTunes files). Each song is found on the music shelf by the tail of its path
first - the last three, two or one segments, so the same files moved under
another root match - through `source.SongFileLister`, which the folder view's
cached song list already answers with real paths; then by artist and title
from `#EXTINF` (or read off an `Artist/Album/01 Title` path), case,
punctuation, a leading "The" and a trailing "(Remastered)" set aside, the
length choosing between versions. A file name alone, or a title alone, must
agree on length when one is given. What is not found is named back to the
person, never guessed. A file becomes one playlist in one write, each song
once, in the file's order (Custom sort). Checked in the preview with a
Plex-shaped file: four of five found - by path, by tags with a Windows path,
through "(Remastered)", by a relative path - and the fifth reported.

**Or straight from Plex, because an M3U is out of reach on a phone.**
Plexamp has no export, so the file route meant a computer and a script.
**Import playlist** now offers "From Plex or Plexamp" (`internal/plex`):
SoundStorm asks plex.tv for a PIN, the person signs in on Plex's own page in
a new tab (opened blank in the tap, so no popup blocker, and pointed at Plex
once the PIN exists; Plex sends it on to `/static/plex-done.html`, "go back
to SoundStorm"), and the app polls `/api/plex/status` until plex.tv hands
over the account token. With it the server lists the account's servers
(`/api/v2/resources`), finds an address one answers on - home network
first, as plain http to its IP too (routers that refuse plex.direct names),
Plex's relay last - and reads its audio playlists and their songs, which go
through the M3U import's matching with the track artist and the album
artist both tried. The token lives in memory per person for 30 minutes of
use and is never sent to the browser or written down; nothing talks to
plex.tv unless somebody taps. A server's addresses are plex.tv's say-so,
so only `*.plex.direct` names and IPs that are not loopback, link-local or
unspecified are dialed, without redirects - never the compose network's
names. Plex lists every app signed in to an account, so the client id is
fixed per install, an HMAC of the state's device key. Tested against a
stand-in plex.tv and Plex server through the real routes (and the screens
in Chrome with stand-in answers); **not yet run against a real Plex
account**, which needs one with a server and playlists.

**Favorites are for anything; playlists are songs only.** A playlist plays in
the audio dock as a queue, advancing on `ended`, with back and forward. A
playlist of films has no player to play it in.

The UI adds a "⋯" button beside each card. The card is itself a `<button>`, so
the two sit in an `.item-holder` wrapper, since a button cannot contain
another. There is one shared menu element, moved to whichever card asked.

**They are in `soundstorm backup`,** as one more field, `collections`, in the
same file, keyed by account id. A field rather than a wrapper, so the backup
still is a state file:
- An older SoundStorm refuses anything without the state's version field. It
  restores the accounts from a new backup and ignores the lists.
- A backup from before the field restores exactly as it did, and leaves
  current lists alone.
- With no lists at all the backup is the state file byte for byte, which the
  original stdout test still asserts.

Restore checks the lists before touching the state, so a damaged backup
changes nothing. It strips the field from the state it writes, and gives the
list files the same owner as the state file, for the same reason the state
needs it.

Verified in Chrome against a throwaway stack with a real Navidrome that
SoundStorm provisioned: favorite, the heart, the Favorites chip, a playlist
built from the menu, Play all, next, and advancing when a song ends.

## The free-space warning, and what a card does when pressed

**Free space** is read from the filesystem the library is on (`statfs`). On
Docker Desktop for Windows that is the real drive, not Docker's disk: `df` in
a container reported the same 826 GB free of 931 GB that Windows did for the
bind-mounted folder. `/api/library` carries it, and a line under the drop line
says so:
- **Low** is under 25 GB *and* under a fifth of the disk. The fraction keeps a
  small card that is simply small from warning for ever.
- **Critical** is under 5 GB, whatever the disk. Uploads stop at 1 GB, so
  "large files won't fit" is the honest wording, not "nothing can be added".

**The square flash on a tap was Chrome's tap highlight.** The cards are
`<button>`s, and the highlight covers a button's whole box as a rectangle,
however rounded the cover inside it is. It cannot be screenshotted, since the
compositor draws it, so it is switched off by name
(`-webkit-tap-highlight-color`) on the cards, the "⋯", the chips and the menu.
Each gets its own press feedback: the cover settles to 97%. The hover
brightening every button gets is off on cards too; a filter on a rounded,
clipped element is one more way to get a square repaint.

**Icons are SVG, never characters.** The "⋯" (U+22EF) rendered as three
dashes in the UI font, and a heart glyph sits on a different baseline in every
font that has one. The menu has a header naming the item, 40px rows (48 on a
touch screen), a filled pink heart for a favorite, and a second page for
playlists with a back arrow.

**Then the button went, on a touch screen: the menu is a press and hold.**
The "⋯" cluttered every cover on a phone, where nothing hovers. A hold of
450ms opens the menu beside the card, which lifts above a dimmed page.
Moving more than 10px cancels it (that is a scroll), and so does lifting
early (that is a tap). Cards turn off text selection and iOS's callout, or
the browser takes the hold for itself.

The finger lifting at the end of a hold is still a click. The card swallows
it and stops it propagating, because the page's "click outside closes the
menu" rule would otherwise close the menu the hold had just opened. That was
the first version's bug, and the emulated-touch check caught it.

A mouse cannot usefully hold, so right-click (`contextmenu`) opens the same
menu and the "⋯" stays, but only on hover and only for a fine pointer. The
menu key and Shift+F10 open it from a keyboard. A hidden gesture has to be
told once: a tip on the first visit from a touch screen, and the empty
Favorites and Playlists screens say "Hold down on" or "Right-click" to
match the device.

## Deleting, into a bin

The owner can delete items, from an item's hold (or right-click) menu:
**Delete from library** asks the server for the preview and says it ("1
file, 321 KB. It stays in the bin for 30 days"), then deletes, with Undo in
the message at the bottom. It was a Select button and a bar first. The
menu is redrawn in the click that opens the confirmation, so that click is
stopped there - otherwise it reaches the page from a button no longer in
the menu, reads as a click outside, and closes it. Two decisions shape it:

- **Owner only.** Every other account shares these shelves with the rest of
  the house, so a member who could delete could empty one everybody uses. The
  routes are on the owner mux. The Select button is not shown to a member, but
  that is presentation; the server refuses either way.
- **Never at once.** Files move to `library/.trash/<entry>/files/<path>`, with
  an `entry.json` saying what they were, for `library.BinKeep` (30 days). An
  undo puts them back, and a daily sweep in `main` empties old entries. A
  top-level dot-directory for the same reason as `.uploads`: no backend has it
  mounted, so a binned item leaves every backend's view at once. Undo never
  overwrites: a path whose place has been taken since stays in the bin and is
  reported.

**Press, slide, release.** A menu opened by a hold (a card, an album, Now
Playing's cover) is used without lifting the finger: slide over it and the
option under the finger lights; lift on one and it is chosen; slide off and
nothing is lit, and lifting there closes the menu. Lifting without having
moved leaves the menu open to tap. Nothing lights until the finger moves -
`:hover` and `:active` only apply with a real pointer now, since on a touch
screen they stuck to whatever the finger rested on when the menu appeared.
The browser's own click for the lift is swallowed. An option that redraws
the menu in place (Shuffle, Repeat, a second page) used to close it: its
button is gone from the page by the time the click reaches the page's
"click outside" rule, which now ignores a click on a removed element.

**In a shelf's list, a hold selects - the owner's design.** Holding a card
selects it (no menu); keeping the finger down and sliding onto other cards
selects them too, and after lifting, a tap adds or takes off one. Holding
any selected card opens the menu for the selection: one alone gets its full
menu (info, delete and all - closing it ends selecting), several get what
can be done to many. That menu is a hold away rather than a sheet at the
foot of the screen, which covered the cards still to be picked ("hard to
select more"). Nothing shows at the foot of the screen - a slim "N
selected / Done" strip lived for a day and went at the owner's asking; the
ticks say what is selected. Taking off the last one, an action in the menu,
its Clear selection, Escape or Back leave selecting. Where nothing is
selected from - Home's rows, album cards, Now Playing's cover - a hold opens
the menu as before. A mouse's right-click (and the hover "...") still opens
a card's menu at once; holding the mouse button selects. A Select option in
the menu lived for one commit and went: holding is the way in.
**Then reversed, at the owner's asking (2026-10-04): a hold opens the menu,
and Select in it starts selecting** with that item ticked; from then taps tick
more, a hold on a ticked one opens the selection's menu, and a hold on an
unticked one ticks it and lets the finger drag across more, as before. The
same in a shelf's list and the photo timeline.

**Many at once: hold, then drag.** Moving the finger on to *another card*
without lifting selects every item from the held one to the one under the
finger (selecting starts on reaching another card, looked for through any
dimmed backdrop; a finger that has been on a menu is choosing from it for
the rest of that touch), in the order shown, as a phone's
photos do (back up and they come off; what was selected before stays).
The bottom 120px of the screen scrolls the page down, and the band under
the pills up, faster nearer the edge, so a drag reaches any number. The
selection bar waits for the lift - over the list it hid the very cards the
finger was heading for, and `elementFromPoint` found the bar. Dragging up
works the same way: a finger over the pills or the tab bar counts as the
item at that edge of the list, so the selection follows the scroll. The
selection gets the item menu itself, fixed at the bottom, with only what
applies to many: play next, add to queue and add to playlist when all are
songs, favorites, download or remove downloads, select all shown, and
Delete (with its preview, and Undo in the toast) for the owner. Only in a
shelf's list; the page swipe stands down while a drag selects.

**A hold owns the finger until it lifts.** From the moment the hold
registers, every touchmove is prevented, the pill swipe stands down, and
`html.holding` sets `overscroll-behavior: none` against pull-to-refresh -
reported as selections refreshing the page or switching category.

**A page being left shows nothing while the next one loads.** Switching
tab, pill or page swaps the old content at once for ghost content - gray
covers and title lines with a slow shimmer (`showSkeleton`: a grid, Home's
rows, or a page with its heading; still under reduced motion). The last page
sitting there read as the new one, and "Loading..." read as stuck. A page
that fails to load clears its ghost and says so. Typing on the same page
keeps the results until the new ones replace them (`state.shownPage` tells
the two apart; detail pages clear through `startLoading`). The pill swipe's
wait for the next page does not count a page still showing ghost content
as drawn.

**A page is only itself.** Opening an album, artist, playlist, author or
series (`enterDetailPage`, from `startLoading`) hides the Continue row and
Home, and closes a menu and any selection; a Continue refresh still on its
way checks again before showing. Reported as an album opened from Home's
New music with Continue still above it - that card switches the page by
hand. Changing page closes menus and selections too, and a search belongs
to its tab: another tab starts with an empty box, though a tab's own pills
keep it (dune across audiobooks and ebooks).

**Home runs in three groups**: Continue and Recently played, then every New
row (music first), then the favorites. They used to interleave.

**Back steps back through the app before leaving it.** Reported on Android:
the back gesture closed the app from inside a film. The app never changed
the address, so back had nothing else to go back to. One history entry is
kept armed while anything back should close is open (`backTarget`, in
order: menu, selection, photo, book, film, Now Playing, Settings, a detail
page, a search, any tab but Home); back pops it, the top thing closes, and
it is armed again if anything is left. It is seen, not remembered: the
overlays and tabs are watched for class changes and every click is
followed, a tick later, by a check - a click's own handler runs after a
listener on the document, so checking at once missed the page it opened.
Something closed by its own x takes the entry away (`history.back`, the
pop ignored), so back never needs pressing twice.

**Content starts right under the bars.** The status line under the pills
takes no room while it has nothing to say (`:has(#status:empty)`); it was
an empty 20px on every page once the counts went.

**Normalization at the edge again.** `source.FileLister` asks each adapter
which files an item is, relative to its shelf. Each backend reports a path
differently, checked against the live servers rather than their docs:
- **Navidrome's** `getSong` path is relative to the music folder.
- **Audiobookshelf** gives `relPath`, the book folder.
- **Immich's** `originalPath` is as its container sees it (`/pictures/...`), so
  the adapter trims its `MediaRoot` with `source.RelativeTo`. That refuses
  anything outside the root, so an asset uploaded to Immich's own storage can
  never be named.
- **Jellyfin** is the same shape as Immich, checked later against a real
  Jellyfin (see "The Continue row").

`library.Resolve` decides what goes with an item. A folder goes whole. A file
takes its same-named companions: `Dune.mkv` takes `Dune.en.srt` but not
`Dune Part Two.mkv`, because the character after the shared stem must end the
name. When nothing of the shelf's kind would be left in the folder, the folder
goes instead, so a film, a Calibre book or an album's last track does not leave
its cover and sidecars behind. Every path must stay inside the shelf and may
never be the shelf itself, the same care `Save` takes with an upload. Emptied
parent folders are removed up to, never including, the shelf.

Verified in Chrome against a throwaway server. The starter ebook was selected,
the preview said "1 file, 193 KB", the delete moved its book folder into the
bin, and undo put it back and on screen.

## Browsing, which is searching for nothing

An empty query is a request to see the shelf, not a request for nothing.
Picking Audiobooks with nothing typed lists every audiobook; typing narrows
from there. `/api/search?q=` used to be a 400, which made that impossible to
ask for.

**Normalization at the edge again**: each adapter turns an empty query into
whichever call its backend offers for listing, and none of them agree.
Verified against a real provisioned stack rather than reasoned about:

- **Navidrome** answers `search3.view?query=` with everything. No change was
  needed, which is the opposite of what was expected going in.
- **Jellyfin** needs `searchTerm` *omitted*, not blank. `/Items` without it is
  its own browse. It also gets `SortBy=SortName`, which is the field Jellyfin
  sorts on and ignores a leading "The" - and that sort is only sent when
  browsing, or it would override Jellyfin's relevance ordering on a search.
- **Audiobookshelf** search matches nothing for an empty `q`, so browsing uses
  `/api/libraries/{id}/items` instead. Same `libraryItem` shape, one wrapper
  shallower - decoding the listing with the search struct yields an empty list
  and looks like an empty library rather than a bug.
- **localbooks** needed only for `matches()` to be reached with no terms,
  which already accepts everything.

Nothing in `federate` changed: `Relevance` scores every item 0 for an empty
query, so `sortItems` falls through to its title tiebreak and a browse comes
back alphabetical for free.

The drop card survived this change and then did not survive the next one; see
"The folders, and the line that replaced the box". What it carried - choose
files, the rescan, the network address - moved to one line under the filters,
because those three genuinely have nowhere else to live and everything else
it held was explaining itself.

**Paging a merged list means re-fetching, and that is not laziness.**
Infinite scroll asks for `?offset=`, and `federate.Search` asks every source
for `offset + window` items and slices *after* the merge - so page two asks
each backend for 200 and throws the first 100 away.

Per-source paging looks like the obvious alternative and is wrong: each
source's second page starts over at the top of its own order, so those items
sort in behind ones already on screen. `TestPagesWalkTheMergedOrderExactly`
is the property that matters - walking every page has to reproduce the single
sorted list with nothing repeated and nothing skipped.

That only holds if **every source returns its own first N in exactly the
merge's order**, because the globally first N can only come from the union of
each source's first N. It is a requirement on the adapters, and **no backend's
own sort satisfies it** - which this file used to understate as "Navidrome can
slip an item at a page boundary". Reported as "I'm seeing repeats in music",
and measured on the real library by scrolling 40 pages through the real merge:

- **Navidrome**: 240 distinct songs and **1,760 repeats**. `search3` lists
  songs in an order of its own; of the first 50 by title in a 4,413-song
  library, its first 50 held none, so every page drew from a different set.
- **Audiobookshelf**: 4 repeats and 4 books never shown, of 113. Its title
  sort ignores case, so "How to Fast" and "How To Overcome" swap.
- **Jellyfin**: SortName drops a leading "The"; untested live for want of
  films, but the same shape.

So there is one comparator, `media.Less` - `OrderKey` (SortKey, else title)
and then the id, so two songs called "Intro" cannot trade places between
pages - and the adapters that browse fetch the **whole** shelf, order it with
that, and cut. Searches return every match and let the merge rank them, since
a cut in the backend's relevance order is a cut in the wrong order.
`media.ShelfCache` keeps a listing for 30 seconds and a rescan clears it, so
scrolling fetches a shelf once: the same 40 pages of music went from 28.6s to
3.2s. After the fix: 2,000 distinct songs, 0 repeats; all 113 audiobooks.

`internal/source/*/paging_test.go` scroll each adapter through the real merge
against a fake that orders the way its backend does - scrambled, case-blind,
"The"-stripped - and require every item once, in order. The music one fails
against the code before this, with the same repeat the user saw.

Immich is the one exception, deliberately: photos order newest first, which
is Immich's own order, and its SortKey *is* the rank in it (see "Pictures").

`localbooks` sorts before it cuts, by title and then path.

`HasMore` is not just "this page was full": a source returning exactly what it
was asked for was probably truncated, so there is more behind it even when the
merged page came up short. Without that, one source holding a long shelf
answers a first page and looks exhausted.

`MaxDepth` (10,000) stops paging rather than serving pages that never arrive.
The work per page grows with the offset, so there has to be an end somewhere.
It was 2,000, which cut a real 4,413-song music shelf off halfway; with each
shelf fetched once and cached, a deep page is a sort in memory, not a bigger
request to a backend, so the cap moved well past a household's shelf.

On the browser side an `IntersectionObserver` alone is not enough. If a page
does not fill the screen the sentinel never leaves the viewport, so it never
crosses back in and never fires again - the list stalls with more to load.
Every append re-checks the sentinel's position once layout has settled.

**Testing this needs a second stack, and `-p` is not enough.** The compose
file pins `container_name` on every service, which a project name does not
namespace - so a second stack collides by name and refuses to start. It fails
safely rather than hijacking, which is the same protection
`Get-ExistingInstallPath` provides on the install side. An override file
renaming all four containers is what makes an isolated stack possible.

## Reaching it from another device

Nothing had to be built for this - compose publishes `0.0.0.0:8099`, so it has
always been reachable at the host's LAN address. What was missing was anybody
being told: the installer only ever printed `http://localhost:8099`, which is
the one address that does not work from a phone.

**Windows prints an IP address, not a `.local` name, and that is deliberate.**

An earlier version led with `<computer>.local`, "verified" by resolving it. The
check ran *on the machine itself*, where Windows answers for its own hostname
regardless - so it proved nothing about whether a phone could resolve it. The
risk was noticed while writing it and shipped anyway. It passed here and failed
on the first other PC it was tried on.

Windows does not reliably advertise its hostname over mDNS. What was answering
on port 5353 on the development machine turned out to be `calibre-server` and
`steamwebhelper`, unrelated applications that happen to run a responder, with
no Bonjour service installed at all. macOS and Linux with avahi genuinely do
advertise, so `install.sh` still offers the name there.

The lesson is narrower than "test more": **a check that runs on the machine
under test cannot answer a question about what another machine sees.** Resolving
your own name, reading your own local address - both were wrong here, in the
same way, a day apart.

**Printing the address was not enough on a laptop, and the development
machine could not show why.** Reported as "once it was up I couldn't connect
from another device". The development machine is on Ethernet with both
profiles Private and Docker's listener allowed on Private and Public, so it
had never hit either cause:

- **Windows makes every new Wi-Fi network Public**, and Public lets nothing
  in.
- **The Windows Security Alert for "Docker Desktop Backend"**
  (`com.docker.backend.exe`, which is what accepts published ports) appears
  during the first `compose up`. Its default ticks Private only, and
  whatever is unticked (everything, on Cancel) gets a **Block** rule. Block
  beats Allow.

So after Step 4 `Set-LanAccess` looks up the profile of the adapter holding
the LAN address. If it is Public, the installer *asks* whether this is the
home network, because marking a cafe's network Private is the wrong
default. With consent, one elevated step marks it Private, adds a
`SoundStorm - ...` Allow rule for the port on **Private only**, and takes
Private out of Docker's Block rules, leaving them blocking on Public. A work
(domain) network is left alone. The check itself needs no administrator, so
an update on an already-working PC asks for nothing. The elevated script is
passed `-EncodedCommand` and interpolates only integers and a fixed name.

`SOUNDSTORM_TLS_HOSTS` used to be written once, so a laptop that moved kept
pointing its secure name at an address it no longer had. `Update-LanAddress`
replaces the first entry, on install and on every launch, only when that
address is gone from every adapter on the machine. An address still present
was chosen, not left behind.

**And on a Mac or Linux it was never updated at all** (2026-10-06): the
address was written once, and running the installer again did not touch it,
so a router handing out a new address (a power cut, a new router) or a move
left the secure name every phone saved pointing nowhere. Now every install or
update applies the same rule (`refresh_lan_address`, and `refresh_gateway`
for the router, its UPnP looked for again), and a small check of its own runs
when the machine starts and every ten minutes (`soundstorm-address.sh` in the
install folder, from the user's crontab - no administrator; a changed
address rewrites `.env` and `compose up -d` starts SoundStorm on it).
Uninstalling takes both out. The check carries its own copy of the helpers:
the installer usually arrives through a pipe, with no file to copy from.
Checked in a Linux container: moved to an address and router it no longer
had, the check changed both and restarted SoundStorm; run again, nothing;
an address still on the machine kept; uninstall removed it. Not tried on a
real Mac, whose crontab may ask for permission the first time.

**Windows asks about a public network when SoundStorm starts, too**
(`Confirm-LanOnLaunch`, from the start-up shortcut and the desktop icon): every
new Wi-Fi network is Public, which keeps phones out with nothing saying why -
a laptop at another house, a new router. It is the setup's question (Yes:
private and the port opened, Windows asking permission); No is remembered per
network name (`SOUNDSTORM_NOT_HOME`, left out of a move), so each network is
asked once; work networks are left alone. Checked with a pretend network:
asked once, not again there, again on another, never on a private one. Not
noticed: a laptop moved while it stays on - a restart or the icon catches it.

As above, the installer cannot see what a phone sees. It reports what it
checked, and names the two causes it cannot check: a guest network and
mobile data. The profile, firewall and generated-script logic were exercised
read-only on the development machine. The elevated change itself has not run
there, because that machine never needed it.

The container cannot work its own LAN address out - inside Docker the only
addresses visible are the container's - so the *installer* does it, on the
host, and prints it. It prefers `192.168.` then `10.` then `172.`, because the
last range is also where Docker and WSL put virtual adapters that reach
nothing. On this machine that correctly picks the Ethernet address over the
Tailscale and WSL ones.

**HTTPS stays off by default, even here, and that was a reversal.** This used
to say "this is the moment HTTPS stops being optional", and the reasoning is
sound: on a shared network the session cookie and the Subsonic stream URLs -
which carry credentials in the query string, because that is the protocol - are
readable by anything else on it. What it left out is the cost to exactly the
people this is for. The only certificate SoundStorm can make without outside
help is one no browser trusts, so turning it on means a full-page "your
connection is not private" on every device, behind "Advanced, continue
(unsafe)" - which reads like being hacked to somebody who has never seen it -
and a second one after every reinstall. Against that, the threat is somebody on
your own Wi-Fi reading your traffic, which most households do not have.

**And then it flipped, as planned.** Real certificates under `soundstorm.dev`
(see "Real certificates, and the one service SoundStorm runs") took the cost
away: no warning, nothing to install, no account. So both installers now write
`SOUNDSTORM_TLS=auto`, for a fresh install and for an existing one whose
`.env` never chose - an absent line meant "off" only because off was the
default. A choice somebody made (off, self-signed, file) is left alone, and
`-Https` now means auto.

Auto is **not** the default in `docker-compose.yml` itself. The compose-only
install has no installer to record the LAN address, so auto there could name
nothing; it would still serve http and self-signed https side by side, but
that is a change of behavior nobody using compose directly asked for.

What the installers print for auto is the `http://` address, because it works
from the first second and the page moves itself to https once it has checked
it can. Then they wait up to 45 seconds for `secureName` from `/api/session` -
asked over plain http on the host, so no certificate is involved in asking -
and print the https address for phones when it arrives, because that is the
one a phone can install the app from. The certificate-warning speech is only
printed for self-signed, the one mode where it is true.

Auto mode serves plain http beside TLS **even when it has no LAN address to
name**. It used to fall back to plain self-signed, https only, which would
have broken the `http://localhost` address the installer prints. Caught while
making auto the default, before it shipped.

## Moving to another computer

`--export PATH` / `-Export PATH` (and `-Move`, the Start menu shortcut with a
folder picker) packs an install into `PATH/SoundStorm-move`; `--import` /
`-Import` installs from one. In the installers, not the app, because it drives
Docker - the thing SoundStorm is denied.

What goes: every data volume except the caches and downloaded models
(jellyfin-cache, immich-models, storyteller-models) and tailscale-state, a
node identity that belongs to one machine. Each volume is a plain tar made in
an `alpine:3` container, so owners travel as numbers and each backend can
read its own files on the other side. `settings.env` is the `.env` without
what describes the old computer (port, LAN address, gateway, UPnP, library
path), which the new one works out afresh; setup code and secrets come
across. The library is copied unless asked not to. SoundStorm is stopped for
the copy - a database copied mid-write may not open - and started again
whatever happens. The folder carries a launcher for each kind of computer,
installing from the folder it sits in.

Import refuses a computer that already has SoundStorm data or an install in
the folder: writing over accounts is not something to do by accident. The
volumes are restored before anything starts (a backend started on empty
volumes sets itself up afresh), labeled as compose labels its own so compose
adopts them without a warning.

Rehearsed on throwaway compose projects - `SOUNDSTORM_PROJECT` exists only
for that - in all four directions: Linux to Linux, Windows to Windows, Linux
to Windows and Windows to Linux, checking settings, media, data and owners
(10001 and 1000) arrived. What the rehearsals found:

- **Line endings.** Windows wrote the manifest and settings with CRLF, so
  Linux refused the manifest and would have read every setting with a
  carriage return in its value. Both are written with LF now, and read
  tolerating either.
- **A Windows-formatted drive refuses some Linux names**, and one refused
  name stopped the whole export. Now everything that can be copied is, and
  the failures are listed. Names Windows cannot hold are listed up front
  (`windows-name-problems.txt`), and a Windows import points at the list.
- **`SOUNDSTORM_FORCE=1` did nothing in install.sh** against "installed in
  another folder", though the message has always said it would. It does now.
- **A drive root breaks the relaunch**: `-Export "E:\"` reached the hidden
  copy as `E:"` plus the rest of the line, a trailing backslash escaping the
  quote. `ConvertTo-ArgumentList` doubles it.

Not exercised: the folder picker and window of `-Move`, and a full import of
real backends (the rehearsals stood in a single container) - a second
machine is what that needs.

## Getting back in

Signup closes permanently once the first account exists, so a forgotten owner
password used to mean hand-editing `state.json` inside a container - a file
with no documented shape holding the credentials for all four backends.
`soundstorm reset-password` is the supported path, and it lives in the main
binary rather than a second one so that it is wherever the server is.

**`state.Open` writes the file every time, even when nothing changed.** It
migrates and then `return s, s.save()` unconditionally. Two consequences:

- Any process that opens the state as a different user takes ownership of the
  file. Run the reset as root against a volume owned by uid 10001 and
  SoundStorm then crash-loops on `read state: permission denied` at startup.
  Confirmed by doing it to a live install.
- So the reset stats the file **before** calling `state.Open`, not after, and
  hands the ownership back. Statting afterwards captures the ownership Open
  has already replaced, which looks correct and fixes nothing.

`docker compose run --rm soundstorm reset-password` runs as the image's own
user and never had the problem. The tool defends itself anyway, because the
documented invocation should not be the thing standing between somebody and a
working server.

The server has to be stopped first: a running SoundStorm holds the state in
memory and rewrites the whole file on its next change, silently undoing the
reset. Sessions are left alone on purpose - recovering your own password is
not evidence of a compromise, and signing every device in the house out would
be its own small disaster.

## Installing, updating, removing

**Updating is installing again.** The installer pulls newer images and
restarts, so there is no separate update path to keep working. Windows gets an
"Update SoundStorm" Start menu shortcut that runs exactly that.

SoundStorm **cannot update itself, and should not learn how**: it has no access
to the Docker socket, which is the whole reason compromising it cannot reach
the host. An in-app update button would mean handing it that socket.

**Uninstalling has one rule: never touch `library/`.** `docker compose down -v`
removes the named volumes - accounts, and Jellyfin's and Navidrome's own
databases - while the library is a bind mount from the install folder and
survives untouched. The uninstaller removes the compose file, the `.env`, the
saved script, the shortcuts and the registry entry, then prints where the media
was left. Deleting the install folder wholesale would take somebody's media
with it, which is why the folder itself is never removed. There is a test for
this: a marker file in `library/music` has to still be readable afterwards.

On Windows it registers under `HKCU\...\CurrentVersion\Uninstall\SoundStorm`, so it
appears in Settings, Apps like anything else - HKCU rather than HKLM because
the install is per-user and needs no administrator. `UninstallString` points at
the copy of the script saved in the install folder, which means **an install
can only be removed by a version of the script that knows how**; anything
installed before this existed needs the current installer run over it first.

## Telling the backends to look

Every backend indexes on its own timer - Navidrome every minute, the ebook
scanner every two, Jellyfin and Audiobookshelf when their watchers notice. So a
file could sit on disk, already uploaded, unsearchable, for up to two minutes
after somebody watched the progress bar finish. The folder guide said
"indexing..." and the drop panel said "it may take a minute", which was honest
and unsatisfying.

`source.Rescanner` is the optional "look now" call, and `handleUpload`
schedules one. All four were checked against the running servers rather than
taken from documentation:

- **Navidrome** `/rest/startScan.view` answers with `scanning: true`, and the
  scan it starts is the *quick* kind - it looks at what changed rather than
  re-reading every tag, which is what makes it cheap enough to fire per upload.
- **Jellyfin** `POST /Library/Refresh` answers 204, and a made-up path under
  `/Library` answers 404, so the 204 means something. It refreshes every
  library rather than one; there is no per-library trigger that does not need
  the library's own id, and the two Jellyfin sources share a server anyway.
- **Audiobookshelf** `POST /api/libraries/{id}/scan`, which provisioning
  already used.
- **Ebooks** have no backend - it is simply the scan the ticker would have run.

**The debounce is the load-bearing part.** A dropped folder arrives as one
upload per file, so triggering per file would ask Navidrome to scan thirty
times for one album. `scheduleRescan` keeps one timer per kind and pushes it
back on each upload, so a long upload produces one scan after the last file.
Two seconds, which is nothing against the minute it replaces.

Failing to start a scan is logged and dropped: the upload already succeeded,
and the backend's own timer will find the file regardless. Measured end to end
after this: an uploaded ebook was searchable in **5 seconds**, and Navidrome's
`lastScan` moved three seconds after being asked.

**A scan only notices a deletion if the folder is not empty.** Jellyfin refuses
to remove items when a library folder comes back empty - it cannot tell
"everything was deleted" from "the drive did not mount", and emptying somebody's
library over a bad mount is much the worse mistake. It logs
`Library folder "/media/movies" is inaccessible or empty, skipping` and changes
nothing, so every deleted film stays searchable for ever. There is nowhere a
user could fix that by hand, because SoundStorm never shows Jellyfin's UI.

This surfaced as "I deleted all the files but some still show". Navidrome was
fine - it flags the rows `missing` and drops them from search - and so was the
ebook scanner. Jellyfin held nine items across repeated refreshes.

What had been holding it up all along was `library/*/README.txt`. The folders
ship with a placeholder so a fresh clone has the structure, which means they are
never empty, which means Jellyfin never took that branch. Nothing *created* it:
it was only in git, so a fresh install has no placeholder in `movies` or `tv`
(the starter library deliberately ships no video) and was one deletion away from
the same trap. `library.EnsurePlaceholders` now writes it, and `rescanNow` calls
that **before** asking any backend to scan - the scan that matters is the one
right after somebody emptied a folder from their file manager. Deleting the
placeholder and asking for a scan restored it and the nine items went in five
seconds, along with the dead studios and genres behind them.

So the placeholder is documentation and it is load-bearing, and its own text
used to end "Deleting it is harmless." It is generated from the same
`Description` and `Example` the home screen shows, and a test asserts it names
none of the backends - a file dropped in somebody's media folder is a poor place
to break the claim that they never learn Jellyfin exists.

**Every backend handles a deleted file differently, and only one of the four
needed no help.** That was worth finding out one at a time rather than assuming
Jellyfin's answer was the general one - the follow-up report was "the audiobooks
never disappeared either", and it was a different bug with the same symptom.

- **Navidrome** flags the row `missing` and excludes it from `search3` itself.
  Nothing to do. Its database still held 52 tracks for an empty folder while
  answering search with none of them, which is exactly right.
- **Jellyfin** removes the item on the next validation, as long as the folder is
  not empty. See above. It can also hold episodes that never had a file - with a
  user's "display missing episodes" preference on it manufactures one per gap in
  a series - so the adapter sends `IsMissing=false`. SoundStorm owns the Jellyfin
  account and never turns that preference on, so that is insurance; it is there
  because the other two backends both turned out to serve items for files that
  were gone.
- **Audiobookshelf** sets `isMissing` and **keeps serving the item** from both
  `/search` and `/items`. That is defensible for a server - a book on an
  unplugged drive should not lose its listening position - and it means a shelf
  somebody emptied still looks full, with a play button behind every entry. So
  the adapter skips them. Six of the seven items in a live library were missing
  when this was found.
- **localbooks** reads the folder on every scan, so the question cannot arise.

The Audiobookshelf filter is client-side because it has to be: the `/items`
`filter` parameter selects items that match a condition, and there is no "not
missing" to ask for. A page can therefore come back shorter than its limit,
which `federate` already tolerates.

Both endpoints are tested, because they are fetched and decoded separately and
only the conversion after them is shared - a fix that covered one would look
complete.

## Accounts, and the one thing that is per person

Two roles, and the gap between them is deliberately thin: the **owner** is
whoever installed the server and can add and remove people; everyone else is a
**member**. That is the entire difference. A media server for a household does
not need a permission matrix, and every role beyond these two is a decision
somebody has to make about their family.

Signup is a first-boot action and closes the moment an account exists - a
second one would be a stranger who found the port claiming somebody else's
server. After that only the owner adds accounts. There are no invite links and
no open registration, because this thing is meant to be reachable from outside
a house.

**Until that first account exists, whoever reaches the port owns the server**,
so the first sign-up needs a setup code. "Only from the home network" was the
first answer and cannot be checked: under Docker Desktop every connection -
`localhost`, the LAN address, and anything a router forwards from the internet
- arrives from Docker's own `172.20.0.1`. Measured with a failed sign-in from
each, not assumed; Linux keeps the real address, Windows and macOS do not.

The code costs the person installing nothing. The installers generate it into
`.env` (`SOUNDSTORM_SETUP_CODE`) and open the browser at `/?setup=<code>`; the
page takes it out of the address once it knows it is staying, so it is not left
in history or a bookmark. With no installer - compose alone - SoundStorm makes
one up at each start and logs it until an account exists. The form only asks
for it when the address did not carry it. Eighty random bits, so there is no
throttle on guessing it.

**That paragraph used to say the code survived the move to the https name,
and it did not.** The page stripped `?setup=` at load, *then* moved itself to
the secure name carrying `location.search` - by then empty. The installer
waits up to 45 seconds for that name before opening the browser, so on a fresh
auto-mode install the move nearly always happened, and the first screen asked
for a code the installer had never shown. Reported from a real laptop install
as "the code didn't show up, and it was too hard to find in the logs" - and the
form's own help text sent people to the logs. `forgetSetupCodeInAddress` now
runs only on the paths that stay. Checked in Chrome against the real
`index.html` and `app.js` behind a stand-in server doing HTTP and TLS on one
port: the committed code arrived at the https page with the field empty and
the code requested; the fix arrived with it filled in and gone from the
address.

Because the code can still go astray - a closed tab, the desktop icon opened
instead - it is no longer only ever in an address. Both installers end on a
framed "NEXT: create your account" box showing it in full, grouped in fours
(case, spaces and dashes are ignored); the desktop icon opens with the code
while no account exists; and the form says where to find it in the order a
person would look: the setup window, then `.env` with Notepad, then, for a
compose-only install, the logs.

**The installer is written for somebody who has never seen a terminal, and
the laptop report was about more than the code.** It said Docker Desktop's
first-run windows were confusing - does the account matter, is it safe to
skip - and that it was hard to tell whether anything was working. So: a
banner up front with how long it takes and "keep this window open"; numbered
steps ("Step 1 of 4"); a box saying what to click in each Docker window
(Accept, Skip, Skip - no account needed, closing it is fine) shown before
Docker opens itself; heartbeats during every wait that could be silent; and
notes in Gray rather than DarkGray, which is close to invisible on Windows
PowerShell's default blue console. The one thing to act on is the last thing
printed, so it is what is on screen when the scrolling stops. `install.ps1`
has no byte order mark, so it stays pure ASCII - box-drawing characters and
em dashes would come out as mojibake in Windows PowerShell 5.1.

**Then the console went, because it was still a console.** Numbered steps
and yellow boxes made it legible, but "typical users are put off by command
prompt" was about the black window itself. An interactive run now relaunches
itself with its console hidden and shows a Windows Forms window instead. The
window has the four steps ticking off, a status line, a progress bar, the
callouts as a colored panel, and at the end the setup code, the phone address
and an Open SoundStorm button. Errors show in the same window with the log's
path. The console text survives under "Show details" and in
`%TEMP%\SoundStorm-setup.log`. The library and home-network questions are
windows too. Neither `-Launch` (the desktop icon) nor `-Console` gets the
window.

How it is built, and why:

- **One thread, pumped.** The window runs on the setup's own thread, and
  every place the script waits keeps it painted: `Write-Host` and
  `Start-Sleep` are wrapped, process waits go through `Wait-ProcessPumped`,
  and each line docker prints calls `Update-Gui`. Windows Forms wants every
  dialog on one thread, and this keeps the setup's logic unchanged. The
  alternatives were a second runspace, which would have to marshal every
  dialog, or a compiled helper, which Smart App Control blocks on exactly
  these PCs. The cost is a window that can stop repainting during a silent
  docker call of a few seconds.
- **`-Wait` is gone from the elevated calls.** It blocked the thread for as
  long as winget took to install Docker Desktop, which is minutes of a window
  marked "Not responding".
- **The relaunched copy is its own temp file.** The file the parent ran may be
  a downloaded copy its own parent is about to delete. The parent exits 99,
  and `SoundStorm-Setup.cmd` skips "Press any key" for that code.
- **A window that cannot be built** restarts the setup visibly in a console,
  rather than leaving a hidden process running with nothing on screen.
- **Shown twice, on purpose.** A process started with `-WindowStyle Hidden`
  has "hidden" applied to the first window it shows. That is meant for the
  console, but PowerShell's console belongs to conhost, so the first window
  was the setup's own. The first end-to-end run sat on its error screen,
  invisible. A probe started the same way reproduced it: shown once, not
  visible; hidden and shown again, visible. Only a real hidden launch shows
  this. Rendering the window in-process, as the layout checks did, cannot.
- **Screenshots of it need `CopyFromScreen`.** `DrawToBitmap` leaves a
  RichTextBox blank, so the first layout check showed an empty panel that
  was fine on screen.

Verified by driving every state (Docker guide, downloading, finished with
code, failed) and capturing the screen, then by running the real installer
into a scratch folder. It relaunched, hid its console, showed the window,
reached the "already installed in another folder" refusal and displayed it,
and wrote nothing.

**Docker's first-run window is acknowledged, not just described.** The
Callout box saying what to click was there, and people still walked away
expecting everything to happen by itself, leaving the setup waiting on a
Docker window nobody clicked. So before Docker's first start - installing it,
or starting one whose settings file has no `LicenseTermsVersion` yet (written
when somebody clicks Accept) - `Confirm-DockerGuide` shows the three steps in
a window with no close button and one button, **I understand**; Alt+F4 shows
it again. Never on `-Launch`. Downloads also count up now ("Downloaded 3 of
12: jellyfin" as each image finishes, "downloaded 3 of 12, still coming: ..."
in the heartbeat), where the heartbeat used to count down what was left, at
the owner's asking - people expect 1 of 12 to 12 of 12.

**Then Docker's questions were answered for them** (2026-10-07, the owner:
"the most annoying part of install"; the box has none, as plain Docker on
Linux has no windows). Docker Desktop is installed with its installer's own
`--accept-license` (winget `--override "install --quiet --accept-license"`;
the switch read out of "Docker Desktop Installer.exe"), and its settings file
gets `DisplayedOnboarding: true` beside `OpenUIOnStartupDisabled` before it
first starts - the key Docker writes itself once its sign-in and survey are
done (in this PC's settings-store.json, and in Docker.Core.dll). The setup
says plainly that installing Docker accepts its terms, links them, and that
there is nothing to click and no account. A Docker already on the PC but
never opened keeps the "I understand" window, now for the one click left -
**Accept** its terms, which the setup never accepts for anybody - and gets
its sign-in and survey skipped too. Settings are written only before a
Docker's first start (a used Docker keeps what was chosen in it). Should
Docker's installer refuse the switches, it is run once more the ordinary way
and the one-click window comes back. The heartbeat at a minute says "accept
its terms, and Skip anything else" in case a window appears anyway. Checked:
the script parses in Windows PowerShell 5.1 and is plain ASCII; both ways
winget is started hand `--override`'s value over as one argument (tested
with a stand-in program); the settings writer against a throwaway folder
(both keys written, an existing file kept, the terms not marked accepted).
**Not checked: a fresh install** - this PC already has Docker (the live
server), and with Hyper-V off it runs no Windows virtual machine; a PC that
has never had Docker is the test, and whether Docker's first start then
shows nothing at all.

**Mac and Linux set Docker up too** (the same day, the owner's asking:
nobody should install it first; "keep Mac to Docker Desktop, quiet"). The one
command now installs it when missing and starts it when stopped
(`install.sh`: `need_docker`, `install_docker_linux`, `start_docker_linux`,
`install_docker_mac`, `quiet_docker_mac`, `start_docker_mac`), the password
asked once by sudo, which reads the terminal though the script comes through
a pipe. **Linux works like the box**: plain Docker by `get.docker.com`, its
output in a log unless it fails (a wall of version tables and a security
WARNING), enabled at boot, and the account added to the docker group and
used at once through `sg docker` (a `docker` shell function for the rest of
the run) - a group only applies from the next login, and that state used to
read as "Docker is installed but not running". **The Mac uses Docker
Desktop, quiet**: `Docker.dmg` for its chip, its own command-line install
with `--accept-license --user`, `DisplayedOnboarding` and
`OpenUIOnStartupDisabled` written to `~/Library/Group Containers/
group.com.docker/settings-store.json` only when it has no settings yet, then
`open -g -a Docker` and a wait; the setup says installing it accepts Docker's
terms. README, the website, the guide and the docs say so. **Checked on Linux,
twice, on a brand-new Debian 13 machine** (the box's base image, a normal
user with sudo, the installer's own Docker code piped into sh): Docker
installed, the password asked exactly once, the account let in and a
container run in the same run, a second run asking nothing - four short
lines on screen. dash's syntax check and shellcheck pass. The rest of the
install (SoundStorm's images, 12GB) was not run there, to spare the live
server's memory. **For the Mac:** the Mac part is untested - try the one
command on a Mac with Docker Desktop removed (or a fresh account), and see
that nothing in Docker asks anything; if a window still appears, it is the
settings file's place or key on macOS to check. *(Tried on the Mac,
2026-10-08, the installer's Docker step on its own: two things stopped it.
**Unmounting the download failed** ("busy", hdiutil 16, straight after
Docker's installer) and under `set -e` that ended the setup with no word -
now tried three times, then forced, never fatal (`detach_dmg`). **And writing
Docker's settings asked "Terminal would like to access data from other
apps"**: its group container is guarded by macOS, even with Docker Desktop
not installed yet (tried), and "Don't Allow" ended the setup silently. So the
Mac no longer writes them (`quiet_docker_mac` gone); its terms are accepted
by its installer, and the setup says before it starts that a Docker window
may open and to click Skip, no account needed. With that, Docker Desktop
installed and started and the step finished (Docker 29.8.2). What its first
window showed, if anything, the owner was watching for.)*
**And any Linux, not only those Docker's installer knows** (the same day, the
owner's asking): `get.docker.com` refuses what it does not know by name
(Arch, openSUSE, Alpine, and Ubuntu's offspring such as Linux Mint), so then
the system's own Docker comes from its own package manager
(`install_docker_distro`: apt's `docker.io` with Compose tried as
`docker-compose-v2`, `-plugin` or `docker-compose`; dnf; pacman; zypper; apk),
and the service is started by systemd or OpenRC (`enable_docker_service`).
A half-made Docker apt repository is removed first, or apt could not update.
`sudo` drops the environment, so apt's non-interactive setting goes through
`env`. **Checked on fresh machines of four** (official cloud images, a normal
user with sudo, the installer's own Docker code piped into sh): Ubuntu 24.04
and Fedora 44 by Docker's installer, Arch and "Linux Mint 22.1" (Ubuntu
made to name itself so, which is what Docker's installer reads) by the
fallback - each with the password asked once, Docker working in the same
run, a second run asking nothing. openSUSE and Alpine are written, not
tried.

**Fewer dead ends** (2026-10-09, the owner's asking, before trying the
setup on the test box's fresh Windows):
- **It carries on by itself after a restart.** A stop whose cure is a restart
  (WSL not finished, Docker not finished or not yet seen, virtualization
  off) has Windows start the setup once more at the next sign-in
  (`Register-Resume`: HKCU RunOnce, a copy kept as
  `%LOCALAPPDATA%\EmberStorm\soundstorm-install.ps1`, which fetches the newest
  setup first), and a **Restart now** button (`Stop-ForRestart`). Any setup
  run takes the entry back first (`Clear-Resume`).
- **Virtualization off: Restart into BIOS setup**, a button (`shutdown /r
  /fw`, administrator), and the steps saved on the desktop to read again.
- **No winget** (a brand-new Windows before the Store updates itself): Docker
  Desktop's installer straight from Docker, the same quiet switches
  (`Install-DockerDirect`), where it was a dead end pointing at a website.
- **A permission question refused** (WSL, Docker, opening the network):
  asked whether to ask again (`Confirm-TryAgain`), where it stopped the setup.
- **A small system drive beside a bigger one** (a 64GB C: and a 1TB D:): the
  library window suggests the bigger one, and closing the window takes it.
- **The questions before the long download**: the home-network question
  (and its permission) comes before the 12GB, not after - people are at the
  screen then - and, while opening the network, Docker's backend gets its
  own firewall rules (allowed on Private, blocked on Public, as the alert's
  default would), so Windows' "allow Docker Desktop Backend?" alert does not
  appear (`DockerRuleName`, `Test-DockerRulesReady`; the callout about it
  only when it still can).
- **Keeping it available, in one window** (`Confirm-AlwaysOn`, right after
  the network question - every question together, then "That is everything -
  the rest needs nothing from you"): stay awake while plugged in (unless it
  already never sleeps), keep running with the lid closed while plugged in
  (laptops), and **sign in to Windows by itself after a restart** - off unless
  ticked, saying the catch (whoever switches the PC on gets into the account;
  the owner's choice, 2026-10-09), since Docker Desktop and so EmberStorm run
  only once somebody is signed in. That one is Windows' own switch
  (`Enable-AutoSignIn`: Windows 11's setting that hides it reset, then
  netplwiz, where the person unticks it and types the password - Windows keeps
  it, encrypted; nothing in a file or this setup). A desktop's finished
  screen says how to have it switch back on after a power cut (the BIOS's
  "Restore on AC power loss").
- **It looks like EmberStorm and shows how far along it is** (2026-10-09, the
  owner's asking: "does it look nice, does it always show something is
  happening"): the app's dark colours and blue, the cloud drawn at the top
  (`Draw-Cloud`, the logo's own shapes), and the library and always-on windows
  alike (`Set-DarkTheme`); a bar of its own with a percentage - each step a
  share by how long it really takes (`Set-GuiStep`), filled from what can be
  measured (Docker's installer in megabytes, "310 of 635 MB"; EmberStorm's
  download from the bytes of each layer docker reports, with the count of
  images - `Set-GuiStepProgress`, never going back) and creeping on where
  nothing can be (an install, a start), with a light running along it so it
  never sits still. Windows' own message boxes and title bar stay Windows'.
- **Room for the download**: 20GB free where Docker keeps it (the person's
  own folder's drive), or a plain stop saying so (`Test-DownloadRoom`).
- The finished screen says it starts when somebody signs in to the PC, not
  "when you turn it on" - Docker Desktop runs only once signed in.
Checked: parses in Windows PowerShell 5.1, plain ASCII; the resume entry
written with its command and taken back, the library suggestion (and none
when C: is roomy) through the real functions with stand-in drives; Docker's
direct link answers (a 635MB installer). The administrator script for the network
decoded and parsed with Docker's rules in it; the plugged-in sleep setting
read on this PC. Not seen: a real restart carrying on, the BIOS restart, the
new buttons on screen, Docker installed without winget, the firewall alert
staying away on a fresh PC, auto sign-in end to end, the bar through a real
download. Seen: the main window part way, the always-on and library windows,
drawn on this PC and captured; Docker's progress lines read from samples.

**The window rebuilt in WPF** (2026-10-09, the owner: it "looks old, plain
text", with empty space at its foot): `New-SetupWindow` and the functions
around it (`Set-GuiStep`, `Set-GuiStatus`, `Set-GuiStepProgress`,
`Set-GuiMessage`, `Complete-Gui`, `Stop-Gui`, `Update-Gui` - the same names
the rest of the setup always called) are WPF now, part of Windows' .NET
Framework, nothing compiled or installed: a borderless window with rounded
corners and a title bar of its own (drag, minimize, close), the cloud drawn
from the logo's own shapes (and as the taskbar picture), steps with markers,
a gradient bar with a light running along it and the percentage, rounded
cards whose console-width lines run on as paragraphs (codes large and
selectable, addresses one line), and a window that sizes itself to what it
shows. **The questions are pages of this window** (`Show-GuiPage`,
`New-GuiPageText`): the library (`Select-LibraryInWindow`, Windows' own folder
picker for another folder), the home network, keeping it available, a
refused permission's Ask again, Docker's one click, the auto sign-in steps -
so, going to plan, a person sees this one window, Windows' permission
prompts, and Windows' own sign-in window if they chose it. The old Windows
Forms dialogs stay only for a run with no window (the console). Checked: each
screen drawn on this PC and looked at (part way, the library and always-on
pages, finished with a code, an error with Restart now); and the real
installer run in a scratch folder, which opened the new window and stopped
on "already installed in another folder", writing nothing.

**A review of the Windows setup (2026-10-09)**: three reviewers - bugs,
security, the window - each finding checked against the script first. Fixed:

- **The questions come first.** On a first install, where the library goes,
  the home network, keeping it available and room for the download (25GB
  while Docker is still to come) are asked before Step 1, then "That is
  everything"; installing Docker is ten minutes and more, and somebody who
  walked away came back to a question. Not when EmberStorm is installed in
  another folder (step 2 refuses, and asks nothing first). An update asks as
  before, in step 2.
- **Docker's own installer is checked as Docker's**: downloaded over https
  only (redirects too), its signature Valid and signed `O=Docker Inc`, then
  checked again as administrator on a copy in a folder under Windows' Temp
  only administrators can change, and run from there - so nothing can swap
  the file between the check and the run. winget failing for any reason now
  falls back to it.
- **An update keeps the compose file it had** (`docker-compose.yml.old`) until
  the new versions are downloaded: a failed download starts the old version
  with its own file, where it used to start nothing.
- **A restart WSL still needs is caught** (Windows' pending-restart marks):
  Docker is installed, then one restart (`Stop-ForRestart`) finishes both;
  and Docker's engine not starting after a fresh install offers the restart
  too.
- **A resumed setup keeps its settings** (`resume.env` beside the copy: the
  install folder, repository, branch, port), and stops offering to carry on
  after three restarts; never for a console run.
- **Firewall**: Docker's backend allowed on Private only, blocked on Public
  and Domain (a work laptop's network is not the setup's to open), the rule
  made at Docker's standard path even before Docker is installed; the network
  checked again once EmberStorm runs; "not my home network" remembered on
  updates too. Elevated scripts load modules only from Windows' own folder.
- **Uninstall** has the window, starts Docker if it is not running (it said
  "removed" with nothing stopped), says when it could not stop it, takes the
  setup's own firewall rules and restart entry away.
- Smaller: the desktop icon never installs Docker; the sleep setting read in
  any Windows language (powercfg's second-last value); a moved install's
  `$` no longer doubled; auto sign-in says honestly what happened.
- **The window**: never taller than the screen (the card and pages scroll,
  kept on screen as it grows), a question lifted to the front, closing stops
  the setup at its next moment rather than from inside the window's event,
  the finish says 100%.

Checked: the script parses in Windows PowerShell 5.1 and is plain ASCII; the
screens drawn and looked at; powercfg's reading on this PC (plugged-in 0);
the real installer in a scratch folder, which asked nothing and stopped on
"already installed in another folder", writing nothing and leaving no
restart entry. **Not checked:** a fresh PC (Docker's signed install, WSL's
restart, the questions first) - the test box is the test; the uninstall (it
would remove the live server).

**A blind review of the Windows setup (2026-10-10)**: three fresh reviewers -
security, bugs, the experience - shown only the script and the setup file,
not these notes; each finding checked against the code. Fixed:

- **A first install stopped part way was an update from then on** (the
  compose file is written before the download): "installed" is now a mark,
  `SOUNDSTORM_INSTALLED=1` in .env, written once EmberStorm has started and
  answered (`Test-InstalledHere`; installs from before it count by their
  uninstall entry). A `.env` already there is kept, not written afresh (it
  held the setup code and every secret, lost with `SOUNDSTORM_FORCE`).
- **After a restart the questions were asked again**, and a library chosen in
  the window was lost: `resume.env` carries `EMBERSTORM_ASKED`, `_LAN` and
  `_LIBRARY`, and the resumed run carries on without asking.
- **Uninstall**: Docker not starting no longer ends it part way
  (`Start-Docker -NoStop`); when EmberStorm could not be stopped, the entry in
  Settings, Apps and its files stay so uninstalling again can finish (they
  were removed, leaving no way back); `down` takes the Tailscale profile too;
  the finish names the saved backup.
- **Docker on a standard account**: the account that ran Docker's installer
  (an administrator's) was the one let in, so Docker never answered this
  person - `Grant-DockerUse` adds them to `docker-users`, then a restart.
- **A move**: an error now starts EmberStorm again before saying it has; an
  imported TLS choice is read after the import. `-NoShortcuts` installs get
  their uninstall entry.
- **Security**: a folder outside the person's own is made theirs alone
  (`Test-OthersCanWrite`, `Protect-PrivateFolder`: this person, SYSTEM and
  Administrators), and files planted under the names the setup writes next
  (`.new`, `.old`, the backup) are refused - another account could take over
  the compose file; a library made now on another drive is theirs alone too
  (at a drive root every account could read members' private photos; an
  existing library is left, as it may be shared on purpose and a big one
  takes a while); Docker's backend is let in on EmberStorm's port only, not
  every port any container publishes; download addresses must be https; the
  folder Docker's installer is copied into is made administrators-only
  first.
- **The window**: one setup at a time (`Local\EmberStormSetup`, a second one
  says it is running); a failure offers **Try again** (`Restart-Setup`);
  **Restart now** asks first (it was the default, and Enter lost unsaved
  work); "That's all the questions. Windows may still ask for permission";
  the status line no longer loses half of two-part messages; no "show this to
  whoever gave you the app"; the failure's subheading claims nothing it
  cannot keep; the finish shows the phone address only when phones can reach
  it (else how to fix it), the address on its own line, and how to add media;
  auto sign-in says whether it really turned on (read back from Winlogon) and
  that it allows password sign-in for every account; Esc is Cancel, the title
  bar's buttons have names for screen readers, steps still to come are
  easier to read.

Checked: the script parses in Windows PowerShell 5.1 and is plain ASCII; the
folder lock on a real folder under C:\ (open to others before, only the
three after, a new file inheriting it); the install mark's four cases and
the resume file read back; the window drawn; the real setup in a scratch
folder (stopped on "already installed in another folder", nothing written,
no restart entry). **Not checked:** a fresh PC, a standard account, the
uninstall, the one-at-a-time message, Try again. **Left:** updates unsigned
from main; the setup file's second download before the window shows (the
signed .exe replaces it); undoing the power and sign-in changes on uninstall;
Docker's data on a bigger drive when C: is small; a visible keyboard focus.

**A second blind review (2026-10-10)**: security, bugs, and one reviewer
walking six real runs through the code (a fresh 64GB mini PC needing a
restart, every permission refused once, an update, a standard account with
a parent's password, uninstall with Docker closed, a double double-click).
Several were in the first review's own fixes. Fixed:

- **The library chosen before a restart was lost**, and media went to the
  small C: - the resumed copy put it in `$Library`, but the work is done by
  the script it hands over to or the window it relaunches; it is now taken
  from the environment, which they all inherit.
- **After a restart, "click Accept in Docker"** waited on a window that never
  comes (the setup had accepted the terms): `EMBERSTORM_DOCKER_OURS` in
  resume.env.
- **Every home administrator was taken for a standard account**:
  `WindowsIdentity.Groups` leaves out deny-only groups, which is how
  Administrators shows in an unelevated token - so an extra permission
  prompt and a needless restart, and a folder owned by Administrators
  refused. `Test-AdminAccount` reads the token's claims (`denyonlysid`
  included). Checked on this PC: the old test said no, the new one yes.
- **Docker always from Docker's own installer** (signature-checked twice):
  winget ran as administrator from the person's own folder, and on a standard
  account its per-user copy failed under the parent's password and was shown
  as "permission refused". Waited on with `WaitForExit`, not `-Wait` (which
  also waits for whatever the installer starts).
- **A failed import could never be finished** (already installed, data
  there): `SOUNDSTORM_IMPORTING` marks an import under way, which carries on.
- **The library**: a chosen folder was never made private (it was made just
  before the check); a folder another account made ahead under the suggested
  name is refused; an empty one is locked too.
- **The move folder** is locked before anything is written in it, and a drive
  that cannot be locked (a USB stick) is said plainly.
- **An update stopped from the window** puts the compose file that names what
  is downloaded back (the next start asked for versions never downloaded).
- Smaller: the network question's refusal page says Skip this (it skips, it
  never stopped) and the question says private also shares files and
  printers; the firewall alert's "Allow access" only on the home network; a
  full disk is not blamed on the internet; `-NoBrowser` honoured; Try again
  keeps a Tailscale key; the console fallback lets go of the one-at-a-time
  lock first; auto sign-in checks it is for this person's account; no line
  break reaches .env, and a network name loses `#` and quotes.

Checked: parses, plain ASCII; the administrator test on this PC; the window
drawn; the real setup in a scratch folder (stopped on "already installed in
another folder", nothing written). **Not checked**: a fresh PC, a standard
account, a restart carrying on. **Left**: Docker's data always on C: (with a
small C:, Docker's disk could be moved before its first start); the first
seconds after the double-click show nothing (the signed .exe); uninstall
leaves Docker running if it started it; a refused firewall cleanup on
uninstall is not said; updates unsigned from main.

**Uninstalling leaves only what the person keeps** (2026-10-10, the owner's
design: after uninstalling, the PC as it was, but for the media and, if
wanted, a copy of the accounts). The setup records each Windows change it
makes and what was there before (`%LOCALAPPDATA%\EmberStorm\changes.json`,
`Save-SetupChange`, the first value kept so a reinstall does not overwrite
it): Docker installed, sleep and hibernate while plugged in, the lid, the
Windows 11 sign-in setting, auto sign-in. Uninstall asks first, one page,
each its own choice and shown only when the setup changed it (an install
from before the record: only when still exactly as EmberStorm leaves it,
put back to Windows' usual): **Remove Docker Desktop**, **Let this PC sleep
when idle again**, **Put the lid setting back**, **Ask for a password at
sign-in again**, **Keep a copy of your accounts** - Docker, sleep and the
lid unticked (somebody may use them for other things: left unless asked,
the owner's call), the password and the copy ticked, Cancel
stops. Always: EmberStorm's own programs taken out of Docker even when Docker
is kept (12GB that stayed), and the setup's own folder in AppData. Left on
purpose: Windows Subsystem for Linux (part of Windows) and the network's
private setting (the home network's right setting). Sleep and the lid need
no permission; the firewall rules, auto sign-in and Docker's own uninstaller
go in one elevated script, so Windows asks once. With no window (console),
Docker stays and only recorded settings go back. Checked: parses, ASCII; the
elevated script generated for three sets of choices, each parsing; the
record kept first values; power settings read on this PC. **Not run**: an
uninstall (it would take this PC's live install's shortcuts and entry);
Docker's `uninstall --quiet` itself; the page on screen.

**A third blind review (2026-10-10)**: security, bugs (the new uninstall
first), and six more runs walked through the code. Fixed:

- **Moving to another computer crashed every time**: the folder-locking
  functions were defined further down than the move uses them (a function
  defined at script level exists only once its line has run). They sit
  before everything now.
- **The uninstall**: "Remove Docker Desktop" is ticked only when the setup
  installed Docker (it was ticked for a Docker already there - one click
  would have wiped the person's own containers); for an install from before
  the record, the settings it can only guess at are offered unticked and
  worded as guesses; Docker already gone counts as nothing left to stop (the
  uninstall could never finish); when EmberStorm cannot be stopped, nothing
  else is taken back - firewall rules, shortcuts, settings, Docker - so it
  keeps working and uninstalling again finishes; an old accounts copy is
  deleted only once EmberStorm has stopped, and the finish says whether the
  copy was really saved; EmberStorm's programs leave Docker whenever it
  stopped; a refused permission keeps the record and the Settings, Apps
  entry so uninstalling again finishes; Docker's uninstaller is checked as
  signed by Docker before it runs as administrator; Cancel no longer clears
  a pending carry-on-after-restart; closing the window says it is removing.
- **Auto sign-in closed without turning it on** put the Windows 11 setting
  back straight away (it stayed off for good, never offered back). Not done:
  netplwiz keeps the password as a Windows secret that `AutoAdminLogon=0`
  makes unused but does not delete (clearing it needs a Windows call the
  setup does not make).
- **Try again keeps the answers** (every question came back, netplwiz too),
  and a run soon after a restart reads the answers from before it whichever
  copy runs (a second double-click won the race and lost the library on D:);
  the room check is not run again on carrying on.
- **An import counts as brought in only once every volume is**
  (`SOUNDSTORM_IMPORTED`), and its marks go once it starts.
- **The download no longer freezes the window**: docker is read as it runs
  (`Read-DockerLines`: both outputs, the window answering between lines), and
  a download silent for 15 minutes is stopped.
- **Security**: a new folder outside the person's own is private from the
  moment it is made, any file at its top another account owns is refused,
  and a drive that cannot keep files private (exFAT) is said; Windows' own
  folders come from Windows, not environment variables (which a program
  running as the person can change, and which reach elevated processes);
  Docker's block rule covers EmberStorm's port only, not the person's own
  containers on other networks.

Checked: parses, ASCII; `Read-DockerLines` against this PC's Docker (lines
from both outputs, the exit code, 39 window updates through the silences, a
silent command stopped); the uninstall's and sign-in's elevated scripts
generated and parsed; the window drawn; the real setup in a scratch folder
(stopped on "already installed in another folder", nothing made). **Not
checked**: an uninstall, a move, a fresh PC. **Left**: the network's private
setting and Docker's own block rules the setup loosens are not put back;
updates unsigned from main; the first seconds after the double-click show
nothing (the signed .exe).

**A fourth blind review (2026-10-10)**: security, bugs, and a first-time
user reading every screen. Fixed:

- **An uninstall left "partly" done could never be finished**: the entry in
  Settings, Apps was kept but the script it runs was deleted. Kept now.
- **The finish never said the accounts copy failed** (it looked for a file
  just deleted), and said "Docker Desktop was kept, as asked" to somebody who
  asked to remove it (the choice was overwritten first). Both read from what
  was asked, kept before anything changes it.
- **"Not my home network" was never remembered for a name like "Bob's
  WiFi"** (kept cleaned, compared raw), and never at all on a first install
  (no settings file yet - written once there is). The fix it advised (Update
  and choose Yes) could no longer ask: the advice is Windows' own setting.
- **A Windows user name with a curly apostrophe (O’Brien) broke the Docker
  install** (only the plain apostrophe was escaped in the elevated script) -
  and a TEMP folder set by malware could have used that to run as admin.
  `EscapeSingleQuotedStringContent` now; checked: the old way failed to parse
  that path, the new one keeps it intact.
- **The restart a new PC needs read as a crash** (red, "could not finish",
  a log): its own calm screen now - "One restart needed", blue, Restart now
  and Later.
- **The room and virtualization checks come before the questions** (they
  stopped the setup after every answer, the questions having changed
  settings by then); a Try again after the room check keeps the answers.
- **Question pages fit a small laptop screen**: the step list steps aside
  while a page shows.
- **Try again on a move works** (it stopped on its own unfinished folder,
  now marked `unfinished.txt` and made again).
- Smaller: answers from before a restart never carry where to download from
  (resume.env keeps the folder and port only); "That's all the questions"
  stays in sight as the subheading and says Windows' question will name
  "Windows PowerShell", which is this setup; the download says "media
  servers" and no program names; Docker's version, "https" and compose file
  names off the window; joining Docker's users explained; auto sign-in for
  another account is a page, not a passing line; the help says Try again.

Checked: parses, ASCII; the curly apostrophe through the old and new
escaping; the calm restart screen and a page without the steps drawn; the
real setup in a scratch folder. **Not checked**: an uninstall, a move, a
fresh PC. **Left**: the desktop icon shows nothing while Docker starts (a
small "Starting EmberStorm" window would help); the finish card is full on
a first install; updates unsigned from main; Windows' prompts name Windows
PowerShell (only an EmberStorm program of its own could change that).

**Docker's data goes with the library** (2026-10-10, the owner's design, after
the test box's 64GB C: had 8GB left beside a 1TB drive). EmberStorm's own data
(Docker's: about 20GB at first, growing with the library - thumbnails, the
media servers' databases) goes on the library's drive when that drive is
another one inside the PC (`<drive>\EmberStorm-Docker`, Docker's installer's
`--wsl-default-data-root`), so everything is where the person chose and C:
does not slowly fill; on C: with the library there; never on a drive that
can be unplugged (USB, SD or FireWire by how Windows says it is connected -
many USB disks call themselves fixed; `Test-InternalDrive`): with the library
external, C:, or the roomiest internal drive when C: is short. The library
page says where it goes and, when C: has 25GB, offers **Keep EmberStorm's own
data on C: instead**; kept across a restart and Try again. The room check
runs after the library question, on the drives things will really use (C:
needing only 5GB for Docker itself when its data goes elsewhere). A Docker
already installed keeps its data where it is (Docker's own settings can move
it). Checked: the rules on this PC's drives (two internal NVMe and two USB
correctly told apart, seven cases), the page drawn, the elevated script with
the option parsing. **Not checked**: Docker's installer taking the option -
the box is the test.
**Then checked for what it touched** (the same day): the library kept on a
short C: now sends Docker's data to the roomiest internal drive too (it
stopped, though the message offered a second drive); the folder, made at a
drive's root, took the drive's permissions - other accounts could reach
EmberStorm's accounts and the databases in it - so it is locked to this
person, SYSTEM and Administrators, and one another account made first is
refused; it is recorded (`dockerData` in changes.json) and the uninstall
deletes it with Docker when empty, else names it. Unchanged: updates, a
Docker already installed, moves and imports (a Docker already there keeps
its data where it is). Checked: the rules with C: roomy and short, the lock
on a test folder, the setup in a scratch folder.

**The window no longer freezes on a quiet step** (2026-10-10, the test box: the
bar sat still for 20-30 seconds and the window could not be dragged, reading as
broken). A command that prints nothing for a while - `docker info` while Docker
starts, `wsl --version`, checking the 600MB installer's signature - held the one
thread the window draws on. `Invoke-Pumped` runs such work on a second
PowerShell and keeps the window answering meanwhile; `Invoke-Native` (output
captured) and the signature check go through it. Checked on this PC: 50 window
updates through a silent 3-second command, its exit code, output and folder
right, a missing program a failure, Docker's signature read. Not seen: on the
test box.

**WSL's welcome window kept away, and the restart said plainly** (2026-10-10,
the test box): Docker's first start of WSL put "Welcome to Windows Subsystem
for Linux" over the setup. It shows while the person's `OOBEComplete` (HKCU
...\Lxss, read by wslservice) is unset, so `Initialize-Docker` sets it first,
never changing one already there. The restart screen says the window comes
back by itself within a minute or two of signing in, and its question says
what Yes and No do (Yes read as "yes, I have programs open"). Checked: the
flag written once and left alone after, on a throwaway key. Not seen: the
welcome staying away on a fresh PC.

**No Docker Desktop icon on the desktop** (2026-10-10): Docker's installer
puts one on every desktop and has no switch against it (its whole option list
read out of the installer: none for shortcuts), so the administrator step that
installs it deletes `Docker Desktop.lnk` from the shared and the person's desktop
straight after; the Start menu entry stays. Checked: the generated script
parses, the removal on a pretend desktop (EmberStorm's icon left).

**A setup stopped part way is uninstalled like any app** (2026-10-10, the
owner's asking: cancelled during the download, there was nothing in Settings,
Apps, and cleaning up meant finding folders, firewall rules and power settings
by hand). The entry is made in step 2, once the folder and its copy of the
script are there, as "EmberStorm (setup not finished)"
(`Register-Uninstaller -Unfinished`, an `EmberStormUnfinished` value), and
made the ordinary one once EmberStorm has started. `Test-InstalledHere` does
not count the unfinished entry, so the setup run again is still a first
install; the uninstall offers no accounts copy for it and leaves one kept
from an earlier install alone. Cancelled before step 2 (during Docker's
install) there is still no entry: Docker is in Settings, Apps on its own.
Checked: the four cases of the entry and the install mark on a throwaway
key, the setup in a scratch folder. Not seen: an uninstall of a stopped setup.

**The finish waits for the click** (2026-10-10, the owner's choices after the
test box): with the window, the browser no longer opens by itself - the window
comes to the front saying "Click Open EmberStorm below" (Enter works), so
nothing pops up for somebody who stepped away and the button has a purpose; a
console run still opens it. The window comes to the front when it finishes or
stops, as for a question, but is not pinned on top: pinned, it would cover
netplwiz, the folder picker and the browser through a half-hour download. The
desktop's folder shortcut is **EmberStorm library** (was "EmberStorm media",
removed on update). Checked: the finished screen drawn with the new words.

**The shortcuts wear the cloud** (2026-10-10, the owner: the desktop's
EmberStorm icon was PowerShell's, as the shortcuts run it, and did not look like
EmberStorm). `emberstorm.ico` at the repository's root, drawn by
`scripts/make-icons.py` (`windows_icon`: the app icon with rounded corners,
16-256px), is fetched by the setup beside its script copy, and every shortcut
that runs EmberStorm uses it, as does the Settings, Apps entry (`DisplayIcon`);
the library folder's shortcut keeps the folder icon. **Plain bitmaps inside**
(`bitmap_format="bmp"`): Pillow's default PNG frames read as noise in
Windows' own icon reader at 48px. Checked: a shortcut made with it, and Windows
reading the icon at 16, 32, 48 and 256. Not seen: on the test box's desktop.

**Signing in by itself names the account to click, first and bold** (2026-10-10,
the test box, twice: Windows signs in as whichever name is highlighted in
netplwiz, a half-made account from Windows' own setup was, and every start
said "wrong password"). Step 1 is now "First, click your own name in the list:
<user name>" (a Microsoft account's said to be listed by its email), in bright
bold (`New-GuiPageText` draws a line starting with "!" so). Checked: the page
drawn.

**Mac and Linux, set beside Windows** (`install.sh`, 2026-10-09):

| | Windows | Mac | Linux |
|---|---|---|---|
| Back after a restart, nobody signed in | only with auto sign-in (offered) | no - Docker Desktop needs a sign-in; the finish says how to turn on automatic login | yes: Docker is a system service, `restart: unless-stopped` |
| Kept from sleeping | asked, with the lid on laptops | asked (`pmset -c sleep 0`), and starts again after a power cut (`pmset autorestart 1`); a MacBook still sleeps lid-closed without a screen | asked on a laptop or a GNOME desktop (`gsettings`, logind lid on mains from the next start); a plain server is not asked |
| Power-cut advice | finish screen, desktops | done by `pmset` | finish, desktops |
| Room for the download | 20GB, a plain stop | 20GB in the home folder | 20GB where Docker keeps images |
| Carries on after a restart | yes, by itself | never needs one | never needs one |
| Progress | window, percentage, MB and GB | terminal lines, images counted | the same |

**And a stop where there was no terminal**: "is there a terminal to ask?" was
`{ : </dev/tty; }`, and `:` is a special built-in, whose redirection failing
ends a POSIX shell outright - so with no terminal at all (automation, a
remote command without one) the installer stopped instead of taking the
default. Now `( : </dev/tty )`, in a subshell. Shown in busybox and dash.
Checked in containers: the questions' defaults, the room check passing and
stopping at 5GB, no question on a machine with no battery or desktop. Not
tried: a real Mac, a real Linux laptop.

**No console stays open, from the double-click on.** The setup file hands
straight to PowerShell started minimized and hidden, and closes - it does not
even download: the hidden PowerShell saves the installer to a file and starts
that file as its own process (never from memory, which Defender blocks; no
detection on this machine), with `SOUNDSTORM_WINDOW` set so it opens its
window without relaunching itself, and a message box if the download fails; the
elevated WSL and Docker steps run hidden; the Update, Move library, Tailscale
and startup shortcuts and the uninstall entry all start hidden. What cannot
be removed is Windows opening a console for a double-clicked .cmd at all:
measured on this machine (Windows Terminal as the default console) at about
0.6 seconds, blank - Terminal's own start-up, since the file does nothing but
start PowerShell - with the setup window up 2.5 seconds after the click,
download included. An .exe would have no console, and an unsigned one is what Smart
App Control blocks outright.

**No message the window can show tells anybody to type a command.** "Show
this to whoever gave you the app: `cd <folder>; docker compose logs`" became
"restart and try again; if it happens again, send this file". The setup first
appends SoundStorm's own last 200 log lines to the setup log
(`Save-SoundStormLog`), and the failure window gets a Show log file button
that opens Explorer with the file selected. It is **SoundStorm's log only**:
it is written never to carry a credential, and the media servers' logs are
not held to that (a Subsonic request carries its credential in the query
string), and this is a file people are told to send to somebody. The other
rewrites:
- "Installed in another folder" now says to uninstall from Settings, Apps.
- The WSL fallback now uses the Windows Features checkboxes.
- The Tailscale hint now points at its admin console.
`-Https`, `-NoHttps` and `-Tailscale` are still named, but only to people who
typed them.

**Which shelves somebody can see is per account.** `User.Libraries` is a list
of media kinds, and nil means all of them - which is what every account created
before the field existed has, and the default for a new one. The owner is always
unrestricted whatever is stored: they are the only account that can change
these, so an owner who locked themselves out would have no way back in.

The enforcement is the reason `Registry.All`, `Matching` and `ByID` take a
context. The context carries `source.Access`, the middleware sets it once for
every guarded route, and a handler physically cannot reach a source without
passing the request's context - which is what applies the restriction. Changing
those three signatures made the compiler find all ten call sites; remembering to
check in each handler would not have.

`AccessFrom` defaults to unrestricted, because provisioning, health checks and
the library counter have no account and must see everything. That is a fail-open
default, so `internal/httpapi/libraries_test.go` walks every endpoint that can
hand over bytes, metadata or a playable URL and asserts a restricted account is
refused - including `/api/stream`, because hiding search results is not a
permission when the URL is guessable from an ordinary result.

The granularity is **a whole media kind**, which is the same thing as a source,
because a source serves exactly one kind. "No films for the seven-year-old" is
answerable; "only these films" is not, and would mean per-user Jellyfin accounts
and its parental ratings. Do not creep toward that without deciding it is worth
a second provisioning path.

**Beyond that, almost nothing differs per person.** Search returns the same
results and a film is the same bytes whoever asked. Exactly two other things are
personal:

- **Reading position**, which is ours, and is keyed `userID/sourceID/itemID`.
- **Listening position**, which is Audiobookshelf's, and is keyed by *its*
  account - so two people sharing one account there overwrite each other.

That second one is why `internal/state` grew `Identity` and why
`provision.TokenFor` exists. A member gets their own Audiobookshelf account,
created lazily on first use rather than when they are added: a backend can be
down or still provisioning when somebody joins, and first use is a retry that
costs nothing to write. The owner is special-cased to the shared administrator
credential - they provisioned the backend and already have an account on it, so
giving them a second one would split their own history in two.

Navidrome and Jellyfin keep one shared account on purpose. Nothing SoundStorm
surfaces from them differs per person, so an account each would be four times
the provisioning for no visible gain. Revisit when watched-state or favorites
reach the UI.

**Signing in is throttled, because it is the one unauthenticated endpoint
that costs real CPU.** Each attempt is 600,000 rounds of PBKDF2, which slows
guessing and is also enough to pin every core of a Pi with a handful of
parallel requests. `internal/auth/throttle.go` answers the two separately: at
most two hashes run at once whoever asks, and past five wrong passwords a client
waits one second, doubling, capped at five minutes. A throttled request is
refused *before* hashing, and refused even with the right password - otherwise
the 429 only fires for wrong answers and a guesser learns something for free.

Clients are told apart by address, and behind Docker Desktop or the Tailscale
sidecar everybody can share one. That is why it is capped backoff and never a
lockout: a script slowing the household down for a few minutes is acceptable,
a household locked out of its own server is not. `Login` itself is unthrottled
for the CLI and for signup; anything answering the network goes through
`SignIn`.

**A device that has signed in before is judged on its own record.** Capped
backoff still let a stranger guessing at a known name once a minute hold that
account's sign-ins in backoff for as long as they liked - the owner's included,
because the throttle refuses before hashing, right password or not - and let a
stranger's wrong guesses on Docker Desktop's one shared address slow the whole
household. So every successful sign-in (and sign-up, and password change) leaves
a device cookie, `soundstorm_device` (`__Host-` over TLS), holding
`id.issued.nonce.HMAC`. The HMAC is keyed by `state.DeviceKey` and covers the
account's current salt, so any password change or reset retires every token
issued before it. A sign-in carrying a valid one for the account being signed
in to skips the address's and the account's backoff and is counted against
that device alone: the same five free failures and doubling wait an address
gets, one guess in flight at a time. A stolen token is therefore no faster a
guessing channel than one address, and the stranger, who has never signed in,
has no token at all. What stays exposed is a brand-new device signing in while
an attack is actually running. Sign-out keeps the device cookie on purpose:
the device is still one the account uses.

**Changing your own password needs the current one, and signs out every other
device.** A session is only a cookie; without the check, a browser left signed
in is enough to take the account over. An owner resetting somebody's password
signs *that* account out everywhere. The `reset-password` command still leaves
sessions alone, as described under "Getting back in" - it is recovery, not a
response to a leak.

`source.WithUserID` carries the account id to the adapters, and it lives in
`internal/source` rather than `internal/auth` so an adapter can find out who is
asking without depending on how signing in works.

Two things verified against Audiobookshelf 2.36.1 rather than read anywhere:

- **`isActive` must be sent when creating a user.** Without it the account is
  created inactive, `POST /api/users` returns an ordinary 200 with a token in
  it, and that token answers `Unauthorized` to everything. Nothing in the
  response suggests a problem.
- **The create response carries a token**, so there is no second login call and
  no reason to keep the password. Deleting the user invalidates it at once.

Removing somebody deletes their sessions, their bookmarks and their backend
accounts. The backend accounts go **first**, because deleting the SoundStorm
account drops the record of which Audiobookshelf user belonged to it and after
that nothing knows what to clean up. It is best effort: a backend that is down
must not stop somebody being removed.

## Before it faces the internet

Reaching SoundStorm from outside without Tailscale was asked for, and a review
came before building any of it. What it found, and what each fix rests on:

- **A path is an instruction to a backend holding an admin credential.**
  `/api/hls/jellyfin/%252e%252e/System/Info` reached Jellyfin's admin API: the
  router decodes once, `%2e%2e` passed a `..` check, and Jellyfin decoded it
  into a dot segment and answered 200. An OPDS `dl:` id could name any URL.
  So `httpx` refuses absolute references, a second leading slash and dot
  segments for every adapter, and HLS paths must match the shapes Jellyfin's
  playlists actually use.
- **One Jellyfin account serves two sources**, so an id is all that tells a
  film from an episode - a member refused films could play one through the
  television source. Every Jellyfin target checks the item comes back from a
  query restricted to its own item types, cached ten minutes because every
  HLS segment asks.
- **Anything a browser would run as a page is sandboxed** when served from our
  origin: every book resource (only ever fetched, never navigated to), and
  streamed HTML, XML, SVG or untyped content. Not PDFs - Chrome will not
  render one in a sandboxed document - and not media, which cannot script.
- **Transport errors carried the request URL**, and a Subsonic URL has its
  credential in the query. `httpx.Redact` strips it; members see "did not
  answer", the owner sees setup detail, and the log gets the rest.
- **The first sign-up needs a setup code** - see "Accounts".
- **Guesses are limited per account as well as per address.** Per address is
  five free guesses from every address an attacker can borrow. Per account it
  is ten free, then doubling to a one-minute cap - short, because it reaches
  the real owner too, and only while somebody is guessing their name. Counted
  for any name, existing or not, or the 429 would say who has an account; a
  spray of made-up names is pruned first so it cannot flush the real one.
- **No global `ReadTimeout`, deliberately.** A read deadline that expires
  while a response is being written cancels the request's context, and a film
  stops with it. Bodies get 30 seconds of their own (`bodyDeadline`), uploads a
  rolling window that only moves when 16KB has arrived in the last minute (see
  the fifth pass, below), and headers the existing 10. Both body deadlines are
  set through `http.ResponseController`, which only reaches the connection if
  every wrapper in front of it has `Unwrap` - for months the logging
  middleware's did not, and both were silently no-ops.
- **Cross-site writes are refused on origin, not site.** The cookie is
  SameSite=Lax, and every install's name is under `soundstorm.dev` - so until
  the zone is on the Public Suffix List, another install *is* the same site.
  `Sec-Fetch-Site` must be same-origin (or absent, from a non-browser client);
  Origin is the fallback.
- **Framing only by our own pages** (a PDF opens in our iframe, so not DENY),
  and HSTS only on the real certificate's name, where it can only help.
- **An upload never replaces a file.** Two uploads of one name both passed the
  "is it there" check and the second rename overwrote the first. Files are
  hard-linked into place, which fails if the name is taken; where a filesystem
  cannot link, the check is repeated right before the rename. Links work on a
  Docker Desktop bind mount - checked, not assumed.
- **Uploads leave 1GB free**, refused up front when the size is known and
  mid-copy when it is not: the library is often Docker's disk too, and a
  backend database that cannot write is a broken server, not a full shelf.
- **`Save` applies the plan's file-type test itself**, because the upload
  endpoint can be called without a plan.
- **Duplicate detection is scoped to music and audiobooks only.** It hashes
  every same-sized, same-extension sibling in the destination folder on every
  upload, which is fine for an album but not for a picture or documents shelf
  that can hold thousands of same-sized files - and the case it exists for
  (an iTunes repurchase, same audio under two names) has no photo or document
  equivalent. Everything else stops at the plain "this name already exists"
  check, which is free.
- **A cold shelf cache is a stampede, not a cost, without coalescing.**
  Opening the app from two devices, or a browser's own retry, used to fetch
  the whole shelf once per request against a cold cache. `ShelfCache.GetOrFetch`
  shares one fetch among concurrent callers of the same key, and eviction is
  LRU rather than "wipe everything once full" - the entry a scroll is still
  paging through must not be evicted by a burst of one-off searches elsewhere.
- **The manual "check for new files" button has no owner-only guard**, and
  each real scan is Jellyfin refreshing every library or Navidrome walking the
  music folder. The 2-second debounce coalesces one upload burst into one
  scan; it does nothing against a stream of separate triggers spaced further
  apart, which is what a script hitting the button repeatedly looks like.
  `minRescanInterval` (30s) floors the gap between two real scans of one kind,
  however many requests ask for it - and a trigger that lands while a
  floor-extended timer is already pending must recompute the same floor
  rather than resetting to the bare debounce delay, or a fast enough stream of
  triggers could keep pushing a real scan two seconds away forever.
- **Filesystem errors quote the container's own absolute path.** `os.MkdirAll`,
  a failed rename and `zip.OpenReader` on a corrupted book all do, and that
  reached a browser as the body of a 400 or 404 - `/library/...` or
  `/var/lib/...`, the internal layout, to whoever's request happened to trip
  over it. `desensitizeFSError` strips it from an upload failure;
  `localbooks.OpenBook` logs the real path and returns a generic one. Every
  other error a client sees was already written for a person and never
  carried a path to begin with.
- **A session token was the literal map key in state.json.** That file is
  what a backup copies and what an old volume leaves behind, with nobody
  running the server around it, so it was itself enough to sign in as anybody
  with a live session - owner included - for as long as it had left to run.
  Sessions are now keyed by a hash of the token, migrated on open: since the
  old key *was* the raw token, hashing it in place computes exactly what a
  real cookie looks up next, so nobody already signed in loses their session.
  SHA-256, not anything slow - the input is 256 bits from `crypto/rand`, not a
  password, so there is nothing to be slow against.
- **A password had no maximum length**, on either end. PBKDF2's cost per
  round rises with the input past SHA-256's 64-byte block, so a login guess
  sent thousands of bytes long (the JSON body allows 4KB) cost several times
  an ordinary attempt for the one hash the throttle's concurrency cap was
  sized around. `MaxPasswordLength` (256) is enforced when a password is set,
  which is what makes it safe to refuse a longer guess before hashing it at
  all, at sign-in and at the current-password check alike - no genuine
  password can already be this long.
- **Reading position had no validation at all**, unlike the audiobook
  position endpoint's `isTime`. A fraction is a JSON number like any other, so
  `1e400` decoded to `+Inf` without error - the same trap already known from
  Audiobookshelf's own API - and an EPUB CFI had no length limit, so a bad
  client could grow `state.json`, the file every login rewrites whole, one
  book at a time. `isFraction` bounds it to [0, 1]; `maxLocationLength` (2KB)
  bounds the CFI.

A follow-up review, deliberately broader than the httpapi surface, looked at
everything else a network attacker could reach and everything already exposed
- the review found only two things worth changing, which says more about the
amount already covered above than about this pass:

- **`cmd/soundstorm-names`'s server had no `IdleTimeout` or `ReadTimeout`.**
  Every other `http.Server` in the project has one; this is the one genuinely
  on the internet already, where a connection opened and left idle costs a
  real file descriptor rather than a theoretical one. Bodies here are already
  capped at 4KB (`readJSON`), so a generous `ReadTimeout` costs nothing
  legitimate; `WriteTimeout` stays six minutes for the challenge-wait handler.
- **A stale claim in a log line.** `provisionCalibreWeb` warned that
  Calibre-Web's published default password was safe because "it is
  unreachable except through SoundStorm" - true when SoundStorm provisioned
  its own Calibre-Web container on an isolated network, and no longer true:
  that container is gone, and `SOUNDSTORM_CALIBREWEB_URL` now always points at
  a server running elsewhere, which SoundStorm does not run and cannot vouch
  for. Checked against the other four backends' compose entries to be sure
  the claim held nowhere else it is made: `docker-compose.yml` publishes
  exactly one port, SoundStorm's own, so it does. The warning now says the
  honest thing - the password is safe only if the operator's own server is.

Everything else this pass checked out clean and is worth recording so it is
not re-litigated from scratch: command injection, template injection and DOM
XSS sinks (none exist - no `exec.Command`, no template package, no
`innerHTML`/`insertAdjacentHTML` in the client); JWS signing in the ACME
client (fixed-width R/S encoding, not the DER `crypto/ecdsa` produces by
default) and TLS configuration generally (no `InsecureSkipVerify` anywhere,
`MinVersion: tls.VersionTLS12`); the names service's address
validation (`netip.ParseAddr` rejects the leading-zero-octal and
IPv4-mapped-IPv6 tricks that would otherwise smuggle a public address past
`IsPrivate()`, verified by hand) and its credential comparison
(`subtle.ConstantTimeCompare`, matching the setup code check); the sweep's
safety valves (minimum forget-after, maximum share per run, a regex that
cannot touch a record this service did not write); every private-key file
written by `internal/servetls` (0600, checked one by one, including the
authority key with `MaxPathLenZero` so it cannot mint another CA); that
`Registry.All/Matching/ByID`'s access check is enforced per-source-kind
rather than per-kind-by-name, which is what makes it apply to Immich and
Documents automatically rather than needing its own test for every kind
added since; that the hardened `httpx` client's dot-segment and
absolute-reference refusal applies to Immich and OPDS for free, being the
same client; EPUB zip-bomb protection (`io.LimitReader` per entry, 16MB);
generated backend credentials (`crypto/rand`, 192 bits); that no credential
is ever logged, request-logged, or present in a URL that reaches the access
log; and that `sameOrigin`'s CSRF check covers every route, verified by
reading `Routes()` top to bottom rather than assuming the wrapping holds.

**A third pass found the biggest thing in this whole review, and it was
architectural rather than a single bad line.** `recover()` did not appear
anywhere in the codebase. Go's own per-request panic recovery in `net/http`
protects exactly one goroutine: the one it creates to serve a connection. Every
`go func(){...}()` spawned from inside a handler, or started once at boot and
left running, gets none of that for free - and this project spawns nine of
them. A panic in one is not a failed request; it is the entire process going
down, for every user, mid-search or mid-scan.

- **`federate.Search` and `HealthAll`** run each source in a goroutine of its
  own, which is the whole point - a slow backend must not hold up the others.
  It also meant a *panicking* backend, not just a slow or erroring one, took
  every source down with it: worse than the dead-backend case this package
  exists to guard against, not a milder version of it. `recoverInto` catches
  it and reports that one source as failed, same shape as an ordinary error.
- **`internal/source/localbooks`'s background scan is the one of these eight
  that is directly, repeatably reachable by an ordinary member.** It parses
  whatever EPUB or PDF anyone with upload access to that shelf just dropped
  in, on a ticker, with nobody watching - the first scan runs synchronously
  during provisioning, every one after that from `rescanLoop`. `internal/tags`'
  own history (syncsafe integers, unsynchronization, an MP4 atom with no
  siblings) is evidence this class of hand-rolled parser gets edge cases wrong
  until a real file finds them; a panic here, unrecovered, would crash the
  server on that one file and crash it again identically on every restart,
  because the file is still on disk. `readSafely` catches it per file, the
  same as a parse error; `scanRecovered` is the outer net for anything else in
  the walk. A short, deliberate fuzz pass over `internal/epub` and
  `internal/pdf` (empty files, zip headers with no body, PDFs with nested
  unclosed parens, an XMP `rdf:Alt` with nothing in it) found no panic in
  either parser as it stands today - which says the fix was still worth making
  before one is found, not that it was unnecessary.
- **`provision.run`, `servetls`'s certificate-renewal loop, and the names
  service's abandoned-install sweep** are the same shape again: started once
  with `go` and answering to no request, each parsing a response from
  something outside the process - a media server during setup, Let's Encrypt
  or the name service during renewal, a DNS provider during a sweep. None of
  them needed a specific bug found to justify the fix; the fix is two lines
  and the alternative is a process that dies from a shape of response nobody
  anticipated in code that already, deliberately, retries every ordinary
  failure with backoff.
- **What did not get this treatment, and why:** `internal/tags` runs
  synchronously inside `library.Save`, which `net/http` already protects - a
  panic there fails one upload request rather than the process. Two of the
  nine goroutines stay unwrapped after being read rather than assumed safe:
  the listener that tells a TLS handshake from a plain HTTP request by its
  first byte (`servetls.sniff.classify`) does one buffered `Peek(1)` and a
  type switch, with nothing in it adversarial bytes can reach; and the
  goroutine in `cmd/soundstorm`'s main that calls `srv.Serve`/
  `ListenAndServeTLS`/`ListenAndServe` hands the actual request handling
  straight to net/http, which already isolates a panic to the one connection
  it happened on - a panic in this goroutine's own few lines would mean a bug
  in `net.Listen` or the `Serve` family itself, not attacker-controlled input.

Every fix above was verified against a real, not synthetic, panic where one
was practical to produce: a nil name-service client inside the certificate
loop, a nil sweep provider, a nil state store inside provisioning - the kind
of bug an unrelated future change could actually introduce - each confirmed
to leave the process running and the failure logged rather than the test
binary crashing.

**A fourth pass - three fresh reviewers who had not seen the code or these
notes, plus a live browser test - found one thing that mattered and several
worth fixing.** The one that mattered:

- **A stored XSS in the reader that ran as the owner, and got past the CSP.**
  A member uploads an EPUB whose chapter contains an *absolute*
  `<script src="https://this-install/api/book/resource?...&path=x.js">`.
  foliate renders chapters in a same-origin blob iframe and rewrites
  *relative* refs to `blob:` URLs the shell CSP blocks - but leaves absolute
  ones alone, and an absolute ref to our own origin is `script-src 'self'`, so
  it runs. `/api/book/resource` served that `.js` as `text/javascript`, and
  the response's own `sandbox` CSP governs it as a document, not as a
  subresource something else pulls in. When the owner opened the book the
  script ran with their session and could call `/api/users`. Confirmed in
  Chrome end to end (it reached `/api/users` and got 200), then confirmed
  closed the same way. Two guards, because one alone leaves a gap: the reader
  only ever reaches this endpoint through `fetch()` (`Sec-Fetch-Dest: empty`),
  so a request whose dest is anything else - script, image, iframe - is
  refused; and a JavaScript content-type is never emitted (mapped to
  `text/plain`), so even a client sending no `Sec-Fetch-Dest` cannot get a
  runnable script back under `nosniff`. The reader reads bytes, not
  `<script>`, so neither guard touches it - verified a normal book, images and
  all, still renders.

And the rest, each traced and fixed:

- **A concurrent burst of sign-in guesses walked past the backoff.** The wait
  was checked before hashing and a strike recorded only after, so N requests
  sent at once all passed the check before any failed. Only the global
  two-hash cap limited it. Now one guess *per account* is admitted at a time
  (`throttle.reserve`), so a burst against one name waits on itself and the
  backoff catches up. A distributed burst against one account is serialized
  the same way.
- **The owner could reset their own password through the admin path**, which
  takes no current password - handing a stolen session the persistence that
  `ChangeOwnPassword`'s current-password check exists to deny. `ResetPassword`
  now refuses `actor.ID == id`; your own password changes through
  `/api/account/password`, everyone else's through the admin path.
- **Reading position was an unauthenticated write into `state.json`.** It took
  any `source`/`id` string, checked neither, and rewrote the whole file per
  save - so any member could bloat it without bound. It now goes through
  `reg.ByID` (the same access check as every other route), caps the id length,
  and caps positions per account.
- **A member could make Jellyfin leak our admin token over HLS.** The HLS
  query reached Jellyfin unfiltered with our token; `SubtitleMethod=Hls` makes
  Jellyfin write a subtitle-playlist URL carrying that token into the master
  playlist, which is piped back. SoundStorm never asks for HLS subtitles
  (they are a separate VTT endpoint), so every `Subtitle*` key is now stripped
  - safe for real playback, which never sends them.
- **Two owners could be created by two first-boot signups racing.** `AddUser`
  now decides the role itself under the lock: the first account is the owner,
  everyone after is a member, whatever was asked for.
- **`.env` was world-readable on Linux** (default umask), and holds the setup
  code and the Tailscale auth key. `install.sh` now writes it 0600 and keeps
  it that way across rewrites.

Left for a decision rather than changed unprompted, because each is a
tradeoff or needs testing against a live backend: the local CA now carries critical
name constraints - a leaked `ca-key.pem` is trusted only for this install's own
networks and local-use names, not `yourbank.com` (narrowed further in the fifth
pass, below), closing the earlier gap; HSTS on the public
name is now a self-healing week rather than a year and is sent only while a
real certificate is actually loaded, so a lapse un-bricks itself instead of
locking a pinned browser out for a year; the name service's
zone can be exhausted and its per-domain Let's Encrypt quota burned by an
abuser (the Public Suffix List is the real fix, already noted); Immich's
Postgres uses a fixed default password reachable by a compromised sibling
container; and the installers and compose pull by moving tag rather than
digest. None is reachable by an ordinary internet user against a default
install; all are recorded here so they are not rediscovered from scratch.

**A fifth pass came after remote access shipped, and ran blind on purpose.**
Five reviewers, one per surface, were given the code and nothing else - not
this file, not the docs, not the commit history - because what found the real
bugs in earlier passes was eyes that had not absorbed this project's reasoning.
Every finding was then checked against the code before anything changed. What
was real:

- **The reader XSS again, through a different door.** `/api/art` served an
  EPUB's cover with a type taken from the file name, and only the last-resort
  cover search checked for `image/*` - so a book whose declared cover was
  `x.js` had it served as `text/javascript`, loadable by a chapter under
  `script-src 'self'`. The fix is central this time, not per handler:
  `stream.GuardActiveContent` demotes every script type to `text/plain`, every
  stream path refuses `Sec-Fetch-Dest` of script/worker/style, and a local
  target's type is resolved from its name *before* the guard runs (ServeContent
  would otherwise fill in `text/javascript` afterwards). A cover must also be
  declared an image, and SVG is not accepted as one.
- **Anyone could switch off renewals for every install.** Registration is free,
  so thirty registrations from a few addresses spent the name service's whole
  daily challenge budget. Challenges are now also limited per client network,
  and every limit keys IPv6 by /64 - keyed on the full address, one host's /64
  was a fresh limit per address. The Public Suffix List is still the real fix.
- **Cookie tossing.** Remote access lets anybody get a trusted
  `*.net.soundstorm.dev` name for a server they control, and without the PSL
  that server is same-site with every install: it could plant a session cookie
  for the whole domain that is sent ahead of the real one, signing a victim out
  on every request. Over TLS the cookie is now `__Host-soundstorm_session`,
  which a browser refuses to accept with a Domain; plain HTTP keeps the old name
  (a domain-scoped cookie never reaches a bare LAN address), and every session
  cookie sent is tried, not just the first.
- **Any member could stall the server through state.json.** Reading positions
  took any book id, sessions had no per-account cap, and HTML escaping made
  each `<` six bytes on disk - in the file every login rewrites under the lock
  every request takes. Positions are now kept only for books that exist
  (`HasBook`), sessions are capped at 50 per account, and the file is written
  without HTML escaping.
- **OPDS ids were an authenticated GET anywhere on the backend.** The id is the
  client's, fetched with the admin credential; it must now stay under the
  server's `/opds/` tree with no query of its own.
- **A crafted EPUB could exhaust memory.** `zip.OpenReader` parses the central
  directory from its start to the end of the file whatever entry count the
  archive claims, at about four times its size. `epub.Open` now reads the
  end-of-directory record first and refuses a directory span over 2MB (and
  zip64 outright); the OPF and the Calibre sidecar are capped at 2MB, the
  sidecar having been an unbounded read on every scan.
- **Name-service hygiene.** Remote-access records were never swept (only the
  LAN label was); the two DELETE endpoints had no rate limit and each spends
  registrar requests from the budget every install shares; the rate-limit table
  inserted past its cap. All three fixed.
- **Smaller.** The TLS/HTTP listener closed itself on any accept error, so
  running out of file descriptors restarted the server - it now backs off and
  retries like net/http. UPnP followed redirects, which walked straight past the
  control-URL pin - it no longer does, and with a gateway configured only a
  device on the gateway's address is used. `{}` sent to the owner-only libraries
  endpoint made a member unrestricted, because `*[]string` cannot tell an absent
  key from null; it is now refused.

Then, decided rather than left: **the local CA's constraints were narrowed to
this install**, accepting that every device which installed `/ca.crt` installs
it once more. It had permitted all of `soundstorm.dev` (every other install's
name, and the name service's), corporate-style TLDs, and every private range -
and a laptop that trusts it goes to the office, where `wiki.corp` and
`10.0.0.0/8` are somebody else's intranet. Now it permits `localhost`,
`.local`, `.lan`, `.home`, `home.arpa` and configured names; loopback; and for
each configured address its /16 (/48 for IPv6), or exactly itself if public.
Dropping `soundstorm.dev` costs nothing real: the page only moves to the real
name once the real certificate answers there.

`loadOrMakeCA` replaces an authority whose constraints are not exactly the
current policy - an unconstrained one from before constraints existed, the
earlier broad one, or one that cannot cover an address configured since - and
logs a warning naming why, since every device that installed it will warn until
it installs the new one. An address change within the same /16 keeps the
authority. The persisted `server.pem` is now also reissued when the current
authority did not sign it; without that it would have kept chaining to the one
just replaced.

**The trickled upload, and the bug it was hiding.** An upload's rolling
deadline was pushed back on every read however little arrived, so one byte
every fifty-nine seconds held a connection, a goroutine, a staging file and a
descriptor open for ever. Now the window only moves once `uploadMinProgress`
(16KB) has arrived in it - a bad mobile link manages that easily, a trickle
never does - and an account may have at most four uploads in flight (the app
sends one file at a time).

Writing the test for it through the real route chain found something larger:
**no read deadline set behind the logging middleware had ever worked.**
`statusRecorder` wrapped the ResponseWriter without an `Unwrap`, so
`http.NewResponseController(w).SetReadDeadline` answered `ErrNotSupported` -
to `bodyDeadline` and to the upload window alike - and both callers discarded
the error. `TestASlowBodyIsCutOff` built `bodyDeadline` on its own, without the
middleware, and so passed the whole time. The recorder now has `Unwrap`, and
`TestASlowBodyIsCutOffThroughTheRealRoutes` goes through `Routes()`; both new
tests were checked to fail with `Unwrap` removed, and the trickle test to fail
against the old push-on-every-read logic. Same lesson as the PWA check: a test
that cannot see the failure is believed anyway.

**The sign-in lockout, fixed without weakening the throttle.** A stranger
guessing once a minute could hold a known account's sign-ins in backoff
indefinitely, the owner's included, and behind Docker Desktop one stranger's
wrong guesses slowed every household sign-in. The per-account limit could not
simply go - it is what stops guessing from many addresses - so a browser that
has signed in before now carries a device token and is judged on its own record
instead (see "Accounts"). The end-to-end test runs every client from 127.0.0.1,
the Docker Desktop case exactly, and fails with the exemption disabled.

**The Windows `.env` now has an ACL of its own**, as the Linux one has had
`umask 077`. It inherited its folder's permissions, which under the user profile
already exclude other users - but `SOUNDSTORM_DIR` can put the install anywhere,
and `C:\SoundStorm` inherits "Users: read" from the drive. `Protect-SecretFile`
replaces the inherited entries with full control for the current user, SYSTEM
and Administrators only, naming them by SID because group names are localized.
It runs after every write of `.env`, once on every install run (so an existing
install is fixed by updating), and on the uninstaller's backup, which holds every
media server's password and arrives through a bind mount with the folder's
permissions. Checked on this machine against a folder granting Users read: five
inherited entries before, three protected ones after, the lock surviving
`Out-File`'s rewrite, and `docker compose config` still reading the file.

**And then it failed on the first laptop it met**, with "the process does not
possess the 'SeSecurityPrivilege' privilege". Windows PowerShell's `Set-Acl`
writes every section of the descriptor, including the audit list, and writing
that needs a privilege ordinary accounts lack. The development machine
tolerated it, and the reason was never pinned down. It now uses
`FileInfo.SetAccessControl`, which persists only the sections that changed,
and falls back to `icacls` with SIDs. A failure of both is a gray note, not a
yellow alarm, because the folder's own permissions still apply.

**A registry rate limit is not the internet connection.** Eight images pulled
at once drew `toomanyrequests` on that same laptop's update. The installer
said "almost always the internet connection" and stopped. Pulls are now
retried after 30, 60 and 120 seconds. A rate limit gets its own message. An
*update* whose pull fails starts the version already installed instead of
stopping, since nothing about the install is broken.

**Neither fix reached the laptop on the first try, for two separate reasons.**
The setup file downloads `install.ps1` from raw.githubusercontent.com. That
host caches a branch URL for five minutes and **ignores the query string**
when it does: a never-before-seen random query came back `X-Cache: HIT`. So
the usual cache-busting trick does nothing there. The installer now asks the
GitHub API for the branch's newest commit and downloads from that commit's
URL, which cannot be stale. It falls back to the branch URL when the API
cannot be reached.

Separately, "Update SoundStorm" ran the copy of the script saved by the
*previous* install, so a fix to the installer only took effect on the update
after the one that fetched it. A saved or downloaded copy
(`soundstorm.ps1`, `soundstorm-install.ps1`) now fetches the newest script
and, when it differs, hands over to it with the same arguments. A local
checkout runs as it is. `SOUNDSTORM_FRESH` stops the chain. A failure inside
the new copy ends the run rather than falling back to the old copy. That,
the argument passing and the exit code were checked with a harness around
the real block.

Nothing from the fifth pass is left open.

**A sixth pass, blind again, after radio, moods, scrobbling and AudioMuse.**
Five reviewers, one per surface, no notes. What was real:

- **A book could run the app inside itself - the reader XSS a third time.**
  Not through a book resource this time but through `/static/app.js`, which
  is a real script and must stay loadable as one: a chapter naming it by
  absolute address ran it under `script-src 'self'`, against the book's own
  copy of the shell's ids, so an invisible label over a copied
  `remote-toggle` switched remote access on at the owner's first tap. Fixed
  where it starts: the reader takes every `<script>` out of every chapter
  document (foliate's `data` event, before the blob exists), and app.js and
  reader.js refuse to run in a frame. Checked in Chrome with a book carrying
  such a script: the chapter rendered, with no script and no request.
- **Links out of a book kept `window.opener`** (foliate calls
  `window.open(href, '_blank')`), so the page could swap the app's tab for a
  fake sign-in. The reader handles `external-link` itself: http(s) only,
  `noopener`.
- **A zip64 gap in the EPUB directory guard.** archive/zip also turns to a
  zip64 record when the 16-bit directory size reads 0xFFFF; the guard only
  refused 0xFFFFFFFF, so it measured one directory while the reader parsed
  another as big as the file - and the scanner reopened it on every start.
  Every trigger archive/zip has is refused now, and any zip64 locator.
- **install.sh took the UPnP answer from anything on the LAN**, and a
  LOCATION with a bare `
` in it (the Python splits on `
`) wrote a
  second line into `.env` - `SOUNDSTORM_IMAGE=` of its choosing. Only a
  one-word `http://<gateway>` URL is kept, and `set_env` refuses any value
  with a line break. install.ps1 already did both.
- **Any member could start a read-along sync for any two books**, ignoring
  Not the same book; Storyteller keeps one sync per recording, so that gave
  the whole house the wrong text for it. `POST /api/readalong` now takes
  only a pair Read Along lists.
- **A Storyteller book is an ebook holding an audiobook's audio**, so
  reading one now needs audiobook access too (`AlsoNeeds`).
- **Jellyfin's master playlist could carry our admin token** in trickplay
  tile URLs, Jellyfin's default. `enableTrickplay=false` is forced, as the
  Subtitle keys are stripped.
- **Unthrottled writes**: plays (each rewrites the collections file and
  appends to the listen log, which had no cap) and reading positions (each
  rewrites state.json). Plays: a burst of 20, then one per 10s, dropped
  quietly past it, and a year's log stops at 32MB. Positions: 30, then one
  per 2s, a 429 past it, which the app keeps unsynced and retries.
- Smaller: artist radio looked any typed name up online (now only library
  artists, as the rest of discovery); the auto-sync switch ran a goroutine
  outside the recovered loop; sign-in clients over IPv6 are keyed by /64;
  the names service limits challenges per IPv4 /24 as well.

Left for a decision: the backends' fixed default secrets (Storyteller's key,
the Immich and AudioMuse Postgres passwords) - generating them means
knowing a fresh install from an existing database, whose password cannot
change under it; reachable only on the compose network. Images pulled by
tag. The Public Suffix List, still the real fix for the names service's
shared budgets.

**A seventh pass, blind again (2026-09-30), after the phone and TV apps,
picture-free streaming, and the Plex and M3U imports.** Five reviewers - the
streaming proxy, imports and pictures, the web client, the Android app, the
Apple apps - with no notes. What was real, and fixed:

- **The Plex import reached the compose network.** A server's addresses are
  what its owner publishes through plex.tv, and the IP rule let every
  private range through, the compose network's included, with the path and
  query of the address kept - a member with a Plex account could aim
  SoundStorm's requests at internal services. Now only scheme and host:port
  are kept, eight addresses at most, and each is checked again as it is
  dialed (`dialGuard`: loopback, link-local, 100.64/10 and any network this
  container is on are refused; a home-network Plex server is not).
- **Any app on an Android phone could repoint SoundStorm** at its own server
  through a launch extra left from testing (`serverURL`); gone.
- **The Android app followed, and saved, any install's name** from a page:
  someone on the same Wi-Fi as a plain-http install could pin the app to
  their own server for good. The move to the secure name is now followed,
  never saved, and from a soundstorm.dev address only to its own twin (same
  id). The iPhone app has the same flaw, not yet fixed (below).
- **The published APK was debuggable**: adb meant the cookies (run-as) and a
  console in the signed-in page. The debug build type is now
  `isDebuggable = false`, still signed with the debug key so it installs
  over earlier ones; WebView debugging follows the flag. A `file:` link no
  longer crashes the app.
- **Two ways a member could exhaust the server's memory**: a cover image tiny
  as a file and hundreds of MB decoded, resized on every request with no
  limit (now 12 megapixels at most, two at a time, results kept); and
  picture-free song headers of up to 17MB cached 128 at a time with no
  coalescing (now 4MB, 64MB in all, one fetch per song shared). Two crafted
  song files panicked a request (a chained ID3 tag cut short, a 64-bit MP4
  box size overflowing); now refused.
- **Imports and pictures could fill things**: imports are rate limited, one
  person's playlists hold 25,000 songs in all (their file is rewritten under
  the lock everyone shares), and their pictures 300MB.
- **Downloads carried over to the next person** signing in on a shared device
  when the last was signed out elsewhere; the device now remembers whose
  they are and clears them for anybody else.
- **The reader's script stripping** read a chapter's declared type exactly, so
  `text/xml` or `text/html; charset=...` skipped it, and removed only
  `<script>`. Now any case, parameters or XML type, and event attributes,
  `javascript:` links, frames, objects and refreshes too. The CSP already
  stopped these; this does not lean on every web view passing it on.
- Smaller: more JavaScript content types refused; the Wikipedia link must be
  a wikipedia.org page.

Left, with reasons: cleartext to a LAN address (Android cannot allow only
private ranges, and http is how a LAN install is first reached); the stream
proxy following a backend's redirects (none known to be steerable, and
refusing them risks breaking playback); covers kept in the browser cache
for a week after sign-out (a URL is needed to see one).

**Fixed on the Mac (2026-09-30), found in this pass (Swift):**
- `WebViewController.swift` `sameInstall`/`decidePolicyFor`: from a plain-http
  address any `https://<anything>.home.soundstorm.dev` on the same port is
  accepted and saved (`ServerAddress.saved`), redirects included - the flaw
  fixed on Android above. Follow, do not save; from a soundstorm.dev address
  only its own id's twin.
- Other schemes are opened from any frame with no user gesture, and
  `createWebViewWith` opens any URL: only main-frame, link-activated.
- The bridge's origin check ignores the port; match `isServer`.
- Apple TV `API.swift` `absolute`: an absolute URL in a playback, track or
  subtitle answer goes to another host; require the server's host.
- `+` in query values is not encoded (URLQueryItem), so "C++" searches as
  "C  ".

All five as asked: the move to a secure name is followed and never saved (as
on Android); other schemes open only from a link followed in the main frame,
and a new window only for an http(s) link followed; the bridge checks the
port too (a default port reads 0 in `WKSecurityOrigin`); `absolute` throws
for any other host (a book file, film or subtitle so refused is not played);
`+` is sent as `%2B`. The iPhone UI tests still pass, Change server included.

**The whole-project review (2026-10-01).** Fixed on the Mac:

- **`/static/` served the app with no CSP.** The shell's policy is set only
  by `ServeShell`; `http.FileServer` answered `/static/` and
  `/static/index.html` with the same page and no policy, so a script that got
  into a book ran with the session. `webui.Assets` now refuses any folder and
  any `index.html`, and puts a no-script policy on every file it serves
  (`plex-done.html` stays: Plex sends people back to it). And the reader's
  stripping missed chapters foliate-js hands over as a Blob (anything not
  exactly XHTML, HTML, CSS or SVG): `withoutScripts` now reads a Blob as text
  first. Not browser-tested; `webui_test.go` covers the first half.
- **Go 1.24 and Alpine 3.20 were out of support**: now Go 1.27 (`go.mod`, both
  Dockerfiles, CI) and Alpine 3.24. The images were not built on the Mac (no
  Docker); the PC's next deploy is the check.
- **Exported volumes were world-readable tars**: `install.sh` writes them under
  umask 077 into a 700 folder, then 600; `install.ps1` runs
  `Protect-SecretFile` on each. The .ps1 was not syntax-checked (PowerShell
  needs a password to install on the Mac).
- **Book and picture reads had no ceiling on how many at once**: an EPUB entry
  (up to 16 MB) and `shrinkLocal`'s whole-file read (up to 20 MB) each took
  memory before any slot. Six book reads and four shrink reads at a time; a
  shrink that waits ten seconds streams the original instead, and a book read
  answers "try again shortly".
- **Apple TV crashes a server or a book could cause**: markup nested past 256
  is abandoned (freeing a tree that deep overflowed the stack, and
  `XML.all` was recursive - now a loop); `Heard.fromServer` refuses uneven
  lanes, an fps outside (0, 1000] and non-finite beats, and normalises `down`;
  `VizEngine` reads NaN time as 0 and clamps frames before converting
  (`Int(.nan)` traps); book parts, PDFs, subtitles and pictures download to
  disk and are refused past a size (`SafeLoad`), and pictures decode through
  `CGImageSourceCreateThumbnailAtIndex` at a bounded size. Builds; not run on
  the TV itself.
- iPhone: a navigation with no target frame (a new window) no longer counts
  as the main frame - `createWebViewWith` decides those. Apple TV: `;` is sent
  as `%3B` (Go drops a query pair holding one).
- Sign-out now clears `soundstorm-native-queue` (the phone player's queue);
  an OPDS reference must start `opds/`, not merely contain it.

**For the PC** - Android, from the same review *(all fixed in 0.16, 2026-10-01, with
the ninth pass's Android items: see "Android 0.16" below the ninth pass)*:

- `NativeAudio.kt` (around lines 91, 116, 135-142) and `AudioService.kt`
  (47-50) load any URL, any scheme or host, that the page passes; allow only
  the server's own origin, as the iPhone's `absolute` does.
- The session cookie is attached by hand in `AudioService` and
  `PlaybackService.loadArt`, and may follow a redirect to another host; turn
  redirects off or re-check the host.
- The `MediaSession` has no `onConnect`, so any app on the phone can control
  playback and read what is playing; accept only the system and the app.
- `MainActivity.kt` (539-543, 614-626): other schemes and `onCreateWindow`
  open without a user gesture; match the iPhone (main frame, link followed).
- No `dataExtractionRules` (Android 12+ backups and device transfer carry the
  WebView's cookie); exclude the WebView and shared-prefs data.
- `taskAffinity=""` is missing on the main activity (task hijacking on older
  Android).

**Left for discussion with the owner**: no limit on sign-in attempts in flight
from one client (`throttle.reserve`); the plain `soundstorm_session` cookie
still accepted over TLS; remote access serving plain HTTP. Smaller, in the
installers and CI: `SoundStorm-Setup.cmd` runs whatever `install.ps1` sits
beside it; `install-here.sh` uses a fixed `/tmp` path; `install.ps1` elevates
by bare name; CI's permissions are broader than publishing needs. In the
server: intake lets companion files join an existing folder; Plex's dial
guard misses Docker Desktop's 192.168.65.0/24 and gateways; the Jellyfin HLS
query is a denylist; Storyteller's `readClips` is not cached. Apple TV: ATS
allows http to public addresses.

**A ninth pass, blind (2026-10-01): eight reviewers, every file.** One
each for the HTTP API; auth, state, TLS and the names service; the backend
adapters; parsers, files and streaming; the web client; Android; the Apple
apps; and the installers and CI - none shown these notes. Each claim below
was checked against the code before anything changed. Fixed on the Mac:

- **A 16MB PDF took 2.6GB to read its title** - an XMP packet of nothing but
  empty `<Description/>`s, each decoded into a few hundred bytes of structs -
  on upload and on every scan, so a small server crash-looped on it. Packets
  over 1MB are not decoded (`maxXMP`); the test fails without it (858MB for
  4MB).
- **A playlist import pinned the CPU**: `matchKey` strips trailing brackets one
  pass at a time, each over the whole name, and a title of thousands of "()"
  took minutes. Names are cut to 300 runes first.
- **A progressive JPEG cover of thousands of empty scans** took minutes to
  decode, holding both decode slots. A JPEG with over 100 start-of-scan
  markers is sent as it is, not shrunk.
- **Jellyfin's admin token, again**: `EnableSubtitlesInManifest` writes the
  same tokened subtitle playlist into the master that `SubtitleMethod=Hls`
  does, and the strip only took keys starting "subtitle". Now any key
  containing it. An allowlist was suggested and is better, but needs every
  key Jellyfin's own playlists carry, so it waits for a live Jellyfin. From
  reading Jellyfin's source; not seen against 12.1.0.
- **A panicking shelf fetch left the shelf pending for ever** (`GetOrFetch`):
  every later request for it hung. The panic is now that fetch's error.
- **The names service's sign-ups had only a per-address limit**: 30 a day per
  /24 (or /48) and 300 a day in all now, against filling the zone's 2,500
  records or the limit table. The challenge budget is unchanged (below).
- **The setup log people are told to send carried the setup code** - SoundStorm
  logs it until an account exists, which is when a failed setup sends the
  file. `Protect-SetupLog` takes out `code=`, `?setup=` and the code in its
  groups of four, before the log is shown or saved. Regexes checked in
  Python; the PowerShell itself not run (no pwsh on the Mac).
- Installers: the move folder's `install-here.sh` downloads to `mktemp`, not a
  fixed `/tmp` name; `SoundStorm-Setup.cmd` runs a neighbouring
  `install.ps1` only in a checkout (beside `.git`); elevated PowerShell and
  wsl.exe are started by full path; `install.sh` makes an existing `.env`
  0600 on every run.
- CI: every action pinned to a commit, `contents: read` unless a job says
  otherwise, checkouts without persisted credentials; the ACME rehearsal on
  Go 1.27.
- Smaller: "playing now" to ListenBrainz is rate limited like plays; a Plex
  playlist id must be a number, and Docker Desktop's 192.168.65.0/24 (where
  host.docker.internal reaches the host's localhost) is refused; a read-along
  piece 0 is refused rather than indexing before the first.
- Web reader: a book part with no type at all (not a stylesheet) is given
  `text/plain`; untyped, a browser sniffed it and could render it as a page
  without the stripping. Not browser-tested.
- **Apple TV**: a contents link of just "#" crashed every opening of the book
  (`split` drops empty pieces; kept now); a PDF page box of no height, or
  thousands of times wider than tall, is not drawn or drawn at most three
  pages wide; a chapter's pictures are decoded once per picture and only the
  first 40, at 1600px; a song over 24MB at 96 kbps is not heard on the TV; a
  saved reading fraction and a clock past Int's range no longer trap.

**For the PC, added by this pass (Android):** a POST form (or a `data:`/
`blob:` page) can load an off-server page in the app's own window - check the
address in `onPageStarted`; the notification cover is decoded at full size
(`PlaybackService.kt:249`) - sample it down; set `allowContentAccess` false;
the Gradle wrapper has no `distributionSha256Sum`; the APK is signed with the
debug key. And the three already above (native player URLs and redirects,
`onConnect`, gestureless launches) were found again independently.

**Android 0.16 fixed both lists (2026-10-01).** The native player and its
covers take only the server's own addresses (`ServerAddress.isServer`: the
page's origin now, else the saved one), checked in `NativeAudio` and again in
`AudioService`'s data source; the session cookie is no longer copied into
headers by hand but given by a `CookieHandler` (`WebCookies`), asked afresh
for each address so a redirect elsewhere carries none; the media session
accepts only trusted controllers (the system, Bluetooth, the notification),
the app and a short list (Android Auto, Wear, the Assistant); other schemes
open only from a tapped link in the main frame, and a new window only for a
tapped http(s) link; `onPageStarted` stops anything that is not the server
starting in the app's window (a posted form, a data: or blob: page) and
shows the server again; no content or file access; nothing carried in cloud
backups or device transfer (`data_extraction_rules.xml` - a new phone signs
in again); `taskAffinity=""`; the notification's cover is decoded sampled
down to about 512px and refused over 8MB; the Gradle wrapper checks its
download's SHA-256. **Still open:** the APK is signed with the debug key -
a release key means every phone uninstalls once, so it waits for the store.
Built; not yet run on a phone.

**Left for discussion, added by this pass**: the names service's daily
challenge budget can still be spent from about eight networks, which would
let real certificates lapse in a month - the Public Suffix List is the fix;
every Windows update runs `main`'s newest installer with no signature; winget
is still elevated by name (it lives in a per-user folder). Smaller: an
Audiobookshelf track's inode is not checked against the book's files; Immich
calls are not scoped to SoundStorm's library; httpx and the stream proxy
follow redirects with Immich's `x-api-key`; Storyteller re-provisions instead
of signing in again if its long-lived token was never issued; expensive GETs
(HLS master, beats) can be started from another install's page while
soundstorm.dev is off the PSL; one lock over every person's collections, and
no fsync before renames; training routes are not owner-only; ACME polling
has no overall deadline; the duplicate check's hashing grows with the square
of an album; a malicious server can still crash the TV through full-size
AsyncImage covers.

**A tenth pass, blind (2026-10-01): nine reviewers, every file**, one per
surface - the HTTP API, photos and intake, auth/TLS/names, the backend
adapters, parsers and streaming, the web client, Android, the Apple apps, the
installers and CI - none shown these notes; each claim checked against the
code before anything changed. No critical or high finding but one; fixed on
the PC:

- **A small zip could run the server out of memory, again at every start**
  (high): every JSON in a photo download was decoded and kept, and a job
  half sorted was begun again at boot. Now 256MB of JSON and two million
  records per download at most (`JSONIndex`), a zip that would unpack to over
  three times its size plus 2GB or holds over 500,000 files is refused
  (`photoimport.ErrImplausible`), and a download is sorted at most twice
  (`importJob.Tries`).
- **Imports and uploads around the photo space**: waiting downloads count
  against the disk and a member's space before another is taken, every piece
  checks the disk, one piece at a time per download, half-sent ones go after
  48 hours, a removed person's downloads go with them; a member's upload with
  no length is refused (`photoRoom`); a corrected photo moves only to a free
  name (`library.MoveNoClobber`); a new account whose photo folder would be
  another's, or a removed person's kept one, is refused.
- **Phone backup on a shared phone**: it carried on into the next person's
  folder. Signing out (or another account appearing) turns it off, the phone
  sends the account it was turned on for (`?account=`), and the server
  refuses photos meant for someone else. And backup goes to the secure name
  the page was on, never plain http on the Wi-Fi when a secure one exists
  (`PhotoBackup.servers`).
- **One member slowing everybody**: favorites, playlists, covers and prefs
  writes are limited (`limited`, `listWrites`: a burst of 60, one a second),
  as are position saves, the scrobble token check and the read-along queue;
  removing a favorite that is not one writes nothing; a person's video
  conversions are capped at four live play sessions (`hlsSessions`) with the
  device always SoundStorm's; Continue looks up the newest 60 places;
  discovery look-ups are one per artist, 32 at most; read-along "next" only
  for a book waiting its turn; training routes are the owner's.
- **Parsers and covers**: four books opened at once (`epub.Open`), a cover's
  decode turn held until it is shrunk and the shrink cache keyed by the
  picture's hash (two books' `cover.jpg` shared an entry), MP4 boxes nested
  past eight refused and four song headers rebuilt at once, tag walks capped.
- **Sign-in and the names service**: a known device has a hashing slot of its
  own, and a guess that waits out its turn counts against its address; the
  names service checks the day's and the network's sign-up caps before an
  address's (refused sign-ups filled the table renewals count in) and lets an
  install's key push out an anonymous one; the public DNS change gets its own
  deadline; ACME has fifteen minutes overall.
- **Adapters**: a Plex server shared with somebody never gets their account
  token; the backend client does not follow a redirect to another host (its
  own headers carry admin keys); a backend's path climbing out with `..` is
  refused.
- **Web client and reader**: offline mode is refused after the server said
  signed out (`soundstorm-locked`) and downloads with no owner are cleared;
  an offline file is never opened as a page; the reader also drops SVG
  animation, links other than stylesheets, XSLT instructions, and any
  reference to the server itself (an `<img>` in a book fetching `/api/...` as
  the reader).
- **Android 0.17**: the backup and secure-name fixes above; from a plain
  address the app follows the page to a secure name only if that name
  resolves to the address in use; off-server links need a tap; the window
  catcher catches posted forms and lets go; service starts from the
  background no longer crash; the server's host is lowercased; old Android
  (before 9) accepts its media controllers.
- **Installers**: the saved script and compose files get the `.env`'s
  permissions on every run (a drive-root install let any account change
  them); winget is elevated by its real path; a move folder carries only the
  install's own choices and secrets.

**Left, with reasons:** the debug signing key (a release key means every
phone reinstalls once); a permanent router mapping left after a crash
(removing the fallback costs remote access on routers that allow only
permanent forwards); the HLS query as a denylist (an allowlist needs a live
Jellyfin's playlists), now with sessions capped; acting as admin when a
request carries no person (no route does); images by tag and the unsigned
installer chain; the names service's shared budgets (the Public Suffix List);
generated backend database passwords; firewall scope.

**For the Mac, from this pass (Apple TV, Swift):**
- `EpubBook.swift:161-165`: `XML.Builder` copies all text into every
  ancestor (`stack.last?.text += done.text`): a nested nav/NCX of 30MB makes
  ~250 copies. Keep text only where needed or cap it (64KB).
- `Reader.swift:258-261` / `EpubText.swift:201-205`: anchors can end past
  the trimmed text; clamp to `out.length`, and `end = min(end, length)` in
  `tick`.
- `CategoryTabs.swift:506`: `Int((progress*100).rounded())` traps on 1e300;
  clamp a finite 0...1 first.
- `EpubText.swift:41-58`: CFI walk is recursive over server-given steps;
  refuse over ~512 steps and loop.
- `SafeLoad.swift:13`, `API.swift:444`: downloads are checked for size only
  once on disk - cancel in a delegate past the limit; `SongAnalysis` decode
  has no cap on samples (stop at ~35 minutes).
- `LibraryViews.swift:300,356`: `AsyncImage` decodes covers at full size;
  use `SafeLoad.image(maxPixels:)`.
- iPhone `WebViewController.swift:193-197`: off-server main-frame links go to
  Safari without a tap, and main-frame `data:`/`blob:` are allowed; match
  Android (a tap; data/blob only in subframes).

*(All seven done on the Mac, 2026-10-01.)* The contents' text is kept to
64KB an element; anchors are clamped to the trimmed text and the lit range to
its length; the sync percent is clamped before it is an Int; the CFI walk is
a loop over at most 512 steps; downloads go through `CappedDownload`, which
cancels as soon as the bytes written (or promised) pass the limit - checked
against a local server, a 50MB file refused in 0.07s and a small one whole -
and a song's samples stop at 35 minutes; grid covers are `SafeImage`, fetched
capped and decoded at 300-600px; the iPhone opens Safari only for a tapped
link and refuses a main-frame blob or data page (downloads aside). Both apps
build; the iPhone UI tests pass; not run on the TV itself.

**A review for bugs, not security (2026-10-01)**: reviewers per surface,
every finding checked before changing. Fixed on the PC:

- **Server:** Jellyfin conversions were never stopped (the stop call was
  dead code) - a play session asked for nothing in three minutes is now
  stopped (`hlsSessions.watch`, `source.PlaySessionStopper`); a TV episode
  looked up by id came back as nothing (Continue and favorites); MP4s named
  by ffprobe's container list went to HLS; a shared shelf fetch ran on its
  first caller's context, failing everybody waiting when that request went
  away, and one begun before a rescan was cached after it (`ShelfCache`
  takes a context, `gen`); a backend set up but not yet answering was set
  up again (now reconnects); a person's Audiobookshelf account made twice at
  once, or made and not recorded when the request ended (`absMu`, its own
  context); every photo request waited on one lock while somebody's Immich
  account was made; a scan asked for during a scan was dropped (`again`); a
  read-along queue reorder stopped half way left books cancelled; last
  year's artists counted as new in the year recap; the rescan timer could
  fire twice; deleting the last PDF in a documents folder took the folder;
  search ranking dropped accents and non-Latin scripts (`foldAccent`);
  history grew past its cap after a clock change; ID3v2.2 tags (old iTunes
  MP3s) were not read, v2.4 unsynchronised and multi-value frames went
  wrong; a PDF took a bookmark's title; PDF octal and hex strings; a 503
  cached as "send it as it is" for an hour and the slim cache's order going
  stale; the training model written when it lost to the rule; the local
  fallback certificate never renewed while running; one refused play
  blocked ListenBrainz scrobbling for good; turning remote access off after
  a failed refresh or a restart could leave the router port and public name
  up; PCP refreshes with a new nonce; port mapping retried every five
  minutes forever with a warning each time.
- **Web app:** the reader's Download button did nothing visible (two
  functions named `downloadBook`; the file one is `saveBookFile`); a film
  closed while loading played on behind the closed player (`videoToken`);
  a late album, artist, playlist or show page replaced the one on screen;
  an audiobook jump landed on the next file (`audio.pendingSeek`); the
  slow-start fallback restarted a song at 0:00; a book closed while opening
  carried on (`opening`); the reader took keys meant for Now Playing.
- **Android 0.18:** one photo the server refused stopped the whole backup
  for good (now passed over and named); the new-photo trigger fired only
  once (KEEP found its own running job); Try again after the home name
  failed kept loading the away name; every pause counted as an interruption;
  a destroyed page kept being written to; a stopped player started the next
  song by itself.
- **Docs:** README, the guide, developers, roadmap, remote-access,
  names-service, both app READMEs and two folder placeholders brought up to
  date (photos, apps, film quality, looks, audiobooks, remote access).

**soundstorm.dev's landing page is `web/`** (2026-10-04, the owner's asking:
the domain becomes SoundStorm's alone, the owner's personal site moving to a
domain of its own): plain HTML and CSS, black like the app - what it is, the
six things in it, four phone screenshots, privacy and the AGPL, the install
steps (the README's, Windows or Mac and Linux by the visitor's system), the
apps, and the box as something being tested, with a sign-up. Published with
the docs website under `/docs` by `.github/workflows/website.yml`, run by hand
only. The screenshots (`web/shots/`) come from throwaway test servers with
made-up music (generated tones and gradient covers, invented band names) and
free Lorem Picsum photos - never the owner's own media. **To go live:** the
personal site's GitHub Pages lets go of soundstorm.dev; this repo's Pages
source set to GitHub Actions, the workflow run, the custom domain set; at
Porkbun the apex's GitHub Pages A and AAAA records, `www` to
`gabrielhollberg.github.io`, the `*` catch-all to Porkbun's parking deleted -
and `home`, `net`, `names`, the `_railway-verify` lines and the install
records never touched. The sign-up is a `mailto:hello@soundstorm.dev` until
a sign-up provider is chosen, and that address needs forwarding set up.

**The documentation website is `site/`** - plain HTML, no build, opens from
disk. `site/assets/nav.js` holds the whole map (sections and pages) and
builds the header, side bar, breadcrumb, table of contents and next and
previous; a page is only its `<main>`, with `<body data-page="section/page"
data-root="..">`. Adding a page means a file and a line in `SITE`. It was
written from this file and the code; keep it in step when a decision changes,
as this file is.

**Left from this review:** stored member tokens are not
cleared when a backend is set up again; the web app's Escape is handled by
seven listeners rather than one top-layer path; object URLs for downloaded
songs, audiobooks and films are not revoked; dead CSS; app.js wants splitting
into modules; Navidrome's whole library is fetched twice per cache window.

**For the Mac, from this review (Apple TV and iPhone):**
- `Player.swift:217` `resume()` after the last item ended plays an empty
  AVQueuePlayer and shows playing: reload the item when `items()` is empty.
- `Player.swift`: no observation of item failure, `timeControlStatus` or
  audio-session interruptions - a 401 or a Siri pause shows "playing" with
  no sound (VideoSession already does this for films).
- `play(book:)` does not save the outgoing book; "end of chapter" never
  stops a book without chapters; the system Now Playing keeps the old
  chapter name (publish on chapter change); `SongListener.listen` writes an
  earlier song's tempo without checking it is still current.
- `API.perform` treats 401 as an ordinary error: route it to sign-in; at
  launch a network error drops to Connect rather than "retrying".
- `VideoPlayer.swift:145`: a failed audio switch ends the film; show a
  message and keep playing.
- iPhone: fall back to the `.net` twin when a saved `.home` name fails away
  from home, as Android does; `present(alert)` while something is presented
  never resumes the page's `confirm()`.

**A second bug review (2026-10-01, after profiles, approval and saved
servers)**: seven reviewers by surface and one for the docs, every finding
checked before changing. Fixed on the PC:

- **Approval of new devices could lock a person out.** The right password on
  a new device started a session before it was held, and sessions are capped
  at 50 an account, oldest dropped - so a leaked password tried fifty times
  from new devices signed the real person out everywhere, and with nothing
  left signed in nobody could approve them. The password is now checked alone
  (`auth.CheckSignIn`; `SignIn` is that plus `SessionFor`), and a held sign-in
  gets its session only once approved (`handlePendingSignIn`). Setting a PIN
  checks the password behind the throttle, as changing it does; "keep me on
  this device" is limited (it rewrites state.json).
- Server, smaller: a playback report is named to the millisecond and person;
  a photo download cancelled or a person removed mid-sort no longer comes back
  after a restart; a Jellyfin library that failed to register (a 503 while it
  loaded) is retried rather than left missing for good; AudioMuse's analysis
  is read on its own context (a request going away failed everyone waiting),
  a panic there is an error, and a failed read waits a minute; a Plex import's
  connections close; a removed member's half-made backend secrets go; a
  router granting a few seconds is not refreshed every second; a Google photo
  with EXIF is filed by its own clock (Takeout's is UTC); a long tag is cut on
  a character; M4A tags after a 64-bit atom are read; the duplicate check's
  atom size cannot overflow; putting back from the bin never replaces a file
  that arrived meanwhile, nor follows a path out of the library.
- Compose now passes `SOUNDSTORM_STARTER_LIBRARY`, `SOUNDSTORM_LOG_LEVEL` and
  the two `SOUNDSTORM_CALIBREWEB_*` settings, which `.env` could not reach.
  install.ps1 writes `.env` as UTF-8 without a byte order mark (ASCII turned
  an accented library folder into a question mark and nothing mounted), and
  reads it as UTF-8.
- Web: the "allow this device?" question takes the focus and is a top layer
  for the TV remote (it could not be answered on a TV); a TV choosing who's
  listening takes over the music the app kept playing; the new-password screen
  closes Now Playing, the film and the book and stops the music; Switch person
  has a Back; one setup poll after showing the app again; Allowed is said only
  when it was; TV code and approval polls no longer overlap; read-along holds
  one wake lock.
- **Android 0.21**: "after this song" stopped nothing - the native player had
  the next songs and moved into them; the page now takes them back while it is
  set, and pauses at the end. The new-photo job was appended on every launch
  (KEEP when opening, REPLACE when options change, appended only from inside
  the job). The old player's notification no longer starts beside the native
  one on a cold start. The playback log also records what the phone itself is
  playing and where (`AudioPlaybackCallback`). The silent-audio report: the
  timeline keeps moving only while the audio track takes data, so the silence
  is after the player - most likely the output route or a system mute tied to
  that one audio track, which a skip replaces; a report from 0.20/0.21 will
  show which. Dragging the timeline within the song when it happens is a
  cheap test (a seek also replaces the track).
  **The first report found it** (2026-10-02, 0.28): a song moved on to the
  next by itself, and 67 seconds later the system stopped seeing the app
  play; the report showed `player=none` - the playing service destroyed,
  the page still alive. That is Android stopping a background service about
  a minute after the app leaves the screen, so the service was not in the
  foreground then, and with it gone the next song could not start either
  (the owner: "when the song ends nothing happens"). `AudioService` now asks
  for the foreground whenever a song is meant to play (`onUpdateNotification`
  with playWhenReady, buffering the next song included), not only when
  Media3 asks, and the log records the foreground changing, the service made
  and destroyed, a start refused, and the app coming and going. A destroyed
  player now tells the page it is idle, rather than the page's clock running
  on in silence. Not checked on a phone: the test server has no music.
  **And the second report found why** (0.29): "app left" at 12:07:47 and
  "service destroyed" at 12:08:47, mid-song and playing, with no "service
  foreground" line at all - the foreground override was never called. Media3
  adds a session to its service, and so manages its notification and the
  foreground, only when a controller connects (`onGetSession`), and nothing in
  this app connects one: the page drives the player directly (NativeAudio).
  So the service had never been in the foreground since songs went native,
  and Android ended it a minute after the app was left, whenever that was.
  `AudioService.onCreate` now calls `addSession`. Not checked on a phone.
  **And paused, it would not play again** (0.41, 2026-10-06, the owner:
  music or an audiobook paused a while would not play until closed and
  started again). Paused, the service leaves the foreground and Android
  ends it about a minute later - fine for the battery - but the page still
  had its song and sent only "play", which the new, empty player could not
  answer. NativeAudio now keeps the song, the songs queued after it and the
  moment when the player is closed (`Resume`, in `detach`), and "play" on an
  empty player picks up from there; a seek meanwhile moves the kept moment;
  a new song or stop forgets it. Built; not yet tried on a phone.
- Docs: README, docs/ and site/ brought up to date (profiles, approval, TV
  sign-in, saved servers, iPhone backup, streaming quality).

**Left from this review**: a drop mixing an epub and an m4b goes wholly to
whichever comes first (`decideGroup`); a companion in an album whose name
another artist shares lands in Unknown Artist (`groupFolder`); a retried
member Immich account can get a second library; two syncs at once can give
Storyteller two books; a missing Audiobookshelf library is taken for
"provision again"; TV codes are limited per address (one behind Docker
Desktop); the sixth wrong PIN is still checked; finished photo downloads are
never forgotten; `.env` values are unquoted (a ` #` or `$` in a library path);
a flapping remote check can reissue certificates; CI's Pebble is unpinned;
Media3 is 1.5.1; **phone backup remembers what was sent per phone, not per
server or account** (Android and iPhone: a second server or person shows "all
backed up" with nothing sent).

**For the Mac, from this review (Apple TV and iPhone):** *(done on the Mac,
2026-10-02, but for the iPhone's `.net` fallback and `confirm()`)*
- **Switching person sends the last person's audiobook place to the new
  account** (`ProfilesView.go` switches before `player.stop()` saves; signing
  in as someone else never stops the player at all): save and stop first,
  awaited, and on `signedIn()` when the user changed; `signOut()` likewise.
- **Radio top-ups exclude nothing**: `exclude` must be `sourceId/id` (the
  server's `songKey`), and at most the last 1,500 - past 2,000 the server
  refuses and the station ends (`Player.swift:92`).
- iPhone backup: `backup.server` is never cleared after moving to a plain-http
  server (photos go to the old one, refused); one refused photo stops backup
  for good (pass over it, as Android 0.18); assets with no resource count in
  the total for ever. And the per-server sent list above.
- Sign-out and server switch keep `video`, `photos`, `reading`, `favorites`,
  `pills`, `look`: share `switched()`'s reset. A TV open when approval is
  turned on never notices (refresh `approveNewDevices` in the loop).
- Smaller: remove observers and remote-command targets in `deinit`;
  `VideoSession.start` after Back; a Back on the profiles picker from
  Settings; lowercase the host in `ServerAddress.parse`; `PagedItems` offset.

**Setup that fails half way finishes on the next attempt (2026-10-01).**
Each backend's first-run setup is several steps - make the admin account,
sign in, make a key, make a library - and the password was saved only once all
had worked, so a failure after the account existed (a timeout, the backend
restarting, AudioMuse's minute-long restart after saving) left an account
whose password nobody held: every retry was refused as "already set up", and
only resetting that backend's volume by hand recovered it. Now every password,
key and token a setup makes comes from `secrets` (`Manager.secretsFor`): kept
in `state.json` (`SetupSecrets`, by backend id and name) before the backend is
ever told it, and the same one on every attempt. A setup that finds its own
account already made, with a kept secret, signs in with it and carries on -
Navidrome checks a Subsonic ping, Jellyfin skips the finished wizard,
Audiobookshelf, Immich and Storyteller go straight to signing in, AudioMuse to
waiting for its kept token. Without a kept secret an initialized backend is
refused exactly as before: carrying on is only for SoundStorm's own unfinished
setup. `SetBackend` clears what a backend's setup kept. A person's own
Audiobookshelf and Immich accounts do the same, under
`<backend>/member/<user id>`, cleared once their identity is saved - a try
whose answer was lost used to leave the name taken and the person's
positions or photos broken for good. Tested with stand-in backends that fail
at the step after the account (`resume_test.go`); not tried against a real
backend failing half way.

**Defence against guessing from many addresses (2026-10-01)**, asked about
as Tor. The throttle already counts guesses per account whatever the address
(ten free, then doubling to a minute: about 1,400 a day), so Tor buys a
guesser only each exit's five free guesses - but 1,400 a day is plenty against
"password1". Two things, both built:

- **Twelve characters and not a common one** (`auth/weak.go`), when a password
  is set, never when checked. Refused: a leaked-list word (a built-in list,
  nothing fetched) once digits and symbols are stripped and look-alikes undone,
  two such words, the account's own name with a little around it, one
  character repeated, a keyboard row or counting run, all digits and symbols.
  Existing passwords are untouched until changed.
- **New devices need approval** (`httpapi/devices.go`; owner switch in People,
  `state.ApproveNewDevices`, off by default). The right password on a device
  with no valid device token for the account is held: `/api/login` answers
  202 with a pending id, the device polls `/api/login/pending/{id}`, and the
  account's signed-in devices (and the owner's) are asked through
  `/api/devices/pending`. Approved, the held session and a device cookie are
  handed over; refused or after ten minutes, deleted. Not by address: the
  owner asked for "known devices from outside", and behind Docker Desktop the
  server cannot tell outside from in, and a request's address can be faked - so
  it is the device token everywhere, the owner's choice when asked. With
  nothing else signed in, the setup code approves - only one from `.env`
  (`SetupCodeFromEnv`), never one made up at start. Checked end to end in two
  browsers: waiting, asked on the owner's screen naming the browser, allowed
  and in.

- **Asking everybody for a new password** (owner button in People,
  `POST /api/users/new-passwords`): marks every account, the owner's too
  (`User.MustChangePassword`); `withUserContext` then answers every guarded
  route but `/api/account/password` with a 403 `{"mustRenew": true}`,
  and the page shows a screen for it (`showRenew`, also raised by any such 403).
  Setting a password clears it. Signing in with a password that would be
  refused today sets the same mark (`SignIn`), so passwords from before the
  rules tightened are replaced as people next sign in. Checked in two
  browsers: the owner asked at once, the member on opening, a weak new one
  refused, a good one and back in.

**For the Mac:** the Apple TV signs in natively through `/api/login` and must
handle the 202: show "waiting for approval on a device already signed in",
poll `GET /api/login/pending/{id}` every few seconds, and take the session
cookie from its 200 (`{"signedIn": true}`); a 403 is refused, a 404 expired.
Until it does, an install with approval on cannot sign the TV in. And a 403 with `mustRenew` from any route means the account is held to a new password: say "Choose a new password on your phone or computer, then sign in again here" (changing it signs the TV out). The iPhone
and Android apps use the page and need nothing.

**Inviting the family by QR code (2026-10-03).** Rather than the owner
making up a password for each person and telling it to them, Settings,
People has **Invite someone**: a name, then a QR code and a link (Copy,
Share) shown once, and a list of invitations still waiting, each with Cancel
(`httpapi/invites.go`). The person scans it with their phone's camera, or
opens the link: "Gabriel invited you to Gabriel's SoundStorm", their name
filled in (changeable), a password of their own (today's rules) - and they
are signed in, with no device approval, as the owner vouched. An invitation
works once, for seven days, at most twenty waiting; its token (128 bits) is
in the code alone, the state keeping a hash (`state.Invites`); lookups are
limited per address. The code points at the away-from-home name when remote
access is on, so it works wherever the person is, else the home name. Opened
while signed in, it says to sign out first. Adding with a password chosen by
the owner stays, below it. Checked: `TestAnInvitationMakesAnAccount`, and in
Chrome as the owner and as an invited member. Not checked: a phone's camera on the code,
and the apps (the code opens a browser; the person then finds the server in
the app and signs in).

**An eleventh pass, blind (2026-10-08, after the rename, chosen names, the
box's screen, USB, reset and Android's Set it up)**: eight reviewers, one per
surface, none shown these notes; each claim checked against the code first.
Fixed on the PC:

- **The power button's password reset trusted the address a request named**
  (its Host): through remote access a stranger could name a home address and
  set the owner's password the moment the button was pressed. Now the
  connection's own address must be private, loopback or link-local
  (`fromHomeNetwork`), for the reset and for saying whose password it is.
- **A few kilobytes of crafted .m4b took the server's memory** (an index
  claiming billions of samples, planned one by one, or one fragment of them
  all): refused at 50 million samples, more than the file could hold, 20,000
  a fragment and 200,000 fragments (`mp4hls`).
- **A PDF of unclosed `/Title(` kept a processor busy for hours**, on upload
  and at every scan: a title is looked for within 64KB of its key, 64 keys
  at most. **A Takeout download of near-alike names was quadratic**: indexed
  by a name's first 20 letters, 1,000 compared at most, an album copy's
  agreement worked out as sidecars are added; a JSON file in a download is
  read whole only under 16MB.
- **emberstorm.app's Open my EmberStorm followed whatever address the names
  service answered** (someone sharing the connection could answer a lookalike
  page or `javascript:`): only an install's own secure name is opened. The
  app's move to its secure name checks the name's shape too.
- **The names service**: asking about a chosen name is limited before the
  registrar is asked anything (20 an hour an install, 40 a network; claims
  3 a day an install, 100 in all), claims are one step under a lock (two at
  once could both write one), and the daily sweep lets go of a chosen name
  whose install is gone and of all but the newest an install holds, with
  their home and away names, and of challenges left for chosen names
  (`TestASweepLetsGoOfNamesNobodyHolds`).
- **Android 0.49**: another of an install's names (a chosen one) is followed
  only when both answer one id **and resolve to one address** - an id is what
  a server says of itself, and anybody can say another's; a TV sign-in link
  naming a server the app does not know is ignored unless it leads to the
  saved server's address (it was handed to the saved server: device-code
  phishing); a server found on the network shows its address under the name
  it gives itself.
- An invitation is answered once (two at once made two accounts); the media
  proxy follows a redirect only on the backend's own host (Immich's key went
  with it).
- **The box**: the data drive is only ever an internal disk, mounted by its
  UUID - a USB stick labelled SSDATA took its place, with whatever accounts
  were put on it; root is locked, no SSH and no console logins on a box sold
  (DEV_SSH keeps SSH), shelves that are links are put back as folders before
  root opens them. Not run in the VM yet.

**A member could take over another person's account through a pretend TV**
(`players.go`, high; fixed the same day, the owner's choice of the ways
offered): any signed-in person could say hello as a TV (`tv: true` is the
page's word), or under the real TV's player id, and a phone sending to a free
TV gave that TV a one-time code signing it in as the sender. Now **a TV is
the house's only once the owner shares it** (`state.SharedTVs`, by player id,
with the hash of the device's own profile cookie): a TV signed in as the
owner is shared as it says hello; anybody else's is shown only to the owner,
"Not shared with the house yet", and choosing it asks "Let everyone in the
house play on <name>?" (`POST /api/tvs/{id}`); Settings, Devices, **Shared
TVs** lists them with Stop sharing. Only a shared TV, saying hello from the
device it was shared from, is shown to others or takes their commands, and
nobody can say hello under a shared TV's id from another device, or under
any player's id another device used in the last 90 seconds (409; the page
then takes a new id). `TestAPretendTVTakesNobodyOver`. **For the Mac:** the
Apple TV's hello keeps its cookies (URLSession does), and on a 409 it should
take a new player id; a member's Apple TV is shared by the owner choosing it
from a phone's device button once.

**Then fuzzing, the bundled libraries, secrets and the containers** (the same
day, by other means than reading): fuzz tests for every file reader
(`fuzz_test.go` in tags, epub, pdf, flac, photoimport, library, mp4hls,
stream; `go test -fuzz=FuzzX ./internal/<pkg>`), a minute each in a container
capped at 2GB - 16 of 19 clean; a tag of dots and spaces cut short named a
folder "." (`tagSegment` trims again), and two song-header helpers trusted
their caller's length checks (each checks now); the finds are kept as tests
(`testdata/fuzz`, `-text` in .gitattributes). hls.js 1.7.3 is the newest and
foliate-js matches upstream file for file, neither with advisories. **The
request log no longer carries secrets in addresses** (`logPath`: invitation
tokens, TV sign-in codes and ids, held sign-ins) - the Windows setup puts its
last lines in the file people send. **The containers**: no container gains
privileges after it starts (`no-new-privileges` on all fifteen), and the
photo and moods databases (and Immich's Redis) are on internal networks only
their own app reaches (`immich-private`, `audiomuse-private`) - their
passwords are fixed defaults on most installs. Applied to the live server:
all nine sources back, Immich and AudioMuse ready from their credentials,
Jellyfin unable to resolve either database. Not tried: `cap_drop` (the
databases' entrypoints change owners as root).

**Left, with reasons:** photo backup and background uploads on Android fall
back to a plain-http address (a typed IP), which anything at that address on
another Wi-Fi could answer - pinning to the home network needs the Wi-Fi's
name (a location permission) or the secure name; the names service's shared
budgets can be spent from a handful of networks (the Public Suffix List, and
Let's Encrypt's override); /v1/find shows installs to anyone on the same
public address (carrier-grade NAT); a rogue device on the Wi-Fi at setup can
answer as a new box and take the sticker's code; a Windows install at a drive
root lets another local account add a compose file (protect the folder, pass
`-f docker-compose.yml`); box updates: seed `current.json` at build, an
expiry and channel in manifests, remember rolled-back serials - before the
first box ships; the caretaker's erase needs no physical factor; Windows
updates run unsigned code from main; images by tag; the Plex import can
reach home-network addresses a Plex owner publishes (fixed paths only).

**For the Mac, from this pass:**
- `AppLinks.swift` `TVLink.invite` / `RootViewController.open`: an invitation
  link for a server the app does not know is remembered as the saved server
  - anybody can make one for their own install, and the page there then
  owns the bridge and photo backup. Do not save it: offer it on the connect
  screen as `/open` does, or open it for that launch only.
- Apple TV `SoundStormTVApp.swift` `secureAddress`/`moveServer`: any
  `secureName` the server offers is taken, the session cookies copied to it
  and saved - from a plain-http address on a LAN a spoofer hands over a name
  of theirs and gets the session. Require the same install (the label, or
  the chosen name's address resolving to the current one) as Android does.
- iPhone `WebViewController.swift` `sameInstall`: from plain http any
  `*.home` name is followed, and between labels only `/healthz`'s id is
  compared - require the names to resolve to one address (Android 0.49), and
  call `rememberServer` only for the saved server or its twin.
- Apple TV `Remote.swift:256`: `Int(position / 5)` traps on 1e300 (from a
  server's saved place or a remote's seek): clamp; and `Player` clamps
  times to finite values.
- iPhone `NativeAudio.swift:480`: lock-screen art read whole and decoded at
  full size - use the capped download and an ImageIO thumbnail.
- Cookies with a parent domain (`.emberstorm.app`) matched by suffix in
  `WebCookies` and `NativeAudio`: only the host's own.

*(All done on the Mac, 2026-10-08.)* An invitation for a server the app does
not know is offered on the connect screen with its address, never saved, and
goes on once that server is chosen (`TVLink.invite` says `known`). Two names
are one machine when they resolve to an address in common
(`ServerAddress.sameMachine`): the iPhone follows another label only with
the same `/healthz` id and that, and from plain http only a home name
resolving to the address in use; the Apple TV moves to an offered name only
with the same label (either zone) or, else, that and the same id - so photo
backup only ever remembers the saved server, its twin, or a name so proven.
The Apple TV clamps every time it is given (`Player.sane`, the report's
`position`) and takes a new player id on a hello's 409 (`renewID`). The
iPhone's lock-screen picture is read to 8MB at most and decoded straight to
a 600px thumbnail (ImageIO); cookies only the host's own, in `WebCookies` and
`NativeAudio`. Checked: both apps build; the iPhone's connect, wrong-address
and TV-code tests pass; the resolve check run on its own: the owner's two
home names one machine, a stranger's name not. Not checked: an invitation
link in the simulator (it hands these wildcard link names to Safari), and a
409 from a real server.

**A twelfth pass, blind (2026-10-08, from the Mac): nine reviewers, every
part** - the HTTP API; auth, state, TLS and the names service; the adapters;
parsers, uploads and streaming; the web client; Android; the Apple apps; the
installers, compose and CI; the box and caretaker - none shown these notes,
each claim checked against the code before changing. **Fixed on the Mac:**

- **The names service's sweep deleted chosen names of installs without
  remote access** (high): the away pass judged claims by the installs it
  could see - those with a `.net` record - so every home-only install lost its
  chosen name daily, free for anybody to claim with a certificate. A claim now
  stands while its install has a record under any label
  (`TestTheAwayPassKeepsAHomeOnlyInstallsName`, failing before).
- **A planted profile cookie took over a member** (high): another install
  under the zone could set `soundstorm_profiles` for every name; over TLS it
  was accepted, adopted by the next "keep me on this device", and the kept
  person was then switched to by anyone holding that id. Over TLS only the
  `__Host-` cookie counts now (`TestAPlantedProfileCookieIsNotTakenOverTLS`).
- **A member could read another member's photos' camera and time** through
  `/api/upload/describe` (two reviewers): pictures are now named only in the
  asker's own folder (`TestNamingAPhotoDoesNotLookInAnothersFolder`).
- **Updates never refreshed the compose file** (both installers): containers
  added since (kokoro, whisper) and every hardening in it never reached an
  install. An update now downloads it, keeping the old on a failed download.
- **iPhone and Apple TV:** a page from any server could point photo backup at
  itself (status changed the destination; `set` switched it on) - now only
  the app's own question, naming the host, sets a new destination, and
  another server's page can change nothing (`askToBackUp`,
  `isDestination`); the zone move only for an install's code, and only when
  the new name's `/healthz` id is that code (`movesHere`); TV-code links for
  an unknown server or none are ignored; replies to the network search and
  checks read to 64KB (`smallData`); integer traps on durations, the sleep
  log and file reads; another server's music stopped on switching; the cover
  download no redirects; the TV's cookies its server's own. Both apps build;
  uploaded to TestFlight. Not checked: an invitation link in the simulator,
  the backup question on a phone.

**Three tests fail on main before any of this, on the Mac**:
`TestABackedUpPictureKeepsThePhonesDate`, `TestABackedUpVideoWithNoDateKeepsThePhonesDate`,
`TestAnOldBackupHealsOnTheNextCheck` ("no date beside it") - **for the PC:**
a time-zone dependence or a regression; `players.go` is also not gofmt'd.

**For the PC, from this pass** (each a reviewer's claim, checked where noted):
- *Android* - as the iPhone: a page's backup message must not change the
  destination (status writes it today, `MainActivity` 1042, `PhotoBackup`
  76-98) - a native question naming the host; backup never over plain http
  (`servers()` ends with the saved http address); `tvLink` ignores an unknown
  server or none (any app can fire `soundstorm://link`); "Set it up" never
  sends `?setup=` over plain http and checks the secure name before trusting
  a found box; replies read through a byte cap (discovery, check,
  installIdAt, backup check/errorOf, Uploads); from plain http a name
  resolving to the LAN IP is weak (any install can name any private IP) -
  require the same `/healthz` id too; below API 28 any app joins the media
  session; Shared copies have no byte cap.
- *Server* - photo import: `zip.OpenReader` reads the whole central directory
  before `plausible()` (a 78MB zip of 1M entries took 849MB; check the end
  record first, as `epub.Open`); iCloud CSV rows uncapped; `mp4hls.Open` (up
  to 256MB of index, ~1GB a open) has no slot or coalescing - 20 parallel
  `init.mp4` asks OOM; the photo limit is checked once per request against a
  cached figure with 64 backup slots (fill past it); photo sends are
  unrated and, across filesystems, full uncounted copies; rate tables
  (`allowance`) never pruned, keyed by address on open routes; side-effect
  GETs (`/api/hls`, `/api/music/beats`, `/api/voices/sample`,
  `/api/reset/summary`) can be fired from another install's page (refuse
  `Sec-Fetch-Site` same-site/cross-site on GET `/api/*`); the power-button
  reset takes the first private address to ask (a guest's laptop in the
  window, perhaps IPv6 through docker-proxy) and "once" races - want a code
  shown at the box and an atomic close; Jellyfin's HLS query a denylist
  (`LiveStreamId` passes); Plex's dial guard misses the host's other bridges
  (172.17.0.1); localbooks follows symlinks (a link to state.json would be
  served); Storyteller clips re-read per itemref; `mp3Pieces` reads a whole
  file; photo privacy exclusions fail open when Immich is briefly down;
  `BioURL` checked only in the client.
- *Names service* - the limit table can be filled by unauthenticated `find`
  and chosen-host keys from many /64s, then renewals 429 (separate table);
  `/v1/find` shows neighbours behind CGNAT; a released name could wait before
  being claimed again; `remoteNameIn` may pick the chosen home name as the
  remote one after a restart.
- *Web reader* (beneath the CSP, which held - no script ran): a part typed
  `text/xsl` or `unknown/unknown` skips the stripping (allowlist the types);
  CSS `@import "..."`, `image-set`, escapes and standalone `text/css` reach
  the server's own addresses; `rel="stylesheet dns-prefetch"` passes; the
  offline PDF blob should be `application/pdf` only; `?link=` raises "Sign in
  a TV?" from any link (show it only for a code typed or scanned here);
  `soundstorm-control` survives sign-out.
- *Installers* - Linux library 0777 with no sticky bit (another local account
  can swap a shelf for a link into a backend's read-write mount: make the
  root 1777); a Windows install dir outside the profile can be planted with
  `compose.yaml`/an override, or files pre-owned (lock the folder, pass `-f`);
  updates run unsigned code from main; images by tag; the backends' default
  secrets never generated; the firewall change opens every Docker port on
  Private; the Tailscale key in process arguments; Windows takes any UPnP
  answer with no gateway, and `$` in `.env` values is interpolated; a move
  folder's tars restored as they are; 8099 on 0.0.0.0.
- *Box* - the power-button reset as above; Erase leaves the system disk's
  logs, container layers, valkey's and tailscale's volumes and `.env`; no
  GRUB password or disk encryption; any USB filesystem parsed by the kernel
  unasked (limit to vfat/exfat/ext4); no `current.json` seeded at build (a
  fresh box accepts the oldest signed release) and no manifest expiry; a
  manifest need not name every service; the container can switch auto
  updates off and skew the health baseline; no PRODUCTION build guard
  (DEV_SSH, test channel, dev key); a single press powers off.

*(Server, done on the PC, 2026-10-08:* a photo download's end records are
read first (`photoimport.preflight`: the zip's own count and directory size,
zip64 too) and a zip past 500,000 entries or a 128MB directory refused before
archive/zip opens it; iCloud's CSVs capped at 256MB and two million rows; a
long book's pieces opened two at a time and once per book (`bookHLS.opening`,
`slots`); a GET under `/api/` from another site is refused (`sameOrigin`);
**the power-button reset needs the code on the box's own screen** - five
presses make a six-digit code (`presses.code`), shown on the screen plugged
into the box (`screen.sh`, from the caretaker's `GET /button`), never sent by
EmberStorm; the sign-in screen asks for it, the password is checked for
strength first, then `POST /button/claim` checks the code and closes the
window in one step, five wrong codes closing it too; Plex's dial guard refuses
172.16.0.0/12, Docker's own range (the host answers on each bridge's
gateway); local books skip links, a book or a cover; rate tables forget full
buckets past 1,024 and refuse newcomers past 100,000; the artist's Wikipedia
link is checked by the server (`wikipediaPage`); and **nothing goes into
somebody's own photo folder until the owner's photo library is known to
leave it out** (`library.BeforePersonal`, `provision.PhotoFolderPrivate`: a
sync is tried there and then, and the photos refused while it fails). Tests
for each. Not checked: the button code on a real box's screen. Then the rest
of the server list: a photo upload or backup holds its size against the
limit as it starts (`holdPhotoRoom`, check and hold in one step - 64 at once
against 1GB let exactly ten 100MB ones in); sending photos is limited (ten,
then one every 30 seconds) and a held copy, where a link cannot be made,
needs the disk's reserve; Storyteller's overlays are read once each, 128MB
and two million clips at most; `mp3Pieces` reads as it goes; Jellyfin's HLS
query loses any `liveStreamId`. Left: the HLS query as an allowlist, which
wants a live Jellyfin's playlists.)*

*(Web, done on the PC, 2026-10-08:* the reader gives a part that is not a
page its own type only from a short list (images, fonts, audio, video;
anything else is text); stylesheets - in a page, a `style` attribute or a
file of their own - are cleaned by `cleanCSS`: `url()`, `@import` and
`image-set()` keep only the book's own files (relative, or the `blob:` of
this page foliate has made of them) or an inline picture, and a stylesheet
spelling a reference with escapes is dropped whole
(`scripts/reader-css-check.js`, 12 cases); a `link` is kept only with `rel`
exactly `stylesheet`; a kept download keeps its type only from a list, and a
PDF opened offline is `application/pdf`; **a TV's code that came in a link -
`?link=` in the address, or the app opened by one - is not shown: the person
types the code their TV shows** (a code typed here, or read by the app's own
scanner, is asked as before); `soundstorm-control` is cleared on signing out.
**For the Mac:** the iPhone's own scanner should hand its code as
`window.__soundstormLink(code, 'scanned')`, and a code from a link (Universal
Link, `soundstorm://`) as it does now - then a scanned code needs no typing,
a sent one does. Android 0.50 does the same.)* *(Done on the Mac, 2026-10-08: the iPhone's
scanner hands its code with `"scanned"` (`handLink(_:scanned:)`), a link's
as before. Built; the TV-code link test still passes against the Mac's test
server, whose page is older.)*

*(Android 0.50, done on the PC, 2026-10-08:* where photos are backed up is
the phone's question: the page's backup messages no longer set it, and
turning backup on asks "Back up this phone's photos?" naming the server
whenever it is not the one already chosen; never to a plain http address;
a TV code from the app's own scanner is handed over as scanned, one from a
link is typed (the page above); a `soundstorm://link` naming no server is
refused; a found box gets its setup code in the address only at its secure
name, which must answer with the same install id (over plain http the
person types it on the page); the move from a plain address to a secure name
needs the same install id as well as the same address; every reply is read
to a limit (`ServerAddress.capped`); a shared file is copied no further
than the size it gave. Left: Android 8 (below API 28) cannot say which app
asks to control the music, so any may - raising the minimum to Android 9 is
the owner's call. Built; not run on a phone.)*

*(Names service, done on the PC, 2026-10-08:* what anybody may ask without
an install's key - finding, opening, signing up - is counted in a table of
its own (`Server.open`), so filling it cannot refuse an install's renewal; a
chosen name let go is kept a month for the install that let it go
(`released:<id>:<time>` in its claim record, `releasedBy`; the sweep keeps it
that month, then takes it), so a stranger cannot take a family's address the
day after a change; after a restart the remote name read from the
certificate is the code's own away name, never a chosen home name
(`remoteNameIn`). Left as it is: `/v1/find` shows the installs on one public
address, which behind a carrier's shared address can be a neighbour's - it
says only that one is there, and opening it still needs its sign-in.)*

*(Installers, done on the PC, 2026-10-08:* the Linux library is sticky
(1777, the box's too), so no other account can swap a shelf for a link; on
Windows every compose command names `docker-compose.yml` (`Add-ComposeFile`),
so an override or `compose.yaml` left in the folder is never read, and a
folder outside the user's own whose folder or kept files belong to another
account is refused (`Test-OwnedByMe`); a Tailscale key goes through the
environment, not the command line (`TS_AUTHKEY=... sh install.sh
--tailscale`; Windows hands it to its relaunch as `SOUNDSTORM_TS_KEY`), and
piped into sh the key is read from the terminal; Windows takes a UPnP answer
only from the known gateway; a `$` in a setting is written `$$`, which
compose reads as one (checked: "My$Music" came through whole, where a lone
`$` gave "My"). Looked at and left: a move folder's archives - restored by
busybox's tar inside a throwaway container, which already strips any `..`
and leading `/` and refuses a link pointing out (tried with crafted
archives); the firewall change opening Docker's other ports on Private
(Windows rules cannot block all but one port of a program cleanly); updates
unsigned, images by tag, the backends' default secrets - as before.)*

*(Box, done on the PC, 2026-10-08:* a reset takes the stack down rather
than stopping it (the containers' own logs go with them) and then runs
`forget.sh`: Tailscale's volume and any unnamed ones, the system's logs, and
on erase every `.env` setting but the setup code (the box's own written
again by `prepare.sh`); the power-button reset needs the code on the box's
screen (above); a USB drive is opened only if FAT, exFAT or ext (the
kernel's) or NTFS (by ntfs-3g, outside the kernel - added to the image),
not xfs, btrfs or HFS+; a release expires (90 days, signed again before
then), must name every service the box runs, and nothing not newer than the
box's own `SERIAL` is taken (`SOUNDSTORM_MIN_SERIAL`); Auto off puts an
update off for a month at most, by the owner or anything reaching the
socket; `PRODUCTION=1` builds refuse the development switches. Tests for the
caretaker's half. **Not checked: a box built and booted with these** -
`forget.sh`, ntfs-3g and the button code on a real screen want the VM, then
the test unit. Left: no GRUB password or disk encryption (somebody with the
box in their hands and a keyboard can boot it otherwise - a decision for the
product, as encryption needs a key somewhere); a single press powers off (by
design); the health baseline is what EmberStorm reports.)*

**A thirteenth pass, blind twice over (2026-10-08, on the PC):** even its
plan was made blind - an agent shown only the code split the project into
nine parts and chose what each should look for; nine fresh reviewers then
took one part each, none shown these notes or the history. Each finding was
checked against the code before anything changed. Fixed:

- **A member could become the owner through a shared TV** (high): once a
  free shared TV had switched to her, her own phone, signed in as the TV's
  person, could read the TV's commands - the switch code the owner's next
  send queued among them - and sign in as the owner with it. A player's
  commands, state and answers are only for the device that said hello as it
  (`playerDevice`, the profile cookie's hash against `KeyHash`), and a
  switch code only works from that device (`switchCode.keyHash`).
  `TestATVsCommandsAreOnlyTheTVs` fails without it.
- **A photo download that understated its entries took the server's memory**
  (high): archive/zip believes neither the zip's count nor its directory
  size and reads headers until one is not a header, so `preflight` now walks
  the headers itself, from both places archive/zip may start, as it would
  (`countHeaders`). `TestAZipUnderstatingItsEntriesIsRefused`.
- **The box:** the power button's code is never on the caretaker's socket
  (the app's container shares it) - only in a root-only file the box's
  screen reads (`ButtonCodeFile`); an update's health is held to no more
  sources than the box last saw installed healthy (`baseline.json`, 32 at
  most), so a compromised version cannot have every update rolled back; a
  rollback that did not work says so; the setup code is no longer written to
  `/etc/issue.d`.
- **Readers:** an .m4b whose samples claim more than the file holds, or
  whose time never moves, is refused (`mp4hls`); Make an ebook gathers at
  most 64MB a piece (`maxPiece`); a PDF's Info reference has ten digits at
  most and only the last match is kept (`lastMatch`); no folder an upload is
  written into may be a link (`placeFile`); tags cannot name a reserved or
  hidden folder (`tagSegment`); a box walk stops at 4,096.
- **The book reader:** `template`, `noscript`, `slot` and declarative shadow
  roots are dropped (the frame read them differently from the cleaning),
  every chapter is the cleaned document re-serialised, `ping` goes, and
  stylesheets are read as a browser tokenises them (`cssReferences`) after
  the quick patterns - `image-set("/api/x?)")`, `@import"..."` and quoted
  urls holding a ")" are caught (`scripts/reader-css-check.js`, 17 cases).
  Ids made of dots are escaped (`escapeId`), the player id and tuner go on
  signing out, the service worker caches no `/static/` address with a query.
- **Media and photos:** checks of what is already there compare at most 100
  files by contents and are rate limited; backup checks, starting an import,
  a read-along sync, an ebook, a voice sample and taking a send are limited;
  taking a send and saving a shared album hold their room as backups do; one
  person may have 20 sends waiting; video conversions are counted per video
  as well as per session (a reused or missing session id started one per
  video).
- **Adapters:** plex.tv's client follows no redirect (its token header went
  with one); a proxied `public` cache header becomes `private`; the book
  shelf skips anything not a plain file and a sidecar that is a link; photo
  privacy is not "known" while the photo library is configured but not set
  up.
- **Sign-in:** over TLS only the `__Host-` session cookie counts; the setup
  code approves a new device only from the home network, never the away name.
- **Names service:** a network's sign-up limits are counted before the day's
  (one client used up every new install's day); a name with a code is held
  whether or not it is on the lists, and a code has ten tries a day; chosen
  names are looked up at most 20 times an hour an install (`lookupAllowed`);
  an install moves only to `<id>.home.<a known zone>`; certificates are made
  only for names the local authority may sign; no UPnP answer is taken with
  the gateway unknown; defaults are emberstorm.app.
- **Installers:** Linux names its compose file too; the address check it
  writes quotes the folder (a `"` or `$(` in it ran every ten minutes).
- **Android 0.51:** backup goes only to the server said yes to and its away
  name, never another in the list; the network search and address checks
  carry no cookies (`WebCookies.NO_COOKIES`).
- A stray test file committed under `internal/httpapi/Personal/` is gone.

**For the Mac, from this pass (iPhone and Apple TV):**
- `PhotoBackup.swift` `candidates()` (around 512-525): only `backup.server`
  and its twin, https only - `typed` and `ServerAddress.saved` are added with
  no scheme check, so photos and the cookie can go over plain http to
  whatever answers at a LAN address elsewhere, or to another saved server.
- `ServerDiscovery.swift` `secureName(of:)` and `ConnectViewController`
  (`begin`, `pendingSetup`): a new box's setup code goes in the address only
  to its secure name, and only when its `/healthz` id matches the plain
  host's; over plain http let the person type it on the page (Android 0.50).
- `WebViewController.swift` `sameInstall` (around 773): from plain http also
  require `healthID(url) == healthID(current)`, not only a shared address;
  the Apple TV's move likewise when the current host is a plain address.
- The iPhone's own scanner hands its code as `__soundstormLink(code,
  'scanned')` (from the twelfth pass, if not done yet).

*(All done on the Mac, 2026-10-09:* backup's addresses are only the one it
was turned on for and its twin, https only; a found box's secure name is
taken only when its `/healthz` id is the plain address's, and the setup code
rides in the address only to an https server - over plain http the person
types it on the page; from plain http the page is followed only with the
same id at both and one machine (iPhone), and the Apple TV's move asks the
same id whatever the current address; the scanner's code is handed as
scanned. And the sign-in is carried between an install's home and away names
as Android 0.52 does (`mirrorCookies`: as each page finishes and before the
fall back, a cookie gone from one gone from the other too). Checked: both
apps build; the connect and TV-code tests pass, and the new-box test passed
against a brand-new server of today's code on port 8099 - found, the code
typed on the page, "tester's EmberStorm" (it had failed first only because
that address was already a saved server in the simulator). Not checked: the
sign-in carried across on a phone leaving its Wi-Fi.)*

**Left, for the owner to decide:**
- **Erase and Start over** need only the owner's password; a compromised
  app container can ask the caretaker for them, snapshots included. Asking
  for the power-button code too would make them need somebody at the box.
- **Updates install whatever is on main**, unsigned (both installers, and
  `latest` on every push) - signed tagged releases and branch protection
  before selling; images by moving tag; the backends' default secrets
  (Storyteller's key on the shared network) never generated.
- **On Linux the library's files are 0666** for every local account
  (members' private photos included); a shared group would close it.
- **This PC:** `soundstorm-backup.json` and
  `soundstorm-backup-before-reset.json` in the project folder hold every
  backend's password and live session tokens, readable by any account on
  the PC (H:\dev's permissions) - delete or move them.
- A Windows install folder outside the profile can still be swapped by
  another account before `-Launch` runs; the names service's records can be
  filled from a few networks (per-network claim limits, the PSL); `/open`
  sends a browser to any install-shaped name; a TV's code sent in a message
  needs the person to type the TV's code, which the server cannot enforce;
  the Jellyfin HLS query is still a denylist; ext and NTFS drives are parsed
  as root; Android 8 media controllers; the debug signing key.

**A network for each backend, and every backend pinned** (2026-10-08, the
owner's asking, after "what if Jellyfin had a security hole?"). The
backends shared one network, so one taken over through a crafted file or a
bug of its own could reach the rest - their admin pages, Storyteller's fixed
key, AudioMuse. Now each has a network of its own with SoundStorm alone
(AudioMuse also has Navidrome's, to read songs through it), and only
SoundStorm and Tailscale's sidecar are on the default one; the two database
networks stay as they were. Checked on the live server: SoundStorm reached
all eight backends, Jellyfin found none of the others by name, AudioMuse
found Navidrome and not Jellyfin, all nine sources up, the after-deploy check
passed. **Every image is pinned by digest** (the version beside it in a
comment; only SoundStorm's own stays a tag), so a backend's new release -
broken, or a registry taken over - reaches nobody until it is tried here
and its digest put in `docker-compose.yml`, which updates fetch: pull the
tag, try it, take `docker image inspect -f '{{index .RepoDigests 0}}'` (the
multi-processor index's digest - checked, amd64 and arm64 in each). The
test server takes its images from the compose file (`pinned` in
`scripts/smoke-stack.sh`). **And it took the house off the internet the next morning** (2026-10-09, the
owner's phone timed out away from home): eight more networks pushed this
PC's Docker past its fifteen /16s, and it gave one (`voices`)
192.168.0.0/20 with gateway 192.168.0.1 - the home router's own address.
SoundStorm's NAT-PMP, PCP and UPnP then went to Docker, not the router; the
router's UPnP lease on 8099 ran out within the hour and the away name was
refused from outside. Every backend network now has a fixed /24 in
10.231.0.0/16 (no home router's, no Docker default), and they are named
`<backend>-net`: a network whose addresses change is rebuilt by Compose with
its containers reattached **without their service names** (every backend
then "no such host" - seen here), while a new name makes Compose recreate the
containers properly. The old networks are left unused on installs that had
them. Checked: subnets all 10.231, all eight backends answering by name,
Jellyfin finding no other, "opened the port on the router" (UPnP) and the
away name answering from outside, the after-deploy check passed. Watch for it:
**any new compose network needs a fixed subnet.**
Not done: the backends' way out to the internet
(Jellyfin's film lookups and the models fetched on first use need it),
Audiobookshelf's audiobooks folder still writable, our own copies of the
images.

**A review for bugs across the whole project (2026-10-09, on the PC)**:
nine reviewers, one per part, every finding checked against the code before
changing. Fixed:

- **Server:** idle video conversions were never stopped (the idle check read
  the session key wrongly); one refused answer from the names service forgot
  the install's name (now only after 72 hours of refusals, and the old
  registration kept aside); a backend set up afresh kept its members' old
  accounts; deleting a photo took every photo sharing its name's start
  (companions on the picture shelf are only its side files); undo across
  drives; sign-ins in use now renew instead of ending after 30 days; "Part 1"
  and "Part 2" films no longer made one film; Storyteller signs in again with
  its kept password; a backend's library listing that failed no longer makes
  a second library; AudioMuse's last good answer is used for a minute after
  it fails; a rescan has ten minutes, not thirty seconds; a play after a
  crash mid-write is kept in the listening history; a held sign-in is
  refused once the password has changed (`TestAHeldSignInDiesWithItsPassword`);
  two taps on a voice sample no longer mix two files.
- **Web:** the remote-access card was hidden by two functions of one name
  (`refreshPlayerRemote`); closing the review sheet no longer blocks every
  later upload; files the phone app holds are never sent by the page as
  text; a retry starts the right file at the right place; a show's page is
  not pulled back by a late refresh; the last film's audio picker hidden;
  "end of this chapter" stops at the chapter, not the file's end; reading in
  the line views saved on closing.
- **Android 0.55:** a book's place saved only once the resume has landed
  (0.54 could save 0:00); a server error no longer counts photos as backed
  up; the sleep timer while loading; lock-screen skips with the page gone;
  uploads kept to their own server and sent once.
- **Box and installers:** container logs capped (10MB, three files); a port
  asked for kept; the setup code shown after an interrupted first install; a
  custom install folder found by its saved script; a library folder whose
  name Docker's settings would cut refused.

**Then every fix checked again** (the owner's asking, the same day: four
fresh reviewers, each fix asked whether it closed its bug and what it broke).
What that found, and was put right:
- **The worst came from a fix**: a backend set up again forgot every member's
  account on it - right for one whose data was reset, wrong for one that only
  refused its key once (an upgrade's 404 counts), when the accounts were still
  there and could never be made again (name taken, password forgotten).
  `SetBackend` now keeps each member's password as an unfinished setup's, so
  the account is signed in to if still there and made again if not, and a
  photo account signed in to again keeps its library (`existingImmichLibrary`;
  a second one had doubled every face). `TestAPhotoAccountSignedInAgainKeepsItsLibrary`.
- The installer's "setup code after an interrupted install" never matched:
  the server indents its JSON (`"hasAccount": false`); it asks the address the
  health check asks now.
- Ripped "Part 1"/"Part 2" films were still joined: a plain "Part N" never
  stacks now, ripped or not (PT., CD, Disc still do).
- A renewed sign-in could still end on its old day in the apps (the renewal's
  cookie answered to a player that drops it): `/api/session` sends the cookie
  again every time (`RefreshCookie`).
- The name service's three days of refusals restarted at every restart: kept
  on disk (`name-refused.txt`).
- A photo took another photo's date file with it (`IMG_0001.JPG.xmp` beside
  `IMG_0001.HEIC`): only its own now.
- AudioMuse made everybody wait on a fresh read with an old copy in hand: the
  old copy is answered and the read runs behind.
- Putting back across drives left the bin's copy.
- Web: the add-files screen's close cancels a review while earlier files go up,
  and a drop closed while still being planned; the audio picker cleared at
  every film (Up next and downloaded episodes kept the last one's); the
  chapter sleep timer worked out again on a seek, a new book or speed, and the
  phone app told the moment again (and nothing while paused); late refreshes
  not run behind Settings; the reader closed once at a time, never a book
  opened meanwhile.
- **Android 0.56**: "previous" with Android's page gone sent a book to its
  start (and saved that as its place) - thirty seconds now; a new photo
  arriving during a long backup left nothing watching for the next one.

**And checked a third time** (the owner's asking: everything the review
changed, as it stands, three fresh reviewers). Every original bug held fixed;
put right: a photo account signed in to again made a second library when the
listing did not answer (now an error, tried later); a retry after a dropped
connection started at the file's old place after Play from the beginning
(`audio.startSeq`); the chapter sleep timer, stopped by the phone app's own
player with the screen off, was set again for the next chapter when the page
woke (a pause no longer works it out again, and a chapter end gone by ends
the timer); a book opened while the last one closed waits for the close
(`closing` in reader.js), which had saved the old place into it. **Android
0.57**: a page made again takes a paused book over without forgetting where
its place is saved; a new-photo job retried added a link to its chain each
time. Left: a member not yet back in Photos after the photo library is set
up again is missing from an album's people until they open it (no
`RemoteID`); Immich keeps the API keys of accounts made again; on the box,
`systemctl cat docker` in the VM to see Debian's unit passes no log options
that `daemon.json` would clash with.

**Left from this review** (smaller or wanting a decision): keep-both
subtitles not following the renamed video; a late second version's
subtitles going to extras; Replace binning the old file before the new one
is checked; renumbering with no rollback if a move fails half way; the names
service's claim race and a lost claim blocking renewals; restore and
reset-password run while the server is up; photo room not held for home
videos and imports; the web's upload progress from the app overwriting the
next review; Back and the TV remote in the newer sheets; queue edits not
sent to a controlled TV; the hls.js first-load race; object URLs never
revoked; uninstall's library path, and `%` in the address check's cron line.

**For the Mac, from this review (iPhone and Apple TV):**
- Apple TV `moveServer`: copies cookies by name, but over TLS the session is
  `__Host-soundstorm_session` - copy both names.
- iPhone `PhotoBackup`: an export error stays shown after later runs work;
  clear it when a run gets through.
- iPhone `FileUploads`: the server is the app's current one, not each job's
  own (a server switched mid-upload sends the rest there); a resumed session
  can hand a file over twice.
- Apple TV `VideoPlayer`: Up next saves the next episode as finished.
- Apple TV `Player`: a book's place can be saved as 0 before its resume
  seek lands (Android 0.54 had the same; send the place only once settled).
- iPhone `VolumeKeys`: stays on after the page reloads while controlling a
  TV; turn it off when the page goes.
- iPhone background task: not scheduled again after it runs.
- iPhone backup queue: copies whose upload finished while the app was
  closed are left on disk; restoring the sessions' tasks races a run
  starting at launch.

## Tailscale, and why it is a profile rather than a service

Reaching SoundStorm away from home is the one thing the LAN address cannot do.
`docker compose --profile tailscale up -d` runs a Tailscale sidecar that puts
it on a tailnet at `https://<hostname>.<tailnet>.ts.net`. `--tailscale` in
both installers writes the auth key into `.env`, generates the serve config
and turns the profile on.

It used to be sold as the way to get a real certificate as well, and for a
while it was the only one. Auto mode does that now, at home, with no account;
Tailscale's job is **away from home**, and nothing else. The name service
deliberately names only private addresses, so auto mode cannot do this part.
Even once SoundStorm can point a name at a home's public address (roadmap),
Tailscale stays: it works behind carrier-grade NAT, where there is no port to
forward, and for anybody who would rather not put a server on the internet.

**It is opt-in, and that is the Plex decision applied again.** Plex was
rejected because it "cannot be provisioned without a human logging into a
cloud service - fatal to the zero-keys claim". Tailscale has exactly that
property: an account, an auth key, and the app on every client device, none of
which can be automated. The difference is that this is remote access rather
than a backend - SoundStorm is fully working without it and nobody is ever
walked through a signup they did not ask for. Make it default and the claim
stops being true.

Three things learned by running it rather than reasoning about it:

- **Userspace networking is the default**, so the sidecar needs no `NET_ADMIN`
  and no `/dev/net/tun`. It stays an ordinary unprivileged container, which
  matters on Docker Desktop.
- **The proxy target must not be called `soundstorm`.** The sidecar takes that
  as its *tailnet* hostname - it is the address people type - and Docker writes
  a container's own hostname into its `/etc/hosts`. So inside the sidecar,
  `soundstorm` resolves to itself, the proxy loops back, and the tailnet
  address answers 502 while every container reports healthy. The compose file
  gives SoundStorm a second network alias, `soundstorm-app`, and the serve
  config points at that. A CI step guards it.
- **The scheme in the serve config is not a constant.** Tailscale reaches
  SoundStorm over the compose network, where it speaks plain HTTP or its own
  self-signed HTTPS depending on `-Https`. Both installers write
  `https+insecure://` or `http://` to match; the wrong one is another silent
  502. `https+insecure` is Tailscale's documented pseudo-scheme for a
  certificate nothing can validate.

`tailscale-serve.json` is written on every install whether or not Tailscale is
wanted, because compose bind-mounts it - and Docker's answer to a bind mount
whose source is missing is to create a *directory* with that name, after which
the container fails in a way that reads like a Tailscale problem.

**Offered where it is needed, not asked during install.** Asking every install
"do you want Tailscale?" would stall the people who say yes (an account, a key,
an app on every device) and is a milder form of the sign-up walk this section
rules out. So it surfaces at the two moments it matters:

- **When remote access cannot work.** The router's own WAN address says so:
  in `100.64.0.0/10` it is carrier-grade NAT, where no forward can ever be
  reached. In a private range it is double NAT, where a forward is needed on
  both routers. `portmap.Maintainer.ExternalAddress` asks for it
  even when the mapping *succeeded*, because a router behind CGNAT opens the
  port without complaint, on an address the internet cannot reach.
  `ClassifyWAN` judges it, the account panel then says what is going on instead
  of "forward port 8099", and it points at Tailscale. A router that answers
  nothing leaves the ordinary advice in place.
- **For anybody who would rather not be on the internet.** A standing line
  under the remote switch.

Setting it up has a Start menu shortcut, **Set up Tailscale** (`-Tailscale`).
It opens a window with the steps, a button to Tailscale's key page, and a key
box that refuses anything not shaped like `tskey-...` while the page is still
open to copy from. Canceling is not an error: the update carries on without
it. Before this, Tailscale was the one feature that could only be reached by
typing an option.

**Unverified:** that a real auth key produces a working `ts.net` address. That
needs a Tailscale account, which is the user's to create. What was checked is
everything up to it: the profile stays off by default, the config mounts as a
file with `${TS_CERT_DOMAIN}` intact, the sidecar starts, and it can fetch
`/healthz` from the proxy target it will actually use.

## TLS without anybody running openssl

`internal/servetls`. A home server has no domain name and no route an ACME
challenge can reach, so the certificate is made locally - but as a local
*authority* rather than a bare self-signed certificate. Install `/ca.crt` once
per device and everything SoundStorm issues afterwards is trusted, including
certificates for addresses it had never seen. A self-signed leaf would have to
be re-trusted on every renewal and every new address.

**A hostname comes from the handshake; a bare IP has to be configured.** SNI
carries a hostname, so those are minted on demand and need no setup. An IP does
not, because browsers send no SNI for one.

`ClientHelloInfo.Conn.LocalAddr()` looks like the answer and is not, and this
shipped wrong first. Docker NATs the published port, so inside the container
the local address is the container's own `172.20.0.5`, never the
`192.168.0.50` the client dialed. That reads correctly in a unit test with a
synthetic connection, and correctly for a binary run directly on the host -
the only two ways it had been tested - and never once in the way it actually
ships. It was caught by asking a client that trusts only the published
authority to fetch `https://192.168.0.50:8099`, which is the exact thing the
feature exists to make work.

So `SOUNDSTORM_TLS_HOSTS` is **required** to reach it by IP, and the installer
writes the machine's LAN address into `.env` at install time - whether or not
TLS is on, because by the time somebody turns it on there is nothing left that
knows the answer.

The SANs are exactly what was configured, plus localhost - not every address
the machine has. An early version enumerated the machine's Tailscale address,
eight IPv6 prefixes and its Windows hostname to anybody who opened a
connection, which is a poor trade for saving somebody one setting.

Off by default: on localhost there is nothing on the wire to protect and a
certificate warning is a poor first screen. An unknown `SOUNDSTORM_TLS` value
is **fatal** - quietly serving plain HTTP to somebody who asked for encryption
is the worst available way to be wrong.

**The server certificate is persisted, not just the authority.** It was not,
and that was not cosmetic. Most devices never install the authority; what
people do instead is click through the browser's warning once, and a browser
pins that exception to the exact certificate it saw. Minting a fresh one on
every start revoked it on every restart - and with a service worker holding
the shell in cache the symptom was not a warning at all but a spinner that
never stopped, because the cached page kept loading while every request behind
it failed TLS. `server.pem` and `server-key.pem` sit beside the authority now,
reissued only near expiry or when `SOUNDSTORM_TLS_HOSTS` changes.

**Persisting it only made the change rarer, and the rare case was a dead end.**
A certificate still changes on a reinstall (`down -v` takes the state volume
and the authority with it) and at the yearly renewal, and every browser that
clicked through is then refused. The service worker used to answer a failed
page load with its cached shell, so what those browsers showed was not the
warning but "cannot reach SoundStorm" - whose advice, "open it in a new tab and
accept the warning", could not be followed, because the worker answered the new
tab too. Found by resetting a real install. Now the worker never answers a page
load at all, and the browser's own warning comes through.

Verified in Chrome, both ways: with the server stopped, the old worker served a
page titled "SoundStorm", and the new one lets `net::ERR_*` through. A string
test in `pwa_test.go` holds the line; the browser run is what showed it
matters. Browsers already holding the old worker stay stuck until they get past
the warning once - clearing the site's data, or opening any `/api/` address,
which the worker never touched.

**Nothing in the UI may assume `fetch` resolves.** `api()` had no catch, so a
failed TLS handshake, a stopped server or a dropped connection rejected
straight through every caller - and `boot()` checked `ok` but never the
rejection, leaving the spinner turning with "Failed to fetch" in a console
nobody opens. It now returns `{ok: false, offline: true}` and the boot screen
says so. It no longer leads with the certificate: the page itself loaded, so
the certificate was fine a moment ago, and "try again" - a reload, which the
service worker leaves alone - is what brings the browser's warning up if it
has changed since. The text warns that the warning is coming.

`SOUNDSTORM_TRUST_PROXY` lets `X-Forwarded-Proto` decide whether the session
cookie is Secure, for a reverse proxy that terminates TLS. Off unless asked
for, because any client can send that header.

## Real certificates, and the one service SoundStorm runs

The local authority works and costs a warning on every device, and the only
thing that removes the warning is a certificate somebody on the internet
vouches for. That needs a name on the internet. `SOUNDSTORM_TLS=auto` gets
one: `internal/names` is a small service, deployed from
`cmd/soundstorm-names`, that gives each install `<id>.home.soundstorm.dev`,
points it at the install's LAN address, and publishes the DNS-01 challenge
that lets Let's Encrypt issue for a server nothing on the internet can reach.
This is what Plex does with `plex.direct`, without the account.

It is the first piece of central infrastructure the project has, which is a
real change and was decided deliberately. Its shape is what keeps that from
becoming the trap the rest of this file warns about:

- **It never carries media.** A relay's cost grows with every film watched;
  DNS records and a challenge every couple of months do not. Roughly $5 a
  month on Railway plus the domain, however many installs. Do not add
  anything that puts it in the data path.
- **It holds no state.** A token is an HMAC of the install's id; Porkbun's
  DNS is the only record of anything. So once a name resolves it resolves
  without the service, and an outage stops registration and renewal, never a
  lookup - and renewal starts with a third of the certificate's life left,
  which is a month of slack.
- **It only names private addresses.** A trusted certificate on a public
  address is a phishing kit with our name on it. It also only publishes values
  shaped like an ACME challenge, and caps challenges per install and overall,
  because every certificate under the zone spends the domain's weekly Let's
  Encrypt allowance until the zone is on the Public Suffix List.
- **Every failure falls back to how things were.** Until the certificate
  arrives, or if it never can, the local authority serves exactly as in
  self-signed mode.

**Why Porkbun's API and not a DNS server of our own.** The first design had
the service answer DNS itself, reading the address out of the name
(`192-168-0-50.<id>...`). Railway cannot host that - it offers no UDP and no
port 53 - and it would have made every lookup depend on our uptime. Writing
ordinary records through the registrar's API is less clever and strictly more
robust.

**The ACME client is ours** (`internal/acme`), for the zero-dependency rule:
RFC 8555 for one account, one name, dns-01 and ES256 is a few hundred lines.
It is believed because of `scripts/acme-rehearsal.sh`, which runs it against
Pebble - Let's Encrypt's own test authority - with pebble-challtestsrv standing
in for Porkbun, in CI. A fake authority written beside the client would share
its mistakes; Pebble does not. It found one thing: at a 50% nonce refusal rate
five retries failed a run in six, so it is ten.

**One port speaks both protocols** (`servetls.Listener`). The address people
already have is `http://localhost:8099`; the certificate is for
`https://<name>:8099`; the published port lives in compose, so it has to be
the same one. A TLS connection opens with byte 0x16 and no HTTP request does.
The listener hands net/http a real `*tls.Conn`, not a wrapper, because that
type check is what fills in `r.TLS`, and `r.TLS` is what makes the session
cookie Secure. HTTP/2 still negotiates: `Serve` configures it when
`srv.TLSConfig` is set, which a test asserts.

**The page moves itself, and only after checking.** `/api/session` offers
`secureName` to a page not already on it; the page fetches
`https://<name>:<port>/healthz` in `no-cors` mode - it resolves only if the
name resolved, the connection opened and the certificate verified - and then
`location.replace`s. Plenty of routers refuse to resolve a public name that
points at a private address (DNS rebinding protection), and a redirect into
that failure would be worse than staying on http. The CSP's `connect-src`
admits the name for exactly this, or the check is refused before it leaves.
Verified in Chrome against the whole system in containers: with the name
resolvable it moved and the app loaded; without, it stayed on http.

**Verified live on 2026-09-23**, Railway, Porkbun and Let's Encrypt all real:
an install registered `k3x9m2p7qa.home.soundstorm.dev`, got a staging
certificate in 13 seconds, then a production one after the switch, and an
ordinary client with the system trust store fetched it by name. The home
router resolved the name, so the rebinding fallback was not exercised there.
The first real run found three things the Pebble rehearsal could not:

- **Compose passes settings by name.** The two new variables were missing
  from `docker-compose.yml`, so staging could not have been selected at all.
  Any new `SOUNDSTORM_` setting has to be added there too.
- **"Service busy; retry later" is typed `rateLimited`** and sent as a 503
  when Let's Encrypt sheds load. Taken for a real limit, it silenced the
  install for a day. Only a 429 is a limit.
- **A push to main redeploys the Railway service**, and a request in flight
  gets a bare 502 from Railway's proxy. The install retries in five minutes,
  which is correct, but do not test right after pushing. Railway's own
  certificate for the custom domain also lagged its "active" badge by about
  half a minute.

Changing the ACME directory now forces a new certificate: the issuer is
recorded beside it (`public-issuer.txt`), because otherwise a staging
certificate survived the switch to production until renewal.

`X-Real-IP` is the right client-address header on Railway - measured through
`/v1/whoami`, not taken from Railway's docs, which contradicted themselves: it
carries the real address and a forged one is overwritten. `X-Forwarded-For`
arrives as `<client>, <Railway edge>`, and the service reads the last entry,
so that header would have put every install behind one shared limit.

**A restart must not forget the remote name.** Keeping the remote name
through a name-service outage relied on the name held in memory, which a
restart empties - so when the DNS provider answered 502 just as the server
came back up, the name was dropped and the certificate reissued without it.
Phones away from home then got the local authority's certificate for
`<id>.net...` and refused it, until the next half-day check. After a restart
the remote name is now read from the certificate on disk, and an unanswered
check is retried after five minutes rather than twelve hours.

**A working away name is not dropped on one failed check** (2026-10-10, a
deploy: the check 17 seconds after the restart, the router's port just opened,
came back 424, and the away name left the certificate until the next half-day
check - phones away from home lost the server). Found unreachable while it had
been working, the name is kept and checked again five minutes on, three times
(`unreachableRetries`, `takeUnreachableTry`), before it is dropped; a success
starts the count afresh. `TestAnUnreachableNameServiceKeepsTheRemoteName`.

**Renewals say what they replace (ACME Renewal Information, RFC 9773;
2026-10-03).** Let's Encrypt's 50 certificates a week per registered domain
counts ordinary renewals too ("ARI renewals" alone are exempt), and every
install shares soundstorm.dev: renewing every two months, the whole domain
would have topped out around 430 installs. So `acme.Client.RenewalWindow` asks
the authority when to renew (its directory's `renewalInfo`, by `CertID`: the
authority key id and serial, base64url, checked against the RFC's own
example), the install renews at a moment in that window chosen from the
certificate's id (spread out, the same each time; asked again as Retry-After
says), and the new order carries `replaces` - refused, it is asked again
without. With no answer, a third of the life left still decides, and a week
left always does. Only new installs now count against the 50 a week. Checked:
the unit tests and a stand-in authority's window; the Pebble rehearsal in CI
asks for a window and renews with `replaces`. **The Public Suffix List** was
looked at the same day and is not for now: it declines projects "serving fewer
than thousands of users", refuses rate limits as a reason ("we do not accept
entries whose sole purpose is to circumvent rate limits"), and wants the
domain registered more than two years ahead (soundstorm.dev ran to 2027-08-30).
When sales near 40 new boxes a week, Let's Encrypt's rate limit adjustment
form (weeks); the PSL once there are thousands, led by keeping installs'
cookies and sign-ins apart. An override (1,000 new certificates a week) was asked of Let's Encrypt; until one is granted, past 50 new installs a week the rest serve the local
certificate until the allowance refills. And the 2,500-record ceiling below means moving
the zone's DNS (not the registration) to a host with more, around 1,500.

**Porkbun's limits, from its OpenAPI spec rather than its docs page, which
states none:** a general budget of 20 requests per 2 seconds per key, measured
but not yet enforced; and **2,500 records per domain**, which is the ceiling
that matters. Each install holds one record for good, so `soundstorm.dev`
tops out around 2,400 installs in use - before Let's Encrypt's limits do.

**Abandoned installs are swept, and nothing is lost by it.** The service has
no state, so the date an install was last heard from lives in Porkbun's notes
field on its own record, refreshed monthly by the twice-daily re-announce.
Records silent for 180 days are deleted daily. The registration is not a
record - it is an id and an HMAC, valid for ever - so a server switched back
on after a year recreates its record, same name, on its first announce. The
sweep only matches install-shaped names, skips undated records, and refuses
outright if it would delete more than a quarter of installs at once: a wrong
clock or a misread stamp would otherwise cost every name together, and a
refusal is cheap to investigate. The re-announce on every
start used to cost a delete of the other address family as well as the
lookup; it now deletes only when the address actually changed, which halves
the calls of the common case.

## Finding your server from soundstorm.dev (2026-10-06)

The owner's report: an address starting with a random code is hard to get back
to on a computer with no bookmark. Two ways, built in this order:

- **Open my SoundStorm**, a button at the top of soundstorm.dev (`web/`). An
  install that allows it (Settings, Devices, Your web address: "Open from
  soundstorm.dev at home", on unless turned off, `state.NotFindable`) sends its
  port and `find` with every address announcement (`names.Client.Announce`);
  the names service notes which internet connection it came from - **in memory
  only** (`internal/names/find.go`, `findIndex`, believed 72 hours), a cache not
  a record, refilled by the twice-daily announcements after a restart - and
  `GET /v1/find` (CORS for the site's origins alone, `NAMES_SITE_ORIGINS`) answers
  a browser with the installs on its own connection, never one it names. One:
  opened at once; several (a shared carrier-grade NAT, or two servers): listed
  by their LAN address; none: type the web address or the code. **No quiet
  check of which answers** was the first plan and was dropped: Chrome now asks
  "access devices on your local network?" for a public page reaching a home
  address, which a navigation does not trigger. The service is IPv4 only
  (Railway gives no AAAA), so a browser and its server are seen on the same
  kind of address; if it gains IPv6, revisit. A server behind a VPN announces
  from the VPN, and is not found.
- **A chosen web address**, hollberg.soundstorm.dev (same card): claimed at the
  service (`PUT /v1/name`, `DELETE /v1/name`; `names.NameStatus`: 3-30 letters,
  numbers and dashes, a list kept for SoundStorm itself, nothing shaped like a
  code), kept as a TXT record `hollberg.claim` holding the install's id - still
  no database - first come first served, six changes a day an install. The
  name points at the names service, whose page there (`handleChosen`) sends a
  visitor to the home name from the server's own connection, the remote name
  from elsewhere when remote access is on, and otherwise says it is at home
  only (after a restart, before any announcement: asks "at home or away?"). No
  name is added to the server's certificate, so it costs nothing against Let's
  Encrypt. `PUT /api/settings/web-name`, `/api/settings/findable` (owner, and
  only in auto HTTPS, `webNamesAvailable`).

Checked: the names service's tests (`find_test.go`: found from its own
connection only, gone when turned off; a name claimed, refused to another,
freed by a move; home, away with and without remote access, unknown), the API
(`TestTheOwnerChoosesAWebAddress`), the site's button in Chrome with stand-in
answers (one, two, none, a name and a code typed), the card at phone size.
**On a phone, Open my SoundStorm opens the app** (2026-10-07, the owner's
asking): a server found on a phone is an **Open SoundStorm** button (a phone
opens an app only from a tap), its link
`https://names.soundstorm.dev/open?to=<the server>`, which the apps claim:
Android 0.43 (an App Link on the names host, path `/open`; the names service
serves assetlinks.json there too) opens a server it knows by address or
install id, and puts one it does not into the Connect screen, never opening a
new server by itself. Without the app the names service sends the browser on
(`handleOpen`: only an install's own home or away name, a 302 to its root). A
typed address on a phone goes the same way; a chosen name (hollberg...) goes
straight to it. A computer opens the server as before. Checked: the redirect's
rules (`TestOpenSendsTheBrowserOnToTheServer`), the website as a Pixel 7 and
a computer with a pretend server found. Not checked: a phone with the app.
**For the Mac:** the iPhone app wants the same - `applinks:names.soundstorm.dev`
in `SoundStorm.entitlements` (the names service's apple-app-site-association
now lists `/open`, on every host it serves), and `/open?to=` handled as
Android's: a known server opened, an unknown one offered on the connect
screen. The iPhone gets the button now, and without that change it simply goes
on to the server in Safari. *(Done on the Mac, 2026-10-07:
`applinks:names.soundstorm.dev` in the entitlements - Apple's cache already
had `/open` - and `TVLink.open` as Android's: a known server opened, an
unknown one written in the connect screen's address box, never opened by
itself. Checked in the simulator: a link to a server the app did not know
showed the connect screen with its address filled in. Not tried: a known
server's link, and a real phone's tap on the website.)*
**Away from home there is nothing to find, but the app knows its server**:
on a phone, the "none found" answer also offers **Open the SoundStorm app**,
`names.soundstorm.dev/open` with no server named - the app opens its own
saved server (Android 0.44); without the app the names service sends the
browser back to `soundstorm.dev/#getapp`: "The SoundStorm app is not on
this phone yet", **Coming soon to Google Play** or **to the App Store** (the
owner's choice while neither store has it - change these to the store links
once listed), and "Or use it in your browser: type its address". On a phone
the typing box is behind that small link, the app being the way in; on a
computer it shows at once. Checked as an Android phone, an iPhone and a
computer. **Then one tap** (the owner: "still doesn't just take you straight
to the app"): on a phone the header's Open my SoundStorm is itself the link
to `names.soundstorm.dev/open`, so a phone with the app opens it at once on
its server, no box first. Without the app the browser comes back
(`#getapp`) and the server is looked for, nothing going by way of the app
again: found at home, it opens in the browser; away, where to get the app,
and the browser as the other way. A computer looks at once, as before. **And the finding list survives the service restarting** (found
the same day: every push redeploys the names service, which forgot every
install until its next address announcement, half a day off - the owner's
first try found nothing even from home): installs say they are still here
every quarter hour (`PUT /v1/here`, `names.Client.Here`, `keepFindable` in
servetls), no DNS record touched, twelve an hour allowed per install.
`TestStillHereRefillsTheFinding`. **For the Mac:** the iPhone's `/open`
handling should take no `to` as its saved server, as Android's does. *(Done on the Mac, 2026-10-07:
`TVLink.open` - no `to`, or an empty one, is the saved server. Checked in the
simulator: from the connect screen, a bare `/open` opened the saved server's
page.)*
**For chosen names to resolve, the owner's to do:** a wildcard custom domain
`*.soundstorm.dev` on the Railway service, and at Porkbun a `*` CNAME to it (in
place of the parking catch-all the website steps already delete); home, net,
names, www and the apex keep their own records. And the website must be live
for the button.

**A chosen name became the server's own address** (2026-10-08, the owner
expected the code to be replaced by it): claiming makes `<name>.home` and
`<name>.net` CNAMEs to the install's code names (`pointChosen`, taken down on
release), the install certifies them beside the code's (challenges carry
`name`, checked against the claim; the per-install challenge allowance went to
20 a day), and `autoCert.name`/`remoteNameNow` answer them once covered - so
the page moves to yourname.home.emberstorm.app:8099, and invites, QR codes and
Reach it from anywhere use them. The code's names stay underneath, never
shown: the install's identity, and every older bookmark. An install claims its
own name again on start to put the CNAMEs back (no code needed for a name it
holds). yourname.emberstorm.app leads to the chosen names, Open my EmberStorm
finds them (`Here`/`Announce` carry the name, checked), `/open` takes them,
`/healthz` carries `id`, and Android 0.46 follows the page from one of the
install's names to another only when both answer with that id. Checked live:
the owner's server certified k3x9m2p7qa.home/.net and yourname.home/.net, both
chosen names answer, and its session offers yourname.home as the secure name.
**For the Mac:** the iPhone's `WebViewController` move rule (`sameInstall`)
must allow a different label the same way - fetch `/healthz` on both and
compare `id` - or the move to yourname.home is refused as a link out. *(Done
on the Mac, 2026-10-08: a different label is followed when both addresses'
`/healthz` give the same `id`; the same label in either zone is the same
install. Checked: the owner's k3x9m2p7qa.home and yourname.home both answer
id k3x9m2p7qa. Not seen: the app following the move itself.)*

**Single first and last names are held back** (the owner's call, the same
day: nobody gets their perfect name, since in time it would be taken anyway;
combinations like thehollbergs stay open). `internal/names/held.go` embeds
`held-names.txt.gz` - 250,324 names, the 2010 Census surnames (100+ people)
and the SSA's baby names since 1880, lowercased letters only - plus
`extraHeld` (hollberg, sphere: hollberg is too rare for the Census list), the
reserved words, anything containing "soundstorm" and anything shaped like a
code. **Asking for one says only "That name isn't available. Try another."**,
the same 409 as a taken name - never why (the owner's asking); `NameStatus`
now checks only how a name is written. **The owner can give one out**:
`NAMES_HELD_CODES` on the names service, `name=code,name=code` (secret, on
Railway only), and the person types the code under "I was given a code for a
name" in Your web address; compared in constant time, capitals, spaces and
dashes set aside (`plainCode`), as the setup code is. Checked:
`TestAChosenNameLeadsToItsServer` (smith, maria, hollberg, sphere,
my-soundstorm, www and a code refused alike; maria taken with its code and
not with a wrong one). A name nobody has opens "This SoundStorm is
unavailable", never "nobody has chosen it" - which would say the name was
free to take when it may be held. Not checked: the card's code field in a browser.

## The folders, and the line that replaced the box

Installing SoundStorm creates `library/` with five subfolders, and they are
still the only part of SoundStorm a user interacts with that has no UI - so
they are created for you and named for what people call the thing ("movies",
not "video").

**Nothing on the first screen is a box any more.** That screen has now been
three things: five folder rows with paths and example filenames, then one
centered card asking for files, then nothing at all. Each version was smaller
than the last and each removal was right, because dropping works anywhere on
the window - so the screen's whole job is to get out of the way and show the
library.

What is left is one line of muted text under the filter chips carrying the
three things with nowhere else to live:

- **choose files**, because a phone cannot drag anything and this is still the
  app that wants files. On a coarse pointer the sentence changes from "drag
  files anywhere" to "add music, films, audiobooks or ebooks:" - telling a
  touch device to drag is an instruction it cannot follow, and it was the
  first line of the app.
- **check for new files**, for media that arrived some other way.
- **the address for another device**, which is the question people ask most.
  Also in the Account panel, which is where somebody looks for it later.

It has no border and no background on purpose. The moment it has a box around
it, it is the box again.

**Then the line was cleaned up, and two of its three things moved.** It had
four styles in one sentence: plain text, underlined words, `·` separators, and
the address as a raw URL in a code font. Now:
- **Choose files** is a real (quiet) button.
- The address sits behind **Use on your phone or TV**. That opens a small
  panel with the home address, the away-from-home address when remote access
  is on and working, and Copy buttons (secure contexts only; on plain http the
  address is still there to copy by hand). It is no longer duplicated in the
  account panel.
- **Check for new files** lives on the account page. Uploads trigger a scan
  and the backends watch their folders, so it is rarely wanted.

**Then the line went altogether.** The main screen is now the header (search
and one Account button), the filters and the library. Everything the line
carried moved to the account page, at the owner's request, for a cleaner
first screen:
- **Add media** (was Choose files), first on the page, beside Check for new
  files. Dropping still works anywhere on the window.
- **Use on your phone or TV** is a card there, shown whenever the server has
  an address to give, not a button that opens a panel.
- **Sign out** sits beside the page's title, so the header has one button.

What is left under the filters is only "Indexing new files...", while it is
true. The empty library message now carries the way in for a phone, which
cannot drag: "Open Account and choose Add media".

The setup box names **shelves, never servers** ("Films and TV: Getting ready",
not "jellyfin: waiting for jellyfin (attempt 6)"). It was the one place a
person setting SoundStorm up learned what runs behind it, along with "No API
keys needed", a line for developers. An owner still gets the raw error, folded
under Details, on a failed shelf.

The account is **a page of its own**, with a back link, while the library
steps aside, not a card pushed between the setup box and the results. The
sections are headed. Password fields are stacked, because the one-line form
put one label above its box and the other beside it once it wrapped. People
rows wrap instead of running past the card on a phone. Links use the accent
color; the browser default was dark blue on a dark background, and the
remote address was the hardest line on the page to read.

One signal survived from the per-kind counts the card used to show:
**indexing…**, displayed only while a backend is still working through what
arrived. It is what tells "nothing has been added" apart from "a scan is still
running", which are the two reasons a search comes back empty and looks
broken. The rest of the counts went; the library itself is now on screen.

The empty state carries the whole of the first-run guidance, because nothing
else does: "Nothing here yet. Drag music, films, audiobooks or ebooks anywhere
on this window."

`internal/library` owns the folders. It reads the directory for exactly two
reasons - creating it, and counting files so the UI can tell those two cases
apart. It does not index. Do not let it grow into an indexer.

**Where the library lives is chosen at install time, not in the app.**
`SOUNDSTORM_LIBRARY_PATH` in `.env` moves it - to an external drive, usually -
and every mount in the compose file reads the same variable, so the shelves
cannot drift apart. It is an installer option (`-Library`, `--library`) and not
a setting, because which folders a container can see is fixed when it starts:
changing it from inside would mean SoundStorm driving Docker, the thing it is
deliberately denied (see "Installing, updating, removing"). The installer
never moves existing media; it says where the old files are.

On Windows a missing drive stops the stack from starting, which is the safe
failure. On Linux an unmounted drive leaves an empty mount point, which looks
to every backend like a library somebody emptied - and `EnsurePlaceholders`
would write a README into it, taking away the empty-folder protection Jellyfin
relies on (see "Telling the backends to look"). Documented rather than guarded
for now; a guard would need to recognize "this is not the library I had" with
no state about the media, which is the line `internal/state` does not cross.

**Uploads survive a shelf on another drive.** They are staged in
`library/.uploads` and renamed into place, and a rename cannot cross
filesystems - found as `invalid cross-device link` while testing pictures,
the shelf most likely to live on its own disk. `placeFile` falls back to
copying to a hidden `.<name>.*.soundstorm-part` beside the destination and
renaming from there: atomic again, and an extension no backend indexes.
Verified by rerunning the exact failing setup - a shelf bind-mounted
separately inside the container - and watching the upload land.

`Hint()` is the path to show a person and `Root()` is the path SoundStorm
sees; inside a container those are `./library` and `/library`, and only the
first exists on anybody's computer. `/api/library` sends the hint as `root`.

## PDFs, and why there is no PDF parser

`internal/pdf` does not parse PDF structure, deliberately. Doing it properly
means the cross-reference table, then cross-reference streams, then object
streams - hundreds of lines before the first title appears. Measured against
five real PDFs from five producers (pdfTeX, Gutenberg, Adobe Designer,
Ghostscript, PDFsam), all that machinery would have returned nothing: every one
had an Info dictionary that was absent or literally empty, `/Title ()`.

Two of the five carried an XMP packet - Dublin Core, plain XML, uncompressed -
which a byte scan and `encoding/xml` reach for free, in the same vocabulary
`internal/epub` already speaks. So the order is XMP, then the Info dictionary if
it happens to be readable, then the filename.

For PDFs the filename is a primary source, not a fallback. Most were never told
anything about themselves and whoever saved the file put the only real
information into its name.

One trap worth knowing: XMP nests every value inside `rdf:Alt` or `rdf:Seq`
containing `rdf:li`, so reading `dc:title` as a plain string yields an empty
one. That is exactly how those five files first looked like they had no
metadata at all.

No covers: extracting one means rendering page one, which needs a PDF renderer
this project is not carrying. No reading position either - PDFs go to the
browser's own viewer in an iframe, and browser viewers do not expose position.

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

## Documents, and the second shelf SoundStorm owns

A PDF is a book or it is a gas bill, and the ebook shelf used to hold both.
Documents is its own kind (`media.KindDocument`, folder `documents/`), with
its own chip, its own access checkbox, and its own rescan - everything a kind
gets, because a half-kind that shares ebooks' permission would be a shelf you
can see into from the wrong account.

It passes the ownership rule above for the same reason ebooks do: a PDF needs
no transcoding, and what it says about itself is read the way PDF books
already are. It is served by the same source type as ebooks -
`localbooks.Config.Kind` picks EPUB-and-PDF or PDF-only - because a document
is opened exactly as a PDF book is; the difference is which shelf it is on.
Two instances, `ebooks` and `documents`, are two backends to provisioning.

**Deciding book or document is the mp3 problem again**, and gets the same
answer: evidence first, a question only without it. A PDF next to a
`metadata.opf` or an epub is a book (Calibre); one under a folder called
`Books`, `Ebooks`, `Calibre`... is a book; one under `Papers`, `Manuals`,
`Taxes`, `Statements`... is a document - whole folder names only, so
"Paperback Writer" is not a paper. A loose PDF with none of that is asked
about, once per dropped folder. A PDF waits for the rest of its group before
deciding anything, or an Audible book's companion PDF would send the whole
audiobook to a book shelf.

Documents are **not** restructured. A tax form has no author to file by, and
`Taxes/2024/` is exactly how somebody finds it again.

## Pictures, and why they are delegated

Pictures fail the ownership rule, and the reason is concrete: **iPhones save
HEIC, and Go's standard library cannot decode it.** SoundStorm could not show
a thumbnail of most phone photos without a dependency, phone clips need
transcoding, and a photo library of any size is the scanning-and-indexing job
this project exists not to rebuild. So they go to Immich, the way music goes
to Navidrome.

Immich was chosen over Photoview (lighter, closer to "the folder is the
interface") for search by what is in a picture - the feature people compare
against Google Photos - at the price of four containers (server, machine
learning, Postgres with vector search, Valkey), about 5GB of images, and 6-8GB
of RAM. Researched, not assumed: v2.0 (October 2025) promised semver, and v3.0
(July 2026) then broke API endpoints "that affect only third-party tools" -
which is what SoundStorm is. **So every Immich image is pinned to its major
version (`v3`), never `latest`**, and moving to v4 is a deliberate change with
an adapter update behind it.

**Provisioned with nobody logging in**, every step checked against a live
3.2.2: `admin-sign-up` accepts `soundstorm@soundstorm.invalid` (RFC 2606's
never-existing domain; Immich validates the shape and sends nothing to it),
login, an API key with `permissions: ["all"]` - the session token expires, the
key does not, and the password is not kept - then an **external library** on
`/pictures`, mounted read-only. Immich's docs only describe creating one
through the admin screens; the endpoint behind them is in the API. Folder
watching is off by default and is switched on by reading `/api/system-config`,
changing `library.watch.enabled` and sending the whole document back, which a
test holds to changing nothing else.

External rather than Immich's own upload storage, deliberately: a photo is a
file in `pictures/` however it arrived, and Immich can never move, rename or
delete one. Its thumbnails and previews live in a named volume, regenerable
from the photos. Its database is a named volume too, and must be: Immich's
Postgres must not sit on a network share or an NTFS drive, and a named volume
lives on Docker's own Linux disk on every host.

**Photos break the title order that paging depends on**, so `media.Item`
grew a `SortKey`. A filename like `IMG_4031` means nothing and nobody browses
a camera roll alphabetically; Immich returns newest first (or most relevant
first for a search), and each item's SortKey is its rank in that order, behind
a `~` so that in a browse of everything photos follow the titled media. The
merged-paging property - each source returns its own first N in merged order -
holds because the key *is* the source's order, and a test walks it.

**The viewer shows Immich's preview, never the original.** The preview is a
JPEG whatever the original was; a browser can show neither HEIC nor raw. The
original is a download (`/api/stream`), and a clip plays through the ordinary
video player from Immich's playback endpoint, which honors `Range` (206), so
seeking works through SoundStorm's proxy. `ArtTarget` takes `<id>@preview` for
the large size.

**Smart search needs models Immich downloads on first use.** On a fresh probe
the first two searches timed out while it did; the adapter falls back to a
filename search, so a new install's first searches still answer.

**A picture drop is decided by what dominates.** `.jpg` was only ever a
companion - an album cover, a film poster - and a folder of nothing but photos
used to be skipped as a folder of companions. Now a group of only images is
pictures; images beside audio or a book stay artwork; beside video, photos
lead only when they plainly outnumber the clips (a camera roll) or a folder
says `DCIM`, `Photos` or `Pictures`. Recognized artwork - `poster`, `fanart`,
`cover`, `extrafanart/` - is never counted as photos, so a film with ten
pieces of fan art stays a film. Pictures keep their dropped folders.

Verified end to end on a throwaway stack: SoundStorm provisioned a fresh
Immich by itself, browse returned newest first, thumbnail (WebP), preview
(JPEG) and original came through SoundStorm, an upload appeared four seconds
later, and in Chrome the viewer opened, stepped with the arrow keys and
closed on Escape.

**Found on the way, not yet fixed:** uploads are staged in `library/.uploads`
and *renamed* into place, which is atomic only on one filesystem. The compose
file mounts `library/` whole, so it holds - but somebody who mounts
`pictures/` from a separate drive (a common wish for photos) would get
`invalid cross-device link` on every upload. The fix is a copy to a hidden
temporary name beside the destination, then the rename.

**People, Places and On this day** come from what Immich works out itself,
through `source.PhotoBrowser`: `GET /api/people` (paged, hidden left out;
a face is art id `person:<id>`, its `/api/people/{id}/thumbnail`), a
person's photos by `personIds`, `GET /api/search/cities` for towns (the id
carries city, state and country, since towns share names) and their photos
by those fields - all checked against the live 3.2.2, read only. Somebody
found but unnamed can be named from their page (`PUT /api/people/{id}`), by
anyone who can see the photos: a name is for the household. On this day is
SoundStorm's own, not Immich's memories, which are made overnight and only
for days Immich picked (empty on the live library): one metadata search per
earlier year, 25 years back, in parallel, cached an hour; 29 February only
asks leap years. Albums are left out for now: the library had none, and
checking them would have meant creating one on it.

**The Photos pill is a timeline, Google Photos' way** (2026-10-03, the
owner's asking): every month under its heading and every day within it,
newest first (`showPhotoTimeline`); each month filled in only as it comes
within 1200px, so thousands open at once; a handle down the right edge that
shows while scrolling, the years marked where they begin, and dragging it (or
a tap on the edge) goes to that place with the month in a bubble beside the
finger (`tlScrubber`); and pinch to zoom, a step a pinch, 2-12 to a row, kept
as `soundstorm-photo-columns` (`tlPinch`, the photo under the fingers kept in
view). The server answers from Immich's own timeline (`source.PhotoTimeline`:
`GET /api/photos/months`, `GET /api/photos/month?m=2024-01`, over Immich's
`/api/timeline/buckets` and `/bucket`, a month's photos in columns), each
person through their own key. Months not yet loaded are guessed at from their
count, so the handle lands near the month and the page settles as they load.
Typing in the search box still searches; on a TV the old grid stays.
**The handle no longer scrolls the page while it is held** (2026-10-04, the
owner's report: glitchy and flickery on an iPhone). It scrolled on every
finger move, and WebKit places a pinned header only once a scroll settles,
so through a drag the header and pills kept vanishing (seen frame by frame in
a recording of the simulator); the months flown past were blank anyway, as
none are fetched until let go. Now the handle and the month beside it follow
the finger, once a frame, and the page jumps once where it is let go. And
held, an iPhone selected the year labels and tinted the page blue: the
handle takes no text selection. Checked in the iPhone simulator with real
touches against a stand-in timeline of eight years. **Not reproduced:** the
top cut off after scrolling up (Android and iPhone) - flicks back up reached
the very top with the first month's heading in full.
Then found, from the owner: **only after using the handle**, in every
category with one. The handle reckoned from the timeline's own start less
80px, so its top left the page about a hundred pixels down - the newest
month's name under the pills, and no further up but by hand - and with a
long timeline one pixel of handle is hundreds of pixels of page, so a finger
never landed on the top anyway. Now one scale for the handle, the years and
the bubble: the top of the handle is the page's top, its foot the end, a
month where the scroll that shows it under the pills is; the handle is held
by its middle, and its ends snap (the foot goes to the new end once the
months landed on have grown the page). Checked in Chrome as a Pixel 7 by
touch: the handle's top landed at 0 with the newest month's name showing,
its foot at the very end, and October 2021 in the bubble landed on October
2021. **For the PC - the wrong month, from the same screenshot:**
"September 2026" held photos headed "Thu, Oct 1". `MonthPhotos` labels each
day from Immich's `fileCreatedAt`, which is UTC, while the buckets (and
`PhotoMonthsOf`) are local months: an evening photo on 30 September in
Mountain Time is 1 October in UTC. The day should be the local one - the bucket's
local offset if Immich gives one (`localOffsetHours` in recent versions;
check against 3.2.2), or `localDateTime` from the asset. *(Fixed on the PC, 2026-10-04: the
bucket's `localOffsetHours` - there in 3.2.2's timeline answer - is added to
`fileCreatedAt`; `TestATimelinePhotoIsDatedByItsOwnClock`.)*
**Videos and Live photos are timelines too**, from the server with no
ceiling: Immich's buckets cannot be asked for one kind, so
`PhotoMonthsOf` counts them by a search of them all (kept three minutes per
person, `typeMonthCache`) and `MonthPhotosOf` fetches a month (a day wider at
each end, as the search goes by UTC and the months by local time, then the
month's own kept); `?type=video|live` on both routes. A video tile carries its
length as a clock (`tlClock`). A first version fetched the whole list in the
page and grouped it there - fine for one household, silently cut at 2,000 for
a bigger one; replaced the same day (the owner: nothing built for one install). Checked
against a throwaway Immich 3.2.2 with 304 generated photos over 2019-2026, in
Chrome at phone size: months and days headed, a drag to the foot showing
"April 2019" and landing in spring 2019, a pinch from four to three a row.

**Photos is stills now, and Photos & videos is everything** (2026-10-03, the
owner's asking): the Photos tab's first pill is **Photos** (`photo-stills`,
`?type=photo`: pictures, Live Photos among them, no clips), then **Photos &
videos** (the `picture` shelf as it was), People, Places, Videos, Live photos.
Its timeline costs no search of every photo: a month's count is the
timeline's less its clips (`stillMonths`), and a month is Immich's own bucket
with the clips left out; typing searches the library's own search, clips left
out. A pill new since somebody saved an order goes where it stands by default,
before the next pill the order names (`tabShelves`), not at the end. On a TV
(no timeline) it is the newest 2,000 stills. **A pill needs its hidden chip
in index.html** (`#filters .chip[data-kind=...]`): the tab presses it, and
without one the whole Photos tab showed nothing (the owner's report, the same
day). Then checked on a throwaway Immich at phone size: Photos 13, Photos &
videos 15, Videos 2, a video playing in the viewer ("1 of 15") and the next
swipe a photo.

**A backed-up video with no date inside it keeps the phone's date too**
(2026-10-03): a clip saved from the web or a message has a zero movie header,
and Immich dated it by its file - the owner's library had one filed in July
showing 1 October. `datePicture` now takes MP4 and QuickTime (`tags.VideoDate`:
any date the file holds, a device's or an encoder's, is left alone), on backup
and on the heal. `TestABackedUpVideoWithNoDateKeepsThePhonesDate`.
**Left, an edge case for a decision:** a backup's month folder is the phone's
moment in UTC, so a photo taken in the evening of a month's last day (west of
UTC) is filed in the next month's folder; the app's dates are right (Immich
reads the photo's own clock), only the folder is off, as is an edited copy
filed by when it was edited while dated by its original. Fixing it means the
phones sending their time zone and the check looking in both months.

**Videos are in the viewer's swipe, as in Google Photos** (2026-10-03, the
owner's asking): the viewer walks photos and videos alike (`photosOnScreen`),
and a video - opened from any tile or swiped to - plays in place over its
still (`#photo-video`, `startClip`/`leaveClip`), with play and a timeline
above the caption, moving with the still in a swipe, never zoomed. The music
pauses for it and plays on after. Controlling a TV it plays on the TV (which
shows it in its own viewer), not on the phone as well. It used to open the
film player and the swipe skipped videos. And the timeline's tiles are
rounded squares again (corners shrinking as more fit a row): Google's square
edges read as worse to the owner. A downloaded clip plays from the server,
not the device.
**No grey video sign, and a preview as you scroll** (the owner: videos
"look kind of bad" opening and swiping, a grey video symbol for a second; and
one should play a preview while scrolling). The viewer's video is unseen
(`pv-waiting`, opacity 0) until its first frame moves, the still beneath it
showing the whole time (`fadeInWhenPlaying`) - measured, opacity 0 at 0.00s
and shown at 0.07s. And the timeline plays one muted preview (`tlPreview`):
the video tile in full view nearest the screen's middle, half a second after
scrolling rests, its first eight seconds round and round, shown only once
playing; one at a time, none on a slow link, a TV, with the viewer open or
the app hidden. Checked on the throwaway Immich at phone size.
**The video last touched is the one previewed** (the owner's asking): a
finger coming down on a video tile (a swipe starting there) makes it the one
that plays while it stays at least half on screen; otherwise the one nearest
the middle. Still one at a time (the owner chose one over several).

**Smoother scrolling through thousands** (2026-10-03, asked as why Google's
is smoother): **blurred stand-ins** - Immich keeps a ThumbHash of every photo
(about 30 bytes, in its timeline buckets and asset records), passed on as
`extra.thumbhash` (64 bytes at most) and decoded on the page
(`thumbHashToRGBA`, the format written out, to a small PNG address, kept per
hash) as the tile's background the moment it exists, the thumbnail fading
in over it (`tlStandIn`); **dragging the handle fetches nothing** until it is
let go, then only the months near where it landed (`TL.dragging`,
`tlLoadNear`); and **months far off are not drawn** (`content-visibility:
auto`, each sized by its own guess until seen, so the handle and the page
agree). Checked on the throwaway Immich with thumbnails held back 3s: all 15
tiles showed their stand-in at once. Not measured with a big library (the
test one has 15): the owner's phone is the check.

**Videos and Live photos** are pills in Photos too (the owner's asking,
2026-10-01): `GET /api/photos/of?type=video|live` (`PhotosOfType`), Immich's
metadata search with `type: VIDEO`, or `isMotion: true` - which its search
repository reads as `livePhotoVideoId is not null` (checked in its code, 3.x).
A Live Photo carries `extra.live`, and the viewer's **LIVE** button plays its
moving part over the still (`/api/stream/<source>/<id>@live`: looked up
through the photo with the asker's own key, so only a photo they can see
gives one), quiet while music plays, gone when it ends. The owner's library
had 63 videos and no linked Live Photos when built, so LIVE is untried
against a real one.

## Everyone's own photos

Asked for alongside phone backup, and designed by the owner: **each member
has their own photo folder, sees only their own photos, and the owner sees
everybody's**. There is no setting to show anyone the owner's photos; what the
owner controls is whether a member has Pictures at all (their library ticks,
as for any shelf) and how much space their photos may take.

- **The folder** is `pictures/Personal/<name>/` (`library.PersonalFolder`, the
  account name made safe as a folder). Everything a member adds to the picture
  shelf goes there (`personalUpload`, `personalPlan` - the drop panel shows the
  real destination), and so does any phone's backup, the owner's included.
- **The owner sees only their own photos too** (2026-10-04, the owner's
  call, a reversal: the owner used to see everybody's). The owner keeps the
  folders on disk, but in the app sees their own photos and the household's
  shared ones (anything outside `Personal/`), never a member's: the
  administrator's library, still the whole pictures folder, leaves out every
  other person's `Personal/<name>` as Immich exclusion patterns
  (`provision.SyncPhotoPrivacy`, `photoprivacy.go`): a removed member's kept
  folder too, names escaped so none becomes a wildcard, Immich's own default
  patterns kept, a scan only when the list changed. Run when somebody is
  added (or accepts an invitation) or removed, a minute after start, and
  every ten minutes. Checked on the after-deploy test server: a member's
  backed-up photo shown to her and not to the owner - not in the timeline,
  not by searching its name - while the owner's own backup and the shared
  photos stayed. Immich only ever reads the folder, so leaving one out
  deletes nothing. `TestOthersPhotosAreLeftOutOfTheOwnersLibrary`.
- **Their own Immich account** (`provision.PhotoAccountFor`, made the first
  time they look, like the per-member Audiobookshelf account): an account per
  member whose one library reads only their folder. Filtering one shared
  account was rejected because faces would leak - Immich groups faces across
  every photo it can see - and with an account each, People, Places, search
  and On this day are worked out from their photos alone. The owner keeps the
  administrator's account, whose library is the whole pictures folder,
  members' folders included. Checked against Immich 3.2.2 before building:
  overlapping libraries are allowed, a member sees only theirs and is refused
  another's photo by id (400). The cost: a member's photos are processed twice
  (thumbnails, faces), once per library; the files are not copied. Removing a
  member deletes their Immich account (`deleteImmichMember`, force) and
  **keeps their folder**, for the owner to decide about.
- **The adapter acts as the person asking** (`immich.Config.ActAs`, `as`): every
  request carries that person's own key and library, the stream and art
  targets too. An error, never a fallback to the administrator's key, when a
  member's account cannot be made. Rescans scan every library. On this day's
  cache is per person.
- **Space**: 100 GB each by default (`state.DefaultPhotoLimitGB`), the owner
  changing the household default (`/api/photos/limit-default`) or one person's
  (`/api/users/{id}/photo-limit`, null for the default, -1 for none) in People;
  the owner's own photos have no limit. Measured by walking the folder, kept a
  minute and added to as files land. At the limit an upload or backup is a 507
  saying so; nothing is deleted. A member's Settings has **Your photos**: how
  much is used, and plainly that the owner of the server can see them.
- **Phone backup's server half**: `POST /api/photos/backup/check` (which of
  these do you have - name, when taken, size - so a reinstall sends nothing
  twice) and `PUT /api/photos/backup?name=&taken=` (one file as the body, to
  `Personal/<name>/<year>/<month>/`, a different file of the same name that
  month kept beside it under a name for what makes it different, its camera
  or when it was taken - it was the name plus the moment it was taken,
  "IMG_0001 1790970939045.jpg", until 2026-10-02; a photo sent again is
  found under any of those names by its size, `personalSentBefore`).
  Dropped and imported photos are named the same way, an identical one
  having been refused by its content first.

**The phones keep no list of what they sent** (2026-10-03, the owner's
asking, after 222 pictures had to be sent again and each phone's own list
said they were done): each backup run asks the server about every photo
(`/api/photos/backup/check`, a hundred at a time - about 16 requests for
1,570) and sends what it lacks, so the server is the one record. **What is
deleted is remembered** (`deletedphotos.go`, `photos-deleted.json` in the
state folder): deleting from a person's folder notes each file's path within
the pictures shelf and size, the check and the backup answer "have it" for
them, and putting them back from the bin forgets them. Matched on size where
the phone gives one (Android), on the path alone where it cannot (the iPhone,
size 0). A backup waits up to three minutes for an upload slot rather than a
429 (`waitUploadSlot`: the iPhone hands several to iOS at once, and 164 of a
re-backup's sends were refused). Android 0.37 dropped its list; **for the
Mac:** the iPhone's `PhotoBackup` should drop its sent list the same way (ask
the server about every asset each run; the per-account list file can go), and
until then it still needs a reinstall to send what the server lost. *(Done on the Mac,
2026-10-03: `PhotoBackup` keeps only what one run has learnt, asks the server
about every asset each run, 60 at a time, and deletes the old
`backup-sent-*.txt` lists. Checked against a fresh server on the Mac: six
sample photos sent, then two removed from the server's disk one at a time,
each sent again on the next run. The UI test's second-run path - finding the
backup switch in Settings when it is already on - fails to tap the switch
now that Settings has more cards above it; the first-run path passes.)*
`TestADeletedBackupIsNotSentBack`.
**And each run checks only what is new** (2026-10-09, the owner's asking:
professional, no more work than needed - a run asked about every photo,
about 500 requests for 50,000, plus the iPhone looking each one up). The
server gives a marker of the person's photo folder (`GET
/api/photos/backup/marker`, `library.PersonalMarker`: a count and an
order-free sum of each file's place and size, date files left out) that
changes when a photo is added, removed, renamed or replaced. The phone keeps
the photos the server confirmed with a stamp (Android: MediaStore's size and
last change, free to read) and the marker from the end of its last run;
while the marker holds it checks only photos not confirmed or edited since.
It checks every photo when the marker moved, a day has passed, the server
has no marker (an older one) or another server or account is chosen; a full
check cut short is finished by the next run (`fullPending`). The server
stays the record - lost photos come back on the next full check. Android
0.53 (`PhotoBackup.confirmed`, `fullDue`). `TestTheBackupMarkerFollowsThePhotoFolder`.
Not tried on a phone. **For the Mac:** the iPhone's `PhotoBackup` wants the
same: keep each asset's `localIdentifier` with a stamp
(`modificationDate`, which changes on an edit) once the server confirms it,
fetch the marker at the start and end of a run, and while it holds look up
and check only assets not confirmed - which also spares the Photos lookups
of every asset that made the looks stutter after opening.
**Then the iPhone's first full run flooded the server**: iOS sent dozens of
the background session's files at once over one connection, the server took
four per person, and the rest waited three minutes and got 429 - 898 refusals
to 33 photos in fifteen minutes, with a check of all 1,570 photos about every
16 seconds (885 checks). Backups now have an allowance of their own, 64 at
once (`maxBackupsPerUser`, `backupSlot`), **and never wait for a slot**: held
waiting, a request does not read its body, and over the one HTTP/2
connection its bytes filled the shared receive window and starved the uploads
under way - "i/o timeout", 98 to 40 photos. Received at once or refused at
once. Afterwards 164 of the 222 came back from the iPhone with their dates; the
other 58 are the Android's and need 0.37 on it (the bin keeps them until
about 2 November).
**For the Mac - the one silent gap left:** the iPhone checks with size 0
(`PhotoBackup.swift`, the check's `"size": 0`), so the server matches on name
and month alone: a *different* picture of the same name in the same month
(the camera's numbering starting over, another device's file) is taken for
the one already there and never sent, and a deleted one's path blocks it too
(`wasDeleted`). Send the real size for an asset whose resource is on the phone
(`PHAssetResource`'s file size), keeping 0 only for one held in iCloud alone;
the server already matches on size when given one. *(Done on the Mac, 2026-10-03:
the check sends the resource's size as Photos records it (`fileSize`, looked
for before it is read; 0 where it is not there), never reading the file.
Checked: the server's copy of one photo made a different size, and the next
run sent that one alone, kept beside the other; the rest matched by size.)* **For the Mac:** a run should not start while the
last one's uploads are still with iOS (it re-checked everything every few
seconds), and a 429 should wait for the next run rather than count as passed
over. *(Done on the Mac, 2026-10-03: `PhotoBackup.start` starts no run
while files are still with iOS - it starts by itself once they are through
(`sentSome`) - and a 429 or 503 hands iOS nothing more for a minute and is
sent again by the next run, not passed over. Checked against a fresh server
on the Mac: six photos, six sends, no refusals, one check a run. A 429 itself
was not produced.)*

**A backed-up picture with no date inside it keeps the phone's date**
(2026-10-03, `backupDated`): screenshots and pictures saved from messages
showed in Immich as the day they were backed up - the backup filed them in the
right month folder by the phone's date but wrote it nowhere, and Immich dated
them by the file. Now that date goes beside the picture (an XMP sidecar,
ranked as a download's record) and onto the file. Videos carry their own.
`TestABackedUpPictureKeepsThePhonesDate`. The 222 already backed up so on
this install were repaired by hand (moved to the bin, sent again by the
phones) - which no other install could do, so **backups heal themselves**
(`backupHeal`): every backup run asks about every photo with when it was
taken, and a picture already here with no date inside, none beside it and a
file date not the phone's gets the phone's date written beside it on that
check (passed by its details alone otherwise; one read per picture,
remembered). **Immich reads a date file beside a photo it already has only
when its sidecar discovery runs** - not on a rescan, not on refresh-metadata
(checked on 3.2.2) - so every date file written for a photo Immich may have
asks for it, gathered into one request 20 seconds later
(`findSidecarsSoon`, `source.SidecarFinder`): `PUT /api/jobs/sidecar`
`{"command":"start"}`. The newer `/api/queues/sidecar` answers the same 200
and only reports the queue - a trap. This also made the duplicate-improves-
the-kept-copy date reach Immich for a photo that stays put (only a move was
ever checked). End to end on a throwaway Immich: a dateless picture copied in
the old way showed today; one phone check later, Immich showed its real date
in about 24 seconds. `TestAnOldBackupHealsOnTheNextCheck`.
**A HEIC's date is read wherever it is stored** (`photoimport/heic.go`,
`ReadTaken`, `FileTaken`): a HEIC keeps its EXIF as an item of its own, and
the file's index (the meta box's iinf and iloc) says where - read there, a
few small reads, never the whole file. Before this every reader looked only
at a file's first bytes, and **on 400 of the owner's real iPhone photos the
date was past the first 512KB in every one**: SoundStorm had never read the
date inside an iPhone photo. Harmless for phone backups (filed by the
phone's date, the same moment; Immich reads HEIC itself), but a HEIC dropped
in by hand was filed by its name or file date - and, since stills stopped
taking a file date, would have gone to Undated. Every reader uses it now:
dropped photos, backups, the date heal, improving a kept copy, naming a
second copy, and the zip import (which reads such a photo again, once, up to
64MB, as a zip cannot be read at a place). `TestAHEICsDateIsFoundWhereverItIs`
builds a HEIC with its date 2MB in.

**The phone's half, in the Android app (0.14, `PhotoBackup.kt`)**: WorkManager
jobs, so Android decides when (Wi-Fi only unless turned off, while charging if
asked) and it carries on with the app closed - one when a photo or video is
added (a content trigger), one every six hours, and each job (eight minutes at
most) chains the next while there is more. Newest first. What was sent is kept
by MediaStore id on the phone, and each batch of 100 is first checked with the
server, so a reinstall or a new phone sends nothing twice. The file sent is the
original (`setRequireOriginal`, with ACCESS_MEDIA_LOCATION asked for alongside
the photo permissions): otherwise Android hands over a copy without the place
it was taken, which would empty Places and differ in size. It signs in with the
web view's own cookie. The page decides nothing: Settings' **Your photos** has
the switch and its options in the app (`soundstormApp.backup('set'|'status')`,
answered through `window.__soundstormBackup`), and after signing in on a phone
with Pictures it asks once, "Back up this phone's photos?", saying for a member
that the owner can see them. A refused permission leaves it off and says why;
a full photo space, a disk or an ended sign-in stop it with the reason shown
until the next job. Not on a TV. Checked: the page's question and controls with
a stand-in app; the app builds. **Not yet run on a real phone.**
**On the owner's phone it looked stuck at 14 of 1209** (0.15): it was sending a
long video, and only whole files moved the count; turning the switch off and on
had started a second job sending the same video beside it. Now one backup runs
at a time (a lock in `BackupWorker`; the switch's job is KEEP, not REPLACE),
Settings shows the file being sent ("Sending a video: 240 of 600 MB", asked
for every two seconds while it shows), and the job runs in the foreground with a
quiet notification (dataSync), since WorkManager stops an ordinary job at ten
minutes - one long video on a slow link. Android may refuse the foreground from
the background; then it runs as before. Only a network error is called "could
not reach the server" now; anything else names itself.
**The iPhone app backs up too (2026-10-01, `ios/SoundStorm/PhotoBackup.swift`)**,
answering the same `window.soundstormApp.backup` messages with the same
status, so the page has no iPhone code. The photo library (full or limited
access, asked when backup is turned on); newest first; the server asked first
which it has (by name and month - size 0, since sizing a photo kept in iCloud
would mean downloading it); each original written to a file
(`PHAssetResourceManager`, iCloud allowed; a Live Photo's still only) and sent
in a background `URLSession`, so a file already going finishes with the app
put away, and one that finishes after the app was closed still counts. It
sends the web view's own session cookie - WKWebView keeps cookies apart from
URLSession's - to the server only, never through a redirect. It runs while
the app is open, 30 seconds after a photo is added, and as a
`BGProcessingTask` iOS starts when it chooses (requires power if "only while
charging"). Wi-Fi only means not expensive or constrained (`NWPathMonitor`).
Where to send: the page's secure address, its away twin, then the address the
app was opened with. Checked with `testPhotoBackupSendsTheCameraRoll` against
a local server: the simulator's six sample photos arrived in
`Personal/owner/<year>/<month>/`, the five JPEGs byte for byte, and Settings
said "All 6 photos and videos are backed up". **Not checked:** the background
task and an upload finishing with the app closed - a real iPhone over a night
is the test. Two things the test met: Swift 6.3 crashes compiling the `async`
form of `willPerformHTTPRedirection` (the completion-handler form is used),
and signing out (or in as a different account) turns backup off, which the
app also counts as "decided" - so the next person was never asked, on Android
too. The page now asks unless backup is on or this person answered on this
device (its own `soundstorm.backupAsked`, set by the question or the Settings
switch and cleared with the downloads), not by the app's flag;
`testBackupIsAskedOfWhoeverSignsInNext` fails against the old page. After
signing out the page focuses the username and the keyboard covers the
password field: a person uses the keyboard's next arrow, the test opens the
app again.

**For the Mac: iPhone backup stops when the phone is locked** (the owner's
report, 2026-10-02). `PhotoBackup.backUp` sends one file at a time and awaits
each (`try await send`), so when iOS suspends the app a few seconds after the
screen locks, only the upload already in the background session finishes and
nothing queues the next; backup then moves only while the app is open and
when iOS grants the `BGProcessingTask` (usually overnight, charging). The fix
is to hand iOS the uploads ahead, which a background session carries on with
the app suspended or ended:
- Export ahead and enqueue many: write the next files' originals
  (`PHAssetResourceManager`) and give each to the background session at once
  rather than awaiting it - say up to 200 files or 1 GB waiting on disk,
  topped up as they finish. iOS uploads them while locked, on its own
  schedule (discretionary for big ones; `isDiscretionary = false` for a
  session started in the foreground helps them start promptly).
- Mark a file sent when its task completes, in the session delegate - also
  when the app is relaunched for `handleEventsForBackgroundURLSession` - and
  delete its exported copy then; keep the queue's keys on disk so a relaunch
  knows what each task was.
- On going to the background, `beginBackgroundTask` for the ~30 seconds iOS
  allows, to export and enqueue as many more as fit.
- The check with the server stays before export (no point exporting what it
  has); Wi-Fi only maps to `allowsExpensiveNetworkAccess = false` and
  `allowsConstrainedNetworkAccess = false` on the session.
A phone actually switched off runs nothing, of course; Settings could say
"Backup carries on while the phone is locked; iOS decides the pace" once this
lands.
*(Done on the Mac, 2026-10-02.)* `PhotoBackup.backUp` checks with the server
as before, then exports each file into `Application Support/backup-queue`
(not temporary files, which iOS may clear while it still has them to send)
and hands it to a background session without waiting - up to 200 files or
1GB at once, topped up as they finish (`Uploader.pending`). Two sessions,
since a session's network rules are fixed: one with expensive and
constrained networks off (Wi-Fi only), one with them on. What each upload is
rides in its `taskDescription` (key, sent list, whether it completes its
photo, name), so `didCompleteWithError` counts it as sent and deletes its
copy even after a relaunch, and at launch the sessions' tasks are read back
(`restore`) so nothing waiting is handed over twice; copies without a task
are cleared. Going to the background (the phone locking) begins a background
task and runs, to hand over as much as fits in iOS's half minute. Also from
the review: the sent list is per account (`backup-sent-<account id>.txt`; the
old single list becomes the current account's), a new server clears the old
secure address, a refused file is named and passed over (401, 403 and 507
stop the run), and a photo with no file at all counts as done. **One more
found testing it**: the web view's cookie store reads empty until a web view
exists in the process - at launch backup ran first and said "sign in again";
in a background run there is no page at all. An invisible `WKWebView` loads
the store, and no cookies at all now ends the run quietly (the page's backup
messages start another). Checked in the simulator: all six sample photos
through the queue, the per-account list, the queue emptied. Not checkable
there: uploads carrying on with the phone locked - the owner's phone is the
test.

**Bringing a photo library in** (`internal/photoimport`, `httpapi/photoimport.go`):
Google and Apple give no way for SoundStorm to pull photos out (Google's
Photos API stopped reading a person's library in 2025; iCloud never had
one), but both hand out a download of everything as zips - Google Takeout,
Apple's "Request a copy of your data" - so Your photos has **Bring your photos
in from Google or Apple**: each service's steps with its link, then the zips
dropped anywhere on the window (a drop of nothing but zips goes here, not to
the shelves) or chosen. Sent in 8MB pieces to `library/.imports/<account>/`
(`POST /api/photos/import`, `PUT /api/photos/import/{id}?offset=`), carrying
on from where the server has it after a dropped connection or a reload (the
same zip chosen again picks up), then sorted one download at a time, a
restart resuming. Into the person's folder by when taken - **the download's
own record** (Takeout's JSON sidecars, found despite Takeout's naming: edited
copies, "(1)" copies, names cut short at 47 characters, album copies whose
sidecar is in the year's folder; iCloud's "Photo Details.csv", whose date has
an unquoted comma), else EXIF (found by its marker, so in a HEIC too), else a
date in the name (IMG_20191225_090000, PXL_..., Screenshot_..., WhatsApp's),
else the zip's date if not the day of the download, else `Undated/` - never
today's month. **Duplicates skipped** by content (SHA-256, against same-size
files already in the folder and within the download): Takeout copies a photo
into every album, and the same photo is often in Google and iCloud and the
phone's backup. **The date and place are written beside the photo** as an XMP
sidecar (`<file>.xmp`) when they came from the download or the photo has no
date of its own - the photo itself is never changed, and Immich reads the
sidecar (checked: a Takeout photo at the Eiffel Tower showed 4 July 2019 in
Paris). Counted against the photo space; at the limit it stops and says so.
The zip is deleted once sorted. The first walkthrough caught a photo filed
under today: the iCloud zip, finished first, had it with no date while the
Google zip knew it - hence the name and Undated steps. Uploads of a phone's
photo or a piece of a download are exempt from the 30-second body deadline
(`bodyDeadline`), which would have cut off a phone's video on a slow uplink.
**A zip is looked inside wherever it arrives** (`zipHoldsPhotos`, in the
shared intake, so a drop anywhere on any page and Add media alike): the
browser reads the zip's own table of contents from its end - zip64 too, as
Takeout's 50GB zips are - without unpacking anything. A Google or iCloud
download (by its folder names) or a zip mostly of photos and videos goes to
Bring your photos in; any other zip is not unpacked and a message says so;
the rest of the same drop is filed as usual. Checked with a Takeout zip, a
zip of notes and a photo dropped together. Not checked: a real zip over 4GB.
**Cards, folders and loose photos are sorted by date too** (the owner's
asking): every upload to the picture shelf - a dropped SD card, an old drive's
folder, files chosen on a phone - goes into the person's folder (the owner's
too, now) by when it was taken (`savePhoto`, `datedPhoto`, through
`library.SaveDecided`, which places a file once its bytes have arrived): the
date inside it, else one in its name, else, for a video only, the file's own
date the browser sends (`taken`, `File.lastModified` - on a camera's card,
when it was filmed), else `Undated/`. **Not a still's file date** (the
owner's call, 2026-10-03): a picture with no date inside it or in its name
was saved from a chat, an email or the web, or scanned, and its file date is
when that happened - it used to be filed in that month without a word;
Undated says its date is not known. A file already in the folder under any name is skipped as
"already in your photos" (`photoIndex`, the folder's files by size with
hashes worked out as needed, kept ten minutes and shared with downloads). The
date is written beside it for Immich unless it came from inside the photo.
**Home videos go to the photos, told by what is inside them** (the owner's
choice, 2026-10-01): a video dropped where the plan said "film" - a renamed
clip like "Jack's 5th birthday.mov" looks exactly like one - is looked inside
once it has arrived (`tags.VideoCamera`, `homevideos.go`, through
`library.SaveRouted`, which may change the shelf after the bytes land): a
phone or camera writes its make and model (`com.apple.quicktime.make`,
`com.android.version`, `©mak`/`©mod`), where it was filmed (`©xyz`), or a
maker's own box (GoPro FIRM, Canon CNCV, Samsung smta), and a film never
does - an encoder's `©too` (HandBrake, ffmpeg) does not count. Such a video
goes to the person's photos by when it was filmed (the phone's own creation
date with its zone, else the movie header's), which also dates every phone
video dropped into Photos. Only with Pictures on the account; the drop panel
shows where it really went. Camcorders' `.mts`/`.m2ts` are now kept at all
(they were skipped), and more device names count as clips: Sony's
`C0001.MP4`, Panasonic's `P1000123.MOV`, a camcorder's `00001.MTS` or
anything in an `AVCHD` folder, and screen recordings (`RPReplay_Final`,
"Screen Recording"). Checked with generated files through the upload route;
not yet with a real phone's video.
**A video named as a camera names it** (MVI_, VID_, PXL_, IMG_, GOPR, DJI_, a
date and time, WhatsApp's) is a clip for the photo shelf even alone with no
folder to say so (`looksLikeCameraClip`) - the first test filed MVI_0002.MOV
as a film. **Keeping folders of one's own**: Your photos says dropping sorts by
date, and that folders are kept by copying them straight into the folder on
the server's drive (for a member, by whoever looks after it). Checked with a
test card: a photo with its date inside (2016/05), a Pixel photo by its name
(2023/03), a clip by its file date (2015/08, with the date beside it), and a
copy of a photo already brought in from Google, skipped.
**A duplicate improves the copy kept** (the owner asked whether a duplicate
had anything useful): byte-identical files carry nothing new inside, but a
copy can arrive with a better date or a place beside it - the first import
walkthrough had a photo from iCloud with no date and Google's copy of it with
one, skipped. So every sidecar records where its date came from
(`soundstorm:DateSource`; one from before is taken as a download's), ranked
none < the file's own date < a name < Google's or Apple's record < inside the
photo (`photoimport.DateSource`, `Better`). A duplicate - from a download or
dropped - with a better-sourced date, or a place where the kept copy has none
(inside it, `ExifHasPlace`, or beside it), rewrites the kept copy's sidecar
and, when the date improved, moves it from Undated or the wrong month to the
right one (`improvePhoto`) - never out of a folder somebody arranged, and the
photo itself never changes. **Only the dated folders count as kept** (the
owner's rule, `managedPhoto`): a photo sitting only in a folder somebody
arranged and copied in does not stop a copy being filed by date, and is never
improved or moved - their folders are left exactly as they are, and such a
photo then shows twice, which Your photos says. The import counts them ("of those gave a photo
you had a better date or place"); a dropped one says "its date was
corrected". Checked on a throwaway Immich: an undated iCloud photo, then
Google's copy - moved from Undated to 2018/06, and Immich showed 26 June 2018
in New York City. Near-duplicates (re-compressed or edited copies) are not
caught; Immich's own duplicate detection could show them as possible
duplicates later.
**Every other place photos live** (the owner asked to cover them all): Your
photos lists nine sources, each folded open to its steps - Google, iCloud,
Facebook, Instagram, Snapchat Memories, Flickr, OneDrive/Dropbox/Amazon (a
plain zip download), WhatsApp and Telegram, and SD cards, cameras, old phones
and computers (drop the DCIM or Pictures folder). Social downloads strip the
photos' own EXIF, so their JSON is the only record: `photoimport.JSONIndex`
walks any JSON in a download for two shapes rather than one parser per
network - Facebook's and Instagram's media objects (`uri` beside
`creation_timestamp`, the posting date, ranked as a file's own date; and
`taken_timestamp` with a latitude and longitude in their EXIF summary, ranked
as a download's record) and Flickr's per-photo files (`id`, `date_taken`,
`geo` in millionths of a degree), matched to a photo by its uri's tail or the
number in a Flickr name. Chats (`messages/`, `inbox/`, Snapchat's
`chat_media/`) are left out - mostly other people's photos - and so are
Snapchat's `-overlay` sticker layers. Telegram's names carry the date
(`photo_12@24-12-2023_18-30-05.jpg`). The browser recognises these zips by
their folders (`zipHoldsPhotos`), or by twenty or more photos making up 30%
of a download's files. Snapchat's memories file holds only links that expire
unless "Export your Memories" was ticked, which the steps say. Each format
was built from its documented shape and checked with a generated download;
only the Facebook one went through end to end (Add media, a throwaway
Immich): three photos filed by their kept dates, the chat photo left out. Not
tried against a real download from any of them.
Not built yet: albums kept as albums.

**For the Mac: iPhone backup should send a Live Photo's moving part**
(asked 2026-10-01). It sends the still only (`PhotoBackup.swift`, "a Live
Photo's still only"), and the owner's library proved what that costs: of 160
iPhone photos, the 10 marked as Live Photos (`livePhotoCID` in Immich's exif)
have no clip on the server, so the Photos tab's Live photos pill is empty and
LIVE never shows. Immich joins a still and its clip by Apple's
ContentIdentifier inside both files, not by name or folder, so sending the
clip is all it takes. What to build:
- For a Live Photo, send the `.pairedVideo` resource too
  (`PHAssetResourceManager`), named as the still with `.MOV`
  (`IMG_0090.HEIC` and `IMG_0090.MOV`), same `taken`, so the server files it
  in the same month folder.
- Track the clip apart from the still in the sent list (say `<id>#live`), and
  check it with the server separately: the server already has the still of
  the ten (the check says so, by name and month), and a phone that marks the
  whole asset sent would never send their clips. This is what makes the ten
  arrive - so long as they are still on the phone as Live Photos.
- Count a Live Photo as one in "N of M backed up".
- Android needs nothing: a motion photo carries its clip inside the JPEG,
  which Immich reads.
Three of the ten sit in a hand-made `2022/` folder rather than the person's
dated one, so backup will file their stills again by date (the managed-folder
rule) beside the clips: those three show twice until the hand-made copies go.
*(Done on the Mac, 2026-10-01.)* `PhotoBackup.items` sends a Live Photo as
two files - the still, and its `.pairedVideo` (or full-size paired video) as
`<still's name>.MOV` with the same `taken` - each kept in the sent list on
its own (`<id>` and `<id>#live`), checked with the server on its own, and the
photo counted once, when both are there. The clip goes even with videos left
out. Checked only that nothing else changed: the simulator's sample photos
hold no Live Photo, and a rerun of the backup test sent nothing new and
passed. The owner's phone, with its ten, is the test.

Verified end to end on a throwaway Immich 3.2.2 that a test SoundStorm
provisioned, sharing one pictures folder: a member saw nothing, backed up a
photo (to `Personal/alice/2025/07/`), a second send was skipped, a dragged-in
photo went to her folder, she saw her two and the owner all four, she was
refused the owner's photo, the owner set her limit and she could not, and
removing her kept her folder.

**Sending photos to someone** (2026-10-04, the owner's design; albums and
sharing albums come after): in Photos, hold a tile to select it (the
timeline selects as a shelf's list does now, `selectRoot`), tap or drag to
add more, and the selection's menu - or one photo's - has **Send to...**,
listing everybody else here with Photos. Nothing lands in their photos yet:
what was sent waits in `library/.photo-inbox/<send id>/` (hard links where
the filesystem allows, so no space is taken; a copy otherwise), with its
date file and a Live Photo's moving part, and a card at the top of their
Photos tab says "Gabriel sent you 12 photos", a strip of them (previews
through the sender's own photo account), **Add to my photos** or **No
thanks**. Taken, each becomes a copy of their own in their folder, filed by
date as any upload is (`datedPhotoKnown`: the date inside it, else the date
file that came with it, else the date the sender's library shows, else its
name) - a copy, not a share, so their folder holds it if it is ever taken
out, and the sender deleting theirs changes nothing. A photo they already
have is skipped. Only the sender's own photos can be sent (their photo
account answers for nothing else); 500 at a time, 100 sends waiting per
person, unanswered ones go after 30 days; a member's photo space is
checked on taking; removing a person removes what waits for them
(`photosend.go`, `photo-sends.json` in the state folder). Checked on the
throwaway test server: smoke selected two in the timeline by a hold and a
tap, sent them to alice, alice's card showed both, and taking them filed
them in her own dated folder with the sender's date beside them. Not
checked: a Live Photo (the test server has none), the apps' own screens
(they show the page). Videos too: a photo and a video sent together showed as
"1 photo and 1 video", the video's preview with a play mark, and taking it
filed the clip in her dated folder with its date file.

**Albums** (2026-10-04, the owner's design; sharing one is next): anybody
with Photos makes albums of their own photos and videos - **Add to album...**
in one photo's menu or a selection's (an album, or a new one by name) - and
finds them under the Photos tab's **Albums** pill: cards with a cover, how
many and when, **New album** above. An album opens as a page of the
timeline's tiles, selectable the same way (`.selectable`, `selectRoot`),
with Rename, Delete album (a second tap; the photos stay) and **Remove from
album** in a photo's or the selection's menu. They are **Immich's own**
(`source.PhotoAlbums`, `immich/albums.go`), made in the person's own photo
account, so as private as the photos, and Immich's album sharing is there
for the next step; the routes are `photoalbums.go` (`/api/photos/albums`,
`/{id}/add`, `/{id}/remove`, PATCH and DELETE, limited like list writes).
Read from 3.2.2's own code, not its docs: `POST /api/albums {albumName,
assetIds}`, `PUT`/`DELETE /api/albums/{id}/assets {ids}` answering a
success per photo, and an album's photos by a metadata search with
`albumIds` - without the library filter, since a shared album will hold
another's. `TestAlbumsAreTheBackendsOwn`; the after-deploy check makes one,
opens it and deletes it. Checked on the test server: an album made from a
selection, opened, renamed, a photo taken out and another added from its own
menu, deleted, and alice seeing none of smoke's. Not built: a cover chosen
by hand, albums on the Apple TV, an album downloaded whole.

**Sharing an album** (2026-10-04, the owner's design): **sharing is seeing,
Save to my photos is keeping.** An album's owner taps **Share** on its page:
a menu of everybody else here with Photos, "They can only look" or "They
can add photos" to choose first, and who is already in it (can look, can
add, waiting) - a tap and another stops sharing or takes an invitation
back. Nothing is pushed on anyone: an invitation is a card at the top of
their Photos (and Albums), "Gabriel shared the album Family trip with you",
its first photos (through the sharer's own account), **Add to my albums** /
**No thanks**; only accepting puts them in it (`photoshare.go`,
`photo-shares.json` in the state folder, thirty days, fifty waiting per
person, gone with a removed person). In it they see its photos while they
are in it - **the files stay the owner's**, never copied, so they are not in
the recipient's folder and go if the owner takes them out - and with "can
add" their own photos join it, staying in their own folder. **Save to my
photos** (an album shared with you: the page's Save all, a photo's or the
selection's menu) makes real copies in their own folder, filed by date and
duplicates skipped as Send to's are (`holdPhotos` and `fileHeld`, split out
of Send to; `immich.VisibleItemFiles`, the one file lookup that takes a
photo outside the asker's own library - only for copying, never deleting or
sending). **Leave album** for the one it was shared with. The photo library
does the sharing: Immich's album users (`PUT /api/albums/{id}/users`,
roles editor and viewer, `DELETE .../user/{id}` and `.../user/me`, read
from 3.2.2's code; its albums name the owner among `albumUsers` as role
"owner"); a member's photo-library id is the one kept with their photo
account (`Identity.RemoteID`), the owner's is asked once. Checked on the
test server with two accounts: smoke shared Family trip with "can add",
alice's card showed it with previews, she accepted, saw its photos with
Save all and Leave, added one of hers (smoke then saw 8), Save all copied
the 3 she did not have into her dated folder and skipped the 4 she had, and
when smoke stopped sharing it left her albums. Not checked: a Live Photo
saved, the owner sharing with themselves through a member (refused).

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

**Reading while listening.** Opening a book no longer stops the music or
the audiobook - reading to something is the point. The mini-player floats
over the reader (above its z-index, at the very bottom since the tab bar is
under the reader) and the reader's page is laid out above the card, so no
line hides behind it; Now Playing still opens over both. A film still stops,
since it would play behind the book.

**Read Along** is a shelf in Books of what somebody has as both an ebook
and an audiobook. `GET /api/books/pairs` lists both shelves through the
registry (so access applies) and matches on a key with the edition noise
taken out - anything bracketed (ASIN, Unabridged, Full-Cast Edition), a
subtitle after a colon, a trailing "Book 1", curly quotes, punctuation, a
leading article - plus a shared author surname, reading "Rowling, J.K." and
"J.K. Rowling" alike. Nothing is stored. Each edition of an audiobook is its
own pair. On the real library: 1,663 ebooks and 113 audiobooks gave four
pairs, and a scan for same-author near-misses found none. The card's cover
reads along (starts the audiobook, opens the book over it); Read and Listen
do one each. The pill only shows once there is a pair.

**Authors and Series** are pills in Books, over both book shelves at
once. `GET /api/books/authors` and `/api/books/series` (with `?key=` for one
author's or series' page) list the shelves through the registry, as Read
Along does (`listBookShelves`, shared), and group on each request - nothing
stored. Names are grouped on a key that reads "Herbert, Frank" and "Frank
Herbert" alike and shown in the reading form, sorted by surname. A comma is
a list (Audiobookshelf joins co-authors with one) unless one side of a
single comma is one word, the sort form. Series come from Calibre's series
and index, or Audiobookshelf's seriesName, which carries the number
("Dune #2"); only a book's first series. An author's page is their series,
each in order, then their other books.

**Every tab has a Favorites pill** - `fav-music`, `fav-watch`, `fav-books`,
`fav-photos` - showing only the favorites of that tab's kinds (Music's is in
its own row of pills). Home has a row per tab likewise - Favorite songs,
films and TV, books, photos - each See all opening that tab's pill. Music's
Mixes lead with **Your favorites**: every hearted song, shuffled, and all
of them rather than a mix's hundred.

## Making an audiobook from an ebook, and an ebook from an audiobook (2026-10-03)

Asked for after ElevenLabs came up: both directions, on the box, nothing
sent out. Decided with the owner:

- **Make an audiobook** in an ebook's hold menu, **Make an ebook** in an
  audiobook's. **Only the owner may start one, unless the owner gives a person
  permission in People** (a book ties the box up for hours).
- **Already have the other version?** It says so ("You already have the
  audiobook of this, narrated by ...") and offers **Make it anyway** or **Use
  the one I have**; the matching is Read Along's (title and author).
- **One at a time, in the background**: a queue in Settings with how far each
  is and the time left, and a message when it is on the shelf.
- **A made audiobook is always marked**: "AI voice" under its title, its
  narrator the voice's name, on the Audiobooks shelf like any other. **A made
  ebook** is marked "From the audiobook", chapters from the audiobook's
  marks, no original formatting.
- **Both come synced for Read Along** - SoundStorm knows which words each
  moment of audio is (the voice's own timings one way, Whisper's word times
  the other) - with no Storyteller step.
- **Several versions of one book**: Read Along shows one card per book; the
  first time a book has more than one version it asks which (audiobook:
  narrator / AI voice; ebook: original / from the audiobook), the best guess
  picked (a real narrator, the original ebook, a pair already synced), and
  remembers it **per person** until changed from the card's menu (**Change
  versions**).

How: two backends, run unmodified in their own containers and provisioned like
the rest. **Kokoro** for the voice (82M, Apache 2.0, about 50 voices, no
cloning; `remsky/kokoro-fastapi-cpu`, a 1.5GB image) - measured on this PC with
4 cores: chapter 1 of The Richest Man in Babylon, 2,105 words and 11m23s of
speech, in 2m35s, 4.4 times real time, so on the ME Mini's slower cores about
twice real time, a 10-hour book overnight. **Whisper** for the other way (MIT;
Storyteller already runs it, base.en, about 40 minutes for a 10-hour book
here). Considered and left: Chatterbox Turbo (MIT, clones a voice from 5-10
seconds, beat ElevenLabs in blind tests) and Qwen3-TTS (Apache 2.0, January
2026) both want a graphics card the box does not have; XTTS-v2 and F5-TTS are
non-commercial. Order: Make an audiobook, then Make an ebook, then the
version picker. Personal use of books somebody owns; nothing is shared.

**Make an audiobook is built (2026-10-03)** (`internal/voices`,
`httpapi/voices.go`, the `kokoro` service in compose, pinned by digest). An
EPUB's hold menu offers it to the owner and anyone given **Can make
audiobooks** in People (`User.CanMakeBooks`); a voice list (English voices,
each with a speaker button playing a sample, made once and kept under the
state dir's `voices/samples`); a book with an audiobook already gets a 409
naming it, and the page asks Make it anyway or Play the one I have. The
queue is kept in `voices/jobs.json`, one book at a time; each chapter (a
spine document of 40 words or more, titled by its first heading, else its
page title) is one request to Kokoro and one MP3, tagged ID3v2.3 with the
book, author, track and `TCOM` "AI voice: Heart" (Audiobookshelf's
narrator); chapters already made survive a restart. Made in the state dir,
then moved through a hidden `.making` folder to `audiobooks/<Author>/<Title>
(AI voice)/` with the ebook's cover, and the shelf rescanned. Settings,
Making audiobooks, shows each with its share done and the time left (from
150 words a minute of speech against the pace measured). Checked on a
throwaway stack: a two-chapter EPUB became two tagged MP3s in that folder.
**Not yet built:** marking "AI voice" under the card (the narrator tag
carries it), the voice's word timings for Read Along (the made audiobook is
not synced yet), the version picker.

**Make an ebook is built (2026-10-03)** (`voices/whisper.go`, `cut.go`,
`ebook.go`, `handleMakeEbook`; the `whisper` service in compose,
whisper-asr-webservice pinned by digest, faster-whisper base.en - its model is
fetched once on first start into `whisper-models`, so a box with no internet
on its first day cannot do this yet). An audiobook's hold menu offers it to
the same people (People's switch is now "Can make audiobooks and ebooks");
an ebook already paired with it gets the 409 and Make it anyway or Read the
one I have. The recording is sent to Whisper in pieces of about ten minutes,
never re-encoded: an MP4 (m4b, m4a) as fragments of its own samples through
`mp4hls`, an MP3 cut at frame boundaries, anything else whole up to 200MB; a
word cut at a piece's edge is the cost. Each piece's words are kept in the
work folder, so a restart carries on. The EPUB is a chapter a file from the
audiobook's chapter list (else its files), paragraphs at a 1.2s pause or 160
words, each sentence a span - and **it carries its own timeline**
(`META-INF/soundstorm-timeline.json`, every sentence's moment and the
audiobook's folder), read through `epub.Open` and its guards. Titled "<Title>
(from the audiobook)" (`bookKey` drops the brackets, so it pairs with its
recording by itself), filed `ebooks/<Author>/<Title> (from the audiobook)/`.
Read Along takes such a pair as synced with no Storyteller at all
(`madeTimeline`: only titles ending so are opened; `/api/readalong` answers
the book itself and its timeline; the auto-sync leaves it alone). Checked on
a throwaway stack with a generated two-chapter m4b (index at the end, as
Audible's): written down in seconds, chapters split at the mark, one word
misheard ("gulls" as "girls"), paired and ready, and in Chrome the page
turned to Chapter Two as the voice reached it with the sentence lit. Whisper
measured 20s of speech in half a second; a real 10-hour book is not yet
measured. **Not built:** the version picker (two ebooks of one recording show
as two Read Along cards for now).

## Read-along: the page follows the audiobook

Asked for as pages turning by themselves with the audiobook. Matching a
recording to its text sentence by sentence is forced alignment - transcribe
the audio, find it in the book - which is the expensive, ML-shaped layer this
project does not own. So it is a backend: **Storyteller** (MIT, Docker),
pinned by digest to web-v2.14.21 because its API moves between releases.
SoundStorm provisions it, hands it pairs from Read Along, and reads the
result. A cheap alternative - chapter-proportional guessing - was offered and
declined: a page off by one reads as broken.

**SoundStorm keeps playing the audiobook through its own player.**
Storyteller's output is an EPUB 3 with media overlays and its own copy of the
audio inside; playing that would lose the lock screen, the dock and
Audiobookshelf's listening position. So only its *text* is used - every
sentence wrapped in a span with an id - plus its SMIL timings, converted to the
audiobook's whole-book timeline (`storyteller.Timeline`). Four times a second
the reader finds the sentence at `elapsed()`, turns to it with foliate's own
`resolveNavigation` + `renderer.goTo` (the same calls its read-aloud uses),
and lights it with a class. Turning the page by hand stops the turning for
twelve seconds.
**A long sentence turns the page as the voice crosses it** (2026-10-03, the
owner's report: run-on sentences turned late, the page waiting for the next
sentence to begin): within a sentence over two seconds, the voice's place is
estimated as the same share of its letters as of its time, and the page
scrolls to the one holding that letter (`followWithinSentence`, foliate's
`scrollToAnchor` with a collapsed range). Not checked against a synced
book here - none on a test server; the owner's phone is the check.

**The timing conversion rests on how Storyteller cuts audio**, read from its
source and then checked end to end: files are numbered in name order
(`Audio/0000F-0000C`), and each is cut at its chapter marks, with long
chapters cut again at 2 hours by default - `maxTrackLength` is set huge at
provisioning so chapters are the only cuts. Piece C of file F therefore starts
at that file's C-th chapter, which is Audiobookshelf's chapter list
(`source.AudioLayouter`). Where the pieces and chapters do not line up, there
is no timeline: the book opens and says it cannot follow, rather than
following the wrong sentence.

Provisioning, all checked live:
- The first account is a Next.js server action on `/init`, which works as a
  plain multipart form. The action id changes per build, so it is scraped off
  the page each time.
- `/api/v2/token` (form data) gives a thirty-day session; `/api/v2/token/app`
  trades it for a 35-year one, and **reads only the `st_token` cookie**: a
  bearer token is sent to the login page. That cost a test cycle.
- Settings: synced books written inside its own `/data` (`INTERNAL`, never
  beside the source - the default writes into the library), `base.en` (tiny
  missed a chapter of synthetic speech), cache cleaned after, OPDS off.

**Two steps to add a pair, because the one-call form is broken.** Naming the
EPUB and the audio in one `POST /api/v2/books` fails when they are in
different folders - each tries to create the book, the second hits a duplicate
id. So the recording is added by reference (read-only mount, never copied)
and the ebook is uploaded into that book over tus. That also keeps the shelf's
file untouched when an EPUB 2 - most of a Calibre library - has to be
upgraded: the upgrade happens to Storyteller's copy. A synced book is found
again by its audiobook's folder (`Extra["folder"]`), so nothing is stored in
SoundStorm.

Measured: on the development machine a 10-hour book should take about 40
minutes (8 minutes of speech in 30s with base.en). A sync queues; one runs at
a time. Disk: roughly the audiobook's size again in Storyteller's volume,
since the synced EPUB embeds the audio. Idle RAM about 300MB, 0.7GB syncing.
The image is 2.9GB. It still syncs its changelog from GitLab on a daily
schedule despite `STORYTELLER_SYNC_CHANGELOG=false`, which only stops the one
at start. Its secret key defaults like Immich's database password; both are
reachable only on the compose network.

**Read along as one line** (2026-10-03, the owner's design): a double tap
on the book while it reads along switches to the book as one endless line of
big text moving right to left with the voice - one colour, full brightness
across most of the screen and fading only in the outer fifth either side (a
sentence lit at a time jumped; then more of it bright, the owner said) - and a double tap goes back to the pages (`toggleLine`, `drawLine` in
reader.js; kept on the device as `soundstorm-readalong-view`). **It is a
stream, never stopping** (the owner, after it stopped between sentences):
each frame its place is a monotone cubic through every sentence's start (time,
place on the line) - the pauses crossed, not waited out, the speed eased from
sentence to sentence, never backwards (`linePlace`) - and the player's clock is
carried on between its reports (`lineClock`), held at the middle.
Measured over 30s of playback: still for at most one frame (19ms), no step
back, 150-260px a second; the sentence text is read from the book's own
chapters (`section.createDocument`, never shown, only `textContent` used),
about twenty sentences around the one being read. The pages carry on
following behind it. A double tap is two touches within 300ms, each under
250ms and 12px, listened for in each chapter's document (the view's `load`)
and on the line; a mouse's double click counts. Checked in Chrome at phone
size on a made book: a finger's double tap into the line, the lit sentence
moving with the voice into chapter two and to the book's last sentence, a
double tap back to the pages. **For the Mac:** the Apple TV's reader could
offer the same as a look (its timeline and sentence text are already there).
**And a word at a time** (`drawWord`), the third view the double tap goes
round to (pages, line, word, pages), tried after the owner found the moving
line tricky to read - moving text gives the eye nothing still to land on.
Nothing moves: the word being said large and bright in the middle, the one
before and after faint either side. The timeline knows only sentences, so the
word is the voice's share of the sentence's time, words weighted by their
letters plus two; between sentences the last word holds. Checked on a made
book in Chrome at phone size: the words stepped with the voice about every
0.2s ("He had a kettle, a chair and a window..."), held through the pause,
and a double tap went on to the pages.
**Both views for any ebook, at your own speed** (the owner's asking): with no
audiobook the same double tap goes round pages, line and word at a time, and
the pace is words a minute on a bar at the foot (play or pause, minus and
plus by 25, 100-800, 250 to start, kept as `soundstorm-read-speed`). It starts
at the first letter on the page (the view's `lastLocation.range`, counted in
the chapter's text nodes, styles and scripts left out - the same walk for the
page shown and for the chapter loaded on its own) and going back to the pages
turns to the word reached (`renderer.goTo` with an anchor so many letters in).
A word at a time gives a word ending a sentence 1.2 more units, a comma 0.4,
a word over eight letters 0.3, the speed the chapter's average; **the line
glides at a constant rate** instead - so many letters a second, the same
average (the owner) - measured 240-290px a second every half second, the
spread being letter widths. Both keep the place in the word timing's units, so
switching views keeps it. Chapters with no words are
skipped; at a chapter's end it carries on into the next. Checked on the
starter ebook in Chrome at phone size: a finger's double tap into the line,
350 a minute gave 19 words in 3s at a word at a time, pause held, and after
15s at 800 the pages turned to exactly the words reached
("...an elaborate salute. "Yet,"). A PDF has neither (it is the browser's
viewer).
**The endless line glitched every few seconds** (2026-10-04, the owner): it
moved word by word - letters a second, each word's span stretched to fit, so
a wide word slid past faster than a narrow one - and was thrown away and laid
out again every 45 words. Now it moves at one speed in pixels (words a minute
times the line's average word width, eased), words are added ahead and
dropped behind with the position moved by exactly what went (nothing on
screen moves), and it runs on from one chapter into the next
(`drawFreeLine`). Measured over 15s on the starter book, speed against its
median in 100ms windows: worst 27% and one jump before, worst 1% after; at
800 words a minute over 25s, worst 2%, the drops invisible.

The reader's **Highlight** button, shown only while reading along, turns
the lit sentence off and on; off, the page still turns with the voice. The
choice is kept on the account like the pill order. So is **audiobook speed**
(Now Playing's speed pill, audiobooks only, 0.75x-3x): set as
`defaultPlaybackRate` as well as `playbackRate`, because giving the player
the next chapter file resets the rate to the default. Read-along's timeline
reads the player's own clock, so it follows at any speed.

**A missed match can be made by hand.** Holding an ebook offers the owner
**Pair with its audiobook** (and an audiobook, its ebook): a search in the
menu, started at the title with its edition noise taken out. Kept as
`manualPairs`, the same key shape and the same kind of decision as
`notPairs`; pairing takes a key off `notPairs`, and Not the same book takes
it off `manualPairs`. A new pair kicks the auto-sync.

**A failed sync does not unmatch.** Failure is usually Storyteller, not the
match - a restart mid-job, a model download, a full disk - and a card
offering Try again is recoverable where a vanished pair is not. Not the
same book is the owner's call. A synced card says nothing under it: synced
is the normal state, so only waiting, working and failed get a line.

**A wrong match can be undone.** Title and author can match two different
books, so a Read Along card's hold menu offers the owner **Not the same
book**: the pair is left out of the list for everyone (and so never synced
by itself), and whatever Storyteller made of it is deleted - its own copies
only; its delete touches nothing outside its storage, and the recording's
folder is mounted read-only to it. Kept in state.json as `notPairs`, a
decision like `StarterInstalled`, not a fact about the media. Undo in the
toast. **Owner routes must be mounted as well as registered**: the owner
mux answers only for paths also handed to it with `guarded.Handle`, and
the read-along switch in Settings was registered, not mounted, and
answered 404 unnoticed until the test for this feature went through the
same door. `TestOwnerSettingsAnswer` now asks every owner setting.

**Books sync by themselves**, as soon as there is both an ebook and an
audiobook of one: a background look two minutes after start, every half hour,
and three minutes after audiobooks or ebooks are scanned (the audiobook server
indexes in its own time). On unless the owner turns it off in Settings, stored
as `readAlongManual` so absent means on. A pair it cannot start is not retried
until the setting is turned on again or the server restarts; one Storyteller
already has, finished or failed, is left alone - a failure retried every half
hour would be the better part of an hour of CPU, repeated. On the real
library the first look started all four pairs within two seconds, and
Storyteller began cutting the 47-hour Long Voyage Home at its chapter marks.

Verified end to end on an isolated stack with synthetic speech: an EPUB 3 with
two MP3s, and an EPUB 2 with a chaptered M4B. Both synced through SoundStorm's
API; each timeline ended exactly where its recording did; in Chrome the lit
sentence moved with the narration and the page turned to chapter two on its
own.

## Verified against live servers

These were checked on a running stack, not inferred. Re-verify if versions move.

- **Jellyfin 12.1.0 rejects `X-Emby-Token` and `?api_key=` with 401.** The only
  form it accepts is `Authorization: MediaBrowser ... Token="..."`. Most docs
  and every older client still show the other two. This is why
  `source.Target` carries headers rather than just a URL.
- **Jellyfin ignores a query parameter it does not know, silently, with a 200.**
  So a 200 is not evidence a filter was applied. `IsVirtualItem=true` returned a
  real film - identical to a parameter name invented for the test - while
  `IsMissing=true` returned nothing for the same film and `IsMissing=false` kept
  the film, an episode and its parent series. That last case is the one that
  matters: a Series has no file of its own, and a filter that dropped it would
  empty the television shelf. Test any filter by asking for the *opposite* and
  checking the count actually changes; the same trap as `ND_SCANINTERVAL`.
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
- **The page-turn arrows are hidden on touch, and swipe is why.** Measured,
  not assumed: `scripts/reader-swipe-check.js` swipes the reader and watches
  the fraction on the relocate event, and swiping reached exactly the
  positions the arrows reach, three times out of three in both directions.
  Two 56px arrows were 29% of a 390px screen.

  A relocate firing is **not** a page turn - it fires on resize too, and the
  first version of that check passed a tap that moved nothing because it only
  counted events. The fraction has to change, and change back.

  The exception is not optional: `paginator.js` handles touch, and
  `fixed-layout.js` contains no touch handling whatsoever. For a comic or an
  illustrated book the arrows are the only way to turn a page on a phone, so
  `reader.js` sets `.fixed-layout` from `view.isFixedLayout` after `open()`
  and the CSS keeps the arrows for those. The rule is `pointer: coarse` rather
  than a width, because a narrow desktop window still has a mouse and cannot
  swipe at all.

  Re-run that script if foliate-js is ever updated. It is vendored third-party
  code and this is a behavior of theirs we are now depending on.
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

## The iPhone app (`ios/`)

The owner wanted SoundStorm on the App Store rather than only as a home
screen web app. Three shapes were weighed:

- **A thin wrapper**: a native shell that loads the server's own web app.
- **A wrapper whose audio is native**: the same shell, with music and
  audiobooks played by iOS itself rather than by the page.
- **A fully native app**: rebuild in Swift.

**Chosen: the second, built in two stages.** Fully native was rejected for the
reason every "client app" is the graveyard (see what SoundStorm does not do):
`app.js` is the whole product across five media types, and a Swift rewrite
means doing every feature twice from then on, with Android still on the web.
What only native can give - CarPlay, audio iOS will not stop, the lock
screen - is all audio, so only the audio goes native. The shell can grow
screen by screen later if that ever looks worth it; nothing commits it to.

**The APK note under Music does not transfer.** That one declined an Android
WebView app because it would have cost lock-screen controls and background
playback to fix a cosmetic problem, on Android's WebView, which lacks the
Media Session API. An iPhone app's WKWebView is Safari's engine: with
`UIBackgroundModes` audio and the `.playback` audio session (both set), the
page's `<audio>` keeps playing locked. Whether the lock screen then shows the
page's Media Session title and controls is **not yet checked on a real
iPhone** - the simulator cannot say. That result decides how much of stage
two is needed.

**Stage one (done): the shell.** Plain Swift, no Capacitor: Capacitor is
built to bundle a web app, and this one lives on each person's own server,
while stage two is Swift anyway. What it does:

- Asks for the server's address on first launch (every install is somebody's
  own) and checks `/healthz` answers like SoundStorm before keeping it, so a
  typo is caught at the door, not as a strange page. No scheme means https.
- Shows the page full screen, laid out under the status bar with the same
  `env(safe-area-inset-*)` the installed web app already uses.
- Shows `alert`/`confirm`/`prompt` - a WKWebView shows none of them unless its
  app does, and SoundStorm asks before removing downloads.
- Sends links off the server, and `target="_blank"`, to Safari.
- Sets `window.soundstormApp` before the page runs. `app.js` uses it to show
  **Change server** beside Sign out, which posts `changeServer` to the app.
  This is the only place the web app knows it is inside the app; keep it
  that way until stage two needs more.
- Allows plain http only on the local network (`NSAllowsLocalNetworking`).

**What the shell does not do, and why that is expected:** no service worker.
WKWebView only runs one for App-Bound Domains, a fixed list of at most ten
set at build time, and every install has its own address. So opening the
app with no connection shows the "can't reach" screen rather than the
downloads - native downloads are stage two's, not a bug in stage one.

**Stage two (next): native audio** - an `AVPlayer` behind a bridge the page
talks to in place of `<audio>`, for Now Playing, remote commands and later
CarPlay. `app.js`'s audio is not one element: crossfade uses a second one,
gapless preloads into a Blob, leveling sets `volume`, audiobooks set
`playbackRate`, and downloads play from the Cache API through `urlMap`. The
bridge has to answer for all of those.

**Stage two, for songs and books: the app's own player (2026-10-02).**
Reported from a real iPhone: paused on the lock screen, the music could not
be started again there. The page played it in the web view, and iOS
suspends an app playing nothing a few seconds after a pause - the web view
with it - so the lock screen's play had nothing awake to answer. So the
iPhone app does what the Android app did in 0.9: `NativeAudio.swift`, an
AVQueuePlayer behind the same stand-in on `<audio id="audio-player">` (the
Android page script's, copied into `WebViewController.pageScript`) and the
same "audio" messages, so the page needed no change - it already speaks to
a native player when `soundstormApp.nativeAudio` is set. Play, pause and
the seek bar on the lock screen are the player's own (`MPRemoteCommandCenter`),
answered with the page asleep; next, previous and a book's jumps go to the
page's media session handlers, which the page script now also passes to the
app (`media` messages) for the lock screen's title, artist and cover. The
songs ahead are handed over (`upcoming`), so the player moves into them by
itself. A downloaded song (blob:) still plays in the page, through the web
view's own media session. The session cookies are given to each item, as on
the Apple TV. Checked in the simulator with `-audioTest <path>` (debug
only): a song played through the app's player, paused, and the lock
screen's play started it again from the same moment, the page following.
Not checked: a real lock screen, the cover there, a call or alarm, a
downloaded song.
**The looks stuttered on the iPhone** (2026-10-06, the owner's report): the
app reports the song's place twice a second, the page set its clock to each
report, and a report is a few hundredths of a second old on arrival - so the
clock stepped back twice a second and every animation with it. While a song
plays on, a report less than 0.35s off now eases the clock 15% of the way
(`__soundstormAudio` in `pageScript`); anything more, a seek or a stall, is
taken at once. **For the PC:** Android's `PageScript.kt` (around line 308)
sets the clock the same way and wants the same change.
**Music stopped mid-song on the iPhone, and play started a different song**
(2026-10-06, the owner's report). The app's player told the page a song had
ended whenever its current song changed - including when the page changed it
itself: a song chosen, and the page's own recovery starting the same song
again after twenty seconds of loading (`audioRecover`, the `retry=` address).
The page took the false "ended" and moved on to the next song in its queue.
Now the page's own changes are not counted (`tookOver` in `NativeAudio`); only
the player moving into the next song by itself is - as Android has always
done (`MEDIA_ITEM_TRANSITION_REASON_AUTO`), which is why Android never showed
it. Checked in the simulator against a stand-in server: a song started again
partway through gave no "ended" and played on from there; a song played to
its end gave one, and the next played. Also seen there: when a connection
goes silent (not closed), iOS's player waits a full minute before asking for
the rest itself, which is why the page's own recovery matters.
**And the looks skipped for a while after the app opened** (2026-10-06,
the owner: Waves and Kaleidoscope skipped, then stopped skipping; reopening
the app brought it back). Photo backup checks every photo with the server on
each run, and every opening started one; its Photos lookups (the roll, each
photo's files and their sizes, 60 at a time over ~1,600 photos) ran on the
main thread, the app's default - the thread a web view's frames pass through
on an iPhone. They run off it now (`Task.detached` in `backUp`, `Item` and
the lookups `nonisolated`), and opening the app starts no run within ten
minutes of the last whole one (`lastWholeRun` is kept in UserDefaults; new
photos still start their own). Built; not run: the Mac's test server has no
photo library, so its page offers no backup. `testPhotoBackupSendsTheCameraRoll`
now opens the folded Your photos card, but needs a server with photos.
**Still skipping after that, and the music stopping for 5-10 seconds at
home** (the owner, the same evening): the skipping settles by itself a while
after the app opens. Not reproducible on the Mac, so the iPhone app now keeps
a playback log as Android's does (`PlayerLog.swift`, the last 600 lines): the
app opening and going away, audio routes and interruptions, the page's
commands, the player's states with iOS's reason for waiting, stalls, network
errors and failures, songs moved into by itself, photo backup runs and how
long they took - and, from the page script while Now Playing is open, every
five seconds the frames drawn, how many came late (over 25 and 50ms) and the
worst gap. Sent with the page's existing **Sound cut out? Send a report**
(Playback on this device; the developer's install only, `soundstormApp.playerLog`)
to the server's `diagnostics/`. **For the PC:** read the owner's report from
there and look for what is busy while the frames come late, and why the
player waited.
*(Read on the PC, 2026-10-07: the first report. **The dropout was the
server's**: on the slow-connection setting songs are converted (128 kbps),
and Navidrome announces a conversion it has not made before by an estimate
of its size - a few percent too big (on the test server 553,451 bytes
promised, 540,552 sent; from its cache, the second time, exact). The player
waited at 192s of 212 for bytes that never came, until the page's recovery
started it again 23 seconds later; the server's log had "stream copy ended
early: unexpected EOF" for that song. The rest of such a song is now sent as
zeros (`padConverted` in `stream.go`, a shortfall of up to a fifth), which a
decoder passes over, and the song ends. Checked on the test server: a fresh
conversion arrived at its full announced size, ffmpeg decoded it, Chrome
played it to its end and fired ended. Not checked on the iPhone itself.
**The frames were fine in that report** - 300 every five seconds, the
worst 17ms - so the skipping looks did not happen that time. **For the
Mac:** the log shows a photo backup run starting every four seconds the
whole time the app was open, each ending at once - cheap, but the page's
backup status messages are still starting runs (`pageAsked`).)* *(Done on the Mac, 2026-10-07: a run
that cannot go - Wi-Fi only off Wi-Fi, or charging only off the charger - is
not started at all (`start`), and one starts by itself when the phone joins
Wi-Fi or is plugged in (`conditionsChanged`); and the page's messages start
at most one run a minute whatever ended the last one early (`lastStarted` in
`pageAsked`). The log says "photo backup: waiting for Wi-Fi" (or the charger,
or files still with iOS). Built; not seen running: the simulator would not
give the app photo access, so backup never starts there.)*

**The page moving itself was sent to Safari** (first TestFlight build,
2026-09-30). Opened by its away-from-home name at home, the page moves to
the install's home name (`moveToSecureName`: the server offers it to any
page not already on it), and the app took that for a link out - the same
bug the Android app had from a LAN address. The iPhone app follows a move to
https on `<id>.home.soundstorm.dev`, same port, where `<id>` is the current
name's; from a LAN address any home name is accepted, as on Android. It
differs from Android in one way on purpose: **it keeps the net name saved**
and only follows the move for this launch. A phone leaves the house; saved,
the home name would stop the app working the moment it did. Only a move off
plain http is saved, since the secure name is strictly better than an IP.
Messages from the page are now accepted only from where the page is, since
anybody can get a `*.net.soundstorm.dev` name.

**The status bar hides while Now Playing is open, and only then** (the
owner's choice, 2026-09-30). Android hides both bars everywhere; an iPhone
has no swipe that brings a hidden status bar back for a look at the time,
so everywhere else it stays. The app watches the page's body for `np-open`
from its document-start script rather than being told by `app.js`, so it
needed no web change and no deploy.

Things that bit while building stage one:

- **A centered `UIStackView` measures a multi-line label as one line**, and
  truncates it, until `preferredMaxLayoutWidth` is set from the laid-out
  width (`ConnectViewController.viewDidLayoutSubviews`).
- **In the UI test, the keyboard's Previous/Next/Done bar sits over the web
  form's submit button**, so a tap on the button lands on the bar. Pressing
  Return submits the form instead, which is also what a person does.
- **`simctl` cannot type or tap**, and scripting Simulator through System
  Events waits on a macOS permission prompt nobody sees. The XCUITest in
  `ios/SoundStormUITests` is how the app gets driven; it skips itself when no
  server answers at `localhost:8080`.
- **The server address is stored as a string**, not with
  `UserDefaults.set(_:URL)`, so it can be given as a launch argument:
  `xcrun simctl launch booted dev.soundstorm.app -serverURL http://localhost:8080`.

## The Apple TV app (`ios/SoundStormTV`)

A second target in `ios/SoundStorm.xcodeproj`, native SwiftUI, since tvOS
has no web view (see "TV apps" for why and what it builds to). Same bundle id
as the iPhone app, `dev.soundstorm.app`, on purpose: Apple's Universal
Purchase makes the two one App Store listing. `ios/Shared/` holds what both
use (`ServerAddress`).

**Step one (2026-09-30): sign in and music.** Connect and sign in (the
setup-code first account is left to a browser: easier than typing a code on a
TV), a side bar of Home, Music, Search and Settings, albums and playlists, and
Now Playing. What it talks to is the page's own /api:

- **The session is the server's cookie**, in the shared cookie store.
  URLSession sends it; **AVPlayer does not read that store**, so every stream
  gets the cookies explicitly (`AVURLAssetHTTPCookiesKey`, `API.cookies`), or
  the server refuses it. Covers go through URLSession (AsyncImage), which does
  send them. No Origin header is sent, which is all the server checks on a POST.
- **Music is AVQueuePlayer with the next song loaded** behind the one playing
  (gapless, like the page's preload). The system's Now Playing and remote
  commands are fed from `Player`. A song counts (`POST /api/history`, which is
  also the scrobble) at half its length or four minutes, as the page counts it.
- **Now Playing follows the TV rules**: no play, previous or next buttons; OK
  plays and pauses; left and right change song; down lights the timeline,
  where left and right move ten seconds; up lights the arrow that puts it
  away; the timeline and arrow fade after four seconds, and faded, OK and
  left and right still act. It is one focusable view that takes the presses
  itself (`onMoveCommand`), so the focus engine cannot wander. It opens on
  play; the side bar's first entry while something plays reopens it.

Checked on the tvOS simulator against a stand-in server (the Mac has no
Navidrome) that, like the real one, refuses a stream or cover without the
session cookie: the cookie went with every song and cover request, songs came
by byte ranges, song 2 loaded while song 1 played and took over at its end,
and song 1 was recorded at 13 of its 24 seconds with no Origin header.
**Not yet checked: the remote's presses** - the simulator run had no remote,
so the Now Playing rules above are written but untested.

Things that bit:

- **A closure MediaPlayer calls on its own queue must be `@Sendable`.** With
  the target's default of main-actor isolation, the artwork closure given to
  `MPMediaItemArtwork` inherited it, and Swift's isolation check stopped the
  app the first time Now Playing asked for the cover. Any AVFoundation or
  MediaPlayer callback not on the main queue needs the same.
- **AVPlayer decides the format from Content-Type.** Go's `ServeFile` called
  an .m4a `audio/mp4a-latm`, and AVPlayer answered -11828 (format not
  recognized) while downloading it happily. The real server passes on
  Navidrome's `audio/mp4`, which works; a stand-in must send the same.
- **The Android TV banner's font is Windows-only** (Segoe UI Bold Italic), so
  `make-icons.py` leaves that banner alone where the font is missing rather
  than redraw it in another and change it on every run on the Mac. The Apple
  TV icons (a layered cloud, 400x240 and 1280x768, and the Top Shelf) are
  drawn by the same script.

**Release builds are signed by hand, with a distribution profile.**
Automatic signing archives with a development profile first, which needs a
registered Apple TV, and the team has none (the one it is for is in another
city, so it cannot be paired). The target's Release uses the "Apple
Distribution" certificate and the profile named "SoundStorm tvOS App Store",
made on developer.apple.com (tvOS App Store Connect, `dev.soundstorm.app`);
`ios/scripts/export-testflight-tv.plist` exports with it. When the profile
expires, make a new one under the same name and nothing here changes. An
unsigned archive (`CODE_SIGNING_ALLOWED=NO`) was tried first and is no good:
the Organizer refuses it with "No Team Found in Archive".

**Step two (2026-09-30): films and TV.** A Watch tab: films and shows as
posters (a search of kind `video`, and of `tv`, which lists shows); a show
opens its episodes by season from `/api/tv/show`, each marked with this
person's progress, and one Play for what to watch now (the page's
`nextToWatch`: the part-watched one, else the one after the last finished,
else the first). A film plays in **AVPlayerViewController as it is**: click
pauses, left and right move ten seconds, a swipe down shows the details, Back
leaves - what the page's TV mode was built to imitate, so nothing is added to
it. `/api/playback` says direct (the file) or HLS (Jellyfin's, through
`/api/hls`), and the cookie goes with the playlist and every segment. The
place is the page's own record, `/api/book/progress` with `t=<seconds>`, so
the TV and the page carry on from the same moment: resumed only past ten
seconds and before 93%, saved every 30 seconds, on pause and on leaving. The
music pauses for a film. An episode's end counts eight seconds into the next
(`/api/tv/next`) with Play now and Cancel.

Checked on the simulator against the stand-in, extended with a 20-second film
served as a file and as HLS, and a show of two episodes: the HLS playlist and
its segments all carried the cookie; episode 1 played from the start (its
saved 8 seconds is under the ten that count), was saved finished at its end,
and eight seconds later episode 2 played over HLS on its own. `-autovideo
<id>` in a debug build plays a film or episode at launch, for a simulator
without a remote.

**Subtitles and audio languages (2026-09-30) are drawn and chosen by the
app.** The server gives subtitles as separate WebVTT files (`/api/subtitle`),
which a browser adds as a `<track>` but AVPlayer cannot attach to a stream it
is handed. Rather than rewrite HLS playlists through a resource loader (and
still have no answer for a direct file), `Subtitles` reads the WebVTT into
cues and `SubtitleOverlay` draws the one showing in the player's
`contentOverlayView` - over the picture, under its controls - checked ten
times a second. Subtitles and Audio are menus in the player's own transport
bar (`transportBarCustomMenuItems`), where a swipe down finds them. As on the
page, subtitles start off; a language chosen carries on to the next episode
when it has one. Another audio language is `/api/playback?audio=<index>`, which
is always Jellyfin's HLS with that stream, played on from the same moment.
The overlay's host view must be pinned with constraints: sized from the
overlay's bounds it was laid out at zero and the text sat mid-screen.

Checked on the simulator against the stand-in with a three-cue WebVTT (markup
and an entity in it) and two audio languages: the file was fetched with the
cookie, each cue showed at its time and not after its end, a two-line cue
kept its break, and switching to the second language three seconds in asked
for `?audio=2` and played on over HLS from the same moment.
`-autosubtitle <n>` and `-autoaudio <n>` in a debug build choose them at start.

**Music, closer to the page (2026-09-30).** Home has the page's rows:
Continue (films and episodes, from `/api/continue`; audiobooks join when they
play here), Favorites, Recently played, Mixes, Albums. Music is rows rather
than tabs, since a remote moves down more easily than across: Mixes, Radio
and Moods (`GET /api/music/radio`), Playlists, Artists (a page of their
albums), Albums. A station plays its first batch and, two songs from the end,
fetches the next with everything already queued in `exclude` - the page's
station topping itself up. **Holding OK in Now Playing opens its menu**
(`.contextMenu`, as a held OK does on the page's TV mode): favorite, add to
playlist, the Lyrics look (synced lines, the one sung lit and the rest dim;
kept between songs and launches), and Up next, where OK on a song plays it.

Checked on the simulator against the stand-in: Home drew Continue and
Favorites; a station tuned with `-autostation YES` asked for its first batch,
and when its second song began asked for more with the three queued songs
excluded; with `-lyricsLook YES` the third line lit about seven seconds in, at
its 6-second start. **Not checked: the hold menu and its sheets**, which need
a remote's press, as Now Playing's arrows do.

**Music levelling and a slow link (2026-10-01).** ReplayGain levelling is
the page's `levelFor` - six decibels under full, the album's gain when an
album plays in order, never past the peak, an untagged song among tagged ones
at the pre-amp - set as an audio mix on each AVPlayerItem rather than the
player's volume, so the level changes exactly where the song does, the next
song loaded behind it included (books keep `player.volume`). Checked by
running the TV's formula on the page's measured cases: 0.50, 0.28 and 1.00 for
0, -5 and +8 dB. Not heard on a TV, and an audio mix is believed to apply to a
progressive stream (which songs are), not to HLS. A song still not playing six
seconds after it was asked for is started again at 128 kbps from where it is,
and every song after it (`slowLink`), as the page does - until two songs in
a row came in over 3 Mbps by the player's own measure of the download
(`accessLog().observedBitrate`, which a plain streamed file has too - checked
against a range-serving local server; Python's http.server serves no ranges,
and AVPlayer plays nothing from it), when the next song is at full quality.
**Crossfade is not built**: off by default on the page and set per device,
so the TV would need a setting of its own just to turn it on - left until
wanted. The sleep timer was built with audiobooks.

**Photos (2026-09-30).** A Photos tab: On this day (`/api/photos/on-this-day`,
a row per earlier year), then the camera roll newest first, the page's own
browse of the picture shelf (`/api/search?kind=picture`), a page of 60 at a
time as it scrolls. A clip (`extra.type` video) plays as a film; the viewer
steps through photos only, as the page's does. The viewer is the whole
screen at Immich's preview size (`/api/art/.../<artId>@preview`), with the
TV rules settled for Android TV: left and right step, **down pauses or plays
the music, which otherwise plays on**, with a message saying which (the
remote's play key does the same), Back closes. The caption - name, date,
place, and "3 of 40" - shows on opening and each step and fades after four
seconds. The photos either side are fetched ahead, and only those three are
kept.

Checked on the simulator against the stand-in with eight generated photos
and a clip, while a radio station played: the roll's second page came as the
grid scrolled, opening the first photo fetched it and the second at preview
size with the cookie, a step right showed the second and fetched the third,
the clip was left out of the count, and down paused the music with "Music
paused". A full-screen cover cannot open over another: the photo viewer does
not open while Now Playing is up (the debug run puts Now Playing away first,
as Back would).

**Audiobooks (2026-09-30).** An Audiobooks tab, and books in Home's
Continue row. `Player` plays a book as its files on the book's own clock
(`/api/playback`'s `tracks`, each with `startSeconds`): AVQueuePlayer holds
the file playing and the next, time is the file's start plus the time into
it, and a seek into another file rebuilds the queue from that file. It starts
where this person got to (`position`, the server's record, which is
Audiobookshelf's - so the page and Audiobookshelf's own apps agree) unless
the book was finished, and saves it every ten seconds while playing, on pause
and on leaving, `finished` at the end of the last file - the page's cadence.
A seek is not a forced save: a run of thirty-second skips would meet the
server's limit on saves. The speed is the account's own (`audiobookSpeed` in
`/api/prefs`, the page's setting, 0.75× to 3×), set as the player's
`defaultRate` so a book's next file keeps it; music is always 1×.

In Now Playing a book shows its chapter under its title, and the timeline is
the chapter's with the whole book beneath (as on the page). Left and right
are thirty seconds, not songs (the TV rule), and the system's remote commands
switch from next and previous to thirty-second skips. The hold menu has
Chapters (a list, the one playing marked), Speed, and a **sleep timer**, which
music has too: 15 to 90 minutes, or the end of the song or chapter.

Checked on the simulator against the stand-in with a book of three
24-second files, four chapters (one starting inside a file), a saved place
at 30 seconds and the account at 1.5×: it asked for the second file and had
the third ready, showed "The Middle" (the chapter from 20 to 40), saved 45 and
60 ten seconds apart (1.5× on the book's clock), crossed into the third file,
and saved `finished` at 72. **Not checked: the hold menu's chapters, speed and
sleep timer**, which need a remote.

**Every tab has the page's categories (2026-09-30)**, asked for by the
owner: a row along the top (`CategoryBar`), where moving along it changes
the category at once, as tvOS's own top bars do. The categories are the
page's `TABS` - Home: Home, Favorites; Music: the page's inner music row
(mixes, radio, playlists, songs, albums, artists, favorites, genres); Watch:
Films, TV, Favorites, Genres; Books: Audiobooks, Authors, Series, Favorites,
Genres; Photos: Photos, People, Places, Favorites - **in the account's own
order and without the ones put away** (`pills` and `hiddenPills` in
`/api/prefs`, set on the page by hold-and-slide and the + button; Genres
start put away, as `DEFAULT_HIDDEN` has it). The TV only follows that
setting; changing it stays on the page. Each category is the page's own
request: shelves page through `/api/search`, Favorites is `/api/favorites`
filtered by the tab's kinds, Genres `/api/genres?kinds=` (a genre's items with
`&name=`), Authors and Series `/api/books/authors|series` (one with `?key=`),
People and Places `/api/photos/people|places` (one with `?id=`).
**Ebooks, Documents and Read Along are left out** until the TV has a reader,
and the Books tab's other categories show audiobooks only.

Checked on the simulator against the stand-in with a saved music order
(albums, mixes, songs) and Places put away: Music's row came in that order
then the rest, without Genres; Photos had no Places, and People drew a named
face and "Add a name on the web" for the unnamed one; Books had Audiobooks,
Authors, Series, Favorites. `-tab <tab>` and `-category <value>` in a debug
build open one. Not checked: opening a genre, author, series, person or
place, which needs a remote.

**Reading (2026-09-30): a native reader, since foliate-js needs a web view
tvOS does not have.** Ebooks and Documents are back in Books, and Favorites
there holds all three kinds. An EPUB (`EpubBook`) is read as any reader reads
it: `META-INF/container.xml` names the OPF, whose spine is the chapters and
whose nav document (EPUB 3) or NCX (EPUB 2) is the contents - one
`/api/book/resource` each, the server doing the unzipping. A chapter's XHTML
(`EpubText`) becomes an attributed string - headings, paragraphs, italic and
bold, pictures as attachments, whitespace collapsed as a browser does - with a
map from every piece of text back to its place in the XHTML, in CFI terms, and
TextKit 1 lays it into screen-sized pages (a container per page).
XMLParser knows only XML's entities, so HTML's (`&nbsp;` and the rest) are
made numeric first.

**The place is the page's own: an EPUB CFI and how far through**
(`/api/book/progress`), read on opening - the CFI's spine step picks the
chapter and its path and offset the page - and written four seconds after the
page stops turning and on closing, for the first character on the page. So
the TV and the page open at the same spot. A PDF is drawn a page at a time by
Core Graphics (no PDFKit on tvOS); the page's reader keeps no place for PDFs,
so the TV's is `page=<n>`, read back only by the TV. The TV rules hold: left
and right turn the page, down pauses or plays the music, holding OK opens
Contents and Text size (kept between books), Back closes.

Checked on the simulator against **the real server code**, not a stand-in -
the ebook and document shelves run without Docker - with the starter
library's EPUB and a generated three-page PDF, signed in through
`-username`/`-password` and opened with `-autoread <id>`, `-autoturn <n>`:
five turns saved `epubcfi(/6/4!/4/2/4/6/4/10/52/2/2/4/1:0)`, and walking that
path through the chapter's XHTML independently (elements counted as even
steps, text as odd) lands on "91", the first text on the TV's page; a PDF
turned once saved `page=2` and drew page 2 of 3. Not checked: a CFI written by
foliate being read by the TV (the parser takes its ranges and `[id]`
assertions), and the hold menu's Contents and Text size.

Things that bit:

- **Saving from inside the pending save cancelled it.** `save()` cancelled the
  pending save task first - which, called from that task, cancelled itself,
  and URLSession sends nothing from a cancelled task. The timer calls
  `write()`, which cancels nothing.
- **The first page drew blank.** The page view was on screen before the first
  chapter was laid out, and with the same chapter and page it was never drawn
  again. A layout counter (`generation`) in its identity redraws it.
- The server logs `/api/book/*` at debug only; `SOUNDSTORM_LOG_LEVEL=debug` to
  see them.

**Read Along (2026-09-30)** is a category in Books, from `/api/books/pairs`,
each card saying whether the page will follow (synced, syncing, not synced -
a sync is still started on the page). Choosing one plays the audiobook and
opens the book over it, as the page does. Synced, it is Storyteller's copy
that opens (`/api/readalong` gives it and the timeline: every sentence's
start and end on the recording's whole-book clock and its place in that
copy, "OEBPS/Text/ch01.xhtml#s0012", a full path inside the EPUB like the
reader's own chapter paths). Four times a second the reader finds the
sentence begun at the player's time, turns to its chapter and page, and
lights it to the next anchor in the chapter - unless the page was turned by
hand in the last twelve seconds, the page's own rule. Lighting is the
account's `readAlongHighlight`, switched from the hold menu. Not synced (or no
timeline), the shelf's ebook opens beside the audiobook with nothing
following.

Checked on the simulator against the stand-in with a synthetic synced book
in Storyteller's shape (two chapters, a span per sentence) and a timeline
putting chapter 2 at 30 seconds, the test audiobook resuming at 30 at 1.5x:
the reader opened the synced copy straight at chapter 2 with its first
sentence lit and moved the light to the fourth seven seconds later. Two
things bit:

- **An anchor sat before its paragraph break**, so the light began on the
  break and drew a grey block at the end of the line above. Anchors are placed
  at the element's first text now, and the lit range drops the break after it.
- **A cancelled request was taken for "not synced".** A task given up on
  (its page gone) failed the `/api/readalong` fetch, and the fallback opened
  the shelf's copy over the synced one. Cancelled, it now does nothing.

**Audiobooks follow the page's later TV rules (2026-09-30):** left and right
go by chapters - the next, or back to this one's start (the one before within
its first three seconds) - and a book without chapters keeps its thirty
seconds; and a book plays 8dB down (`player.volume`, the page's `bookGainDb`
default; the page lets a device change it, the TV does not yet). Not checked:
both need a remote or an ear.

**Looks and visualizers, stage one (2026-09-30).** Now Playing's look is the
account's own (`coverStyle` in `/api/prefs`, the page's setting), chosen from
the hold menu's Look: Lyrics, Cover, Spinning disc and Record, and so far the
Orb (`pulse`) and Spectrum (`bars`) of the page's twelve visualizers. A look
chosen on the page that the TV does not draw yet shows as the Orb. A
visualizer fills the screen with the title above it, as on the page's TV
layout; a book always has the plain cover.

**The song is heard on the TV, as on the page** - nothing of it is kept on
the server. `SongAnalysis` is `hearSong` ported step for step: the song at
96 kbps (`stream?kbps=96`) decoded by AVAssetReader to 11025 Hz mono, frames
of 256 (about 23ms), a one-pole split at 150 Hz and 2500 Hz, onsets, loudness
over half a second scaled 5%-97%, the tempo by autocorrelation in tenths of a
frame with AudioMuse's tempo (`/api/music/sound`) as the prior, beats by
Ellis's dynamic programming, and the downbeat as the beat of four where the
bass lands hardest. The last six songs are kept. Only a visualizer asks for
it: hearing a song is decoding the whole of it. `VizEngine` is the page's
frame loop (`viz.frame`): beat and phase from the beats found, kick and snare
envelopes measured against the last two bars' typical hit, surges, drops on a
downbeat in the loudest 30%, drive and brightness. Drawn in SwiftUI's Canvas
at 30 frames a second with the page's TV halving of particles. The page's
fading trail canvas has no Canvas equivalent: each vortex particle keeps its
last seven places and draws them fading by a fifth, as the page's fade does.

Checked: the analysis alone, compiled on the Mac, against the page's own test
- a generated 120 bpm click track, first beat at 0.23s, a quiet section - found
all 120 beats, mean error 6ms (the page's check: 13ms), downbeat on the
accented beat, loudness 0.14 quiet and 1.00 loud, in 2 seconds a minute of
song. On the simulator with that track as the song: the Orb drew its drop
ring on a loud downbeat and dimmed and calmed in the quiet section; Spectrum,
Spinning disc and Record drew. Not checked: the hold menu's Look (a remote),
and how fast an Apple TV hears a song (the debug build is unoptimized).

**Stage two (2026-09-30): Warp, Waves, Kaleidoscope, Fireworks**
(`Visualizers2.swift`), ported from `VIZ_SCENES` with their counts and
responses (Warp's stars halved to 160, as on a TV). Fireworks' sparks keep
their last nine places for a trail, as the Orb's vortex does. Checked on the
simulator with the click track: each drew - the tunnel and its stars, five
ribbons, the ten-way mirror, and bursts with their glows and falling trails.

**Stage three (2026-09-30): Flow, Storm, Synthwave, Galaxy, Aurora, Lava**
(`Visualizers3.swift`), from `flowScene` and `FULL_SCENES`, with the page's TV
counts (Flow 260 particles, Storm 210 drops, Galaxy 450 stars). With these
all twelve are drawn, so the Orb stands in only for a look the TV does not
know. Many small marks with an alpha each (stars, folds) are drawn as four
paths per colour, one per alpha level - the page batches the same way for a
phone. Synthwave's striped sun cuts its stripes out in a layer
(`drawLayer`, `.destinationOut`), the page's `destination-out`. **Galaxy's
stars had to be bigger than the page's numbers**: 0.8 to 2.4 canvas pixels at
the page's TV scale come out much larger on screen than the same number of
points, and the first try was a faint smudge; 2.4 to 6 points reads as the
page's. Checked on the simulator with the click track: each of the six drew,
Storm with a double strike on a drop.

Not checked, for all twelve: how smoothly an Apple TV draws them (the
simulator runs on the Mac's GPU).

**The Apple TV asks the server what it heard (2026-09-30)**, as the page
does: `/api/music/beats?source&id&v=8`, the page's kept shape (three lanes as
bytes 0-255, float32 beats, base64; `Heard.fromServer`), and hears a song
itself only when the server has not. Its own hearing stays the older one
(hits scaled to each song), so a `Heard` says which (`fixedScale`): the
server's gets the page's lightning now - `strikesAt` (60% on the fixed scale,
within 70ms of a beat or halfway between two), every frame since the last
drawn looked at, `dropPower`/`dropLoud`/`dropBass` from the four frames from
the crossing, the first after six quiet seconds marked - and the TV's own the
older rule (a downbeat in the loudest 30%). Its copy for hearing asks
`listen=1`, unpaced. Checked by running `internal/beats` itself on a click
track with a noise crack on 2 and 4 and decoding its answer with the TV's
Swift on the Mac: 2605 frames, 121 beats from 0.21s, downbeat right, and a
strike on every crack. (A crack on a sparse synthetic track is "heard" even in
its quiet part: the 95th percentile of its highs is the hiss between hits.
Real music is not like that.)

**Storm on the TV is the rebuilt one** (`Storm.swift`, the page's `storm`
and its `stormX` helpers): the cloud deck of five rows of puffs, the dark
band and solid cap, each strike lighting it its own way (`stormLights`), lit
puffs painted over rather than added, lightning by midpoint displacement with
few short forking branches, half of it spreading through the clouds
(`stormSpider`) and drawn over them, a quarter leaning, a close bolt's glow,
spray and splashes where it lands, the faint after-image; rain at three
depths, heavier with the music, gusts, and mist. The soft shapes are pictures
made once per cover colour and stretched (`GraphicsContext.draw` of a
resolved image), as the page's sprites. Line widths are the page's canvas
pixels times 1.5 as points. Checked on the simulator with the server's
hearing of a click track with a crack on 2 and 4: a ground strike with spray,
lightning spreading under the clouds, a leaning bolt, the deck lit and the
after-images, in eight frames a quarter-second apart.

**New passwords and new devices on the TV (2026-10-01).** Both arrived on
the PC (`fcd7065` and devices.go) and the TV met neither: held to choosing a
new password the server answers everything 403, and with approval of new
devices on a sign-in answers 202 `pending` - the TV took that for signed in,
with no cookie. Now:
- **Choosing a new password** (`RenewPasswordView`): shown in place of the
  library whenever the session says `mustRenew` or any request is refused
  with it (`API.renewDemanded`), current and new password as on the page;
  saving signs every other device out and this one carries on.
- **Waiting for approval**: the sign-in view says so and asks every three
  seconds, with the setup code too where the server allows it, and Cancel.
- **Approving others**: while approval is on, a signed-in TV is asked every
  eight seconds, as the page is - at the parents' house the TV may be the only
  thing signed in. Allow, Don't allow, and Not now, which is what Back does:
  Back must not refuse somebody's real sign-in.

Checked on the tvOS simulator against a local server: with approval on, the
TV waited and the server listed it; approved with curl, it went to the
library; asked everyone for new passwords, it showed the screen within eight
seconds; with `-password <old> -newPassword <new>` (debug only) it saved,
went back to the library, the old password was refused and the new one taken
(waiting for approval, as every other device now is); and that sign-in came
up on the TV as "Allow this sign-in?". Not checked: pressing the alert's
buttons, which needs a remote.

**Signing a TV in from a phone (2026-10-01, the owner's asking; built on
the Mac, server and web included).** A TV's sign-in offers **Sign in with
your phone**: a code (`KXT-4PM`) and a QR code of `/?link=KXT4PM` on the
secure name; a phone signed in scans it or types the code under Settings,
Sign in a TV, is told "<device> showing KXT-4PM will be signed in as <you>",
and allows it; the TV, polling with a 128-bit id only it knows, is handed a
session (`auth.SessionFor`). Ten minutes, once; lookups limited per person;
skips device approval, since the phone vouched. Server `tvlink.go`, the QR
`internal/qr` (no dependency: byte mode, level M, versions 1-10 - every
version read back exactly by macOS's CIDetector when written; the server's
own PNG decoded to the right address too). The page: the gate's phone sign-in
in TV mode (Google TV and the rest), the Settings card, and the "Sign in a
TV?" question for a scanned `?link=`, taken out of the address once asked.
The Apple TV: the same, first on its sign-in screen. Checked: Go tests
through the real routes (allowed, refused, used up, signed out, guessing);
Chrome with Playwright as TV and phone - scanned address, typed lower case
with a space, and refused; and the Apple TV simulator, allowed with curl,
into the library. Not checked: a real phone's camera scanning a real TV - an
iPhone's camera opens the address in Safari, not the app, so it is allowed
there (signing in to Safari first if need be); typing the code in the app's
Settings avoids that.

**A TV's QR code opened the website, not the app** (the owner's report): a
camera opens a web address in the browser, which is usually not signed in.
Android App Links cannot help - each install has its own name, and verifying
`*.home.soundstorm.dev` would ask the zone's apex. So the Android app (0.22)
takes `soundstorm://link?server=<origin>&code=<code>` (an intent filter on
MainActivity; `tvLink`): only a server it already knows - by origin, by the
install's id in a soundstorm.dev name, or the secure name it moved to - else
its own, where a strange code finds no TV; the page already open is handed
the code (`window.__soundstormLink`), or the server's page loaded with
`?link=`. And the page, opened by a QR in an Android phone's browser, offers
**Open in the SoundStorm app** (an `intent://` link with the browser as the
fallback, so without the app nothing changes). Checked: the link opens the
app on the emulator, and the button shows in an Android browser. **Then the app came forward and nothing asked** (0.23): with the app
already open, the page in it had been loaded before it had the hand-off, so
the code went nowhere. The page now says whether it took the code
(`__soundstormLink` returns true), and when it does not - an older page, or
not signed in - the app loads it again with `?link=`. Checked on the
emulator end to end: signed in to a throwaway server, a TV (Chrome) showing a
code, the link fired at the open app - "Sign in a TV?" with that code, Allow,
and the TV signed in. **And then it still did not ask** (0.24): the server's log showed the TV
polling and the phone never looking the code up. The app reloaded the page
with `?link=`, the page moved itself to the install's secure name, and the
app, following that move, reopened the name's root - without the code. The
move now carries `link` across (`pendingLink`, in `shouldOverrideUrlLoading`).
Not reproducible on the emulator (it needs a signed-in app on the real
server's home address); the same address path checked in a browser.
**For the Mac:** the iPhone app wants the same - `soundstorm` in
CFBundleURLTypes, the same `link?server=&code=` handled (known servers
only), and the page then offering the button on an iPhone too (it shows on
Android only now; `/iPhone/` would be added once the app takes the link).
**Then the camera opens the app itself, and the app can scan** (2026-10-02,
the owner asking why a web page had to come first). Two ways, both built:
- **Scan the TV's code** in the app (0.25), beside the code box in Settings:
  Google's code scanner from Play services (`play-services-code-scanner`,
  its own screen, no camera permission), the code read from the QR's
  address, `?link=`, or a bare six characters, and handed to
  `__soundstormLink`. Checked on the emulator: the button shows and opens the
  scanner.
- **An App Link after all** (0.26). The reasoning above was half right:
  Android verifies a wildcard host at its root, so `*.home.soundstorm.dev` is
  checked at `https://home.soundstorm.dev/.well-known/assetlinks.json` - one
  file, not one per install - and the names service now serves it
  (`handleAssetLinks`: the package and the signing key's SHA-256,
  `NAMES_ANDROID_CERTS` to change it; the debug key until there is a store
  key, and a new key needs this list updated before it ships). The QR is now
  `https://<name>/link/<code>` (`linkURL`); in a browser `/link/{code}`
  redirects to `/?link=`. The app's filter (`autoVerify`, both labels,
  `/link/`) goes to `tvLink`, which takes the code from the path and still
  opens only a server it knows. **It works only once `home.soundstorm.dev`
  and `net.soundstorm.dev` point at the names service** (Railway custom
  domains and CNAMEs at Porkbun - the owner's to do; both answered a parked
  address when this was built), and a phone checks when the app is
  installed, so 0.26 must be installed after that. Checked on the emulator
  with the link approved by hand (`pm set-app-links-user-selection`): the QR's
  address opened the app. Not checked: real verification.

**An address can be just the code at its start (2026-10-02).** Typed on an
Apple TV at another house, the away name without its port (`:8099`) found
nothing, and "yourname.home.soundstorm.dev" read as if a name were wanted.
Both apps' connect screens now say "abc123.home.soundstorm.dev at home or
abc123.net.soundstorm.dev away - or just the abc123 at its start", and
`ServerAddress.find` tries every address that could mean (`candidates`): the
code alone is its home and away names, each with `:8099` and without; a
soundstorm.dev name without a port is tried with `:8099` too. All at once,
the best that answers kept - a TV the home name first, a phone the away one
(the page moves it home when it can). Checked: the list for nine ways of
typing it. Not checked against a real server from another house.

**Found on the network (2026-10-02)**, the owner's asking: somebody who
buys a box, plugs it in and opens the TV app should be told "We found
SoundStorm on your network", not asked for an address. Bonjour cannot do it
- Docker on Windows and macOS keeps a container's announcements off the
home network - so the apps look (`ios/Shared/ServerDiscovery.swift`): every
address on the device's own /24, and on 192.168.0.x and 192.168.1.x, asked
`/healthz` on port 8099 at once (two seconds at most each), and any that
answers asked its secure home name (`/api/session`'s `secureName`), kept
when it answers too. The second pair is not a guess for its own sake: a
mesh or second router inside the first puts the TV on one network and the
server on the one outside it - the owner's house, 192.168.68.x inside
192.168.0.x, where only that found the real server. Shown above Your servers
on both apps' first screens, one tap; searched again every ten seconds while
nothing is found (the server may be starting, and iOS asks for local network
permission the first time). A phone keeps the away name when a found home
name has one that answers. Checked in both simulators: the test server on
this Mac and the owner's real server (by its home name) both found. Not
checked: the permission prompt on a real iPhone, and a real Apple TV.
**For the PC:** the Android app's connect screen (and so Google TV) wants
the same - the same addresses, `/healthz` then `/api/session`.
*(Done in Android 0.34, 2026-10-03: `ServerAddress.candidates`/`find` and
`ServerDiscovery.kt`, Wi-Fi and wired networks only - mobile data has
private addresses too. Checked on the emulator, a fresh install: it found
the PC's server within ten seconds, "Use it - k3x9m2p7qa", and opened its
sign-in page.)*

**The server's name, and setting up a new box from the phone (2026-10-03)**,
the owner's design. A server has a name devices show before anybody signs
in - in the network search, the list of servers, and the sign-in ("Sign in
to Gabriel's SoundStorm"): none while it is not set up, then "<owner>'s
SoundStorm", or what the owner sets in Settings, Devices, **Server name**
(`state.ServerName`, `PUT /api/settings/server-name`, owner only, 60
characters, empty back to the default). It is in `/healthz` (`name`, and
`setUp` - whether it has an owner) and `/api/session` (`serverName`), so
anyone on the network sees it, as a Wi-Fi network's name; the card says so.
The apps keep a saved server's name in step with the server's
(`ServerAddress.serverCalls`, from `check`, the TV's session, and the
iPhone's page loading) unless renamed on that device (`custom`).
A found server with no owner is a new box: the phone app says **"We found
your new SoundStorm" - Set it up**, then the setup code - scanned off the
sticker (an address with `?setup=`, or the code alone) or typed - and opens
the page with it in the address, so its sign-up asks only a name and
password. A wrong code there now shows the code box to fix it (it was hidden
for a code from the address). The Apple TV shows such a box as "Not set up
yet - set it up with the SoundStorm app on your phone first". Checked:
`TestTheServerHasAName`, and `testSetUpANewBoxFromThePhone` (a brand-new
server on :8099 found, Set it up, the code typed, an account made, and the
server then "tester's SoundStorm"); all seven iPhone UI tests pass. The
circle at the top opens a menu, so iOS reports it as a pop-up, not a
button: tests find it by its label. Not checked: scanning a real sticker.

**Nothing found, said plainly; a welcome after the first sign-in
(2026-10-03).** When the apps' search finds nothing and no server is known,
the first screen says why rather than offering only an address box
(`ServerDiscovery.nothingFound`): not on Wi-Fi at all (no private address -
mobile data), else a new box takes a few minutes to start the first time,
check the same Wi-Fi and not a guest one, and on an iPhone that Don't Allow
on the local network question is undone in Settings - and that it keeps
looking. The iPhone no longer jumps to the keyboard, which covered it. On
the web, the owner's Home opens with **Welcome to SoundStorm**
(`renderWelcome`): add your media (from a USB drive too, on a box), use it
away from home (where remote access is possible), add your family, set up
your TV - each ticked when done (the library not empty, remote access on,
more than one account, a TV among the players), a button to the place that
does it, and a line that the first day after a lot is added is busy. Done
closes it on every device (`prefs.welcomeDone`); all ticked closes it too.
Checked in Chrome: the steps, Add people opening People, Done kept after a
reload. **Its first step is the address** (2026-10-06, the owner asking
whether a new owner knows which address to use - they did not: the page moves
to the secure name without a word, and the address was only on the setup's
last screen and in a folded card): "Your SoundStorm address", the home address
(`/api/library`'s `shareURL`) or the chosen web address, large, with Copy and
"open it in the browser on any phone, tablet or computer at home, sign in, and
save it as a bookmark", and **Pick an easier one** opening Your web address
when none is chosen. Done on Got it (`prefs.addressSeen`) or once another of
the owner's devices, not a TV, is open; until then Use on your phone or TV
opens unfolded. **Then one step at a time, over the whole screen** (2026-10-10, the owner
after the test box: a list with its buttons far off at the side was read past,
and "Add your music" was ticked on a server holding only the starter
library). The welcome is a screen over everything (`#welcome`, at the end of the
page), each step answered - done, or Not now - before the next, "1 of 4" at the
top and Skip the rest of the welcome at the foot: **the address first**, with an
easier name offered right there ("That's hard to remember. Give it an easier
name", saved through `/api/settings/web-name`) or Keep this address; then
media ("drop them on any EmberStorm screen - here, or any time later" - "window" read as the dashed box alone, the owner said, Choose files - a drop while it
shows answers it), away from home, family (Invite someone) and the TV. A step
already done is passed over; answered ones are kept on the account
(`prefs.welcomeSeen`, only the five names), so it carries on where it was; a step that opens another page steps aside and Home brings the next. Media
counts only what was added: `/api/library`'s `added` is the library's files
less the starter files still there (`starter.Present`). Checked: the Go tests
(`TestPresentCountsTheStarterFilesLeft`, `TestTheWelcomesStepsAreKept`), and on
the test server at phone and computer size, made to look brand new
(`scripts/smoke/welcome.js`): four steps in order, kept, no page error.
Not seen: the name saved from the welcome, and a real new install.
**And installing it as an app is offered once on each browser** (the owner's
asking, the same day: a taskbar icon - which Windows lets no installer pin).
After signing in, anybody, any device: "Install EmberStorm on this computer?"
(or phone, tablet), Install and Not now (`offerInstall`, `#install-offer`).
Chrome and Edge give the page their install question (`beforeinstallprompt`,
kept and asked with the card); Safari on an iPhone or iPad has none, so the card
says Share, then Add to Home Screen. Never in the phone apps, on a TV, in an app
already installed, offline or while the owner's welcome shows; answered either
way, remembered on the device (`soundstorm-install-offered`) and never asked
there again - the browser's own install button stays for later. The setup's
finished screen says to pin the EmberStorm shortcut (Start menu, right-click,
Pin to taskbar): on the computer it runs on, the shortcut starts EmberStorm
first where the app only opens the page (the owner's choice, after trying the
other way). **Not offered on that computer at all**: the setup's Open EmberStorm and
the desktop icon open the page with `?here=server`, which the page keeps on the
browser (`soundstorm-install-offered` = server, set on each address it lands on,
carried across the move to the secure name) and takes out of the address with
the setup code. Typed by hand there, it is offered once - harmless. Checked on
the test server: opened so, the address cleaned, no offer. Checked on the test server (`scripts/smoke/installoffer.js`, the
install question played by hand - a real one comes only on a trusted address):
Chrome offered, Install asked the browser, not again after a reload; an iPhone
told how, not again after Not now. Not seen: Chrome's real install box. Checked in Chrome at phone size with a stand-in address: shown
first, Got it ticked and kept. Not seen: Pick an easier one (the test server
has no automatic HTTPS). Not checked: the nothing-found words on a phone (this Mac's network
always finds the owner's server).

**The Apple TV moves to the secure name by itself (2026-10-03).** Kept by a
bare address - found before a new box had its secure name, or typed - the TV
stayed on plain http at a number the router may change after a power cut,
where a phone's page moves itself (`moveToSecureName`). Now, each time the
TV checks its session (`AppModel.refreshSession`), a `secureName` offered
that answers from the TV replaces the saved address (`ServerAddress.moved`:
same place and name in the list), the session's cookies are copied to it,
and the app opens there. Checked in the simulator against the owner's real
server, read only: opened at `http://192.168.0.50:8099`, it saved
`https://k3x9m2p7qa.home.soundstorm.dev:8099`. Not checked: staying signed
in across the move (not signed in to the real server; the server takes the
plain cookie's name over TLS, which is what the copy relies on).

**Saved servers (2026-10-01), the first half of profiles.** The owner's
design, after Prime Video's account-then-people: a device may know several
servers (their own, their parents'), and later several people on each. The
apps' connect screen is now **Your servers** - the latest used first, the one
in use ticked, tap to switch, hold for Rename and Remove - above **add
another**. `ServerAddress.all` (shared by the iPhone and Apple TV apps) keeps
`{url, name}`; `remember` puts a server at the top and makes it current, an
app from before the list starts it with the server it had, and a first name
is the address without `.home.soundstorm.dev` (renamed in the app). Sign-ins
stay with each server, since cookies belong to an address, so switching signs
nobody out - the iPhone test switches away and back and is still signed in.
Browsers need nothing: each server is its own site and its own bookmark.
**For the PC (Android):** the same in `ServerAddress.kt` - a JSON list in the
prefs beside the current address (the current one seeds it), the connect
screen listing them above the address box, a long press for Rename and
Remove. *(Done in Android 0.19: `ServerAddress.all`/`remember`/`forget`/
`rename`, the connect screen's rows; with a list the keyboard waits, and a
server already listed is not typed into the box again. Checked on the
emulator: two servers, the newest first and ticked, a rename kept.)*
**Profiles (2026-10-01): "Who's listening?"**, the second half, designed
with the owner: optional PINs, the owner always needing their PIN (or
password, with none), and a TV asking every time it opens. A device gets a
profile cookie of its own (`soundstorm_profiles`, a random id, not a
sign-in); the server keeps who may be switched to on it (`state.Kept`, keyed
by a hash of the id, each person with a fingerprint of their password's salt,
so a new password takes them off every device). "Keep me on this device" at
sign-in (`keep` in the login body; ticked on a TV; kept through device
approval; a TV signed in from a phone keeps its person by itself) puts them on
it. `GET /api/profiles` and `POST /api/profiles/switch` are open, since a TV
asks before anybody is signed in; a switch revokes the device's session and
starts the person's. Wrong PINs are counted before they are checked (so a
burst is counted as one), five then a wait of 15 minutes doubling, fifteen
and the person is taken off. **Sign out takes you off the device; Switch
person keeps everybody** - on a friend's phone, staying switchable would be
staying signed in. The page: the picker on a TV's every opening, Someone
else to sign in kept, Settings' On this device (switch, keep, take off) and
PIN. The Apple TV: the same, and Switch person in its Settings. On a TV the PIN or password step offers **Use your phone instead**
(`#profiles-phone`, the owner's asking), which opens the sign-in screen's
code and QR code: typing a password with a remote was the chore. Checked: Go
tests through the routes; Chrome as a TV with two people (password for the
owner, none then a PIN for the other, a wrong PIN refused); the Apple TV
simulator (`-profile <name> -profileSecret <s>`, debug only) - the picker on
opening, a wrong PIN refused, the right one into the library; the iPhone UI
tests still pass. Not checked: a remote's presses on the TV picker.

**Your circle, top right, and pictures of your own (2026-10-02)**, asked for
as switching being too buried in Settings, "like most apps, in the top
corner". The page's header ends in the signed-in person's circle (`me-btn`,
`renderMe`, `paintAvatar` - their picture, or their initial on the colour the
picker already gave them). It opens a menu: the others kept on this device
(one tap, their PIN or password asked where needed), Someone else, Add or
Change picture, Remove picture, and **Settings, which left the tab bar** -
except on a TV, where the side bar is where the remote goes and the circle
heads it (`tv-me-tab`, opening "Who's listening?"). The Apple TV's side bar
starts with the circle too (tvOS has no side bar header, so it is drawn into
an image, `AvatarImage`), and its picker tiles show pictures (`Avatar`).

The picture is kept like a cover of one's own, under the key `me`
(`collections.ProfileArtKey`): `PUT`/`DELETE /api/account/picture`, cropped
square on the device by the same `pickCoverImage`. It goes out as `picture`
on the person (session, sign-in answers, people list, `/api/profiles`), an
address that changes with the picture. `GET /api/profiles/{id}/picture` is
open, because "Who's listening?" shows before anybody is signed in - but only
for somebody kept on this device, or to anyone signed in to the server.
Checked: Go test through the routes, Chrome at phone, desktop and TV sizes
(menu, switching, a picture added and shown on the other device's picker),
and the Apple TV simulator's picker. Not checked: the Apple TV side bar's
circle itself (the simulator cannot open the side bar), and Android back
closing the menu (Escape and a tap outside do).
**Pictures are rounded squares, not circles** (2026-10-04, the owner's
asking): every place a person's picture or initial shows - "Who's
listening?", the menu at the top, the TV side bar, the Send to and Share
lists - is a square with corners a quarter of its size (`border-radius:
24%`), matching the header's button. **For the Mac:** the Apple TV's
`Avatar` and `AvatarImage` (the picker tiles and the side bar) are circles;
make them rounded squares the same way (a corner radius of about a quarter
of the side). *(Done on the Mac, 2026-10-04: `Avatar` and
`AvatarImage` at 24% of the side, the picker's focus ring following. Built,
not seen on screen.)*

## The Android app (`android/`)

The same shape as the iPhone app, and the same stage: a native shell around
the server's own web app (Kotlin, two screens, no Compose or AppCompat), so
`app.js` stays the one product. It does what `ios/` does - asks for the
server's address and checks `/healthz`, shows the page edge to edge, answers
`alert`/`confirm`/`prompt`, sends links off the server to the browser, and
recovers from a failed load with Try again / Change server - and the web app
cannot tell the two apps apart: the page gets the same `window.soundstormApp`
and the same `window.webkit.messageHandlers.soundstorm.postMessage`, so "Change
server" needed no Android code in `app.js`.

**Android is where the APK note under Music applies, and this is its answer.**
Android's WebView has no Media Session API and is stopped in the background
unless the app holds a media-playback foreground service - the two things that
note said a WebView app would lose. So:

- `PageScript` runs before the page's own scripts (`addDocumentStartJavaScript`,
  the server's origin only) and gives it a `navigator.mediaSession` that passes
  what `app.js` already says - the song, playing or paused, the position, which
  buttons work - to the app, and hands buttons back to the page's own handlers
  (`window.__soundstormMediaAction`). The page does all the playing; nothing
  about the audio is native yet.
- `PlaybackService` is a foreground service of the `mediaPlayback` type with a
  `MediaSessionCompat` and a MediaStyle notification: lock screen, notification,
  headphones, Bluetooth and a car's controls. It goes to the foreground as soon
  as the page says it is playing, drops back (notification kept, swipeable) on
  pause, and goes away when the player closes.
- The channel is `addWebMessageListener`, for the server's origin and the main
  frame only - not `addJavascriptInterface`, which every frame of every origin
  would see, EPUB chapters included.
- Covers for the notification are fetched with the page's own cookie
  (`CookieManager`), since covers are served only to someone signed in.
- The status bar takes the page's theme colour (a MutationObserver on
  `meta[name=theme-color]`), as the installed web app's does in Now Playing.
- Back steps back through the page's own history, and with nothing left moves
  the app to the background rather than closing it - closing would stop the
  music. File pickers (Add media, Import playlist, Change cover) and a film's
  full screen are the platform's.

**Both system bars are hidden, everywhere** - the owner's asking, and the
thing the installed web app never could do, since Chrome owns its bars. A
swipe in from an edge shows them for a moment (`BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE`);
they are hidden again whenever the window regains focus. The app draws into the
camera cutout (`LAYOUT_IN_DISPLAY_CUTOUT_MODE_ALWAYS`), so there is no black
strip over the camera - the one the owner disliked in the Chrome app. Android
shows its own "Viewing full screen - swipe down to exit" card once, the first
time.

**The web view reports 0 for the cutout it draws into** (measured:
`env(safe-area-inset-top)` read 0px on a Pixel 7 profile with a 136px cutout),
so drawn full height the Home header would sit under the camera, and padding
the page down instead left a band in the theme colour that never quite matched
Now Playing's backdrop. So style.css no longer uses `env(safe-area-inset-*)`
directly: every edge is spaced by `--safe-top/right/bottom/left`, which default
to the env() values (a browser and the iPhone are unchanged), and the Android app
sets them to the cutout's size in CSS pixels - as a document-start script,
replaced when the cutout changes, and on the page already showing. Checked:
`--safe-top` read 51px, the header ran up to the edge with the logo below the
camera, and Now Playing's backdrop reached the top with no band.

Plain http is allowed (`usesCleartextTraffic`), because an install is often
reached by its LAN address and Android cannot allow a range of private
addresses by itself. Unlike an iPhone's WKWebView, Android's WebView runs the
service worker, so downloads should work offline in the app as in Chrome -
not yet checked.

**Checked on an emulator (Android 17 image), not yet on a phone:** the connect
screen refused `example.com` ("isn't SoundStorm") and took the test server;
inside the app the page had `soundstormApp`, the media session and the message
channel; a song played; the media session was active and PLAYING with the
song's title, artist and album; the service was in the foreground as
`mediaPlayback`; with the screen off for 45 seconds playback carried on
(0:40 to 1:28) and the queue moved on to the next song by itself; pause, play
and next from `cmd media_session dispatch` (what headphones send) worked; the
notification shade showed the controls with a seek bar; the status bar took
Now Playing's colour; Change server returned to the connect screen, prefilled,
and stopped the music.

Things that bit:

- **The emulator's default 2GB is killed as the page loads** - Android's
  low-memory killer ended the app in the foreground ("min watermark is
  breached"). 4GB (`-memory 4096`, `hw.ramSize`) is fine. A phone is not the
  emulator; worth watching on a small one.
- **Taps and typing through `adb shell input` do not reliably reach a web
  page.** Drive it through the debug build's DevTools instead
  (`adb forward ... webview_devtools_remote_<pid>`, Playwright
  `connectOverCDP`), which is how the checks above ran.
- **Change server prefilled the host alone**, as the iPhone app does, which
  for a plain-http server is read back as https and fails. It keeps `http://`
  now.

**Signed in at home, asked again away** (2026-10-08, Android 0.52, the owner's report): cookies belong to one name, and the app's fall back from the home name to its away twin found no sign-in there. The app now copies the session, device and profile cookies from the name in use to its twin (`mirrorCookies`: before the fall back, as each page finishes, and as the app is left; a cookie gone - signed out - goes from the twin too). Not tried on a phone. **For the Mac:** the iPhone's fall back to the `.net` twin wants the same copy (it copies cookies only for the zone move).

## Naming

The product is **SoundStorm**; every identifier is **soundstorm**. Module path,
binary, container names, the `SOUNDSTORM_` env prefix, the session cookie, the
account created on each backend, the client identity sent to Jellyfin and
Subsonic - all lowercase. Prose, the page title, the wordmark and anything a
person reads use the brand casing. `TestUIShellIsServed` asserts the shell
carries the display form, which is the one place the distinction is checked.

It was called atrium until the rename. Nothing in the repo should say so.

**The logo is a cloud, and it is drawn, not pasted.** It arrived as a
500x500 PNG with the cloud 150 pixels wide - too small to enlarge into a
512px icon. The cloud is three shapes (two circles and a rounded bar, cut
flat along the bottom), measured off that image, and `scripts/make-icons.py`
draws every icon from them: the favicon (black, white in a dark browser),
`cloud.svg` for the wordmark, and the PNG app icons - a white cloud on
the app's own dark background, so the home screen icon looks like the app
it opens. A lightning bolt drops out of the cloud's flat bottom, for the storm in
the name; the wordmark centers cloud and bolt together on the name.
Change the icons by changing the script, not by editing the PNGs.
The wordmark is the cloud as a CSS mask in the text's color, then the name
in heavy italic, matching the logo.

## How it gets installed

The install is `docker-compose.yml` plus an installer script, and the split
between them is the point: the compose file names a **published image** and
contains no `build:` key, so it works alone in an empty folder for somebody who
never cloned anything. `docker-compose.dev.yml` adds `build: .` back for us.
A `build:` key in the main file would make it refer to a Dockerfile the user
does not have.

`.github/workflows/publish.yml` is what makes that true - without a published
image the install instructions point at nothing. It gates publishing on
gofmt/vet/test, and builds linux/amd64 and linux/arm64, because a lot of home
media servers are a Pi, a Synology or an Apple silicon Mac. The Dockerfile
pins its *builder* to `$BUILDPLATFORM` and cross-compiles via
`GOOS`/`GOARCH`, so the arm64 image does not run the Go toolchain under QEMU.

`install.ps1` is aimed at somebody who has never opened a terminal, because
that is who a self-hosted media server usually gets given to. It installs
Docker Desktop itself with `winget` rather than sending them to a website,
starts Docker rather than telling them to, and leaves desktop, Start Menu and
startup shortcuts rather than an address to remember. `SoundStorm-Setup.cmd`
exists only so the thing can be double-clicked out of the Downloads folder;
asking somebody to open PowerShell and paste a command is the step that loses
people.

Three bugs in that script were found only by running it, and all three are
PowerShell-specific traps worth knowing:

- **A line break before an operator inside `if (...)` is a syntax error** in
  5.1. The file had one for two commits and would not parse at all. It went
  unnoticed because the syntax check discarded the error collection -
  `PSParser::Tokenize` returns tokens and reports errors through a `[ref]`
  parameter, so ignoring it means every file "parses clean".
- **PowerShell strips the inner double quotes** out of
  `--format '{{index .Config.Labels "com.docker..."}}'` on the way to a native
  program, and docker then fails with `function "com" not defined`. Read the
  labels with `ConvertFrom-Json` instead.
- **`Start-Process -PassThru` hands back a Process with no cached handle**, and
  without one `WaitForExit(timeout)` never observes the exit: it returns false
  at the deadline for a program that finished in a second. Reading `.Handle`
  once is what caches it. The symptom is every launch taking exactly the
  timeout and then reporting failure while the containers run perfectly well
  behind it.
- **Native stderr becomes an ErrorRecord**, so with
  `$ErrorActionPreference = 'Stop'` a `docker compose up` fails the script by
  printing its ordinary progress. Everything that shells out goes through
  `Invoke-Docker`, which pins the preference to Continue and pipes stderr
  through `Write-Host` so it prints as text rather than as a red block that
  looks like a crash.

The desktop shortcut runs the launcher **minimized**, which is right for the
common case - clicking it when the stack is already up takes half a second -
and wrong for every failure, because console text written into a minimized
window is text nobody will ever see. So `-Launch` failures also open a dialog,
and `compose up -d` gets a deadline: without one, an unreachable registry makes
the icon do nothing at all, for minutes, with no way to tell that from a broken
shortcut. The dialog dismisses itself after two minutes, because at startup
there may be nobody there to click it.

Measured on the development machine: 2.9 seconds from every container stopped,
0.4 seconds when it is already running.

`install.sh` is `/bin/sh`, not bash - a stock Debian's `/bin/sh` is dash, and
that is exactly the cheap box this is aimed at. Every failure message says what
to do next; "Docker is installed but not running" is the most common one by a
distance and is worth its own branch.

Two things learned by testing the installer rather than reasoning about it:

- **The compose project name is fixed (`name: soundstorm`), so a second install
  in a second folder adopts the first one's containers** rather than getting its
  own. It then points at an empty library folder and looks broken. Both
  installers check `com.docker.compose.project.working_dir` on the running
  container and refuse, naming the other folder. Keeping one install per machine
  is right; silently hijacking is not.
- **The POSIX port pre-check cannot always answer.** It asks `nc`, then `ss`,
  then `lsof`, and a machine with none of them - Git Bash on Windows, for one -
  falls through to "assume free". So the real backstop is parsing `compose up`'s
  output for "already allocated" and saying which port and how to change it.

The two gaps that once stopped this being a product for other people - one
user and no HTTPS - are both closed: see "Accounts" and "TLS without anybody
running openssl". What remains is signing: an unsigned setup file is what
Smart App Control blocks (see Gotchas).

## The starter library

A fresh install arrives with **one item per stocked shelf** - ~17MB embedded in
the binary, unpacked once into whichever library folders are empty.

	ebook       The Richest Man in Babylon (1926)   Wikisource, public domain
	audiobook   As a Man Thinketh                   LibriVox, public domain
	music       Aria, Open Goldberg Variations      CC0 1.0

Bundled rather than downloaded, deliberately. Fetching on install means every
installation spends somebody else's bandwidth, and Project Gutenberg states
outright that automated access to its website earns an IP block. It also means
the first run works air-gapped.

**One of each, not a selection.** It was ten ebooks and four music tracks, which
demonstrated nothing the first of each did not and made the ebook shelf look
like somebody else's taste in books. What a first run has to answer is "does
each kind of media work", and that takes exactly one of each.

**No video, and it was tried - do not re-litigate this from scratch.** Big Buck
Bunny was bundled for exactly one commit (`07731d7`, reverted in the next). What
was measured, so nobody has to measure it again: archive.org's CC-BY copy is
640x360 stereo at 830k despite being named `720p_surround`, re-encoding at crf 28
takes 62MB down to 25MB, Jellyfin identified it from the folder name alone and
**direct-played** it with no transcode, and it was 60% of a 42MB bundle - more
than everything else combined. That last number is the same arithmetic that kept
video out originally, and it did not change.

The argument for including it was that the shelf a media server is judged on
should not be the empty one, and it is a real argument. It lost to size: three
shelves' worth of working media is enough to show the thing works, and a film is
the one kind of media everybody already has a copy of. `scripts/fetch-starter-
media.sh` no longer fetches it; the credit points at Blender instead, which is
the better use of the space - what changes how somebody uses a media server is
learning that free films exist.

The `24 << 20` budget in `starter_test.go` has deliberately narrow headroom
against the 17MB bundle. It came *down* from 30MB rather than being left as
slack, because slack is what a film grows into.

**Television gets nothing either**, for the same reason twice over.

**A free license is a hard constraint, not a preference** - this is compiled into
a published binary. A genuinely famous song or film is almost certainly
somebody's copyright, so each item is as recognisable as a free license allows
rather than as recognisable as possible. Two traps found while picking them:

- **The Richest Man in Babylon is not on Project Gutenberg** - its whole
  79,433-title catalog was checked, and no work by Clason is either. The 1926
  edition is US public domain; the *later expanded* editions are not, and most
  copies circulating are those. Wikisource has it and tags it
  `{{PD-US|1957|1926}}`, which is a license review by somebody whose job that is,
  so that is the source. The archive.org text hits are Internet Archive lending
  scans and anonymous uploads - neither is a license basis.
- **`download.blender.org` and `musopen.org` both answer 403** from this
  environment, so a Blender film has to come from archive.org's CC-BY mirror and
  Musopen is not available as a source for a better-known piece of music. Worth
  knowing before reaching for either again.

Nothing in the UI hardcodes any of this; it renders `starter.Attributions`,
which is the more useful half of the idea - the point is telling somebody
LibriVox and Wikisource exist. A test requires a credit for `movies` too, even
though nothing ships there: naming where free films are is the whole reason that
entry exists.

**It unpacks once, ever, and the flag that says so is in the state file.** It
used to run on every boot into any shelf with no media on it, which reads like
it respects a decision and does the opposite: an emptied shelf and a never-used
one are identical on disk, so somebody who deleted the samples on purpose got
them back at the next restart with nothing to explain why. Reported as "so now
am I able to delete everything from the library?" - and the honest answer was
"yes, until you restart".

`state.StarterInstalled` is the only thing in there that is not a credential or
an account. It is still not a fact about anybody's media - it says what
SoundStorm has done. A marker file in the library folder was the obvious
alternative and is worse for a reason easy to miss from Linux: **Windows
Explorer does not hide dot-files**, so it would sit at the top of "the folders
are the interface" as the one item nobody recognizes, and deleting it - the
natural response - would bring the samples back.

The flag is set even when nothing was unpacked, because a boot that found every
shelf occupied has answered the question just as well; leaving it clear would
keep the samples waiting for the first shelf somebody empties. Absent means
not-yet-done, so `omitempty` is safe here - the opposite of `User.Libraries`,
where the absent value had to mean "restricted".

`docker compose down -v` forgets, so a reinstall over an existing library can
still put samples on a shelf that happens to be empty. That is the documented
full reset, and something to look at is the whole point of the feature.
`SOUNDSTORM_STARTER_LIBRARY=false` still turns it off outright.

Verified on a running server: ten ebooks and their placeholder deleted, restart,
`books=0` and the folder holding nothing but the README it wrote back itself.

Two Windows-specific traps live in here, both of which cost real time:

- `go:embed` rejects file names containing an apostrophe. The bundled files do
  without; what a reader sees comes from metadata, not the file name.
- `os.MkdirAll` is documented to succeed for an existing directory, and does on
  a normal filesystem. On a Docker Desktop bind mount it can return EEXIST
  instead - which made the starter library skip exactly the folders compose
  mounts into backends (music, audiobooks) while succeeding for ebooks, which
  nothing mounts. `ensureDir` treats an existing directory as success.

Do not move `library/` while the stack is running. The backends hold bind mounts
into its subfolders, and replacing the directory leaves them dangling in a state
where stat and mkdir disagree about whether a path exists.

**The bundle was silently corrupt for months, and the lesson is about binaries
in general.** Something read the committed media as text and wrote it back,
which on Windows drops every carriage return. Nine of the ten ebooks then could
not be opened at all - removing a byte moves a zip's central directory out from
under its own recorded offsets - and the four music tracks and the audiobook
lost frame sync about once per 64KB, which a decoder survives as a click rather
than an error. Nineteen of the twenty-one screenshots in `docs/shots` lost the
carriage return out of the PNG signature, `89 50 4E 47 0D 0A 1A 0A`, so the
README's images rendered as broken-image icons on the page where somebody
decides whether to install this.

Every symptom was quiet. The files were the right length, opened, and began
with the right magic number; a fresh install simply showed one book instead of
ten. It was found only by chasing an unrelated report and noticing
`failed=9` in a scan log.

It was **not** git. `.gitattributes` marks these binary, and a test confirms
git leaves a PNG alone even with `core.autocrlf=true` and no attributes at all -
its own binary detection catches it. The conversion happened before the files
were ever committed, in whatever wrote them.

So the rule is: **a committed binary needs a check that reads it the way its
consumer will.** Length and magic number prove nothing.

- `internal/starter/integrity_test.go` opens every bundled epub's
  `META-INF/container.xml` - the entry, not the listing, because a damaged zip
  lists it perfectly - and walks every mp3's frame chain end to end. The check is
  itself checked against damage: the carriage returns are stripped out of a
  bundled mp3 and the walk must object. A checker for a silent fault is worth
  exactly what its evidence is. (An MP4 box walk lived here for one commit,
  alongside the film, and went with it.)
- `scripts/check-images.py` verifies every PNG against its own per-chunk CRC32
  and inflates the pixels to compare with the header. Wired into CI.

The repair is worth knowing for next time. The ebooks and audio had to be
fetched again - `scripts/fetch-starter-media.sh`, which also records how each
file is made, something that existed nowhere before and is why the damage could
not simply be undone. Every re-fetched ebook came back exactly as many bytes larger as the
carriage returns that had been removed (2, 2, 1, 2, 10, 3, 1, 12, 3 and 0),
which is what confirmed the diagnosis. The screenshots needed no re-capture:
PNG carries a CRC32 per chunk, so for each damaged chunk the missing carriage
returns could be found by trying each newline position until the checksum
matched. All twenty-one now inflate to exactly the size their headers claim.

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

- **Every `:hover` rule that changes how something looks goes inside
  `@media (hover: hover)`.** A phone keeps `:hover` on whatever was tapped
  last, so the cards' accent outline and title stuck to one item - a "half
  selected" look - and on an iPhone a hover change can make the first tap
  count only as a hover, so selecting needed two taps (the owner's report,
  2026-10-04). The cards, buttons' brightening, the "..." and track rows were
  outside it.
- **Code that runs as app.js loads must not touch what is declared further
  down.** A `const` (ICONS, and others) is unusable before its line runs, and
  one thrown error stops the whole page: the app would not load at all
  (2026-10-03, a video button painting its icon at load). `node --check` does
  not see it; loading the page does. After any app.js change, open the page
  and look for an error before deploying is done (`pageerror` in Playwright).
- **Provisioning is not idempotent across a volume reset.** If a backend's
  volume is wiped but SoundStorm's state survives (or vice versa), you get a
  backend with an account whose password nobody holds. The provisioners detect
  this and say so rather than retrying forever. The fix is a human decision.

  `soundstorm backup` and `soundstorm restore` are now that decision's tools,
  and the uninstaller takes a backup into the install folder before `down -v`,
  which is the one moment the credentials stop existing anywhere. Every
  ordinary `save()` also leaves a sibling `.bak`, which covers a bad write and
  nothing else - a deleted volume takes the `.bak` with it.

  **An unknown argument is an error, not a server.** `main` used to fall
  through to its normal path for anything it did not recognize, so
  `soundstorm backup` against an image too old to have the command quietly
  started a *second* SoundStorm against the same state volume - two writers on
  the one file that cannot be regenerated, in answer to what was effectively a
  typo. Found by doing exactly that. The server takes no arguments at all, so
  every argument is a subcommand or a mistake.

  **Backup is read-only, and that is not a nicety.** `state.Open` migrates and
  saves unconditionally, so backing up through it would rewrite the file being
  backed up and hand its ownership to whoever ran the command. `state.Inspect`
  and `state.CopyTo` exist so the command can look without touching. Proven by
  running the backup as root against a volume owned by uid 10001 and checking
  the ownership afterwards.

  **Restore has to put the ownership back**, for the same reason the password
  reset does. It falls back to the *directory's* owner when there is no state
  file to copy from, which is the normal case - the whole point of a restore is
  often that the volume is empty. A fresh named volume mounted at
  `/var/lib/soundstorm` inherits 10001 from the image, because the Dockerfile
  chowns that path and declares it a VOLUME. Mount it anywhere else and the
  directory is root's, which is what made the first attempt look broken.

  **A backup written through a bind mount was unreadable to whoever asked for
  it, on Linux.** The container writes it as its own user, uid 10001, mode 0600
  - and neither side can fix that afterwards: the installer runs as the user and
  cannot chown a file it does not own, and the container is not root and cannot
  hand the file to a uid it is not. So `backup -` writes to standard output and
  `restore -` reads standard input, and the host shell creates the file, as the
  user, under `umask 077`. `install.sh` does exactly that, checks the output
  starts with `{` - an image from before `-` prints its human summary there
  instead - and falls back to the old bind-mount form for such an image, since a
  backup only root can read beats none. `backup -` refuses a terminal: the
  passwords would scroll past on screen, and a pseudo-terminal turns newlines
  into CRLF, which is why the documented form passes `-T`. On macOS and Windows,
  Docker Desktop presents a bind-mounted file as the host user's, so
  `install.ps1` keeps the bind mount (plus `Protect-SecretFile`).
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
- **That CSP also forbids inline `<script>`, and says so only in the console.**
  The service-worker registration shipped inline first. It was refused
  silently: the app worked, nothing looked wrong, and "Add to Home Screen"
  produced a bookmark instead of an app. The fix is never to relax the policy -
  that is what protects the session from an EPUB - but to put the script in a
  file. `assets/sw-register.js` is that file, and
  `TestShellHasNoInlineScript` walks the shell so the next one cannot slip in.
- **The PWA needs a *trusted* certificate, not merely https - and localhost
  hides that.** Android would not install SoundStorm from its LAN address, and
  the reason is that Chrome answers `ERR_CERT_AUTHORITY_INVALID` for a
  certificate signed by the local authority. It treats such an origin as
  having a certificate error, and refuses to register a service worker there -
  so "Install app" never appears. Clicking through the interstitial lets you
  browse and does not change that.

  `navigator.serviceWorker` being undefined outside a secure context is still
  true and is why plain http never worked either. The part that was missed is
  that a self-signed https origin is not a secure context in Chrome's eyes.

  **Every PWA test had been run against localhost or with `ignoreHTTPSErrors:
  true`**, both of which are secure contexts whatever the certificate. So
  `scripts/mobile-check.js` reported a registered worker, truthfully, while a
  real phone could not install it at all. A check that cannot see the failure
  it is meant to catch is worse than no check, because it is believed. The
  script now says what it does and does not prove.

  Fixed by auto mode, now the default: the `….home.soundstorm.dev` address
  carries a real Let's Encrypt certificate, so Android installs from it with
  no warning. Away from home a Tailscale `ts.net` address does the same. In
  self-signed mode `/ca.crt` still has to be installed on each device, which
  Android accompanies with a standing "network may be monitored" notice.
  iPhone is unaffected: Safari's Add to Home Screen does not go through a
  service worker.
- **A service worker is served from `/`, not `/static/`.** Its scope is its own
  directory, so from `/static/sw.js` it could never control `/` - the only page
  there is. It would register, report success and intercept nothing. Same for
  the manifest's content type: Go's mime table has no `.webmanifest`, and
  Chrome ignores a manifest not handed to it as JSON, so both are served by
  hand in `internal/webui` rather than by the file server.
- **The service worker must never answer a range request, anything under
  `/api/`, or a page load.** Caching a search result serves stale state;
  answering a range request without honoring `Range` breaks seeking in a way
  indistinguishable from a corrupt file; answering a page load hides the
  browser's certificate warning behind a cached page that cannot work (see TLS
  above). Requests it does not handle are left alone entirely -
  it never calls `respondWith`, so the browser behaves as if no worker existed.
  It is network-first throughout, so a `docker compose pull` cannot pin anybody
  to an old build.
- **`grid-template-columns: minmax(0, 1fr)`, not `1fr`.** A grid item's
  min-width is `auto`, which resolves to its min-content - so one
  `white-space: nowrap` example path inside the folder guide made the row
  refuse to shrink and pushed the page to 531px on a 390px phone. The
  `text-overflow: ellipsis` was there the whole time and could never fire. The
  first screen a new user sees had a horizontal scrollbar.
- **`repeat(auto-fill, minmax(168px, 1fr))` is one column on a phone.** With
  padding, 390px cannot fit two 168px tracks, so every result filled the entire
  screen and twenty-five of them meant twenty-five screens of scrolling. Phones
  get an explicit two columns.
- **iOS Safari zooms in on any input whose text is under 16px, and does not
  zoom back out.** Inputs inherit the 15px body font, so tapping the search box
  shoved the layout sideways. One rule at the mobile breakpoint fixes it.
- **A safe-area rule must be in the rule that sets the padding.** The
  reader's bar had `padding-top: max(10px, env(safe-area-inset-top))` in an
  early `@supports` block, and two later rules - the bar's own and a phone
  breakpoint - set its whole padding again, so on an iPhone the bar sat
  under the camera cutout and its close button could not be tapped. The
  video overlay's close sat at a fixed 18px, under the status bar. Chrome's
  `Emulation.setSafeAreaInsetsOverride` (CDP) gives a real inset to test
  against; without it every inset is 0 and the bug cannot be seen.
- **Mobile layout is measured, not eyeballed.** The screenshot script asserts
  `document.scrollWidth <= clientWidth` per screen and lists anything sticking
  out, which is what found all of the above. Elements inside a deliberately
  side-scrolling container (the filter chips) have to be excluded or every
  screen reports a false positive.
- **An empty `libraries` list means nothing, and must never read back as nil.**
  `User.Libraries` has no `omitempty` for exactly this reason: with it, an
  account allowed no libraries would serialize to nothing, read back as nil,
  and silently mean *every* library. The one mistake this field cannot make is
  failing open, and there is a test that writes it, reads it back through the
  accounts list, and checks.
- **The state file has a schema version and a migration.** Version 1 had one
  `user`, sessions that were bare expiry timestamps, and bookmarks keyed by
  source and item alone; version 2 has accounts. The upgrade makes the existing
  account the owner, gives its sessions its id and its bookmarks its prefix, so
  nobody is signed out and nobody loses their place. `Session.UnmarshalJSON`
  accepts both shapes, which keeps the old one in a single place. Tested
  against a real v1 file, on disk, both in `internal/state` and on a running
  container.
- **Audiobook chapters are files, not chapter marks.** `TrackLister` reports an
  item's audio files and the dock plays through them, which covers a per-chapter
  rip - the shape of every LibriVox book. A single m4b with twenty chapter marks
  still returns one track: it plays through correctly but has no navigation,
  because seeking inside one file is a different problem from switching between
  several. Chapter *names* come from Audiobookshelf's chapter list when there is
  one per file, then the ID3 title tag, then the filename.
- **Listening position lives upstream, not in `internal/state`.** It is written
  to Audiobookshelf's own `PATCH /api/me/progress/{id}`, which is what makes a
  chapter finished in its mobile app the place a browser picks up. A private
  copy would quietly fork from the one every other client reads. Position is
  measured across the whole book, so `Track.StartSeconds` converts between "two
  hours in" and "a file and an offset".
- **Audiobookshelf does not derive `progress` from `currentTime`.** Send one
  without the other and the player resumes correctly while its own shelf and
  "continue listening" row keep showing the old percentage. The fraction is
  computed in the adapter for that reason. Verified on 2.36.1.
- **`isFinished` is one-way, and sending `false` is destructive.** For a book
  already marked finished it does not merely clear the flag - it resets
  `currentTime` and `progress` to zero, which is what "mark as unfinished"
  means in the Audiobookshelf UI. Someone who reached the end and scrubbed back
  would lose their place. So the flag is only ever sent as `true`; a later
  position clears it as a side effect anyway. Also note `progress: 1` alone is
  enough to mark a book finished.
- **Audiobookshelf stores a progress record without validating it.** PATCHing
  the string `"x"` as `currentTime` is accepted with a 200 and read straight
  back. So nothing leaves `handleSetPosition` that is not a time (a JSON number
  is never NaN, but `1e999` decodes to `+Inf` without complaint), and
  `mediaProgress` decodes leniently - strict decoding would turn one junk
  record into a permanent error for that book.
- **Some LibriVox MP3s ship mangled ID3 tags.** `fables_01_00_lafontaine` has
  double-encoded UTF-8 declared as latin-1, so its chapter reads
  "00 - ÃƒÂ€ Monseigneur le Dauphin". The damage is in the published file, not
  in Audiobookshelf and not in us; repairing it means guessing at a chain of
  mis-decodings that would corrupt any title legitimately containing those
  characters. Left alone deliberately.
- **PowerShell here-strings carry CRLF into `docker exec bash -c`**, and a
  trailing carriage return makes bash misread the command. `scripts/` passes
  single-line commands for that reason.
- **`Invoke-WebRequest` cannot do https once a scriptblock is in the
  certificate callback.** PowerShell 5.1 has no `-SkipCertificateCheck`, so
  trusting our own self-signed cert means assigning
  `ServicePointManager.ServerCertificateValidationCallback`. Do that with a
  scriptblock and `Invoke-WebRequest` then fails against *every* https address,
  github.com included, with "The underlying connection was closed: An
  unexpected error occurred on a send." It runs the request off the pipeline
  thread, where there is no runspace to execute a scriptblock in, so the
  delegate throws and the connection is torn down. The error never mentions the
  callback. `[Net.HttpWebRequest]::Create(...).GetResponse()` runs on the
  pipeline thread and works, which is what `Test-Healthz` in `install.ps1` uses.
  Symptom if you get this wrong: the installer waits its full 180 seconds and
  reports the server never answered, while curl gets a 200 from the same URL.
- **`--no-https` binds to nothing without a hyphenated alias.** PowerShell
  treats a leading `--` as a single dash, so `--https` reaches `-Https` by
  itself - but `--no-https` becomes `-no-https`, and a parameter *name* cannot
  contain a hyphen. It was accepted and ignored in silence: the installer
  reported success and left the install on http. `[Alias('no-https')]` fixes
  it. `[CmdletBinding()]` already rejects an outright typo, so the aliases are
  the only gap.
- **Do not fix a `.cmd` file with `sed -i`.** It strips the CRLF the file needs
  (`.gitattributes` has `*.cmd -text`), and cmd.exe mishandles `goto` without
  it. Use a tool that writes the line endings back. The CI guard that exists to
  catch this was itself a no-op for two months: `grep -q "\r"` reaches grep as
  an escape a POSIX basic regexp does not define, and GNU grep reads it as a
  plain letter r - so it passed on any file containing the letter r, which is
  every file. It now counts carriage returns against line count, which also
  catches half a file being converted.
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
- **Docker Desktop opens its dashboard on every start, and that has to be
  turned off before it first runs.** `OpenUIOnStartupDisabled` in
  `%APPDATA%\Docker\settings-store.json` does it - confirmed as a real key by
  finding the string inside Docker's own binary rather than by trusting a
  blog. Docker only stores settings that differ from its defaults, so on a
  fresh machine the key is absent and has to be added.

  It is written **before** the winget install as well as after, because Docker
  Desktop launches itself the moment its installer finishes - too early for
  anything the script does afterwards to prevent. Writing the file first is
  the only way the window never appears at all.

  Written without a byte order mark: `Set-Content -Encoding utf8` adds one in
  PowerShell 5.1, and a BOM in front of JSON is a good way to discover whether
  the reader is strict. Only ever called on the fresh-install path, so it sets
  a default rather than overriding somebody's choice.
- **Every external call in `install.ps1` must go through `Invoke-Native` or
  `Invoke-Docker`.** With `$ErrorActionPreference = 'Stop'`, a bare native
  call throws on its first line of stderr. `Test-DockerRunning` was written as
  a bare `docker info` and therefore crashed in the one situation it exists to
  detect - Docker installed but not running, which is precisely the state
  immediately after installing Docker Desktop. It had been fixed once for
  `docker compose` and missed here, so the rule is now the helper, not
  vigilance.
- **Install WSL before Docker, not after Docker complains.** Docker Desktop
  runs its engine inside WSL2. On a machine that has never had it, Docker
  installs and launches perfectly and *then* puts up a dialog asking the user
  to install or update WSL by hand - as administrator, followed by a restart.
  That is three steps past where an installer should have stopped asking, and
  it is exactly where the first person to use this got stuck after the BIOS.
  `Install-WSL` runs `wsl --install --no-distribution` elevated first.

  `--no-distribution` matters: without it Windows also fetches Ubuntu, which
  is a gigabyte, several more minutes, and a first-run prompt asking for a
  Linux username nobody here will ever use again. Docker brings its own.

  Detection is `wsl --version`, not `Get-Command wsl`. wsl.exe ships in
  System32 on every Windows 10 and 11 whether or not WSL is installed, so
  finding the command proves nothing; `--version` answers only where the real
  thing is present, and the exit code is the whole answer - the text it prints
  is UTF-16 and arrives full of null bytes through a pipe.

  Skipped entirely on the `-Launch` path. That runs minimized from a desktop
  shortcut at startup, where a UAC prompt with no visible window behind it is
  worse than the failure it would be fixing.
- **Docker's progress output is filtered, and the filter must not require a
  colon.** Without a terminal docker cannot redraw in place, so it prints a
  whole line per progress tick: a 3GB pull becomes many hundreds of lines of
  hex and megabytes scrolling past, which reads far more like a fault than
  like progress. The pipe is not the thing to remove - it is what stops
  docker's stderr arriving as ErrorRecords and printing as a red block that
  looks like a crash - so `Invoke-Docker -Calm` drops the churn instead and
  beats every 30 seconds so the window never looks hung.

  `docker pull` writes `5c3b447848a9: Extracting`; **`docker compose pull`
  writes `f5be9333d3a8 Extracting` with no colon**, and compose is what this
  script runs. The first version of that regexp required the colon and would
  have filtered precisely nothing on the only command it exists for. Test it
  against real output, not against what the format looks like it should be.
- **Check virtualization before installing Docker, and ask
  `HypervisorPresent` first.** Docker on Windows runs Linux in a VM, so a PC
  with VT-x/SVM switched off in its firmware cannot run any of this - and the
  first person to try the installer found that out only after half a gigabyte
  of Docker Desktop had installed itself, from a Docker error that offers to
  fix it by signing in. Signing in fixes nothing; it is the BIOS.

  The order of the two `Win32_ComputerSystem` properties is the whole trick.
  Once a hypervisor is running, Windows can no longer see the firmware to ask,
  and reports `VirtualizationFirmwareEnabled` as false - or blank, which is
  what this development machine returns while happily running Docker. Testing
  that property first would tell a perfectly working PC it cannot run the app.
  So: `HypervisorPresent` is proof and settles it, and only in its absence is
  the firmware flag worth reading. Both are readable without administrator.

  It returns true whenever it cannot tell, including when the CIM query throws.
  Refusing to install on a machine that is actually fine is a worse failure
  than the check never firing.
- **Docker Desktop's installer needs administrator rights**, and a setup file
  run by double-clicking does not have them - so winget fails and the whole
  install stops on its first action with a bare exit code 1. `Install-Docker`
  asks for elevation with `Start-Process -Verb RunAs` for that one step rather
  than demanding the whole setup be run as administrator, which would put the
  library in whichever profile did the elevating.
- **Do not decode winget exit codes; look at whether docker is there.** The
  "already installed" code is `-1978335135` (0x8A150061), not the
  `-1978335189` that was hardcoded here for several commits, and there are
  more of them - a reboot-required result is a success that reads like a
  failure. Asking `Get-Command docker` afterwards is both simpler and right.
- **Smart App Control blocks any script downloaded from the web, by extension.**
  Double-clicking the downloaded `SoundStorm-Setup.cmd` gives "An Application
  Control policy has blocked this file. Dangerous file extension from the web",
  with no Run anyway. It has nothing to do with the contents - a two-line
  hello-world .cmd with mark-of-the-web is blocked identically - and it cannot
  be fixed in the file. Right-click, Properties, Unblock clears the mark and it
  then runs, which is why that is step one in the README.

  This hid behind a testing mistake worth remembering: `cmd /c script.cmd`
  does **not** go through the shell's reputation check, so every test passed.
  A double-click does. Reproduce it with `Start-Process`, which uses
  ShellExecute, not with `cmd /c`.

  The real fixes are a signed binary or a winget package; both are open.
- **Never pipe a downloaded script into PowerShell from the setup file.**
  `powershell -Command "iex ((New-Object Net.WebClient).DownloadString(...))"`
  is the canonical malware download cradle, and Windows Defender blocks it on
  sight - "Access is denied", no explanation, on the very first thing a new
  user touches. It survived testing because `install.ps1` sat beside the .cmd
  in this checkout, so every run took the local branch and never fetched
  anything; the failure only appears when the file is downloaded on its own,
  which is exactly how everybody gets it. Fetch to a file with `curl.exe`
  (shipped with Windows since 10 build 1803) and run it with `-File`.
  Confirmed in `Get-MpThreatDetection`, and confirmed fixed by the same.
- **The Windows setup file is linked from a release, not from raw.** Clicking a
  `raw.githubusercontent` link opens the file in the browser rather than saving
  it: raw serves `text/plain` with no `Content-Disposition`, so a `.cmd` is
  displayed as text. A release asset serves `application/octet-stream` with
  `Content-Disposition: attachment`, which is an actual download. The stable
  link is `/releases/latest/download/SoundStorm-Setup.cmd`, so it follows the
  newest release without the README changing. Attach the file to every release.
- **The owner is `GabrielHollberg`, checked against `gh api user` rather than
  assumed.** It was a guessed name for most of this project's life, not a
  GitHub account at all (`gh api users/<that name>` answered 404). Every
  install URL in the README pointed at
  a dead end, and the Go import path named nobody. That is worth re-checking if
  it ever moves.
- **A GitHub username may have capitals; a container registry path may not.**
  `GabrielHollberg` is a fine account name and an invalid image reference, so
  the publish workflow folds `GITHUB_REPOSITORY_OWNER` to lowercase and the
  compose file names `ghcr.io/gabrielhollberg/soundstorm`. GitHub URLs keep
  their capitals; only registry references are folded.
- **Smart App Control blocks `go test` at random, and must not be turned off.**
  It refuses to exec freshly built, unsigned test binaries, so a run dies with
  "An Application Control policy has blocked this file" for a handful of
  packages that varies with every rebuild. Disabling it is **one-way**: Windows
  will not let it be re-enabled without a reset or reinstall, and it has no
  exclusion list. So work around it instead - run the suite in a container,
  which executes no Windows binary at all and is what CI does anyway:

  ```sh
  docker run --rm -v "//c/dev/atrium:/src" -w /src golang:1.27-alpine go test ./...
  ```

  The doubled slash is for MSYS, which otherwise rewrites `/src` into a Windows
  path. `go test -c -o` and then running the binary also usually works, but
  only usually - the container is the one that always does.
- **Dev on Windows, deploy to Linux** - but Windows is a deployment target
  too, and always has been: the whole stack runs on Docker Desktop, which is
  what `install.ps1` sets up. Go lives at `C:\dev\tools\go` (installed from
  the zip, on the user PATH). Docker Desktop must be running.
- **The binary itself runs natively on Windows**, verified: a `GOOS=windows`
  build produces a 37MB exe that serves, creates the library folders and
  scans ebooks with no Docker and no WSL. That is not the same as the product
  running natively - the other three media types are containers. Making those
  native would mean SoundStorm installing and supervising Jellyfin, Navidrome
  and Audiobookshelf as Windows processes, which is the "own the expensive
  part" trap this project exists to avoid, and Audiobookshelf has no official
  Windows build anyway.
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
open ios/SoundStorm.xcodeproj          # the iPhone app; see ios/README.md
cd android && ./gradlew installDebug     # the Android app; see android/README.md
```

`docker compose logs -f SoundStorm` is the fastest way to see why a backend is not
answering.
