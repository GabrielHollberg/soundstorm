#!/bin/sh
# Mounts the box's data drive at /srv/soundstorm, preparing it the first time.
#
# The data drive is the internal disk that is not the system disk (on the ME
# Mini, the NVMe drive beside the eMMC). It is known by its label, SSDATA.
# A disk is only ever formatted when it is blank - no partition table, no
# filesystem, nothing a probe recognises. A disk holding anything at all is
# left alone, whatever it is: losing somebody's files to a wrong guess here is
# the one mistake this script must never make. USB disks are never taken:
# those are backup drives.
#
# Without a data drive the box still starts, on the system disk, and says so
# in /run/soundstorm/no-data-drive for the app to report.
set -eu

MNT=/srv/soundstorm
LABEL=SSDATA
log() { echo "soundstorm-storage: $*"; }

mkdir -p "$MNT" /run/soundstorm
rm -f /run/soundstorm/no-data-drive

mounted() { mountpoint -q "$MNT"; }

root_disk() {
	src=$(findmnt -no SOURCE /)
	lsblk -no PKNAME "$src" | head -1
}

# A disk is blank when nothing on it is recognised: no partitions, and no
# signature (filesystem, RAID member, partition table) by a full probe.
blank() {
	dev=/dev/$1
	[ "$(lsblk -no NAME "$dev" | wc -l)" -eq 1 ] || return 1
	[ -z "$(blkid -p -o value -s TYPE "$dev" 2>/dev/null)" ] || return 1
	[ -z "$(blkid -p -o value -s PTTYPE "$dev" 2>/dev/null)" ] || return 1
	return 0
}

if ! mounted; then
	dev=$(blkid -L "$LABEL" 2>/dev/null || true)
	if [ -z "$dev" ]; then
		rootd=$(root_disk)
		# Whole internal disks: not the system disk, not USB, not removable,
		# not loop/zram/optical.
		for name in $(lsblk -dno NAME,TYPE,TRAN,RM | awk '$2=="disk" && $3!="usb" && $NF=="0" {print $1}'); do
			[ "$name" = "$rootd" ] && continue
			case "$name" in zram* | loop* | sr* | mmcblk*boot*) continue ;; esac
			if blank "$name"; then
				log "preparing /dev/$name as the data drive"
				mkfs.btrfs -q -L "$LABEL" "/dev/$name"
				udevadm settle
				dev=/dev/$name
				break
			fi
			log "/dev/$name is not blank; leaving it alone"
		done
	fi
	if [ -n "$dev" ]; then
		grep -q "LABEL=$LABEL" /etc/fstab ||
			echo "LABEL=$LABEL $MNT btrfs defaults,noatime,nofail,x-systemd.device-timeout=10s 0 0" >> /etc/fstab
		mount "$MNT"
		log "data drive $dev mounted at $MNT"
	else
		log "no data drive; keeping everything on the system disk"
		touch /run/soundstorm/no-data-drive
	fi
fi

# The three parts, as subvolumes on the data drive so each can be snapshotted
# on its own: the library, the volumes that hold data, and caches.
for part in library volumes cache; do
	if [ ! -e "$MNT/$part" ]; then
		if mounted && [ "$(findmnt -no FSTYPE "$MNT")" = btrfs ]; then
			btrfs -q subvolume create "$MNT/$part"
		else
			mkdir -p "$MNT/$part"
		fi
	fi
done

# Anyone's files go in the library, whoever the backends run as (see the
# note on 0777 library folders in CLAUDE.md). The shelves are made here, not
# left to SoundStorm: Docker starts the backends first and makes any missing
# folder they mount as root's, 0755, which SoundStorm (uid 10001) then cannot
# write into - the first VM boot lost its starter song and audiobook so.
chmod 0777 "$MNT/library"
for shelf in music movies tv audiobooks ebooks documents pictures; do
	mkdir -p "$MNT/library/$shelf"
	chmod 0777 "$MNT/library/$shelf"
done

# Every folder compose.box.yml binds a volume to.
sed -n 's|.*device: \(/srv/soundstorm/[a-z]*/[a-z0-9-]*\).*|\1|p' /opt/soundstorm/compose.box.yml |
	while read -r dir; do mkdir -p "$dir"; done

# SoundStorm runs as uid 10001 and must own its state; the backends' own
# images fix their folders' ownership themselves when they start.
state="$MNT/volumes/soundstorm-state"
[ "$(stat -c %u "$state")" = 10001 ] || chown 10001:10001 "$state"
