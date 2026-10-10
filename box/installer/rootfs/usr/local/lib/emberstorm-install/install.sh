#!/bin/sh
# Runs when the box starts from the EmberStorm USB stick: writes the system
# onto the box's built-in drive (the eMMC), checks every byte, gives the copy
# disk IDs of its own and switches the box off. Made by box/installer.sh.
#
# The storage drive (NVMe) is left as it is - a box being put right keeps
# its accounts and media - unless the stick was made for the factory
# (FACTORY=1 when building it), which wipes it so the box starts new.
set -u

P=/opt/emberstorm-install
image="$P/box.raw.zst"
want_sum=$(cat "$P/box.sha256")
size=$(cat "$P/box.size")

clear_screen() { printf '\033[2J\033[H'; }
say() { printf '\n   %s\n' "$*"; }
fail() {
	printf '\n\n   \033[1;31mThe installation did not finish.\033[0m\n'
	say "$*"
	say "Nothing was written to the storage drive. Switch the box off,"
	say "start it from the stick again, and if this happens again, contact"
	say "support."
	exit 1
}

# Kernel messages over the screen would hide what it says.
dmesg -n 1 2>/dev/null || true
setterm --blank 0 --powersave off 2>/dev/null || true
clear_screen
printf '\n   \033[1mEmberStorm installer\033[0m\n'
udevadm settle 2>/dev/null || true
sleep 3

# The stick itself: the disk this system is running from. Never written.
self=$(lsblk -no PKNAME "$(findmnt -no SOURCE /)" 2>/dev/null | head -n 1)

# The built-in drive. An eMMC is mmcblkN (its boot0, boot1 and rpmb parts
# are not disks to write); a box with no eMMC names it when the stick is
# made (INSTALL_TARGET: the VM's is vda).
target=""
if [ -s "$P/target" ]; then
	target=$(cat "$P/target")
else
	found=$(lsblk -dno NAME | grep -E '^mmcblk[0-9]+$' | grep -vx "${self:-none}")
	[ "$(printf '%s\n' "$found" | grep -c .)" = 1 ] && target=$found
fi
[ -n "$target" ] && [ -b "/dev/$target" ] ||
	fail "This box's built-in drive was not found."
[ "$target" != "$self" ] || fail "The built-in drive is the stick itself."
have=$(blockdev --getsize64 "/dev/$target")
[ "$have" -ge "$size" ] ||
	fail "The built-in drive is too small: $((have / 1000000000)) GB, $((size / 1000000000 + 1)) GB needed."
part() { case "$1" in *[0-9]) echo "/dev/${1}p$2" ;; *) echo "/dev/$1$2" ;; esac; }

say "This erases the box's built-in drive ($((have / 1000000000)) GB) and installs"
say "EmberStorm on it."
if [ -e "$P/factory" ]; then
	say "This stick is for the factory: the storage drive is wiped too."
else
	say "Media, accounts and settings on the storage drive are kept."
fi
# A stick for putting a box right waits for somebody to say yes: plugged
# into the wrong computer it would wipe that computer's built-in drive (the
# box's blind security review). A factory stick, or one made for testing
# (INSTALL_AUTO), carries on by itself after 20 seconds.
if [ -e "$P/factory" ] || [ -e "$P/auto" ]; then
	say "To stop, switch the box off now. Starting in 20 seconds."
	sleep 20
else
	say "To install, press Enter. To stop, switch the box off."
	while :; do
		IFS= read -r answer || { sleep 5; continue; }
		[ -z "$answer" ] && break
	done
fi

clear_screen
printf '\n   \033[1mInstalling EmberStorm\033[0m - about ten minutes. Leave the box on.\n\n'
# Written straight to the drive, every block, then flushed. A failure on
# either side of the pipe is noted, as its status would be lost.
rm -f /tmp/write-failed /tmp/dd.log
(zstd -dc "$image" || echo zstd >> /tmp/write-failed) |
	(dd of="/dev/$target" bs=4M iflag=fullblock oflag=direct status=progress 2>/tmp/dd.log ||
		echo dd >> /tmp/write-failed) &
