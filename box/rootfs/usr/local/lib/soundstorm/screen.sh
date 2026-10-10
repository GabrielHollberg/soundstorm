#!/bin/sh
# The box's own screen. A monitor plugged into the box shows, in big text,
# how to reach EmberStorm: before it has an owner, the address to open on
# any computer or tablet at home, the setup code and a QR code carrying
# both; after, its name and the address to use. Somebody with no
# smartphone can set the box up from any other screen in the house, and
# "what does the box's screen say?" is a question support can ask.
#
# Run by soundstorm-screen.service on tty1, in place of the login prompt,
# and redrawn when anything on it changes (checked every few seconds).
set -u
# A keyboard plugged into the box cannot stop or freeze the screen: Ctrl-C
# restarted it, and Ctrl-Z stopped it for good (the box's blind review).
trap '' INT QUIT TSTP
stty -isig 2>/dev/null || true

env=/opt/soundstorm/.env
port=8099
# The power button's code, from the caretaker's own folder (root only):
# "<code> <until>". Never asked of its socket, which the app's container
# shares.
buttoncode=/var/lib/soundstorm-caretaker/button-code
# An erase or start over asked for in the app, waiting for ten presses:
# "<until> <mode>".
erasewait=/var/lib/soundstorm-caretaker/erase-waiting

# Big letters, where the font is there (console-setup-linux), and no kernel
# messages written over the screen.
fonts=/usr/share/consolefonts
setfont $fonts/Lat15-TerminusBold32x16.psf.gz 2>/dev/null || true
# The welcome and its QR code are about 30 lines: a smaller monitor gets
# smaller letters rather than a QR code cut off at the foot.
rows=$(stty size 2>/dev/null | cut -d" " -f1)
if [ "${rows:-0}" -lt 32 ]; then
	setfont $fonts/Lat15-TerminusBold24x12.psf.gz 2>/dev/null || setfont $fonts/Lat15-Terminus24x12.psf.gz 2>/dev/null || true
fi
dmesg -n 1 2>/dev/null || true
setterm --cursor off 2>/dev/null || true

# A value from .env, or nothing.
setting() {
	sed -n "s/^$1=//p" "$env" 2>/dev/null | tail -n 1 | tr -d '\r'
}

# A string field from a JSON answer, or nothing. The answers are the
# server's own and flat; this is not a JSON reader and need not be.
field() {
	printf '%s' "$1" | tr -d '\n' | sed -n "s/.*\"$2\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p" | LC_ALL=C tr -cd '[:print:]' | cut -c1-80
}

# The box's address on the home network: the one its route out leaves
# from, as prepare.sh takes it - never one of Docker's own networks (the
# box's fixed 10.231.x ones were shown, with their QR code, when the cable
# was out: the blind reviews, 2026-10-10). Nothing, with no network.
address() {
	ip -4 route get 1.1.1.1 2>/dev/null | sed -n 's/.* src \([0-9.]*\).*/\1/p' | head -n 1
}

# The name the box is really announced by: avahi gives a second box on one
# network "soundstorm-2.local", and the hostname alone sent people to the
# first box (the blind reviews).
announced() {
	n=$(busctl call org.freedesktop.Avahi / org.freedesktop.Avahi.Server GetHostNameFqdn 2>/dev/null | sed -n 's/^s "\(.*\)"$/\1/p')
	echo "${n:-$(hostname).local}"
}

