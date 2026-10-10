#!/bin/sh
# EmberStorm installer for macOS and Linux.
#
#   curl -fsSL https://raw.githubusercontent.com/GabrielHollberg/emberstorm/main/install.sh | sh
#
# It downloads one compose file, picks a free port, starts the stack and waits
# until it answers. Everything it needs is Docker; everything it leaves behind
# is a folder you can delete.
#
#   (no arguments)   install, or update an existing install
#   --https          real https for a soundstorm.dev name (the default already)
#   --no-https       plain http only
#   --tailscale      also reach it away from home, over a tailnet
#   --no-tailscale   stop doing that
#   --uninstall      remove it, keeping the media library
#   --export PATH    pack it up to move to another computer
#   --import PATH    install it from a move made with --export
#   --library PATH   keep the media library somewhere else - an external drive
#
# Written for /bin/sh rather than bash, because a stock Debian's /bin/sh is dash
# and an installer that only works under bash is an installer that fails on the
# exact cheap home server this is aimed at.

set -eu

REPO="${SOUNDSTORM_REPO:-GabrielHollberg/emberstorm}"
BRANCH="${SOUNDSTORM_BRANCH:-main}"
COMPOSE_URL="${SOUNDSTORM_COMPOSE_URL:-https://raw.githubusercontent.com/$REPO/$BRANCH/docker-compose.yml}"
DIR="${SOUNDSTORM_DIR:-$PWD/soundstorm}"
# The compose project, whose name prefixes every data volume. Always
# soundstorm (docker-compose.yml says so); overridable only so a move can be
# rehearsed on a throwaway project without touching a real install's data.
PROJECT="${SOUNDSTORM_PROJECT:-soundstorm}"
FIRST_PORT="${SOUNDSTORM_PORT:-8099}"

# --- saying things ----------------------------------------------------------

# Color only when stdout is a terminal. Piping this into a log should not
# produce escape codes, and `curl | sh` is a very normal way to run it.
if [ -t 1 ]; then
	BOLD=$(printf '\033[1m'); DIM=$(printf '\033[2m')
	RED=$(printf '\033[31m'); GREEN=$(printf '\033[32m'); YELLOW=$(printf '\033[33m')
	CYAN=$(printf '\033[36m'); OFF=$(printf '\033[0m')
else
	BOLD=''; DIM=''; RED=''; GREEN=''; YELLOW=''; CYAN=''; OFF=''
fi

# Notes are plain, not dimmed: a dim line is easy to skim past, and most of
# what this says is something the person needs to read. DIM is kept for the
# reference lines at the very end that nobody needs on a first read.
say()  { printf '%s\n' "$*"; }
step() { printf '\n%s%s==>%s %s%s\n' "$CYAN" "$BOLD" "$OFF$BOLD" "$*" "$OFF"; }
note() { printf '    %s\n' "$*"; }
important() { printf '    %s%s%s\n' "$YELLOW" "$*" "$OFF"; }

# frame draws a box around the one thing somebody has to act on, so it cannot
# be lost among the lines above it. Arguments are lines; one starting with "*"
# is the thing itself (a code, an address) and is drawn in yellow.
frame() {
	_frame_title=$1
	shift
	_frame_dashes=$(printf '%s' "--------------------------------------------------------------------" |
		cut -c1-$((62 - ${#_frame_title})))
	printf '\n  %s+--- %s %s%s\n' "$YELLOW$BOLD" "$_frame_title" "$_frame_dashes" "$OFF"
	for _frame_line in "$@"; do
		case "$_frame_line" in
			\**) printf '  %s|%s  %s%s%s\n' "$YELLOW$BOLD" "$OFF" "$YELLOW$BOLD" "${_frame_line#?}" "$OFF" ;;
			*)   printf '  %s|%s  %s\n' "$YELLOW$BOLD" "$OFF" "$_frame_line" ;;
		esac
	done
	printf '  %s+%s%s\n\n' "$YELLOW$BOLD" "---------------------------------------------------------------------" "$OFF"
}

# format_code groups the setup code in fours so it can be read and typed. The
# server ignores case, spaces and dashes, so this is only presentation.
format_code() {
	printf '%s' "$1" | tr 'a-f' 'A-F' | sed 's/\(....\)/\1-/g; s/-$//'
}

# die prints why it stopped and, more importantly, what to do about it. An
# installer that says "error: 1" has failed twice.
die() {
	printf '\n%s%s%s\n\n%s\n\n' "$RED$BOLD" "${DIE_HEADING:-EmberStorm could not start.}" "$OFF" "$1" >&2
	exit 1
}

# --- the things that have to be true ----------------------------------------

# ask_yes asks a yes-or-no question on the terminal - the script may arrive
# through a pipe, so it reads the terminal itself - and answers with $2 (yes
# or no) when there is nobody to ask.
ask_yes() {
	answer=''
	if [ -t 0 ]; then
		printf '  %s ' "$1"
		read -r answer || answer=''
	elif [ -r /dev/tty ] && ( : </dev/tty ) 2>/dev/null; then
		printf '  %s ' "$1"
		read -r answer </dev/tty || answer=''
	fi
	case "$answer" in
	[Yy]*) return 0 ;;
	[Nn]*) return 1 ;;
	esac
	[ "$2" = "yes" ]
}

# keep_awake offers to keep the computer from sleeping while plugged in:
# asleep, nothing reaches EmberStorm (2026-10-09, as the Windows setup does).
# A Mac is also told to start up again after a power cut, which it can do by
# itself. On Linux only a laptop or a desktop with a sleeping session is asked;
# a plain server does not sleep.
keep_awake() {
	case "$(uname -s)" in
	Darwin)
		sleep_ac=$(pmset -g custom 2>/dev/null | awk '/AC Power/{ac=1} ac && $1=="sleep"{print $2; exit}')
		[ "$sleep_ac" = "0" ] && return 0
		say ""
		say "EmberStorm can only be reached while this Mac is awake."
		# The Mac setup app asks in its own window and says the answer here.
		if ask_yes "Keep it awake while plugged in, and start up again after a power cut? [Y/n]" "${EMBERSTORM_KEEP_AWAKE:-yes}"; then
			if as_root pmset -c sleep 0 && as_root pmset -a autorestart 1; then
				note "This Mac stays awake while plugged in, and starts again after a power cut."
			else
				note "Could not change it. System Settings, Energy, has the same switches."
			fi
			if pmset -g batt 2>/dev/null | grep -q InternalBattery; then
				note "A MacBook still sleeps with its lid closed unless a screen is plugged in."
			fi
		fi
		;;
	Linux)
		laptop=0
		for b in /sys/class/power_supply/BAT*; do [ -e "$b" ] && laptop=1; done
		gnome=0
		if command -v gsettings >/dev/null 2>&1 &&
			[ "$(gsettings get org.gnome.settings-daemon.plugins.power sleep-inactive-ac-type 2>/dev/null)" = "'suspend'" ]; then
			gnome=1
		fi
		[ "$laptop" = "0" ] && [ "$gnome" = "0" ] && return 0
		say ""
		say "EmberStorm can only be reached while this computer is awake."
		if ask_yes "Keep it awake while plugged in? [Y/n]" yes; then
			if [ "$gnome" = "1" ]; then
				gsettings set org.gnome.settings-daemon.plugins.power sleep-inactive-ac-type 'nothing' 2>/dev/null &&
					note "It no longer goes to sleep on its own while plugged in."
			fi
			if [ "$laptop" = "1" ] && [ -d /etc/systemd ]; then
				# The lid too, on mains power only; logind reads it from the
				# next start.
				if printf '[Login]\nHandleLidSwitchExternalPower=ignore\n' |
					as_root sh -c 'mkdir -p /etc/systemd/logind.conf.d && cat > /etc/systemd/logind.conf.d/emberstorm-lid.conf'; then
					note "Closing the lid will not sleep it while plugged in (from the next restart)."
				fi
			fi
		fi
		;;
	esac
}

# check_room stops before the long download when the disk Docker keeps its
# images on is short of the 20GB it needs, saying so plainly - a full disk
# failed part way through with an error from Docker nobody could read.
check_room() {
	case "$(uname -s)" in
	Darwin) where="$HOME" ;;
	*) where=$(docker info -f '{{.DockerRootDir}}' 2>/dev/null || true); [ -n "$where" ] && [ -d "$where" ] || where=/ ;;
	esac
	free_kb=$(df -Pk "$where" 2>/dev/null | awk 'NR==2{print $4}')
	case "$free_kb" in ''|*[!0-9]*) return 0 ;; esac
	[ "$free_kb" -ge 20971520 ] && return 0
	die "EmberStorm's programs need about 20 GB free where Docker keeps them
($where), and there is $((free_kb / 1048576)) GB.

Free some space there, then run this again. Your library can still go on
another drive (--library)."
}

need_docker() {
	if ! command -v docker >/dev/null 2>&1; then
		case "$(uname -s)" in
		Linux) install_docker_linux ;;
		Darwin) install_docker_mac ;;
		*) die "Docker is not installed.

Docker runs the media servers EmberStorm sits on top of. Install it from here,
then run this again:

  https://docs.docker.com/engine/install/" ;;
		esac
	fi
	docker_reachable && return
	case "$(uname -s)" in
	Darwin) start_docker_mac ;;
	*) start_docker_linux ;;
	esac
}

# Docker is set up as part of the install (2026-10-07, the owner's asking:
# nobody should have to install it first). On Linux it is plain Docker, as on
# the box - a background service, no windows, no account, started at boot.
# On a Mac it is Docker Desktop, made quiet as the Windows setup makes it: its
# installer's own --accept-license, its sign-in and survey marked done before
# it first starts, its dashboard kept away. Each asks for the computer's
# password once - installing system software always does - which sudo reads
# from the terminal even though this script arrives through a pipe.

# docker_reachable is whether docker answers - the one test that matters.
docker_reachable() { docker info >/dev/null 2>&1; }

# as_root runs a command as root: as it is when this is root, else by sudo.
as_root() {
	if [ "$(id -u)" = 0 ]; then
		"$@"
	elif [ -n "${SUDO_ASKPASS:-}" ] && ! [ -t 0 ] && command -v sudo >/dev/null 2>&1; then
		# The Mac setup app (mac/): no terminal to type into, so the password
		# is asked in a window of its own.
		sudo -A "$@"
	elif command -v sudo >/dev/null 2>&1; then
		sudo "$@"
	else
		die "Setting up Docker needs administrator rights, and this computer has no
sudo to ask for them. Run this again as root, or install Docker from here:

  https://docs.docker.com/engine/install/"
	fi
}

