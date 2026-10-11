"""The box release key on a YubiKey (5.7 or newer), on the PC it is plugged into.

A release - the images every box updates to - is trusted by a box only when
signed by a key in box/release.pub. Whoever holds that key can update every
box, so it is made inside a YubiKey and never leaves it: signing needs the
YubiKey, its PIN and a touch. Two YubiKeys each make their own key - the
release key, and a backup kept somewhere else - and boxes trust both
(release.pub, the release key's line first), so losing one does not leave
every box unable to update.

    py box/yubikey.py info                 which YubiKey, and its key if made
    py box/yubikey.py setup                make its key (wipes its PIV keys)
    py box/yubikey.py sign MANIFEST        write MANIFEST.sig, as boxes check it

`setup` and `sign` ask for the PIN themselves: run them in a terminal, where
nothing else sees what is typed. The key is Ed25519 in the PIV signature
slot (9c) with the PIN and a touch asked for every signature; the YubiKey
signs the manifest's bytes themselves (PureEdDSA), exactly what a box checks
with ed25519.Verify. Needs: py -m pip install --user yubikey-manager
"""

import base64
import getpass
import json
import os
import secrets
import sys

from cryptography.hazmat.primitives.serialization import Encoding, PublicFormat
from ykman.device import list_all_devices
from ykman.piv import pivman_set_mgm_key
from yubikit.core.smartcard import SmartCardConnection
from yubikit.piv import (
    DEFAULT_MANAGEMENT_KEY,
    KEY_TYPE,
    MANAGEMENT_KEY_TYPE,
    PIN_POLICY,
    SLOT,
    TOUCH_POLICY,
    PivSession,
)

SLOT_USED = SLOT.SIGNATURE
HERE = os.path.dirname(os.path.abspath(__file__))
RELEASE_PUB = os.path.join(HERE, "release.pub")


def die(msg):
    print(msg, file=sys.stderr)
    sys.exit(1)


def one_yubikey():
    devices = list_all_devices()
    if not devices:
        die("No YubiKey is plugged in.")
    if len(devices) > 1:
        die("More than one YubiKey is plugged in: leave only the one to use.")
    device, info = devices[0]
    if info.version < (5, 7, 0):
        die(f"This YubiKey's firmware is {info.version}: Ed25519 needs 5.7 or newer.")
    return device, info


def raw_public(key):
    return base64.b64encode(key.public_bytes(Encoding.Raw, PublicFormat.Raw)).decode()


def slot_key(piv):
    """The public key in the signature slot, base64, or None."""
    try:
        meta = piv.get_slot_metadata(SLOT_USED)
    except Exception:
        return None
    if meta.key_type != KEY_TYPE.ED25519:
        return None
    return raw_public(meta.public_key)


def trusted():
    if not os.path.exists(RELEASE_PUB):
        return []
    with open(RELEASE_PUB, encoding="utf-8") as f:
        return [l.strip() for l in f if l.strip() and not l.startswith("#")]


def info():
    device, inf = one_yubikey()
    with device.open_connection(SmartCardConnection) as conn:
        piv = PivSession(conn)
        pin = piv.get_pin_metadata()
        key = slot_key(piv)
    print(f"YubiKey serial {inf.serial}, firmware {inf.version}")
    print(f"PIN: {'still the factory default - run setup' if pin.default_value else 'set'}, "
          f"{pin.attempts_remaining} tries left")
    if key:
        where = trusted()
        role = ("the release key" if where and where[0] == key else
                "the backup key" if key in where else "not in box/release.pub")
        print(f"Release key: {key}  ({role})")
    else:
        print("No release key on it yet.")


def ask_twice(what, lo, hi):
    while True:
        a = getpass.getpass(f"New {what} ({lo}-{hi} digits or letters): ")
        if not lo <= len(a) <= hi:
            print(f"  It must be {lo} to {hi} long.")
            continue
        if a in ("123456", "12345678", "000000", "00000000"):
            print("  That one is too easy to guess.")
            continue
        if getpass.getpass(f"Type the {what} again: ") != a:
            print("  They did not match.")
            continue
        return a


def setup():
    device, inf = one_yubikey()
    print(f"YubiKey serial {inf.serial}.")
    print("This wipes the YubiKey's PIV keys (not its other uses: logins with")
    print("FIDO, OTP and the rest are untouched), sets a PIN and PUK of your own,")
    print("and makes the release key inside it. Write the PIN and the PUK down")
    print("and keep them apart from the YubiKey: three wrong PINs lock it, and")
    print("the PUK unlocks it; without both, this YubiKey's key is lost for good.")
    if input(f"Type the serial ({inf.serial}) to go on: ").strip() != str(inf.serial):
        die("Stopped: nothing changed.")
    pin = ask_twice("PIN", 6, 8)
    puk = ask_twice("PUK", 6, 8)
    with device.open_connection(SmartCardConnection) as conn:
        piv = PivSession(conn)
        piv.reset()
        piv.change_pin("123456", pin)
        piv.change_puk("12345678", puk)
        piv.verify_pin(pin)
        # The default management key, then a random one kept on the
        # YubiKey behind the PIN, so nothing about it is known outside.
        piv.authenticate(DEFAULT_MANAGEMENT_KEY)
        pivman_set_mgm_key(piv, secrets.token_bytes(24), MANAGEMENT_KEY_TYPE.AES192,
                           touch=False, store_on_device=True)
        print("Making the key - touch the YubiKey's gold contact if it blinks.")
        pub = piv.generate_key(SLOT_USED, KEY_TYPE.ED25519,
                               pin_policy=PIN_POLICY.ALWAYS, touch_policy=TOUCH_POLICY.ALWAYS)
    key = raw_public(pub)
    out = os.path.join(HERE, f"release-{inf.serial}.pub")
    with open(out, "w", encoding="utf-8") as f:
        f.write(key + "\n")
    print(f"Done. Its public key (safe to share): {key}")
    print(f"Also written to {out}.")


def sign(manifest):
    with open(manifest, "rb") as f:
        data = f.read()
    try:
        json.loads(data)
    except ValueError:
        die(f"{manifest} is not a manifest (not JSON).")
    device, inf = one_yubikey()
    with device.open_connection(SmartCardConnection) as conn:
        piv = PivSession(conn)
        key = slot_key(piv)
        if not key:
            die("This YubiKey has no release key: run setup first.")
        where = trusted()
        if key not in where:
            die(f"This YubiKey's key is not in box/release.pub, so no box would take what it signs.")
        print(f"Signing with YubiKey {inf.serial} "
              f"({'the release key' if where[0] == key else 'the backup key'}).")
        piv.verify_pin(getpass.getpass("PIN: "))
        print("Touch the YubiKey's gold contact now.")
        sig = piv.sign(SLOT_USED, KEY_TYPE.ED25519, data, None)
        meta = piv.get_slot_metadata(SLOT_USED)
    # Checked as a box checks it, before anything is written.
    meta.public_key.verify(sig, data)
    with open(manifest + ".sig", "w", encoding="ascii", newline="\n") as f:
        f.write(base64.b64encode(sig).decode() + "\n")
    print(f"Signed: {manifest}.sig")


def main():
    args = sys.argv[1:]
    if args == ["info"]:
        info()
    elif args == ["setup"]:
        setup()
    elif len(args) == 2 and args[0] == "sign":
        sign(args[1])
    else:
        print(__doc__)
        sys.exit(2)


if __name__ == "__main__":
    main()
