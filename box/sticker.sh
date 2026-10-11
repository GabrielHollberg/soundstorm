#!/bin/sh
# Print sheets for the boxes, made for the Avery sheets the owner chose:
#
#   labels  Avery 64510 - 2" x 2" waterproof film, 12 a sheet (3 x 4): the
#           label under each box, its own setup code as a QR code
#           (http://soundstorm.local/?setup=CODE, which the app's scanner and
#           a phone's camera both take), the code, and the serial.
#   cards   Avery 35703 - 2.5" x 2.5" rounded cards, 9 a sheet (3 x 3): the
#           card in every box, the same for all. Front: a QR code to
#           emberstorm.app/start, the instructions page, the cloud in its
#           middle. Back: the logo and three steps (cards-back.html, the
#           same sheet fed again).
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
# LEFT_MM=1 UP_MM=1 moves the whole print a millimetre left and up.
# START=5 begins at the fifth spot of a label sheet already partly used
# (spots count left to right, top to bottom).
#
#   sh box/sticker.sh labels units.csv [OUTDIR] [--test]
#   sh box/sticker.sh label EM-XXXX-XXXX CODE [OUTDIR] [--test]
#   sh box/sticker.sh reprint EM-XXXX-XXXX [LEDGER] [OUTDIR]   (from the ledger)
#   sh box/sticker.sh cards [OUTDIR] [--test]
#   sh box/sticker.sh logo [COUNT] [OUTDIR]   (icon labels, from spot START)
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

# qr TEXT [LEVEL]: the card uses H, so the cloud over its middle costs nothing.
qr() { qrencode -t SVG --svg-path -m 0 -l "${2:-M}" -o - "$1" | base64 -w0; }