install_docker_linux() {
	step "Installing Docker"
	note "EmberStorm runs on Docker, which is set up now (free and open source)."
	important "Your computer may ask for your password - type it and press Enter."
	tmp=$(mktemp)
	fetch https://get.docker.com "$tmp"
	# Docker's own installer: it knows Debian, Ubuntu, Fedora, Raspberry Pi
	# OS and the rest, and adds Docker's repository so updates come with the
	# system's.
	# Its own output is a wall of version tables and advice meant for
	# administrators, a WARNING among them: kept in a log, shown only if it
	# fails. sudo still asks for the password on the terminal.
	log=$(mktemp)
	note "Installing... (a few minutes)"
	if ! as_root sh "$tmp" >"$log" 2>&1; then
		# Docker's installer knows the common systems by name only, and
		# refuses the rest - Arch, openSUSE, Alpine, and some made from
		# Ubuntu (Linux Mint, Pop!_OS). Then the system's own Docker, from
		# its own package manager.
		if ! install_docker_distro >>"$log" 2>&1; then
			tail -n 15 "$log" >&2
			rm -f "$tmp" "$log"
			die "Docker could not be installed on this system automatically.
Install Docker from here, then run this again:

  https://docs.docker.com/engine/install/"
		fi
	fi
	rm -f "$tmp" "$log"
	enable_docker_service
	note "Docker is installed."
}

# install_docker_distro installs the system's own Docker and Compose with
# whatever package manager it has. Compose has a different name almost
# everywhere, so each name is tried in turn.
install_docker_distro() {
	if command -v apt-get >/dev/null 2>&1; then
		# A half-made Docker repository from Docker's own installer would stop
		# apt from updating at all.
		as_root rm -f /etc/apt/sources.list.d/docker.list
		as_root apt-get update -q &&
			as_root env DEBIAN_FRONTEND=noninteractive apt-get install -yq docker.io || return 1
		for c in docker-compose-v2 docker-compose-plugin docker-compose; do
			as_root env DEBIAN_FRONTEND=noninteractive apt-get install -yq "$c" && return 0
		done
		return 1
	elif command -v dnf >/dev/null 2>&1; then
		as_root dnf install -y docker || as_root dnf install -y moby-engine || return 1
		as_root dnf install -y docker-compose || as_root dnf install -y docker-compose-plugin
	elif command -v pacman >/dev/null 2>&1; then
		as_root pacman -Sy --noconfirm --needed docker docker-compose
	elif command -v zypper >/dev/null 2>&1; then
		as_root zypper --non-interactive install docker docker-compose
	elif command -v apk >/dev/null 2>&1; then
		as_root apk add docker docker-cli-compose
	else
		return 1
	fi
}

# enable_docker_service starts Docker now and at every boot: systemd almost
# everywhere, OpenRC on Alpine.
enable_docker_service() {
	if command -v systemctl >/dev/null 2>&1; then
		as_root systemctl enable --now docker >/dev/null 2>&1
	elif command -v rc-update >/dev/null 2>&1; then
		as_root rc-update add docker default >/dev/null 2>&1
		as_root rc-service docker start >/dev/null 2>&1
	fi
}

start_docker_linux() {
	# Running, but this account may not use it yet: a new install adds it to
	# the docker group, which only a new login brings - and the old message,
	# "not running", sent people to start what was already running.
	if docker info 2>&1 | grep -qi 'permission denied'; then
		if [ "$(id -u)" != 0 ] && ! id -nG | tr ' ' '\n' | grep -qx docker; then
			note "Letting this account use Docker."
			as_root usermod -aG docker "$(id -un)" || die "Could not let this account use Docker. Run this again with sudo."
		fi
		# For the rest of this run, through the group just joined (sg reads it
		# afresh, no new login needed); from the next login, as it is.
		if command -v sg >/dev/null 2>&1; then
			docker() { sg docker -c "docker $(shell_quote "$@")"; }
		else
			docker() { as_root docker "$@"; }
		fi
		docker_reachable && return
	fi
	if command -v systemctl >/dev/null 2>&1 || command -v rc-service >/dev/null 2>&1; then
		note "Starting Docker."
		enable_docker_service
		i=0
		while [ $i -lt 30 ] && ! docker_reachable; do sleep 2; i=$((i + 1)); done
		docker_reachable && return
	fi
	die "Docker is installed but would not start.

  sudo systemctl start docker

Then run this again."
}

# shell_quote quotes each argument for sh -c.
shell_quote() {
	for a in "$@"; do
		printf "'%s' " "$(printf '%s' "$a" | sed "s/'/'\\\\''/g")"
	done
}

# detach_dmg lets go of the Docker download's disk image. Straight after
# Docker's own installer it can still be busy (hdiutil exits 16), and under
# set -e that ended the whole setup with no word said - found on the first
# real run on a Mac. Tried again, then forced; never fatal: Docker is
# installed either way, and macOS lets go of it at the latest on restart.
detach_dmg() {
	for _ in 1 2 3; do
		hdiutil detach -quiet "$1" 2>/dev/null && return 0
		sleep 2
	done
	hdiutil detach -quiet -force "$1" 2>/dev/null || true
}

install_docker_mac() {
	step "Installing Docker Desktop"
	note "EmberStorm runs inside Docker Desktop, which is set up now. There is"
	note "nothing to click in it and no Docker account is needed."
	note "Docker Desktop is free for personal use and small businesses; installing"
	note "it accepts Docker's terms: docker.com/legal/docker-subscription-service-agreement"
	important "Your Mac will ask for your password - type it and press Enter."
	case "$(uname -m)" in
	arm64) arch=arm64 ;;
	*) arch=amd64 ;;
	esac
	tmp=$(mktemp -d)
	note "Downloading Docker Desktop (about 600 MB)..."
	fetch "https://desktop.docker.com/mac/main/$arch/Docker.dmg" "$tmp/Docker.dmg"
	hdiutil attach -nobrowse -quiet -mountpoint "$tmp/mnt" "$tmp/Docker.dmg" ||
		die "Could not open the Docker Desktop download. Run this again."
	# Docker's own command-line install: its terms accepted, and set up for
	# this user so its first start needs no password of its own.
	if ! as_root "$tmp/mnt/Docker.app/Contents/MacOS/install" --accept-license --user="$(id -un)"; then
		detach_dmg "$tmp/mnt"
		rm -rf "$tmp"
		die "Docker Desktop did not finish installing. Install it from here, then
run this again:

  https://www.docker.com/products/docker-desktop/"
	fi
	detach_dmg "$tmp/mnt"
	rm -rf "$tmp"
	# Where its command lives, should the installer not have linked it into
	# a folder on PATH yet.
	PATH="$PATH:/Applications/Docker.app/Contents/Resources/bin"
	note "Docker Desktop is installed."
}

# Docker Desktop's settings are not written ahead on a Mac, as they are on
# Windows (Hide-DockerDashboard): they live in its group container,
# ~/Library/Group Containers/group.com.docker, which macOS guards - writing
# there stopped the setup on "Terminal would like to access data from other
# apps" (seen on the first real run on a Mac, 2026-10-08, and asked even with
# Docker Desktop not installed), and "Don't Allow" then ended the setup with
# no word said. Its terms are accepted by its own installer; what it may
# still show on its first start - its sign-in and a survey - is said below.

start_docker_mac() {
	[ -d /Applications/Docker.app ] || die "Docker is installed but Docker Desktop is not in Applications.
Open Docker Desktop, wait until it says Running, then run this again."
	note "Starting Docker Desktop. This takes a minute or two."
	note "If a Docker window opens, you don't need a Docker account: click Skip"
	note "(or Continue without signing in) on anything it asks, then close it."
	open -g -a Docker
	PATH="$PATH:/Applications/Docker.app/Contents/Resources/bin"
	i=0
	while [ $i -lt 150 ] && ! docker_reachable; do
		sleep 2
		i=$((i + 1))
		[ $((i % 15)) -eq 0 ] && note "Docker is still starting... ($((i * 2)) seconds). This is normal."
	done
	docker_reachable && return
	die "Docker Desktop was started but never came up.

Open Docker Desktop: it may be waiting on you (accepting its terms, or a
permission). Once it says Running, run this again."
}

# compose_cmd sets COMPOSE to whichever form of compose exists. v2 is a docker
# subcommand; v1 was a separate binary and is still what some distributions
# package.
compose_cmd() {
	if docker compose version >/dev/null 2>&1; then
		# The file named: left to itself, compose also reads an override or a
		# compose.yaml left in the folder (the thirteenth security pass).
		COMPOSE="docker compose -f docker-compose.yml"
	elif command -v docker-compose >/dev/null 2>&1; then
		COMPOSE="docker-compose -f docker-compose.yml"
	else
		die "Docker is running but Docker Compose is missing.

Docker Desktop includes it. On Linux:

  sudo apt install docker-compose-plugin     # Debian, Ubuntu
  sudo dnf install docker-compose-plugin     # Fedora

Then run this again."
	fi
}

# fetch downloads a url to a file using whatever the machine has.
fetch() {
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL "$1" -o "$2"
	elif command -v wget >/dev/null 2>&1; then
		wget -qO "$2" "$1"
	else
		die "Neither curl nor wget is installed, so this script cannot download
anything. Install either one, or download the compose file by hand:

  $COMPOSE_URL"
	fi
}

# port_taken answers only when it can actually tell. Guessing "free" and letting
# compose report the conflict is better than guessing "taken" and moving a
# server off the port somebody expected it on.
port_taken() {
	if command -v nc >/dev/null 2>&1; then
		nc -z 127.0.0.1 "$1" >/dev/null 2>&1
	elif command -v ss >/dev/null 2>&1; then
		ss -ltn 2>/dev/null | grep -q "[:.]$1[[:space:]]"
	elif command -v lsof >/dev/null 2>&1; then
		lsof -iTCP:"$1" -sTCP:LISTEN >/dev/null 2>&1
	else
		return 1
	fi
}

