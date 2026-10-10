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
#   RELEASE_KEY=F    the release key's public half that updates must be
#                    signed with (default box/release.pub)
#   RELEASES=URL     where this box looks for updates (a development box's
#                    own test channel; the default is the published one)
#   SIZE=28G         the system disk's size; it grows to fill the real disk
#                    (the ME Mini's 64GB eMMC) on first boot
#   SERIAL=N         the release the box's images are (box/release.sh's
#                    serial): the box never installs one not newer
#   PRODUCTION=1     a box to sell: refuses DEV_SSH, another release channel,
#                    a release key other than box/release.pub, and no SERIAL
set -eu

# A box that is sold must not carry a development build's ways in or out
# (the twelfth security pass): SSH for root, a test channel, a test key, or
# no floor under the releases it takes.
if [ "${PRODUCTION:-}" = 1 ]; then
	[ "${DEV_SSH:-}" != 1 ] || { echo "PRODUCTION: DEV_SSH is for development boxes." >&2; exit 1; }
	[ -z "${RELEASES:-}" ] || { echo "PRODUCTION: RELEASES points at a test channel." >&2; exit 1; }
	[ -z "${RELEASE_KEY:-}" ] || { echo "PRODUCTION: only box/release.pub signs a sold box's updates." >&2; exit 1; }
	[ -n "${SERIAL:-}" ] || { echo "PRODUCTION: SERIAL, the release these images are, is needed." >&2; exit 1; }
	# The release keys a sold box believes are the ones committed in the
	# project, never a file that happens to sit there (a development key
	# made into that path would pass): box/release.pub tracked and unchanged.
	pub_repo=$(cd "$(dirname "$0")/.." && pwd)
	if ! git -c safe.directory='*' -C "$pub_repo" ls-files --error-unmatch box/release.pub >/dev/null 2>&1 ||
		! git -c safe.directory='*' -C "$pub_repo" diff --quiet HEAD -- box/release.pub; then
		echo "PRODUCTION: box/release.pub must be the release keys committed in the project." >&2
		exit 1
	fi
fi
case "${SERIAL:-0}" in *[!0-9]*) echo "SERIAL is a number." >&2; exit 1 ;; esac

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
for pkg in libguestfs-tools qemu-utils qemu-system-x86 ovmf curl skopeo golang-go git linux-image-amd64; do
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
say "The caretaker"
# Debian's Go fetches the toolchain go.mod names (GOTOOLCHAIN=auto, checked
# against Go's checksum database).
mkdir -p "$stage/usr/local/bin" "$stage/etc/soundstorm"
(cd "$repo" && CGO_ENABLED=0 GOTOOLCHAIN=auto GOFLAGS=-trimpath \
	go build -ldflags=-s -o "$stage/usr/local/bin/soundstorm-caretaker" ./cmd/soundstorm-caretaker)
key=${RELEASE_KEY:-$here/release.pub}
[ -f "$key" ] || { echo "No release key at $key (soundstorm-caretaker keygen makes one)." >&2; exit 1; }
cp "$key" "$stage/etc/soundstorm/release.pub"
{
	[ -z "${RELEASES:-}" ] || echo "SOUNDSTORM_RELEASES=$RELEASES"
	[ -z "${SERIAL:-}" ] || echo "SOUNDSTORM_MIN_SERIAL=$SERIAL"
} > "$stage/etc/soundstorm/caretaker.env"

say "EmberStorm's own image"
# The one GitHub built from a commit on main (box/app-image.sh), never
# ":latest"; a production unit only with its provenance checked.
# shellcheck source=box/app-image.sh
. "$here/app-image.sh"
if [ "${PRODUCTION:-}" = 1 ]; then
	app_image strict
elif ! app_image; then
	# A development box may be built before its commit is pushed and built.
	APP_IMAGE=$APP_REPO:latest
	echo "  development build: using $APP_IMAGE"
fi

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
		[ "$svc" = soundstorm ] && ref=$APP_IMAGE
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