writer=$!
while kill -0 "$writer" 2>/dev/null; do
	written=$(tr '\r' '\n' < /tmp/dd.log 2>/dev/null | grep copied | tail -n 1 | cut -d' ' -f1)
	[ -n "$written" ] && printf '\r   %s%% written   ' "$((written * 100 / size))"
	sleep 2
done
wait "$writer"
[ ! -s /tmp/write-failed ] || fail "Writing to the built-in drive failed."
sync
printf '\r   100%% written   \n'

say "Checking what was written..."
echo 3 > /proc/sys/vm/drop_caches 2>/dev/null || true
got_sum=$(head -c "$size" "/dev/$target" | sha256sum | cut -d' ' -f1)
[ "$got_sum" = "$want_sum" ] ||
	fail "What was written to the built-in drive does not match. The drive may be failing."

say "Finishing..."
# The old disk IDs, then new ones: every box made from this stick - and the
# stick itself - would otherwise share them, and a box started with the
# stick still in could start the stick's system instead of its own.
old_root=$(blkid -c /dev/null -s PARTUUID -o value "$(part "$target" 1)")
old_efi=$(blkid -c /dev/null -s PARTUUID -o value "$(part "$target" 15)")
old_fs=$(blkid -c /dev/null -s UUID -o value "$(part "$target" 1)")
# -e moves the second copy of the partition table to the drive's end (the
# image is smaller than the drive); -G gives the disk and partitions new IDs.
sgdisk -e -G "/dev/$target" >/dev/null || fail "The built-in drive's partitions could not be set up."
blockdev --rereadpt "/dev/$target" 2>/dev/null || partprobe "/dev/$target" 2>/dev/null || true
udevadm settle 2>/dev/null || true
e2fsck -fy "$(part "$target" 1)" >/dev/null 2>&1
tune2fs -U random "$(part "$target" 1)" >/dev/null || fail "The built-in drive's system could not be given its own ID."
new_root=$(blkid -c /dev/null -s PARTUUID -o value "$(part "$target" 1)")
new_efi=$(blkid -c /dev/null -s PARTUUID -o value "$(part "$target" 15)")
new_fs=$(blkid -c /dev/null -s UUID -o value "$(part "$target" 1)")
[ -n "$old_root" ] && [ -n "$new_root" ] && [ -n "$old_fs" ] && [ -n "$new_fs" ] ||
	fail "The built-in drive's IDs could not be read."

mkdir -p /mnt/box
mount "$(part "$target" 1)" /mnt/box || fail "The installed system could not be opened."
mount "$(part "$target" 15)" /mnt/box/boot/efi || { umount /mnt/box; fail "The installed system's start-up files could not be opened."; }
for f in /mnt/box/etc/fstab /mnt/box/boot/grub/grub.cfg /mnt/box/boot/efi/EFI/debian/grub.cfg; do
	[ -f "$f" ] || continue
	sed -i -e "s/$old_root/$new_root/g" -e "s/$old_fs/$new_fs/g" ${old_efi:+-e "s/$old_efi/$new_efi/g"} "$f"
done
grep -q "$new_root" /mnt/box/etc/fstab && grep -q "$new_fs" /mnt/box/boot/efi/EFI/debian/grub.cfg
ok=$?
umount /mnt/box/boot/efi /mnt/box
[ "$ok" = 0 ] || fail "The installed system could not be told its new IDs."

# The box starts from its built-in drive first. The firmware also finds it
# by itself (EFI/BOOT), so a failure here is not fatal.
efibootmgr -q -c -d "/dev/$target" -p 15 -L EmberStorm -l '\EFI\debian\shimx64.efi' 2>/dev/null || true

if [ -e "$P/factory" ]; then
	for d in $(lsblk -dno NAME | grep -E '^nvme[0-9]+n[0-9]+$'); do
		[ "$d" = "$self" ] && continue
		say "Wiping the storage drive ($d)..."
		wipefs -a -f "/dev/$d" >/dev/null || fail "The storage drive could not be wiped."
	done
fi

clear_screen
printf '\n   \033[1;32mEmberStorm is installed.\033[0m\n'
say "Take the USB stick out. The box switches itself off in 30 seconds;"
say "switch it on again and it starts EmberStorm."
sync
sleep 30
systemctl poweroff
