#!/bin/sh
# Writes the box's settings before SoundStorm starts, every time it starts.
#
# The setup code is made once (a unit made at the factory already has one, to
# match its sticker); the box's address and its router's are written fresh
# each start, because a box only ever has the address it has now - unlike an
# install on a laptop, there is no address somebody chose to keep.
set -eu

cd /opt/soundstorm
umask 077
touch .env
chmod 600 .env

get_env() { sed -n "s/^$1=//p" .env | tail -1; }
set_env() {
	case "$2" in *"
"*) echo "prepare: refusing a value with a line break for $1" >&2; exit 1 ;; esac
	# grep says 1 when it keeps no line (the setting was the only one), 2 when
	# it could not read: never the second, or .env would be written with this
	# one setting alone and the box's secrets lost.
	rc=0
	grep -v "^$1=" .env > .env.new || rc=$?
	[ "$rc" -le 1 ] || { echo "prepare: could not read .env" >&2; rm -f .env.new; exit 1; }
	printf '%s=%s\n' "$1" "$2" >> .env.new
	mv .env.new .env
}

[ -n "$(get_env SOUNDSTORM_SETUP_CODE)" ] ||
	set_env SOUNDSTORM_SETUP_CODE "$(od -An -N10 -tx1 /dev/urandom | tr -d ' \n')"
[ -n "$(get_env SOUNDSTORM_TLS)" ] || set_env SOUNDSTORM_TLS auto

# The backends' own secrets, made for this box - not the compose file's
# defaults every box would share (the box's blind review). Only for a
# database not made yet: one already made keeps the password it was made with.
secret() { od -An -N16 -tx1 /dev/urandom | tr -d ' \n'; }
volume_empty() { [ -z "$(ls -A "/srv/soundstorm/volumes/$1" 2>/dev/null)" ]; }
own_secret() {
	[ -n "$(get_env "$1")" ] && return 0
	if volume_empty "$2"; then set_env "$1" "$(secret)"; else set_env "$1" "$3"; fi
}
own_secret SOUNDSTORM_IMMICH_DB_PASSWORD immich-db soundstorm-immich
own_secret SOUNDSTORM_AUDIOMUSE_DB_PASSWORD audiomuse-db soundstorm-audiomuse
own_secret SOUNDSTORM_STORYTELLER_SECRET storyteller-data soundstorm-storyteller
set_env SOUNDSTORM_LIBRARY_PATH /srv/soundstorm/library
set_env SOUNDSTORM_LIBRARY_HINT "the box's drive"

# The address the box is reached at, and its router, from the default route.
route=$(ip -4 route get 1.1.1.1 2>/dev/null || true)
lan=$(printf '%s\n' "$route" | sed -n 's/.* src \([0-9.]*\).*/\1/p' | head -1)
gw=$(printf '%s\n' "$route" | sed -n 's/.* via \([0-9.]*\).*/\1/p' | head -1)
[ -n "$lan" ] && set_env SOUNDSTORM_TLS_HOSTS "$lan"
[ -n "$gw" ] && set_env SOUNDSTORM_GATEWAY "$gw"

# Compose bind-mounts this whether or not Tailscale is used; a missing source
# would become a directory.
[ -f tailscale-serve.json ] || cat > tailscale-serve.json <<'EOF'
{
  "TCP": { "443": { "HTTPS": true } },
  "Web": {
    "${TS_CERT_DOMAIN}:443": {
      "Handlers": { "/": { "Proxy": "http://soundstorm-app:8080" } }
    }
  }
}
EOF
chmod 644 tailscale-serve.json

# What a monitor plugged into the box shows at its login prompt: where to go.
# Never the setup code - it approves new devices after setup too, and the
# box's own screen (screen.sh) shows it while it is still needed.
port=$(get_env SOUNDSTORM_PORT)
mkdir -p /etc/issue.d
{
	echo "SoundStorm"
	[ -n "$lan" ] && echo "  Open http://$lan:${port:-8099} on a phone or computer on this network."
	echo
} > /etc/issue.d/soundstorm.issue
