# SoundStorm for Android

A native shell around the server's own web app - the Android counterpart of
`ios/`, and the app for Google TV, Android TV and Fire TV as well. Why it is
built this way is in `CLAUDE.md` under "The Android app" and "TV apps".

What is native, and what is still the page:

- **Songs from the server are played natively**, by Media3's ExoPlayer in a
  media session service (`AudioService`, driven through `NativeAudio`). The
  page keeps deciding everything - the queue, the looks, the lyrics - and its
  `<audio>` element is a stand-in that passes play, pause, seek, volume and
  speed to the player and reports its state back. Android then treats the app
  as a music player: it keeps playing with the screen off, pauses for an alarm
  or a call and plays on after, and gives the lock screen, notification,
  headphones and a car their controls. The next ten songs are handed over
  ahead, so the music carries on even if Android ends the page. Downloads
  (`blob:` addresses) and crossfade still play in the page.
- **Photo backup** (`PhotoBackup.kt`): WorkManager jobs that send the phone's
  camera roll to `PUT /api/photos/backup`, checking each batch with
  `POST /api/photos/backup/check` first, so a reinstall sends nothing twice.
  The page's Settings → Your photos holds the switch and its options (Wi-Fi
  only, videos, only while charging); not on a TV. One backup runs at a time,
  in the foreground with a quiet notification where Android allows (an ordinary
  job is stopped at ten minutes), and Settings shows the file being sent.
  Signing out, or another account appearing, turns it off.
- **Several saved servers** (`ServerAddress.kt`): the connect screen lists
  **Your servers**, the latest used first; tap to switch, long-press to rename
  or remove. Each keeps its own sign-in.
- **TV**: the manifest adds `LEANBACK_LAUNCHER` and a banner, and marks leanback
  and a touchscreen as not required, so phones install it as before. On a TV the
  web view's user agent gains `SoundStormTV/1` and the page switches to its
  remote-control mode.

## Running it on your phone

The quickest way is the debug build, sideloaded:

1. On the phone: Settings → About phone → tap **Build number** seven times, then
   Settings → System → Developer options → turn on **USB debugging**.
2. Plug the phone in and allow the computer when it asks.
3. From this folder:

   ```sh
   ./gradlew installDebug        # gradlew.bat on Windows
   ```

4. Open SoundStorm and enter your server's address - the one you open in a
   browser.

Or build `app/build/outputs/apk/debug/app-debug.apk` with `./gradlew
assembleDebug` and open it on the phone (it will ask to allow installs from
wherever you opened it). A TV takes the same APK over the network:
`adb connect <tv-address>`, then `adb install -r app-debug.apk`.

Android Studio opens this folder as it is. Building needs a JDK 17 or newer and
the Android SDK (platform 36); `local.properties` says where the SDK is and is
not committed.

## Releasing a build

Builds are published from `main`, from the PC:

1. Raise `versionCode` (and `versionName`) in `app/build.gradle.kts`, so a
   phone installs over the last one.
2. `./gradlew assembleDebug`.
3. Publish `app-debug.apk` as a GitHub **pre-release** named
   `android-<version>`. Never mark it Latest: Latest is the server's installer,
   which the README's download link follows.

The published build is the debug build type, signed with the debug key so each
installs over the last, but with `isDebuggable = false` - a published APK that
is debuggable hands its cookies to anybody with adb. A release signing key is
still to come.

## The things to check on a real phone

- Play a song, lock the phone, wait a few minutes: does it keep playing and move
  on to the next song?
- Do the lock screen, the notification, headphone buttons and a car's controls
  pause, skip and seek?
- Set an alarm (or take a call) while music plays: does it pause, then play on
  after?
- Leave the app for another for a while: is the music still playing, and does
  the app take it over again when reopened?
- Does a film go full screen?
- Turn on photo backup in Settings → Your photos: do the photos arrive in your
  folder on the server, and does a second run send nothing again?
- Add a second server under Change server: does switching back keep you signed
  in?

## Testing on the emulator

```sh
# an emulator phone with enough memory - the default 2GB is killed as the page loads
avdmanager create avd -n soundstorm-test -k "system-images;android-37.0;google_apis_playstore_ps16k;x86_64" -d pixel_7
emulator -avd soundstorm-test -memory 4096 -no-audio

./gradlew installDebug
```

Then open the app and enter `http://10.0.2.2:8099` - 10.0.2.2 is the computer
the emulator runs on. (A launch extra that set the address was removed: any app
on the phone could have used it to point SoundStorm at its own server.)

The web view can be inspected at `chrome://inspect`, or driven by Playwright
with `adb forward tcp:9333 localabstract:webview_devtools_remote_<pid>` and
`chromium.connectOverCDP('http://localhost:9333')` - but only from a build with
`isDebuggable = true`, which is how WebView debugging is switched on. Flip it
locally for that and never commit or publish it.

On the developer's own install (the one with training switched on), Settings →
Playback on this device in the app has **Sound cut out? Send a report**, which
sends what the native player saw to the server; no other install shows it.

`adb shell cmd media_session dispatch pause` (or `play`, `next`, `previous`)
presses a media button, as headphones or the lock screen would.

## Icons

The launcher icon, the TV banner and the connect screen's logo are drawn by
`scripts/make-icons.py`; the notification icon is `res/drawable/ic_stat_soundstorm.xml`,
from the same shapes. Change the script, not the PNGs.
