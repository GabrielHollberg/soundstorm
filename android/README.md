# SoundStorm for Android

A native shell around the server's own web app, with background playback and
lock-screen controls - the Android counterpart of `ios/`. Why it is built this
way is in `CLAUDE.md` under "The Android app".

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
wherever you opened it).

Android Studio opens this folder as it is. Building needs a JDK 17 or newer and
the Android SDK (platform 36); `local.properties` says where the SDK is and is
not committed.

## The things to check on a real phone

- Play a song, lock the phone, wait a few minutes: does it keep playing and move
  on to the next song?
- Do the lock screen, the notification, headphone buttons and a car's controls
  pause, skip and seek?
- Does a film go full screen?

## Testing on the emulator

```sh
# an emulator phone with enough memory - the default 2GB is killed as the page loads
avdmanager create avd -n soundstorm-test -k "system-images;android-37.0;google_apis_playstore_ps16k;x86_64" -d pixel_7
emulator -avd soundstorm-test -memory 4096 -no-audio

./gradlew installDebug
# 10.0.2.2 is the computer the emulator runs on
adb shell am start -n dev.soundstorm.app/.MainActivity --es serverURL http://10.0.2.2:8099
```

The debug build's web view can be inspected at `chrome://inspect`, or driven by
Playwright with `adb forward tcp:9333 localabstract:webview_devtools_remote_<pid>`
and `chromium.connectOverCDP('http://localhost:9333')`.

`adb shell cmd media_session dispatch pause` (or `play`, `next`, `previous`)
presses a media button, as headphones or the lock screen would.

## Icons

The launcher icon and the connect screen's logo are drawn by
`scripts/make-icons.py`; the notification icon is `res/drawable/ic_stat_soundstorm.xml`,
from the same shapes. Change the script, not the PNGs.
