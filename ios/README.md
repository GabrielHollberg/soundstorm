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

The **SoundStormTV** scheme builds the Apple TV app, a native SwiftUI app in
the same project (tvOS has no web view), with the same bundle id for Universal
Purchase. It talks to the same `/api` the web page uses and follows the rules
settled for Android TV. What it has: Home, Music (mixes, radio and moods,
playlists, artists, albums, Now Playing with the twelve visualizers and
lyrics), Watch (films and shows, subtitles and audio languages, Up next),
Books (audiobooks with chapters, ebooks and PDFs in a native reader, Read
Along), Photos (the roll, On this day, people and places), each tab with the
account's own categories in its order. Settings and anything that changes the
library stay on the web.

Run it on the Apple TV simulator from Xcode, or on a real Apple TV paired with
Xcode (Settings → Remotes and Devices → Remote App and Devices on the TV).

A debug build takes launch arguments, for a simulator with no remote:

| Argument | Does |
| --- | --- |
| `-serverURL <address>` | the server, skipping the connect screen |
| `-username <name> -password <pw>` | signs in |
| `-tab <tab>`, `-category <value>` | opens a tab (`home`, `music`, `watch`, `books`, `photos`) and one of its categories |
| `-autoplay YES` | plays the first recently played song |
| `-autostation YES` | tunes the first radio station |
| `-autovideo <id>` | plays a film or episode; `-autosubtitle <n>`, `-autoaudio <n>` choose its tracks |
| `-autobook YES` | plays the first audiobook |
| `-autoread <id>`, `-autoturn <n>` | opens a book and turns n pages |
| `-autopair YES` | opens the first Read Along book |
| `-autophoto YES`, `-autostep YES` | opens the first photo, then steps and pauses the music |

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

### The Apple TV build

The TV target's Release is **signed by hand**: automatic signing archives with
a development profile first, which needs a registered Apple TV. It uses the
"Apple Distribution" certificate and the profile named **SoundStorm tvOS App
Store** (tvOS, App Store Connect, `dev.soundstorm.app`), made on
developer.apple.com; when it expires, make a new one under the same name.

```sh
xcodebuild archive -project ios/SoundStorm.xcodeproj -scheme SoundStormTV   -configuration Release -destination 'generic/platform=tvOS'   -archivePath /tmp/SoundStormTV.xcarchive
xcodebuild -exportArchive -archivePath /tmp/SoundStormTV.xcarchive   -exportOptionsPlist ios/scripts/export-testflight-tv.plist   -exportPath /tmp/SoundStormTV-export
```

An unsigned archive (`CODE_SIGNING_ALLOWED=NO`) does not work: the Organizer
refuses it with "No Team Found in Archive".

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
