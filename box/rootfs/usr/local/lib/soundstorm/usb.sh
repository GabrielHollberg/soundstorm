#!/bin/sh
# A USB drive plugged into the box. Plugged in, it is only noticed (seen):
# what it calls itself, its filesystem and size, written for the caretaker,
# nothing mounted - a stranger's stick is not read by the kernel's
# filesystem code until the owner says "bring in what is on it" in the app
# (the box's blind security review). The caretaker then opens it (mount):
# read-only, as a folder under /run/soundstorm/drives named after its label,
# which SoundStorm sees as /drives (httpapi/drives.go). Nothing on a drive is
# ever written: a dirty journal is not replayed, and a filesystem not listed
# here is not opened at all. soundstorm-usb@<partition> runs seen, and
# unmount when the drive goes.
#
#   usb.sh seen sdb1     usb.sh mount sdb1     usb.sh unmount sdb1
#
# The data drive (SSDATA) is internal and never USB; a drive labelled SSBACKUP
# is kept for backups (the caretaker's, not built yet) and not opened here.
set -eu

DIR=/run/soundstorm/drives
STATE=/run/soundstorm/usb
WAITING=/run/soundstorm/usb-waiting
log() { echo "soundstorm-usb: $*"; }
mkdir -p "$DIR" "$STATE" "$WAITING"
chmod 700 "$WAITING" "$STATE"

action=$1
part=$2
case $part in sd[a-z]*) ;; *) log "$part is not a USB partition"; exit 1 ;; esac
dev=/dev/$part

# The label as a folder may be called (drives.go takes letters, digits,
# space, dot, dash and underscore).
safe_label() {
	n=$(printf '%s' "${1:-USB drive}" | tr -c 'A-Za-z0-9 ._-' '-' | sed 's/^[^A-Za-z0-9]*//' | cut -c1-48)
	printf '%s' "${n:-USB drive}"
}

case $action in
seen)
	label=$(blkid -o value -s LABEL "$dev" 2>/dev/null || true)
	type=$(blkid -o value -s TYPE "$dev" 2>/dev/null || true)
	case $label in SSDATA | SSBACKUP) exit 0 ;; esac
	case $type in vfat | exfat | ntfs | ntfs3 | ext2 | ext3 | ext4) ;; *)
		log "$part: $type is not a filesystem SoundStorm opens"
		exit 0
		;;
	esac
	size=$(blockdev --getsize64 "$dev" 2>/dev/null || echo 0)
	printf 'label=%s\ntype=%s\nsize=%s\n' "$(safe_label "$label")" "$type" "$size" >"$WAITING/$part.new"
	mv "$WAITING/$part.new" "$WAITING/$part"
	log "$part ($type, ${label:-no label}) plugged in; waiting for the owner to open it"
	;;
mount)
	# Only a drive noticed, and only once.
	[ -f "$WAITING/$part" ] || { log "$part was not plugged in"; exit 1; }
	[ -f "$STATE/$part" ] && exit 0
	label=$(blkid -o value -s LABEL "$dev" 2>/dev/null || true)
	type=$(blkid -o value -s TYPE "$dev" 2>/dev/null || true)
	case $label in SSDATA | SSBACKUP) exit 0 ;; esac
	# Read-only, and nothing on it runs or acts as a device. Filesystems with
	# owners keep theirs; those without are readable by SoundStorm's user.
	#
	# Only the filesystems drives come in: a drive is anybody's, and each
	# filesystem the kernel reads is code a crafted one can aim at (the
	# twelfth security pass). FAT and exFAT (cards, most bought drives) and
	# ext (Linux) by the kernel; NTFS (Windows) by ntfs-3g, outside the
	# kernel. xfs, btrfs and HFS+ - rare on a drive somebody brings - are not
	# opened.
	opts=ro,nosuid,nodev,noexec
	case $type in
	vfat | exfat) opts=$opts,umask=022 ;;
	ntfs | ntfs3) type=ntfs-3g opts=$opts,umask=022 ;;
	ext2 | ext3 | ext4) opts=$opts,noload ;;
	*)
		log "$part: $type is not a filesystem SoundStorm opens"
		exit 0
		;;
	esac
	# Named after its label, made safe (drives.go takes letters, digits,
	# space, dot, dash and underscore); a second drive of the same name is
	# "Name 2".
	name=$(safe_label "$label")
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
	rm -f "$WAITING/$part"
	[ -f "$STATE/$part" ] || exit 0
	target=$(cat "$STATE/$part")
	# Lazily: the drive is already gone, or going.
	umount -l "$target" 2>/dev/null || true
	rmdir "$target" 2>/dev/null || true
	rm -f "$STATE/$part"
	log "$part closed"
	;;
esac
