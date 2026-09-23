#!/bin/sh
# Rebuild the starter library: one item per stocked shelf, from its source.
#
# The bundle is in git, so normally there is nothing to run. This exists for two
# reasons. The committed copies were once damaged - every carriage return had
# been stripped out of them, which leaves a zip unopenable and an mp3 losing
# frame sync about once per 64KB - and a repository cannot repair that from
# itself. And two of the three are re-encoded rather than downloaded verbatim, so
# without this there is no record of how they were made.
#
#   sh scripts/fetch-starter-media.sh
#
# Wants docker (for ffmpeg, from the jellyfin image this project already pulls),
# curl and python. Downloads about 55MB, nearly all of it the eight audiobook
# chapters that get joined into one.
#
# Everything here is public domain or CC, and that is a hard constraint rather
# than a preference: these files are compiled into a binary and published. A
# genuinely well-known song or film is almost certainly somebody's copyright, so
# "well known" here means as recognisable as a free licence allows.
#
#   ebook      The Richest Man in Babylon (1926), George S. Clason, from
#              Wikisource, which tags it {{PD-US|1957|1926}} - the 1926 edition
#              specifically. Later expanded editions are still in copyright, and
#              most copies floating around are those, which is why this comes
#              from a source that reviews licences rather than from a search.
#              Not on Project Gutenberg; its 79,433-title catalogue was checked.
#   audiobook  As a Man Thinketh, read for LibriVox, public domain. Eight
#              chapter files joined into one and re-encoded at 32k mono - spoken
#              word, and even so it is 13MB of the 17MB bundle.
#   music      The Aria from Kimiko Ishizaka's Open Goldberg Variations (2012),
#              CC0, at 96k. One track of thirty-one.
#
# No film. Big Buck Bunny was bundled for one commit and measured at 25MB of a
# 42MB bundle - more than everything else combined, which is the arithmetic that
# kept video out to begin with. starter.Attributions points at Blender instead.
set -e

DEST=${DEST:-internal/starter/media}
FFIMAGE=${FFIMAGE:-jellyfin/jellyfin}
DELAY=${DELAY:-2}

WS="https://ws-export.wmcloud.org"
LV="https://archive.org/download/as_a_man_thinketh_mc_librivox"
OGV="https://archive.org/download/OpenGoldbergVariations"
OGV_TRACK="Kimiko Ishizaka - J.S. Bach- -Open- Goldberg Variations, BWV 988 (Piano) - 01 Aria.mp3"

if [ ! -d "$DEST" ]; then
  echo "No $DEST - run this from the repository root." >&2
  exit 1
fi

work=.starter-media
rm -rf "$work"
mkdir -p "$work/in" "$work/out"
trap 'rm -rf "$work"' EXIT

# Docker wants a Windows path on a Windows host; pwd -W gives one and fails
# everywhere else, which is exactly the test we want.
HOSTDIR=$(pwd -W 2>/dev/null || pwd)

ff() {
  MSYS_NO_PATHCONV=1 docker run --rm -v "$HOSTDIR/$work:/work" \
    --entrypoint /usr/lib/jellyfin-ffmpeg/ffmpeg "$FFIMAGE" \
    -nostdin -hide_banner -loglevel error -y "$@"
}

probe() {
  MSYS_NO_PATHCONV=1 docker run --rm -v "$HOSTDIR/$work:/work" \
    --entrypoint /usr/lib/jellyfin-ffmpeg/ffprobe "$FFIMAGE" \
    -v error -show_entries format=duration -of csv=p=0 "$1"
}

get() {
  # $1 url  $2 destination. Throttled, and the URL is encoded here because the
  # track names carry spaces - pre-encoding them would mean undoing it again to
  # write the file.
  url=$(printf '%s' "$1" | sed 's/ /%20/g')
  sleep "$DELAY"
  if ! curl -fsSL --max-time 900 -o "$2" "$url"; then
    echo "  FAILED $url" >&2
    exit 1
  fi
}

report() {
  printf '  %8.1f MB  %s\n' "$(python -c "import os,sys;print(os.path.getsize(sys.argv[1])/1e6)" "$1")" "$2"
}

echo
echo "  Ebook - The Richest Man in Babylon, from Wikisource"
# Author/Title like everything else on the shelf, so a fresh install starts out
# in the shape every later upload is filed into.
BOOK="$DEST/ebooks/George S. Clason/The Richest Man in Babylon/The Richest Man in Babylon - George S. Clason.epub"
mkdir -p "$(dirname "$BOOK")"
get "$WS/?lang=en&format=epub&page=The_Richest_Man_In_Babylon" "$work/out/book.epub"
# Checked before it is allowed to replace anything: an epub whose zip does not
# open is the failure this script exists to undo, and writing one into the
# bundle would ship it to every install.
python -c '
import sys, zipfile
z = zipfile.ZipFile(sys.argv[1])
if z.testzip() is not None:
    raise SystemExit("a member failed its CRC")
z.read("META-INF/container.xml")
' "$work/out/book.epub"
cp "$work/out/book.epub" "$BOOK"
report "$work/out/book.epub" "$(basename "$BOOK")"

echo
echo "  Audiobook - As a Man Thinketh, eight chapters joined"
: > "$work/in/list.txt"
i=0
while [ "$i" -le 7 ]; do
  get "$LV/asamanthinketh_${i}_allen.mp3" "$work/in/ch$i.mp3"
  echo "file '/work/in/ch$i.mp3'" >> "$work/in/list.txt"
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
  -id3v2_version 4 -write_id3v1 0 /work/out/book.mp3
cp "$work/out/book.mp3" "$BOOKDIR/As a Man Thinketh.mp3"
report "$work/out/book.mp3" "As a Man Thinketh.mp3  ($(probe /work/out/book.mp3)s)"

echo
echo "  Music - the Aria from the Open Goldberg Variations"
MUSICDIR="$DEST/music/Kimiko Ishizaka/Open Goldberg Variations, BWV 988"
mkdir -p "$MUSICDIR"
get "$OGV/$OGV_TRACK" "$work/in/aria.mp3"
ff -i /work/in/aria.mp3 -c:a libmp3lame -b:a 96k -map_metadata -1 \
  -metadata title="Aria" \
  -metadata artist="Kimiko Ishizaka" \
  -metadata album_artist="Kimiko Ishizaka" \
  -metadata album="Open Goldberg Variations, BWV 988" \
  -metadata composer="J.S. Bach" \
  -metadata track="1" \
  -metadata date="2012" \
  -metadata comment="CC0 1.0 Public Domain Dedication" \
  -id3v2_version 4 -write_id3v1 0 /work/out/aria.mp3
cp "$work/out/aria.mp3" "$MUSICDIR/01 Aria.mp3"
report "$work/out/aria.mp3" "01 Aria.mp3  ($(probe /work/out/aria.mp3)s)"

echo
echo "  Done. Check it with: go test ./internal/starter"
echo