# stretch EXPECTED MEASURED: how much to stretch so MEASURED lands at EXPECTED.
stretch() {
	[ -n "$2" ] || { echo 1; return; }
	awk -v e="$1" -v m="$2" 'BEGIN { if (m < e * 0.9 || m > e * 1.1) exit 1; printf "%.5f", e / m }' ||
		{ echo "A measurement of $2 is too far from $1 to be a printer's shrink: measure again." >&2; exit 2; }
}
# LEFT_MM and UP_MM move everything printed that many millimetres left and up
# (negative: right and down), for a printer that puts the page a little off;
# FRONT_LEFT_MM and FRONT_UP_MM move the cards' fronts only, on top of that.
page_open() {
	fl=0 fu=0
	[ "${side:-}" = front ] && fl=${FRONT_LEFT_MM:-0} fu=${FRONT_UP_MM:-0}
	dx=$(awk -v m="${LEFT_MM:-0}" -v f="$fl" 'BEGIN { printf "%.4f", -(m + f) / 25.4 }')
	dy=$(awk -v m="${UP_MM:-0}" -v f="$fu" 'BEGIN { printf "%.4f", -(m + f) / 25.4 }')
	echo '<svg xmlns="http://www.w3.org/2000/svg" width="8.5in" height="11in" viewBox="0 0 8.5 11" font-family="Helvetica, Arial, sans-serif">'
	echo "<g transform=\"translate($dx $dy) scale($KX $KY)\">"
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

# label X Y SERIAL CODE: one 2" label with its top left at X,Y inches, in
# the card's look: nothing but a big QR code to set the box up, the cloud in
# its middle (level H), and the setup code under it to type where nothing
# can scan (a laptop at soundstorm.local). White kept round the code: the
# box it goes on is black, and a code needs a margin to be found.
label() {
	x=$1 y=$2 serial=$3 code=$4
	grouped=$(printf '%s' "$code" | tr a-f A-F | sed 's/..../& /g; s/ $//')
	setup=$(qr "http://soundstorm.local/?setup=$code" H)
	echo "<g transform=\"translate($x $y)\">"
	[ "$TEST" = 1 ] && echo '<rect width="2" height="2" fill="none" stroke="#999" stroke-width="0.01"/>'
	cat <<EOT
<image x="0.24" y="0.11" width="1.52" height="1.52" href="data:image/svg+xml;base64,$setup"/>
<rect x="0.8005" y="0.6785" width="0.399" height="0.383" rx="0.07" fill="#fff"/>
EOT
	cloud 0.845 0.739 0.31 "#000" "l$serial"
	cat <<EOT
<text x="1" y="1.84" font-size="0.11" font-weight="bold" text-anchor="middle" font-family="Menlo, Consolas, monospace">$grouped</text>
</g>
EOT
}

# cloud X Y W FILL: the EmberStorm cloud and bolt, W inches wide.
cloud() {
	k=$(awk -v w="$3" 'BEGIN { printf "%.6f", w / 148 }')
	cat <<EOF
<g transform="translate($1 $2) scale($k) translate(-176.5 -177)" fill="$4">
<clipPath id="flat$5"><rect x="0" y="0" width="1000" height="249"/></clipPath>
<g clip-path="url(#flat$5)"><circle cx="234" cy="220.5" r="43.5"/><circle cx="283" cy="212.7" r="23.7"/><rect x="176.5" y="207" width="148" height="42" rx="21"/></g>
<polygon points="243,240 271,240 260,261 278,261 238,302 251,273 231,273"/>
</g>
EOF
}

# Both sides are black past the card's edge by 0.15" (about 4mm), its corners
# rounded with the card's: a card printed black only to its outline showed
# white edges wherever the die-cut and the print disagreed, and a centimetre
# all round was more toner than it needed. A QR code needs dark on light, so the
# front's sits on white.
PANEL='<rect x="-0.15" y="-0.15" width="2.8" height="2.8" rx="0.52" fill="#000"/>'

# card X Y N: the front of one 2.5" card with its top left at X,Y inches -
# nothing but the QR code to the instructions page, as large as the card's
# rounded corners allow (0.14" in: any less and the corner cuts into a finder
# square), the cloud in its middle. White: the card's own edge is the code's
# margin.
card() {
	x=$1 y=$2 i=$3
	echo "<g transform=\"translate($x $y)\">"
	[ "$TEST" = 1 ] && echo '<rect width="2.5" height="2.5" rx="0.375" fill="none" stroke="#999" stroke-width="0.01"/>'
	cat <<EOF
<image x="0.14" y="0.14" width="2.22" height="2.22" href="data:image/svg+xml;base64,$CARD_QR"/>
<rect x="0.959" y="0.97" width="0.582" height="0.56" rx="0.1" fill="#fff"/>
EOF
	cloud 1.023 1.058 0.454 "#000" "q$i"
	echo '</g>'
}

# card_back X Y N: the back - the cloud alone, 66% of the card wide and
# centred, as on the app's icon (make-icons.py, icon(1024, 0.66)). The steps
# are on the page the front's code opens (the owner's choice: the card is
# the brand, the page the instructions).
card_back() {
	x=$1 y=$2 i=$3
	echo "<g transform=\"translate($x $y)\">"
	[ "$TEST" = 1 ] && echo '<rect width="2.5" height="2.5" rx="0.375" fill="none" stroke="#999" stroke-width="0.01"/>'
	echo "$PANEL"
	cloud 0.425 0.553 1.65 "#fff" "b$i"
	echo '</g>'
}

# logo_label X Y N: a 2" label of the app's icon - black past the edge by
# 0.1", the white cloud 66% of the width and centred - for the box's top,
# the card's back in the label's size.
logo_label() {
	x=$1 y=$2 i=$3
	echo "<g transform=\"translate($x $y)\">"
	[ "$TEST" = 1 ] && echo '<rect width="2" height="2" fill="none" stroke="#999" stroke-width="0.01"/>'
	echo '<rect x="-0.1" y="-0.1" width="2.2" height="2.2" rx="0.2" fill="#000"/>'
	cloud 0.34 0.4425 1.32 "#fff" "g$i"
	echo '</g>'
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
	# START: the spot on the first sheet to begin at (1-12, left to right,
	# top to bottom), for a sheet already partly used.
	start=${START:-1}
	case "$start" in [1-9] | 1[0-2]) ;; *) echo "START is a spot from 1 to 12" >&2; exit 2 ;; esac
	n=$((start - 1))
	while IFS=, read -r serial code; do
		case "$serial" in EM-????-????) ;; *) echo "skipping $serial: not a serial" >&2; continue ;; esac
		case "$code" in *[!0-9a-fA-F]* | "") echo "skipping $serial: not a setup code" >&2; continue ;; esac
		slot=$((n % 12))
		{ [ "$slot" = 0 ] || [ "$n" = $((start - 1)) ]; } && page_open
		col=$((slot % 3 + 1))
		row=$((slot / 3 + 1))
		label "$(echo $LCOLS | cut -d' ' -f$col)" "$(echo $LROWS | cut -d' ' -f$row)" "$serial" "$code"
		n=$((n + 1))
		[ $((n % 12)) = 0 ] && page_close
	done <"$1"
	[ $((n % 12)) = 0 ] || page_close
	echo "$((n - start + 1))" >&3
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
reprint)
	# reprint SERIAL [LEDGER] [OUTDIR]: a box's label again, from the ledger.
	[ -n "${2:-}" ] || { echo "usage: sticker.sh reprint EM-XXXX-XXXX [LEDGER] [OUTDIR]" >&2; exit 2; }
	ledger=${3:-$(cd "$(dirname "$0")" && pwd)/../../emberstorm-private/units/units.csv}
	[ -f "$ledger" ] || { echo "No ledger at $ledger" >&2; exit 2; }
	code=$(tr -d '\r' <"$ledger" | awk -F, -v s="$2" '$1 == s { print $2; exit }')
	[ -n "$code" ] || { echo "$2 is not in the ledger." >&2; exit 1; }
	KX=$(stretch 5.875 "${MEASURED_X:-}"); KY=$(stretch 8.375 "${MEASURED_Y:-}")
	out=${4:-stickers}
	mkdir -p "$out"
	tmp=$(mktemp)
	printf '%s,%s\n' "$2" "$code" >"$tmp"
	{ labels_from "$tmp" >"$out/$2.html"; } 3>/dev/null
	rm -f "$tmp"
	echo "  $out/$2.html: $2's label, at spot ${START:-1} of a sheet of Avery 64510"
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
	echo "  $out/$2.html: one label, at spot ${START:-1} of a sheet of Avery 64510"
	;;
