#!/bin/sh
# Print sheets for the boxes, made for the Avery sheets the owner chose:
#
#   labels  Avery 64510 - 2" x 2" waterproof film, 12 a sheet (3 x 4): the
#           label under each box, its own setup code as a QR code
#           (http://soundstorm.local/?setup=CODE, which the app's scanner and
#           a phone's camera both take), the code, and the serial.
#   cards   Avery 35703 - 2.5" x 2.5" rounded cards, 9 a sheet (3 x 3): the
#           card in every box, the same for all: a QR code to
#           emberstorm.app/start, the instructions page.
#
# Each sheet is an HTML page sized to Letter with no margins: open it in
# Chrome or Edge, print at 100% (Default scale, margins None), through the
# LaserJet's Tray 1. --test draws each label's outline, for a plain-paper
# test held against a real sheet. The layouts were measured from Avery's own
# templates (U-0431-01 and U-1383-01).
#
# A printer that shrinks the page a little (each column further left than the
# last) is undone by measuring a test sheet with a ruler: MEASURED_X, inches
# from the paper's left edge to the right-hand column's left edge (5.875 on
# the labels, 5.75 on the cards, when right), and MEASURED_Y, inches from the
# paper's top to the bottom row's top (8.375 labels, 7.625 cards):
#   MEASURED_X=5.80 MEASURED_Y=8.30 sh box/sticker.sh labels units.csv
#
#   sh box/sticker.sh labels units.csv [OUTDIR] [--test]
#   sh box/sticker.sh label EM-XXXX-XXXX CODE [OUTDIR] [--test]
#   sh box/sticker.sh cards [OUTDIR] [--test]
#
# units.csv (unit,setup_code,made) is a factory stick's record of the boxes
# it set up. The codes are each box's own: keep it with the factory's
# private records, never in this repository.
set -eu

command -v qrencode >/dev/null || { echo "Needs qrencode (apt-get install qrencode)." >&2; exit 1; }

TEST=0
for a in "$@"; do [ "$a" = --test ] && TEST=1; done
args=""
for a in "$@"; do [ "$a" = --test ] || args="$args $a"; done
# shellcheck disable=SC2086
set -- $args

qr() { qrencode -t SVG --svg-path -m 0 -l M -o - "$1" | base64 -w0; }

# stretch EXPECTED MEASURED: how much to stretch so MEASURED lands at EXPECTED.
stretch() {
	[ -n "$2" ] || { echo 1; return; }
	awk -v e="$1" -v m="$2" 'BEGIN { if (m < e * 0.9 || m > e * 1.1) exit 1; printf "%.5f", e / m }' ||
		{ echo "A measurement of $2 is too far from $1 to be a printer's shrink: measure again." >&2; exit 2; }
}
page_open() {
	echo '<svg xmlns="http://www.w3.org/2000/svg" width="8.5in" height="11in" viewBox="0 0 8.5 11" font-family="Helvetica, Arial, sans-serif">'
	echo "<g transform=\"scale($KX $KY)\">"
}
page_close() { echo '</g></svg>'; }