say "Models"
# The models the backends download on first use, so a box set up with no
# internet has photo search, Make an ebook and read-along syncing from its
# first start. box/models.sh makes them from a working install (this build
# runs no Docker); storage.sh lays each into its empty cache folder on the
# data drive at first boot, and they stay here for Start over.
if ls "$out/models/"*.tar >/dev/null 2>&1; then
	# Every file checked against box/models.sha256, the list last reviewed
	# and committed: models taken from a machine's running backends are
	# whatever those backends wrote, a program (whisper.cpp) among them.
	check="$work/models-check"
	rm -rf "$check"
	for tar in "$out/models/"*.tar; do
		n=$(basename "$tar" .tar)
		mkdir -p "$check/$n"
		tar -xf "$tar" -C "$check/$n"
		sh "$here/models-list.sh" -c "$check/$n" "$n" >>"$check/list"
	done
	if [ -f "$here/models.sha256" ]; then
		if ! tr -d '\r' <"$here/models.sha256" | cmp -s - "$check/list"; then
			echo "The models differ from box/models.sha256, the list last reviewed:" >&2
			tr -d '\r' <"$here/models.sha256" | diff - "$check/list" | head -n 20 >&2
			echo "If the change is expected (box/models.sh was run again), review it with" >&2
			echo "git diff box/models.sha256 and commit the new list; then build again." >&2
			exit 1
		fi
		echo "  every model file matches box/models.sha256"
	elif [ "${PRODUCTION:-}" = 1 ]; then
		echo "PRODUCTION: no box/models.sha256 to check the models against." >&2
		exit 1
	else
		echo "  development build: no box/models.sha256, models not checked"
	fi
	rm -rf "$check"
	mkdir -p "$stage/var/lib/soundstorm-models"
	cp "$out/models/"*.tar "$stage/var/lib/soundstorm-models/"
	du -sh "$stage/var/lib/soundstorm-models"
else
	echo "  none in $out/models - run box/models.sh first, or the box downloads them on first use"
fi

find "$stage" -type f ! -name '*.tar' ! -name soundstorm-caretaker -exec sed -i 's/\r$//' {} +
# Each top folder is copied in whole; copying merges into what is there.
copy_args=""
for top in "$stage"/*; do
	copy_args="$copy_args --copy-in $top:/"
done

# A box sold has no way in but the app: no SSH, root locked, no login prompt
# on the screen's other consoles or a serial port. DEV_SSH, for development,
# adds SSH with this machine's key.
ssh_args=""
ssh_pkg=""
ssh_units=""
if [ "${DEV_SSH:-}" = 1 ]; then
	ssh_pkg=",openssh-server"
	ssh_units=" ssh-hostkeys"
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
	--run-command "sed -i 's/^Components: main\$/Components: main non-free-firmware/' /etc/apt/sources.list.d/*.sources" \
	--install intel-microcode,firmware-intel-graphics,firmware-misc-nonfree,firmware-realtek \
	--install docker.io,docker-compose,btrfs-progs,cloud-guest-utils,avahi-daemon,curl,qrencode,kbd,console-setup-linux,ntfs-3g,systemd-zram-generator$ssh_pkg \
	--run-command 'growpart /dev/sda 1 && resize2fs /dev/sda1' \
	$copy_args \
	--run-command 'chmod 755 /usr/local/lib/soundstorm/*.sh /usr/local/bin/soundstorm-caretaker' \
	--run-command 'chmod 644 /etc/systemd/system/soundstorm*.service /etc/systemd/system/ssh-hostkeys.service /etc/udev/rules.d/90-soundstorm-usb.rules /etc/systemd/logind.conf.d/soundstorm-button.conf /etc/systemd/logind.conf.d/soundstorm-consoles.conf /etc/tmpfiles.d/soundstorm-drives.conf /etc/docker/daemon.json /etc/systemd/system/soundstorm-address.timer /etc/systemd/system/soundstorm-scrub.timer /etc/systemd/network/05-emberstorm-no-usb-network.network /etc/systemd/journald.conf.d/emberstorm.conf /etc/systemd/zram-generator.conf /etc/systemd/system/*.d/soundstorm-*.conf /etc/apt/apt.conf.d/52soundstorm-upgrades' \
	--run-command 'docker compose version' \
	--run-command "systemctl enable docker soundstorm-grow soundstorm-storage soundstorm-images soundstorm soundstorm-caretaker soundstorm-screen soundstorm-address.timer soundstorm-scrub.timer$ssh_units avahi-daemon" \
	--run-command 'systemctl mask ctrl-alt-del.target' \
	--run-command '/usr/local/lib/soundstorm/lock-grub.sh && rm /usr/local/lib/soundstorm/lock-grub.sh' \
	--root-password disabled \
	--run-command 'ln -sf /dev/null /etc/systemd/system/serial-getty@.service' \
	--run-command 'rm -f /etc/ssh/ssh_host_*' \
	$ssh_args \
	--delete /var/lib/systemd/random-seed \
	--truncate /etc/machine-id

say "Compressing"
qemu-img convert -c -O qcow2 "$disk" "$out/soundstorm-box.qcow2"
rm -f "$disk"
ls -lh "$out/soundstorm-box.qcow2"
say "Done: box/out/soundstorm-box.qcow2 - boot it with box/run-vm.sh"
