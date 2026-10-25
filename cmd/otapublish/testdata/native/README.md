# Native artifact fixtures

Real tool output for the parsers in `axml.go` and `plist.go` and the D10
artifact checks in `artifacts.go`. Each file was made once, on macOS, with
the Android SDK and Xcode tools; nothing here is built during `go test`.

| File | What it is |
|---|---|
| `aapt2-AndroidManifest.xml` | The source manifest of `aapt2.apk`: the meta-data an expo-updates release build carries. The certificate is written with `&#xA;` line breaks, as Expo's config plugin (xml2js / xmlbuilder) writes them. |
| `aapt2.apk` | `aapt2-AndroidManifest.xml` compiled and linked by aapt2 36.0.0 against `platforms/android-36/android.jar`, with `assets/fingerprint`, `assets/app.manifest` and `assets/index.android.bundle`. Its manifest strings are UTF-16 and its resource strings UTF-8, like a Gradle release build. `EXPO_RUNTIME_VERSION` is the reference `@string/expo_runtime_version` (`file:fingerprint`). Unsigned. |
| `aapt2-signed.apk` | `aapt2.apk` after `zipalign -p -f 4` and `apksigner sign --v4-signing-enabled false` (build-tools 36.0.0) with a throwaway keystore that `keytool` made for this and that was then deleted. It carries v2 and v3 signatures. |
| `fixture.plist` | An XML property list with every value type the reader handles (dictionaries, arrays, strings with non-ASCII text, booleans, integers of every width, a negative integer, a real, a date, data). |
| `fixture.bplist` | `fixture.plist` converted with `plutil -convert binary1`, the format Xcode writes into an app's `Info.plist` and `Expo.plist`. |

The commands, in a scratch directory holding the manifest, `res/values/strings.xml`
(`app_name` and `expo_runtime_version`) and `assets/`:

```bash
BT=~/Library/Android/sdk/build-tools/36.0.0
$BT/aapt2 compile --dir res -o res.zip
$BT/aapt2 link -o aapt2.apk -I ~/Library/Android/sdk/platforms/android-36/android.jar --manifest AndroidManifest.xml -A assets res.zip
$BT/zipalign -p -f 4 aapt2.apk aligned.apk
$BT/apksigner sign --ks <throwaway keystore> --ks-key-alias alexmessages --v4-signing-enabled false --out aapt2-signed.apk aligned.apk
plutil -convert binary1 -o fixture.bplist fixture.plist
```

`TestSampleBuilds` also reads a real Gradle release APK and a real Xcode IPA of build 1 (made before expo-updates)
when they exist in the gitignored `mobile/dist/` and `mobile/build/`, and
skips otherwise.