pick_port() {
	port="$FIRST_PORT"
	attempts=0
	while port_taken "$port"; do
		attempts=$((attempts + 1))
		if [ "$attempts" -gt 20 ]; then
			die "Ports $FIRST_PORT to $port are all in use. Pick one yourself:

  SOUNDSTORM_PORT=9000 sh install.sh"
		fi
		port=$((port + 1))
	done
	PORT="$port"
}

# get_env and set_env read and rewrite one line of .env and leave the rest
# alone, because the port and the certificate hosts live in there too and were
# worked out on a run nobody is going to repeat.
# A $ in a value is written as $$, which compose reads as one $ rather than
# the start of a variable (a library folder called "My$Music" mounted as
# "My" - the twelfth security pass), and read back as one.
get_env() {
	[ -f .env ] || return 0
	sed -n "s/^[[:space:]]*$1=//p" .env | head -n 1 | sed 's/\$\$/$/g'
}

set_env() {
	# One line per setting: a value carrying a line break would write a
	# second setting of its own choosing - and some values come from the
	# network (the router's UPnP answer).
	case "$2" in
	*"
"* | *"$(printf '\r')"*)
		note "Ignored a setting for $1 that was not a single line."
		return
		;;
	esac
	if [ -f .env ]; then
		# umask 077 so the rewrite keeps .env private - mv takes the new
		# file's permissions, and .env holds the setup code and auth key.
		( umask 077; grep -v "^[[:space:]]*$1=" .env > .env.new ) || true
		mv .env.new .env
	fi
	printf '%s=%s\n' "$1" "$(printf '%s' "$2" | sed 's/\$/$$/g')" >> .env
}

# library_path is where this install keeps its media: beside it, unless .env
# says otherwise.
library_path() {
	chosen=$(get_env SOUNDSTORM_LIBRARY_PATH)
	if [ -n "$chosen" ]; then
		printf '%s' "$chosen"
	else
		printf '%s' "$DIR/library"
	fi
}

# installed_scheme reports what this install actually serves rather than
# assuming http. Telling somebody the wrong scheme hands them a browser error
# with nothing in it to suggest the address was the problem.
#
# Auto mode is http: it answers http and https on the same port, http works
# from the first second, and the page moves itself to the real https address
# once it has checked the browser can reach it. Only self-signed and file are
# https alone.
installed_scheme() {
	case "$(get_env SOUNDSTORM_TLS)" in
		self-signed|file) printf 'https' ;;
		*) printf 'http' ;;
	esac
}

# secure_address waits briefly for auto mode's real https address, asking
# EmberStorm over plain http on this machine, so no certificate is involved in
# the asking. Empty if it has not arrived by the deadline; http works meanwhile.
secure_address() {
	waited=0
	while [ "$waited" -lt 45 ]; do
		if command -v curl >/dev/null 2>&1; then
			body=$(curl -fsS "http://localhost:$PORT/api/session" 2>/dev/null) || body=''
		elif command -v wget >/dev/null 2>&1; then
			body=$(wget -q -O - "http://localhost:$PORT/api/session" 2>/dev/null) || body=''
		else
			return 0
		fi
		name=$(printf '%s' "$body" | sed -n 's/.*"secureName":"\([^"]*\)".*/\1/p')
		if [ -n "$name" ]; then
			printf 'https://%s:%s' "$name" "$PORT"
			return 0
		fi
		sleep 3
		waited=$((waited + 3))
	done
}

# write_serve_config writes the file Tailscale proxies through.
#
# The scheme is the one thing that cannot be a constant. Tailscale talks to
# EmberStorm over the internal compose network, where EmberStorm is speaking
# either plain HTTP or its own self-signed HTTPS depending on --https. Point it
# at the wrong one and the tailnet address answers 502 while everything else
# looks fine. https+insecure is Tailscale's documented pseudo-scheme for a
# certificate nothing can validate, which is what a local authority issues.
write_serve_config() {
	if [ "$(installed_scheme)" = "https" ]; then
		target="https+insecure://soundstorm-app:8080"
	else
		target="http://soundstorm-app:8080"
	fi
	# The backslash is load-bearing: TS_CERT_DOMAIN is substituted by the
	# Tailscale container at run time, so it has to reach the file as literal
	# text. $target, one line down, is meant to expand here and does.
	cat > tailscale-serve.json <<EOF
{
  "TCP": { "443": { "HTTPS": true } },
  "Web": {
    "\${TS_CERT_DOMAIN}:443": {
      "Handlers": {
        "/": { "Proxy": "$target" }
      }
    }
  }
}
EOF
}

# health_ok tolerates a self-signed certificate, because with --https that is
# precisely what the server just minted for itself. The request goes to this
# machine, for a certificate this machine made.
health_ok() {
	if command -v curl >/dev/null 2>&1; then
		curl -fsSk "$1" -o /dev/null >/dev/null 2>&1
	elif command -v wget >/dev/null 2>&1; then
		wget -q --no-check-certificate -O /dev/null "$1" >/dev/null 2>&1
	else
		return 0
	fi
}

# lan_address is this machine's address on the local network.
#
# The container cannot work this out for itself - inside Docker the only
# addresses visible are the container's own - and "localhost" is useless the
# moment somebody picks up a phone.
lan_address() {
	if command -v ip >/dev/null 2>&1; then
		# The source address the kernel would use to reach the internet, which
		# is the one other machines on the network can reach back on.
		ip route get 1.1.1.1 2>/dev/null | awk '{for (i=1;i<=NF;i++) if ($i=="src") {print $(i+1); exit}}'
		return
	fi
	if command -v ipconfig >/dev/null 2>&1; then
		for interface in en0 en1 eth0; do
			addr=$(ipconfig getifaddr "$interface" 2>/dev/null) || true
			if [ -n "$addr" ]; then
				printf '%s' "$addr"
				return
			fi
		done
	fi
	if command -v hostname >/dev/null 2>&1; then
		hostname -I 2>/dev/null | awk '{print $1}'
	fi
}

# gateway_address is the home router's LAN address - the default route's next
# hop - so remote access can ask it to open the port. The container cannot find
# this itself, for the same reason it cannot find the LAN address: its own
# default route is the Docker bridge, not the router.
gateway_address() {
	if command -v ip >/dev/null 2>&1; then
		ip route get 1.1.1.1 2>/dev/null | awk '{for (i=1;i<=NF;i++) if ($i=="via") {print $(i+1); exit}}'
		return
	fi
	if command -v route >/dev/null 2>&1; then
		route -n get default 2>/dev/null | awk '/gateway:/{print $2; exit}'
		return
	fi
	if command -v netstat >/dev/null 2>&1; then
		netstat -rn 2>/dev/null | awk '$1=="default" || $1=="0.0.0.0" {print $2; exit}'
	fi
}

# machine_addresses lists every IPv4 address this machine has, one a line.
machine_addresses() {
	if command -v ip >/dev/null 2>&1; then
		ip -o -4 addr show 2>/dev/null | awk '{split($4, a, "/"); print a[1]}'
		return
	fi
	if command -v ifconfig >/dev/null 2>&1; then
		ifconfig 2>/dev/null | awk '$1 == "inet" {print $2}' | sed 's/^addr://'
		return
	fi
	hostname -I 2>/dev/null | tr ' ' '\n'
}

# refresh_lan_address points the recorded LAN address at where this machine is
# now, when it has moved - another network, or a router that handed out a new
# address (a power cut, a new router). Written once and never looked at again,
# the secure name every phone saved kept pointing at an address this machine no
# longer had. Only the first entry, and only when it is on none of this
# machine's interfaces: an address still here was chosen, not left behind (the
# Windows installer's Update-LanAddress, the same rule). True when it changed.
refresh_lan_address() {
	hosts=$(get_env SOUNDSTORM_TLS_HOSTS)
	[ -n "$hosts" ] || return 1
	first=${hosts%%,*}
	rest=
	case "$hosts" in *,*) rest=",${hosts#*,}" ;; esac
	case "$first" in '' | *[!0-9.]*) return 1 ;; esac
	now=$(lan_address)
	[ -n "$now" ] && [ "$now" != "$first" ] || return 1
	if machine_addresses | grep -qxF "$first"; then return 1; fi
	set_env SOUNDSTORM_TLS_HOSTS "$now$rest"
	return 0
}

# refresh_gateway does the same for the router: a machine on another network
# kept asking the old house's router to open its port. A new router also means
# its UPnP address is looked for again. True when it changed.
refresh_gateway() {
	was=$(get_env SOUNDSTORM_GATEWAY)
	[ -n "$was" ] || return 1
	now=$(gateway_address)
	[ -n "$now" ] && [ "$now" != "$was" ] || return 1
	set_env SOUNDSTORM_GATEWAY "$now"
	set_env SOUNDSTORM_UPNP_URL ""
	return 0
}

