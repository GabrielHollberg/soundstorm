#!/bin/sh
# Makes EmberStorm.dmg from a notarized EmberStorm.app: the app beside a link
# to Applications, the Mac's usual way to install - dragged across by Finder,
# so the app never has to move itself out of Downloads (which macOS guards:
# "EmberStorm would like to access files in your Downloads folder").
#
#   sh mac/make-dmg.sh /tmp/EmberStorm-mac-out/EmberStorm.app ~/Downloads/EmberStorm.dmg
#
# The disk image is signed with the Developer ID and notarized itself, with the
# App Store Connect key named by NOTARY_KEY, NOTARY_KEY_ID and NOTARY_ISSUER.
set -eu
app=$1
out=$2
: "${NOTARY_KEY:?the .p8 key file}" "${NOTARY_KEY_ID:?its id}" "${NOTARY_ISSUER:?its issuer}"

stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
ditto "$app" "$stage/EmberStorm.app"
ln -s /Applications "$stage/Applications"
rm -f "$out"
hdiutil create -volname EmberStorm -srcfolder "$stage" -format UDZO -fs HFS+ "$out" >/dev/null
codesign --sign "Developer ID Application" --timestamp "$out"
xcrun notarytool submit "$out" --key "$NOTARY_KEY" --key-id "$NOTARY_KEY_ID" --issuer "$NOTARY_ISSUER" --wait
xcrun stapler staple "$out"
spctl -a -t open --context context:primary-signature -vv "$out"
