#!/bin/sh
# Makes the sticker for each box a factory stick installed (its units.csv:
# serial and setup code): an SVG of 90 x 50 mm to print, with the serial,
# the setup code, a QR code to set the box up (the phone app's scanner and a
# phone's camera both take it: http://soundstorm.local/?setup=CODE), a QR
# code to get the app, and "On a computer, open soundstorm.local". The codes
# are each box's own; keep units.csv with the factory's records, never here.
#
#   sh box/sticker.sh units.csv [OUTDIR]       a sticker for every unit
#   sh box/sticker.sh EM-XXXX-XXXX CODE [OUTDIR]
set -eu

command -v qrencode >/dev/null || { echo "Needs qrencode (apt-get install qrencode)." >&2; exit 1; }

qr() { qrencode -t SVG -m 0 -o - "$1" | base64 -w0; }

# sticker SERIAL CODE OUTDIR
sticker() {
	serial=$1 code=$2 dir=$3
	case "$serial" in EM-????-????) ;; *) echo "skipping $serial: not a serial" >&2; return 0 ;; esac
	case "$code" in *[!0-9a-f]* | "") echo "skipping $serial: not a setup code" >&2; return 0 ;; esac
	grouped=$(printf '%s' "$code" | tr a-f A-F | sed 's/..../& /g; s/ $//')
	setup=$(qr "http://soundstorm.local/?setup=$code")
	app=$(qr "https://emberstorm.app/#getapp")
	cat >"$dir/$serial.svg" <<EOF
<svg xmlns="http://www.w3.org/2000/svg" width="90mm" height="50mm" viewBox="0 0 90 50" font-family="Helvetica, Arial, sans-serif">
  <rect width="90" height="50" fill="#fff"/>
  <text x="4" y="7" font-size="4.2" font-weight="bold" font-style="italic">EmberStorm</text>
  <text x="86" y="7" font-size="2.4" text-anchor="end" fill="#444">$serial</text>
  <image x="4" y="10" width="24" height="24" href="data:image/svg+xml;base64,$setup"/>
  <text x="16" y="37.5" font-size="2.3" text-anchor="middle">Scan to set up</text>
  <image x="62" y="10" width="24" height="24" href="data:image/svg+xml;base64,$app"/>
  <text x="74" y="37.5" font-size="2.3" text-anchor="middle">Get the app</text>
  <text x="45" y="17" font-size="2.6" text-anchor="middle">Setup code</text>
  <text x="45" y="22.5" font-size="3.3" font-weight="bold" text-anchor="middle" font-family="Menlo, Consolas, monospace">$(printf '%s' "$grouped" | cut -d' ' -f1-3)</text>
  <text x="45" y="27" font-size="3.3" font-weight="bold" text-anchor="middle" font-family="Menlo, Consolas, monospace">$(printf '%s' "$grouped" | cut -d' ' -f4-5)</text>
  <text x="45" y="44" font-size="2.6" text-anchor="middle">On a computer, open <tspan font-weight="bold">soundstorm.local</tspan></text>
</svg>
EOF
	echo "  $dir/$serial.svg"
}

if [ -f "${1:-}" ]; then
	out=${2:-stickers}
	mkdir -p "$out"
	tr -d '\r' <"$1" | while IFS=, read -r serial code _; do
		[ "$serial" = unit ] && continue
		sticker "$serial" "$code" "$out"
	done
else
	[ $# -ge 2 ] || { echo "usage: sticker.sh units.csv [OUTDIR] | sticker.sh SERIAL CODE [OUTDIR]" >&2; exit 2; }
	out=${3:-stickers}
	mkdir -p "$out"
	sticker "$1" "$2" "$out"
fi