# A check this machine runs by itself - when it starts, and every ten minutes
# - so a changed address is picked up with nobody running the installer: the
# secure name follows the new address, and the router the machine is behind
# now is the one asked to open the port. Through the user's crontab, which
# needs no administrator; uninstalling takes it out again. (On Windows the
# start-up shortcut does the same; the EmberStorm box does it on every start.)
ADDRESS_TAG='# soundstorm-address'
# On a Mac, a LaunchAgent of this name does it instead (no crontab).
ADDRESS_AGENT='dev.soundstorm.address'
install_address_watch() {
	[ "$(uname -s)" = Darwin ] || command -v crontab >/dev/null 2>&1 || return 0
	watch="$DIR/soundstorm-address.sh"
	docker_dir=$(dirname "$(command -v docker 2>/dev/null || echo /usr/local/bin/docker)")
	{
		printf '#!/bin/sh\n'
		printf '# Points EmberStorm at this machine'"'"'s current network address when it has moved.\n'
		printf '# Run by cron (installed by install.sh); safe to run by hand.\n'
		# Quoted as sh reads them: the folder is the person's to name, and a
		# quote or $( in it would have run in this script every ten minutes
		# (the thirteenth security pass).
		qdocker=$(shell_quote "$docker_dir")
		qdir=$(shell_quote "$DIR")
		qcompose=$(shell_quote "$COMPOSE")
		printf 'PATH=%s":/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin:$PATH"\n' "${qdocker% }"
		printf 'cd %s || exit 0\n' "${qdir% }"
		printf '[ -f .env ] && [ -f docker-compose.yml ] || exit 0\n'
		printf 'COMPOSE=%s\n' "${qcompose% }"
		# Its own copies of the installer's helpers: the installer usually
		# arrives through a pipe (curl | sh), with no file to copy them from.
		cat <<'WATCH'
get_env() {
	sed -n "s/^[[:space:]]*$1=//p" .env | head -n 1
}
set_env() {
	case "$2" in *"
"* | *"$(printf '\r')"*) return ;; esac
	( umask 077; grep -v "^[[:space:]]*$1=" .env > .env.new ) || true
	mv .env.new .env
	printf '%s=%s\n' "$1" "$2" >> .env
}
lan_address() {
	if command -v ip >/dev/null 2>&1; then
		ip route get 1.1.1.1 2>/dev/null | awk '{for (i=1;i<=NF;i++) if ($i=="src") {print $(i+1); exit}}'
		return
	fi
	if command -v ipconfig >/dev/null 2>&1; then
		for interface in en0 en1 eth0; do
			addr=$(ipconfig getifaddr "$interface" 2>/dev/null) || true
			if [ -n "$addr" ]; then printf '%s' "$addr"; return; fi
		done
	fi
	if command -v hostname >/dev/null 2>&1; then hostname -I 2>/dev/null | awk '{print $1}'; fi
}
gateway_address() {
	if command -v ip >/dev/null 2>&1; then
		ip route get 1.1.1.1 2>/dev/null | awk '{for (i=1;i<=NF;i++) if ($i=="via") {print $(i+1); exit}}'
		return
	fi
	if command -v route >/dev/null 2>&1; then
		route -n get default 2>/dev/null | awk '/gateway:/{print $2; exit}'
		return
	fi
	if command -v netstat >/dev/null 2>&1; then
		netstat -rn 2>/dev/null | awk '$1=="default" || $1=="0.0.0.0" {print $2; exit}'
	fi
}
machine_addresses() {
	if command -v ip >/dev/null 2>&1; then
		ip -o -4 addr show 2>/dev/null | awk '{split($4, a, "/"); print a[1]}'
		return
	fi
	if command -v ifconfig >/dev/null 2>&1; then
		ifconfig 2>/dev/null | awk '$1 == "inet" {print $2}' | sed 's/^addr://'
		return
	fi
	hostname -I 2>/dev/null | tr ' ' '\n'
}
refresh_lan_address() {
	hosts=$(get_env SOUNDSTORM_TLS_HOSTS)
	[ -n "$hosts" ] || return 1
	first=${hosts%%,*}
	rest=
	case "$hosts" in *,*) rest=",${hosts#*,}" ;; esac
	case "$first" in '' | *[!0-9.]*) return 1 ;; esac
	now=$(lan_address)
	[ -n "$now" ] && [ "$now" != "$first" ] || return 1
	if machine_addresses | grep -qxF "$first"; then return 1; fi
	set_env SOUNDSTORM_TLS_HOSTS "$now$rest"
	return 0
}
refresh_gateway() {
	was=$(get_env SOUNDSTORM_GATEWAY)
	[ -n "$was" ] || return 1
	now=$(gateway_address)
	[ -n "$now" ] && [ "$now" != "$was" ] || return 1
	set_env SOUNDSTORM_GATEWAY "$now"
	set_env SOUNDSTORM_UPNP_URL ""
	return 0
}
changed=
refresh_lan_address && changed=1
refresh_gateway && changed=1
# Compose sees the changed .env and starts EmberStorm again on it.
[ -n "$changed" ] && $COMPOSE up -d >/dev/null 2>&1
exit 0
WATCH
	} > "$watch.new" || return 0
	chmod 755 "$watch.new"
	mv "$watch.new" "$watch"
	if [ "$(uname -s)" = Darwin ]; then
		# A Mac: a LaunchAgent, macOS's own way to run something on a timer.
		# Changing the crontab there stopped the setup on "would like to
		# administer your computer" (the first real run, 2026-10-10).
		agent="$HOME/Library/LaunchAgents/$ADDRESS_AGENT.plist"
		mkdir -p "$HOME/Library/LaunchAgents"
		cat >"$agent" <<AGENT
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>$ADDRESS_AGENT</string>
	<key>ProgramArguments</key><array><string>/bin/sh</string><string>$(printf '%s' "$watch" | sed 's/&/\&amp;/g; s/</\&lt;/g; s/>/\&gt;/g')</string></array>
	<key>StartInterval</key><integer>600</integer>
	<key>RunAtLoad</key><true/>
	<key>StandardOutPath</key><string>/dev/null</string>
	<key>StandardErrorPath</key><string>/dev/null</string>
</dict>
</plist>
AGENT
		launchctl bootout "gui/$(id -u)/$ADDRESS_AGENT" 2>/dev/null || true
		launchctl bootstrap "gui/$(id -u)" "$agent" 2>/dev/null || true
		# One from before this, in the crontab, is left alone: reading the
		# crontab is enough to ask the same question.
		return 0
	fi
	{
		crontab -l 2>/dev/null | grep -vF "$ADDRESS_TAG"
		printf '@reboot sleep 90; sh "%s" >/dev/null 2>&1 %s\n' "$watch" "$ADDRESS_TAG"
		printf '*/10 * * * * sh "%s" >/dev/null 2>&1 %s\n' "$watch" "$ADDRESS_TAG"
	} | crontab - 2>/dev/null || true
}

remove_address_watch() {
	rm -f "$DIR/soundstorm-address.sh"
	if [ "$(uname -s)" = Darwin ]; then
		launchctl bootout "gui/$(id -u)/$ADDRESS_AGENT" 2>/dev/null || true
		rm -f "$HOME/Library/LaunchAgents/$ADDRESS_AGENT.plist"
		# A crontab line from an older setup is left: touching the crontab
		# asks to "administer your computer", and the line only runs a
		# script that is gone now, quietly.
		return 0
	fi
	command -v crontab >/dev/null 2>&1 || return 0
	if crontab -l 2>/dev/null | grep -qF "$ADDRESS_TAG"; then
		crontab -l 2>/dev/null | grep -vF "$ADDRESS_TAG" | crontab - 2>/dev/null || true
	fi
}

# upnp_url discovers the router's UPnP device-description URL over SSDP, the
# fallback for opening the port on routers that speak UPnP but not NAT-PMP/PCP.
# Done on the host because SSDP multicast does not cross the Docker bridge; the
# SOAP that uses the URL later is unicast and does work from the container.
#
# Best effort, and there is no one tool every box has, so it tries what is
# there: miniupnpc's upnpc, then a short python3 M-SEARCH. Where neither is
# present it prints nothing and NAT-PMP/PCP or a manual forward remain.
upnp_url() {
	if command -v upnpc >/dev/null 2>&1; then
		url=$(upnpc -l 2>/dev/null | awk -F'[ \t]*' '/desc:/ {print $2; exit}')
		if [ -n "$url" ]; then
			printf '%s' "$url"
			return
		fi
	fi
	if command -v python3 >/dev/null 2>&1; then
		python3 - <<-'PY' 2>/dev/null
			import socket
			m = ("M-SEARCH * HTTP/1.1\r\n"
			     "HOST: 239.255.255.250:1900\r\n"
			     "MAN: \"ssdp:discover\"\r\n"
			     "MX: 2\r\n"
			     "ST: urn:schemas-upnp-org:device:InternetGatewayDevice:1\r\n\r\n")
			s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
			s.settimeout(3)
			try:
			    s.sendto(m.encode(), ("239.255.255.250", 1900))
			    while True:
			        data, _ = s.recvfrom(2048)
			        for line in data.decode(errors="ignore").split("\r\n"):
			            if line.lower().startswith("location:"):
			                print(line.split(":", 1)[1].strip())
			                raise SystemExit
			except SystemExit:
			    pass
			except Exception:
			    pass
		PY
		return
	fi
}

# mdns_name is the name other devices can use instead of an IP address.
#
# macOS always answers for "<hostname>.local"; Linux does when avahi is
# running. Printed only when it actually resolves, because an address that
# does not work is worse than an ugly one that does.
mdns_name() {
	name=$(hostname -s 2>/dev/null || hostname 2>/dev/null) || return
	[ -n "$name" ] || return
	candidate="$name.local"

	if command -v getent >/dev/null 2>&1 && getent hosts "$candidate" >/dev/null 2>&1; then
		printf '%s' "$candidate"
		return
	fi
	if command -v dscacheutil >/dev/null 2>&1 &&
		dscacheutil -q host -a name "$candidate" 2>/dev/null | grep -q 'ip_address'; then
		printf '%s' "$candidate"
		return
	fi
	if ping -c 1 -W 1 "$candidate" >/dev/null 2>&1; then
		printf '%s' "$candidate"
	fi
}

open_browser() {
	# Never when piped: an installer run from a provisioning script should not
	# try to launch a GUI on a headless box.
	[ -t 1 ] || return 0
	if command -v xdg-open >/dev/null 2>&1; then xdg-open "$1" >/dev/null 2>&1 || true
	elif command -v open >/dev/null 2>&1; then open "$1" >/dev/null 2>&1 || true
	fi
}

# --- removing it -------------------------------------------------------------

# save_backup writes the state backup to $1, owned by whoever runs this and
# readable by nobody else.
#
# Through standard output ("backup -"), so this shell creates the file. Written
# through a bind mount instead, it belonged to the container's user (uid 10001)
# with mode 0600 - so on Linux the person uninstalling could not read or copy
# the one file they were told to carry off the machine, without root.
#
# The output must start with "{" before it replaces anything: an image older
# than "backup -" prints its summary there rather than the backup. For such an
# image the old bind-mount form is used - a backup only root can read is far
# better than none, at the one moment nothing else holds these passwords.
save_backup() {
	part="$1.part"
	rm -f "$part"
	if (umask 077 && $COMPOSE run --rm -T soundstorm backup - >"$part" 2>/dev/null) &&
		[ "$(head -c 1 "$part" 2>/dev/null)" = "{" ]; then
		mv -f "$part" "$1"
		return 0
	fi
	rm -f "$part"
	$COMPOSE run --rm -v "$DIR:/backup" soundstorm backup "/backup/$(basename "$1")" >/dev/null 2>&1 &&
		[ -f "$1" ]
}

