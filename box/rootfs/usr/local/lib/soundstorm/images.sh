#!/bin/sh
# Loads the container images built into the box, the first time it starts,
# and deletes each archive once loaded so the system disk gets the room back.
# An archive that fails to load is kept, for the next boot to try again, and
# said on the box's screen (storage-problem, screen.sh): EmberStorm would
# otherwise try for ever to download images named only for this box, the
# screen saying "starting" (the box's blind review).
set -eu
dir=/var/lib/soundstorm-images
failed=""
for tar in "$dir"/*.tar; do
	[ -f "$tar" ] || continue
	echo "soundstorm-images: loading $(basename "$tar")"
	if docker load -q -i "$tar"; then
		rm -f "$tar"
	else
		echo "soundstorm-images: could not load $tar; keeping it" >&2
		failed=1
	fi
done
if [ -n "$failed" ]; then
	mkdir -p /run/soundstorm
	printf '%s\n' "EmberStorm's programs could not be loaded from the box's built-in drive. Switch the box off and on again; if this stays, contact support." >/run/soundstorm/storage-problem
	exit 1
fi
