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
- **Unsynchronisation** rewrites every `0xFF 0x00` pair so no part of a tag can
  look like an audio frame. Left undone, every length after the first such pair
  is wrong. The first real file tested had the flag set.
- **MP4's `meta` is a full atom**: four bytes of version and flags before its
  children, which its siblings do not have. Descending without skipping them
  lands mid-atom and finds nothing, which is the usual reason an M4A looks
  untagged.

**Only the missing levels are filled in.** A drop of `Laughing Stock/01.flac`
already names the album, so only the artist is added - replacing a folder
somebody chose with whatever a tag says would be worse than leaving it. With
nothing known at all it is `Unknown Artist/Unknown Album`, because the file
that says nothing about itself is the one that most needs somewhere obvious to
be found and fixed. Two levels deep or more is left alone entirely.

**Plan and Save share the rule**, and that matters: Plan runs it with empty
tags because the bytes have not arrived, Save runs it again with the file's
own. They agree for an untagged file, and where they differ Save is better
informed than the prediction - never worse.

Films and television are not restructured. Jellyfin matches on the *name*, not
the depth, and anything dropped there is already folder-shaped; imposing a
layout would be inventing one. Ebooks are flat on purpose.

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

Everything else stays decisive: `.epub` is a book, `.m4b` is an audiobook, a
path mentioning audiobooks is believed, and `S01E01`, `1x02` or a `Season 01`
folder is television. A question only offers libraries the account actually
has, and with one option left it stops being a question.

**Nothing appears at its destination until all of it is there.** Navidrome and
Audiobookshelf watch these folders and a half-written file is exactly what a
scanner indexes as a corrupt track. Bytes land in `<library>/.uploads` and are
renamed into place, which is atomic because it is the same filesystem, and
invisible to the backends because a top-level dot-directory is not mounted into
any of them. `ClearStaging` sweeps it at boot for the crash case.

**Every path is attacker-supplied.** One trap here cost a real bug, caught by
its own test: `TrimRight(segment, ". ")` ran *before* the `..` check, so ".."
became "" and was skipped - which quietly turned `../../etc/passwd.mp3` into
`etc/passwd.mp3` and wrote it. No escape, but a strange file in somebody's music
folder and no sign a path had been rewritten. Dot runs are now refused before
anything is trimmed. `Save` also re-checks the joined path is still inside the
folder, which is a thing worth doing twice.

Uploading follows the same permission as reading: you can add to a shelf you can
see. A restriction search honours and uploading does not is not a restriction,
so the plan refuses forbidden kinds and so does the upload.

On the browser side, `walkEntry` must call `readEntries` **until it returns an
empty batch**. It hands back at most a hundred at a time, and reading once
silently truncates a large folder - the classic way to lose half an album. There
is a test for it that builds a fake entry tree, because a synthetic DataTransfer
gets no filesystem entries and no automated drag can produce real ones.

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

That only holds if **every source returns its own title-first N**, because the
globally first N can only come from the union of each source's first N. It is
a requirement on the adapters, not an internal detail:

- `localbooks` sorts before it cuts. It used to cut in scan order, which would
  have made page two repeat some books and skip others.
- Jellyfin gets `SortBy=SortName`, Audiobookshelf `sort=media.metadata.title`.
- Navidrome's `search3` order is its own; nothing in the Subsonic API asks for
  a sort, so a music-only browse can slip an item at a page boundary. Known,
  and cheap to fix only if Navidrome grows the parameter.

`HasMore` is not just "this page was full": a source returning exactly what it
was asked for was probably truncated, so there is more behind it even when the
merged page came up short. Without that, one source holding a long shelf
answers a first page and looks exhausted.

`MaxDepth` (2000) stops paging rather than serving pages that never arrive.
The work per page grows with the offset, so there has to be an end somewhere.

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

The container cannot work its own LAN address out - inside Docker the only
addresses visible are the container's - so the *installer* does it, on the
host, and prints it. It prefers `192.168.` then `10.` then `172.`, because the
last range is also where Docker and WSL put virtual adapters that reach
nothing. On this machine that correctly picks the Ethernet address over the
Tailscale and WSL ones.

**This is the moment HTTPS stops being optional.** On localhost there is
nothing on the wire to protect; on a shared network the session cookie and the
Subsonic stream URLs - which carry credentials in the query string, because
that is the protocol - are readable by anything else on it. The README says so
where somebody is deciding to do it, not in a security section nobody reads.

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
the provisioning for no visible gain. Revisit when watched-state or favourites
reach the UI.

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

## Tailscale, and why it is a profile rather than a service

