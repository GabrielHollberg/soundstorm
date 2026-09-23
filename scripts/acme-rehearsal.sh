#!/bin/sh
# Rehearse the whole certificate chain on this machine, with nobody's account:
# the name service registers an install and publishes its DNS challenge, and a
# real ACME authority validates it and issues.
#
#   Pebble                Let's Encrypt's own test authority, in place of
#                         Let's Encrypt
#   pebble-challtestsrv   its DNS server, in place of Porkbun
#
# Everything runs in containers on a throwaway network and is removed after.
# Needs Docker and nothing else.
#
#   sh scripts/acme-rehearsal.sh
#
# Pebble refuses a share of nonces on purpose and verifies every signature and
# challenge the way Let's Encrypt does, which is the point: a fake authority
# written alongside the client would share the client's mistakes.
set -eu

repo=$(cd "$(dirname "$0")/.." && pwd)
net=soundstorm-acme-rehearsal

cleanup() {
	docker rm -f soundstorm-pebble soundstorm-challtestsrv >/dev/null 2>&1 || true
	docker network rm "$net" >/dev/null 2>&1 || true
}
trap cleanup EXIT
cleanup
docker network create "$net" >/dev/null

# The DNS server answers every name nobody set with nothing, rather than its
# default of 127.0.0.1, so a record the test forgot cannot pass by accident.
docker run -d --name soundstorm-challtestsrv --network "$net" \
	ghcr.io/letsencrypt/pebble-challtestsrv:latest \
	-defaultIPv4 "" -defaultIPv6 "" >/dev/null

# PEBBLE_VA_NOSLEEP skips Pebble's random validation delay; nonce rejection is
# left on, so the client's retry is exercised.
docker run -d --name soundstorm-pebble --network "$net" \
	-e PEBBLE_VA_NOSLEEP=1 \
	ghcr.io/letsencrypt/pebble:latest \
	-config test/config/pebble-config.json -strict -dnsserver soundstorm-challtestsrv:8053 >/dev/null

# MSYS would otherwise rewrite /src into a Windows path under Git Bash.
MSYS_NO_PATHCONV=1 docker run --rm --network "$net" \
	-v "$repo:/src" -w /src \
	-e ACME_PEBBLE_DIRECTORY=https://soundstorm-pebble:14000/dir \
	-e ACME_PEBBLE_ROOT=https://soundstorm-pebble:15000/roots/0 \
	-e CHALLTESTSRV_URL=http://soundstorm-challtestsrv:8055 \
	-e CHALLTESTSRV_DNS=soundstorm-challtestsrv:8053 \
	golang:1.24-alpine \
	go test -count=1 -v -run Pebble ./internal/acme