draw() {
	health=$(curl -fsS -m 3 "http://127.0.0.1:$port/healthz" 2>/dev/null || true)
	ip=$(address)
	local_name=$(announced)
	printf '\033[2J\033[H\n'
	# The storage drive missing or failing: EmberStorm is not started at all
	# (storage.sh), and this is the only place that can say so.
	if [ -s /run/soundstorm/storage-problem ]; then
		printf '   EmberStorm cannot start\n\n'
		fold -s -w 60 /run/soundstorm/storage-problem | tr -d '\033' | sed 's/^/   /'
		printf '\n   To switch the box off, press the power button twice.\n'
		return
	fi
	if [ -e /run/soundstorm/no-data-drive ]; then
		printf '   Note: no storage drive was found, so everything is kept on\n'
		printf '   the small built-in one. Contact support.\n\n'
	fi
	if [ -z "$health" ]; then
		printf '   EmberStorm is starting...\n\n'
		printf '   The first start takes a few minutes. This screen\n'
		printf '   changes by itself when it is ready.\n'
		[ -n "$ip" ] || printf '\n   No network yet: is the cable plugged into your router?\n'
		return
	fi
	case "$health" in
	*'"setUp": false'* | *'"setUp":false'*)
		code=$(setting SOUNDSTORM_SETUP_CODE)
		printf '   Welcome to your EmberStorm\n\n'
		printf '   On a computer or tablet at home, open:\n\n'
		printf '      http://%s\n' "$local_name"
		[ -n "$ip" ] && printf '   or http://%s\n' "$ip"
		printf '\n   Setup code:  %s\n\n' "$code"
		printf '   Or scan this with a phone, or get the EmberStorm app.\n'
		if [ -n "$code" ] && command -v qrencode >/dev/null; then
			qrencode -t UTF8 -m 2 "http://${ip:-$local_name}/?setup=$code" 2>/dev/null | sed 's/^/   /'
		fi
		;;
	*)
		name=$(field "$health" name)
		session=$(curl -fsS -m 3 "http://127.0.0.1:$port/api/session" 2>/dev/null || true)
		secure=$(field "$session" secureName)
		printf '   %s\n\n' "${name:-EmberStorm}"
		printf '   Open it on any phone, tablet, computer or TV at home:\n\n'
		if [ -n "$secure" ]; then
			printf '      https://%s:%s\n' "$secure" "$port"
			printf '   or http://%s\n' "$local_name"
		else
			printf '      http://%s\n' "$local_name"
			[ -n "$ip" ] && printf '   or http://%s\n' "$ip"
		fi
		# The power button pressed five times: the code a new password
		# needs, shown here only - somebody at the box is who may set it.
		bcode=
		if [ -r "$buttoncode" ]; then
			read -r c until <"$buttoncode" || true
			case "$c" in [0-9][0-9][0-9][0-9][0-9][0-9]) ;; *) c= ;; esac
			[ -n "$c" ] && [ "${until:-0}" -gt "$(date +%s)" ] 2>/dev/null && bcode=$c
		fi
		ewait=
		if [ -r "$erasewait" ]; then
			read -r eu emode <"$erasewait" || true
			[ "${eu:-0}" -gt "$(date +%s)" ] 2>/dev/null && ewait=1
		fi
		if [ -n "$ewait" ]; then
			if [ "${emode:-}" = erase ]; then
				printf '\n   ERASING THIS BOX WAS ASKED FOR IN THE APP.\n'
				printf '   Pressing the power button ten times now erases\n'
				printf '   everything on it, media included.\n'
			else
				printf '\n   STARTING OVER WAS ASKED FOR IN THE APP.\n'
				printf '   Pressing the power button ten times now deletes every\n'
				printf '   account and setting. The media stays.\n'
			fi
			printf '   To keep it as it is, do not press the button:\n'
			printf '   it is cancelled by itself within ten minutes.\n'
		elif [ -n "$bcode" ]; then
			printf '\n   The power button was pressed. To choose a new password,\n'
			printf '   open the sign-in screen and type this code:\n\n'
			printf '      %s %s\n\n' "$(printf %s "$bcode" | cut -c1-3)" "$(printf %s "$bcode" | cut -c4-6)"
			printf '   It works once, for fifteen minutes.\n'
		else
			printf '\n   Forgot the password? Press the power button on the box\n'
			printf '   five times quickly, then choose a new one on the sign-in screen.\n'
			printf '   To switch the box off, press the power button twice.\n'
		fi
		;;
	esac
}

last=
while :; do
	# Drawn into a variable first, so the screen only flickers when what it
	# says has changed.
	now=$(draw)
	if [ "$now" != "$last" ]; then
		printf '%s\n' "$now"
		last=$now
	fi
	# Every few seconds, so a button code shows soon after the presses.
	sleep 3
done