uninstall() {
	say ""
	say "${BOLD}Removing EmberStorm${OFF}"
	say ""

	library=$(library_path)

	if [ -f "$DIR/docker-compose.yml" ]; then
		cd "$DIR"
		if command -v docker >/dev/null 2>&1; then
			# A copy first, into the folder rather than the volume about to be
			# deleted. This is the exact moment the credentials for four
			# backends stop existing anywhere.
			step "Saving your accounts first"
			if save_backup "$DIR/soundstorm-backup.json"; then
				note "saved to $DIR/soundstorm-backup.json"
				note "keep it if you might reinstall - it is the only copy of the"
				note "passwords EmberStorm made on the media servers"
			else
				note "could not save a copy; carrying on with the uninstall"
			fi

			step "Stopping it and removing its data"
			note "accounts and the servers own settings go; your media does not"
			# down -v takes the named volumes with it - EmberStorm accounts,
			# and Jellyfin and Navidrome own databases. The library is a bind
			# mount from the folder and is untouched by this.
			$COMPOSE down -v >/dev/null 2>&1 || true
		else
			note "docker is not available, so the containers were left alone"
		fi
		step "Cleaning up"
		# soundstorm-backup.json is deliberately not in this list.
		rm -f "$DIR/docker-compose.yml" "$DIR/.env"
		remove_address_watch
	else
		note "nothing installed in $DIR"
	fi

	say ""
	say "${GREEN}${BOLD}Done.${OFF} EmberStorm is gone."
	say ""
	if [ -d "$library" ]; then
		say "Your media has been left exactly where it was:"
		say ""
		say "    $library"
		say ""
		say "Delete that folder yourself if you want it gone."
	fi
	say ""
	note "Docker was left installed - other things may be using it."
	say ""
	exit 0
}

# --- moving it to another computer ------------------------------------------

# What a move carries, besides the library: EmberStorm's own state (accounts,
# the passwords it made on every backend, favorites, playlists, positions, the
# install's name) and each backend's own database. Left out on purpose:
# jellyfin-cache, immich-models, storyteller-models and audiomuse-temp, which rebuild or
# download themselves; and tailscale-state, a node identity that belongs to one
# machine - the new one joins the tailnet afresh.
MOVE_VOLUMES="soundstorm-state navidrome-data jellyfin-config abs-config abs-metadata immich-data immich-db storyteller-data audiomuse-db"

# Settings that describe this computer and its network rather than the
# install. They are worked out again on the new one.
MOVE_LOCAL='^SOUNDSTORM_(PORT|TLS_HOSTS|LIBRARY_PATH|LIBRARY_HINT|GATEWAY|UPNP_URL|NOT_HOME)='

