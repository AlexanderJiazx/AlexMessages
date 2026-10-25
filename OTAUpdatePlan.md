# Over-the-air (OTA) updates for the Alex Messages mobile app: plan

Date: 2026-10-22. Branch: `react`. Paths are relative to the repo root unless they start with `/` or `~`.

This version applies the findings of `OTAUpdatePlan-Review.md` (4 reviewers: security-ops, codebase-facts, expo-facts, completeness-clarity). Section 10 lists what changed and the review findings that were not applied.

## 0. Status

**OTA updates do NOT work today.**
- `expo-updates` is not a dependency of `mobile/`.
- `mobile/app.json` has no `updates` block and no `runtimeVersion`.
- The server's update endpoint exists only in local commits (Phase 1, below). Production does not run it.
- `cmd/otactl` and `cmd/otapublish` exist only in local commits (Phase 2). They have run only in tests, against a temporary directory that stands in for the Pi. No key exists (Phase 3).
- The prebuild output switches updates off: `EXUpdatesEnabled` is false in `mobile/ios/AlexMessages/Supporting/Expo.plist`, and `expo.modules.updates.ENABLED` is false in `mobile/android/app/src/main/AndroidManifest.xml`.

**No phone that has the app today can ever receive an OTA update.** OTA needs native code (the expo-updates client) inside the app binary. Every phone must install one new binary by hand ("build 2", Phase 6). After that install, JS updates arrive by themselves.

**What this plan delivers once every phase is done:**
1. A JS/TS or bundled-asset change that you publish from your Mac reaches every phone on the matching native build without a reinstall. Phones on an older native build never receive it. The app downloads the update while it is open, without showing anything. It applies the update at one of three moments: the next cold start after the download; the first return to the app after at least 15 minutes in the background, when nothing would be lost; or when the user taps Settings → Software Update → Restart to Update. It never applies during a call.
2. Every OTA update and every rollback directive is signed with a key that never sits on the Pi, the GCP relay, Cloudflare or GitHub. A phone rejects JS that this key did not sign. **Not covered by that signature:** the replay of an older signed update to a phone that runs something even older (D4, "What the signature does not stop").
3. A bad update can be stopped in seconds over SSH without the key, and phones can be sent back to the JS built into their binary. On a phone whose update broke the updater itself, the user must open and close the app twice (D9).
4. Android (every user except you): build 2 is downloaded as an APK from `https://messages.alexanderjia.com/app`. From build 3 on, the user taps [Install] in the app's banner instead. On Android 12 and newer no system dialog follows; on Android 7–11 the system shows one confirm screen. iPhone (only your own): you download the IPA from the same `/app` page and install it through 万能签.

**What OTA can never do, whatever is built:**
- Change native code: `mobile/modules/*` (Swift/Kotlin), react-native-webrtc, InCallManager, any npm package with native code, permissions, Info.plist/AndroidManifest entries, entitlements, the app icon, the ringtone files in `mobile/assets/sounds/`, Android notification channel settings.
- Extend how long 万能签's signature on your iPhone stays valid.
- Update a phone whose app is never opened.

**What does not change:** the web client (`web/`), the legacy `static/` frontend and Alex Meet (`cmd/meet`) get no OTA feature and no visible change. The web build picks up the shared-core changes (`shared/`) and must still pass its build and e2e suite (Phase 4.3).

**Done so far:** research, this plan, a review of the plan, your answers (section 8), step 0.2 (local commit `37e425d`, 2026-10-22, on your Mac only, not on GitHub: the DM-calls work, the security fixes, `SecurityReview.md` and both plan files), and **Phase 1, steps 1.1–1.6, and Phase 2, steps 2.1–2.3 (both 2026-10-25), in local commits on `react` that are NOT pushed and NOT deployed**. Phases 1 and 2 changed parts of this plan; section 10 lists them under "Changes made while building Phase 1" and "Changes made while building Phase 2".

**Not done:** every step of Phases 0 and 3–9 (section 5) except 0.2. Your answers to the questions are in section 8; three items there are still open. Every claim in section 9 is unverified. Sections 6 and 7 describe use after the build; nothing in them works today.

---

## 1. Facts that shape this plan

Each fact was checked in the repo, in the expo-updates 57.0.26 source, or against a live endpoint on 2026-10-22.

