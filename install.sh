#!/bin/sh
# SoundStorm installer for macOS and Linux.
#
#   curl -fsSL https://raw.githubusercontent.com/GabrielHollberg/soundstorm/main/install.sh | sh
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
#   --library PATH   keep the media library somewhere else - an external drive
#
# Written for /bin/sh rather than bash, because a stock Debian's /bin/sh is dash
# and an installer that only works under bash is an installer that fails on the
# exact cheap home server this is aimed at.

set -eu

REPO="${SOUNDSTORM_REPO:-GabrielHollberg/soundstorm}"
BRANCH="${SOUNDSTORM_BRANCH:-main}"
COMPOSE_URL="${SOUNDSTORM_COMPOSE_URL:-https://raw.githubusercontent.com/$REPO/$BRANCH/docker-compose.yml}"
DIR="${SOUNDSTORM_DIR:-$PWD/soundstorm}"
FIRST_PORT="${SOUNDSTORM_PORT:-8099}"

# --- saying things ----------------------------------------------------------

# Colour only when stdout is a terminal. Piping this into a log should not
# produce escape codes, and `curl | sh` is a very normal way to run it.
if [ -t 1 ]; then
	BOLD=$(printf '\033[1m'); DIM=$(printf '\033[2m')
	RED=$(printf '\033[31m'); GREEN=$(printf '\033[32m'); OFF=$(printf '\033[0m')
else
	BOLD=''; DIM=''; RED=''; GREEN=''; OFF=''
fi

say()  { printf '%s\n' "$*"; }
step() { printf '%s==>%s %s\n' "$BOLD" "$OFF" "$*"; }
note() { printf '    %s%s%s\n' "$DIM" "$*" "$OFF"; }

# die prints why it stopped and, more importantly, what to do about it. An
# installer that says "error: 1" has failed twice.
die() {
	printf '\n%sSoundStorm could not start.%s\n\n%s\n\n' "$RED$BOLD" "$OFF" "$1" >&2
	exit 1
}

# --- the things that have to be true ----------------------------------------

need_docker() {
	if ! command -v docker >/dev/null 2>&1; then
		die "Docker is not installed.

Docker runs the media servers SoundStorm sits on top of, so it is the one thing
you have to install yourself. It is free for personal use.

  macOS and Windows   https://www.docker.com/products/docker-desktop/
  Linux               https://docs.docker.com/engine/install/

Install it, then run this again."
	fi

	# Installed is not running, and this is the single most common failure:
	# somebody installs Docker Desktop, never opens it, and gets a wall of
	# socket errors that say nothing about which application to launch.
	if ! docker info >/dev/null 2>&1; then
		die "Docker is installed but not running.

  macOS and Windows   open Docker Desktop and wait for it to say Running
  Linux               sudo systemctl start docker

Then run this again."
	fi
}

# compose_cmd sets COMPOSE to whichever form of compose exists. v2 is a docker
# subcommand; v1 was a separate binary and is still what some distributions
# package.
compose_cmd() {
	if docker compose version >/dev/null 2>&1; then
		COMPOSE="docker compose"
	elif command -v docker-compose >/dev/null 2>&1; then
		COMPOSE="docker-compose"
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
get_env() {
	[ -f .env ] || return 0
	sed -n "s/^[[:space:]]*$1=//p" .env | head -n 1
}

set_env() {
	if [ -f .env ]; then
		# umask 077 so the rewrite keeps .env private - mv takes the new
		# file's permissions, and .env holds the setup code and auth key.
		( umask 077; grep -v "^[[:space:]]*$1=" .env > .env.new ) || true
		mv .env.new .env
	fi
	printf '%s=%s\n' "$1" "$2" >> .env
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
# SoundStorm over plain http on this machine, so no certificate is involved in
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
# SoundStorm over the internal compose network, where SoundStorm is speaking
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
	say "${BOLD}Removing SoundStorm${OFF}"
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
				note "passwords SoundStorm made on the media servers"
			else
				note "could not save a copy; carrying on with the uninstall"
			fi

			step "Stopping it and removing its data"
			note "accounts and the servers own settings go; your media does not"
			# down -v takes the named volumes with it - SoundStorm accounts,
			# and Jellyfin and Navidrome own databases. The library is a bind
			# mount from the folder and is untouched by this.
			$COMPOSE down -v >/dev/null 2>&1 || true
		else
			note "docker is not available, so the containers were left alone"
		fi
		step "Cleaning up"
		# soundstorm-backup.json is deliberately not in this list.
		rm -f "$DIR/docker-compose.yml" "$DIR/.env"
	else
		note "nothing installed in $DIR"
	fi

	say ""
	say "${GREEN}${BOLD}Done.${OFF} SoundStorm is gone."
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

# --- go ---------------------------------------------------------------------

# A loop rather than a case on $1: --https has to be able to arrive alongside
# nothing else and still reach the install below, which a single case cannot do.
HTTPS=''
TAILSCALE=''
REMOTE=''
AUTHKEY=''
LIBRARY=''
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
			;;
		--library)
			shift
			LIBRARY="${1:-}"
			[ -n "$LIBRARY" ] || die "--library needs a folder after it"
			;;
		--help|-h)
			say "SoundStorm installer"
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


say ""
say "${BOLD}SoundStorm${OFF} - one login and one search box over your media library"
say ""

step "Checking Docker"
need_docker
compose_cmd
note "$(docker --version)"

