#!/bin/sh
# Starts SoundStorm's containers.
set -eu
cd /opt/soundstorm
exec docker compose -f compose.yml -f compose.box.yml up -d --remove-orphans
