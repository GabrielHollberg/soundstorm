#!/bin/sh
# Builds the SoundStorm box's system disk: Debian 13 with Docker, the compose
# files and the services that prepare the data drive and start SoundStorm.
#
# Run as root on a Debian machine with KVM - on the Windows PC that is the
# Debian WSL distro:
#
#   wsl -d Debian -u root -- sh box/build.sh
#
# The result is box/out/soundstorm-box.qcow2. box/run-vm.sh boots it.
#
# Settings, all optional:
#   DEV_SSH=1        let root in over SSH with this machine's key (development
#                    only - a box that is sold has no SSH)
#   SIZE=12G         the system disk's size; it grows to fill the real disk
#                    (the ME Mini's 64GB eMMC) on first boot
set -eu

here=$(cd "$(dirname "$0")" && pwd)
repo=$(dirname "$here")
out="${OUT:-$here/out}"
work="$out/work"
mkdir -p "$out" "$work"

SIZE=${SIZE:-12G}
# A dated release directory would pin the base exactly; "latest" is the
# newest point release, checked against its own SHA512SUMS.
RELEASE=${DEBIAN_RELEASE:-latest}
BASE="https://cloud.debian.org/images/cloud/trixie/$RELEASE"
IMG=debian-13-nocloud-amd64.qcow2

say() { printf '\n== %s\n' "$*"; }

[ "$(id -u)" = 0 ] || { echo "Run as root." >&2; exit 1; }
[ -e /dev/kvm ] || { echo "No /dev/kvm: this machine cannot run the build's helper VM fast." >&2; exit 1; }

say "Tools"
need=""
for pkg in libguestfs-tools qemu-utils qemu-system-x86 ovmf curl linux-image-amd64; do
	dpkg -s "$pkg" >/dev/null 2>&1 || need="$need $pkg"
done
if [ -n "$need" ]; then
	# linux-image-amd64 is not booted here: libguestfs builds its helper VM
	# from a kernel in /boot, and WSL has none of its own there.
	apt-get update -q
	DEBIAN_FRONTEND=noninteractive apt-get install -yq --no-install-recommends $need
fi
export LIBGUESTFS_BACKEND=direct

say "Debian base image"
if [ ! -f "$out/$IMG" ]; then
	curl -fL --retry 3 -o "$out/$IMG.part" "$BASE/$IMG"
	mv "$out/$IMG.part" "$out/$IMG"
fi
curl -fsSL --retry 3 -o "$out/SHA512SUMS" "$BASE/SHA512SUMS"
(cd "$out" && grep " $IMG\$" SHA512SUMS | sha512sum -c -) || {
	echo "The base image does not match Debian's checksum; delete $out/$IMG and run again." >&2
	exit 1
}

say "Files for the box"
# Everything copied in is made LF here, whatever the checkout did to it: a
# carriage return at the end of a shebang or a unit line breaks it silently.
stage="$work/stage"
rm -rf "$stage"
mkdir -p "$stage"
cp -r "$here/rootfs/." "$stage/"
mkdir -p "$stage/opt/soundstorm"
cp "$repo/docker-compose.yml" "$stage/opt/soundstorm/compose.yml"
cp "$here/compose.box.yml" "$stage/opt/soundstorm/compose.box.yml"
find "$stage" -type f -exec sed -i 's/\r$//' {} +
# Each top folder is copied in whole; copying merges into what is there.
copy_args=""
for top in "$stage"/*; do
	copy_args="$copy_args --copy-in $top:/"
done

ssh_args=""
if [ "${DEV_SSH:-}" = 1 ]; then
	[ -f /root/.ssh/id_ed25519 ] || ssh-keygen -q -t ed25519 -N '' -f /root/.ssh/id_ed25519
	ssh_args="--ssh-inject root:file:/root/.ssh/id_ed25519.pub"
fi

say "Building the system disk"
disk="$work/soundstorm-box.qcow2"
cp "$out/$IMG" "$disk"
qemu-img resize -q "$disk" "$SIZE"
# shellcheck disable=SC2086
virt-customize -a "$disk" \
	--hostname soundstorm \
	--install docker.io,docker-compose,btrfs-progs,cloud-guest-utils,avahi-daemon,openssh-server,curl \
	--run-command 'growpart /dev/sda 1 && resize2fs /dev/sda1' \
	$copy_args \
	--run-command 'chmod 755 /usr/local/lib/soundstorm/*.sh' \
	--run-command 'chmod 644 /etc/systemd/system/soundstorm*.service /etc/systemd/system/ssh-hostkeys.service' \
	--run-command 'docker compose version' \
	--run-command 'systemctl enable docker soundstorm-grow soundstorm-storage soundstorm ssh-hostkeys avahi-daemon' \
	--run-command 'rm -f /etc/ssh/ssh_host_*' \
	$ssh_args \
	--truncate /etc/machine-id

say "Compressing"
qemu-img convert -c -O qcow2 "$disk" "$out/soundstorm-box.qcow2"
rm -f "$disk"
ls -lh "$out/soundstorm-box.qcow2"
say "Done: box/out/soundstorm-box.qcow2 - boot it with box/run-vm.sh"
