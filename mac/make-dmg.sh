#!/bin/sh
# Makes EmberStorm.dmg from a notarized EmberStorm.app: the app beside a link
# to Applications, an arrow between them and what to do, the Mac's usual way
# to install - dragged across by Finder, so the app never has to move itself
# out of Downloads (which macOS guards: "EmberStorm would like to access files
# in your Downloads folder").
#
#   sh mac/make-dmg.sh /tmp/EmberStorm-mac-out/EmberStorm.app ~/Downloads/EmberStorm.dmg
#
# The window's layout is written by dmgbuild (no Finder scripting, so no
# permission to ask), in a Python of its own under ~/Library/Caches; the
# background is drawn by mac/dmg/background.py. The disk image is signed with
# the Developer ID and notarized itself, with the App Store Connect key named
# by NOTARY_KEY, NOTARY_KEY_ID and NOTARY_ISSUER.
set -eu
app=$1
out=$2
: "${NOTARY_KEY:?the .p8 key file}" "${NOTARY_KEY_ID:?its id}" "${NOTARY_ISSUER:?its issuer}"
here=$(cd "$(dirname "$0")/dmg" && pwd)

venv="$HOME/Library/Caches/emberstorm-dmg-venv"
if ! "$venv/bin/python" -c 'import dmgbuild, PIL' 2>/dev/null; then
	python3 -m venv "$venv"
	"$venv/bin/pip" install -q dmgbuild pillow
fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
"$venv/bin/python" "$here/background.py"
tiffutil -cathidpicheck "$here/background.png" "$here/background@2x.png" -out "$work/background.tiff" >/dev/null
rm -f "$out"
"$venv/bin/dmgbuild" -s "$here/settings.py" -D app="$app" -D background="$work/background.tiff" EmberStorm "$out"
codesign --sign "Developer ID Application" --timestamp "$out"
xcrun notarytool submit "$out" --key "$NOTARY_KEY" --key-id "$NOTARY_KEY_ID" --issuer "$NOTARY_ISSUER" --wait
xcrun stapler staple "$out"
spctl -a -t open --context context:primary-signature -vv "$out"
