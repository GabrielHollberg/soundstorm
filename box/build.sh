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
#   NO_IMAGES=1      leave the container images out (a quick build; the box
#                    downloads them on first start instead)
#   SIZE=28G         the system disk's size; it grows to fill the real disk
#                    (the ME Mini's 64GB eMMC) on first boot
set -eu

here=$(cd "$(dirname "$0")" && pwd)
repo=$(dirname "$here")
out="${OUT:-$here/out}"
work="$out/work"
mkdir -p "$out" "$work"

SIZE=${SIZE:-28G}
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
for pkg in libguestfs-tools qemu-utils qemu-system-x86 ovmf curl skopeo linux-image-amd64; do
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
say "Container images"
# Every image the stack runs goes into the disk, so a box starts with no
# downloads - on a slow line the first start would otherwise be hours. Each is
# fetched by skopeo (no Docker needed here) into a docker-archive under a
# name of the box's own, soundstorm-box/<service>:built, and compose.images.yml
# points each service at it: a digest-pinned reference cannot survive
# docker save/load, a plain name can, and the box then runs exactly what it
# was built with. They are loaded on first boot (soundstorm-images.service).
# Tailscale, a profile nobody has switched on, is left out.
images="$stage/var/lib/soundstorm-images"
mkdir -p "$images"
overrides="$stage/opt/soundstorm/compose.images.yml"
echo "# Made by box/build.sh: each service runs the image built into this box." > "$overrides"
echo "services:" >> "$overrides"
awk '/^services:/{s=1;next} s&&/^[a-z]/{s=0}
	s&&/^  [a-z0-9-]+:$/{svc=$1; sub(":","",svc)}
	s&&/^    profiles:/{skip[svc]=1}
	s&&/^    image: /{img[svc]=$2; order[++n]=svc}
	END{for(i=1;i<=n;i++){v=order[i]; if(!skip[v]) print v, img[v]}}' "$repo/docker-compose.yml" |
	while read -r svc ref; do
		# ${VAR:-default} -> default
		ref=$(printf '%s' "$ref" | sed 's/^\${[A-Z_]*:-\(.*\)}$/\1/')
		# Two services on one image share one archive and one name.
		key=$(printf '%s' "$ref" | sha256sum | cut -c1-12)
		name="soundstorm-box/$svc:built"
		if [ -f "$images/$key.name" ]; then
			name=$(cat "$images/$key.name")
		else
			echo "  $ref"
			if [ "${NO_IMAGES:-}" != 1 ]; then
				skopeo copy -q --override-os linux --override-arch amd64 \
					"docker://$ref" "docker-archive:$images/$key.tar:$name"
			fi
			echo "$name" > "$images/$key.name"
		fi
		printf '  %s:\n    image: %s\n' "$svc" "$name" >> "$overrides"
	done
rm -f "$images"/*.name
if [ "${NO_IMAGES:-}" = 1 ]; then
	# A quick development build: the box pulls as before.
	rm -f "$overrides"
	rmdir "$images"
fi
du -sh "$images" 2>/dev/null || true

find "$stage" -type f ! -name '*.tar' -exec sed -i 's/\r$//' {} +
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
	--run-command 'systemctl enable docker soundstorm-grow soundstorm-storage soundstorm-images soundstorm ssh-hostkeys avahi-daemon' \
	--run-command 'rm -f /etc/ssh/ssh_host_*' \
	$ssh_args \
	--truncate /etc/machine-id

say "Compressing"
qemu-img convert -c -O qcow2 "$disk" "$out/soundstorm-box.qcow2"
rm -f "$disk"
ls -lh "$out/soundstorm-box.qcow2"
say "Done: box/out/soundstorm-box.qcow2 - boot it with box/run-vm.sh"