# existing_install prints where SoundStorm is already installed, if it is.
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
if [ -n "$previous" ] && [ "$previous" != "$DIR" ] && [ ! -f "$DIR/docker-compose.yml" ]; then
	die "SoundStorm is already installed in another folder:

  $previous

Installing it here as well would not give you a second copy - both folders
would drive the same containers, and this one would point at an empty library.

To use the existing install:   cd \"$previous\"
To move it here instead:       cd \"$previous\" && $COMPOSE down, then run this again
To install anyway:             SOUNDSTORM_FORCE=1 sh install.sh"
fi

mkdir -p "$DIR"
cd "$DIR"

if [ -f docker-compose.yml ] && [ "${SOUNDSTORM_FORCE:-}" != "1" ]; then
	note "already installed here - upgrading it instead"
	UPGRADE=1
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
	[ -n "$PORT" ] || PORT="$FIRST_PORT"
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
if [ "$UPGRADE" != "1" ]; then
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
# app, nothing on the host is running to work it out. An existing value stands.
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
	if [ -n "$upnp" ]; then
		set_env SOUNDSTORM_UPNP_URL "$upnp"
	fi
fi

# Where the library lives: beside the install unless --library says otherwise,
# which is how it goes on an external drive. Compose mounts every shelf from
# the same setting, so they all follow. Existing media is never moved - that is
# not an operation a script should risk failing halfway through - so somebody
# moving it is told where the old files are.
if [ -n "$LIBRARY" ]; then
	mkdir -p "$LIBRARY" || die "Could not use $LIBRARY for the library. Check the drive is mounted, then run this again."
	full=$(cd "$LIBRARY" && pwd)
	previous=$(library_path)
	set_env SOUNDSTORM_LIBRARY_PATH "$full"
	set_env SOUNDSTORM_LIBRARY_HINT "$full"
	note "keeping the library in $full"
	if [ "$previous" != "$full" ] && [ -n "$(find "$previous" -type f ! -name README.txt 2>/dev/null | head -1)" ]; then
		say ""
		say "Your existing media is still in $previous."
		note "To bring it across: stop SoundStorm ($COMPOSE down), move the folders"
		note "inside it into $full, then start it again ($COMPOSE up -d)."
		say ""
	fi
fi
LIBRARY_DIR=$(library_path)

# The library folders are made here rather than left to Docker. A bind mount to
# a path that does not exist is created by the daemon as root, which on Linux
# leaves somebody unable to copy files into their own media folder.
for shelf in music movies tv audiobooks ebooks documents pictures; do
	mkdir -p "$LIBRARY_DIR/$shelf"
done

# Remote access. A separate decision from --https: one is about the wifi at
# home, the other about being away from it.
if [ "$TAILSCALE" = "on" ]; then
	key="$AUTHKEY"
	[ -n "$key" ] || key=$(get_env SOUNDSTORM_TAILSCALE_AUTHKEY)
	if [ -z "$key" ]; then
		say ""
		say "Reaching SoundStorm from outside the house needs a Tailscale account."
		say "It is free for personal use and takes about two minutes."
		say ""
		say "  1. Sign up at https://tailscale.com"
		say "  2. Open the admin console, Settings, then Keys"
		say "  3. Generate an auth key and copy it"
		say ""
		# Only reachable from a flag somebody typed. Piping this script into
		# sh leaves no terminal to read from, hence --auth-key.
		if [ -t 0 ]; then
			printf '  Paste the auth key here: '
			read -r key
		fi
	fi
	if [ -z "$key" ]; then
		die "No auth key, so there is nothing to connect with.

SoundStorm is installed and working on this network either way. Run this again
with --tailscale when you have a key, or pass it directly:

  sh install.sh --tailscale --auth-key tskey-..."
	fi
	set_env SOUNDSTORM_TAILSCALE_AUTHKEY "$key"
	write_serve_config
	note "Tailscale will be started with SoundStorm"
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

if [ "$UPGRADE" = "1" ]; then
	step "Checking for newer versions"
else
	step "Downloading the media servers"
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

step "Waiting for SoundStorm to answer"
URL="$SCHEME://localhost:$PORT"
waited=0
until health_ok "$URL/healthz"; do
	waited=$((waited + 2))
	if [ "$waited" -gt 120 ]; then
		die "SoundStorm started but never answered on $URL.

  cd $DIR && $COMPOSE logs soundstorm"
	fi
	sleep 2
done

say ""
if [ "$UPGRADE" = "1" ]; then
	say "${GREEN}${BOLD}Up to date.${OFF} SoundStorm is running at ${BOLD}$URL${OFF}."
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
		note "SoundStorm is still getting its secure address, and moves there"
		note "by itself when it has one."
	fi
	say ""
fi

if [ "$TLS_MODE" = "self-signed" ]; then
	# Said plainly and up front, because the alternative is somebody deciding
	# their own install is broken or unsafe. No outside authority can vouch for
	# a certificate covering an address like 192.168.0.19, so the warning
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

say "${DIM}stop:    cd $DIR && $COMPOSE down${OFF}"
say "${DIM}logs:    cd $DIR && $COMPOSE logs -f${OFF}"
say "${DIM}upgrade: run this installer again${OFF}"
say ""

if [ -n "$SETUP_QS" ]; then
	note "The first address you open creates the owner account; the code at"
	note "the end of it is what lets it. After that, plain addresses work."
	say ""
fi
open_browser "$URL$SETUP_QS"
