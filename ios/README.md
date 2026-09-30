# SoundStorm for iPhone

A native shell around the server's own web app, with background audio. Why
it is built this way, and what comes next, is in `CLAUDE.md` under
"The iPhone app".

## Running it on your iPhone

1. Open `ios/SoundStorm.xcodeproj` in Xcode (26 or newer).
2. Select the **SoundStorm** target → **Signing & Capabilities** → choose your
   **Team**. A free Apple ID works for your own phone; the App Store needs the
   paid Apple Developer Program.
3. Plug the iPhone in (or pair it over Wi-Fi), pick it as the run destination,
   and press **Run**. The first time, the phone asks you to turn on
   **Developer Mode** (Settings → Privacy & Security) and to trust the
   developer (Settings → General → VPN & Device Management).
4. Enter your server's address - the one you open in a browser.

The bundle identifier is `dev.soundstorm.app`. If Xcode says it is taken,
change it to something of your own under the target's **General** tab.

## The thing to check first

Play a song, lock the phone, and wait a minute:

- Does it keep playing?
- Does the lock screen show the song, its cover, and working
  play/pause/skip buttons?
- Do headphone buttons and Control Center work?

Stage two (native audio) is shaped by these answers. The simulator cannot
answer them.

## Apple TV

The **SoundStormTV** scheme builds the Apple TV app (same project, same
bundle id for Universal Purchase). Run it on the Apple TV simulator from
Xcode, or on a real Apple TV paired with Xcode (Settings → Remotes and
Devices → Remote App and Devices on the TV). In a debug build,
`-autoplay YES` as a launch argument plays the first recently played song,
for a simulator with no remote:

```sh
xcrun simctl launch booted dev.soundstorm.app -serverURL https://yourname.home.soundstorm.dev:8099 -autoplay YES
```

## TestFlight

The app record exists in App Store Connect (bundle `dev.soundstorm.app`,
team `LZA2K5LLDS`), so an upload is two commands on the Mac, signed in to
Xcode:

```sh
xcodebuild archive -project ios/SoundStorm.xcodeproj -scheme SoundStorm \
  -configuration Release -destination 'generic/platform=iOS' \
  -archivePath /tmp/SoundStorm.xcarchive -allowProvisioningUpdates
xcodebuild -exportArchive -archivePath /tmp/SoundStorm.xcarchive \
  -exportOptionsPlist ios/scripts/export-testflight.plist \
  -exportPath /tmp/SoundStorm-export -allowProvisioningUpdates
```

The build number is raised by the upload itself (`manageAppVersionAndBuildNumber`),
so nobody edits it by hand. Apple takes 10-30 minutes to process a build
before it shows in the TestFlight app. Uploads are internal-testing only
until the app goes to review.

What blocked the first upload, in order, so it is quicker next time:
"Failed to Use Accounts" (Xcode's sign-in had lapsed - sign in again under
Xcode → Settings → Accounts), "PLA Update available" (accept the new
Program License Agreement at developer.apple.com/account), then
`missingApp` (no app record yet; Xcode's Organizer created it - the command
line cannot).

## Tests

`SoundStormUITests` drives the app against a throwaway server:

```sh
SOUNDSTORM_LISTEN=127.0.0.1:8080 SOUNDSTORM_STATE_DIR=/tmp/ss-state \
SOUNDSTORM_LIBRARY_DIR=/tmp/ss-library SOUNDSTORM_STARTER_LIBRARY=false \
SOUNDSTORM_SETUP_CODE=test-setup-code go run ./cmd/soundstorm

xcodebuild test -project ios/SoundStorm.xcodeproj -scheme SoundStorm \
  -destination 'platform=iOS Simulator,name=iPhone 17'
```

The tests skip themselves when nothing answers at `localhost:8080`.

## Icons

`AppIcon` and `Logo` are drawn by `scripts/make-icons.py` with the web app's
icons. Change the script, not the PNGs.