logo)
	# logo [COUNT] [OUTDIR]: COUNT icon labels from spot START (default 1).
	KX=$(stretch 5.875 "${MEASURED_X:-}"); KY=$(stretch 8.375 "${MEASURED_Y:-}")
	count=${2:-1}
	out=${3:-stickers}
	mkdir -p "$out"
	start=${START:-1}
	{
		html_open
		page_open
		n=0
		while [ $n -lt "$count" ]; do
			slot=$((start - 1 + n))
			[ $slot -lt 12 ] || break
			col=$((slot % 3 + 1)); row=$((slot / 3 + 1))
			logo_label "$(echo $LCOLS | cut -d' ' -f$col)" "$(echo $LROWS | cut -d' ' -f$row)" $n
			n=$((n + 1))
		done
		page_close
	} >"$out/logo-labels.html"
	echo "  $out/logo-labels.html: $n icon label(s) from spot $start of a sheet of Avery 64510"
	;;
cards)
	KX=$(stretch 5.75 "${MEASURED_X:-}"); KY=$(stretch 7.625 "${MEASURED_Y:-}")
	out=${2:-stickers}
	mkdir -p "$out"
	CARD_QR=$(qr "https://emberstorm.app/start" H)
	# The fronts, then the backs: the same sheet fed again the other way up.
	# The sheet is laid out evenly left and right, so each back lands behind
	# its own front however it is turned over side to side.
	for side in front back; do
		{
			html_open
			page_open
			n=0
			for y in $CROWS; do for x in $CCOLS; do
				n=$((n + 1))
				if [ $side = front ]; then card "$x" "$y" $n; else card_back "$x" "$y" $n; fi
			done; done
			page_close
		} >"$out/cards-$side.html"
	done
	echo "  $out/cards-front.html and cards-back.html: 9 cards, Avery 35703 - print the fronts, turn the sheet over side to side, print the backs"
	;;
*)
	sed -n '2,35p' "$0" | sed 's/^# \{0,1\}//' >&2
	exit 2
	;;
esac
