#!/usr/bin/env bash
# Builds the native app for one platform and copies it to the Pi's data/app/,
# where https://messages.alexanderjia.com/app offers it for download.
#
#   mobile/scripts/publish-app.sh android   # AlexMessages.apk
#   mobile/scripts/publish-app.sh ios       # AlexMessages.ipa, unsigned: 万能签 signs it
#
# Before a new build, raise expo.version, android.versionCode and
# ios.buildNumber in app.json (native build N is version 1.N.0). OTA updates
# reach only the apps whose version they were published under, so the old
# build never runs JavaScript that needs the new native code.
#
# The APK is signed with the debug keystore of Expo's Android template (the
# same file after every prebuild), so it installs over the old app and keeps
# its data.
#
# APP_LOCAL=<data dir> copies the file into the data/ directory of a local
# server instead of the Pi (for testing). PI and PI_PORT override the SSH
# target.
set -euo pipefail

cd "$(dirname "$0")/.."
platform="${1:-}"
if [[ "$platform" != android && "$platform" != ios ]]; then
  echo "usage: $0 android|ios" >&2
  exit 2
fi

# A build for the phones always uses the server URL and update URL from
# app.json. CocoaPods needs a UTF-8 locale. EXPO_NO_GIT_STATUS: prebuild
# does not stop to ask about uncommitted changes (android/ and ios/ are
# generated, never edited by hand).
unset EXPO_PUBLIC_AM_SERVER AM_UPDATES_URL
export LANG=en_US.UTF-8 LC_ALL=en_US.UTF-8 EXPO_NO_GIT_STATUS=1

out=$(mktemp -d)
trap 'rm -rf "$out"' EXIT

if [[ "$platform" == android ]]; then
  # Gradle needs a JDK and the Android SDK; Android Studio ships both.
  if [[ ! -x "${JAVA_HOME:-}/bin/java" ]]; then
    export JAVA_HOME="/Applications/Android Studio.app/Contents/jbr/Contents/Home"
  fi
  export ANDROID_HOME="${ANDROID_HOME:-$HOME/Library/Android/sdk}"
  npx expo prebuild --platform android --clean
  # The template's 512 MB metaspace runs out in the release lint step.
  (cd android && ./gradlew -Dorg.gradle.jvmargs="-Xmx4g -XX:MaxMetaspaceSize=1g" assembleRelease)
  file=AlexMessages.apk
  cp android/app/build/outputs/apk/release/app-release.apk "$out/$file"
else
  npx expo prebuild --platform ios --clean
  xcodebuild -workspace ios/AlexMessages.xcworkspace -scheme AlexMessages \
    -configuration Release -destination generic/platform=iOS \
    -archivePath "$out/AlexMessages.xcarchive" CODE_SIGNING_ALLOWED=NO archive
  mkdir "$out/Payload"
  cp -R "$out/AlexMessages.xcarchive/Products/Applications/AlexMessages.app" "$out/Payload/"
  file=AlexMessages.ipa
  (cd "$out" && zip -qry "$file" Payload)
fi

# The file appears under its final name only once it is complete.
if [[ -n "${APP_LOCAL:-}" ]]; then
  mkdir -p "$APP_LOCAL/app"
  cp "$out/$file" "$APP_LOCAL/app/.$file"
  mv "$APP_LOCAL/app/.$file" "$APP_LOCAL/app/$file"
  echo "Copied $file into $APP_LOCAL/app."
  exit 0
fi

pi="${PI:-alexander@35.226.215.209}"
ssh -p "${PI_PORT:-2222}" "$pi" \
  "set -e; d=alexmessage/data/app; mkdir -p \$d; cat > \$d/.$file; mv \$d/.$file \$d/$file" < "$out/$file"
echo "Published $file to $pi."
