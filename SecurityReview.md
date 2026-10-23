# Gemini observations verified — October 13, 2026

**Note added 2026-10-22:** the validation counts in this file (the 62 shared
Vitest tests and the 41 Playwright tests under **Validation**) are from
2026-10-13, before the DM-calls work. The current counts are in the
**Testing** section of `CLAUDE.md` and `AGENTS.md`, in `shared/README.md`,
and in `web/README.md`.

The confirmed implementation defects are fixed in this local `react` checkout.
Production has NOT received these changes. The initial checkout was clean at
commit `11fe43af193c7acaeed89b0b70e272617a4039f3`.

**Done:** reviewed all 22 observations against the original code, implemented
the corrections below, and added regression coverage. The reports mostly
describe real problems. Qualifications to their exploit claims and operational
conclusions appear below.

**Not done:** publishing mobile binaries and deploying the server changes.
Native compilation passed; mobile export, sharing, and the failed-image UI have
not been exercised on a device. No production database or service was modified.

## Security observations

| Report | Verification of original code | Local correction |
| --- | --- | --- |
| 1.1 Stored XSS and anonymous uploads | Accurate: arbitrary HTML/SVG was served on the app origin without authentication or safe download headers. XSS could export data and use the socket; changing a password still required the current password. | GET/HEAD uploads requires an approved session and ownership or participation in a message referencing that exact file. Only detected inert media is inline; other content downloads as octet-stream with attachment disposition, `nosniff`, sandbox CSP, and private/no-store caching. Mobile media and native preview requests carry bearer headers. |
| 1.2 Upload allocation before size check | Accurate: `io.ReadAll` preceded the 20 MB check. | Limit the complete multipart request to 21 MB, read at most 20 MB + 1 byte from the file, and remove multipart temporary files. A generated 100 MB test stream is rejected before being consumed. |
| 1.3 WebSocket origins and Secure cookies | Code claims accurate. Unrestricted origins did not automatically imply cookie-based hijacking from every foreign site: SameSite=Lax restricts cookie delivery, while same-site hostile origins remained a concern. | Both socket servers require same-origin or an exact configured `WS_ALLOWED_ORIGINS` entry. A connection without an Origin header is accepted; only the Android ring service's call listener sends none. The React Native app's sockets send an Origin built from the server URL, which passes the same-origin check while it matches the request's Host and scheme, and otherwise passes only when it is listed in `WS_ALLOWED_ORIGINS`. Cookies carry Secure for TLS or HTTPS forwarded by loopback nginx. Direct localhost HTTP remains supported. |
| 1.4 Push after logout | Accurate: logout deleted sessions but retained subscriptions. | Delete every session and push subscription for that user in one transaction. This stops new delivery to the stored subscriptions; it cannot recall notifications already handed to a push provider. |

## Broken-feature observations

| Report | Verification of original code | Local correction |
| --- | --- | --- |
| 2.1 Mobile logout | Accurate: the handler only read the cookie. | Logout uses the shared bearer/cookie extractor and invalidates all sessions. |
| 2.2 Mobile password change | Accurate: the old bearer was revoked without returning a replacement. | Password changes retain the authenticated device's existing token and revoke other sessions atomically. The response includes that still-valid `session_token`. The mobile client does not need to replace or reconnect its credential. |
| 2.3 Mobile export path | Confirmed SDK 57 API misuse: `Paths.cache` is a Directory, not a string path. The precise native exception was not reproduced on a device. | Construct `new File(Paths.cache, "alex-messages-export.json")`, create with overwrite enabled, write the archive, then share it. Checked the exact SDK 57 filesystem documentation and installed types. |
| 2.4 Admin links | Accurate: the production host without a port produced the wrong URL in both clients. | One shared resolver maps the deployed messaging hostname to `https://admin.alexanderjia.com/` and uses port 8001 for development hosts, including the Android emulator alias. |
| 2.5 Admin fonts and icons | Accurate: their directories were not mounted. A read-only production request to the font stylesheet returned 404 during this review. | Mount both directories on the admin server; regression tests request the font stylesheet and favicon through the actual engine. |
| 2.6 React release omitted | Accurate: the workflow neither built nor bundled `web/dist`, and extraction could retain a stale build. The public login page served the legacy inline-script shell during this review. | CI builds React and runs Playwright, transfers the tested `react-web-dist` artifact into the deploy job, and includes it in the release. Deployment replaces the entire build directory, rejects releases missing its index, and restores the previous React build with the binaries on rollback. |

## Backend and database observations

