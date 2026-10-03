#!/bin/sh
# Makes a signed release for boxes: every image in docker-compose.yml pinned
# to the digest it has right now, written as manifest.json and signed with
# the release key. Run where box/build.sh runs (it needs skopeo and Go):
#
#   sh box/release.sh -k KEYFILE -s SERIAL -v VERSION [-n NOTES] [-o DIR]
#
# Then publish DIR/manifest.json and DIR/manifest.json.sig as assets of the
# GitHub release "box-channel" (the caretaker's default address). SERIAL must
# be higher than the last release's: boxes ignore anything not newer.
#
# -i SERVICE=REF replaces one service's image (testing: a release that must
# fail its health check).
set -eu

here=$(cd "$(dirname "$0")" && pwd)
repo=$(dirname "$here")
key="" serial="" version="" notes="" dir="$here/out/release" swaps=""
while getopts k:s:v:n:o:i: opt; do
	case "$opt" in
	k) key=$OPTARG ;;
	s) serial=$OPTARG ;;
	v) version=$OPTARG ;;
	n) notes=$OPTARG ;;
	o) dir=$OPTARG ;;
	i) swaps="$swaps $OPTARG" ;;
	*) exit 2 ;;
	esac
done
[ -n "$key" ] && [ -n "$serial" ] && [ -n "$version" ] || {
	echo "usage: release.sh -k KEYFILE -s SERIAL -v VERSION [-n NOTES] [-o DIR]" >&2
	exit 2
}
mkdir -p "$dir"

pins=""
for line in $(awk '/^services:/{s=1;next} s&&/^[a-z]/{s=0}
	s&&/^  [a-z0-9-]+:$/{svc=$1; sub(":","",svc)}
	s&&/^    profiles:/{skip[svc]=1}
	s&&/^    image: /{img[svc]=$2; order[++n]=svc}
	END{for(i=1;i<=n;i++){v=order[i]; if(!skip[v]) print v "=" img[v]}}' "$repo/docker-compose.yml"); do
	svc=${line%%=*}
	ref=$(printf '%s' "${line#*=}" | sed 's/^\${[A-Z_]*:-\(.*\)}$/\1/')
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
# shellcheck disable=SC2086
GOTOOLCHAIN=auto go run ./cmd/soundstorm-caretaker manifest -serial "$serial" -version "$version" -notes "$notes" $pins > "$dir/manifest.json"
GOTOOLCHAIN=auto go run ./cmd/soundstorm-caretaker sign "$key" "$dir/manifest.json"
echo "Signed: $dir/manifest.json and manifest.json.sig"
