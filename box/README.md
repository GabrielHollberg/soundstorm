# The SoundStorm box

What turns a small computer into a SoundStorm box: a system disk made from
Debian 13 with Docker, and the services that prepare the data drive and start
SoundStorm on it. See "Selling it on a box" in `CLAUDE.md` for why.

    box/build.sh        builds out/soundstorm-box.qcow2
    box/run-vm.sh       boots it as a pretend box (UEFI, eMMC + NVMe drive)
    box/compose.box.yml the box's additions to docker-compose.yml
    box/rootfs/         files copied into the system disk

On the Windows PC both run in the Debian WSL distro, which has KVM (Docker
Desktop's own does not, and Hyper-V is off):

    wsl -d Debian -u root -- sh -c 'cd /mnt/h/dev/soundstorm && OUT=/root/box-out DEV_SSH=1 sh box/build.sh'
    wsl -d Debian -u root -- sh -c 'cd /mnt/h/dev/soundstorm && OUT=/root/box-out sh box/run-vm.sh'

`OUT` keeps the disks on WSL's own filesystem: the Windows drive is slow
through `/mnt`.

## How a box starts

1. `soundstorm-grow` grows the system partition to fill the eMMC.
2. `soundstorm-storage` mounts the data drive (label `SSDATA`) at
   `/srv/soundstorm`, formatting it as btrfs the first time - **only a blank
   disk is ever formatted**, and never a USB one. It holds three subvolumes:
   `library`, `volumes` (every container volume that holds data) and `cache`.
   With no data drive the box runs on the system disk and leaves
   `/run/soundstorm/no-data-drive`.
3. `soundstorm` writes `/opt/soundstorm/.env` (setup code once; the box's
   address and router every start) and runs `docker compose up -d`.

## Not built yet

The images are downloaded on first start rather than shipped in the disk;
writing the disk to a real box (a USB installer); a unit's sticker and
pre-made setup code; the caretaker (updates, drive health, factory reset);
backups; snapshots.
