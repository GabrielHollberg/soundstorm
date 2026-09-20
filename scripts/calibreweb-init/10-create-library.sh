#!/usr/bin/with-contenv bash
# Create an empty Calibre library if /books does not already contain one.
#
# Calibre-Web cannot be configured without a metadata.db to point at, so on a
# fresh install with no ebooks yet there is nothing for SoundStorm's provisioner to
# do and setup would fail. calibredb creates a valid empty library from nothing,
# which turns that dead end into a working (if empty) ebook backend.
#
# Someone who already has a Calibre library just mounts it at /books instead and
# this does nothing.
set -e

if [ -f /books/metadata.db ]; then
    echo "[soundstorm-init] /books already holds a calibre library"
    exit 0
fi

if ! command -v calibredb >/dev/null 2>&1; then
    echo "[soundstorm-init] calibredb not found; is DOCKER_MODS=linuxserver/mods:universal-calibre set?"
    exit 0
fi

echo "[soundstorm-init] creating an empty calibre library in /books"
mkdir -p /books
calibredb list --with-library /books >/dev/null 2>&1 || true
chown -R "${PUID:-1000}:${PGID:-1000}" /books
echo "[soundstorm-init] done"
