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
	--install zstd,gdisk,efibootmgr,cloud-guest-utils \
	--run-command 'growpart /dev/sda 1 && resize2fs /dev/sda1' \
	$copy_args \
	--run-command 'chmod 755 /usr/local/lib/emberstorm-install/install.sh' \
	--run-command 'chmod 644 /etc/systemd/system/emberstorm-install.service' \
	--run-command 'systemctl enable emberstorm-install' \
	--run-command 'systemctl disable unattended-upgrades apt-daily.timer apt-daily-upgrade.timer || true' \
	--run-command 'ln -sf /dev/null /etc/systemd/system/serial-getty@.service' \
	--root-password disabled \
	--truncate /etc/machine-id

say "Writing the stick's image"
qemu-img convert -O raw "$stick" "$out/emberstorm-installer.img"
rm -rf "$work"
ls -lh "$out/emberstorm-installer.img"
say "Done: write box/out/emberstorm-installer.img to a USB stick with balenaEtcher or Rufus"
