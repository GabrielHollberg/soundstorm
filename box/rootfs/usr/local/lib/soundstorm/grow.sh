#!/bin/sh
# Grows the system partition to fill the system disk: the image is built
# small, and the ME Mini's eMMC is 64GB. Safe to run every boot - once it
# fills the disk there is nothing to grow.
set -eu
src=$(findmnt -no SOURCE /)
disk=/dev/$(lsblk -no PKNAME "$src" | head -1)
part=$(cat "/sys/class/block/$(basename "$src")/partition")
if growpart "$disk" "$part" >/dev/null 2>&1; then
	resize2fs "$src"
	echo "soundstorm-grow: grew $src to fill $disk"
fi
