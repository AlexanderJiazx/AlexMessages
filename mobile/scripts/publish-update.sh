#!/usr/bin/env bash
# Publishes the mobile app's current JavaScript as an over-the-air update.
#
#   mobile/scripts/publish-update.sh
#
# It exports the app with `npx expo export` and copies the export over SSH to
# the Pi's data/updates/<app version>/<unix seconds>/. Every phone whose app
# version (app.json → expo.version) matches downloads it at its next start
# and runs it from the start after that. Only JavaScript and assets travel
# this way. After a native change (a new native module or config plugin, a
# change to app.json's native settings), raise expo.version,
# android.versionCode and ios.buildNumber, and build and hand out a new app
# instead (DEPLOY.md, "Mobile app updates").
#
# Before it exports anything it refuses to publish to the Pi while mobile/ or
# shared/ has uncommitted or untracked files, and it runs the shared
# typecheck and unit tests and the mobile typecheck. Each update directory
# holds commit.txt, the commit the update was built from. The upload is
# checked with SHA-256 on the Pi before the update gets its final name.
#
# OTA_LOCAL=<data dir> publishes into the data/ directory of a local server
# instead of the Pi (for testing; a dirty tree is allowed then and the commit
# gets the suffix -dirty). PI and PI_PORT override the SSH target.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=lib-upload.sh
source "$here/lib-upload.sh"
cd "$here/.."
# An update for the phones always carries the server URL and update URL from
# app.json: EXPO_PUBLIC_* values are built into the bundle.
if [[ -z "${OTA_LOCAL:-}" ]]; then
  unset EXPO_PUBLIC_AM_SERVER AM_UPDATES_URL
fi

dirty=$(git -C .. status --porcelain -- mobile shared)
if [[ -n "$dirty" && -z "${OTA_LOCAL:-}" ]]; then
  echo "The update was NOT published: these files are not committed." >&2
  echo "$dirty" >&2
  echo "Commit them, then run this script again." >&2
  exit 1
fi
commit=$(git -C .. rev-parse HEAD)
if [[ -n "$dirty" ]]; then
  commit="$commit-dirty"
fi

(cd ../shared && npm run typecheck && npm test)
npx --no -- tsc --noEmit

version=$(node -p 'require("./app.json").expo.version')
stamp=$(date +%s)
out=$(mktemp -d)
trap 'rm -rf "$out"' EXIT

npx expo export --platform android --platform ios --output-dir "$out/update"
npx expo config --type public --json > "$out/update/expoConfig.json"
echo "$commit" > "$out/update/commit.txt"

# The update appears under its final name only once every file is in place,
# so the server never serves half an upload.
if [[ -n "${OTA_LOCAL:-}" ]]; then
  dest="$OTA_LOCAL/updates/$version"
  mkdir -p "$dest"
  cp -R "$out/update" "$dest/.$stamp"
  mv "$dest/.$stamp" "$dest/$stamp"
  echo "Published update $stamp for app version $version (commit $commit) into $dest."
  exit 0
fi

pi="${PI:-alexander@35.226.215.209}"
COPYFILE_DISABLE=1 tar --no-xattrs -C "$out/update" -cf "$out/update.tar" .
upload_verified "$out/update.tar" "alexmessage/data/updates/$version/.$stamp.tar" \
  "d=alexmessage/data/updates/$version; mkdir -p \$d/.$stamp; tar -C \$d/.$stamp -xf \$d/.$stamp.tar; rm \$d/.$stamp.tar; mv \$d/.$stamp \$d/$stamp"
echo "Published update $stamp for app version $version (commit $commit) to $pi."
