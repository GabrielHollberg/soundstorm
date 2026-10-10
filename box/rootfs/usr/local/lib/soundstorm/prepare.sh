#!/bin/sh
# Writes the box's settings before SoundStorm starts, every time it starts.
#
# The box's identity - its setup code and serial (on its sticker) and the
# backends' own secrets - is kept on the storage drive (box.env, root's
# alone), made once: by the factory stick (identity.env on the system disk),
# else here. The system disk's .env is written from it at every start, so a
# repair from the USB stick, which writes the system disk afresh and keeps
# the storage drive, keeps the sticker's code and the databases' passwords
# (the box's blind review: it lost both). The box's address and its router's
# are written fresh each start, because a box only ever has the address it
# has now - unlike an install on a laptop, there is no address somebody
# chose to keep.
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

ID_DIR=/srv/soundstorm/identity
ID="$ID_DIR/box.env"
FACTORY=/etc/soundstorm/identity.env
mkdir -p "$ID_DIR"
chmod 700 "$ID_DIR"
touch "$ID"
chmod 600 "$ID"
id_get() { sed -n "s/^$1=//p" "$ID" | tail -1; }
id_set() {
	case "$2" in *"
"*) echo "prepare: refusing a value with a line break for $1" >&2; exit 1 ;; esac
	rc=0
	grep -v "^$1=" "$ID" > "$ID.new" || rc=$?
	[ "$rc" -le 1 ] || { echo "prepare: could not read $ID" >&2; rm -f "$ID.new"; exit 1; }
	printf '%s=%s\n' "$1" "$2" >> "$ID.new"
	mv "$ID.new" "$ID"
}
factory_get() { [ -r "$FACTORY" ] && sed -n "s/^$1=//p" "$FACTORY" | tail -1; true; }
# keep NAME MAKE: the identity's value, else the factory's, else what .env
# had (a box from before), else MAKE's - kept on the drive, written to .env.
keep() {
	v=$(id_get "$1")
	[ -n "$v" ] || v=$(factory_get "$1")
	[ -n "$v" ] || v=$(get_env "$1")
	[ -n "$v" ] || v=$($2)
	[ "$(id_get "$1")" = "$v" ] || id_set "$1" "$v"
	[ "$(get_env "$1")" = "$v" ] || set_env "$1" "$v"
}
new_code() { od -An -N10 -tx1 /dev/urandom | tr -d ' \n'; }
# EM- and eight letters and digits nobody misreads (no I, L, O or U).
new_unit() {
	od -An -N8 -tu1 /dev/urandom | awk '{ for (i = 1; i <= NF; i++) s = s substr("0123456789ABCDEFGHJKMNPQRSTVWXYZ", $i % 32 + 1, 1) }
		END { printf "EM-%s-%s", substr(s, 1, 4), substr(s, 5, 4) }'
}
keep SOUNDSTORM_SETUP_CODE new_code
keep SOUNDSTORM_UNIT new_unit
[ -n "$(get_env SOUNDSTORM_TLS)" ] || set_env SOUNDSTORM_TLS auto

# The backends' own secrets, made for this box - not the compose file's
# defaults every box would share (the box's blind review). Only for a
# database not made yet: one already made keeps the password it was made
# with (the compose default, for a box set up before this).
secret() { od -An -N16 -tx1 /dev/urandom | tr -d ' \n'; }
volume_empty() { [ -z "$(ls -A "/srv/soundstorm/volumes/$1" 2>/dev/null)" ]; }
own_secret() {
	if [ -z "$(id_get "$1")" ] && [ -z "$(get_env "$1")" ] && ! volume_empty "$2"; then
		set_env "$1" "$3"
	fi
	keep "$1" secret
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
