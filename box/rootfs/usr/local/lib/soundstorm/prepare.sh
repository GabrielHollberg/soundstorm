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
	grep -v "^$1=" .env > .env.new || true
	printf '%s=%s\n' "$1" "$2" >> .env.new
	mv .env.new .env
}

[ -n "$(get_env SOUNDSTORM_SETUP_CODE)" ] ||
	set_env SOUNDSTORM_SETUP_CODE "$(od -An -N10 -tx1 /dev/urandom | tr -d ' \n')"
[ -n "$(get_env SOUNDSTORM_TLS)" ] || set_env SOUNDSTORM_TLS auto
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

# What a monitor plugged into the box shows at its login prompt: where to go,
# and the setup code until somebody has used it.
code=$(get_env SOUNDSTORM_SETUP_CODE)
port=$(get_env SOUNDSTORM_PORT)
mkdir -p /etc/issue.d
{
	echo "SoundStorm"
	[ -n "$lan" ] && echo "  Open http://$lan:${port:-8099} on a phone or computer on this network."
	echo "  Setup code: $code"
	echo
} > /etc/issue.d/soundstorm.issue
