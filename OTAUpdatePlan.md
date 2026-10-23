# Over-the-air (OTA) updates for the Alex Messages mobile app: plan

Date: 2026-10-22. Branch: `react`. Paths are relative to the repo root unless they start with `/` or `~`.

## 0. Status

**OTA updates do NOT work today.**
- `expo-updates` is not a dependency of `mobile/`.
- `mobile/app.json` has no `updates` block and no `runtimeVersion`.
- The server has no update endpoint.
- The prebuild output switches updates off: `EXUpdatesEnabled` is false in `mobile/ios/AlexMessages/Supporting/Expo.plist`, and `expo.modules.updates.ENABLED` is false in `mobile/android/app/src/main/AndroidManifest.xml`.

**No phone that has the app today can ever receive an OTA update.** OTA needs native code (the expo-updates client) inside the app binary. Every phone must install one new binary by hand ("build 2", Phase 6). After that install, JS updates arrive by themselves.

**What this plan delivers once every phase is done:**
1. A JS/TS or bundled-asset change that you publish from your Mac reaches every phone on the matching native build without a reinstall. Phones on an older native build never receive it. The phone downloads it in the background, then applies it at one of three moments: the next cold start; the first return to the app after at least 15 minutes in the background, when nothing would be lost; or when the user taps Settings → Restart to Update. It never applies during a call.
2. Every update is signed with a key that never sits on the Pi, the GCP relay, Cloudflare or GitHub. A phone rejects code that this key did not sign.
3. A bad update can be stopped in seconds over SSH without the key, and phones can be sent back to the code built into their binary.
4. Android: from build 3 on, native updates install from inside the app (one tap, or no tap on Android 12+ in a later build). iOS: native updates reach users through an AltStore/SideStore source, if that is how the iPhones install the app (question Q2).

