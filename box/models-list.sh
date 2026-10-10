#!/bin/sh
# The fingerprint of every file in the models a box carries (box/models.sh
# makes them, box/build.sh builds them in): "NAME SHA256 PATH" a file, "NAME
# link PATH -> TARGET" a link, sorted. box/models.sha256 is the list as last
# reviewed and committed; the build refuses models that differ from it, so a
# model - or the whisper.cpp program among them - changed on the machine
# they are taken from cannot reach a box unseen (the box's blind security
# review). Plain POSIX tools, so it gives the same text in Alpine (busybox)
# and Debian.
#
#   sh box/models-list.sh DIR NAME     the list for one model folder
#   sh box/models-list.sh -c DIR NAME  also refuse what no model needs:
#                                      setuid or setgid files, devices, fifos
set -eu
check=""
[ "${1:-}" = -c ] && { check=1; shift; }
dir=$1 name=$2
cd "$dir"
if [ -n "$check" ]; then
	odd=$(find . \( -perm -4000 -o -perm -2000 -o -type b -o -type c -o -type p -o -type s \) -print | head -n 5)
	if [ -n "$odd" ]; then
		echo "models $name hold what no model needs (setuid, setgid or a device):" >&2
		echo "$odd" >&2
		exit 1
	fi
fi
find . \( -type f -o -type l \) | LC_ALL=C sort | while IFS= read -r f; do
	if [ -L "$f" ]; then
		printf '%s link %s -> %s\n' "$name" "$f" "$(readlink "$f")"
	else
		printf '%s %s %s\n' "$name" "$(sha256sum <"$f" | cut -d' ' -f1)" "$f"
	fi
done