1. **The installed binaries predate DM calls.** The IPA (`mobile/build/AlexMessages-previous.ipa`, 2026-10-07) and the APK (`mobile/dist/AlexMessages-1.0.0-unsigned.apk`, 2026-10-12) contain none of the call modules. Today's JS cannot run on them. Every phone needs a new binary anyway, with or without OTA.
2. **The Android release signing key is public.** `android/app/build.gradle` signs release builds with `debug.keystore`, which is byte-identical to the keystore inside the public `expo` npm template. Anyone can build an APK that Android accepts as an update of `com.alexanderjia.messages`.
3. **The DM-calls feature exists only on your Mac.** It is in local commit `37e425d` (2026-10-22), which is not on GitHub (`react` is 1 commit ahead of `origin/react` on 2026-10-25). A checkout of GitHub's `react` cannot build the app.
4. **The repo is public**, so GitHub Actions logs and artifacts are world-readable. `SecurityReview.md` (inside local commit `37e425d`) describes vulnerabilities that production still has ("Production has NOT received these changes").
5. **Continuous deployment fails before it reaches the Pi** (run 37958891653, 2026-10-09: host key verification). The repo variable `HEALTH_PORTS` is `8765 9090 9091`, but the units listen on `6001 6002 6003` (the default in `deploy.yml` is already right). **`DEPLOY_ENABLED` is `true`, so fixing the host key alone deploys on the next push to `main` or `react`.** `DEPLOY_SSH_KEY` (root-equivalent on the Pi) is a repository-level secret.
6. **Deploying the current server breaks images in today's apps.** DEPLOY.md, bullet "Updated mobile clients are required for protected attachments": older mobile builds request media without the bearer header and get 401. So the server deploy and the hand-out of build 2 must happen on the same day.
7. **Cloudflare sits in front of every public domain**, which DEPLOY.md does not mention. The path is phone → Cloudflare → GCP relay `35.226.215.209` → Pi nginx → Go. Cloudflare caches by file extension only (`.js`, `.png`, `.mp3`, `.apk`, `.bin` yes; extension-less paths such as `/api/ota/manifest`, and `.ipa`/`.json`, no), **including 404 answers**, unless the origin sends `Cache-Control: no-store` or `private`.
8. **Android rarely cold-starts.** The START_STICKY ring service keeps the process, and the JS runtime, alive after the app is swiped away. expo-updates' default "apply at the next cold start" may therefore never happen. The app has to reload itself at a safe moment.
9. **A reload ends a live call.** The server lets only the same device id resume a call, and a reload creates a new JS runtime with a new device id.
10. **Every build so far is version 1.0.0, build 1**, including the builds without call code.
11. **Bundle sizes of the shipped pre-call builds:** 3.8 MB (Android) and 4.4 MB (iOS) Hermes bytecode, about 1.3–1.5 MB with brotli (both clients decode brotli). Build 2's bundle is larger by an unmeasured amount (`otapublish ota --dry-run` prints it). A typical update downloads only the bundle, because assets already inside the binary are reused by hash.
12. **expo-updates for SDK 57:** this plan was checked against 57.0.26. `npx expo install expo-updates` writes the range `~57.0.22`, so Phase 4.1 pins `57.0.26` exactly.
13. **Debug builds never run updates.** Only Release builds (`npx expo run:ios --configuration Release`, `npx expo run:android --variant release`) can be used to test OTA.
14. **The client only accepts an update newer than what it runs.** It compares `createdAt` with the running update's time, and the embedded bundle's time is the moment the binary was built (the build machine's clock). An older update can never be pushed "back"; a rollback means republishing old content with a new time, or a signed `rollBackToEmbedded` directive. The client has no revocation list.
15. **`npx expo export` deletes its output directory first.** The default `dist` would delete `mobile/dist/` and the APK in it.
16. **The server cannot see a trustworthy client IP.** `webapp.NewEngine` uses `gin.New()` without `SetTrustedProxies`, so `c.ClientIP()` returns the left-most `X-Forwarded-For` value, which the client writes. The origin also answers requests that bypass Cloudflare.
17. **The domain is registered in mainland China.** `whois alexanderjia.com`: registrar Jiangsu Bangning Science & Technology (55hl.com), expiry 2028-05-25, status `ok` (no `clientTransferProhibited`).
18. **No repo file builds an IPA.** `mobile/package.json` has only start/android/ios/web scripts. `mobile/ios/AlexMessages/AlexMessages.entitlements` has `aps-environment = development` (added by expo-notifications), which a free Apple ID cannot sign. The app uses only local notifications.
19. **Claude's own command tool has no terminal**, so it cannot answer a passphrase prompt. Claude can start a command in your Terminal panel, where you type the passphrase.

---

## 2. The design in one picture

```
Your Mac (the only machine with keys)          Raspberry Pi: cmd/server                      Phone (build 2+)
───────────────────────────────────────        ──────────────────────────────                ─────────────────────
otapublish ota (built from a clean worktree    data/ota/   (never touched by deploys)        expo-updates (native)
  of a pushed commit)                            assets/<sha256>.<ext>  immutable              GET /api/ota/manifest
  npm ci --ignore-scripts → typecheck → tests    updates/<id>/manifest.json + .sig + .leaf    GET /ota/assets/<sha>.<ext>
  runtime registered? → expo export              channels/<ch>/<platform>/<runtime>.json      (through Cloudflare)
  build manifests → passphrase → SIGN            runtimes/, native/, certs/ (public)
  rsync (or tar) over ssh -p 2222 ──────────►   bin/otactl ingest (re-verifies signatures)     UpdateHost (JS)
  verify through Cloudflare, then activate       /api/ota/manifest only SELECTS stored bytes    checks on resume,
  archive in ~/AlexMessagesReleases            census.db = census of every install            reloads only when safe
```

The Pi never signs anything and holds no private key. It serves bytes that the Mac signed.

---

## 3. Decisions

### D1. Provider: self-host the Expo Updates protocol in the existing Go server

**Decision:** `cmd/server` serves Expo Updates protocol v1 at `messages.alexanderjia.com`. The client is Expo's own `expo-updates` 57.0.26. Manifests and directives are signed on the Mac at publish time.

**Why:**
- Updates use the same host and the same Cloudflare path as chat. A user who can chat, including from mainland China, can update (the download speed from China is not measured before build 2; section 8).
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
| `GET, HEAD /ota/assets/<sha256-b64url>.<ext>` | Update assets. `<ext>` is `js` for the launch bundle (served as `application/javascript`) and the asset's own extension otherwise; it must match `^[a-z0-9]{1,10}$`. | `public, max-age=31536000, immutable` |
| `GET /api/app/latest` | Native-build policy for both platforms (D10) | `no-cache` |
| `GET /app` | Install page: links, SHA-256 sums, instructions for Android and iPhone | `no-cache` |
| `GET, HEAD /app/android/AlexMessages-<version>-<build>.apk` | APK | `public, max-age=31536000, immutable` |
| `GET, HEAD /app/ios/AlexMessages-<version>-<build>.ipa` | Unsigned IPA; on the iPhone, open it in 万能签, which signs and installs it | `public, max-age=31536000, immutable` |

There is **no public `/api/version`**: in a public repo it would tell anyone which known bugs production still has. `bin/otactl version` (over SSH) prints the commit the deployed binaries were built from.

Rules for every answer on these paths:
- 400 and 404 answers carry `Cache-Control: no-store`, so Cloudflare never caches a 404 for an asset or APK that is uploaded later.
- Every `/ota/assets/` and `/app/` file answer carries `X-Content-Type-Options: nosniff`, `Content-Security-Policy: sandbox` and `Content-Disposition: attachment` (expo-updates ignores them; they stop an SVG/HTML/XML asset from rendering on the web app's origin).
- The server never sends `expo-manifest-filters` or `expo-server-defined-headers`. These headers are not signed and phones store them.
- Every file name is used once. A rebuilt binary that was already uploaded gets the next build number; a binary that was never uploaded may be rebuilt under the same number (no cache has seen its URL).

Asset URLs in a manifest are **absolute** (`https://messages.alexanderjia.com/ota/assets/…`). The client does not resolve relative URLs.

Why `.js` and real extensions: Cloudflare then caches and brotli-compresses the assets with no dashboard rule. The client checks every asset's SHA-256 after decoding, and the publisher checks every asset through Cloudflare before activating an update (D6 step 16), so a transforming CDN cannot slip in changed bytes.

**Manifest endpoint logic:**
1. Validate headers. `expo-protocol-version` must be `1`, `expo-platform` must be `ios` or `android`, and `expo-runtime-version` must match `^[a-zA-Z\d][a-zA-Z\d._+()-]{0,254}$`. Anything else gets 400.
2. If the environment variable `OTA_DISABLED=1` is set (the kill switch, D9), answer `204` with `expo-protocol-version: 1`.
3. Pick the channel: `beta` if `Expo-Extra-Params` has `am_channel="beta"` and a beta pointer exists for this platform and runtime; otherwise `production`.
4. Read the pointer `data/ota/channels/<channel>/<platform>/<runtime>.json` and answer:

| Pointer state | Answer |
|---|---|
| no pointer, `halted`, or the runtime is not registered | `204` with `expo-protocol-version: 1` |
| `update`, and the update id is in `revoked.txt` | `204` |
| `update`, and `Expo-Current-Update-ID` equals it | `204` |
| `update` | `multipart/mixed`: the `manifest` part (stored bytes, stored `expo-signature` part header) and a `certificate_chain` part (the leaf named in `manifest.leaf`) |
| `rollback`, and the phone already runs its embedded bundle | `204` |
| `rollback` | `multipart/mixed`: the `directive` part (the pre-signed `rollBackToEmbedded`) and a `certificate_chain` part (the leaf named in `rollback.leaf`) |

`revoked.txt` blocks only the `update` state. A `rollback` pointer serves the directive even though its update id is revoked: `rollback-embedded` (D9) puts the bad id into `revoked.txt` and points at that same update's directive, and the directive is how phones leave the bad update.

5. Record the census row (D8). No row is written when step 2 (`OTA_DISABLED`) answered, or when `data/ota/FORMAT` is missing (the store is then treated as absent and every request gets `204`).

Response headers on every answer: `expo-protocol-version: 1`, `expo-sfv-version: 0`. The server ignores `A-IM: bsdiff` and always sends full files; both clients accept that.

**Storage on the Pi:** `/home/alexander/alexmessage/data/ota/`. `bin/otactl init` creates it. The env var `OTA_DIR` overrides the path for the server, but it stays unset on the Pi: `bin/otactl` runs over SSH, which never reads the units' environment file, so `otactl` finds the store as `../data/ota` next to its own executable (`~/alexmessage/bin/otactl` → `~/alexmessage/data/ota`). `--ota <dir>`, or `OTA_DIR` in the same shell, overrides that for tests.

```
data/ota/
  FORMAT                                          "1"
  certs/ota-root-2026.pem, leaf-<yyyymm>.pem      public certificates only (the server finds a leaf by the SHA-256 in a
                                                  .leaf file among certs/leaf-*.pem, so the name may carry a suffix)
  assets/<sha256-b64url>.<ext>                    write-once; never deleted (the spec forbids removing them)
  updates/<updateId>/manifest.json                the exact signed bytes
                     manifest.sig, manifest.leaf  the expo-signature value (sig="…", keyid="root-2026", alg="rsa-v1_5-sha256");
                                                  lowercase hex SHA-256 of the DER of the leaf that made it
                     rollback.json                rollBackToEmbedded, commitTime = createdAt + 1 s
                     rollback.sig, rollback.leaf
                     info.json                    {"platform","runtime","label","commit","created_at","notes","critical"};
                                                  created_at is exactly the manifest's createdAt string
  runtimes/<platform>/<runtime>.json              native builds registered for this runtime, with their embedded update id:
                                                  {"platform","runtime","builds":[{"build","version","embedded_update_id",
                                                  "commit_time","commit","registered_at"}]}
  channels/<production|beta>/<platform>/<runtime>.json
                                                  {"state":"update"|"rollback"|"halted","update":"<id>","since":"<RFC 3339>"};
                                                  a beta pointer may also be {"state":"test_unsigned","update":"<id>","since":…,
                                                  "until":"<RFC 3339>","previous":{<the pointer it replaced>}} (step 6.4)
  test/<id>/manifest.json, info.json              the unsigned manifest that otactl test-unsigned serves (step 6.4)
  revoked.txt                                     update ids, one per line ("#" starts a comment), that are never activated
                                                  or served as an update again
  native/policy.json, native/android/*.apk, native/ios/*.ipa
  census.db                                       SQLite census (D8); not in alexmessage.db
  incoming/<publishId>/                           upload staging (same filesystem, so ingest is a rename)
  log/actions.log                                 append-only, one JSON line per otactl command that changed the store
  .lock                                           held by every otactl command that changes the store
```

- Pointers are written to a temp file and renamed, so every write is atomic. A halted pointer keeps the id of the update it halted.
- **Server deploys never touch `data/ota/`.** `release.tgz` never contains `data/`, and `remote_deploy.sh` only copies the database out of it. A new test in `deploy/remote_deploy_test.py` checks that `data/ota/` is byte-identical after a deploy and after a forced rollback.
- **A server rollback does not move the channels.** It swaps only `bin/` and `web/dist/`. A rolled-back server older than the OTA routes answers 404, and phones keep the update they have.
- **Pointers are files, not database rows.** Restoring `alexmessage.db` from `backups/` would otherwise silently move every channel.
- **Which copy is authoritative:** the Mac archive `~/AlexMessagesReleases/ota/` for the write-once files (certificates, assets, updates, runtimes, native builds); the Pi for the pointers, `revoked.txt` and `policy.json`. Every pointer change that `otapublish` makes is appended to `~/AlexMessagesReleases/ota/pointers.log` on the Mac. A command run on the Pi itself (`halt`, `rollback-embedded`, `revoke`, `policy`, `test-unsigned`) changes only the Pi. `otapublish audit` compares the two copies, reports every difference (including a pointer in `rollback` state that the Mac did not set) and exits 1 when anything differs; assets are compared by size (the name is the hash), registered builds without `registered_at`, and an expired leaf or an update whose signatures no longer verify is reported as history, not as a difference. `otapublish audit --record`, after a typed `yes`, stores the Pi's pointers, revoked ids and policy as the Mac's record (`pointers.log`, `ota/revoked.txt`, `ota/native/policy.json` in the archive); it never records a loss and never touches the write-once files.
- **Backup and restore:** the Mac archive must be inside a backup you choose in Phase 3.0 (for example Time Machine). After a lost SD card: `bin/otactl init`, then `otapublish restore-pi`. It uploads every write-once file the Pi lacks, registers the archived native builds, adds the recorded revoked ids, writes the recorded policy if the Pi has none, and writes every recorded pointer the Pi lacks (a `test_unsigned` pointer as its `previous`); it asks for `yes` once and runs `otactl doctor` at the end. It never replaces anything the Pi has, and it refuses when the Pi holds a write-once file with other bytes. An update signed by an expired leaf cannot be ingested again (its chain no longer verifies), so it and the pointers that need it are not restored: publish or republish instead. A halt or rollback made on the Pi after the last `audit --record` is not restored.
- **Disk:** about 8 MB per OTA publish and about 51 MB per native release, never deleted. `otactl doctor` warns when the filesystem that holds `data/ota` has less than 1 GB free.

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
1. `otapublish native` reads the runtime **out of the built artifacts**: the runtime setting (`EXUpdatesRuntimeVersion` in the IPA's `Expo.plist`; in the APK the meta-data `expo.modules.updates.EXPO_RUNTIME_VERSION`, a reference to `@string/expo_runtime_version`) is `file:fingerprint`, and expo-updates then reads the runtime, byte for byte, from `Payload/AlexMessages.app/EXUpdates.bundle/fingerprint` in the IPA or `assets/fingerprint` in the APK (unverified on a build with expo-updates until step 5.3; section 9). It compares each with `npx --no -- expo-updates runtimeversion:resolve --platform <p>` on the same clean tree and aborts on any difference. It then runs the artifact checks of D10 ("Checks on every native artifact"). Only then does it register `runtimes/<p>/<runtime>.json` on the Pi.
2. `otapublish ota` resolves the runtime of the tree it exports. It publishes to a platform only if that runtime is registered. Otherwise it stops with: "Native code changed since the last native build of `<p>`. Run `otapublish native`."
3. `otactl activate` on the Pi refuses an update whose runtime is not registered, and the manifest endpoint answers 204 for an unregistered runtime.
4. Every native build and every export runs in a clean `git worktree` of a commit that is contained in `origin/react`, `origin/main`, or `origin/release/native-N` (the backport branch of the runtime being published, section 6). Untracked files cannot shift the fingerprint. Three exceptions accept a local commit: `--profile drill` (Phase 5), `otapublish native --no-upload --allow-local` (the pre-release build of Phase 5.3) and `otapublish ota --dry-run --allow-local` (a dry run uploads nothing); such artifacts can be uploaded only after their commit is on `origin`.
5. Native builds come from the newest commit that contains everything already published by OTA for that runtime. A binary built later from older JS would ignore every update published before its build time.
6. `otapublish` refuses to run while `AM_OTA_PROFILE`, any `EXPO_PUBLIC_*` variable, or any other variable that `app.config.js` reads is set, and sets `EXPO_NO_DOTENV=1`. The Xcode and Gradle steps re-evaluate the config to compute the fingerprint, so a stray variable would change the shipped binary.

**Rejected:** `appVersion` (every build so far is 1.0.0, including the builds without calls); `nativeVersion` or a hand-written string (correct only if someone remembers to bump it, and this repo changes three native modules often; a forgotten bump produces the "undefined is not a function" crash CLAUDE.md describes).

### D4. Code signing and key custody

**Decision: a two-level certificate chain**, which both clients support (`certificate_chain` response part).

| | Root | Leaf |
|---|---|---|
| Key | RSA-4096 | RSA-3072 |
| Certificate | self-signed; `basicConstraints CA:true, pathlen:0`; keyUsage `keyCertSign` | keyUsage `digitalSignature`; EKU `codeSigning` |
| Validity | 20 years, `notBefore` 24 h in the past | 400 days, `notBefore` 24 h in the past, never past the root's `notAfter`; replaced every 12 months |
| Where the certificate lives | committed at `mobile/certs/ota-root-2026.pem`, embedded in every binary; a copy at `data/ota/certs/ota-root-2026.pem` | `data/ota/certs/leaf-<yyyymm>.pem` on the Pi; sent in every manifest response |
| Where the private key lives | encrypted with a passphrase (age), on one USB stick, plus one copy in your password manager. Never on the Mac's disk. | encrypted with a different passphrase (age) at `~/.alexmessages/keys/ota-leaf-<yyyymm>.key.age` on the Mac |
| Where the passphrase lives | in your head, and on paper stored with the USB stick (not in the password manager that holds the encrypted key) | in your head, and on paper stored with the USB stick |
| Used for | issuing a leaf, about once a year | signing every manifest and rollback directive |

- app.json: `codeSigningCertificate: "./certs/ota-root-2026.pem"`, `codeSigningMetadata: {"keyid": "root-2026", "alg": "rsa-v1_5-sha256"}`.
- No app.json key turns on chain mode. A small config plugin, `mobile/plugins/withOtaCertificateChain.js`, sets `EXUpdatesCodeSigningIncludeManifestResponseCertificateChain` (iOS `Expo.plist`) and `expo.modules.updates.CODE_SIGNING_INCLUDE_MANIFEST_RESPONSE_CERTIFICATE_CHAIN` (Android manifest) to true.
- **A binary without the certificate verifies nothing.** expo-updates skips every signature check when no certificate is embedded, and accepts unsigned manifests when `EXUpdatesConfigCodeSigningAllowUnsignedManifests` (iOS) or `expo.modules.updates.CODE_SIGNING_ALLOW_UNSIGNED_MANIFESTS` (Android) is true. So every shipped artifact is checked (D10, "Checks on every native artifact").
- `mobile/.gitignore` ignores `*.pem`; add exactly `!certs/ota-root-2026.pem`. CI (`mobile-checks.yml`, test `TestNoPrivateKeysTracked`) fails if any tracked file holds a PEM private key (a line that matches `-----BEGIN ([A-Z0-9]+ )*PRIVATE KEY-----`) or ends in `.key`, `.p12`, `.pfx`, `.jks` or `.keystore`. A search for the bare words `PRIVATE KEY` would also match this plan and the code that writes PEM files.
- **Key files** are ASCII-armored age files (scrypt passphrase), created with `O_EXCL` and mode 0600. The root key is `ota-root-2026.key.age`; `keys init-root --out` must be a folder under `/Volumes/` on another device than your home folder. Leaf names are `<yyyymm>`, then `<yyyymm>-2`, `<yyyymm>-3`… for more leaves in the same month: the key `ota-leaf-<name>.key.age` and the certificate `ota-leaf-<name>.pem` in `~/.alexmessages/keys/`, and `leaf-<name>.pem` in the archive and on the Pi. `keys issue-leaf` refuses a leaf passphrase equal to the root's.
- **Signing happens on the Mac only**, inside `otapublish`. It signs the exact manifest bytes and that update's `rollBackToEmbedded` directive, records the signing leaf in `manifest.leaf` and `rollback.leaf`, and checks both signatures against the root before uploading. `otactl ingest` checks them again on the Pi with Go's `crypto/x509`, using the client's rules. The signing code lives in `internal/ota/sign`, imported only by `cmd/otapublish`; verification lives in `internal/ota`. `TestServerBinariesNeverImportSigning` fails if `cmd/server`, `cmd/admin`, `cmd/meet` or `cmd/otactl` imports `internal/ota/sign`, its test fixtures, or `filippo.io/age`.
- **Leaf rotation needs no new binary:** plug in the USB stick, run `otapublish keys issue-leaf`, then `otapublish resign-active`. It re-signs with the new leaf, for every update that any pointer references (production and beta, every platform and runtime) and for the last 3 updates per platform and runtime, both `manifest.json` and `rollback.json`; the bodies stay byte-identical, and the `.leaf` files are updated. `otactl activate` and `otactl rollback-embedded` refuse a signature whose leaf expires within 30 days; `otactl doctor` checks the leaf of every pointer-referenced manifest and of its rollback directive.
- **What a lost leaf costs:** issuing a new one from the root. **A lost root** costs a reinstall of your own iPhone. Android phones get a binary with a new root through `app-updater` (D10), because that install is signed with the Android key, not the OTA key.
- **Fallback, decided now:** if the local drill (Phase 5) shows that chain mode fails on either platform, build 2 instead embeds one self-signed RSA-3072 code-signing certificate valid for 20 years, with its key stored like the root.

**What the signature does not stop** (each needs control of the delivery path: Cloudflare, the relay or the Pi):
- **Stolen leaf:** someone who also controls the delivery path could push code until that leaf expires, at most 400 days.
- **Replay, with no key at all:** anyone on the delivery path can serve any update signed by a non-expired leaf to a phone whose running update is older, including a revoked update. Phones that applied a rollback directive are protected (the directive moved their embedded time past the bad update). D9 step 6 shortens this exposure by publishing a fix promptly.
- **Freeze:** anyone on the delivery path can answer 204 and stop all updates.
- **Fleet-wide rollback:** the pre-signed rollback directives sit on the Pi (chosen so the emergency rollback needs no key). Anyone who controls the Pi, the relay or Cloudflare can send every phone back to its built-in JS until you publish from the Mac again. `otapublish audit` reports such a pointer.
- **Limit of the encryption:** it protects the keys against a process that reads files. It does not protect against malware that stays on the Mac and records the passphrase.

**Rejected:** signing on the Pi or in a request handler (a Pi compromise would become code execution on every phone); a GitHub Actions secret (public repo, public logs); `npx expo-updates codesigning:generate` (it makes an RSA-2048 certificate that cannot act as a chain root, and writes an unencrypted `private-key.pem`); an unencrypted key file with mode 0600 (every npm package that runs on the Mac runs as the same user); keeping the rollback directives only on the Mac (the emergency rollback would then need the Mac).

### D5. Channels

**Decision:** two channels, `production` and `beta`. No percentage rollouts.
- `updates.requestHeaders = {"expo-channel-name": "production"}` is baked in and **never changed**. A changed header value in a later binary deletes every downloaded update.
- The app never calls `Updates.setUpdateRequestHeadersOverride` or `Updates.setUpdateURLAndRequestHeadersOverride` (the first works even with anti-bricking on, and would wipe the downloaded updates). A unit test greps `mobile/` for both names.
- A phone joins beta through `Updates.setExtraParamAsync("am_channel", "beta")`. The client sends it as `Expo-Extra-Params` on every manifest request. The switch is in Settings → Software Update and is shown only to admins. The app stores the choice in its own settings and calls `setExtraParamAsync` at every launch, because expo-updates deletes extra params when a later binary changes the update URL or request headers.
- The channel a phone uses is read from `(await Updates.getExtraParamsAsync()).am_channel ?? "production"`, never from `Updates.channel` (that always reads the baked `production` header).
- `otapublish ota` publishes to beta by default. `otapublish promote <label>` points production at the **same update id**, with no rebuild and no re-signing. It refuses unless, for each platform, an approved admin's bearer session reported `update=<that beta id>` in its `X-AM-Client` header (D8 item 2) and no authenticated failure report exists for it. It prints which platform is missing. `--force` skips that check for an urgent fix.
- A phone that leaves beta stays on its newer beta update until production publishes something newer.

**Rejected:** percentage rollouts (10% of a few dozen phones measures nothing); a channel baked per binary (separate builds, and switching wipes the cache); a promote gate on the anonymous census (anyone can forge it).

### D6. Publish pipeline

**Decision:** everything that needs a key runs on your Mac. `cmd/otapublish` is a Go program that is never deployed; it shares the manifest and verification code in `internal/ota` with the server and `otactl`, so the publisher and the server cannot drift apart. Files reach the Pi over `ssh -p 2222` to the relay, which bypasses Cloudflare. GitHub Actions only runs checks (typecheck and tests, no secrets).

**Who runs it:** Claude starts every `otapublish` command that needs a key (`ota`, `republish`, `resign-active`, `native`, `keys …`) in your Terminal panel, and you type the passphrase there. Claude never runs a keyed command through its own command tool (fact 19). Claude may run `--dry-run`, `status` and `audit` by itself. `promote` needs no key. `audit --record` and `restore-pi` need no key but wait for you to type `yes`.

**What `go run ./cmd/otapublish ota --notes "<text>" [--channel beta|production] [--critical] [--dry-run]` does, in order.** It stops at the first failure, and nothing goes live before step 16.
1. Check that the commit is contained in an allowed branch (D3 item 4; `git fetch` first) and create a clean `git worktree` of it in a temp directory.
2. Build `otapublish` again inside that worktree (`go build -ldflags "-X main.commit=<HEAD>"`) and run the rest from that binary. It refuses to continue unless its embedded commit equals the worktree HEAD. The `go run` copy from your working tree only does steps 1 and 2, so an edited or tampered checkout never touches a key.
3. Refuse if a Deploy workflow run is in progress (`gh run list --workflow Deploy --status in_progress`), and refuse the variables of D3 item 6.
4. Refuse if the Mac's clock differs by more than 2 minutes from the Pi's (`ssh … date +%s`) or from the `Date` header of `https://messages.alexanderjia.com/`. A clock that runs ahead would stamp an update in the future, and phones would then ignore every correct update until that time.
5. `cd shared && npm ci --ignore-scripts && npm run typecheck && npm test`.
6. `cd mobile && npm ci --ignore-scripts && npx --no -- tsc --noEmit`. Every `npx` call of `otapublish` is `npx --no -- …`: it runs only a package that `npm ci` installed and never downloads one.
7. For each platform: `npx --no -- expo-updates runtimeversion:resolve --platform <p>`, and ask the Pi (`ssh … bin/otactl runtimes --json`) whether it is registered. Unregistered platforms are skipped with the message from D3.
8. Server compatibility: compare `ssh … bin/otactl version` with the commit. If `internal/` or `cmd/server` differ, print the diff summary and require typing `yes`.
9. Native-contract warning: print `git diff --stat` since the registered binary's commit for `mobile/src/call`, `mobile/modules`, `mobile/src/notify.ts`, `mobile/src/session.tsx`, `mobile/app.json`, and require `yes` if anything changed.
10. `npx --no -- expo export --platform ios --platform android --output-dir <temp>/export --dump-assetmap --source-maps`. Never `mobile/dist`. Print the bundle sizes.
11. `npx --no -- expo config --json --type public` → `extra.expoClient`.
12. Build one manifest per platform: random UUIDv4 `id`; `createdAt` = now; each asset with `hash` (SHA-256, base64url, no padding), `key` (MD5 hex, so assets already inside the binary are not downloaded again), `contentType`, `fileExtension`, absolute `url`; `metadata: {}` (always empty: non-empty metadata would let the unsigned `expo-manifest-filters` header force a downgrade); `extra: {expoClient, am: {label, commit, notes, critical}}`. Refuse if `createdAt` is not later than both the current pointer's update and the registered binary's build time.
13. List every process of your user whose executable is `node`, `npm`, `npx`, `esbuild`, `watchman`, `java` or `gradle` (`ps -U $(id -u) -o pid=,etime=,comm=`). Such a process that started after this publish began comes from its own build steps: wait up to 10 s for it to exit, then refuse. Such processes that were already running when the publish started (Metro, a dev server, an editor's language server) are listed, and you must type `yes`; close the ones you do not need first. (Refusing every such process would block every publish while any long-running `node` program is open.) Then ask for the leaf passphrase on the terminal; sign each manifest and its rollback directive; write the `.leaf` files; verify both signatures against the root certificate.
14. Copy into the Mac archive, then upload to `data/ota/incoming/<publishId>/` with `rsync --ignore-existing`. If rsync is not available on the Pi (or on the Mac), use `tar -C <dir> -cf - . | ssh … 'mkdir data/ota/incoming/<publishId> && tar -C data/ota/incoming/<publishId> -xf -'`, with `COPYFILE_DISABLE=1` and, on macOS only, `--no-mac-metadata` (GNU tar has no such option), so no `._` files reach the Pi; `incoming/<publishId>` is always a new directory, and `otactl ingest` enforces that published bytes are never overwritten.
15. `ssh … bin/otactl ingest <publishId>`: re-verify signatures, leaves and hashes; refuse a manifest whose `metadata` is not `{}` or whose `createdAt` is more than 5 minutes ahead of the Pi's clock; move assets and updates into place.
16. Download every asset through Cloudflare and check its SHA-256. On a mismatch, print the exact URL to purge (Cloudflare dashboard → Caching → Configuration → Custom Purge) and stop. Only then `otactl activate --channel <ch> …` flips the pointer and appends to `pointers.log`. **The update is live from this moment.**
17. Smoke test: request the manifest through Cloudflare with phone headers (and `am_channel` for beta), check the id and signature. On failure, restore the previous pointer and exit non-zero.
18. `git tag ota-<label>`; print who gets it and when.

`--dry-run` runs steps 1–12 with no key and no upload, and prints the warnings of steps 8 and 9 without asking for `yes`. `--allow-local` (with `--dry-run` only) accepts a commit that is not on origin. `--fixture <file>` (with `--dry-run` only) replaces steps with prepared data (an export, the runtimes, the config, the npm and Deploy checks); the Go tests and the check of step 2.2 use it, because before Phase 4 the app has no expo-updates. `--pi-dir <dir>` uses a local update store instead of the Pi (dry runs and the drill); a publish into it also needs `--public-url <url>` (the server that phones talk to), except in the drill.

**Drill profile** (`otapublish ota --profile drill`, Phase 5): uses throwaway keys in `~/.alexmessages/drill-keys/`, the asset base URL `http://localhost:8799` for iOS and `http://10.0.2.2:8799` for Android, copies into the local scratch `OTA_DIR` (given as `--pi-dir`; the drill never uses the Pi) instead of uploading, accepts a local commit in step 1, and skips steps 3 (deploy check), 4, 8, the process check of 13, 16, 17 and the git tag of 18. Its keys use a fixed passphrase written in the code (drill keys protect nothing), and it archives into `~/AlexMessagesReleases/drill/`, apart from real updates.

**Rejected:** publishing from GitHub Actions (key in a public-repo CI; a Linux-computed iOS fingerprint is not proven to match the macOS build); uploading through the admin panel (Cloudflare's 100 MB request cap, weak admin login protections); a separate macOS user for the npm steps (stronger, but more setup than the process check; can be added later).

### D7. When and how a phone applies an update

**Native configuration (baked into build 2):** `enabled: true`, `checkAutomatically: "ON_LOAD"`, `fallbackToCacheTimeout: 0`, `useEmbeddedUpdate: true`, `disableAntiBrickingMeasures: false`.
- `ON_LOAD` makes the native code check at every cold start, independent of JS. That is the self-repair path if a bad update breaks the JS updater: one cold start downloads the fix, and the following cold start runs it.
- `0` means a cold start never waits on the network (important for China and for answering a call from a cold start). It is also why a downloaded update runs only from the next launch.
- `disableAntiBrickingMeasures: false` means JS can never repoint the update URL. (JS could still change the channel header; D5 forbids that call.)

**JS: `mobile/src/updates/UpdateHost.tsx`**, mounted in `app/_layout.tsx` inside `<SessionProvider>` next to `<CallHost>`, inside its own error boundary. It does nothing in debug builds, on Expo web, or when `Updates.isEnabled` is false. It never checks or reloads in the `app/index.tsx` boot gate. `UpdateHost` is the **only** place in the app that calls `Updates.reloadAsync`.
- **Check:** when the app becomes active and the last check (native or JS) is at least 30 minutes old, call `checkForUpdateAsync()`, then `fetchUpdateAsync()` if one is available. A downloaded rollback directive is handled the same way.
- **Automatic apply:** when the app returns to `active` after **at least 15 minutes in the `background` state**, an update or rollback is downloaded (`useUpdates().isUpdatePending`), and the guard is clear:
  1. wait 1.5 s, so a notification tap or call action can arrive;
  2. run the guard again;
  3. save the open conversation to AsyncStorage key `ota:restore` with a timestamp;
  4. flush the DebugReporter and wait for its POST to finish, at most 1 s (new: `DebugReporter.flush()` and `ApiClient.debugReport()` return the fetch promise);
  5. check `store.state.call` and `isCallLive()` once more, synchronously;
  6. call `Updates.reloadAsync({reloadScreenOptions: {backgroundColor: "#F6F4EE", image: require("../../assets/icon.png"), fade: true}})`.

  After the reload, boot reopens the saved conversation if the record is less than 60 s old.
- **Cold start:** expo-updates launches the newest downloaded update by itself.
- **Manual:** Settings → Software Update → **Restart to Update**, shown while an update is pending.
- **Critical update** (`extra.am.critical: true`), or an update pending for more than 24 h: a dismissible banner on the DM list with a Restart button.

**Reload guard: `mobile/src/updates/guard.ts`.** Hard blockers stop every reload, including a manual one:
1. a call that is ringing or live: `store.state.call` with phase other than `ended`, or `isCallLive()`;
2. Android: the app is shown over the lock screen (`lockedOverCall()`), or a call notification was tapped in the last 60 s, or an Answer is still waiting. New `msSinceCallAction(): number` is exported from all three of `src/call/system.android.ts` (milliseconds since the last Answer/open/chat action from `CallRinger`; the value moves from the `attachSystemCalls` closure to module scope), `src/call/system.ios.ts` and `src/call/system.ts` (both return `Number.POSITIVE_INFINITY`), so the three files keep identical exports;
3. an APK install in progress (D10).

Soft blockers stop the automatic apply. A manual restart lists them and asks first:
- a composer draft, an upload in progress, staged attachments, or a reply target;
- a message with `_status` `sending` or `failed`, or a queued `message`/`edit` frame (new `ChatSocket.pendingCount(types)` in `shared/src/socket.ts`, read by new `ChatStore.hasUnsentWork()` in `shared/src/store.ts`; typing and read frames do not count);
- a recording, a recorded voice message not yet sent, or media playing (new read-only `isMediaActive()` in `src/media.ts`);
- unsaved profile edits; text typed on the login screen; a data export in progress;
- an activity outside the app is open: image or document picker, camera, share sheet, `WebBrowser`, a system settings page, a permission dialog (each awaited call is wrapped in `holdReload("external")`);
- iOS: the native image viewer or a UIKit context menu is presented (`holdReload("native-ui")`);
- a route other than `/chat` or `/conversation` is on screen (automatic apply only);
- the session `phase` is not `ready`.

Each blocked automatic apply logs `ota.blocked {reasons}` at most once per hour per update.

**Rejected:** reloading right after the download (interrupts the user); reloading in the background (Expo calls it experimental; iOS suspends the app within seconds); applying only at cold start (Android almost never cold-starts); a 3-minute threshold (coming back from a picker or the camera after 3 minutes would reload and lose the picked file); `WIFI_ONLY` (China users on mobile data would never update); `fallbackToCacheTimeout > 0` (blocks launch on slow networks).

### D8. Telemetry: who runs what, and what failed

1. **Census from the manifest request (server).** Every manifest request upserts one row in `data/ota/census.db` (its own SQLite file, so a flood cannot fill `alexmessage.db`), keyed by `EAS-Client-ID` (a per-install UUID both clients send). It stores platform, runtime, current and embedded update ids, channel, `Expo-Recent-Failed-Update-IDs`, `Expo-Fatal-Error` (first 1024 characters), the claimed user id (extra param `am_uid`, set after sign-in and cleared at logout; a hint only), first and last seen. Limits, because every field is unauthenticated:
   - a row is created only when `Expo-Embedded-Update-ID` equals the embedded update id of a registered binary (recorded by `otapublish native`);
   - at most 5,000 rows; when full, existing rows are still updated, new ones are dropped, and one `ota.census_full` debug event is logged per hour;
   - header formats and sizes are validated; IPs are not stored; rows unseen for 90 days are pruned by the existing maintenance sweep;
   - no per-IP rate limit, because the server cannot see a trustworthy client IP (fact 16).

   A new failed id or fatal error emits a debug-console event `ota.client_failure`. **The census is for reading only.** No gate and no automatic action uses it.
2. **`X-AM-Client` header (build 2).** Value: `v=1;platform=android;version=1.2.0;build=2;runtime=<fp>;update=<uuid|embedded>;channel=<am_channel or production>`. `ApiClient` sends it on every REST call (new `clientInfo` option in `shared/src/api.ts`); the `/ws` upgrade carries it as the `client=` query parameter (`shared/src/socket.ts`); `RingSocket.kt` sends `v=1;platform=android;build=2;component=ring`. The RingSocket part is native, so it must be in build 2. The server stores it per session (`sessions.client`, `sessions.client_seen_at`): at once when the value changes (so a newly applied update id reaches the promote gate without delay), otherwise at most one write per 10 minutes. The ring service's value is never stored: it shares the app's session and carries less. A value that does not parse counts as absent: it is not stored and never gated. Because it arrives on an authenticated session, it is the data the promote gate (D5) and the automatic halt (below) use. The web client sends nothing and is never gated.
3. **Debug-console events from the app.** First give the mobile `DebugReporter` a per-install session id: `session.tsx` creates it without `sessionStorage`, so today its session id is a new random 8-character value after every launch and every OTA reload, and events before and after an update cannot be joined. Pass a storage adapter backed by the hydrated prefs map, and expose the reporter from `Session`. Events: `ota.boot {updateId, label, runtime, build, isEmbeddedLaunch, isEmergencyLaunch, emergencyLaunchReason, channel}`, `ota.check`, `ota.download {durationMs, bytes}`, `ota.apply {trigger}`, `ota.blocked`, `ota.error` (with a short summary of `Updates.readLogEntriesAsync(3600000)`), `apk.download`, `apk.install`.
4. **Crash breadcrumb.** A JS global error handler writes `{updateId, ts, message, stack ≤ 2 KB}` to a file and reports it as `ota.crash` at the next launch. It **must save the previous handler and always call it**: expo-updates' automatic rollback depends on the fatal error reaching React Native's fatal handler.
5. **Personal data.** Logout calls `Updates.setExtraParamAsync("am_uid", null)`. `POST /api/me/delete` deletes the census rows whose `am_uid` is that user; `GET /api/me/export` includes them. The admin pages render every census field as text (`textContent` or `escapeHTML`), never as HTML.

**Where you read it:** first a read-only admin endpoint `GET https://admin.alexanderjia.com/api/ota/installs` (JSON) and `bin/otactl census` over SSH (the Pi has no `sqlite3`); later an "Updates" section in the admin panel (Phase 9).

**Automatic halt (Phase 9, server only, beta channel only):** the beta pointer switches to `halted` when at least 2 distinct approved users report the active beta update as failed over an authenticated channel (an `ota.crash` or `ota.error` event sent through REST by a signed-in user, deduplicated per user), within 60 minutes. Census headers and anonymous debug reports never count. Production halts stay manual. A halt stops further downloads; it does not roll back phones that already have the update.

### D9. Rollback of a bad update

**Emergency, no key needed.** Use your normal SSH key from the Mac, or a separate emergency key (Phase 3.4) that the Pi restricts to `bin/otactl emergency`, so a phone with an SSH app never carries a root-equivalent key. Each command prints every pointer it changed.
1. **Stop:** `ssh -p 2222 alexander@35.226.215.209 'cd ~/alexmessage && bin/otactl halt --all'` sets every pointer (every channel, platform and runtime) to `halted`. Targeted: `bin/otactl halt --label <label>` halts every pointer (production and beta, both platforms) that references that label's updates. Phones that have not downloaded the bad update stop getting it at once. With the emergency key: `ssh -p 2222 -i <emergency key> alexander@35.226.215.209 halt`.
2. **Roll back:** `ssh -p 2222 alexander@35.226.215.209 'cd ~/alexmessage && bin/otactl rollback-embedded --label <label>'`. Every pointer that references the label's updates (production and beta, both platforms) switches to that update's pre-signed `rollBackToEmbedded` directive (its `commitTime` is the bad update's `createdAt` + 1 s, so it applies to that update and to anything older). The bad ids go into `revoked.txt`, and the endpoint never serves them again. With the emergency key: `… rollback-embedded` (acts on every pointer that is in state `update` or `halted`, as `otactl rollback-embedded --all-active` does).
3. Phones apply the rollback after they have downloaded it: at the next resume after 15 minutes, at Restart to Update, or at the second cold start after the directive is live. `bin/otactl status` counts the installs still reporting the bad id.
4. If the bad update broke `UpdateHost` itself, tell the affected users: "Open the app and keep it open for 30 seconds on Wi-Fi or mobile data. Close it completely (iPhone: swipe it away; Android: Settings → Apps → Alex Messages → Force stop). Open it again." The first open downloads the rollback; the second open runs it. `bin/otactl status` shows the phone on its embedded id after the second open.

**Kill switch, if `data/ota` itself is damaged:** add `OTA_DISABLED=1` to `/etc/alexmessage/alexmessage.env` and run `sudo systemctl restart alexmessage`. The manifest endpoint then answers 204 to every request. The restart ends every live call (calls are held in memory).

**Fix forward, at the Mac:**
5. `go run ./cmd/otapublish republish <last good label>` re-signs the last good content from the Mac archive as a new id with a new `createdAt`. Assets are reused by hash, so phones download only the manifest. Or publish a fix normally.
6. Do this promptly. Any new update replaces the rollback pointer, because its `createdAt` is later than the directive's `commitTime`, and it also moves every phone past the bad update, which ends the replay exposure of D4.

**Rule:** `rollback-embedded` is allowed only while the server still works with the embedded JS of every runtime in the census, because that is the code phones go back to. `otactl rollback-embedded` prints the native builds the census shows and asks for `yes` (`--yes` skips the question; the emergency key's form asks too: type `yes` and press Enter).

**What expo-updates does by itself:** an update that throws before React Native mounts its first view (the boot splash) on its **first** launch on a phone is marked failed there and never launched again; the phone relaunches the newest other launchable update or the embedded bundle. An update that has rendered once on a phone is never marked failed there: if it later crashes at startup, it crash-loops until a newer update or a rollback directive is downloaded by the cold-start check (the error handler waits at most 5 s for that download). Errors more than 10 s after the first view are ignored by expo-updates. Steps 1–6 handle every case it does not.

**Bad native build N** (crashes, or breaks something OTA cannot fix): build N+1 from the last good `native-*` tag with only the version bumped, and set `min_build` = N+1. Android phones install it through `app-updater`, or, if build N cannot start, from `/app` in the browser (same key, so it installs over N without an uninstall). You install N+1 on your iPhone from `/app` through 万能签. If expo-updates itself misbehaves, build N+1 has `updates.enabled: false`; OTA then returns only with another native build.

### D10. Native updates that OTA can never deliver

**Android: a private release key, one clean break, then an in-app updater.**
- You create a private keystore (PKCS12, RSA-4096) at `~/.alexmessages/keys/android-release.p12` (Phase 3.2). Its password is never written to a file on the Mac: it is typed at `apksigner`'s prompt and kept on paper with the USB stick and in your password manager.
- **Gradle never sees the key.** Gradle builds an **unsigned** release APK. A config plugin, `mobile/plugins/withReleaseSigning.js`, removes the debug signing config from the `release` build type and fails the build if any signing config is attached to it, so a release can never again be signed with the public key. Reason: Gradle evaluates the build scripts of every autolinked npm package and starts Node during the build, so any secret given to Gradle is readable by npm package code. Gradle runs as `./gradlew --no-daemon -Pkotlin.compiler.execution.strategy=in-process :app:assembleRelease`, so no Gradle or Kotlin daemon outlives the build; `otapublish native` refuses an `app-release.apk` or any APK that already carries a signature. After every Gradle and Node process has exited (the process check of D6 step 13), `otapublish native` runs `zipalign -p -f 4`, then `apksigner sign --ks ~/.alexmessages/keys/android-release.p12 --ks-key-alias alexmessages --v4-signing-enabled false` (you type the password at its prompt; v4 signing would write a separate `.idsig` file that nothing serves, while the v2 and v3 signatures sit inside the APK), then `apksigner verify --print-certs -v` (exactly one signer, and a v2 or v3 signature) and compares the signer's certificate SHA-256 with `mobile/release/android-signer.sha256` (committed, public; 64 hex characters, or keytool's `AB:CD:…` form). `zipalign` and `apksigner` come from the newest stable build-tools in `$ANDROID_HOME` (default `~/Library/Android/sdk`); a `JAVA_HOME` without `bin/java` falls back to Android Studio's JDK.
- Local debug and drill builds keep the debug key under the drill package id.
- **Every Android phone uninstalls the old app once** (Android rejects an update signed with a different key). This happens during the manual install of build 2 that is needed anyway. Users sign in again and grant notifications, full-screen calls and background use again. Messages are on the server and are not lost.
  - Rejected: APK v3 key rotation from the public key. The lineage would start from a key everyone has, Android 7.0–8.1 (minSdk is 24) ignores v3, and phones need a manual install anyway.
- **Build 2 contains a minimal `mobile/modules/app-updater` (Kotlin; a no-op stub for iOS and Expo web).** Functions: `installedBuild()`, `canInstall()`, `openInstallPermissionSettings()`, `downloadAndInstall(url, sha256, minBuild)`. It downloads the APK with Range resume. As a pre-check it reads the file with `getPackageArchiveInfo(GET_SIGNING_CERTIFICATES)`: package `com.alexanderjia.messages`, a versionCode higher than the installed one, and **the signer equals the installed app's signer**. It then opens the file once and streams it into a `PackageInstaller` session while hashing, and abandons the session on a SHA-256 mismatch. The session sets `setAppPackageName("com.alexanderjia.messages")`, so Android fails the install for any other package, and Android itself refuses a different signer or a lower versionCode. Result: even malicious JS can at most install a newer build of this app signed with your key. On Android 12+ the session uses `USER_ACTION_NOT_REQUIRED` (no system dialog after the user's tap on [Install]); on Android 7–11 the system shows its confirm screen. `REQUEST_INSTALL_PACKAGES` is declared in the module's manifest. The hard reload blockers also block an install, and `RingReceiver` already restarts the ring service on `MY_PACKAGE_REPLACED`.
  - Why build 2 and not later: without it, build 3 again needs a manual reinstall on every Android phone, including phones in China you cannot reach by cable.
  - Later, in build 3 or after: a JobScheduler job that installs with no tap while the app is not on screen.
- `android.versionCode` goes up by 1 on every native release. A number that was uploaded is never reused (D2).
- **Android developer verification:** Google's rule that apps must come from a registered developer applies to phones with Google Play services. None of the Android phones has the Play Store (your answer), so it does not apply (inference, UNVERIFIED). Re-check at every native release, and if a phone with the Play Store joins, register `com.alexanderjia.messages` in a free limited-distribution Android developer account.

**iOS (only your own iPhone, signed on the phone by 万能签):**
- **IPA recipe** (inside `otapublish native`, after `npx --no -- expo prebuild --clean --no-install`, one prebuild for both platforms, and `pod install` in `mobile/ios`: `--no-install` skips CocoaPods, and the workspace does not exist without it): `xcodebuild -workspace ios/AlexMessages.xcworkspace -scheme AlexMessages -configuration Release -sdk iphoneos -destination 'generic/platform=iOS' -archivePath <tmp>/AM.xcarchive archive CODE_SIGNING_ALLOWED=NO CODE_SIGNING_REQUIRED=NO`; then copy `AM.xcarchive/Products/Applications/AlexMessages.app` into `<tmp>/Payload/` (`cp -pR`) and `zip -qry AlexMessages-1.N.0-N.ipa Payload`. The IPA stays unsigned; 万能签 signs it on the phone.
- **Install:** on the iPhone, open `https://messages.alexanderjia.com/app` in Safari, download the IPA, open it in 万能签, which signs it and installs it over the old app.
- **Push entitlement removed:** a config plugin `mobile/plugins/withoutPushEntitlement.js` deletes `aps-environment` from the entitlements. The app uses only local notifications, and the certificate 万能签 signs with may not allow that entitlement.
- **When a newer build exists:** Settings → Software Update shows "Alex Messages 1.3.0 is available" with a button that opens `/app` (from `client_policy`, below).
- **The IPA download is not authenticated.** The IPA carries no signature of yours, and 万能签 signs whatever file it is given. Whoever controls the Pi, the relay or Cloudflare could replace the IPA on `/app`. Before you download a new IPA, run `otapublish audit` on the Mac: it compares the IPA's SHA-256 on the Pi with the Mac archive.
- **How long the app keeps opening** depends on the certificate 万能签 uses (open item in section 8). OTA cannot extend it.

**Checks on every native artifact** (`otapublish native`, before anything is uploaded or registered; it aborts on any failure):
1. From the IPA's `Expo.plist` and the APK's `AndroidManifest.xml`: the update URL equals `https://messages.alexanderjia.com/api/ota/manifest`; the request headers equal `{"expo-channel-name": "production"}`; updates are enabled; the bundle id / package name is `com.alexanderjia.messages`; `codeSigningMetadata` has keyid `root-2026`. A boolean setting must be a real boolean: expo-updates reads the string `"true"` as absent (Android `Bundle.getBoolean`, the iOS plist reader).
2. The embedded code-signing certificate is byte-equal to `mobile/certs/ota-root-2026.pem`; the chain flag is true; `EXUpdatesConfigCodeSigningAllowUnsignedManifests` / `expo.modules.updates.CODE_SIGNING_ALLOW_UNSIGNED_MANIFESTS` is absent or false.
3. The IPA contains `Payload/AlexMessages.app/main.jsbundle` and `EXUpdates.bundle/app.manifest`; the APK contains `assets/index.android.bundle` and `assets/app.manifest`.
4. The embedded `app.manifest` `commitTime` is not more than 2 minutes in the future.
5. The fingerprints (D3 item 1) and the APK signer (above) match.
6. `version` (`1.N.0`), `buildNumber` and `versionCode` in the artifacts equal `app.json` on the commit, and N is the next build number: the highest build registered on the Pi for either platform + 1 (2 when none is registered). Both platforms share this counter, so `otapublish native` always builds both (it has no `--platform`).

**What `otapublish native` keeps and publishes:** the checked APK and IPA and a `build.json` (commit, build, version, notes, `created_at`, and per platform the file name, SHA-256, size, runtime, embedded update id, its commit time and the APK signer) go to `~/AlexMessagesReleases/builds/native-N-<UTC time>/`. `--no-upload` stops there; it still reads the Pi for the clock check and the build number (a Pi without an update store counts as no registered build, so N = 2). `--upload-only <dir>` checks that directory's files again in a clean worktree of its commit, which must now be on `origin`, and uploads them; running it again after a success changes nothing. The upload copies both files into `~/AlexMessagesReleases/ota/native/<platform>/`, ingests them, registers both runtimes, writes `latest` with `otactl policy`, copies the registered runtimes and `policy.json` into the archive, checks both downloads through `/api/app/latest`, and only then tags `native-N` (a local tag). `native` refuses `--profile drill`: step 5.1 builds the drill profile in Release itself and registers its runtimes in the scratch store with `otactl register-runtime`.

The dynamic proof that the shipped binary rejects an unsigned manifest is Phase 6.4 on your own phones (`otactl test-unsigned`), because the shipped binary only talks to the production URL.

**Minimum-client gate:**
- `data/ota/native/policy.json`: per platform `min_build` (0 = off), `hard_gate` (default `false`) and `latest {version, build, url, sha256, size, notes}`. `latest.url` must be an `http` or `https` URL whose path is `/app/<platform>/AlexMessages-<version>-<build>.<apk|ipa>` for that platform, version and build, with no query, fragment or user info; `sha256` is 64 lowercase hex characters. A missing file means no gate and no published build.
- **Soft gate (default):** the server adds `client_policy {state: ok|update_available|update_required, latest}` to the WS `init`, based on the `X-AM-Client` build (`update_required` below `min_build`, `update_available` below `latest.build`). The app shows a Settings row for `update_available` and a blocking screen for `update_required` (Android: Install; iPhone: a button that opens `/app`).
- **Hard gate (only when an old native protocol is about to be removed; only while `hard_gate` is `true` for the platform):** REST under `/api/` answers 426 (`{"detail", "client_policy"}`) and `/ws` and the call listener close with code **4426**, only for clients that send `X-AM-Client` with a build below `min_build`. The WebSocket gate is checked before authentication, so a gated build sees 4426 even with an expired session. Never gated: `/api/app/`, `/api/ota/`, `/api/debug/report` and `/api/logout` (the app must still read the policy, report errors and sign out). Build 2's `ChatSocket` stops its 1.5 s reconnect loop on 4426, shows "Update Alex Messages to keep messaging", and tries again at every return to the foreground. Build 2's `RingSocket` keeps its service, posts "Update Alex Messages to keep receiving calls", and tries again every 30 minutes. So a corrected policy heals phones by itself. **Clients without the header are never gated:** today's builds reconnect every 1.5 s on any close code except 4401.
- A `policy.json` that cannot be read or fails validation gates nobody (logged as `ota.store_error`).
- `otactl policy` sets `min_build`, `hard_gate` and `latest`. It refuses a `min_build` above the highest registered build of that platform, prints how many census installs it would gate, and asks for `yes`.
- Native-only protocols (`/ws?mode=calls`, the ring flows) must stay backward-compatible with the lowest build in the census.

### D11. The one-time migration to build 2

Build 2 = version **1.2.0**, `ios.buildNumber` **"2"**, `android.versionCode` **2**. It contains expo-updates, the root certificate and chain flag, the update UX and guard, `X-AM-Client` (including RingSocket), 4426 handling, `app-updater`, the push-entitlement removal, and the private Android key. Order (details in Phase 0, Phase 5 and Phase 6):
1. `DEPLOY_ENABLED=false` **before** fixing CD, so no push deploys by surprise. From then on every server deploy is manual: set `DEPLOY_ENABLED` to `true`, run Actions → Deploy → Run workflow, and set it back to `false` when the run ends. (The deploy job runs only while the variable is `true`, for a manual run too. Do not push while it is `true`.)
2. Commit all current work locally, including the DM-calls code. Done: local commit `37e425d`. Do not push it before release day (your answer in section 8).
3. Pass the local drill on both platforms (Phase 5).
4. The download speed from mainland China is not tested before build 2 (no tester; section 8). The first real update measures it (D8 `ota.download` records each download's duration).
5. Build the release artifacts from the local release commit and test them on your own iPhone and on the Mac's Android emulator against a staging server (Phase 5.3–5.4), before production changes. No real Android phone is available for testing before the hand-out (section 8).
6. Release day (Phase 6): push, deploy the server, upload and register build 2, check that your iPhone and the emulator reject an unsigned update, and hand out build 2 **on the same day** (fact 6): first to one Android user, then to everyone else. Android users download the APK from `/app` first and uninstall the old app second. You install the IPA on your iPhone from `/app` through 万能签. The APK SHA-256 and the signer SHA-256 go into DEPLOY.md so others can check the download.
7. Check that every phone appears in the census on build 2.

### D12. Testing before trusting it

1. **Go unit tests** (`internal/ota`, `internal/ota/sign`, `internal/webapp`, `internal/db`, `cmd/otactl`, `cmd/otapublish`): exact multipart bytes and headers; every 204 path (including revoked ids and `OTA_DISABLED`); the directive path; 400s for bad headers; the leaf sent is the one named in `.leaf`; asset name validation (`../`, wrong length, bad extension), content type, Cache-Control (including `no-store` on 404), the nosniff/sandbox/attachment headers, Range and HEAD; chain checks that mirror the client (non-CA root, leaf without codeSigning, expired leaf, bad signature are rejected); `ingest` rejects non-empty `metadata` and a future `createdAt`; census limits (cap, registered embedded id, header sizes) and SFV parsing; `activate` refuses a revoked id, an unregistered runtime, a leaf that expires within 30 days, and a `createdAt` that is not newer; `halt --all` and `--label` touch every matching pointer; `policy` refuses a `min_build` above the highest registered build; atomic pointer writes; a sign-then-verify round trip; a test that `cmd/server`, `cmd/admin`, `cmd/meet` and `cmd/otactl` import neither `internal/ota/sign` nor `filippo.io/age`; the private-key guard (D4); every `otactl` and `otapublish` command against a temporary directory that stands in for the Pi (fake `ssh` and build tools, the real `otactl`).
2. **shared Vitest:** `X-AM-Client`; `pendingCount` and `hasUnsentWork()`; 4426 stops the reconnect loop and retries on foreground; 426 handling; `DebugReporter.flush()` resolves after the POST.
3. **mobile unit test:** `mobile/` contains neither `setUpdateRequestHeadersOverride` nor `setUpdateURLAndRequestHeadersOverride`.
4. **`deploy/remote_deploy_test.py`:** `data/ota/` is unchanged by a deploy and by a forced rollback; `bin/otactl` is installed by rename; a release that contains `data/` is refused.
5. **Local drill on release builds** (Phase 5). A drill profile (`AM_OTA_PROFILE=drill` in `mobile/app.config.js`) changes the package and bundle id to `com.alexanderjia.messages.drill`, the update URL to `http://localhost:8799/api/ota/manifest` (iOS Simulator) or `http://10.0.2.2:8799/api/ota/manifest` (Android emulator, with cleartext allowed for that profile only), and uses throwaway keys. Because the package id differs, a drill build can sit next to the real app and has its own runtime. Scenarios:

| # | Scenario | Passes when |
|---|---|---|
| a | publish U1; cold start twice; then publish U1b and resume past the threshold (set to 10 s for the drill) | `Updates.updateId` = U1 after the second cold start and U1b after the resume; a census row exists; `Updates.manifest.extra.am` is readable |
| b | U2 throws at import, on its first launch on the phone | the phone falls back to U1; U2 is marked failed; the census shows U2 as failed |
| c | U3 crashes 15 s after the first screen | breadcrumb reported; `rollback-embedded` returns the phone to the built-in bundle after the D9 step 4 sequence; `republish` then moves it forward |
| d | flip one byte of `manifest.json`, then of an asset | both rejected; the phone keeps its update |
| e | serve an unsigned response; a wrong leaf; an expired leaf; a manifest older than the running update; a revoked manifest newer than the running update; inject `expo-manifest-filters` through a proxy | the first four are rejected or ignored; the revoked newer manifest is **accepted** (known limit: the client has no revocation, D4); the injected filter has no effect |
| f | publish for an unregistered runtime | refused by `otapublish` |
| g | call between Simulator and emulator, publish, resume | no reload until the call ends |
| h | Android: swipe the app away with the ring service running, publish, reopen | applied through the resume path |
| i | a picker is open for more than the threshold | no reload; the picked file arrives |
| j | after a reload, check notifications and links | no old notification tap or deep link is replayed |
| k | build 3 (no-op change) installed by `app-updater` on API 35 and API 30 emulators; then a debug-key-signed APK; then an APK of another package | 35: installs after the tap with no system dialog; 30: the system confirm screen; debug-signed: refused "signer mismatch"; other package: refused by the session |
| l | `min_build` = 3 with `hard_gate` = `true` | build 2 shows the blocking screen; REST 426; WS 4426; the server sees one connection, not a reconnect loop; after `min_build` = 0 the phone reconnects on the next foreground and RingSocket within 30 minutes |
| m | calls in **release** builds (R8 minification is on) on both platforms | calls ring, connect and end; the ring service works after swipe-away |
| n | U4 renders once, then crashes at startup on its second cold start | the phone crash-loops (expected); after `halt` + `rollback-embedded` and the D9 step 4 sequence it runs the built-in bundle |
| o | `keys issue-leaf` (drill keys) + `resign-active`, then `rollback-embedded` | the directive verifies on both platforms; the endpoint sends the new leaf |

6. **Maestro:** a new `mobile/e2e/ota.yaml` (Settings → Software Update shows the version; the beta switch is hidden for non-admins), run on a Release drill build (UpdateHost is off in debug builds); the 8 existing flows still pass.
7. **Production drills** (Phase 7): beta → promote; `halt`; `rollback-embedded`; `republish`. Repeat the rollback drill every 6 months.

---

## 4. What is baked into build 2

These choices can only change with **another manual reinstall of your iPhone**, and with a new native build on Android (installed through `app-updater` from build 3 on). Decide them before Phase 4.

| Baked into build 2 | Value |
|---|---|
| Update URL | `https://messages.alexanderjia.com/api/ota/manifest` |
| Request headers | `{"expo-channel-name": "production"}` |
| Native update settings | `enabled: true`, `checkAutomatically: "ON_LOAD"`, `fallbackToCacheTimeout: 0`, `useEmbeddedUpdate: true`, `disableAntiBrickingMeasures: false` |
| Runtime policy | fingerprint + `fingerprint.config.js` |
| Trust anchor | root certificate `ota-root-2026`, chain mode on (or the single-certificate fallback) |
| Code-signing metadata | keyid `root-2026`, alg `rsa-v1_5-sha256` |
| Android signer | your private release key |
| Android permission | `REQUEST_INSTALL_PACKAGES` |
| `app-updater` module | PackageInstaller install with the signer and package checks |
| `RingSocket.kt` | `X-AM-Client` header; on 4426 keep the service and retry every 30 minutes |
| iOS entitlements | no `aps-environment` |
| Version identity | 1.2.0 / build `"2"` / versionCode 2 |
| `expo-application` | already linked natively through expo-notifications; added as a direct JS dependency only |

Everything else is JS or server code: `UpdateHost`, the guard, telemetry, banners, the Settings page, the gates, the census, the admin page. It can be improved later by OTA or by a server deploy. Build 2 ships only after drill scenarios a, g, h, i and j pass, because an `UpdateHost` that throws leaves phones updating only at cold starts, which Android rarely does (fact 8).

Also permanent: **the domain.** Every binary depends on `messages.alexanderjia.com` for updates. If the domain lapses, is put on hold by the registrar, or the Cloudflare account is lost, every phone is stranded until it reinstalls. The registrar is in mainland China and has no transfer lock today (fact 17); step 0.7 deals with it before build 2.

---

## 5. Implementation phases

"Claude" means a code change or command I make; "you" means something only you can do. Nothing is pushed, deployed or published without your go-ahead in chat.

**Effort** is in working days of implementation including test runs, excluding your review time. The figures are estimates.

| Phase | Effort |
|---|---|
| 0 prerequisites | 1 day |
| 1 server | 4–6 days |
| 2 tools | 6–9 days |
| 3 keys (you) | half a day |
| 4 client | 7–10 days |
| 5 drill and pre-release test | 4–6 days (8 or more if the chain fallback is needed) |
| 6 release day | 1–2 days, plus every user's availability on that day |
| 7 first OTA | 1 day plus a 24 h wait |
| 8 native path | 1–2 days |
| 9 hardening | 3–5 days |

Total to release day (Phases 0–6): about 23–34 working days, about 5–7 calendar weeks. To the end of Phase 8: about 3 more weeks, including the 14-day census watch.

Calendar: week 1 Phase 0 and 1; weeks 2–3 Phase 2; then Phase 3 (you, half a day: its key commands are built in Phase 2.2, and Phase 4.1 needs the root certificate and the signer hash it creates); weeks 3–5 Phase 4; week 6 Phase 5; week 6–7 release day; then 14 days of census watch; then Phases 7 and 8. Phase 9 any time after Phase 6.

### Phase 0: prerequisites (no OTA code yet)

| # | Who | Step | Done when |
|---|---|---|---|
| 0.1 | you | GitHub → repo Settings → Secrets and variables → Actions → **Variables**: set `DEPLOY_ENABLED` to `false`. From now on it is `false` at all times except during a manual deploy run (6.2, section 6). | `gh variable list` shows `DEPLOY_ENABLED=false`. |
| 0.2 | — | **Done (2026-10-22):** local commit `37e425d` holds every change that was uncommitted, including `SecurityReview.md` and both plan files. It is not pushed. Keep the Mac in a backup (step 3.0), because this work exists only on it until switch-over day. | `git status --porcelain` lists only later edits; `git log origin/react..react` lists `37e425d`. |
| 0.3 | you | Delete the secret `DEPLOY_KNOWN_HOSTS` (it overrides the pinned host key). Set the variable `HEALTH_PORTS` to `6001 6002 6003`, or delete it (the default is right). Move `DEPLOY_SSH_KEY` from repository secrets to the `production` environment's secrets; give `production` a deployment branch policy of `main` and `react` and yourself as required reviewer. Delete the unused `DEPLOY_HOST`, `DEPLOY_USER` and `DEPLOY_PATH` secrets. On the Pi, restrict the deploy key in `~/.ssh/authorized_keys` with `command=` to the deploy script. | `ssh-keyscan -p 2222 -t ed25519 35.226.215.209` prints the key pinned in `deploy.yml`; `gh secret list` shows no `DEPLOY_KNOWN_HOSTS`, `DEPLOY_HOST`, `DEPLOY_USER`, `DEPLOY_PATH` or repository-level `DEPLOY_SSH_KEY`; `gh variable list` shows `DEPLOY_ENABLED=false`. The first real CI SSH connection happens at 6.2; if it fails there, the deploy job stops before it changes the Pi. |
| 0.4 | you | Check that your own account is an admin: in the app, Settings shows an **Admin** tab. If it does not, make it one in the admin panel. | The Admin tab shows. (The beta switch and the promote gate need an admin.) |
| 0.5 | — | Dropped: nobody in mainland China can run a speed test (section 8). | — |
| 0.6 | Claude | Before any `prebuild --clean`: copy today's `mobile/ios` and `mobile/android`, run `npx expo prebuild --clean` in a scratch copy, and diff the two, so hand edits in the gitignored native folders are found instead of lost. | The diff is reviewed; any hand edit is moved into a config plugin or explicitly dropped. |
| 0.7 | you | The domain: (a) confirm at the registrar that the real-name verification of `alexanderjia.com` is complete and its contact e-mail works; (b) turn on auto-renew and `clientTransferProhibited`; or, preferred, (c) transfer `alexanderjia.com` to a registrar outside mainland China (for example Cloudflare Registrar). | `whois alexanderjia.com` shows `clientTransferProhibited` and an expiry at least 2 years away. |
| 0.8 | you | Run `ssh -p 2222 alexander@35.226.215.209 'command -v rsync; df -h ~/alexmessage'`. If rsync is missing, either run `sudo apt install rsync` on the Pi or accept the tar fallback (D6 step 14). | The output shows rsync (or your choice of the fallback is in the chat) and at least 2 GB free. |

### Phase 1: server (Claude; deployed only on release day)

All of Phase 1–4 stays in local commits until release day (0.2). "Tests pass" means locally; CI first runs them at the push in 6.1.

**Status (2026-10-25): steps 1.1–1.6 are done in local commits on `react`, NOT pushed and NOT deployed:** `884c288` (1.1), `8f355a8` (1.2 and 1.3), `dfa0335` (1.4), `662acb0` and `396bd6e` (1.5), `08fcb9a` (1.6). The `deploy.yml` change of 1.5 is in `662acb0`; the workflow was not run. The `deploy.yml` line that cross-compiles `otactl` was added in 2.1 (`1fbf4ed`).

| # | Step | Done when |
|---|---|---|
| 1.1 | `internal/ota/`: store layout and validators, manifest selection, multipart writer, SFV parsing, certificate-chain verification with the client's rules, census (`census.db`, cap, registered embedded ids), policy. `internal/ota/sign`: signing only. | `go test ./internal/ota/...` passes (D12 item 1). |
| 1.2 | `internal/webapp/ota_routes.go` (all D2 URLs and header rules, `OTA_DISABLED`, registered in the module list in `webapp.go`); the `/app` page (Android: download first, then uninstall the old app, allow "Install unknown apps" for the browser or file manager, install, sign in, allow notifications, full-screen calls and background use; iPhone: download the IPA and open it in 万能签; the SHA-256 sums; English only); `internal/webapp/client_info.go` (`X-AM-Client` middleware); `client_policy` in the WS `init` and the `client=` parameter in `ws.go`; the hard-gate code path (off by default); account deletion and export cover the census rows. | `go vet ./... && go test ./...` pass; a local `curl` with phone headers gets `204` with `expo-protocol-version: 1`. |
| 1.3 | `internal/db`: `sessions.client` and `sessions.client_seen_at` through `ensureColumn`; census prune in the maintenance sweep. | DB tests pass; an old database migrates on start. |
| 1.4 | `internal/adminapp`: read-only `GET /api/ota/installs` (JSON `{"installs": […], "max_rows": 5000}`; JSON escapes `<`, `>` and `&`; the admin page that renders the fields as text comes in 9.1). | Test passes. |
| 1.5 | `deploy.yml`: stamp every binary with `-X main.commit=$GITHUB_SHA`; the line that cross-compiles `otactl` into `bin/` is added in Phase 2.1, together with `cmd/otactl`. `remote_deploy.sh`: install `bin/otactl` by rename (`install -m 755 new bin/otactl.tmp && mv bin/otactl.tmp bin/otactl`), never by overwrite. `deploy/remote_deploy_test.py`: `data/ota` survives a deploy and a rollback. | Tests pass. |
| 1.6 | Docs: an OTA section in `DEPLOY.md` and `CLAUDE.md`. | DEPLOY.md contains the emergency commands of D9 verbatim, the kill switch, the publish commands of section 6, the key locations, and the leaf renewal steps. |

### Phase 2: tools (Claude)

**Status (2026-10-25): steps 2.1–2.3 are done in local commits on `react`, NOT pushed:** `88ea2dd` (shared checks in `internal/ota`), `1fbf4ed` (2.1, with the `deploy.yml` line that cross-compiles `otactl`), `0f10f9c`, `c1a09b0`, `8b48f45`, `8f88091` and `cd1aad7` (2.2), `aea522f` (2.3), `d5250f4` (DEPLOY.md and CLAUDE.md), `eddf897` (a fix to 2.1 and 2.2: only `otactl`'s exit status 3 means "no update store yet") and `987f0d1` (DEPLOY.md and CLAUDE.md for that fix). The plan fixes of section 10 are in `b6381c6` and in the commit after `987f0d1`. Neither workflow was run. Before Phase 4, `otapublish ota` and `otapublish native` run end to end only against fixtures and fake tools; section 9 lists what only the real tools can show.

| # | Step | Done when |
|---|---|---|
| 2.1 | `cmd/otactl` (runs on the Pi): `init` (creates `data/ota`, `FORMAT`, `certs/`), `version`, `ingest`, `activate`, `halt` (`--all`, `--label`), `rollback-embedded` (`--label`), `revoke`, `status`, `census`, `runtimes`, `register-runtime`, `policy`, `doctor` (leaf expiry within 30 days for every pointer-referenced manifest and directive, the served leaf chains to the root, free disk), `emergency` (reads one word from `SSH_ORIGINAL_COMMAND`: `halt`, `rollback-embedded` or `status`), `repair-asset` (replaces `assets/<hash>.<ext>` only with bytes that hash to `<hash>`), `test-unsigned` (Phase 6.4). Every command that changes the store holds `data/ota/.lock` and appends one JSON line to `log/actions.log`. Every command except `init` and `version` exits with status 3 while the store has no `FORMAT` file; `otapublish` reads only that status as "no update store yet", and any other failure as an error. Also built: `promote` (the D5 gate; it runs on the Pi because it reads `alexmessage.db`), `restore-pointer` (writes one exact pointer back, for the smoke test and `restore-pi`), `inventory` (every stored file with its SHA-256, for `audit`), `rollback-embedded --all-active` (the emergency key's form), `test-unsigned --clear` (puts the previous beta pointers back). `version` reads `vcs.revision` from each binary's build info (`deploy.yml` builds with `-trimpath`, which leaves `-ldflags` out of the build info). `repair-asset --asset <name>` reads the bytes from stdin. `status --json` includes `"store"` (the store's absolute path, the upload target). `otactl` finds the store next to its own executable (D2). | Tests pass. |
| 2.2 | `cmd/otapublish` (Mac only, never deployed): `keys init-root | issue-leaf | upload-certs | show` (`upload-certs` copies the root and every unexpired leaf certificate to `data/ota/certs/`; `issue-leaf` runs it by itself once the Pi has an update store, after `otactl init` in 6.3), `ota` (with `--profile drill`, D6), `promote`, `republish`, `resign-active`, `native` (`--no-upload`, `--allow-local`, `--upload-only <artifact dir>`; the IPA recipe, the unsigned Gradle build, `apksigner`, the artifact checks of D10), `audit` (with `--record`, D2), `restore-pi`, `status`, `--dry-run` (with `--allow-local` and `--fixture`, D6), and the flags `--pi-dir` and `--public-url` (a local store instead of the Pi). age-encrypted keys (`filippo.io/age`, imported only by `cmd/otapublish`). | Tests pass; `ota --dry-run` against a fixture export completes. |
| 2.3 | `.github/workflows/mobile-checks.yml`: `TestServerBinariesNeverImportSigning` (the import guard) and `TestNoPrivateKeysTracked` (the private-key file guard, D4), then `cd shared && npm ci --ignore-scripts && npm run typecheck && npm test` and `cd mobile && npm ci --ignore-scripts && npx --no -- tsc --noEmit` (the commands of D6 steps 5 and 6). No secrets. | The same commands pass locally; the workflow is green on the push in 6.1. |

### Phase 3: keys (you, about half a day)

| # | Step | Done when |
|---|---|---|
| 3.0 | Turn on FileVault if it is off (System Settings → Privacy & Security → FileVault). Choose and tell Claude in chat: the password manager for the key backups, where the USB stick and the passphrase paper are kept, and the backup that covers `~/AlexMessagesReleases` and the local commits (for example Time Machine). | FileVault shows On; your choices are in the chat. |
| 3.1 | Plug in a USB stick. Run `go run ./cmd/otapublish keys init-root --id root-2026 --out /Volumes/<stick> --cert-out mobile/certs/ota-root-2026.pem` (asks for a new passphrase; writes the encrypted key to the stick and the public certificate into the repo). Copy the encrypted root key into your password manager as an attachment. Write the root passphrase on paper and keep it with the stick. Run `go run ./cmd/otapublish keys issue-leaf --root /Volumes/<stick>/ota-root-2026.key.age --out ~/.alexmessages/keys/` (asks for the root passphrase and a new leaf passphrase; write that one on paper too). It then asks the Pi over SSH whether it has an update store; the Pi has none before step 6.3, so the command prints "Not uploaded" and succeeds, and 6.3 uploads the certificates. Unplug the stick. | `otapublish keys show --root /Volumes/<stick>/ota-root-2026.key.age` decrypts the stick copy; the password-manager copy, exported to a temporary file, decrypts with `keys show --root <temp file>` (delete the temp file afterwards); `mobile/certs/ota-root-2026.pem` exists. Once a year (9.3), repeat both checks on a second machine with the passphrase read from the paper. |
| 3.2 | `keytool -genkeypair -v -storetype PKCS12 -keystore ~/.alexmessages/keys/android-release.p12 -alias alexmessages -keyalg RSA -keysize 4096 -validity 10950 -dname "CN=Alex Messages"`. Write the keystore password on paper (with the stick) and into your password manager. Copy the keystore to the USB stick and the password manager. Do **not** write the password into `~/.gradle/gradle.properties` or any other file. Paste the printed SHA-256 (public) into the chat; Claude commits it as `mobile/release/android-signer.sha256`. | `keytool -list -v` shows the same SHA-256 for every copy, opened with the password from the paper. |
| 3.3 | — | Not needed: no Android phone has the Play Store (D10, "Android developer verification"). | — |
| 3.4 | Optional emergency key: `ssh-keygen -t ed25519 -f ~/.ssh/am-emergency`; add its public key to the Pi's `~/.ssh/authorized_keys` with `command="/home/alexander/alexmessage/bin/otactl emergency",no-port-forwarding,no-agent-forwarding,no-pty`; copy the private key to the device you would use in an emergency. Works after Phase 6.2 (when `otactl` exists). | After 6.2: `ssh -p 2222 -i ~/.ssh/am-emergency alexander@35.226.215.209 status` prints the pointers; `… ls` is refused. |

### Phase 4: client (Claude)

| # | Step | Done when |
|---|---|---|
| 4.1 | `cd mobile && npx expo install expo-updates expo-application`, then pin `"expo-updates": "57.0.26"` exactly in `mobile/package.json`. Claude makes the release commit `release: native 2`: `app.json` version `1.2.0`, `ios.buildNumber` `"2"`, `android.versionCode` `2`, `runtimeVersion` fingerprint, the `updates` block (D7, D4, D5), the plugins. New: `fingerprint.config.js`, `app.config.js` (drill profile only), `plugins/withOtaCertificateChain.js`, `plugins/withReleaseSigning.js`, `plugins/withoutPushEntitlement.js`, the `.gitignore` exception, `release/android-signer.sha256`. | `npx expo prebuild --clean` passes; the generated `Expo.plist` and `AndroidManifest.xml` show updates enabled, the URL, the certificate, and the chain flag; the entitlements have no `aps-environment`. |
| 4.2 | `mobile/src/updates/`: `UpdateHost.tsx`, `guard.ts`, `telemetry.ts`, `crashBreadcrumb.ts`, `clientInfo.ts`, `restore.ts`, `UpdateBanner.tsx`, `UpdateRequiredScreen.tsx`. `app/settings/update.tsx` (Software Update page: version, update label, last checked, Check for Updates, Restart to Update, beta switch for admins); `app/settings/_layout.tsx` (`<Stack.Screen name="update" options={{ title: "Software Update" }} />`); `app/settings/index.tsx` (the Software Update row and the version line in the colophon). Edits: `app/_layout.tsx`, `app/chat.tsx`, `session.tsx` (DebugReporter session id and exposure, `clientInfo`, `am_uid` set at sign-in and cleared at logout, `am_channel` re-applied at launch); holds in `src/components/Composer.tsx`, `src/components/DictationBar.tsx`, `src/components/attachments.tsx`, `app/settings/profile.tsx`, `app/settings/data.tsx`, `app/settings/admin.tsx`, `app/settings/notifications.tsx`, `app/login.tsx`, `src/notify.ts` (permission request); `src/media.ts` (`isMediaActive`); `src/call/system.android.ts`, `system.ios.ts` and `system.ts` (`msSinceCallAction`). | `npx tsc --noEmit` passes; the 8 Maestro flows pass on debug builds. |
| 4.3 | `shared/`: `api.ts` (`clientInfo`, 426, `debugReport` returns its promise), `debug.ts` (`flush` returns a promise), `socket.ts` (`client=` parameter, `pendingCount`, 4426 handling), `store.ts` (`hasUnsentWork()`, `updateRequired`), `types.ts` (`client_policy`), tests. | `cd shared && npm run typecheck && npm test` pass; `cd web && npm run build && npm run test:e2e` pass. |
| 4.4 | Native: `mobile/modules/app-updater/` (Kotlin; a no-op stub for iOS and Expo web, like `call-ringer`'s); `RingSocket.kt` (`X-AM-Client`; on 4426 keep the service, post "Update Alex Messages to keep receiving calls", retry every 30 minutes). | Release builds compile on both platforms. |
| 4.5 | `mobile/e2e/ota.yaml` (D12 item 6). | It passes on a Release drill build. |

### Phase 5: local drill and pre-release test (Claude, then you)

| # | Who | Step | Done when |
|---|---|---|---|
| 5.1 | Claude | Start a scratch server on :8799 with `OTA_DIR` in the scratchpad; build the drill profile in Release on the iOS Simulator and on Android emulators (API 35 and API 30); register each build's runtime in the scratch store with `otactl register-runtime --ota <scratch store>` (`otapublish native` refuses the drill profile). | Both launch; the scratch store lists their runtimes. |
| 5.2 | Claude | Run scenarios a–o (D12 item 5). If chain mode fails on either platform, switch to the single-certificate fallback (D4) and repeat. | Every scenario passes; results are written up for you. |
| 5.3 | Claude starts it in your Terminal panel; you type the keystore password | `go run ./cmd/otapublish native --notes "<text>" --no-upload --allow-local` on the local release commit from 4.1: builds the unsigned APK and the IPA, signs the APK with `apksigner`, and runs every artifact check of D10. It also reads the Pi over SSH for the clock check and the build number (no update store there yet, so N must be 2). | The command prints "All artifact checks passed. Artifact directory: <dir>". |
| 5.4 | you, with Claude | Install the 5.3 IPA on your iPhone (through 万能签). Claude installs the 5.3 APK (the real build 2, not the drill build) on the Mac's Android emulator; it stands in for an Android phone, because none is available. Claude starts a staging server on the Mac (`cmd/server` from the release commit, its own data directory, reachable on your LAN and from the emulator). On the iPhone and the emulator, enter the staging server's URL in the login screen's server field, sign in, and test: a call each way between them (on the iPhone this is the first real test of CallKit), a voice message, an upload, a cold start. | Every test works on both. If one fails: Claude fixes it, and 5.3–5.4 repeat under the same build number (nothing was uploaded yet). |
| 5.5 | you | Review the 5.2 and 5.4 results and approve release day. | Your approval is in the chat. |

### Phase 6: release day (you and Claude)

| # | Who | Step | Done when |
|---|---|---|---|
| 6.1 | you, then Claude | Approve the push. Claude pushes `react` (`37e425d` and every later local commit, including the 4.1 release commit). This publishes the security fixes and `SecurityReview.md`; 6.2 patches production a few minutes later, so do 6.2 right after 6.1. `DEPLOY_ENABLED` is `false`, so the push itself deploys nothing. | `git log origin/react..react` prints nothing; `mobile-checks.yml` is green. |
| 6.2 | you | Set `DEPLOY_ENABLED` to `true`, run Actions → Deploy → Run workflow on `react`, and set `DEPLOY_ENABLED` back to `false` when the run ends (D11 item 1). | `ssh -p 2222 alexander@35.226.215.209 'cd ~/alexmessage && bin/otactl version'` prints the pushed commit for `otactl`, `server`, `admin` and `meet` (`otactl doctor` exits with status 3, "not an update store", until `otactl init` in 6.3); `curl -sI 'https://messages.alexanderjia.com/sound/RingTone.mp3?v=<commit>'` returns 200. From this moment old apps get 401 on images. |
| 6.3 | Claude | `bin/otactl init`; `go run ./cmd/otapublish keys upload-certs` puts the root and leaf certificates into `data/ota/certs/`; `go run ./cmd/otapublish native --upload-only <5.3 artifact dir>` checks that the artifacts' commit is now on `origin`, uploads them, registers both runtimes, writes `policy.json`, and tags `native-2`. No passphrase is needed. | `curl https://messages.alexanderjia.com/api/app/latest` shows build 2 for both platforms; `bin/otactl doctor` exits 0; `otapublish audit` prints "No differences". |
| 6.4 | you, with Claude | On your iPhone and the emulator (both on build 2 from 5.4): sign in to production (login screen → server field) with your account, turn on Beta updates. Claude runs `bin/otactl test-unsigned --channel beta`, which serves beta devices an **unsigned** copy of the built-in update for 10 minutes. On each, Settings → Software Update → Check for Updates. Then Claude runs `bin/otactl test-unsigned --clear`, which puts the previous beta pointers back. Then a call each way on production. | Both still show "built-in" and Settings → Software Update shows a code-signing error; `otactl census` shows both on build 2; both calls work. If either accepts the unsigned update: stop, do not hand out build 2, and rebuild with the next build number. |
| 6.5 | you | Hand out build 2 the same day, first to **one** Android user: send them the `/app` link in a DM. They download the APK and check that the download finished (the Files app shows its size); then uninstall the old app; then tap the downloaded file; then sign in, allow notifications, full-screen calls and background use, and allow "Install unknown apps" for the browser or file manager that opened the APK (later, for `app-updater`, also for Alex Messages itself; the Settings page offers the shortcut). Make a call with that user. If it works, send the `/app` link to every other Android user. Your iPhone: if not done in 6.4, download the IPA from `/app` and install it through 万能签. | The first user's call works; for every approved account in the admin panel, `otactl census` shows an install with `am_uid` = that account and build 2. |
| 6.6 | Claude, after your go-ahead | Commit the APK SHA-256 and the signer SHA-256 to `DEPLOY.md`; push. | The commit is on `origin/react`. |
| 6.7 | you, daily for 14 days | Ask Claude in chat "check the OTA census" (or approve a scheduled task that runs `bin/otactl census` each morning and reports phones without `X-AM-Client`). Claude does not act between your messages unless such a task exists. | 14 days with no legacy client in the census. |

### Phase 7: first real OTA and the rollback drill (you and Claude)

| # | Who | Step | Done when |
|---|---|---|---|
| 7.1 | Claude, then you | Claude makes a small visible JS change, commits it, and pushes after your go-ahead. Claude starts `go run ./cmd/otapublish ota --notes "First update"` in your Terminal panel; you type the leaf passphrase. On each beta phone: Settings → Software Update → Check for Updates → Restart to Update, check the change, then Check for Updates once more (this reports the running update to the server). Then `otapublish promote <label>`. | Within 24 h the census shows every phone on the new label and no failure. |
| 7.2 | you, with Claude | On beta: `otactl halt --label <label>`, `otactl rollback-embedded --label <label>`, the D9 step 4 sequence on one phone, then `otapublish republish`. | Beta phones go to the built-in bundle, then to the republished update. |

### Phase 8: prove the native path (you and Claude)

| # | Who | Step | Done when |
|---|---|---|---|
| 8.1 | Claude prepares and starts it in your Terminal panel; you type the keystore password | Claude makes the release commit `release: native 3` (version 1.3.0, no functional change) and pushes after your go-ahead. `otapublish native` builds, signs, checks and uploads build 3. | Android phones install it from inside the app; your iPhone shows "Alex Messages 1.3.0 is available", and you install it from `/app` through 万能签. |

`min_build` stays 0 until an old native protocol has to be removed; drill scenario l tested the gate. (Setting it to 2 would gate nothing, because builds below 2 send no `X-AM-Client`.)

### Phase 9: hardening (Claude; server only, any time after Phase 6)

| # | Step | Done when |
|---|---|---|
| 9.1 | Admin login rate limit, and a custom header plus exact `Origin` check on every mutating admin route. Then the admin "Updates" section (pointers, installs, failures, Halt, Roll back to built-in). | Tests pass; a halt from the admin panel works on beta. |
| 9.2 | Automatic halt of beta (D8) and a Web Push to admins when it fires. | A drill failure reported by 2 signed-in test users produces a halt and a push; a forged census header does not. |
| 9.3 | The server checks the active leaf's `notAfter` once a day; 30 days before it, a debug-console event `ota.leaf_expiring` and a Web Push to admins. You: calendar reminders: replace the leaf every 12 months; rollback drill every 6 months; once a year decrypt both root copies and the keystore on a second machine with the paper passphrases. | The event fires in a test with a short-lived leaf; the reminders exist. |
| 9.4 | you: in the Cloudflare dashboard, confirm that Bot Fight Mode does not challenge okhttp or CFNetwork requests to `/api/ota/manifest`. | Manifest checks keep appearing in the census from both platforms. |

**Not built, on purpose:** bsdiff patches (full bundles are about 1.3–1.5 MB compressed); percentage rollouts; automatic rollback of phones (only automatic halt of beta); VoIP push; an iPhone signature-expiry banner (only your iPhone is affected, and 万能签 shows the expiry itself); a separate macOS user for the npm build steps (D6).

---

## 6. Day-to-day use once it is built

**Ship a JS fix:**
1. If the fix needs a server change, deploy the server first, the manual way of D11 item 1 (pushes never deploy). The server must keep working for phones still on older updates.
2. Commit and push.
3. Claude starts `go run ./cmd/otapublish ota --notes "Fixes the send button on iPad"` in your Terminal panel; you type the leaf passphrase. It publishes to beta and prints the label.
4. On your phone: Settings → Software Update → Check for Updates → Restart to Update; try the fix.
5. On each beta phone (iOS and Android): Check for Updates once more, so the server sees the running update.
6. `go run ./cmd/otapublish promote <label>`.
7. Next day: `bin/otactl census` (or the admin page) shows every phone on the label and no failure.

**Ship a native change:**
1. Claude makes the release commit `release: native N` (`app.json`: version `1.N.0`, `ios.buildNumber` `"N"`, `android.versionCode` N) and pushes after your go-ahead.
2. Claude starts `go run ./cmd/otapublish native --notes "…"` in your Terminal panel; you type the keystore password. It refuses unless N is the next build number (the highest build registered on the Pi + 1), builds both platforms from a clean worktree of the pushed commit, runs every artifact check, saves the artifact directory, uploads, registers the runtimes, updates `policy.json`, checks the downloads, and tags `native-N` only after the upload succeeded (D10).

**Backport a JS fix to phones on an older runtime:** `git switch -c release/native-N native-N`, cherry-pick the fix, push `release/native-N` (deploy.yml does not run on that branch), then `otapublish ota --from release/native-N`.

**Upgrade Expo SDK, React Native or expo-updates:** always a native release (a new runtime on both platforms). Before it ships, re-run drill scenarios a, b, d, e, n, o and the rollback part of c on the new versions, and check that `fingerprint.config.js` still parses (`npx @expo/fingerprint fingerprint:generate` lists the skips). Phones that do not install the new build keep working with the server and receive only backported fixes.

**Numbering:** native build N, one counter for both platforms; version `1.N.0`; OTA label `YYYYMMDD.k`; git tags `native-N` and `ota-<label>`. Settings shows `Alex Messages 1.2.0 (2) · update 20261105.1`, or `· built-in`.

---

## 7. What users see

| Moment | What the user sees |
|---|---|
| An update downloads | Nothing. |
| An update applies (cold start, or back after ≥ 15 min away with nothing at risk) | About 1 s of a paper-coloured screen with the app icon, then the same conversation. |
| An update is critical or has waited 24 h | A banner on the DM list: "A new version of Alex Messages is ready." [Restart] [×] |
| Restart is blocked | Settings → Software Update shows why: "Available after your call ends", or a list of what a restart would discard, with a confirmation. |
| A native update exists (Android) | A banner with [Install]. Android 7–11 also show the system's confirm screen. |
| A native update exists (your iPhone) | Settings → Software Update: "Alex Messages 1.3.0 is available" [Open download page] |
| Below `min_build` | A full-screen "This version is no longer supported" with the install action. |

The texts are English, like the rest of the app (your answer).

---

## 8. Your answers (2026-10-25) and what is still open

**Answers and what each one changed:**
1. **iPhone:** only you use an iPhone; the app is signed on it by 万能签. → The `/app` page keeps the IPA download; no AltStore/SideStore list; no `LSApplicationQueriesSchemes`.
2. **Android:** every other user (under 10 people in total) uses Android and downloads the APK from `/app`. Brands are unknown; treat them as ordinary Android phones. → The `/app` page has generic Android steps. A phone that cannot install an APK at all (for example a Huawei phone on HarmonyOS NEXT) cannot run the app; that would show up at the hand-out.
3. **Play Store:** none of the Android phones has it. → Android developer verification does not apply; step 3.3 is dropped.
4. **Test Android phone:** you have none. → The Mac's Android emulator runs the real build 2 as your Android test device (5.4, 6.4), and the hand-out starts with one Android user (6.5).
5. **When the security fixes go to GitHub:** on switch-over day. → Commits stay local until 6.1.
6. **When an update switches on:** after 15 minutes away, if nothing would be lost. → D7 as written.
7. **Broken images in the old app for a few hours on switch-over day:** OK. → Phase 6 as written.
8. **Language:** English only.
9. **Speed test in China:** nobody can run it. → Step 0.5 is dropped; the first real update measures the speed (D8 `ota.download`). If it is too slow, a download mirror reachable from China can be added later without a new app build (the signed update lists the file addresses and their hashes).

**Still open (none blocks the start of work):**
1. Which certificate 万能签 signs your iPhone app with (a bought certificate, or your own Apple ID). It decides how long the app keeps opening before you must sign it again. Check in 万能签's certificate screen when convenient.
2. The Phase 3.0 choices: FileVault, the password manager, where the USB stick and the passphrase paper are kept, and the backup of the Mac. Asked at Phase 3.0.
3. Whether your account is an admin in the app. Checked at step 0.4.

---

## 9. Still UNVERIFIED, and the step that settles each

| Claim | Settled by |
|---|---|
| Chain mode works on both platforms, including for directives and after a leaf rotation | Phase 5, scenarios a, c, e, o |
| `Constants.expoConfig.extra` comes from the manifest's `extra.expoClient` under an OTA launch | scenario a (`session.tsx` falls back to the hard-coded server URL either way) |
| `Updates.manifest.extra.am` (label, notes, critical, native) is readable in JS | scenario a; fallback: read it from the full signed `Updates.manifest`, or from a small signed asset. `metadata` stays `{}` in every case. |
| The fingerprint file sits at `EXUpdates.bundle/fingerprint` (IPA) and `assets/fingerprint` (APK), and a build with expo-updates sets the runtime to `file:fingerprint` (`EXUpdatesRuntimeVersion`; in the APK through `@string/expo_runtime_version`). Phase 2's readers were tested on aapt2 and plutil output and on build 1, which has no expo-updates. | step 5.3 (`otapublish native` fails if not) |
| The real tools do what the fakes in `cmd/otapublish`'s tests do: `expo prebuild --clean --no-install`, Gradle writing `app-release-unsigned.apk` once `withReleaseSigning.js` exists, `pod install`, `xcodebuild archive` with signing off, `runtimeversion:resolve`, `expo export`, and `apksigner`'s password prompt and `verify --print-certs -v` output with the release keystore | step 5.3 (native); the first `ota --dry-run` without `--fixture` after 4.1 (ota) |
| `otactl` and `otapublish` work against the real Pi through the relay: SSH, rsync or the tar fallback, the store found next to `bin/otactl`, the clock check | steps 6.2–6.3 |
| The process check of D6 step 13 lets a publish through on your Mac once you close or accept the listed processes | step 5.3 |
| The IPA recipe of D10 produces an IPA that 万能签 installs | step 5.4 on your iPhone |
| After 4.1 adds expo-updates, `mobile/` still has no package with an install script, so `npm ci --ignore-scripts` installs what `npm ci` installs and the export works. (Checked on 2026-10-25, before 4.1, in a fresh clone: in `shared/` the only install script is esbuild's, and the typecheck and the 92 tests pass without it; `mobile/` had no package with an install script, and its typecheck passes.) | step 4.1 (list the install scripts after the install) and the first `ota --dry-run` without `--fixture` (Phase 5) |
| A reload does not replay the last notification tap or deep link | scenario j |
| Cloudflare passes `.js` assets byte-for-byte after decoding and does not challenge okhttp/CFNetwork | 6.4 (manifest requests from both platforms appear in the census) and 7.1 (D6 step 16 asset check); 9.4 |
| `USER_ACTION_NOT_REQUIRED` and `setAppPackageName` behave as described on the users' phones | scenario k on emulators; step 8.1 on the users' phones (the first real Android test of `app-updater`) |
| Installing a new IPA through 万能签 keeps the app's data | step 8.1 on your iPhone |
| Downloads from mainland China take an acceptable time | the first real update (7.1): `ota.download` times |
| rsync exists on the Pi | step 0.8 |
| Phones without the Play Store are not affected by Android developer verification | re-check at every native release (D10) |
| expo-updates 57.0.26 parses the server's multipart answer (lowercase part headers, a `certificate_chain` part holding only the leaf) on both platforms. Phase 1's Go tests check the bytes against the expo-updates source, not against a running app. | Phase 5, scenario a |
| `internal/ota`'s chain checks accept and reject the same certificates as the client | Phase 5, scenarios a, e and o |

---

## 10. Changes from the review

**Applied** (review finding → where):
- Blockers: Android signing moved out of Gradle; the password is typed at `apksigner` and never stored (D10, 3.2). Every shipped artifact is checked for URL, headers, package id, certificate bytes, chain flag and the allow-unsigned keys, and your phones must reject an unsigned update before hand-out (D10 "Checks on every native artifact", 6.4).
- Release-day order: real-phone test before production changes (5.3–5.4); push, deploy, then upload (6.1–6.3); download before uninstall (6.5).
- Security: `otapublish` built from the clean worktree, `npm ci --ignore-scripts`, process check (D6); the unauthenticated IPA download stated, with an audit check before you install (D10); census not used by any gate, own file, capped (D5, D8); rollback directives on the Pi with the risk stated (D4); per-signature leaf files and full re-signing on rotation (D2, D4); CD stays off (D11, 0.1); no early push of the security fixes (0.2); `metadata` stays `{}` (D6, §9); Android developer verification checked (D10); passphrase and password backups (D4, 3.1, 3.2); clock check (D6 step 4, D10); domain registrar (0.7); restricted emergency key (3.4); CI secret moved to the environment (0.3); no public `/api/version` (D2).
- Expo behaviour: automatic recovery stated precisely (D9); replay limit (D4, scenario e); two cold starts (§0, D7, D9 step 4); channel from extra params; header override banned (D5).
- Codebase: `msSinceCallAction` in all three platform files (D7); rsync check and tar fallback (0.8, D6 step 14); `pendingCount`; flush returns a promise; DebugReporter fact corrected (D8); Settings stack edits (4.2); `otactl init`; corrected facts 3, 6, 7, 11, 12.
- Clarity: one owner for version and tag (4.1, section 6); IPA recipe and push entitlement (D10); every question answered or listed as open (section 8); emergency commands with scope (D9); effort and calendar (section 5); keyed commands run in your Terminal panel (D6); backport branch (D3, section 6); web build and e2e (4.3); unassigned items given steps (1.2, 1.6, 4.5, 6.6); §9 step references; §4 table completed.

**Changes made while building Phase 1 (2026-10-25):**
- D2, manifest table: a revoked id now blocks only a pointer in state `update`. Before, the row "the update id is in `revoked.txt` → 204" also covered `rollback` pointers, so the directive that `rollback-embedded` (D9) points at would never have been served.
- D2, census: no census row is written while `OTA_DISABLED=1` or without `data/ota/FORMAT` (both answer before step 5).
- D2, storage: the exact formats of `.sig`, `.leaf` (lowercase hex SHA-256 of the leaf's DER), `info.json` (`created_at`, not `createdAt`), `runtimes/…json`, the pointer's `since` (RFC 3339) and `revoked.txt`; leaf files are found by hash among `certs/leaf-*.pem`.
- D4: the root's `notBefore` is also 24 h in the past, and a leaf never outlives the root.
- D8 item 2: `sessions.client` is written at once when the value changes; the ring service's value is never stored; a value that does not parse counts as absent.
- D10: the hard gate has its own switch, `hard_gate` in `policy.json` (default `false`). Without it, setting `min_build` would have turned on 426/4426 at once, and the plan's "soft gate (default)" could not exist. Also stated: the paths the hard gate never refuses, 4426 before the authentication check, the `latest.url` rule, and that a broken `policy.json` gates nobody. Scenario l sets `hard_gate`.
- D12 item 4: the deploy test also checks that a release with `data/` is refused (new in `remote_deploy.sh`).
- 1.4: the endpoint is JSON; the page that renders census fields as text is 9.1.
- Section 9: two new unverified claims about the multipart format and the chain checks.

**Changes made while building Phase 2 (2026-10-25):**
- D2, storage: the pointer state `test_unsigned` (beta only; `until`, `previous`) and `test/<id>/` for step 6.4 (before, the plan named the command but no way for the server to serve an unsigned manifest); `.lock`; `log/actions.log` holds one JSON line per change; `info.json`'s `created_at` is exactly the manifest's `createdAt` string.
- D2: `otactl` finds the store next to its own executable, and `OTA_DIR` stays unset on the Pi. Before, an `OTA_DIR` in the units' environment file would have made the server read one store while `otactl` over SSH wrote another.
- D2, "Which copy is authoritative": `pointers.log` holds only the pointer changes `otapublish` makes (it cannot see a halt typed on the Pi); `audit --record` stores the Pi's pointers, revoked ids and policy as the Mac's record (new archive file `ota/revoked.txt`). `audit` compares assets by size and runtimes without `registered_at`, reports expired leaves as history, and exits 1 on a difference.
- D2, "Backup and restore": what `restore-pi` restores and what it cannot (updates signed by an expired leaf, and Pi changes made after the last `audit --record`).
- D3 item 1: the runtime setting is `file:fingerprint` and the runtime is the fingerprint file's content (the APK reaches it through `@string/expo_runtime_version`); unverified until 5.3 (section 9).
- D3 item 4: `ota --dry-run --allow-local` also accepts a local commit.
- D4: the private-key guard matches the PEM header, not the bare words `PRIVATE KEY` (which this plan contains), and also refuses `.pfx`. Key files are ASCII-armored age files; leaf names `<yyyymm>`, `<yyyymm>-2`…; a leaf passphrase equal to the root's is refused; `keys init-root --out` must be under `/Volumes/` on another device. The import guard also covers `cmd/otactl` and `filippo.io/age`.
- D6: every `npx` call is `npx --no -- …`. Step 13's process check waits up to 10 s for processes that the publish itself started and then refuses, but asks `yes` for processes that were already running: refusing every `node` process would block every publish while any long-running `node` program is open. Step 14's tar fallback passes `--no-mac-metadata` only on macOS. `--dry-run` prints the warnings of steps 8 and 9 without asking; `--fixture` and `--pi-dir`/`--public-url` exist. The drill profile also skips the process check and the git tag, uses a fixed passphrase, and archives under `drill/`.
- D9: the emergency key's `rollback-embedded` is `--all-active` and asks for `yes`, like the `--label` form.
- D10, Android: Gradle runs with `--no-daemon` and the Kotlin compiler in-process (no daemon outlives the build and blocks the process check); `zipalign -p -f 4`; `apksigner sign --ks-key-alias alexmessages --v4-signing-enabled false`; `apksigner verify` must show one signer and a v2 or v3 signature; the signer file may use keytool's colon form; where `zipalign`, `apksigner` and the JDK come from.
- D10, iOS: the IPA recipe needs `pod install` after `expo prebuild --no-install` (without it the workspace does not exist).
- D10, checks: booleans must be real booleans; the version is `1.N.0`; N is the highest build registered for either platform + 1, one counter, so `native` always builds both platforms.
- D10, new paragraph "What `otapublish native` keeps and publishes": the artifact directory and `build.json`; `--no-upload` still reads the Pi (clock, build number); `--upload-only` re-checks and is safe to repeat; the archive copies; the download check; the tag after the upload; `native` refuses the drill profile.
- D12 item 1: the tool tests and both guards.
- Phase 2 table: the commands that were built beyond the plan (`promote`, `restore-pointer`, `inventory`, `rollback-embedded --all-active`, `test-unsigned --clear`, `audit --record`), `version` reading `vcs.revision`, `repair-asset --asset`, `status --json` with `"store"`, and 2.3's exact commands (`npm ci --ignore-scripts`, as in D6).
- 2.1: every `otactl` command except `init` and `version` exits with status 3 while the store has no `FORMAT` file, and `otapublish` reads only that status as "no update store yet". Before (fixed in `eddf897`), any failure of `otactl status` counted as no store, so `otapublish native --no-upload` could take N = 2 on a Pi whose store it could not read.
- 2.2 and 3.1: `keys issue-leaf` uploads the certificates once the Pi has an update store (after `otactl init`), not once `otactl` exists; before 6.3 it prints "Not uploaded" and succeeds. `keys upload-certs` copies only unexpired leaves.
- 5.1: the drill builds' runtimes are registered with `otactl register-runtime`, because `otapublish native` refuses the drill profile.
- 5.3: the command needs `--notes`, reads the Pi over SSH, and prints "All artifact checks passed. Artifact directory: <dir>".
- 6.2: `otactl doctor` exits with status 3 until `otactl init` (6.3), so 6.2 checks only `otactl version`; 6.3 checks `doctor` and `audit`.
- 6.4: the beta pointers are put back with `otactl test-unsigned --clear`.
- Section 9: the `npm ci --ignore-scripts` row now holds only what is left after the check of 2026-10-25; new rows for the real build tools, the real Pi, and the process check.

**Updated from your answers (2026-10-25):** section 8 lists each answer and what it changed: the iPhone part now covers only your iPhone with 万能签 (IPA download on `/app` kept; AltStore list, iPhone banner and `LSApplicationQueriesSchemes` removed); Android testing uses the Mac's emulator and the hand-out starts with one user; steps 0.5 and 3.3 are dropped; step 3.0 added.

**Not applied:**
- Running the npm build steps as a separate macOS user (security-ops F2, "Better"): listed as rejected for now in D6.
- A daily automated `otapublish audit` (security-ops F3, item 3): the plan runs it by hand before you install a new IPA.
- Shorter-lived leaves (expo-facts F2, item 3): the leaf stays at 400 days; D9 step 6 (publish a fix promptly) is the chosen answer to replay.
- The verification run was stopped early, so findings that the reviewers had not reached yet do not exist in `OTAUpdatePlan-Review.md`.
