#!/bin/sh
# Makes a signed release for boxes: every image in docker-compose.yml pinned
# to the digest it has right now, written as manifest.json and signed with
# the release key. Run where box/build.sh runs (it needs skopeo and Go):
#
#   sh box/release.sh -k KEYFILE -s SERIAL -v VERSION [-n NOTES] [-o DIR]
#
# -k yubikey leaves the manifest for the release key on its YubiKey: sign it
# on the PC the YubiKey is plugged into with py box/yubikey.py sign (the PIN
# and a touch), which writes manifest.json.sig as -k KEYFILE would.
#
# A release also carries the box's own files (DIR/system-SERIAL.tar.gz):
# box/rootfs, the compose files and the caretaker built here, with
# box/packages.txt and box/units.txt - so a fix to any of them reaches every
# box, where it used to need the USB stick at each (internal/caretaker/
# system.go). -S leaves them out (images only).
#
# Then publish DIR/manifest.json, DIR/manifest.json.sig and
# DIR/system-SERIAL.tar.gz as assets of the GitHub release "box-channel" (the
# caretaker's default address). SERIAL must be higher than the last
# release's: boxes ignore anything not newer.
#
# EmberStorm's own image is the one GitHub built from a commit on main, with
# its provenance checked (box/app-image.sh): APP_COMMIT=<commit or tag>,
# default this checkout's HEAD. Never ":latest".
#
# -i SERVICE=REF replaces one service's image (testing: a release that must
# fail its health check).
set -eu

here=$(cd "$(dirname "$0")" && pwd)
repo=$(dirname "$here")
key="" serial="" version="" notes="" dir="$here/out/release" swaps="" system=1
while getopts k:s:v:n:o:i:S opt; do
	case "$opt" in
	k) key=$OPTARG ;;
	s) serial=$OPTARG ;;
	v) version=$OPTARG ;;
	n) notes=$OPTARG ;;
	o) dir=$OPTARG ;;
	i) swaps="$swaps $OPTARG" ;;
	S) system="" ;;
	*) exit 2 ;;
	esac
done
[ -n "$key" ] && [ -n "$serial" ] && [ -n "$version" ] || {
	echo "usage: release.sh -k KEYFILE -s SERIAL -v VERSION [-n NOTES] [-o DIR]" >&2
	exit 2
}
mkdir -p "$dir"

# shellcheck source=box/app-image.sh
. "$here/app-image.sh"
case " $swaps" in
*" soundstorm="*) APP_IMAGE="" ;; # a test release names EmberStorm's image itself
*) app_image strict ;;
esac

pins=""
for line in $(awk '/^services:/{s=1;next} s&&/^[a-z]/{s=0}
	s&&/^  [a-z0-9-]+:$/{svc=$1; sub(":","",svc)}
	s&&/^    profiles:/{skip[svc]=1}
	s&&/^    image: /{img[svc]=$2; order[++n]=svc}
	END{for(i=1;i<=n;i++){v=order[i]; if(!skip[v]) print v "=" img[v]}}' "$repo/docker-compose.yml"); do
	svc=${line%%=*}
	ref=$(printf '%s' "${line#*=}" | sed 's/^\${[A-Z_]*:-\(.*\)}$/\1/')
	[ "$svc" = soundstorm ] && ref=$APP_IMAGE
	for swap in $swaps; do
		[ "${swap%%=*}" = "$svc" ] && ref=${swap#*=}
	done
	case "$ref" in
	*@sha256:*) pinned=$ref ;;
	*)
		# The digest is the sha256 of the manifest as the registry serves
		# it - one request, where inspect's own answer took a minute an
		# image (it reads the image's whole config too). Same digest.
		digest=$(skopeo inspect --raw "docker://$ref" | sha256sum | cut -d' ' -f1)
		pinned="${ref%:*}@sha256:$digest"
		;;
	esac
	echo "  $svc  $pinned"
	pins="$pins $svc=$pinned"
done

cd "$repo"
sysargs=""
if [ -n "$system" ]; then
	# The box's own files, as build.sh puts them on a box: LF throughout,
	# scripts and the caretaker 0755, the rest 0644, owned by root, in name
	# order with no dates, so the same files make the same bundle.
	bundle="$dir/system-$serial.tar.gz"
	stage=$(mktemp -d)
	cp -r "$here/rootfs/." "$stage/"
	rm -f "$stage/usr/local/lib/soundstorm/lock-grub.sh" # run once, at build
	mkdir -p "$stage/opt/soundstorm" "$stage/usr/local/bin"
	cp "$repo/docker-compose.yml" "$stage/opt/soundstorm/compose.yml"
	cp "$here/compose.box.yml" "$stage/opt/soundstorm/compose.box.yml"
	find "$stage" -type f -exec sed -i 's/\r$//' {} +
	CGO_ENABLED=0 GOTOOLCHAIN=auto GOFLAGS=-trimpath \
		go build -ldflags=-s -o "$stage/usr/local/bin/soundstorm-caretaker" ./cmd/soundstorm-caretaker
	find "$stage" -type f -exec chmod 644 {} +
	chmod 755 "$stage"/usr/local/lib/soundstorm/*.sh "$stage/usr/local/bin/soundstorm-caretaker"
	tar -C "$stage" --sort=name --owner=0 --group=0 --numeric-owner --mtime=@0 -czf "$bundle" .
	rm -rf "$stage"
	echo "  system files: $(basename "$bundle"), $(du -h "$bundle" | cut -f1)"
	sysargs="-system $bundle -packages $(grep -v '^#' "$here/packages.txt" | tr -d '\r' | paste -sd, -)"
	sysargs="$sysargs -enable $(grep -v '^#' "$here/units.txt" | tr -d '\r' | paste -sd, -)"
	sysargs="$sysargs -restart soundstorm-screen.service,soundstorm-address.timer,soundstorm-scrub.timer"
fi
# shellcheck disable=SC2086
GOTOOLCHAIN=auto go run ./cmd/soundstorm-caretaker manifest -serial "$serial" -version "$version" -notes "$notes" $sysargs $pins > "$dir/manifest.json"
if [ "$key" = yubikey ]; then
	rm -f "$dir/manifest.json.sig"
	echo "Made $dir/manifest.json. Sign it with the YubiKey, on Windows:"
	echo "  py box\yubikey.py sign $(wslpath -w "$dir/manifest.json" 2>/dev/null || echo "$dir/manifest.json")"
	exit 0
fi
GOTOOLCHAIN=auto go run ./cmd/soundstorm-caretaker sign "$key" "$dir/manifest.json"
echo "Signed: $dir/manifest.json and manifest.json.sig"
