#!/bin/sh
# Starts SoundStorm's containers (or stops them, with "stop" - never down -v:
# the volumes are people's accounts and settings). The images built into the
# box are used when compose.images.yml is there; a box without it downloads.
set -eu
cd /opt/soundstorm
set -- "${1:-up}"
files="-f compose.yml -f compose.box.yml"
[ -f compose.images.yml ] && files="$files -f compose.images.yml"
case "$1" in
stop) exec docker compose $files stop ;;
*) exec docker compose $files up -d --remove-orphans ;;
esac
