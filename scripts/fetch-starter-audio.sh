#!/bin/sh
# Rebuild the audio bundled in internal/starter/media from its sources.
#
# Like scripts/fetch-starter-ebooks.sh, this is a repair tool rather than part
# of a build: the files are in git and normally nothing needs to run. It exists
# because the committed copies were once damaged - every carriage return had
# been stripped out of them, which in an mp3 means the decoder loses frame sync
# roughly once per 64KB - and a repository cannot repair that from itself.
#
#   sh scripts/fetch-starter-audio.sh
#
# Unlike the ebooks, these are not downloaded verbatim: they are re-encoded
# smaller, because the whole bundle is embedded in the binary and the recordings
# as published are several times the size of the budget. So this script is also
# the record of how they were made, which did not exist before and is why the
# damage could not simply be undone.
#
#   music      Kimiko Ishizaka, The Open Goldberg Variations (2012), CC0.
#              The first four tracks only, at 96k. All 31 would be 80MB.
#   audiobook  As a Man Thinketh, read for LibriVox, public domain. The eight
#              chapter files joined into one, at 32k mono - spoken word, and
#              the bundle's largest single item either way.
#
# ffmpeg comes from the jellyfin image, which this project already pulls, so
# there is nothing to install. Python is used to read one JSON listing.
set -e

DEST=${DEST:-internal/starter/media}
FFIMAGE=${FFIMAGE:-jellyfin/jellyfin}
DELAY=${DELAY:-2}

OGV="https://archive.org/download/OpenGoldbergVariations"
OGV_PREFIX="Kimiko Ishizaka - J.S. Bach- -Open- Goldberg Variations, BWV 988 (Piano) - "
LV="https://archive.org/download/as_a_man_thinketh_mc_librivox"

if [ ! -d "$DEST" ]; then
  echo "No $DEST - run this from the repository root." >&2
  exit 1
fi

# A working directory inside the repository, because it has to be bind mounted
# into a container and a path under /tmp is not reachable from one on Windows.
work=.starter-audio
rm -rf "$work"
mkdir -p "$work/in" "$work/out"
trap 'rm -rf "$work"' EXIT

# Docker wants a Windows path on a Windows host; pwd -W gives one and fails
# everywhere else, which is exactly the test we want.
HOSTDIR=$(pwd -W 2>/dev/null || pwd)

ff() {
  MSYS_NO_PATHCONV=1 docker run --rm \
    -v "$HOSTDIR/$work:/work" \
    --entrypoint /usr/lib/jellyfin-ffmpeg/ffmpeg "$FFIMAGE" \
    -nostdin -hide_banner -loglevel error -y "$@"
}

probe() {
  MSYS_NO_PATHCONV=1 docker run --rm \
    -v "$HOSTDIR/$work:/work" \
    --entrypoint /usr/lib/jellyfin-ffmpeg/ffprobe "$FFIMAGE" \
    -v error -show_entries format=duration -of csv=p=0 "$1"
}

get() {
  # $1 url  $2 destination. Throttled: these are donated services.
  #
  # The track names carry spaces, so the URL is encoded here rather than being
  # pasted in pre-encoded - the names are read from the listing and a %20 in a
  # filename would then have to be undone again to write the file.
  url=$(printf '%s' "$1" | sed 's/ /%20/g')
  sleep "$DELAY"
  if ! curl -fsSL --max-time 600 -o "$2" "$url"; then
    echo "  FAILED $url" >&2
    exit 1
  fi
}

echo
echo "  Music - Open Goldberg Variations, first four tracks"

# track|source suffix|destination name|title tag
MUSICDIR="$DEST/music/Kimiko Ishizaka/Open Goldberg Variations, BWV 988"
mkdir -p "$MUSICDIR"
while IFS='|' read -r n src dest title; do
  [ -z "$n" ] && continue
  get "$OGV/$OGV_PREFIX$src" "$work/in/$n.mp3"
  ff -i "/work/in/$n.mp3" -c:a libmp3lame -b:a 96k -map_metadata -1 \
    -metadata title="$title" \
    -metadata artist="Kimiko Ishizaka" \
    -metadata album_artist="Kimiko Ishizaka" \
    -metadata album="Open Goldberg Variations, BWV 988" \
    -metadata composer="J.S. Bach" \
    -metadata track="$n" \
    -metadata date="2012" \
    -metadata comment="CC0 1.0 Public Domain Dedication" \
    -id3v2_version 4 -write_id3v1 0 "/work/out/$n.mp3"
  cp "$work/out/$n.mp3" "$MUSICDIR/$dest"
  printf '  %8d bytes  %5.1fs  %s\n' \
    "$(wc -c < "$work/out/$n.mp3" | tr -d ' ')" "$(probe "/work/out/$n.mp3")" "$dest"
done <<'TRACKS'
1|01 Aria.mp3|01 Aria.mp3|Aria
2|02 Variatio 1 a 1 Clav..mp3|02 Variatio 1 a 1 Clav..mp3|Variatio 1 a 1 Clav.
3|03 Variatio 2 a 1 Clav..mp3|03 Variatio 2 a 1 Clav..mp3|Variatio 2 a 1 Clav.
4|04 Variatio 3 a 1 Clav. Canone all Unisuono.mp3|04 Variatio 3 a 1 Clav. Canone allUnisuono.mp3|Variatio 3 a 1 Clav. Canone all'Unisuono
TRACKS

echo
echo "  Audiobook - As a Man Thinketh, eight chapters joined"

: > "$work/in/list.txt"
i=0
while [ "$i" -le 7 ]; do
  get "$LV/asamanthinketh_${i}_allen.mp3" "$work/in/ch$i.mp3"
  echo "file '/work/in/ch$i.mp3'" >> "$work/in/list.txt"
  printf '  %8d bytes  chapter %d\n' "$(wc -c < "$work/in/ch$i.mp3" | tr -d ' ')" "$i"
  i=$((i + 1))
done

BOOKDIR="$DEST/audiobooks/James Allen/As a Man Thinketh"
mkdir -p "$BOOKDIR"
ff -f concat -safe 0 -i /work/in/list.txt -c:a libmp3lame -b:a 32k -ac 1 \
  -map_metadata -1 \
  -metadata title="As a Man Thinketh" \
  -metadata artist="James Allen" \
  -metadata album="As a Man Thinketh" \
  -metadata date="1903" \
  -metadata comment="LibriVox public domain recording" \
  -id3v2_version 4 -write_id3v1 0 "/work/out/book.mp3"
cp "$work/out/book.mp3" "$BOOKDIR/As a Man Thinketh.mp3"
printf '\n  %8d bytes  %.1fs  As a Man Thinketh.mp3\n' \
  "$(wc -c < "$work/out/book.mp3" | tr -d ' ')" "$(probe /work/out/book.mp3)"

echo
echo "  Done. Check them with: go test ./internal/starter"
echo
