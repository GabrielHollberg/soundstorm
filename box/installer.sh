#!/bin/sh
# Makes the EmberStorm USB stick: a small system that, started on a box,
# writes box/out/soundstorm-box.qcow2 onto its built-in drive (the eMMC),
# checks it, gives it disk IDs of its own and switches the box off
# (installer/rootfs/.../install.sh). It is how a real box gets its system -
# the eMMC is soldered in, so it cannot be written from another computer -
# and how one is put right later. Run as root where box/build.sh ran, after
# it:
#
#   wsl -d Debian -u root -- sh box/installer.sh
#
# The result, box/out/emberstorm-installer.img, is written to a USB stick of
# 16 GB or more with balenaEtcher or Rufus (as a disk image, "DD mode").
#
#   FACTORY=1          the stick also wipes the storage drive (NVMe), so the
#                      box starts new; without it a box keeps its media and
#                      accounts, for putting one right.
#   INSTALL_TARGET=vda the drive to write when it is not an eMMC (the VM's).
#   INSTALL_AUTO=1     install without waiting for Enter (testing in the VM;
#                      a FACTORY stick never waits either).
set -eu

here=$(cd "$(dirname "$0")" && pwd)
out="${OUT:-$here/out}"
work="$out/work-installer"
IMG=debian-13-nocloud-amd64.qcow2
box="$out/soundstorm-box.qcow2"

say() { printf '\n== %s\n' "$*"; }

[ "$(id -u)" = 0 ] || { echo "Run as root." >&2; exit 1; }
[ -f "$box" ] || { echo "No $box: run box/build.sh first." >&2; exit 1; }
[ -f "$out/$IMG" ] || { echo "No Debian base image in $out: run box/build.sh first." >&2; exit 1; }
for tool in zstd virt-customize qemu-img; do
	command -v "$tool" >/dev/null || { echo "Missing $tool (apt-get install zstd libguestfs-tools qemu-utils)." >&2; exit 1; }
done
export LIBGUESTFS_BACKEND=direct
rm -rf "$work"
mkdir -p "$work/stage/opt/emberstorm-install"
payload="$work/stage/opt/emberstorm-install"

say "The box's system, as it will be written"
qemu-img convert -O raw "$box" "$work/box.raw"
# Only as far as the last partition: the rest of the image is empty, and
# the box grows its system to fill the drive at first start.
end=$(sfdisk -l -o End -q "$work/box.raw" | tail -n +2 | sort -n | tail -n 1)
size=$(((end + 1) * 512 + 1024 * 1024)) # and room for the second partition table
truncate -s "$size" "$work/box.raw"
sha256sum "$work/box.raw" | cut -d' ' -f1 > "$payload/box.sha256"
echo "$size" > "$payload/box.size"
zstd -q -T0 -10 --rm -o "$payload/box.raw.zst" "$work/box.raw"
ls -lh "$payload/box.raw.zst"
[ "${FACTORY:-}" = 1 ] && : > "$payload/factory"
[ -z "${INSTALL_TARGET:-}" ] || echo "$INSTALL_TARGET" > "$payload/target"
[ "${INSTALL_AUTO:-}" = 1 ] && : > "$payload/auto"

cp -r "$here/installer/rootfs/." "$work/stage/"
find "$work/stage" -type f ! -name '*.zst' -exec sed -i 's/\r$//' {} +
copy_args=""
for top in "$work/stage"/*; do
	copy_args="$copy_args --copy-in $top:/"
done

say "The stick"
stick="$work/emberstorm-installer.qcow2"
cp "$out/$IMG" "$stick"
zst=$(stat -c %s "$payload/box.raw.zst")
qemu-img resize -q "$stick" "$(((zst / 1073741824) + 4))G"
# shellcheck disable=SC2086
virt-customize -a "$stick" \
	--hostname emberstorm-installer \
	--install zstd,gdisk,efibootmgr,cloud-guest-utils,qrencode \
	--run-command 'growpart /dev/sda 1 && resize2fs /dev/sda1' \
	$copy_args \
	--run-command 'chmod 755 /usr/local/lib/emberstorm-install/install.sh' \
	--run-command 'chmod 644 /etc/systemd/system/emberstorm-install.service' \
	--run-command 'systemctl enable emberstorm-install' \
	--run-command 'systemctl disable unattended-upgrades apt-daily.timer apt-daily-upgrade.timer || true' \
	--run-command 'ln -sf /dev/null /etc/systemd/system/serial-getty@.service' \
	--root-password disabled \
	--truncate /etc/machine-id

say "The stick's own disk IDs"
# Made from the same Debian base as the box's system, the stick would share
# its disk IDs with the copy it writes - and with an install cut short (the
# power, a failed check) before that copy got IDs of its own, starting from
# the stick again could start the half-written copy instead (the box's blind
# review). So the stick gets IDs no box has.
ids=$(guestfish --ro -a "$stick" run : part-get-gpt-guid /dev/sda 1 : part-get-gpt-guid /dev/sda 15 : vfs-uuid /dev/sda1)
old_root=$(echo "$ids" | sed -n 1p)
old_efi=$(echo "$ids" | sed -n 2p)
old_fs=$(echo "$ids" | sed -n 3p)
[ -n "$old_root" ] && [ -n "$old_efi" ] && [ -n "$old_fs" ] || { echo "Could not read the stick's disk IDs." >&2; exit 1; }
new_root=$(cat /proc/sys/kernel/random/uuid)
new_efi=$(cat /proc/sys/kernel/random/uuid)
new_fs=$(cat /proc/sys/kernel/random/uuid)
guestfish --rw -a "$stick" <<EOF
run
part-set-gpt-guid /dev/sda 1 $new_root
part-set-gpt-guid /dev/sda 15 $new_efi
e2fsck-f /dev/sda1
set-uuid /dev/sda1 $new_fs
mount /dev/sda1 /
mount /dev/sda15 /boot/efi
sh "sed -i -e 's/$old_root/$new_root/gI' -e 's/$old_efi/$new_efi/gI' -e 's/$old_fs/$new_fs/gI' /etc/fstab /boot/grub/grub.cfg /boot/efi/EFI/debian/grub.cfg"
EOF
counts=$(guestfish --ro -a "$stick" -i sh "grep -c $new_root /etc/fstab; grep -c $new_fs /boot/efi/EFI/debian/grub.cfg; grep -c $new_root /boot/grub/grub.cfg" || true)
if [ -z "$counts" ] || printf '%s
' "$counts" | grep -qx 0; then
	echo "The stick's new disk IDs were not written everywhere." >&2
	exit 1
fi
echo "  root $new_root, filesystem $new_fs"

say "Writing the stick's image"
qemu-img convert -O raw "$stick" "$out/emberstorm-installer.img"
rm -rf "$work"
ls -lh "$out/emberstorm-installer.img"
say "Done: write box/out/emberstorm-installer.img to a USB stick with balenaEtcher or Rufus"
