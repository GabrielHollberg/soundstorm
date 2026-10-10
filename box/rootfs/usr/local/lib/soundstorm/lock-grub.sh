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

sed -i 's/^CLASS="--class gnu-linux --class gnu --class os"$/CLASS="--class gnu-linux --class gnu --class os --unrestricted"/' /etc/grub.d/10_linux
grep -q -- '--unrestricted"$' /etc/grub.d/10_linux || { echo "lock-grub: the normal entry could not be left open" >&2; exit 1; }

printf 'GRUB_DISABLE_RECOVERY=true\n' > /etc/default/grub.d/90_emberstorm.cfg
update-grub
grep -q '^set superusers=' /boot/grub/grub.cfg || { echo "lock-grub: grub.cfg has no lock" >&2; exit 1; }
echo "lock-grub: start-up menu locked"
