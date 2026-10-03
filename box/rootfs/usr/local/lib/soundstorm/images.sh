#!/bin/sh
# Loads the container images built into the box, the first time it starts,
# and deletes each archive once loaded so the system disk gets the room back.
# An archive that fails to load is kept, for the next boot to try again.
set -eu
dir=/var/lib/soundstorm-images
for tar in "$dir"/*.tar; do
	[ -f "$tar" ] || continue
	echo "soundstorm-images: loading $(basename "$tar")"
	if docker load -q -i "$tar"; then
		rm -f "$tar"
	else
		echo "soundstorm-images: could not load $tar; keeping it" >&2
	fi
done
