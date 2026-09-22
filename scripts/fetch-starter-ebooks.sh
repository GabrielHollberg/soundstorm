#!/bin/sh
# Re-fetch the ebooks bundled in internal/starter/media/ebooks.
#
# These are in git, so normally there is nothing to do. This exists because the
# committed copies were once damaged - every carriage return had been stripped
# out of them, which moves a zip's central directory and leaves nine of the ten
# unopenable - and a damaged bundle cannot be repaired from the repository. It
# has to be fetched again.
#
#   sh scripts/fetch-starter-ebooks.sh
#
# Nothing is written until a download has been checked, so a failed run leaves
# the existing files alone.
#
# It reads from gutenberg.pglaf.org, a mirror, at two second intervals. Project
# Gutenberg states plainly that its website "is intended for human users only"
# and that automated access "will result in a temporary or permanent block of
# your IP address"; the mirrors are the sanctioned route and serve byte
# identical files. Do not point this at www.gutenberg.org.
set -e

MIRROR=${MIRROR:-http://gutenberg.pglaf.org/cache/epub}
DEST=${DEST:-internal/starter/media/ebooks}
DELAY=${DELAY:-2}

# id|filename. The filenames are what internal/starter embeds, so they are not
# cosmetic: go:embed also rejects an apostrophe in one, which is why Alice's is
# spelled without it.
BOOKS='46|A Christmas Carol in Prose - Charles Dickens.epub
1080|A Modest Proposal - Jonathan Swift.epub
11|Alices Adventures in Wonderland - Lewis Carroll.epub
4507|As a man thinketh - James Allen.epub
84|Frankenstein - Mary Wollstonecraft Shelley.epub
219|Heart of Darkness - Joseph Conrad.epub
16|Peter Pan - J. M. Barrie.epub
1342|Pride and Prejudice - Jane Austen.epub
74|The Adventures of Tom Sawyer, Complete - Mark Twain.epub
1952|The Yellow Wallpaper - Charlotte Perkins Gilman.epub'

if [ ! -d "$DEST" ]; then
  echo "No $DEST - run this from the repository root." >&2
  exit 1
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

first=1
echo
printf '  Fetching %s ebooks from %s\n\n' "$(echo "$BOOKS" | wc -l | tr -d ' ')" "$MIRROR"

echo "$BOOKS" | while IFS='|' read -r id name; do
  [ -z "$id" ] && continue
  # Throttled, and before the request rather than after, so a failure part way
  # through does not turn the retry into a burst.
  if [ "$first" = 0 ]; then sleep "$DELAY"; fi
  first=0

  url="$MIRROR/$id/pg$id.epub"
  out="$tmp/$id.epub"
  if ! curl -fsSL --max-time 120 -o "$out" "$url"; then
    echo "  FAILED to download $url" >&2
    exit 1
  fi

  # Checked before it is allowed to replace anything. An epub whose zip does
  # not open is exactly the failure this script exists to undo, and writing one
  # into the bundle would ship it to every install.
  if ! python -c '
import sys, zipfile
p = sys.argv[1]
z = zipfile.ZipFile(p)
if z.testzip() is not None:
    raise SystemExit("a member failed its CRC")
z.read("META-INF/container.xml")
' "$out"; then
    echo "  REJECTED $name - the download did not open as an epub" >&2
    exit 1
  fi

  cp "$out" "$DEST/$name"
  printf '  %7d bytes  %s\n' "$(wc -c < "$out" | tr -d ' ')" "$name"
done

echo
echo "  Done. Check them with: go test ./internal/starter"
echo
