#!/bin/sh
# Locks the box's start-up menu, run once by box/build.sh inside the image
# (and then removed). Somebody with a keyboard at the box could otherwise
# edit the start-up line - init=/bin/sh - and have root on the box and every
# account on its drive (the box's blind security review). The normal entry
# still starts with no password (--unrestricted); editing an entry, the
# command line, the advanced entries and the firmware entry need the
# password of a user nobody knows: 32 random bytes made here and never kept.
# Recovery mode goes too. Putting a box right is the USB stick's job, never
# this menu's.
set -eu

pw=$(head -c 32 /dev/urandom | base64)
hash=$(printf '%s\n%s\n' "$pw" "$pw" | grub-mkpasswd-pbkdf2 2>/dev/null |
	sed -n 's/.* is \(grub\.pbkdf2\.[^ ]*\)$/\1/p')
unset pw
[ -n "$hash" ] || { echo "lock-grub: no password hash made" >&2; exit 1; }

cat > /etc/grub.d/01_emberstorm_lock <<EOF
#!/bin/sh
cat <<'END'
set superusers="emberstorm"
password_pbkdf2 emberstorm $hash
END
EOF
chmod 755 /etc/grub.d/01_emberstorm_lock

# The normal entry left open without editing the package's 10_linux - an
# update would replace the edit and the box would then ask a password at
# every start that nobody knows (the box's blind review). The package's own
# script is moved aside by dpkg (updates go there too) and run through a
# filter adding --unrestricted to the normal entry, which carries the class
# "os"; the advanced entries stay locked.
mkdir -p /usr/share/emberstorm
if ! dpkg-divert --list /etc/grub.d/10_linux | grep -q emberstorm; then
	dpkg-divert --package emberstorm --add --rename \
		--divert /usr/share/emberstorm/10_linux.grub /etc/grub.d/10_linux
fi
cat > /etc/grub.d/10_linux <<'EOF'
#!/bin/sh
# EmberStorm: grub's own 10_linux (diverted to /usr/share/emberstorm by dpkg,
# kept up to date by its updates), with the normal entry starting freely.
/bin/sh /usr/share/emberstorm/10_linux.grub "$@" |
	sed 's/^\(menuentry .* --class os\)\( \$menuentry_id_option\)/\1 --unrestricted\2/'
EOF
chmod 755 /etc/grub.d/10_linux

printf 'GRUB_DISABLE_RECOVERY=true\n' > /etc/default/grub.d/90_emberstorm.cfg
update-grub
grep -q '^set superusers=' /boot/grub/grub.cfg || { echo "lock-grub: grub.cfg has no lock" >&2; exit 1; }
grep -q '^menuentry .*--unrestricted' /boot/grub/grub.cfg || { echo "lock-grub: the normal entry is not left open" >&2; exit 1; }
echo "lock-grub: start-up menu locked"