| Report | Verification of original code | Local correction |
| --- | --- | --- |
| 3.1 Main socket deadlines | Accurate: reads and writes were unbounded. | Add a 75-second read deadline, 5-second write deadline, and 128 KB frame cap. Broadcast writes run independently so one stalled connection cannot postpone delivery to healthy peers. Failed writes close the socket. |
| 3.2 Socket revocation and phantom messages | Accurate defect; the claimed `created_at: 0` was incorrect because `InsertMessage` returned its attempted timestamp even on error. The ignored error could still broadcast an unsaved message. | Recheck sessions on frames and close idle revoked sockets within 5 seconds, including revocation written by the admin process. Validate author sessions inside message transactions and in the edit statement. Commit messages and attachments together; broadcast only after successful persistence. Registered Meet connections also recheck sessions. |
| 3.3 Disabling/rejecting the last admin | Accurate. The old count also included admins who could not log in. | Protect status changes, demotion, and deletion in atomic conditional database statements. Count approved admins. Concurrent attempts to disable the final two admins leave one approved. |
| 3.4 Same-second pagination/read/clear | Accurate. Raising clock resolution alone would not guarantee correct paging or ordering. | Preserve second-based display timestamps and add a durable monotonic message `position`. Updated clients page by exact `before_id`; read/clear state and receipts use sequence positions. The clock survives purges, and a database trigger assigns positions to messages inserted by an older binary after rollback. Migrate old cutoffs once; legacy timestamp cursors remain accepted for old clients. |
| 3.5 SQLite WAL | Accurate: WAL was not enabled in the original DSN. | Enable WAL on pooled connections and retain busy timeout and foreign keys. A regression test checks the actual journal mode. |
| 3.6 Lost DM state updates | Accurate: full-row read/modify/write could overwrite independent changes. | Use single-statement upserts that modify only the relevant columns; preserve unrelated flags and monotonic read/clear positions. |
| 3.7 Queries per conversation/socket | Accurate. | Batch init histories with a window query and bounded attachment batches, and batch unread/peer-read summaries. Presence/profile broadcasts share two visibility queries across viewers and batch session authorization instead of querying the conversation graph for each socket. |
| 3.8 Orphan uploads | Accurate storage behavior; retention after purge was explicitly documented, rather than an accidental deletion failure. Unsent uploads had no cleanup. | Maintenance removes unreferenced regular files older than 24 hours while preserving recent drafts and referenced files. SQLite write transactions serialize pruning with message attachment insertion. Purging messages relies on the single atomic foreign-key cascade; the admin UI describes the retention interval. |

## Client observations

| Report | Verification of original code | Local correction |
| --- | --- | --- |
| 4.1 Failed messages disappear on init | Accurate: init replaced history. | Preserve pending text, files, delivery state, and conversation entries across init and DM-open snapshots. Reconcile server history by persisted `client_id`. Persist nonces with a unique index so retry cannot save a duplicate. |
| 4.2 Editing before echo | Accurate: the client sent its temporary ID as a server message ID. | Keep the edit pending until echo or history supplies the persisted ID, then send the edit and preserve the local text. |
| 4.3 Invisible iOS image viewer | Accurate from the failure branches: missing data, bad status, or undecodable images could return without opening or dismissing the controller. | Show Close while an uncached image downloads; dismiss on failure; cancel on close and ignore late callbacks. Download the protected image with bearer headers. The native module compiles for ARM64 and x86_64 simulators. |
| 4.4 Mobile debug identity/reporting | Accurate: ingestion was cookie-only and mobile had no reporter. | Use bearer/cookie identity, connect a DebugReporter to the mobile store, and discard queued events at logout. A regression test confirms server-side identity and ignores a supplied user ID. |

## Validation

- `go test -race ./...`, `go vet ./...`, and `go build ./...`: passed.
- Shared TypeScript typecheck and Vitest: 62 tests passed, including 7 new regressions.
- React production build: passed. Playwright: all 41 tests passed.
- Mobile `npx tsc --noEmit`: passed.
- iOS simulator `xcodebuild`, Debug, generic simulator destination, signing disabled: BUILD SUCCEEDED for both simulator architectures.
- Deployment script syntax and 3 isolated release/rollback tests: passed. The tests replace service commands and curl with local stubs; they do not deploy.

An additional confirmed deployment bug was corrected: a curl failure previously
produced `000000`, which passed the script's `000` failure check. Failed health
requests now trigger rollback.

Read-only inspection confirmed that the production Messages and Meet nginx
virtual hosts include `snippets/proxy-common.conf`, which forwards `Host`,
`X-Forwarded-Proto`, and WebSocket upgrade headers. No proxy change was made.

## Remaining release and device verification actions

These actions are outside the completed local code review and fixes.

1. **User, mobile release environment:** build and distribute updated iOS and
   Android apps from this `react` checkout before enabling protected uploads on
   the production server. Include the updated `ImagePreview` native module;
   an OTA JavaScript update alone cannot update its function signature. For
   local device verification, run `npm run ios` or `npm run android` in `mobile/`
   with a development backend, then test Settings → Data → Export my data,
   attachment playback/sharing, and closing an uncached image after a failed
   download. Production signing/distribution credentials were not inspected.
2. **User, repository and GitHub Actions:** commit the reviewed changes on
   `react`, and push them only after step 1's mobile builds are out. The
   push itself deploys: the `Deploy` workflow runs on every push to `main`
   or `react`, and when the repository variable `DEPLOY_ENABLED` is `true`,
   its deploy job ships that push to production as soon as the workflow's
   `Build & test` job passes. This push also ships the DM-calls server.
   Before pushing, check in Settings → Secrets and variables → Actions
   whether `DEPLOY_ENABLED` is `true`, and that the `DEPLOY_SSH_KEY` secret
   exists. If `DEPLOY_ENABLED` was `true` when you pushed, do not run the
   workflow by hand: a manual run deploys a second time. If it was not
   `true`, set it to `true` and then run the `Deploy` workflow by hand with
   branch `react`. The current variable and secret values were not
   inspected, and no push or deployment was performed in this review.

Deployment requirements and proxy headers are documented in `DEPLOY.md`.