html_open() {
	cat <<'EOF'
<!doctype html>
<meta charset="utf-8">
<title>EmberStorm print sheet</title>
<style>
  @page { size: letter; margin: 0; }
  html, body { margin: 0; padding: 0; background: #fff; }
  svg { display: block; page-break-after: always; break-after: page; }
</style>
EOF
}

# label X Y SERIAL CODE: one 2" label with its top left at X,Y inches.
label() {
	x=$1 y=$2 serial=$3 code=$4
	grouped=$(printf '%s' "$code" | tr a-f A-F | sed 's/..../& /g; s/ $//')
	line1=$(printf '%s' "$grouped" | cut -d' ' -f1-3)
	line2=$(printf '%s' "$grouped" | cut -d' ' -f4-)
	setup=$(qr "http://soundstorm.local/?setup=$code")
	echo "<g transform=\"translate($x $y)\">"
	[ "$TEST" = 1 ] && echo '<rect width="2" height="2" fill="none" stroke="#999" stroke-width="0.01"/>'
	cat <<EOF
<text x="0.14" y="0.27" font-size="0.15" font-weight="bold" font-style="italic">EmberStorm</text>
<text x="1.86" y="0.27" font-size="0.085" text-anchor="end" fill="#333">$serial</text>
<image x="0.475" y="0.36" width="1.05" height="1.05" href="data:image/svg+xml;base64,$setup"/>
<text x="1" y="1.6" font-size="0.15" font-weight="bold" text-anchor="middle" font-family="Menlo, Consolas, monospace">$line1</text>
<text x="1" y="1.76" font-size="0.15" font-weight="bold" text-anchor="middle" font-family="Menlo, Consolas, monospace">$line2</text>
<text x="1" y="1.89" font-size="0.075" text-anchor="middle" fill="#333">Setup code - on a computer, open soundstorm.local</text>
</g>
EOF
}

# card X Y: one 2.5" card with its top left at X,Y inches.
card() {
	x=$1 y=$2
	echo "<g transform=\"translate($x $y)\">"
	[ "$TEST" = 1 ] && echo '<rect width="2.5" height="2.5" rx="0.375" fill="none" stroke="#999" stroke-width="0.01"/>'
	cat <<EOF
<text x="1.25" y="0.4" font-size="0.17" font-weight="bold" text-anchor="middle">Welcome to <tspan font-style="italic">EmberStorm</tspan></text>
<image x="0.625" y="0.54" width="1.25" height="1.25" href="data:image/svg+xml;base64,$CARD_QR"/>
<text x="1.25" y="2.0" font-size="0.15" font-weight="bold" text-anchor="middle">Scan to get started</text>
<text x="1.25" y="2.16" font-size="0.09" text-anchor="middle" fill="#333">or visit emberstorm.app/start</text>
<text x="1.25" y="2.3" font-size="0.075" text-anchor="middle" fill="#333">Your setup code is on the bottom of the box.</text>
</g>
EOF
}

# Avery 64510: columns at 0.625, 3.25, 5.875; rows at 0.625, 3.2083,
# 5.7917, 8.375 (inches from the sheet's top left).
LCOLS="0.625 3.25 5.875"
LROWS="0.625 3.2083 5.7917 8.375"
# Avery 35703: columns at 0.25, 3.0, 5.75; rows at 0.875, 4.25, 7.625.
CCOLS="0.25 3.0 5.75"
CROWS="0.875 4.25 7.625"

# labels_from FILE: SERIAL,CODE lines, twelve to a sheet.
labels_from() {
	html_open
	n=0
	while IFS=, read -r serial code; do
		case "$serial" in EM-????-????) ;; *) echo "skipping $serial: not a serial" >&2; continue ;; esac
		case "$code" in *[!0-9a-fA-F]* | "") echo "skipping $serial: not a setup code" >&2; continue ;; esac
		slot=$((n % 12))
		[ "$slot" = 0 ] && page_open
		col=$((slot % 3 + 1))
		row=$((slot / 3 + 1))
		label "$(echo $LCOLS | cut -d' ' -f$col)" "$(echo $LROWS | cut -d' ' -f$row)" "$serial" "$code"
		n=$((n + 1))
		[ $((n % 12)) = 0 ] && page_close
	done <"$1"
	[ $((n % 12)) = 0 ] || page_close
	echo "$n" >&3
}

case "${1:-}" in
labels)
	KX=$(stretch 5.875 "${MEASURED_X:-}"); KY=$(stretch 8.375 "${MEASURED_Y:-}")
	[ -f "${2:-}" ] || { echo "usage: sticker.sh labels units.csv [OUTDIR] [--test]" >&2; exit 2; }
	out=${3:-stickers}
	mkdir -p "$out"
	tmp=$(mktemp)
	tr -d '\r' <"$2" | awk -F, 'NR > 1 || $1 != "unit" { print $1 "," $2 }' >"$tmp"
	count=$( { labels_from "$tmp" >"$out/labels.html"; } 3>&1 )
	rm -f "$tmp"
	echo "  $out/labels.html: $count labels, $(( (count + 11) / 12 )) sheet(s) of Avery 64510"
	;;
label)
	KX=$(stretch 5.875 "${MEASURED_X:-}"); KY=$(stretch 8.375 "${MEASURED_Y:-}")
	[ $# -ge 3 ] || { echo "usage: sticker.sh label SERIAL CODE [OUTDIR] [--test]" >&2; exit 2; }
	out=${4:-stickers}
	mkdir -p "$out"
	tmp=$(mktemp)
	printf '%s,%s\n' "$2" "$3" >"$tmp"
	{ labels_from "$tmp" >"$out/$2.html"; } 3>/dev/null
	rm -f "$tmp"
	echo "  $out/$2.html: one label, top left of a sheet of Avery 64510"
	;;
cards)
	KX=$(stretch 5.75 "${MEASURED_X:-}"); KY=$(stretch 7.625 "${MEASURED_Y:-}")
	out=${2:-stickers}
	mkdir -p "$out"
	CARD_QR=$(qr "https://emberstorm.app/start")
	{
		html_open
		page_open
		for y in $CROWS; do for x in $CCOLS; do card "$x" "$y"; done; done
		page_close
	} >"$out/cards.html"
	echo "  $out/cards.html: a sheet of 9 cards, Avery 35703"
	;;
*)
	sed -n '2,32p' "$0" | sed 's/^# \{0,1\}//' >&2
	exit 2
	;;
esac