# windows_name_problems lists files under $1 that cannot exist on Windows:
# names with a character Windows refuses, names ending in a dot or a space,
# reserved device names, and two names in one folder that differ only in
# case. Moving to Windows would fail on exactly these, halfway through a copy.
windows_name_problems() {
	find "$1" -mindepth 1 2>/dev/null | while IFS= read -r path; do
		name=${path##*/}
		case "$name" in
			*[:\*\?\"\<\>\|\\]*|*.|*' ') printf '%s\n' "${path#"$1"/}" ;;
		esac
		case "$(printf '%s' "${name%%.*}" | tr '[:lower:]' '[:upper:]')" in
			CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9]) printf '%s\n' "${path#"$1"/}" ;;
		esac
	done
	find "$1" -mindepth 1 2>/dev/null | awk '{ k = tolower($0); if (k in seen) print substr($0, length(root) + 2); seen[k] = 1 }' root="$1"
}

# export_move packs this install into a EmberStorm-move folder inside $1: the
# data volumes as tar files, the settings that belong to the install, and
# (unless NO_LIBRARY) a copy of the library. EmberStorm is stopped while its
# data is copied - a database copied while it is being written is a database
# that may not open - and started again after, whatever happened.
export_move() {
	[ -f "$DIR/docker-compose.yml" ] || die "EmberStorm is not installed in $DIR, so there is nothing to move.

If it is installed somewhere else, run this with SOUNDSTORM_DIR set to that folder."
	[ -d "$1" ] || die "$1 does not exist. Give a folder that does - an external drive, say."
	dest="$(cd "$1" && pwd)/EmberStorm-move"
	[ ! -e "$dest" ] || die "$dest is already there.

Move or delete it first, so an older move is not mixed into this one."
	cd "$DIR"
	library=$(library_path)
	DIE_HEADING='EmberStorm could not be packed up.'

	say ""
	say "${BOLD}Packing up EmberStorm to move to another computer${OFF}"

	step "Checking there is room"
	need=0
	if [ -z "${NO_LIBRARY:-}" ] && [ -d "$library" ]; then
		need=$(du -sk "$library" 2>/dev/null | awk '{ print $1 }')
	fi
	# The data, measured where it lives, plus a little to spare.
	for v in $MOVE_VOLUMES; do
		docker volume inspect "${PROJECT}_$v" >/dev/null 2>&1 || continue
		size=$(docker run --rm -v "${PROJECT}_$v:/v:ro" "$MOVE_IMAGE" du -sk /v 2>/dev/null | awk '{ print $1 }')
		need=$((need + ${size:-0}))
	done
	need=$((need + 100 * 1024))
	avail=$(df -Pk "$1" | awk 'NR == 2 { print $4 }')
	if [ -n "$avail" ] && [ "$avail" -lt "$need" ]; then
		die "There is not enough room in $1: about $((need / 1024 / 1024 + 1)) GB is needed and $((avail / 1024 / 1024)) GB is free.

Choose a bigger drive, or leave the media out with --no-library and copy it
yourself."
	fi
	note "about $((need / 1024 / 1024 + 1)) GB to copy, $((avail / 1024 / 1024)) GB free"

	# The volumes hold every backend's admin password, the accounts' password
	# hashes and the certificate keys: only this user may read them (a
	# security review found them readable by everyone, as settings.env never was).
	(umask 077; mkdir -p "$dest/volumes")
	chmod 700 "$dest/volumes"

	if [ -z "${NO_LIBRARY:-}" ] && [ -d "$library" ]; then
		problems=$(windows_name_problems "$library")
		if [ -n "$problems" ]; then
			printf '%s\n' "$problems" > "$dest/windows-name-problems.txt"
			say ""
			important "$(printf '%s\n' "$problems" | wc -l | tr -d ' ') files have names Windows does not allow. They move"
			important "fine to a Mac or Linux, but not to Windows. The list is in"
			important "$dest/windows-name-problems.txt"
		fi
	fi

	step "Stopping EmberStorm while its data is copied"
	$COMPOSE stop >/dev/null 2>&1 || true
	# Started again however this ends, a failure included.
	trap '$COMPOSE start >/dev/null 2>&1 || true' EXIT

	step "Copying accounts, settings and the media servers' data"
	me="$(id -u):$(id -g)"
	for v in $MOVE_VOLUMES; do
		docker volume inspect "${PROJECT}_$v" >/dev/null 2>&1 || continue
		note "$v"
		# tar inside a container: the volume is Docker's, and only a container
		# can read it. Owners are kept as numbers, which is what each backend
		# needs to read its own files on the other side.
		docker run --rm -v "${PROJECT}_$v:/from:ro" -v "$dest/volumes:/to" "$MOVE_IMAGE" \
			sh -c "umask 077; tar -cf /to/$v.tar -C /from . && chown $me /to/$v.tar && chmod 600 /to/$v.tar" ||
			die "Could not copy $v. EmberStorm has been started again, unchanged."
	done
	(umask 077; grep -Ev "$MOVE_LOCAL" .env > "$dest/settings.env")

	if [ -z "${NO_LIBRARY:-}" ] && [ -d "$library" ]; then
		step "Copying your media"
		note "this is the long part"
		mkdir -p "$dest/library"
		# Everything that can be copied is: a drive formatted for Windows
		# refuses the names listed above, and one refused name must not cost
		# the rest of the library. What failed is listed and said out loud.
		if ! cp -R "$library/." "$dest/library/" 2>"$dest/copy-errors.txt"; then
			say ""
			important "$(wc -l < "$dest/copy-errors.txt" | tr -d ' ') files could not be copied, usually because the drive"
			important "does not allow their names. They are listed in"
			important "$dest/copy-errors.txt - copy those by hand."
		fi
		[ -s "$dest/copy-errors.txt" ] || rm -f "$dest/copy-errors.txt"
	fi

	write_move_launchers "$dest"
	{
		printf 'format=1\n'
		printf 'created=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
		printf 'from=%s\n' "$(uname -s)"
		if [ -d "$dest/library" ]; then printf 'library=yes\n'; else printf 'library=no\n'; fi
	} > "$dest/manifest.txt"

	step "Starting EmberStorm again"
	$COMPOSE start >/dev/null 2>&1 || true
	trap - EXIT

	say ""
	say "${GREEN}${BOLD}Packed.${OFF} Everything is in"
	say ""
	say "    $dest"
	say ""
	say "On the new computer:"
	say "  Windows:        copy the folder over and double-click"
	say "                  \"Install EmberStorm here.cmd\" inside it"
	say "  Mac or Linux:   sh \"<the folder>/install-here.sh\""
	say ""
	note "Anything changed here from now on does not move. Once the new computer"
	note "is working, uninstall EmberStorm here: sh install.sh --uninstall"
	if [ ! -d "$dest/library" ]; then
		say ""
		important "Your media was not included. Copy it to the new computer yourself:"
		important "    $library"
	fi
	say ""
	exit 0
}

# write_move_launchers puts a one-click installer for each kind of computer
# inside the move folder, each installing from the folder it sits in.
write_move_launchers() {
	cat > "$1/install-here.sh" <<'EOF'
#!/bin/sh
# Installs EmberStorm on this computer from the move folder this file is in.
here=$(cd "$(dirname "$0")" && pwd)
# A fresh private file, not a fixed /tmp name another user could plant first.
t=$(mktemp) || exit 1
trap 'rm -f "$t"' EXIT
curl -fsSL https://raw.githubusercontent.com/GabrielHollberg/emberstorm/main/install.sh -o "$t" &&
	sh "$t" --import "$here"
EOF
	chmod +x "$1/install-here.sh"
	# A .cmd wants CRLF, or cmd.exe mishandles it.
	printf '%s\r\n' \
		'@echo off' \
		'rem Installs EmberStorm on this computer from the move folder this file is in.' \
		'setlocal' \
		'set "HERE=%~dp0"' \
		'set "HERE=%HERE:~0,-1%"' \
		'set "PS=%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe"' \
		'set "SOUNDSTORM_SETUP_URL=https://raw.githubusercontent.com/GabrielHollberg/emberstorm/main/install.ps1"' \
		'start "" /min "%PS%" -NoProfile -WindowStyle Hidden -ExecutionPolicy Bypass -Command "$ProgressPreference = '"'"'SilentlyContinue'"'"'; [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12; $f = Join-Path $env:TEMP '"'"'soundstorm-install.ps1'"'"'; try { Invoke-WebRequest -UseBasicParsing -Uri $env:SOUNDSTORM_SETUP_URL -OutFile $f } catch { Add-Type -AssemblyName System.Windows.Forms; [void][System.Windows.Forms.MessageBox]::Show('"'"'EmberStorm could not download its installer. Check the internet connection and try again.'"'"', '"'"'EmberStorm Setup'"'"'); exit 1 }; $env:SOUNDSTORM_WINDOW = '"'"'1'"'"'; $q = [char]34; Start-Process -FilePath (Join-Path $PSHOME '"'"'powershell.exe'"'"') -WindowStyle Hidden -ArgumentList ('"'"'-NoProfile -ExecutionPolicy Bypass -STA -File '"'"' + $q + $f + $q + '"'"' -Import '"'"' + $q + $env:HERE + $q)"' \
		'exit /b 0' > "$1/Install EmberStorm here.cmd"
}

# check_move_folder says whether $1 is a move folder this version can read.
check_move_folder() {
	DIE_HEADING='EmberStorm could not be moved here.'
	[ -f "$1/manifest.txt" ] || die "$1 is not a EmberStorm move folder: it has no manifest.txt.

Point --import at the EmberStorm-move folder made by --export."
	# A move written on Windows may carry carriage returns; either is fine.
	tr -d '\r' < "$1/manifest.txt" | grep -q '^format=1$' || die "$1 was made by a newer EmberStorm. Update this installer and try again."
}

# import_volumes restores the data volumes from a move folder. Refused where
# EmberStorm already has data: an import is for a computer it is new to, and
# writing over accounts that exist here is not something to do by accident.
import_volumes() {
	if docker volume inspect "${PROJECT}_soundstorm-state" >/dev/null 2>&1; then
		die "This computer already has EmberStorm data, so importing would write over it.

Uninstall EmberStorm here first (sh install.sh --uninstall; your media is
kept), then import again."
	fi
	for tarfile in "$1"/volumes/*.tar; do
		[ -f "$tarfile" ] || continue
		v=$(basename "$tarfile" .tar)
		case " $MOVE_VOLUMES " in
			*" $v "*) ;;
			*) note "skipping $v, which this version does not know"; continue ;;
		esac
		note "$v"
		# Labeled as compose labels its own, so compose adopts the volume
		# rather than warning that something else made it.
		docker volume create --label "com.docker.compose.project=$PROJECT" \
			--label "com.docker.compose.volume=$v" "${PROJECT}_$v" >/dev/null ||
			die "Could not create the $v volume."
		docker run --rm -v "${PROJECT}_$v:/to" -v "$1/volumes:/from:ro" "$MOVE_IMAGE" \
			sh -c "cd /to && tar -xf /from/$v.tar" ||
			die "Could not restore $v from the move folder."
	done
}

# import_settings carries the install's own settings across: the setup code,
# secrets, https and remote access choices. What belongs to the old computer's
# network is left out and worked out here.
import_settings() {
	[ -f "$1/settings.env" ] || return 0
	tr -d '\r' < "$1/settings.env" | grep -Ev "$MOVE_LOCAL" | while IFS= read -r line; do
		key=${line%%=*}
		# Only the install's own choices and secrets: a move folder could
		# otherwise set the image that runs, or the services it trusts.
		case "$key" in
			SOUNDSTORM_SETUP_CODE|SOUNDSTORM_REMOTE_ACCESS|SOUNDSTORM_TAILSCALE_AUTHKEY|SOUNDSTORM_TAILSCALE_HOSTNAME|SOUNDSTORM_TLS|SOUNDSTORM_AUDIOMUSE_DB_PASSWORD|SOUNDSTORM_IMMICH_DB_PASSWORD|SOUNDSTORM_STORYTELLER_SECRET|SOUNDSTORM_LOG_LEVEL|TS_AUTHKEY)
				set_env "$key" "${line#*=}" ;;
		esac
	done
}

# --- go ---------------------------------------------------------------------

# A loop rather than a case on $1: --https has to be able to arrive alongside
# nothing else and still reach the install below, which a single case cannot do.
HTTPS=''
TAILSCALE=''
REMOTE=''
AUTHKEY=''
LIBRARY=''
IMPORT=''
NO_LIBRARY=''
EXPORT=''
MOVE_IMAGE='alpine:3'
while [ $# -gt 0 ]; do
	case "$1" in
		--uninstall|-u)
			need_docker
			compose_cmd
			uninstall
			;;
		--https)
			HTTPS='on'
			;;
		--no-https)
			HTTPS='off'
			;;
		--tailscale)
			TAILSCALE='on'
			;;
		--no-tailscale)
			TAILSCALE='off'
			;;
		--remote)
			REMOTE='on'
			;;
		--no-remote)
			REMOTE='off'
			;;
		--auth-key)
			shift
			AUTHKEY="${1:-}"
			[ -n "$AUTHKEY" ] || die "--auth-key needs a key after it"
			note "a key given on the command line can be read by other accounts here; TS_AUTHKEY=... sh install.sh --tailscale keeps it to this one"
			;;
		--library)
			shift
			LIBRARY="${1:-}"
			[ -n "$LIBRARY" ] || die "--library needs a folder after it"
			;;
		--export)
			shift
			EXPORT="${1:-}"
			[ -n "$EXPORT" ] || die "--export needs a folder after it, where the move is written"
			;;
		--no-library)
			NO_LIBRARY=1
			;;
		--import)
			shift
			IMPORT="${1:-}"
			[ -n "$IMPORT" ] || die "--import needs the EmberStorm-move folder after it"
			IMPORT=$(cd "$IMPORT" 2>/dev/null && pwd) || die "Could not open ${1:-that folder}."
			check_move_folder "$IMPORT"
			;;
		--help|-h)
			say "EmberStorm installer"
			say ""
			say "  (no arguments)   install, or update an existing install"
			say "  --https          real https for a soundstorm.dev name (the default)"
			say "  --no-https       plain http only"
			say "  --tailscale      also reach it away from home, over a tailnet"
			say "  --no-tailscale   stop doing that"
			say "  --remote         reach it from anywhere over the internet (off by default)"
			say "  --no-remote      keep it to the home network"
			say "  --uninstall      remove it, keeping your media library"
			say "  --library PATH   keep the media library somewhere else"
			say "  --export PATH    pack it up in PATH to move to another computer"
			say "                   (--no-library leaves the media out)"
			say "  --import PATH    install it here from a move folder"
			say ""
			exit 0
			;;
		*)
			die "Unknown option: $1

Run with --help to see what this accepts."
			;;
	esac
	shift
done

# Packing up needs nothing below: no install, no update.
if [ -n "$EXPORT" ]; then
	need_docker
	compose_cmd
	export_move "$EXPORT"
fi


say ""
say "${BOLD}EmberStorm${OFF} - one login and one search box over your media library"
say ""
# Said before anything happens: "is it still working?" is the question for the
# next ten minutes, and the honest answer on a first install is "for a while".
if [ -f "$DIR/docker-compose.yml" ]; then
	say "Updating EmberStorm. Your library, accounts and settings are kept."
else
	say "This sets everything up by itself. The first time takes about 10 to 30"
	say "minutes, mostly downloading."
fi
say "${YELLOW}${BOLD}Leave this running.${OFF}${YELLOW} It says when it is finished and what to do next.${OFF}"

step "Checking Docker"
need_docker
compose_cmd
note "$(docker --version)"

# existing_install prints where EmberStorm is already installed, if it is.
#
# The compose project name is fixed, so a second install in a second folder
# does not get its own stack - it adopts the first one, ends up pointing at a
# library folder nobody put anything in, and looks broken for no visible
# reason. Compose records the directory it was launched from, so we can just
# ask.
existing_install() {
	docker inspect soundstorm \
		--format '{{index .Config.Labels "com.docker.compose.project.working_dir"}}' \
		2>/dev/null || true
}

step "Setting up $DIR"

previous=$(existing_install)
if [ -n "$previous" ] && [ "$previous" != "$DIR" ] && [ ! -f "$DIR/docker-compose.yml" ] && [ "${SOUNDSTORM_FORCE:-}" != "1" ]; then
	die "EmberStorm is already installed in another folder:

  $previous

Installing it here as well would not give you a second copy - both folders
would drive the same containers, and this one would point at an empty library.

To use the existing install:   cd \"$previous\"
To move it here instead:       cd \"$previous\" && $COMPOSE down, then run this again
To install anyway:             SOUNDSTORM_FORCE=1 sh install.sh"
fi

mkdir -p "$DIR"
cd "$DIR"

if [ -n "$IMPORT" ] && [ -f docker-compose.yml ]; then
	die "EmberStorm is already installed in $DIR, so importing would write over it.

Uninstall it first (sh install.sh --uninstall; your media is kept), then
import again."
fi

if [ -f docker-compose.yml ] && [ "${SOUNDSTORM_FORCE:-}" != "1" ]; then
	note "already installed here - upgrading it instead"
	UPGRADE=1
	# The current compose file too: an update used to keep the one it was
	# installed with, so containers added since and the hardening in it
	# (private networks, pinned versions) never reached an install (the
	# blind security review). A failed download keeps the one there.
	if (fetch "$COMPOSE_URL" docker-compose.yml.new) 2>/dev/null && [ -s docker-compose.yml.new ]; then
		mv docker-compose.yml.new docker-compose.yml
		note "downloaded the current docker-compose.yml"
	else
		rm -f docker-compose.yml.new
		note "could not download the current docker-compose.yml - keeping the one here"
	fi
else
	UPGRADE=0
	fetch "$COMPOSE_URL" docker-compose.yml.new
	# Only replace a working file once the download has actually succeeded.
	mv docker-compose.yml.new docker-compose.yml
	note "downloaded docker-compose.yml"
fi


if [ "$UPGRADE" = "0" ]; then
	step "Choosing a port"
	pick_port
	if [ "$PORT" != "$FIRST_PORT" ]; then
		note "$FIRST_PORT was busy, using $PORT"
	else
		note "using $PORT"
	fi
	# Compose reads .env from beside the compose file, so these stick for every
	# later `docker compose up` without anyone having to remember them.
	#
	# The LAN address is recorded even though TLS is off, because it is needed
	# the moment somebody turns TLS on and cannot be worked out then: the
	# server is in a container and sees only the container's own addresses.
	# Better written now, by the machine that knows.
	#
	# Created 0600 before anything is written into it: .env holds the setup
	# code and, with --tailscale, a reusable auth key, and the default umask
	# would otherwise leave it world-readable for any other local account.
	( umask 077; : > .env )
	printf 'SOUNDSTORM_PORT=%s\n' "$PORT" > .env
	lan=$(lan_address)
	if [ -n "$lan" ]; then
		printf 'SOUNDSTORM_TLS_HOSTS=%s\n' "$lan" >> .env
	fi
else
	PORT=$(get_env SOUNDSTORM_PORT)
	# A port asked for when running it (SOUNDSTORM_PORT=9000 sh install.sh,
	# as the busy-port message says) is the port from now on: kept only in
	# the environment, compose published that one while this waited on the
	# old, and the next update went back to it (a review, 2026-10-09).
	if [ -n "${SOUNDSTORM_PORT:-}" ] && [ "$SOUNDSTORM_PORT" != "$PORT" ]; then
		PORT=$SOUNDSTORM_PORT
		set_env SOUNDSTORM_PORT "$PORT"
		note "using port $PORT, as asked"
	fi
	[ -n "$PORT" ] || PORT="$FIRST_PORT"
fi

# An install from before .env was made private kept it readable by everyone,
# and an update that rewrites no line of it never changed that.
chmod 600 .env 2>/dev/null || true

# A move brings the install's own settings - setup code, secrets, choices -
# on top of the fresh file, before anything below reads them.
if [ -n "$IMPORT" ]; then
	import_settings "$IMPORT"
fi

# This computer on another network than when it was installed - or given a new
# address by its router: the secure name follows it.
if [ "$UPGRADE" = "1" ] && refresh_lan_address; then
	note "this computer's network address has changed; EmberStorm will use the new one"
fi

# After the port, so that on a fresh install this amends the file just written
# rather than being overwritten by it.
#
# The first sign-up needs a setup code, so that whoever reaches the port
# before the owner does - from the internet, once it faces it - cannot claim
# the server. It goes into the addresses printed and opened below, so nobody
# installing has to type it. Kept once written: a second run must hand out
# the code the server already has.
SETUP_CODE=$(get_env SOUNDSTORM_SETUP_CODE)
if [ -z "$SETUP_CODE" ]; then
	SETUP_CODE=$(od -An -N10 -tx1 /dev/urandom | tr -d ' 
')
	set_env SOUNDSTORM_SETUP_CODE "$SETUP_CODE"
fi
# Only a fresh install shows it: an existing one already has its owner.
SETUP_QS=''
if [ "$UPGRADE" != "1" ] && [ -z "$IMPORT" ]; then
	SETUP_QS="/?setup=$SETUP_CODE"
fi

# Auto is the default: a real certificate for a <id>.home.soundstorm.dev name,
# with plain http still answering on the same port. Written for a fresh
# install and for an existing one that never chose - an absent line meant
# "off" only because off was the default then. A choice somebody made (off,
# self-signed, file) is left alone.
if [ "$HTTPS" = "on" ] || { [ -z "$HTTPS" ] && [ -z "$(get_env SOUNDSTORM_TLS)" ]; }; then
	# Auto points its name at the LAN address, and only this machine can say
	# what that is - the server sees the container's address, not the host's.
	# An install from before .env carried that line is topped up here.
	if [ -z "$(get_env SOUNDSTORM_TLS_HOSTS)" ]; then
		lan=$(lan_address)
		if [ -n "$lan" ]; then
			set_env SOUNDSTORM_TLS_HOSTS "$lan"
		fi
	fi
	set_env SOUNDSTORM_TLS auto
	if [ "$HTTPS" = "on" ] || [ "$UPGRADE" = "1" ]; then
		note "turning on https"
	fi
elif [ "$HTTPS" = "off" ]; then
	set_env SOUNDSTORM_TLS off
	note "turning https off"
fi
TLS_MODE=$(get_env SOUNDSTORM_TLS)
SCHEME=$(installed_scheme)

# Remote access is off unless --remote is given, and it can be turned on later
# from inside the app - so this only writes when the flag is present, and any
# existing choice (the app's, or a previous run's) is left alone otherwise. It
# needs auto https for a real certificate; the in-app toggle is hidden and the
# server refuses the change without one, so the flag just seeds that default.
if [ "$REMOTE" = "on" ]; then
	set_env SOUNDSTORM_REMOTE_ACCESS on
	note "turning on access from the internet"
	if [ "$TLS_MODE" != "auto" ]; then
		note "reaching it from the internet needs https on"
	fi
elif [ "$REMOTE" = "off" ]; then
	set_env SOUNDSTORM_REMOTE_ACCESS off
	note "keeping it to the home network"
fi

# The router address, for opening the port automatically when remote access is
# on (NAT-PMP/PCP). Written whether or not remote access is on yet, for the same
# reason as the LAN address: by the time somebody turns it on from inside the
# app, nothing on the host is running to work it out. An existing value stands,
# unless it is no longer this machine's router (refresh_gateway).
refresh_gateway || true
if [ -z "$(get_env SOUNDSTORM_GATEWAY)" ]; then
	gw=$(gateway_address)
	if [ -n "$gw" ]; then
		set_env SOUNDSTORM_GATEWAY "$gw"
	fi
fi

# The router's UPnP URL, the fallback for routers that do not speak NAT-PMP/PCP.
# Same reasoning as the gateway: discovered on the host, an existing value left
# alone, best effort.
if [ -z "$(get_env SOUNDSTORM_UPNP_URL)" ]; then
	upnp=$(upnp_url)
	# Anything on the network can answer SSDP, so only a plain http URL on
	# the router's own address is kept - one word, no spaces or line breaks.
	gw=$(get_env SOUNDSTORM_GATEWAY)
	case "$upnp" in
	*" "* | *"	"* | *"
"* | *"$(printf '\r')"*) upnp= ;;
	http://"$gw":* | http://"$gw"/*) [ -n "$gw" ] || upnp= ;;
	*) upnp= ;;
	esac
	if [ -n "$upnp" ]; then
		set_env SOUNDSTORM_UPNP_URL "$upnp"
	fi
fi

install_address_watch

# Where the library lives: beside the install unless --library says otherwise,
# which is how it goes on an external drive. Compose mounts every shelf from
# the same setting, so they all follow. Existing media is never moved - that is
# not an operation a script should risk failing halfway through - so somebody
# moving it is told where the old files are.
if [ -n "$LIBRARY" ]; then
	mkdir -p "$LIBRARY" || die "Could not use $LIBRARY for the library. Check the drive is mounted, then run this again."
	full=$(cd "$LIBRARY" && pwd)
	# Docker's settings file cuts a value at " #" and stops at a starting
	# quote: the shelves were mounted from an empty folder beside the media
	# (a review, 2026-10-09).
	case "$full" in
	*" #"* | \'* | \"*)
		die "The folder $full cannot be used for the library: its name has \" #\" in it, or starts with a quote. Rename it, then run this again."
		;;
	esac
	previous=$(library_path)
	set_env SOUNDSTORM_LIBRARY_PATH "$full"
	set_env SOUNDSTORM_LIBRARY_HINT "$full"
	note "keeping the library in $full"
	if [ "$previous" != "$full" ] && [ -n "$(find "$previous" -type f ! -name README.txt 2>/dev/null | head -1)" ]; then
		say ""
		say "Your existing media is still in $previous."
		note "To bring it across: stop EmberStorm ($COMPOSE down), move the folders"
		note "inside it into $full, then start it again ($COMPOSE up -d)."
		say ""
	fi
fi
LIBRARY_DIR=$(library_path)

# The library folders are made here rather than left to Docker. A bind mount to
# a path that does not exist is created by the daemon as root, which on Linux
# leaves somebody unable to copy files into their own media folder.
#
# And they are opened to everyone (0777, the folder and its shelves only, not
# what is in them): EmberStorm runs as its own user, 10001, so a folder made by
# whoever ran this, 0755, is one it cannot add to - every upload, the starter
# library and a new shelf of its own all failed on Linux. Docker Desktop on a
# Mac or Windows maps ownership away, which is why it never showed there.
# Measured on a native Docker host: 10001 could not write a 0755 folder owned
# by uid 1000, and could once it was 0777. See the note on 0777 library
# folders in CLAUDE.md.
for shelf in music movies tv audiobooks ebooks documents pictures; do
	mkdir -p "$LIBRARY_DIR/$shelf"
	chmod 0777 "$LIBRARY_DIR/$shelf" 2>/dev/null ||
		note "could not open $LIBRARY_DIR/$shelf to EmberStorm; adding files there may fail"
done
# The library itself is sticky (1777): its shelves stay open to EmberStorm,
# but nobody else on the machine can rename one away and put a link in its
# place, which every backend would then read and write through (the twelfth
# security pass).
chmod 1777 "$LIBRARY_DIR" 2>/dev/null || true

# A move: the media first, then the data, and only then does anything start -
# a backend started on empty volumes would set itself up afresh.
if [ -n "$IMPORT" ]; then
	if [ -d "$IMPORT/library" ]; then
		step "Copying your media from the move folder"
		note "this is the long part"
		cp -R "$IMPORT/library/." "$LIBRARY_DIR/" || die "Could not copy the media into $LIBRARY_DIR."
	fi
	step "Restoring accounts, settings and the media servers' data"
	import_volumes "$IMPORT"
fi

# Remote access. A separate decision from --https: one is about the wifi at
# home, the other about being away from it.
if [ "$TAILSCALE" = "on" ]; then
	# Best in the environment (TS_AUTHKEY=tskey-... sh install.sh --tailscale):
	# a command line any account on the machine can read, the environment only
	# this one (the twelfth security pass).
	key="${AUTHKEY:-${TS_AUTHKEY:-}}"
	[ -n "$key" ] || key=$(get_env SOUNDSTORM_TAILSCALE_AUTHKEY)
	if [ -z "$key" ]; then
		say ""
		say "Reaching EmberStorm from outside the house needs a Tailscale account."
		say "It is free for personal use and takes about two minutes."
		say ""
		say "  1. Sign up at https://tailscale.com"
		say "  2. Open the admin console, Settings, then Keys"
		say "  3. Generate an auth key and copy it"
		say ""
		# Only reachable from a flag somebody typed. Piped into sh, the
		# script reads the terminal itself.
		if [ -t 0 ]; then
			printf '  Paste the auth key here: '
			read -r key
		elif [ -r /dev/tty ] && ( : </dev/tty ) 2>/dev/null; then
			printf '  Paste the auth key here: '
			read -r key </dev/tty
		fi
	fi
	if [ -z "$key" ]; then
		die "No auth key, so there is nothing to connect with.

EmberStorm is installed and working on this network either way. Run this again
with --tailscale when you have a key, or give it directly:

  TS_AUTHKEY=tskey-... sh install.sh --tailscale"
	fi
	set_env SOUNDSTORM_TAILSCALE_AUTHKEY "$key"
	write_serve_config
	note "Tailscale will be started with EmberStorm"
elif [ "$TAILSCALE" = "off" ]; then
	set_env SOUNDSTORM_TAILSCALE_AUTHKEY ""
	note "turning off remote access"
fi

# Whether the profile is wanted at all, which outlives this run: somebody who
# set it up in January should still get it after an upgrade in June.
PROFILE=""
if [ "$TAILSCALE" != "off" ] && [ -n "$(get_env SOUNDSTORM_TAILSCALE_AUTHKEY)" ]; then
	PROFILE="--profile tailscale"
fi

# Always, whether or not Tailscale is wanted: compose bind-mounts this file,
# and Docker answers a missing bind-mount source by creating a directory there.
[ -f tailscale-serve.json ] || write_serve_config

# Before the long download, while somebody is still watching: whether the
# computer may sleep, and whether there is room for what comes next.
if [ "$UPGRADE" != "1" ]; then
	keep_awake
	check_room
fi

if [ "$UPGRADE" = "1" ]; then
	step "Checking for newer versions"
else
	step "Downloading the media servers"
	note "That is everything - the rest needs nothing from you."
	note "about 8GB the first time - the photo and film servers are most of it"
fi
if ! $COMPOSE $PROFILE pull; then
	die "Could not download the images. That is almost always the network.
Check your connection and run this again - anything already downloaded is kept."
fi

step "Starting"
# Captured rather than streamed, so a failure can be read and explained instead
# of leaving somebody to interpret a Docker error.
if ! out=$($COMPOSE $PROFILE up -d 2>&1); then
	printf '%s\n' "$out" >&2
	# The pre-flight port check above cannot always tell - a machine with no
	# nc, ss or lsof has nothing to ask - so this is where a busy port is
	# usually discovered, and it deserves a better answer than the logs.
	if printf '%s' "$out" | grep -qiE 'already allocated|address already in use|forbidden by its access permissions'; then
		die "Port $PORT is already being used by something else.

Pick another one and run this again:

  SOUNDSTORM_PORT=9000 sh install.sh"
	fi
	die "The containers would not start. This usually says why:

  cd $DIR && $COMPOSE logs"
fi

step "Waiting for EmberStorm to answer"
URL="$SCHEME://localhost:$PORT"
waited=0
note "Waiting for EmberStorm to answer - usually under a minute."
until health_ok "$URL/healthz"; do
	waited=$((waited + 2))
	# Two minutes with nothing on screen is when somebody decides it has hung.
	if [ $((waited % 20)) -eq 0 ]; then
		note "still starting... (${waited}s). This is normal the first time."
	fi
	if [ "$waited" -gt 120 ]; then
		die "EmberStorm started but never answered on $URL.

  cd $DIR && $COMPOSE logs soundstorm"
	fi
	sleep 2
done

# Run again after a first install stopped part way (a download that failed),
# it counts as an update, but nobody has made an account yet: it still shows
# the setup code, or the first screen asked for a code never shown (a review,
# 2026-10-09). The server says whether anybody has.
if [ "$UPGRADE" = "1" ] && [ -z "$SETUP_QS" ] && [ -z "$IMPORT" ]; then
	if command -v curl >/dev/null 2>&1; then
		session=$(curl -fsSk "$URL/api/session" 2>/dev/null) || session=''
	elif command -v wget >/dev/null 2>&1; then
		session=$(wget -q --no-check-certificate -O - "$URL/api/session" 2>/dev/null) || session=''
	else
		session=''
	fi
	# The server indents its JSON ("hasAccount": false).
	case "$session" in
	*'"hasAccount": false'* | *'"hasAccount":false'*) SETUP_QS="/?setup=$SETUP_CODE" ;;
	esac
fi

say ""
if [ "$UPGRADE" = "1" ] && [ -z "$SETUP_QS" ]; then
	say "${GREEN}${BOLD}Up to date.${OFF} EmberStorm is running at ${BOLD}$URL${OFF}."
else
	say "${GREEN}${BOLD}Ready.${OFF} Open ${BOLD}$URL$SETUP_QS${OFF} and create your account."
fi
say ""
say "Your media goes in ${BOLD}$LIBRARY_DIR${OFF}:"
say "    music/  movies/  tv/  audiobooks/  ebooks/  documents/  pictures/"
say ""
note "The media servers are still setting themselves up in the background."
note "The app shows you when each one is ready - that takes a minute or two."
say ""
lan=$(lan_address)
mdns=$(mdns_name)
secure=''
if [ "$TLS_MODE" = "auto" ]; then
	step "Getting a secure address"
	secure=$(secure_address)
fi
if [ -n "$secure" ]; then
	# The real certificate is in: no warning on any device, and a phone can
	# install the app from this address.
	say "On your phone, TV or another computer on this network:"
	say ""
	say "    ${BOLD}$secure$SETUP_QS${OFF}"
	if [ -n "$lan" ]; then
		note "http://$lan:$PORT    (if your router refuses the name)"
	fi
	say ""
	note "Same account either way."
	say ""
elif [ -n "$lan" ] || [ -n "$mdns" ]; then
	say "On your phone, TV or another computer on this network:"
	say ""
	if [ -n "$mdns" ]; then
		say "    ${BOLD}$SCHEME://$mdns:$PORT$SETUP_QS${OFF}"
		if [ -n "$lan" ]; then
			note "$SCHEME://$lan:$PORT    (if the name does not work)"
		fi
	else
		say "    ${BOLD}$SCHEME://$lan:$PORT$SETUP_QS${OFF}"
	fi
	say ""
	note "Same account. Open the port on the firewall if nothing loads."
	if [ "$TLS_MODE" = "auto" ]; then
		note "EmberStorm is still getting its secure address, and moves there"
		note "by itself when it has one."
	fi
	say ""
fi

if [ "$TLS_MODE" = "self-signed" ]; then
	# Said plainly and up front, because the alternative is somebody deciding
	# their own install is broken or unsafe. No outside authority can vouch for
	# a certificate covering an address like 192.168.0.50, so the warning
	# cannot be avoided without a real domain name - but it is fixable per
	# device, and that fix is the useful half of this message.
	say "The first visit shows a certificate warning on every device."
	note "Expected: the certificate was made by this machine, and nobody"
	note "outside can vouch for a home network address. Choose Advanced,"
	note "then continue."
	say ""
	ca_host="${mdns:-${lan:-localhost}}"
	say "To stop it asking, open this on each device and install the"
	say "certificate it downloads:"
	say ""
	say "    ${BOLD}https://$ca_host:$PORT/ca.crt${OFF}"
	say ""
	note "Run this again with --no-https to go back to plain http."
	say ""
elif [ "$TLS_MODE" = "off" ]; then
	note "Run this again with --https to encrypt the connection."
	say ""
fi

# When it runs: on Linux Docker is a system service, so EmberStorm comes back
# when the computer starts, signed in or not; on a Mac, Docker Desktop starts
# when somebody signs in.
if [ "$UPGRADE" != "1" ]; then
	case "$(uname -s)" in
	Darwin)
		note "EmberStorm starts when you sign in to this Mac. To have it come back by"
		note "itself after a restart: System Settings, Users & Groups, Automatically"
		note "log in as (not offered while FileVault is on)."
		;;
	*)
		note "EmberStorm starts by itself whenever this computer starts - no sign-in"
		note "needed."
		laptop=0
		for b in /sys/class/power_supply/BAT*; do [ -e "$b" ] && laptop=1; done
		if [ "$laptop" = "0" ]; then
			note "To have it switch itself back on after a power cut, turn on \"Restore on"
			note "AC power loss\" (or \"Power on after power failure\") in its BIOS setup."
		fi
		;;
	esac
	say ""
fi

say "${DIM}stop:    cd $DIR && $COMPOSE down${OFF}"
say "${DIM}logs:    cd $DIR && $COMPOSE logs -f${OFF}"
say "${DIM}upgrade: run this installer again${OFF}"
say ""

# The one thing to act on goes last, so it is what is on screen when the output
# stops - with the setup code in full. It used to appear only inside the
# addresses above, so anybody who opened a plain address instead was asked for
# a code this never showed them on its own.
if [ -n "$SETUP_QS" ]; then
	frame "NEXT: create your account" \
		"Open EmberStorm and choose a username and password on the first" \
		"screen - that is your account." \
		"" \
		"*    $URL" \
		"" \
		"If the page asks for a SETUP CODE, type this one:" \
		"" \
		"*        $(format_code "$SETUP_CODE")" \
		"" \
		"Capitals and dashes do not matter. It is also saved in:" \
		"    $DIR/.env"
fi
# For the Mac setup app (mac/), which shows these in its own window.
if [ -n "${EMBERSTORM_RESULT:-}" ]; then
	{
		printf 'URL=%s\n' "$URL"
		printf 'SETUP=%s\n' "$SETUP_QS"
		printf 'CODE=%s\n' "$(format_code "$SETUP_CODE")"
		printf 'SECURE=%s\n' "$secure"
		printf 'LAN=%s\n' "${lan:+$SCHEME://$lan:$PORT}"
		printf 'LIBRARY=%s\n' "$LIBRARY_DIR"
		printf 'DIR=%s\n' "$DIR"
		printf 'UPGRADE=%s\n' "$UPGRADE"
	} >"$EMBERSTORM_RESULT" 2>/dev/null || true
fi
open_browser "$URL$SETUP_QS"
