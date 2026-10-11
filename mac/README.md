# EmberStorm for the Mac

One app that sets EmberStorm up on a Mac the first time it is opened, and
starts and opens it every time after - the Windows setup window and its
desktop icon in one, so nobody types a command into Terminal.

It drives the Mac and Linux installer (`install.sh` at the repository's root)
rather than doing the installing itself: the newest script on main, fetched
at its commit, run with no terminal. Its questions are asked first, in the
window (where the media goes, keeping the Mac awake), and passed on as
settings (`--library`, `EMBERSTORM_KEEP_AWAKE`); the Mac's password is asked
in a macOS window (`SUDO_ASKPASS`); what it ends with comes back through
`EMBERSTORM_RESULT` for the finished page. Running `install.sh` in Terminal is
unchanged by any of this.

Installed, opening the app starts Docker Desktop if it is not running, starts
EmberStorm, waits for it to answer and opens it in the browser with
`?here=server`. Update and Uninstall are on its window and in its menu.

```sh
xcodebuild -project mac/EmberStorm.xcodeproj -scheme EmberStorm build
```

In a debug build, `-script /path/to/install.sh` runs a checkout's installer
instead of main's, `-page questions|finished` opens straight on a page, and
`-snapshot out.png` draws the window to a picture (nothing else may capture
the screen without Screen Recording permission).

The icon is an Icon Composer document (`EmberStorm/AppIcon.icon`), drawn by
`scripts/make-icons.py`: macOS 26 put any older-style icon on a grey plate.

To hand it out it must be signed with a **Developer ID Application**
certificate and notarized, or macOS refuses to open it. The account holder
makes that certificate (developer.apple.com, Certificates); then:

```sh
xcodebuild archive -project mac/EmberStorm.xcodeproj -scheme EmberStorm \
  -archivePath /tmp/EmberStorm-mac.xcarchive -allowProvisioningUpdates
xcodebuild -exportArchive -archivePath /tmp/EmberStorm-mac.xcarchive \
  -exportOptionsPlist mac/export-developer-id.plist -exportPath /tmp/EmberStorm-mac
xcrun notarytool wait <id> ...   # the export uploads it; then
xcodebuild -exportNotarizedApp -archivePath /tmp/EmberStorm-mac.xcarchive -exportPath /tmp/EmberStorm-mac-out
NOTARY_KEY=... NOTARY_KEY_ID=... NOTARY_ISSUER=... \
  sh mac/make-dmg.sh /tmp/EmberStorm-mac-out/EmberStorm.app ~/Downloads/EmberStorm.dmg
```

It is handed out as a disk image with a link to Applications, dragged across
as any Mac app is. It does not move itself: moving or deleting its own copy in
Downloads made macOS ask "EmberStorm would like to access files in your
Downloads folder" (the second run, 2026-10-10).