**What OTA can never do, whatever is built:**
- Change native code: `mobile/modules/*` (Swift/Kotlin), react-native-webrtc, InCallManager, any npm package with native code, permissions, Info.plist/AndroidManifest entries, the app icon, the ringtone files in `mobile/assets/sounds/`, Android notification channel settings.
- Extend the 7-day expiry of an iPhone app signed with a free Apple ID.
- Update a phone whose app is never opened (Android's later background installer is the only exception, and only for native updates).

**Done so far:** research and this plan. No repo file was changed.

**Not done:** everything in sections 5 to 7.

---

## 1. Facts that shape this plan

Each fact was checked in the repo, in the expo-updates 57.0.26 source, or against a live endpoint on 2026-10-22.

1. **The installed binaries predate DM calls.** The IPA (`mobile/build/AlexMessages-previous.ipa`, 2026-10-07) and the APK (`mobile/dist/AlexMessages-1.0.0-unsigned.apk`, 2026-10-12) contain none of the call modules. Today's JS cannot run on them. Every phone needs a new binary anyway, with or without OTA.
2. **The Android release signing key is public.** `android/app/build.gradle` signs release builds with `debug.keystore`, which is byte-identical to the keystore inside the public `expo` npm template. Anyone can build an APK that Android accepts as an update of `com.alexanderjia.messages`.
3. **The DM-calls feature is untracked in git.** `mobile/modules/call-ringer/`, `mobile/modules/system-calls/`, `mobile/src/call/`, `internal/webapp/calls.go` and 30 more paths show as `??`. A clean checkout cannot build the app.
4. **The repo is public**, so GitHub Actions logs and artifacts are world-readable.
5. **Continuous deployment fails before it reaches the Pi** (run 37958891653, 2026-10-09: host key verification). The repo variable `HEALTH_PORTS` is `8765 9090 9091`, but the units listen on `6001 6002 6003`. **`DEPLOY_ENABLED` is `true`, so fixing the host key alone deploys on the next push.**
6. **Deploying the current server breaks images in today's apps.** DEPLOY.md:212-215: older mobile builds request media without the bearer header and get 401. So the server deploy and the hand-out of build 2 must happen on the same day.
7. **Cloudflare sits in front of every public domain**, which DEPLOY.md does not mention. The path is phone → Cloudflare → GCP relay `35.226.215.209` → Pi nginx → Go. Cloudflare caches by file extension (`.js`, `.png`, `.apk` and `.bin` yes; extension-less, `.ipa` and `/api/*` no).
8. **Android rarely cold-starts.** The START_STICKY ring service keeps the process, and the JS runtime, alive after the app is swiped away. expo-updates' default "apply at the next cold start" may therefore never happen. The app has to reload itself at a safe moment.
9. **A reload ends a live call.** The server lets only the same device id resume a call, and a reload creates a new JS runtime with a new device id.
10. **Every build so far is version 1.0.0, build 1**, including the builds without call code.
11. **Bundle sizes:** the Hermes bundle is 3.8 MB (Android) and 4.4 MB (iOS), about 1.3–1.5 MB with brotli. A typical update downloads only the bundle, because assets already inside the binary are reused by hash.
12. **expo-updates for SDK 57 is 57.0.26** (`npx expo install expo-updates`).
13. **Debug builds never run updates.** Only Release builds (`npx expo run:ios --configuration Release`, `npx expo run:android --variant release`) can be used to test OTA.
14. **The client only accepts an update newer than what it runs.** It compares `createdAt` with the running update's time, and the embedded bundle's time is the moment the binary was built. An older update can never be pushed "back"; a rollback means republishing old content with a new time, or a signed `rollBackToEmbedded` directive.
15. **`npx expo export` deletes its output directory first.** The default `dist` would delete `mobile/dist/` and the APK in it.

---

## 2. The design in one picture

```
Your Mac (the only machine with keys)          Raspberry Pi: cmd/server                      Phone (build 2+)
───────────────────────────────────────        ──────────────────────────────                ─────────────────────
go run ./cmd/otapublish ota                    data/ota/   (never touched by deploys)        expo-updates (native)
  clean worktree of a pushed commit              assets/<sha256>.<ext>  immutable              GET /api/ota/manifest
  npm ci → typecheck → tests                     updates/<id>/manifest.json + .sig            GET /ota/assets/<sha>.<ext>
  runtime registered? → expo export              channels/<ch>/<platform>/<runtime>.json      (through Cloudflare)
  build manifests → passphrase → SIGN            runtimes/, native/, certs/ (public)
  rsync over ssh -p 2222 (not Cloudflare) ───►  bin/otactl ingest (re-verifies signatures)     UpdateHost (JS)
  verify through Cloudflare, then activate       /api/ota/manifest only SELECTS stored bytes    checks on resume,
  archive in ~/AlexMessagesReleases            ota_installs table = census of every install   reloads only when safe
```

The Pi never signs anything and holds no private key. It serves bytes that the Mac signed.

---

## 3. Decisions

### D1. Provider: self-host the Expo Updates protocol in the existing Go server

**Decision:** `cmd/server` serves Expo Updates protocol v1 at `messages.alexanderjia.com`. The client is Expo's own `expo-updates` 57.0.26. Manifests and directives are signed on the Mac at publish time.

**Why:**
- Updates use the same host and the same Cloudflare path as chat. A user who can chat, including from mainland China, can update.
- Code signing costs nothing. EAS Update signs only on the $199/month Production plan.
- No new vendor or account sits in the code-delivery path, and nothing new runs on the Pi (no Node, no database server).
- The protocol is small: one manifest GET and immutable asset files.

**Rejected:**
- *EAS Update Free:* no code signing; Expo's own docs list Cloudflare throttling as a failure cause, and there is a public Error 1010 report from China.
- *EAS Update Production:* $199/month for signing.
- *xprem (expo-open-ota), expo/custom-expo-updates-server:* both sign on the server, so the key would live on the Pi. xprem also needs an Expo token; the reference server is a Next.js demo with a path-traversal pattern.
- *Pushy/Cresc:* best China CDN, but it replaces expo-updates and puts a vendor in the code path.
- *Hot Updater, CodePush forks:* replace expo-updates, need Node plus a database, or are archived upstream.

### D2. URLs and storage

**Public URLs**, all on `https://messages.alexanderjia.com`, all without login (expo-updates sends no session):

| URL | What | Cache-Control |
|---|---|---|
| `GET /api/ota/manifest` | The manifest endpoint. This exact URL is baked into build 2 **forever**. | `private, max-age=0, no-store` |
| `GET, HEAD /ota/assets/<sha256-b64url>.<ext>` | Update assets. `<ext>` is `js` for the launch bundle (served as `application/javascript`) and the asset's own extension otherwise. | `public, max-age=31536000, immutable` |
| `GET /api/app/latest` | Native-build policy for both platforms (D10) | `no-cache` |
| `GET /app` | Install page: links, SHA-256 sums, instructions | `no-cache` |
| `GET, HEAD /app/android/AlexMessages-<version>-<build>.apk` | APK, `Content-Disposition: attachment` | immutable |
| `GET, HEAD /app/ios/AlexMessages-<version>-<build>.ipa` | Unsigned IPA | immutable |
| `GET /app/altstore.json` | AltStore/SideStore source (only if Q2 says so) | `no-cache` |
| `GET /api/version` | `{"commit":"<sha>"}`, stamped with `-ldflags` at build | `no-store` |

Asset URLs in a manifest are **absolute** (`https://messages.alexanderjia.com/ota/assets/…`). The client does not resolve relative URLs.

Why `.js` and real extensions: Cloudflare then caches and brotli-compresses the assets with no dashboard rule. The client checks every asset's SHA-256 after decoding, and the publisher checks every asset through Cloudflare before activating an update (D6), so a transforming CDN cannot slip in changed bytes.

**Manifest endpoint logic:**
1. Validate headers. `expo-protocol-version` must be `1`, `expo-platform` must be `ios` or `android`, and `expo-runtime-version` must match `^[a-zA-Z\d][a-zA-Z\d._+()-]{0,254}$`. Anything else gets 400.
2. Pick the channel: `beta` if `Expo-Extra-Params` has `am_channel="beta"` and a beta pointer exists for this platform and runtime; otherwise `production`.
3. Read the pointer `data/ota/channels/<channel>/<platform>/<runtime>.json` and answer:

| Pointer state | Answer |
|---|---|
| no pointer, `halted`, or the runtime is not registered | `204` with `expo-protocol-version: 1` |
| `update`, and `Expo-Current-Update-ID` equals it | `204` |
| `update` | `multipart/mixed`: the `manifest` part (stored bytes, stored `expo-signature` part header) and a `certificate_chain` part (the leaf certificate) |
| `rollback`, and the phone already runs its embedded bundle | `204` |
| `rollback` | `multipart/mixed`: the `directive` part (the pre-signed `rollBackToEmbedded`) and the `certificate_chain` part |

4. Record the census row (D8).

Response headers on every answer: `expo-protocol-version: 1`, `expo-sfv-version: 0`. The server ignores `A-IM: bsdiff` and always sends full files; both clients accept that.

**Storage on the Pi:** `/home/alexander/alexmessage/data/ota/` (the env var `OTA_DIR` overrides it).

```
data/ota/
  FORMAT                                          "1"
  certs/root-2026.pem, leaf-<yyyymm>.pem          public certificates only
  assets/<sha256-b64url>.<ext>                    write-once; never deleted (the spec forbids removing them)
  updates/<updateId>/manifest.json                the exact signed bytes
                     manifest.sig                 the expo-signature value
                     rollback.json, rollback.sig  rollBackToEmbedded, commitTime = createdAt + 1 s
                     info.json                    platform, runtime, label, commit, createdAt, notes, critical
  runtimes/<platform>/<runtime>.json              native builds registered for this runtime
  channels/<production|beta>/<platform>/<runtime>.json
                                                  {"state":"update"|"rollback"|"halted","update":"<id>","since":…}
  revoked.txt                                     update ids that can never be activated again
  native/policy.json, native/altstore.json, native/android/*.apk, native/ios/*.ipa
  incoming/<publishId>/                           upload staging (same filesystem, so ingest is a rename)
  log/actions.log                                 append-only record of every otactl action
```

- Pointers are written to a temp file and renamed, so every write is atomic.
- **Server deploys never touch `data/ota/`.** `release.tgz` never contains `data/`, and `remote_deploy.sh` only copies the database out of it. A new test in `deploy/remote_deploy_test.py` checks that `data/ota/` is byte-identical after a deploy and after a forced rollback.
- **A server rollback does not move the channels.** It swaps only `bin/` and `web/dist/`. A rolled-back server older than the OTA routes answers 404, and phones keep the update they have.
- **Pointers are files, not database rows.** Restoring `alexmessage.db` from `backups/` would otherwise silently move every channel.
- **Which copy is authoritative:** the Mac archive `~/AlexMessagesReleases/ota/` for the write-once files (assets, updates, runtimes, native); the Pi for pointers and `policy.json`. `otapublish audit` compares the two.

### D3. Runtime version: fingerprint, and only runtimes that a real binary has

**Decision:** `"runtimeVersion": {"policy": "fingerprint"}` (one hash per platform), plus `mobile/fingerprint.config.js`:

```js
/** @type {import('expo/fingerprint').Config} */
module.exports = {
  // A configured sourceSkips REPLACES the default (512); PackageJsonScriptsAll covers it.
  sourceSkips: [
    "ExpoConfigVersions",      // version / versionCode / buildNumber are identity, not native ABI
    "ExpoConfigExtraSection",  // `extra` reaches JS through the manifest's extra.expoClient
    "PackageJsonScriptsAll",   // adding an npm script must not cut off every phone
    "GitIgnore",               // editing .gitignore does not change native code
  ],
  extraSources: [
    { type: "file", filePath: "certs/ota-root-2026.pem", reasons: ["ota-code-signing-root"] },
    { type: "dir",  filePath: "assets/sounds",           reasons: ["native-ringtones"] },
  ],
};
```

- `ExpoConfigEASProject` is **not** skipped: it would remove `updates.url` from the hash, and a binary built with a test URL would then share the production runtime.
- `extraSources`: replacing a ringtone or the root certificate is a native change, but by default only their paths are hashed.

**Guarantee that a publish reaches only binaries that can run it:**
1. `otapublish native` reads the runtime **out of the built artifacts** (`Payload/AlexMessages.app/EXUpdates.bundle/fingerprint` in the IPA; `assets/fingerprint` in the APK). It compares each with `npx expo-updates runtimeversion:resolve --platform <p>` on the same clean tree and aborts on any difference. Only then does it register `runtimes/<p>/<runtime>.json` on the Pi.
2. `otapublish ota` resolves the runtime of the tree it exports. It publishes to a platform only if that runtime is registered. Otherwise it stops with: "Native code changed since the last native build of `<p>`. Run `otapublish native`."
3. `otactl activate` on the Pi refuses an update whose runtime is not registered, and the manifest endpoint answers 204 for an unregistered runtime.
4. Every native build and every export runs in a clean `git worktree` of a commit that exists on `origin`. Untracked files cannot shift the fingerprint.
5. Native builds come from the newest commit that contains everything already published by OTA for that runtime. A binary built later from older JS would ignore every update published before its build time.
6. Every environment variable that `app.config.js` reads must be identical during prebuild, the native build, and the export (the Xcode and Gradle steps re-evaluate the config to compute the fingerprint). The production path sets none of them; step 1 catches a mismatch.

**Rejected:** `appVersion` (every build so far is 1.0.0, including the builds without calls); `nativeVersion` or a hand-written string (correct only if someone remembers to bump it, and this repo changes three native modules often; a forgotten bump produces the "undefined is not a function" crash CLAUDE.md describes).

### D4. Code signing and key custody

**Decision: a two-level certificate chain**, which both clients support (`certificate_chain` response part).

| | Root | Leaf |
|---|---|---|
| Key | RSA-4096 | RSA-3072 |
| Certificate | self-signed; `basicConstraints CA:true, pathlen:0`; keyUsage `keyCertSign` | keyUsage `digitalSignature`; EKU `codeSigning` |
| Validity | 20 years | 400 days, `notBefore` 24 h in the past; replaced every 12 months |
| Where the certificate lives | committed at `mobile/certs/ota-root-2026.pem`, embedded in every binary | `data/ota/certs/` on the Pi; sent in every manifest response |
| Where the private key lives | encrypted with a passphrase (age), on one USB stick, plus one copy in your password manager. Never on the Mac's disk. | encrypted with a different passphrase (age) at `~/.alexmessages/keys/ota-leaf-<yyyymm>.key.age` on the Mac |
| Used for | issuing a leaf, about once a year | signing every manifest and rollback directive |

- app.json: `codeSigningCertificate: "./certs/ota-root-2026.pem"`, `codeSigningMetadata: {"keyid": "root-2026", "alg": "rsa-v1_5-sha256"}`.
- No app.json key turns on chain mode. A small config plugin, `mobile/plugins/withOtaCertificateChain.js`, sets `EXUpdatesCodeSigningIncludeManifestResponseCertificateChain` (iOS `Expo.plist`) and `expo.modules.updates.CODE_SIGNING_INCLUDE_MANIFEST_RESPONSE_CERTIFICATE_CHAIN` (Android manifest) to true.
- `mobile/.gitignore` ignores `*.pem`; add the exception `!certs/*.pem`. Only public certificates go there.
- **Signing happens on the Mac only**, inside `otapublish`, after every Node process has exited. It signs the exact manifest bytes and that update's `rollBackToEmbedded` directive, and checks both signatures against the root before uploading. `otactl ingest` checks them again on the Pi with Go's `crypto/x509`, using the client's rules.
- **Leaf rotation needs no new binary:** plug in the USB stick, run `otapublish keys issue-leaf`, then `otapublish resign-active` (re-signs every manifest and directive that a pointer references; the bodies stay byte-identical).
- **What a stolen leaf allows:** someone who also controls the delivery path (Cloudflare, the relay or the Pi) could push code until that leaf expires, at most 400 days. A stolen single long-lived key would allow it for its whole lifetime.
- **What a lost leaf costs:** issuing a new one from the root. A lost root with no backup costs a manual reinstall on every phone; hence the two copies.
- **Limit of the encryption:** it protects the keys against a process that reads files. It does not protect against malware that stays on the Mac and records the passphrase.
- **Fallback, decided now:** if the local drill (Phase 5) shows that chain mode fails on either platform, build 2 instead embeds one self-signed RSA-3072 code-signing certificate valid for 20 years, with its key stored like the root.

**Rejected:** signing on the Pi or in a request handler (a Pi compromise would become code execution on every phone); a GitHub Actions secret (public repo, public logs); `npx expo-updates codesigning:generate` (it makes an RSA-2048 certificate that cannot act as a chain root); an unencrypted key file with mode 0600 (every `npm ci` install script runs as the same user).

### D5. Channels

**Decision:** two channels, `production` and `beta`. No percentage rollouts.
- `updates.requestHeaders = {"expo-channel-name": "production"}` is baked in and **never changed**. A changed header value in a later binary deletes every downloaded update.
- A phone joins beta through `Updates.setExtraParamAsync("am_channel", "beta")`. The client sends it as `Expo-Extra-Params` on every manifest request and keeps it across launches; changing it deletes nothing. The switch is in Settings → Software Update and is shown only to admins.
- `otapublish ota` publishes to beta by default. `otapublish promote <label>` points production at the **same update id**, with no rebuild and no re-signing. It refuses unless the census shows at least one beta install per platform that has launched the update and reported no failure. `--force` skips that check for an urgent fix.
- A phone that leaves beta stays on its newer beta update until production publishes something newer.

**Rejected:** percentage rollouts (10% of a few dozen phones measures nothing); a channel baked per binary (separate builds, and switching wipes the cache).

### D6. Publish pipeline

**Decision:** everything that needs a key runs on your Mac. `cmd/otapublish` is a Go program that is never deployed; it shares the manifest and signature code in `internal/ota` with the server and `otactl`, so the publisher and the server cannot drift apart. Files reach the Pi with `rsync` over `ssh -p 2222` to the relay, which bypasses Cloudflare. GitHub Actions only runs checks (typecheck and tests, no secrets).

**What `go run ./cmd/otapublish ota --notes "<text>" [--channel beta|production] [--critical] [--dry-run]` does, in order.** It stops at the first failure, and nothing goes live before step 14.
1. Check that the commit is pushed (`git fetch`; it must be contained in `origin/react` or `origin/main`) and create a clean `git worktree` of it in a temp directory.
2. Refuse if any `EXPO_PUBLIC_*` variable is set, and set `EXPO_NO_DOTENV=1`. Such values are inlined into the bundle.
3. `cd shared && npm ci && npm run typecheck && npm test`.
4. `cd mobile && npm ci && npx tsc --noEmit`.
5. For each platform: `npx expo-updates runtimeversion:resolve --platform <p>`, and ask the Pi (`ssh … bin/otactl runtimes --json`) whether it is registered. Unregistered platforms are skipped with the message from D3.
6. Server compatibility: compare `GET /api/version` with the commit. If `internal/` or `cmd/server` differ, print the diff summary and require typing `yes`.
7. Native-contract warning: print `git diff --stat` since the registered binary's commit for `mobile/src/call`, `mobile/modules`, `mobile/src/notify.ts`, `mobile/src/session.tsx`, `mobile/app.json`, and require `yes` if anything changed.
8. `npx expo export --platform ios --platform android --output-dir <temp>/export --dump-assetmap --source-maps`. Never `mobile/dist`.
9. `npx expo config --json --type public` → `extra.expoClient`.
10. Build one manifest per platform: random UUIDv4 `id`; `createdAt` = now; each asset with `hash` (SHA-256, base64url, no padding), `key` (MD5 hex, so assets already inside the binary are not downloaded again), `contentType`, `fileExtension`, absolute `url`; `metadata: {}`; `extra: {expoClient, am: {label, commit, notes, critical}}`. Refuse if `createdAt` is not later than both the current pointer's update and the registered binary's build time.
11. All Node processes have exited. Ask for the leaf passphrase on the terminal; sign each manifest and its rollback directive; verify both against the root certificate.
12. Copy into the Mac archive, then `rsync --ignore-existing` to `data/ota/incoming/<publishId>/`. Published bytes can never be overwritten.
13. `ssh … bin/otactl ingest <publishId>`: re-verify signatures and hashes, move assets and updates into place.
14. Download every asset through Cloudflare and check its SHA-256. Only then `otactl activate --channel <ch> …` flips the pointer. **The update is live from this moment.**
15. Smoke test: request the manifest through Cloudflare with phone headers (and `am_channel` for beta), check the id and signature. On failure, restore the previous pointer and exit non-zero.
16. `git tag ota-<label>`; print who gets it and when.

`--dry-run` runs steps 1–10 with no key and no upload. Claude may run it; Claude runs the real publish only when you ask in chat.

**Rejected:** publishing from GitHub Actions (key in a public-repo CI; CD is broken; a Linux-computed iOS fingerprint is not proven to match the macOS build); uploading through the admin panel (Cloudflare's 100 MB request cap, weak admin login protections).

### D7. When and how a phone applies an update

**Native configuration (baked into build 2):** `enabled: true`, `checkAutomatically: "ON_LOAD"`, `fallbackToCacheTimeout: 0`, `useEmbeddedUpdate: true`, `disableAntiBrickingMeasures: false`.
- `ON_LOAD` makes the native code check at every cold start, independent of JS. That is the self-repair path if a bad update breaks the JS updater.
- `0` means a cold start never waits on the network (important for China and for answering a call from a cold start).
- `disableAntiBrickingMeasures: false` means JS can never repoint the update URL.

**JS: `mobile/src/updates/UpdateHost.tsx`**, mounted in `app/_layout.tsx` inside `<SessionProvider>` next to `<CallHost>`, inside its own error boundary. It does nothing in debug builds, on Expo web, or when `Updates.isEnabled` is false. It never checks or reloads in the `app/index.tsx` boot gate. `UpdateHost` is the **only** place in the app that calls `Updates.reloadAsync`.
- **Check:** when the app becomes active and the last check (native or JS) is at least 30 minutes old, call `checkForUpdateAsync()`, then `fetchUpdateAsync()` if one is available. A downloaded rollback directive is handled the same way.
- **Automatic apply:** when the app returns to `active` after **at least 15 minutes in the `background` state**, an update or rollback is downloaded (`useUpdates().isUpdatePending`), and the guard is clear:
  1. wait 1.5 s, so a notification tap or call action can arrive;
  2. run the guard again;
  3. save the open conversation to AsyncStorage key `ota:restore` with a timestamp;
  4. flush the DebugReporter (at most 1 s);
  5. check `store.state.call` and `isCallLive()` once more, synchronously;
  6. call `Updates.reloadAsync({reloadScreenOptions: {backgroundColor: "#F6F4EE", image: require("../../assets/icon.png"), fade: true}})`.

  After the reload, boot reopens the saved conversation if the record is less than 60 s old.
- **Cold start:** expo-updates launches the newest downloaded update by itself.
- **Manual:** Settings → Software Update → **Restart to Update**, shown while an update is pending.
- **Critical update** (`extra.am.critical: true`), or an update pending for more than 24 h: a dismissible banner on the DM list with a Restart button.

**Reload guard: `mobile/src/updates/guard.ts`.** Hard blockers stop every reload, including a manual one:
1. a call that is ringing or live: `store.state.call` with phase other than `ended`, or `isCallLive()`;
2. Android: the app is shown over the lock screen (`lockedOverCall()`), or a call notification was tapped in the last 60 s, or an Answer is still waiting (new `msSinceCallAction()` in `src/call/system.android.ts`);
3. an APK install in progress (D10).

Soft blockers stop the automatic apply. A manual restart lists them and asks first:
- a composer draft, an upload in progress, staged attachments, or a reply target;
- a message with `_status` `sending` or `failed`, or a non-empty socket send queue (new `ChatStore.hasUnsentWork()` in `shared/src/store.ts`);
- a recording, a recorded voice message not yet sent, or media playing (new read-only `isMediaActive()` in `src/media.ts`);
- unsaved profile edits; text typed on the login screen; a data export in progress;
- an activity outside the app is open: image or document picker, camera, share sheet, `WebBrowser`, a system settings page, a permission dialog (each awaited call is wrapped in `holdReload("external")`);
- iOS: the native image viewer or a UIKit context menu is presented (`holdReload("native-ui")`);
- a route other than `/chat` or `/conversation` is on screen (automatic apply only);
- the session `phase` is not `ready`.

Each blocked automatic apply logs `ota.blocked {reasons}` at most once per hour per update.

**Rejected:** reloading right after the download (interrupts the user); reloading in the background (Expo calls it experimental; iOS suspends the app within seconds); applying only at cold start (Android almost never cold-starts); a 3-minute threshold (coming back from a picker or the camera after 3 minutes would reload and lose the picked file); `WIFI_ONLY` (China users on mobile data would never update); `fallbackToCacheTimeout > 0` (blocks launch on slow networks).

### D8. Telemetry: who runs what, and what failed

1. **Census from the manifest request (server).** Every manifest request upserts one row in a new table `ota_installs` in `alexmessage.db`, keyed by `EAS-Client-ID` (a per-install UUID both clients send). It stores platform, runtime, current and embedded update ids, channel, `Expo-Recent-Failed-Update-IDs`, `Expo-Fatal-Error` (first 1024 characters), the claimed user id (extra param `am_uid`, set after sign-in; a hint only, never used for access), first and last seen. Header formats and sizes are validated, writes are rate-limited per IP, IPs are not stored, rows unseen for 90 days are pruned by the existing maintenance sweep. A new failed id or fatal error emits a debug-console event `ota.client_failure`.
2. **`X-AM-Client` header (build 2).** Value: `v=1;platform=android;version=1.2.0;build=2;runtime=<fp>;update=<uuid|embedded>;channel=production`. `ApiClient` sends it on every REST call (new `clientInfo` option in `shared/src/api.ts`); the `/ws` upgrade carries it as the `client=` query parameter (`shared/src/socket.ts`); `RingSocket.kt` sends `v=1;platform=android;build=2;component=ring`. The RingSocket part is native, so it must be in build 2. The server stores it per session (`sessions.client`, `sessions.client_seen_at`, at most one write per 10 minutes). The web client sends nothing and is never gated.
3. **Debug-console events from the app.** First fix the DebugReporter so mobile clients stop reporting session `"anon"` (`session.tsx` creates it without `sessionStorage`), and expose it from `Session`. Events: `ota.boot {updateId, label, runtime, build, isEmbeddedLaunch, isEmergencyLaunch, emergencyLaunchReason, channel}`, `ota.check`, `ota.download`, `ota.apply {trigger}`, `ota.blocked`, `ota.error` (with a short summary of `Updates.readLogEntriesAsync(3600000)`), `apk.download`, `apk.install`.
4. **Crash breadcrumb.** A JS global error handler writes `{updateId, ts, message, stack ≤ 2 KB}` to a file and reports it as `ota.crash` at the next launch. It **must save the previous handler and always call it**: expo-updates' automatic rollback depends on the fatal error reaching React Native's fatal handler.

**Where you read it:** first a read-only admin endpoint `GET https://admin.alexanderjia.com/api/ota/installs` (JSON) and `bin/otactl census` over SSH (the Pi has no `sqlite3`); later an "Updates" section in the admin panel (Phase 9).

**Automatic halt (Phase 9, server only):** the production pointer switches to `halted` (204 to everyone) when an install that is linked to an approved user, and was first seen before the activation, reports the active update as failed, or when `ota.crash` for it arrives from two installs within 60 minutes. A halt stops further downloads; it does not roll back phones that already have the update.

### D9. Rollback of a bad update

**Emergency, no key needed, from any device with SSH:**
1. `ssh -p 2222 alexander@35.226.215.209 'cd alexmessage && bin/otactl halt --channel production'`. Phones that have not downloaded the bad update stop getting it at once.
2. `bin/otactl rollback-embedded --channel production`. The pointer switches to the bad update's pre-signed `rollBackToEmbedded` directive (its `commitTime` is the bad update's `createdAt` + 1 s, so it applies to that update and to anything older). The bad id goes into `revoked.txt`.
3. Phones apply it at their next check plus reload: a resume after 15 minutes, a cold start, or Restart to Update. `bin/otactl status` counts the installs still reporting the bad id.
4. If the bad update broke `UpdateHost` itself, tell the affected users: iOS, swipe the app away and reopen it; Android, Settings → Apps → Alex Messages → Force stop, then reopen. That cold start runs the native check, which needs no JS.

**Fix forward, at the Mac:**
5. `go run ./cmd/otapublish republish <last good label>` re-signs the last good content from the Mac archive as a new id with a new `createdAt`. Assets are reused by hash, so phones download only the manifest. Or publish a fix normally.
6. Any new update replaces the rollback pointer, because its `createdAt` is later than the directive's `commitTime`.

**Rule:** `rollback-embedded` is allowed only while the server still works with the embedded JS of every runtime in the census, because that is the code phones go back to.

**Automatic, no action needed:** an update that crashes before its first screen is marked failed on that phone and never launched there again; the phone falls back to the last update that launched successfully, or to the embedded bundle. An update that breaks later is not caught by expo-updates; steps 1–6 handle it.

### D10. Native updates that OTA can never deliver

**Android: a private release key, one clean break, then an in-app updater.**
- You create a private keystore (PKCS12, RSA-4096) at `~/.alexmessages/keys/android-release.p12`, backed up with the OTA root. A config plugin, `mobile/plugins/withReleaseSigning.js`, adds `signingConfigs.release` from `AM_RELEASE_*` Gradle properties in `~/.gradle/gradle.properties` and **fails the release build if they are missing**, so a release can never again be signed with the public key. The certificate's SHA-256 is committed at `mobile/release/android-signer.sha256`, and every APK is checked against it.
- **Every Android phone uninstalls the old app once** (Android rejects an update signed with a different key). This happens during the manual install of build 2 that is needed anyway. Users sign in again and grant notifications, full-screen calls and background use again. Messages are on the server and are not lost.
  - Rejected: APK v3 key rotation from the public key. The lineage would start from a key everyone has, Android 7.0–8.1 (minSdk is 24) ignores v3, and phones need a manual install anyway.
- **Build 2 contains a minimal `mobile/modules/app-updater` (Kotlin).** Functions: `installedBuild()`, `canInstall()`, `openInstallPermissionSettings()`, `downloadAndInstall(url, sha256, minBuild)`. It downloads the APK with Range resume, checks the SHA-256, then checks with `getPackageArchiveInfo(GET_SIGNING_CERTIFICATES)` that the package is `com.alexanderjia.messages`, the versionCode is higher than the installed one, and **the signer equals the installed app's signer**. It installs through a `PackageInstaller` session (`USER_ACTION_NOT_REQUIRED` on API 31+; the system confirm screen on `STATUS_PENDING_USER_ACTION`). `REQUEST_INSTALL_PACKAGES` is declared in the module's manifest. Even malicious JS can only install a newer build of this app signed with your key. The hard reload blockers also block an install, and `RingReceiver` already restarts the ring service on `MY_PACKAGE_REPLACED`.
  - Why build 2 and not later: without it, build 3 again needs a manual reinstall on every Android phone, including phones in China you cannot reach by cable.
  - Later, in build 3 or after: a JobScheduler job that installs silently while the app is not on screen.
- `android.versionCode` goes up by 1 on every native release and is never reused.

**iOS (no paid Apple account):**
- The IPA stays unsigned; each user's sideload tool signs it.
- If the iPhones use AltStore or SideStore (Q2): `/app/altstore.json` lists each IPA with `version` = `CFBundleShortVersionString` and `buildVersion` = `CFBundleVersion`. The app shows "Alex Messages 1.3.0 is available. Update it in SideStore." with a button that opens `sidestore://` or `altstore://`. Build 2 needs `ios.infoPlist.LSApplicationQueriesSchemes: ["altstore", "sidestore"]` for that check.
- If they use Sideloadly or Xcode: every native release needs you to reinstall on each iPhone.
- **Not solvable here:** a free-Apple-ID app stops opening 7 days after signing unless the tool refreshes it. SideStore refreshes on the phone; AltStore needs AltServer on a computer.

**Minimum-client gate:**
- `data/ota/native/policy.json`: per platform `min_build` (0 = off) and `latest {version, build, url, sha256, size, notes}`.
- **Soft gate (default):** the server adds `client_policy {state: ok|update_available|update_required, latest}` to the WS `init`, based on the `X-AM-Client` build. The app shows a Settings row for `update_available` and a blocking screen for `update_required` (Android: Install; iOS: instructions).
- **Hard gate (only when an old native protocol is about to be removed):** REST answers 426 and `/ws` closes with code **4426** — only for clients that send `X-AM-Client` with a build below `min_build`. Build 2's `ChatSocket` and `RingSocket` stop reconnecting on 4426 and show "Update Alex Messages to keep messaging" / "… to keep receiving calls". **Clients without the header are never gated:** today's builds reconnect every 1.5 s on any close code except 4401.
- Native-only protocols (`/ws?mode=calls`, the ring flows) must stay backward-compatible with the lowest build in the census.

### D11. The one-time migration to build 2

Build 2 = version **1.2.0**, `ios.buildNumber` **"2"**, `android.versionCode` **2**. It contains expo-updates, the root certificate and chain flag, the update UX and guard, `X-AM-Client` (including RingSocket), 4426 handling, `app-updater`, `LSApplicationQueriesSchemes`, and the private Android key. Order (details in Phase 0 and Phase 6):
1. `DEPLOY_ENABLED=false` **before** fixing CD, so no push deploys by surprise.
2. Commit all current work, including the untracked DM-calls code.
3. Build and pass the local drill on both platforms (Phase 5).
4. Test reachability from a phone in mainland China before the URL is baked in.
5. Release day: deploy the server and hand out build 2 **on the same day** (fact 6). Android users uninstall and install; iPhone users reinstall through their tool. Where possible, install Android phones yourself with `adb install`; publish the APK SHA-256 and the signer SHA-256 in DEPLOY.md on GitHub so others can check the download.
6. Check that every phone appears in the census on build 2.

### D12. Testing before trusting it

1. **Go unit tests** (`internal/ota`, `internal/webapp`, `internal/db`, `cmd/otactl`, `cmd/otapublish`): exact multipart bytes and headers; every 204 path; the directive path; 400s for bad headers; asset name validation (`../`, wrong length), content type, Cache-Control, Range and HEAD; chain checks that mirror the client (non-CA root, leaf without codeSigning, expired leaf, bad signature are rejected); census header limits and SFV parsing; `activate` refuses a revoked id, an unregistered runtime, and a `createdAt` that is not newer; atomic pointer writes; a sign-then-verify round trip; a test that `cmd/server`, `cmd/admin` and `cmd/meet` do not import the signing package.
2. **shared Vitest:** `X-AM-Client`; `hasUnsentWork()`; 4426 stops reconnecting; 426 handling.
3. **`deploy/remote_deploy_test.py`:** `data/ota/` is unchanged by a deploy and by a forced rollback.
4. **Local drill on release builds** (Phase 5). A drill profile (`AM_OTA_PROFILE=drill` in `mobile/app.config.js`) changes the package and bundle id to `com.alexanderjia.messages.drill`, the update URL to `http://localhost:8799/api/ota/manifest` (iOS Simulator) or `http://10.0.2.2:8799/api/ota/manifest` (Android emulator, with cleartext allowed for that profile only), and uses throwaway keys. Because the package id differs, a drill build can sit next to the real app and has its own runtime. Scenarios:

| # | Scenario | Passes when |
|---|---|---|
| a | publish U1; cold start; then resume past the threshold (set to 10 s for the drill) | `Updates.updateId` = U1 both ways; a census row exists |
| b | U2 throws at import | the phone falls back to U1; the census shows U2 as failed |
| c | U3 crashes 15 s after the first screen | breadcrumb reported; `rollback-embedded` returns the phone to the built-in bundle; `republish` then moves it forward |
| d | flip one byte of `manifest.json`, then of an asset | both rejected; the phone keeps its update |
| e | serve an older manifest (replay); serve an unsigned response; a wrong leaf; an expired leaf | all ignored or rejected |
| f | publish for an unregistered runtime | refused by `otapublish` |
| g | call between Simulator and emulator, publish, resume | no reload until the call ends |
| h | Android: swipe the app away with the ring service running, publish, reopen | applied through the resume path |
| i | a picker is open for more than the threshold | no reload; the picked file arrives |
| j | after a reload, check notifications and links | no old notification tap or deep link is replayed |
| k | build 3 (no-op change) installed by `app-updater` on API 35 and API 30 emulators; then a debug-key-signed APK | 35: installs; 30: system dialog; debug-signed: refused "signer mismatch" |
| l | `min_build` = 3 | build 2 shows the blocking screen; REST 426; WS 4426; the server sees one connection, not a reconnect loop |
| m | calls in **release** builds (R8 minification is on) on both platforms | calls ring, connect and end; the ring service works after swipe-away |

5. **Maestro:** a new `mobile/e2e/ota.yaml` (Settings → Software Update shows the version; the beta switch is hidden for non-admins); the 8 existing flows still pass.
6. **Production drills** (Phase 7): beta → promote; `halt`; `rollback-embedded`; `republish`. Repeat the rollback drill every 6 months.

---

## 4. What is baked into build 2

These choices can only change with **another manual reinstall on every phone**. Decide them before Phase 4.

| Baked into build 2 | Value |
|---|---|
| Update URL | `https://messages.alexanderjia.com/api/ota/manifest` |
| Request headers | `{"expo-channel-name": "production"}` |
| Runtime policy | fingerprint + `fingerprint.config.js` |
| Trust anchor | root certificate `ota-root-2026`, chain mode on (or the single-certificate fallback) |
| Android signer | your private release key |
| `app-updater` module | interactive PackageInstaller install with the signer check |
| `RingSocket.kt` | `X-AM-Client` header and 4426 handling |
| `LSApplicationQueriesSchemes` | `altstore`, `sidestore` |
| `expo-application` | direct dependency (native) |

Everything else is JS or server code: `UpdateHost`, the guard, telemetry, banners, the Settings page, the gates, the census, the admin page. It can be improved later by OTA or by a server deploy. Build 2 still needs a solid `UpdateHost`, because a broken one leaves phones relying on cold starts.

Also permanent: **the domain.** Every binary depends on `messages.alexanderjia.com` for updates. If the domain lapses or the Cloudflare account is lost, every phone is stranded until it reinstalls. Turn on auto-renew and the registrar lock.

---

## 5. Implementation phases

Effort figures are rough estimates. "Claude" means a code change I make; "you" means something only you can do. Nothing is pushed, deployed or published without your go-ahead in chat.

### Phase 0: prerequisites (no OTA code yet)

| # | Who | Step | Done when |
|---|---|---|---|
| 0.1 | you | GitHub → repo Settings → Secrets and variables → Actions → **Variables**: set `DEPLOY_ENABLED` to `false`. | The next push runs tests only. |
| 0.2 | you, then Claude | Approve committing the current work. Claude commits the 109 changed and untracked entries on `react` in feature-grouped commits (calls server, mobile calls, shared call engine, tests, docs) and pushes after your approval. | `git status --porcelain` prints nothing; the commits are on `origin/react`. |
| 0.3 | you | Delete the secret `DEPLOY_KNOWN_HOSTS` (it overrides the pinned host key). Set the variable `HEALTH_PORTS` to `6001 6002 6003`. Leave `DEPLOY_ENABLED` at `false`. | `ssh -p 2222 alexander@35.226.215.209 true` works; a push still deploys nothing. |
| 0.4 | you | Answer Q1–Q10 (section 8). | Answers are in the chat. |
| 0.5 | you, and a person in mainland China | Copy a 1.5 MB test file to the Pi: `head -c 1500000 /dev/urandom > /tmp/ota-speedtest.bin && scp -P 2222 /tmp/ota-speedtest.bin alexander@35.226.215.209:/home/alexander/alexmessage/static/`. The person opens `https://messages.alexanderjia.com/static/ota-speedtest.bin` on their phone on mobile data and reports the time. Then delete the file on the Pi. | You know whether 1.5 MB downloads in an acceptable time from China. |
| 0.6 | Claude | Before any `prebuild --clean`: copy today's `mobile/ios` and `mobile/android`, run `npx expo prebuild --clean` in a scratch copy, and diff the two, so hand edits in the gitignored native folders are found instead of lost. | The diff is reviewed; any hand edit is moved into a config plugin or explicitly dropped. |

### Phase 1: server (Claude, about 2–3 days; deployed only on release day)

| # | Step | Done when |
|---|---|---|
| 1.1 | `internal/ota/`: store layout and validators, manifest selection, multipart writer, SFV parsing, certificate-chain verification with the client's rules, census, policy. | `go test ./internal/ota/...` passes (D12 item 1). |
| 1.2 | `internal/webapp/ota_routes.go` (all D2 URLs, registered in the module list in `webapp.go`); `internal/webapp/client_info.go` (`X-AM-Client` middleware); `client_policy` in the WS `init` and the `client=` parameter in `ws.go`; the hard-gate code path (off by default); `/api/version` with `-ldflags` in `deploy.yml`. | `go vet ./... && go test ./...` pass; a local `curl` with phone headers gets `204` with `expo-protocol-version: 1`. |
| 1.3 | `internal/db`: table `ota_installs`; `sessions.client` and `sessions.client_seen_at` through `ensureColumn`; prune in the maintenance sweep. | DB tests pass; an old database migrates on start. |
| 1.4 | `internal/adminapp`: read-only `GET /api/ota/installs`. | Test passes. |
| 1.5 | `deploy/remote_deploy_test.py`: `data/ota` survives a deploy and a rollback. `deploy.yml`: cross-compile `otactl` into `bin/`. | Tests pass in CI. |
| 1.6 | Docs: an OTA section in `DEPLOY.md` and `CLAUDE.md`. | Written. |

### Phase 2: tools (Claude, about 2 days)

| # | Step | Done when |
|---|---|---|
| 2.1 | `cmd/otactl` (runs on the Pi): `ingest`, `activate`, `halt`, `rollback-embedded`, `revoke`, `status`, `census`, `runtimes`, `register-runtime`, `policy`, `doctor` (warns 30 days before the leaf expires). Every action is appended to `log/actions.log`. | Tests pass. |
| 2.2 | `cmd/otapublish` (Mac only, never deployed): `keys init-root | issue-leaf | show`, `ota`, `promote`, `republish`, `resign-active`, `native`, `audit`, `status`, `--dry-run`. age-encrypted keys (`filippo.io/age`, imported only by `cmd/otapublish`). | Tests pass; `ota --dry-run` against a fixture export completes. |
| 2.3 | `.github/workflows/mobile-checks.yml`: shared typecheck and tests, `cd mobile && npm ci && npx tsc --noEmit`, the import-guard test. No secrets. | Green on a push. |

### Phase 3: keys (you, about 30 minutes)

| # | Step | Done when |
|---|---|---|
| 3.1 | Plug in a USB stick. Run `go run ./cmd/otapublish keys init-root --id root-2026 --out /Volumes/<stick>` (asks for a new passphrase), then copy the encrypted root key into your password manager as an attachment. Run `go run ./cmd/otapublish keys issue-leaf --root /Volumes/<stick>/ota-root-2026.key.age --out ~/.alexmessages/keys/` (asks for the root passphrase and a new leaf passphrase). Unplug the stick. | `otapublish keys show` decrypts both copies of the root; `mobile/certs/ota-root-2026.pem` exists. |
| 3.2 | `keytool -genkeypair -v -storetype PKCS12 -keystore ~/.alexmessages/keys/android-release.p12 -alias alexmessages -keyalg RSA -keysize 4096 -validity 10950 -dname "CN=Alex Messages"`. Add the four `AM_RELEASE_*` lines to `~/.gradle/gradle.properties` and `chmod 600` it. Copy the keystore to the USB stick and the password manager. Paste the printed SHA-256 (public) into the chat. | `keytool -list -v` shows the same SHA-256 for every copy. |

### Phase 4: client (Claude, about 3 days)

| # | Step | Done when |
|---|---|---|
| 4.1 | `cd mobile && npx expo install expo-updates expo-application`. `app.json`: version `1.2.0`, `ios.buildNumber` `"2"`, `android.versionCode` `2`, `runtimeVersion` fingerprint, the `updates` block (D7, D4, D5), `LSApplicationQueriesSchemes`, the two plugins. New: `fingerprint.config.js`, `app.config.js` (drill profile only), `plugins/withOtaCertificateChain.js`, `plugins/withReleaseSigning.js`, the `.gitignore` exception, `release/android-signer.sha256`. | `npx expo prebuild --clean` passes; the generated `Expo.plist` and `AndroidManifest.xml` show updates enabled, the URL, the certificate, and the chain flag. |
| 4.2 | `mobile/src/updates/`: `UpdateHost.tsx`, `guard.ts`, `telemetry.ts`, `crashBreadcrumb.ts`, `clientInfo.ts`, `restore.ts`, `UpdateBanner.tsx`, `UpdateRequiredScreen.tsx`; `app/settings/update.tsx` (Software Update page: version, update label, last checked, Check for Updates, Restart to Update, beta switch for admins); the version line in the Settings colophon. Edits: `app/_layout.tsx`, `app/chat.tsx`, `session.tsx` (DebugReporter session id, `clientInfo`, `am_uid`), holds in `Composer.tsx`, `DictationBar.tsx`, `attachments.tsx`, `settings/profile.tsx`, `login.tsx`, the picker/camera/share/WebBrowser/settings call sites, `media.ts` (`isMediaActive`), `system.android.ts` (`msSinceCallAction`). | `npx tsc --noEmit` passes; the 8 Maestro flows pass on debug builds. |
| 4.3 | `shared/`: `api.ts` (`clientInfo`, 426), `socket.ts` (`client=` parameter, 4426 stops reconnecting), `store.ts` (`hasUnsentWork()`, `updateRequired`), `types.ts` (`client_policy`), tests. | `cd shared && npm run typecheck && npm test` pass. |
| 4.4 | Native: `mobile/modules/app-updater/` (Kotlin; iOS stub); `RingSocket.kt` (`X-AM-Client`, 4426 → stop the service and post "Update Alex Messages to keep receiving calls"). | Release builds compile on both platforms; the APK's signer equals `android-signer.sha256`. |

### Phase 5: local drill (Claude, about 2 days, Simulator and emulators; no real phones)

| # | Step | Done when |
|---|---|---|
| 5.1 | Start a scratch server on :8799 with `OTA_DIR` in the scratchpad; build the drill profile in Release on the iOS Simulator and on Android emulators (API 35 and API 30). | Both launch; the scratch store lists their runtimes. |
| 5.2 | Run scenarios a–m (D12 item 4). If chain mode fails on either platform, switch to the single-certificate fallback (D4) and repeat. | Every scenario passes; results are written up for you. |
| 5.3 | you: review the results and approve the release-day build. | Your approval is in the chat. |

### Phase 6: release day (you and Claude, one day)

| # | Who | Step | Done when |
|---|---|---|---|
| 6.1 | Claude, after your go-ahead | Tag `native-2`; `go run ./cmd/otapublish native` builds the APK and IPA from that tag, checks the signer and both fingerprints, uploads them, registers both runtimes, writes `policy.json` (and `altstore.json` if Q2 says so). You type the keystore and leaf passphrases. | `/api/app/latest` shows build 2 for both platforms after 6.2. |
| 6.2 | you | Set `DEPLOY_ENABLED` to `true` and run the Deploy workflow on `react` (Actions → Deploy → Run workflow). | `/api/version` shows the tag's commit; `curl -sI https://messages.alexanderjia.com/sound/RingTone.mp3` returns 200. |
| 6.3 | you | Install build 2 on your own iPhone and Android phone, sign in, turn on Beta updates, and test: a call each way (on the iPhone this is the first real test of CallKit), a voice message, an upload, a cold start. | Both phones appear in the census on build 2. |
| 6.4 | you | Hand out build 2 the same day. Android: uninstall, install from `https://messages.alexanderjia.com/app` (or `adb install` in person), sign in, allow notifications, "Install unknown apps", and background use. iPhone: install through the sideload tool. Send each user the `/app` link in a DM. | Every known user appears in the census on build 2. |
| 6.5 | Claude | Watch the census daily for 14 days and report phones still without the header. | 14 days with no legacy client. |

### Phase 7: first real OTA and the rollback drill (you and Claude)

| # | Who | Step | Done when |
|---|---|---|---|
| 7.1 | Claude, then you | Claude makes a small visible JS change. You run `go run ./cmd/otapublish ota --notes "First update"`, check it on your beta phones, then `otapublish promote <label>`. | Within 24 h the census shows every phone on the new label and no failure. |
| 7.2 | you, with Claude | On beta: `otactl halt`, `otactl rollback-embedded`, then `otapublish republish`. | Beta phones go to the built-in bundle, then to the republished update. |

### Phase 8: prove the native path (you and Claude)

| # | Who | Step | Done when |
|---|---|---|---|
| 8.1 | Claude, then you | `otapublish native` for build 3 (version 1.3.0, no functional change). | Android phones install it from inside the app; iPhones show the banner, and their users update through the source (or you reinstall them). |
| 8.2 | you | Set `min_build` 2 for both platforms in `policy.json` (`otactl policy`). | The census shows no phone below build 2. Header-less builds are unaffected. |

### Phase 9: hardening (Claude; server only, any time after Phase 6)

| # | Step | Done when |
|---|---|---|
| 9.1 | Admin login rate limit, and a custom header plus exact `Origin` check on every mutating admin route. Then the admin "Updates" section (pointers, installs, failures, Halt, Roll back to built-in). | Tests pass; a halt from the admin panel works on beta. |
| 9.2 | Automatic halt (D8) and a Web Push to admins when it fires. | The drill's failure report produces a halt and a push. |
| 9.3 | you: calendar reminders: replace the leaf every 12 months; rollback drill every 6 months; `otactl doctor` monthly. | Reminders exist. |
| 9.4 | you: in the Cloudflare dashboard, confirm that Bot Fight Mode does not challenge okhttp or CFNetwork requests to `/api/ota/manifest`. | Manifest checks keep appearing in the census from both platforms. |

**Not built, on purpose:** bsdiff patches (full bundles are 1.3–1.5 MB compressed); percentage rollouts; automatic rollback of phones (only automatic halt); VoIP push.

---

## 6. Day-to-day use once it is built

**Ship a JS fix:**
1. If the fix needs a server change, deploy the server first. The server must keep working for phones still on older updates.
2. Commit and push.
3. `go run ./cmd/otapublish ota --notes "Fixes the send button on iPad"` → publishes to beta and prints the label.
4. On your phone: Settings → Software Update → Check for Updates → Restart to Update; try the fix.
5. `go run ./cmd/otapublish promote <label>`.
6. Next day: `bin/otactl census` (or the admin page) shows every phone on the label and no failure.

**Ship a native change:** commit and push, then `go run ./cmd/otapublish native --notes "…"`. It bumps the build number N and version `1.N.0`, runs `prebuild --clean`, builds both platforms, checks the signer and fingerprints, uploads, registers the runtimes, updates `policy.json` and `altstore.json`, and tags `native-N`. JS fixes for phones still on the older runtime are published from a worktree at the older `native-N` tag with the fix cherry-picked.

**Numbering:** native build N, one counter for both platforms; version `1.N.0`; OTA label `YYYYMMDD.k`; git tags `native-N` and `ota-<label>`. Settings shows `Alex Messages 1.2.0 (2) · update 20261105.1`, or `· built-in`.

---

## 7. What users see

| Moment | What the user sees |
|---|---|
| An update downloads | Nothing. |
| An update applies (cold start, or back after ≥ 15 min away with nothing at risk) | About 1 s of a paper-coloured screen with the app icon, then the same conversation. |
| An update is critical or has waited 24 h | A banner on the DM list: "A new version of Alex Messages is ready." [Restart] [×] |
| Restart is blocked | Settings shows why: "Available after your call ends", or a list of what a restart would discard, with a confirmation. |
| A native update exists (Android) | A banner with [Install]; the system may ask for one tap. |
| A native update exists (iPhone) | "Alex Messages 1.3.0 is available. Update it in SideStore." [Open SideStore], if Q2 says SideStore/AltStore. |
| Below `min_build` | A full-screen "This version is no longer supported" with the install action. |

---

## 8. Questions only you can answer

1. **Q1.** Which key signed the APK on the Android phones? On one phone: `adb shell dumpsys package com.alexanderjia.messages | grep -A3 -i signatures`. (The plan assumes an uninstall is needed either way.)
2. **Q2.** How is the app installed on each iPhone: AltStore, SideStore, Sideloadly, or Xcode? With a free Apple ID? Does it refresh itself every 7 days today? This decides whether the AltStore source is built.
3. **Q3.** How many users and phones per platform? Which phones are yours, for beta? Is your account an admin in the app?
4. **Q4.** Android makers and versions: any Huawei/Honor (Pure Mode; HarmonyOS NEXT cannot install APKs), Xiaomi, or Android 11 and older?
5. **Q5.** Release day breaks images in today's apps until each phone installs build 2. Are a few hours of that acceptable?
6. **Q6.** Can you install build 2 in person with `adb` on the Android phones, or must users download it?
7. **Q7.** May Claude commit the current work (109 entries) to `react` in feature-grouped commits and push it to the public repo?
8. **Q8.** Is FileVault on? Which password manager holds the key backups, and where is the USB stick kept?
9. **Q9.** Is "apply automatically when the app comes back after 15 minutes away, if nothing is at risk" acceptable, or should updates apply only at cold start or on Restart?
10. **Q10.** Do the mainland-China users reach `https://messages.alexanderjia.com` reliably today, and can one of them run the test in step 0.5?

---

## 9. Still UNVERIFIED, and the step that settles each

| Claim | Settled by |
|---|---|
| Chain mode works on both platforms, including for directives | Phase 5, scenarios a, c, e |
| `Constants.expoConfig.extra` comes from the manifest's `extra.expoClient` under an OTA launch | scenario a (`session.tsx` falls back to the hard-coded server URL either way) |
| `Updates.manifest.extra.am` (label, notes, critical) is readable in JS | scenario a; fallback: put them in `metadata` as strings |
| The fingerprint file sits at `EXUpdates.bundle/fingerprint` (IPA) and `assets/fingerprint` (APK) | step 4.4 / 6.1 (`otapublish native` fails if not) |
| A reload does not replay the last notification tap or deep link | scenario j |
| Cloudflare passes `.js` assets byte-for-byte after decoding and does not challenge okhttp/CFNetwork | step 2.2 publish check through Cloudflare; step 9.4 |
| `USER_ACTION_NOT_REQUIRED` behaviour on the users' ROMs | scenario k on emulators; step 8.1 on real phones |
| SideStore reads the AltStore source format; AltStore's permission check accepts our IPA | step 6.4 on one iPhone first |
| Re-signing with a sideload tool keeps the app's data on reinstall | step 6.4 |
| Downloads from mainland China take an acceptable time | step 0.5 |