Reaching SoundStorm away from home is the one thing the LAN address cannot do.
`docker compose --profile tailscale up -d` runs a Tailscale sidecar that puts
it on a tailnet at `https://<hostname>.<tailnet>.ts.net`, with a **real
certificate** - which also removes the warning the local authority cannot
avoid. `--tailscale` in both installers writes the auth key into `.env`,
generates the serve config and turns the profile on.

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
`192.168.0.19` the client dialled. That reads correctly in a unit test with a
synthetic connection, and correctly for a binary run directly on the host -
the only two ways it had been tested - and never once in the way it actually
ships. It was caught by asking a client that trusts only the published
authority to fetch `https://192.168.0.19:8099`, which is the exact thing the
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

**Nothing in the UI may assume `fetch` resolves.** `api()` had no catch, so a
failed TLS handshake, a stopped server or a dropped connection rejected
straight through every caller - and `boot()` checked `ok` but never the
rejection, leaving the spinner turning with "Failed to fetch" in a console
nobody opens. It now returns `{ok: false, offline: true}` and the boot screen
says so, with the certificate explanation first because that is the usual
cause here.

`SOUNDSTORM_TRUST_PROXY` lets `X-Forwarded-Proto` decide whether the session
cookie is Secure, for a reverse proxy that terminates TLS. Off unless asked
for, because any client can send that header.

## The folders, and the line that replaced the box

Installing SoundStorm creates `library/` with five subfolders, and they are
still the only part of SoundStorm a user interacts with that has no UI - so
they are created for you and named for what people call the thing ("movies",
not "video").

**Nothing on the first screen is a box any more.** That screen has now been
three things: five folder rows with paths and example filenames, then one
centred card asking for files, then nothing at all. Each version was smaller
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
  code and this is a behaviour of theirs we are now depending on.
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

The desktop shortcut runs the launcher **minimised**, which is right for the
common case - clicking it when the stack is already up takes half a second -
and wrong for every failure, because console text written into a minimised
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

Nothing here is a substitute for the two gaps that actually stop this being a
product for other people: it is **single-user and has no HTTPS**. The README
says so plainly next to the install instructions rather than burying it.

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

**A free licence is a hard constraint, not a preference** - this is compiled into
a published binary. A genuinely famous song or film is almost certainly
somebody's copyright, so each item is as recognisable as a free licence allows
rather than as recognisable as possible. Two traps found while picking them:

- **The Richest Man in Babylon is not on Project Gutenberg** - its whole
  79,433-title catalogue was checked, and no work by Clason is either. The 1926
  edition is US public domain; the *later expanded* editions are not, and most
  copies circulating are those. Wikisource has it and tags it
  `{{PD-US|1957|1926}}`, which is a licence review by somebody whose job that is,
  so that is the source. The archive.org text hits are Internet Archive lending
  scans and anonymous uploads - neither is a licence basis.
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
are the interface" as the one item nobody recognises, and deleting it - the
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
  through to its normal path for anything it did not recognise, so
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

  Two fixes, and Tailscale is the better one: a `ts.net` address carries a real
  Let's Encrypt certificate, so it installs with no warning and works away from
  home as well. Otherwise `/ca.crt` has to be installed on each device, which
  Android accompanies with a standing "network may be monitored" notice.
  iPhone is unaffected: Safari's Add to Home Screen does not go through a
  service worker.
- **A service worker is served from `/`, not `/static/`.** Its scope is its own
  directory, so from `/static/sw.js` it could never control `/` - the only page
  there is. It would register, report success and intercept nothing. Same for
  the manifest's content type: Go's mime table has no `.webmanifest`, and
  Chrome ignores a manifest not handed to it as JSON, so both are served by
  hand in `internal/webui` rather than by the file server.
- **The service worker must never answer a range request or anything under
  `/api/`.** Caching a search result serves stale state; answering a range
  request without honouring `Range` breaks seeking in a way indistinguishable
  from a corrupt file. Requests it does not handle are left alone entirely -
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
- **Mobile layout is measured, not eyeballed.** The screenshot script asserts
  `document.scrollWidth <= clientWidth` per screen and lists anything sticking
  out, which is what found all of the above. Elements inside a deliberately
  side-scrolling container (the filter chips) have to be excluded or every
  screen reports a false positive.
- **An empty `libraries` list means nothing, and must never read back as nil.**
  `User.Libraries` has no `omitempty` for exactly this reason: with it, an
  account allowed no libraries would serialise to nothing, read back as nil,
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

  Skipped entirely on the `-Launch` path. That runs minimised from a desktop
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
  assumed.** It was `gabehollberg` for most of this project's life - guessed
  from an email address, and not a GitHub account at all (`gh api
  users/gabehollberg` answers 404). Every install URL in the README pointed at
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
  docker run --rm -v "//c/dev/atrium:/src" -w /src golang:1.24-alpine go test ./...
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
```

`docker compose logs -f SoundStorm` is the fastest way to see why a backend is not
answering.
