#!/bin/sh
# Opens a USB drive plugged into the box, read-only, as a folder under
# /run/soundstorm/drives named after its label - which SoundStorm sees as
# /drives and offers to bring media in from (httpapi/drives.go). Nothing on a
# drive is ever written: a dirty journal is not replayed, and a filesystem not
# listed here is not opened at all. Called by soundstorm-usb@<partition>.
#
#   usb.sh mount sdb1     usb.sh unmount sdb1
#
# The data drive (SSDATA) is internal and never USB; a drive labelled SSBACKUP
# is kept for backups (the caretaker's, not built yet) and not opened here.
set -eu

DIR=/run/soundstorm/drives
STATE=/run/soundstorm/usb
log() { echo "soundstorm-usb: $*"; }
mkdir -p "$DIR" "$STATE"

action=$1
part=$2
dev=/dev/$part

case $action in
mount)
	label=$(blkid -o value -s LABEL "$dev" 2>/dev/null || true)
	type=$(blkid -o value -s TYPE "$dev" 2>/dev/null || true)
	case $label in SSDATA | SSBACKUP) exit 0 ;; esac
	# Read-only, and nothing on it runs or acts as a device. Filesystems with
	# owners keep theirs; those without are readable by SoundStorm's user.
	opts=ro,nosuid,nodev,noexec
	case $type in
	vfat | exfat) opts=$opts,umask=022 ;;
	ntfs | ntfs3) type=ntfs3 opts=$opts,umask=022 ;;
	ext2 | ext3 | ext4) opts=$opts,noload ;;
	xfs) opts=$opts,norecovery ;;
	btrfs) opts=$opts,rescue=nologreplay ;;
	hfsplus) ;;
	*)
		log "$part: $type is not a filesystem SoundStorm opens"
		exit 0
		;;
	esac
	# Named after its label, made safe (drives.go takes letters, digits,
	# space, dot, dash and underscore); a second drive of the same name is
	# "Name 2".
	name=$(printf '%s' "${label:-USB drive}" | tr -c 'A-Za-z0-9 ._-' '-' | sed 's/^[^A-Za-z0-9]*//' | cut -c1-48)
	[ -n "$name" ] || name="USB drive"
	target="$DIR/$name"
	n=2
	while [ -e "$target" ]; do
		target="$DIR/$name $n"
		n=$((n + 1))
	done
	mkdir "$target"
	if ! mount -t "$type" -o "$opts" "$dev" "$target"; then
		rmdir "$target"
		log "$part: could not open it"
		exit 1
	fi
	printf '%s\n' "$target" >"$STATE/$part"
	log "$part ($type, ${label:-no label}) opened at $target"
	;;
unmount)
	[ -f "$STATE/$part" ] || exit 0
	target=$(cat "$STATE/$part")
	# Lazily: the drive is already gone, or going.
	umount -l "$target" 2>/dev/null || true
	rmdir "$target" 2>/dev/null || true
	rm -f "$STATE/$part"
	log "$part closed"
	;;
esac
