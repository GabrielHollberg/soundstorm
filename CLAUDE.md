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

A file with more than one audio language gets an Audio picker beside
Subtitles. Choosing one asks `/api/playback?audio=<stream index>`: a browser
plays only a file's first audio track, so another language is always Jellyfin
streaming HLS with `AudioStreamIndex`, and it plays on from the same moment.
Checked end to end on a throwaway Jellyfin 12.1.0 with a generated show:
seasons, Up next into season 2, and English to Spanish. Skipping intros is
not built: Jellyfin knows where they are only with a plugin.

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
  `Atomic Habits [1524779261]` beats the tag's `Atomic Habits (Unabridged)`,
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

**The same recording under another name is skipped.** An upload was only ever
refused when its destination name existed, and an iTunes library keeps
repurchases side by side: `03 Heathens.m4a` and `03 Heathens 1.m4a`. Measured
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
uploading the real `Heathens` pair (skipped, naming the original) and the real
`Notice Me` pair (kept) through `Save`.

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
  **And a tap on a touch screen moves to the next look**, in the sheet's
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
  on (`updateChapterCaption`). Checked on the stand-in book: left on
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
- **Four looks for the cover, a tap moving to the next** (`coverStyle` on
  the account; `coverSpin` from before still reads as "spin"): the cover, a
  spinning disc, a **record** and **moving with the music**. The record and the
  moving cover are a layer (`#np-deco`) laid exactly over the cover image,
  which stays in place (hidden, for the record), because the swipe, the hold
  icons and the cover morph all work on that image; the swipe moves the layer
  with it. The record is grooves turning at 4s a turn, the cover as the
  label, and the title, artist and album round it on an SVG textPath, whole
  repeats only (a first version cut "GLASSHOUS" off where the ring closed);
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
  then runs of booms - the "boom boom boom" at 0:37 in Thunder. Booms were a
  sub-bass (under 100 Hz) jump of 10dB out of a lull, three or more under
  0.45s apart; tuned on that file they found 0:37 exactly, but the owner
  reported the other strikes off the beat and the same booms at 2:17
  missed, and the hit list showed why: 10-20dB jumps every quarter second
  through the whole song, the booms no different by any measure tried. So
  it went back to the beat grid, which is what an eye checks lightning
  against. Those booms are syncopated, off the grid, so no beat rule strikes
  on them.

  **And the grid was wrong for fast songs, twice over.** The tempo search
  tried whole-frame lags (23ms): Thunder's 170 bpm falls between two, the
  error adds up over every beat, and 112 won. Lags now go in tenths of a
  frame, 70-180 bpm. Separately, two calls to `keepTime` for one song (the
  song starting and the look applying) raced: the second read the tempo
  lookup as "not known" while it was in flight and started hearing the song
  with no tempo to lean on, and the hearing is cached per song, so the
  later call's tempo never counted. In-flight lookups are now shared
  (`soundAsking`). With both, Thunder's beats sit at 0.714s (84, felt at
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
  unused pictures are deleted; removing a person removes theirs. Not in
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
at a hundred - then Your favorites (shuffled, only with
some), Library radio and Newly added. On a phone the three sit in one row,
the icon over the name.

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
- **Sleep timer** in Now Playing; "end of this song" stops in the `ended`
  handler instead of advancing - a chapter, for an audiobook. **It never stopped with the screen off**, which is when a sleep timer
  is used: the eight-second fade was stepped by animation frames, a phone
  with its screen off draws none, and the fade never got past its first
  step - reported as the timer not working. It is stepped by a timer now (a
  page playing sound keeps its timers); checked with animation frames
  switched off entirely, the music paused on time. **Custom time** is a menu
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
otherwise it reports a path built from the tags - `2CELLOS/2Cellos/01-06 -
The Resistance.m4a` for a file really called `06 The Resistance.m4a` - which
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
Drag handles only show in Custom order. The **Playlists page** is cards,
A to Z, like the mixes: a collage of up to four covers, name and count,
"New playlist" first; tapping opens one, a shuffle button on the cover
plays it shuffled (as a mix card has its play button), a hold offers Play,
Shuffle and Delete. It was a plain list of names with Play buttons.

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
`192.168.0.19` the client dialed. That reads correctly in a unit test with a
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
(`192-168-0-19.<id>...`). Railway cannot host that - it offers no UDP and no
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
an install registered `6lm2ahm6pn.home.soundstorm.dev`, got a staging
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
Storyteller began cutting the 47-hour Monte Cristo at its chapter marks.

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
