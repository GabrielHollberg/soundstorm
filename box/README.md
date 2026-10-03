# The SoundStorm box

What turns a small computer into a SoundStorm box: a system disk made from
Debian 13 with Docker, and the services that prepare the data drive and start
SoundStorm on it. See "Selling it on a box" in `CLAUDE.md` for why.

    box/build.sh        builds out/soundstorm-box.qcow2
    box/run-vm.sh       boots it as a pretend box (UEFI, eMMC + NVMe drive)
    box/release.sh      makes a signed release (manifest) for boxes to update to
    box/compose.box.yml the box's additions to docker-compose.yml
    box/rootfs/         files copied into the system disk

On the Windows PC both run in the Debian WSL distro, which has KVM (Docker
Desktop's own does not, and Hyper-V is off):

    wsl -d Debian -u root -- sh -c 'cd /mnt/h/dev/soundstorm && OUT=/root/box-out DEV_SSH=1 sh box/build.sh'
    wsl -d Debian -u root -- sh -c 'cd /mnt/h/dev/soundstorm && OUT=/root/box-out sh box/run-vm.sh'

`NO_IMAGES=1` makes a quick build without the images (about 12GB of them);
`OFFLINE=1` on `run-vm.sh` cuts the VM off the internet. `OUT` keeps the disks on WSL's own filesystem: the Windows drive is slow
through `/mnt`.

## How a box starts

1. `soundstorm-grow` grows the system partition to fill the eMMC.
2. `soundstorm-storage` mounts the data drive (label `SSDATA`) at
   `/srv/soundstorm`, formatting it as btrfs the first time - **only a blank
   disk is ever formatted**, and never a USB one. It holds three subvolumes:
   `library`, `volumes` (every container volume that holds data) and `cache`.
   With no data drive the box runs on the system disk and leaves
   `/run/soundstorm/no-data-drive`.
3. `soundstorm-images` loads the container images built into the disk
   (`/var/lib/soundstorm-images`, deleted once loaded), so a box starts with
   no downloads; `compose.images.yml` points each service at its built-in
   image, `soundstorm-box/<service>:built`.
4. `soundstorm` writes `/opt/soundstorm/.env` (setup code once; the box's
   address and router every start) and runs `docker compose up -d`.

## Updates (the caretaker)

`soundstorm-caretaker` (`cmd/`, `internal/caretaker`) runs on the box as
root, beside SoundStorm, never in it. A release is `manifest.json`: every
service's image pinned by digest, a serial, a version and notes, signed with
the release key (`.sig` beside it). The box takes only a manifest signed by
the key built into it (`box/release.pub`, or `RELEASE_KEY` when building)
and only one with a higher serial than it runs, so neither a forged nor an
old replayed one moves it. Releases are looked for at the GitHub release
`box-channel` (`RELEASES` when building points a development box elsewhere).

An update downloads every image first (nothing changes if that fails), stops
the stack, snapshots the `volumes` subvolume, writes `compose.images.yml`
and starts it; if SoundStorm does not answer healthy with as many sources as
before within ten minutes, the snapshot is put back in place of the volumes
and the old images file, and the box is told "went back to the version you
had". The newest snapshot is kept for going back by hand. It checks three
minutes after start and every six hours, and installs at night (2-5am)
unless the owner turned Auto off. SoundStorm will reach it through
`/run/soundstorm-caretaker/caretaker.sock` (`GET /status`, `POST /check`,
`POST /update`, `GET`/`PUT /settings`) - not yet mounted into it.

Making a release (needs the release key, which never goes on a box):

    sh box/release.sh -k KEYFILE -s SERIAL -v VERSION -n "what's new"

## Not built yet

Writing the disk to a real box (a USB installer); a unit's sticker and
pre-made setup code; Settings > Updates in the app (the socket mounted into
SoundStorm); the real release key and the first published release; the
caretaker updating itself; drive health, factory reset; backups.
