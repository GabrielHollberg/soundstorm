#!/bin/sh
# Adds the boxes a factory stick set up to the ledger: one row per box, its
# serial and setup code and what identifies the hardware (box/installer.sh's
# units.csv). The ledger is the one record of every box made - what support
# looks a setup code up in by serial, and what a label is printed again
# from (box/sticker.sh reprint) - so it lives in the private repository,
# never this one, with a copy kept offline too.
#
#   sh box/units-add.sh /mnt/e/units.csv [LEDGER]
#
# (/mnt/e is the stick's EMBERSTORM drive seen from WSL: E: in Windows.)
# LEDGER defaults to units/units.csv in emberstorm-private beside this
# repository. A serial already there with the same code is passed over (a
# list added twice); one with a different code stops everything, as two
# boxes cannot share a serial: nothing is written then.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
from=${1:-}
ledger=${2:-$here/../../emberstorm-private/units/units.csv}
[ -f "$from" ] || { echo "usage: units-add.sh STICK/units.csv [LEDGER]" >&2; exit 2; }

header="unit,setup_code,made,stick,product,product_serial,mac,builtin_gb,storage"
umask 077
mkdir -p "$(dirname "$ledger")"
[ -f "$ledger" ] || echo "$header" >"$ledger"

tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
# Both files read with Windows line ends taken off; a row from an older
# stick (three columns) is padded to the ledger's.
if ! tr -d '\r' <"$from" | awk -F, -v OFS=, -v ledger="$ledger" -v added="$tmp" '
	BEGIN {
		while ((getline line < ledger) > 0) {
			sub(/\r$/, "", line)
			split(line, f, ",")
			if (f[1] ~ /^EM-/) have[f[1]] = f[2]
		}
	}
	$1 == "unit" || $1 == "" { next }
	$1 !~ /^EM-[0-9A-HJKMNP-TV-Z][0-9A-HJKMNP-TV-Z][0-9A-HJKMNP-TV-Z][0-9A-HJKMNP-TV-Z]-[0-9A-HJKMNP-TV-Z][0-9A-HJKMNP-TV-Z][0-9A-HJKMNP-TV-Z][0-9A-HJKMNP-TV-Z]$/ {
		print "skipping a line that is not a box: " $1 > "/dev/stderr"; next
	}
	$2 !~ /^[0-9a-f]+$/ { print "skipping " $1 ": no setup code" > "/dev/stderr"; next }
	$1 in have {
		if (have[$1] != $2) { print $1 " is already in the ledger with another setup code: nothing added." > "/dev/stderr"; bad = 1 }
		else same++
		next
	}
	{
		while (NF < 9) $(NF + 1) = ""
		print > added
		have[$1] = $2
		new++
	}
	END {
		if (bad) exit 1
		printf "%d new, %d already in the ledger\n", new, same
	}'; then
	exit 1
fi
cat "$tmp" >>"$ledger"
echo "  $ledger: $(grep -c '^EM-' "$ledger") boxes in all"
echo "  Commit it in emberstorm-private, and copy it to the offline backup."
