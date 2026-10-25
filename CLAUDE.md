# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Communicating with the user (mandatory)

**THE MOST IMPORTANT RULE: NO AMBIGUITY AT ALL.** Every word and every
sentence must have exactly one possible meaning. If a sentence could be read
two ways, rewrite it before sending.

- **Lead with the direct answer to "does it work right now?"** Say "X works now"
  or "X does NOT work yet" before any explanation.
- **Never call something done, ready, or working when it still needs a step.**
  Example of what NOT to write: "the workflow can deploy" when deploys are
  switched off. Write: "Pushing does NOT deploy yet. It only runs tests."
- **Separate what is done from what is not done**, under explicit labels.
- **List every remaining step as a numbered action** that says who does it (you
  or the user), where exactly, and the exact names and values to use.
- **No vague words** ("set up", "wired", "should work", "ready", "in place")
  without saying exactly what happens and what does not.
- **Never contradict an earlier sentence.** Reread the reply before sending it.

## What this is

**Alex Messages** — an internet-hosted messaging app. The backend is a Go/Gin
server (originally a faithful port of the Python/FastAPI project at
`~/Code/ChatRoom`). On the `react` branch the client layer is a
**shared-TypeScript rewrite**: one framework-agnostic core (`shared/`, imported
as `@alexmessages/shared`) drives two React clients — a responsive **web app**
(`web/`, Vite + React) and a **native mobile app** (`mobile/`, Expo + React
Native for iOS and Android). The legacy vanilla-JS frontend under `static/` is
still shipped and served as a fallback whenever `web/dist` has not been built.

Since the June 2026 upgrades (`Legacy/UpgradeJune.md`, then `UltimateUpdate.md`)
the app has **diverged** from the Python original:

- **Channels were removed entirely.** The app is DM-only: every conversation is
  a `dm:<min>:<max>` thread. No channel catalog, no default channel.
- **Profile photos** — `POST`/`DELETE /api/me/avatar`, files stored at
  `data/avatars/<uid>_<token>.<ext>`, served under `/avatars/`;
  `runtime.PublicUser` carries an `avatar` URL field.
- **Read receipts** — marking a DM read broadcasts a `dm_read` WS event to both
  participants; init/`dm_opened` `dm_state` payloads include `peer_last_read_at`.
- **Message editing** — the author can edit their own text messages: WS `edit`
  client message → `messages.edited_at` stamped → `message_edited` broadcast to
  both participants. History payloads carry `edited_at` (null until edited);
  clients render an inline editor (Save/Cancel) and an "(edited)" tag.
- **Image dimensions** — `/api/upload` decodes images and returns
  `width`/`height`, persisted on `attachments`, so clients reserve a correctly
  sized loading placeholder.
- **Link previews** — `GET /api/link-preview?url=` fetches OpenGraph metadata
  server-side (SSRF-guarded dialer, in-memory TTL cache) for preview cards.
- **UI refresh** — message bubbles (own messages right-aligned), sidebar rows
  show avatar + name + last-message preview, compose icon starts a new chat.
  The rail header is a large **"Messages"** navigation title (Instrument Serif);
  the signed-in account lives in a **bottom-left trigger** that opens Settings.
  The composer is a long pill (iMessage-style) with a circular arrow-up send
  button and thumbnails for pending attachments; the recipient's online status
  is a chip under their name in the topbar.
- **Optimistic send** — a sent message renders immediately with a
  **Sending… → Delivered** footnote (only under the latest own message; flips
  to **Read** once the peer's read receipt covers it). The client tags each
  send with a `client_id` nonce; the server echoes it in the broadcast so the
  optimistic bubble is reconciled in place (no duplicate). If no echo arrives
  within 10s the bubble shows **Failed to send · Retry**; Retry re-sends with
  the same nonce. The "Reconnecting…" banner only appears after the WS has
  stayed down for >3s, and clears on reconnect.
- **Settings overlay** — Apple-System-Settings-style overlay with vertical tabs
  (**Profile / Account / Notifications / Data / Admin** — Admin only for
  admins). Profile: avatar upload/remove, name, bio, Save/Cancel bar. Account:
  account info, password change (`POST /api/me/password` — verifies current
  password, rotates sessions, keeps this device), log out (ends **every**
  session — `POST /api/logout` drops all of the user's sessions), delete
  account (`POST /api/me/delete` — password-verified, refuses the last admin).
  Notifications: a toggle for DM push alerts. Data: JSON export
  (`GET /api/me/export`). Peer profiles use a separate read-only sheet
  (Message · Voice call · Video call · add/remove contact).
- **Voice dictation** — a ChatGPT-style dictation bar (cancel · live waveform +
  timer · stop → transcribe into the composer · send-audio voice message ·
  send → transcribe-and-send) on the web; the mobile app instead morphs the
  composer into an iOS 26 Messages-style recorder (see the mobile section).
  `POST /api/transcribe` accepts an audio clip and
  proxies it to an OpenRouter chat-completion model that accepts
  `input_audio`; the OpenRouter key lives only on the server
  (`OPENROUTER_API_KEY`). wav/mp3 clips route to `mistralai/voxtral-small-24b-2507`;
  other containers route to `google/gemini-3.1-flash-lite`
  (`OPENROUTER_TRANSCRIBE_MODEL` / `OPENROUTER_BASE_URL` env-overridable).
- **Rich media attachments** — image, video, audio, GIF, and generic-file
  attachments; clients render inline image/video players, audio players, and
  file cards from `attachmentKind`/`attachmentLabel` in `shared/src/format.ts`.
- **Mobile bearer auth** — `POST /api/login` returns `session_token` alongside
  the `am_session` cookie so cookie-less clients (the RN app) can authenticate
  REST calls with `Authorization: Bearer <token>`, and the `/ws` upgrade with
  that header or `?token=`.
- **DM calls** — 1:1 voice and video calls inside a DM, on the web client and
  both mobile apps. Ringing and WebRTC signaling ride `/ws`
  (`internal/webapp/calls.go` owns the call state machine); audio and video
  flow peer to peer. Every finished call is logged in the thread as a
  `type="call"` message ("Video call · 4:32", "Missed voice call"). On iOS
  calls ring in the system call UI (CallKit; LiveCommunicationKit in mainland
  China; NOT yet tested on a real iPhone, and the iOS Simulator has no system
  call UI). On Android they also ring
  while the app is closed, through a
  foreground service with its own socket (`/ws?mode=calls`). See **DM calls**
  below.
- **OTA updates (server side and tools; NOT live)** — `cmd/server` serves
  the Expo Updates protocol v1 manifest endpoint, the update files, the
  `/app` install page and the native-build policy from the update store
  `data/ota/`; `cmd/otactl` manages that store on the Pi and
  `cmd/otapublish` signs and publishes from the publisher's Mac. Nothing is
  deployed, the keys do not exist, and no app build contains expo-updates
  yet: OTA updates do NOT work. See **OTA updates** below.
- **Branding** — user-visible name is **"Alex Messages"**. The Go module
  remains `alexmessage`.

There are **three independent server binaries** that share one SQLite database:

| Binary            | Default addr        | Purpose |
|-------------------|---------------------|---------|
| `cmd/server`      | `0.0.0.0:8765`      | User-facing chat app + `/ws` |
| `cmd/admin`       | `127.0.0.1:8001`    | Admin control panel (+ debug console) |
| `cmd/meet`        | `127.0.0.1:8002`    | **Alex Meet** — multi-party meetings |

All three are served through `httpx.Serve` (in `internal/httpx/serve.go`), which
adds slow-loris timeouts (`ReadHeaderTimeout`/`IdleTimeout`, but **no**
`WriteTimeout` — that would kill the WS control plane and the SSE debug stream)
and drains in-flight requests on SIGINT/SIGTERM. Every static directory the
three servers mount (`/static`, `/fonts`, `/sound`, `/avatars`, `/assets`) is
served through `httpx.NoListFS`, so a request for a directory returns 404
instead of a file listing. Each `main` also calls
`db.StartBackgroundMaintenance()`, a 10-minute sweep that deletes upload files
older than 24 hours that no attachment references, prunes expired sessions,
and caps the debug ring buffer; `cmd/server` adds the OTA census prune to it
with `db.AddMaintenance`. Each `main` logs
`<server|admin|meet> built from commit <sha>` at start: `deploy.yml` sets
`main.commit` with `-ldflags "-X main.commit=$GITHUB_SHA"`, and a build
without the flag logs `unknown`.

## Repository layout

```
cmd/
  server/main.go   entrypoint: bootstrap + serve user app
  admin/main.go    entrypoint: bootstrap + serve admin panel
  meet/main.go     entrypoint: bootstrap + serve Alex Meet
  otactl/          the OTA update store's tool on the Pi (verifies, never signs)
  otapublish/      the publisher's Mac tool: keys, signed OTA publish, native
                   builds, audit, restore (never deployed)
internal/
  db/        SQLite schema + every query helper; debug.go (ring buffer) + maintenance.go
  auth/      scrypt hashing, session tokens, admin bootstrap
  push/      VAPID bootstrap + Web Push delivery
  runtime/   main-app shared state: presence, visibility, broadcasts
  httpx/     shared HTTP helpers: {"detail": …} errors, cookies, WS origin check,
             NoListFS (static mounts without directory listings), graceful Serve
  debuglog/  debug-console ingestion: rate-limited /api/debug/report + server-side Emit
  webapp/    the user app: engine + one file per route module
  adminapp/  the admin panel + adminapp/debug.go (debug-console read API)
  meet/      Alex Meet (state.go, ws.go, routes.go, volctoken.go)
  ota/       OTA update store, manifest selection, signature checks, policy;
             ota/census (install census), ota/sign (signing: never in a
             server binary), ota/otatest (test fixtures)
deploy/      remote_deploy.sh (runs on the Pi for every deploy) + its Python test
shared/      @alexmessages/shared — framework-agnostic TS core (see shared/README.md)
web/         React web client (Vite + React 18 + TS)          (see web/README.md)
mobile/      Expo React Native client (iOS + Android)         (see mobile/README.md)
static/      legacy vanilla-JS frontend — fallback when web/dist is absent
index.html, login.html, admin.html, meet.html, meet_login.html   legacy shells
fonts/, sound/                                                   shared assets
```

The webapp route files: `pages.go`, `auth_routes.go`, `me.go`, `account.go`
(password change / account deletion / data export), `users.go`,
`uploads.go`, `push_routes.go`, `dm_state.go`, `history.go`, `ws.go`,
`transcribe.go` (voice dictation proxy), `calls.go` (DM calls: state machine +
signaling), `call_listener.go` (the call listener socket that rings a closed
Android app) and `ice.go` (STUN/TURN servers for calls), plus `avatar.go`
(profile photos), `linkpreview.go` (link previews), `ota_routes.go` (OTA
updates: manifest, update files, `/app`, native-build policy) and
`client_info.go` (the mobile app's `X-AM-Client` header and the hard gate).

## Clients

### `shared/` — `@alexmessages/shared`

Framework-agnostic TypeScript core consumed by both React clients (and unit-
tested directly with Vitest). One module per concern:

- `types.ts` — wire models mirroring the Go JSON **exactly** (snake_case):
  `PublicUser`, `Attachment`, `HistoryMessage`, `BroadcastMessage`,
  `DmStatePayload`/`DmState`, `ServerEvent`, `ClientMessage`, response shapes.
- `dm.ts` — `dm:<lo>:<hi>` channel helpers (`isDM`, `parseDMChannel`,
  `dmPeerOf`, `dmChannelFor`) mirroring `db.DMChannelID`.
- `format.ts` — display formatting ported from `static/app.js`: `PEER_COLORS`
  + `hash()`/`colorFor()` (peer avatar colors — must match the server/web
  palette byte-for-byte), `nameFor`/`handleFor`/`initialsFor`, `fmtTime`,
  `fmtSize`, `attachmentKind`/`attachmentLabel`, `lastMessagePreviewFor`,
  `bodySegments` (text + mention/link segmentation), `firstUrl`.
- `api.ts` — `ApiClient`: REST client with two auth modes — the web uses the
  HttpOnly `am_session` cookie; native passes `token` sent as
  `Authorization: Bearer`. Throws `ApiError(status, detail)`.
- `socket.ts` — `ChatSocket`: `/ws` control plane. Sends are queued while the
  socket is down and flushed on reconnect; fixed 1.5 s retry with a delayed
  "reconnecting" notice so blips never flash UI; `?token=` auth for native;
  keepalive ping; injectable `WebSocketImpl` for tests.
- `store.ts` — `ChatStore`: the client state machine. Owns all chat state,
  reduces every `ServerEvent` into it, exposes the actions the UI calls
  (send/edit/reply/pin/read/contacts/typing…). Framework-free — views
  subscribe and re-render on a bumped `version` counter (React binds via
  `useSyncExternalStore`). Platform side effects (toasts, reconnect banner,
  local notifications) surface through `onToast`/`onNotify` hooks.
- `debug.ts` — `DebugReporter`: batches `Debug.*` events and streams them to
  `POST /api/debug/report` (same contract as the legacy clients).
- `wav.ts` — PCM→16-bit WAV encoder; dictation captures mono PCM and packages
  it as WAV so no server-side transcoding is needed.
- `call.ts` — `CallManager`: the WebRTC call engine both clients share,
  behind a `CallPlatform` adapter (see **DM calls**).

Run its tests with `cd shared && npm test` (Vitest). `npm run typecheck` =
`tsc --noEmit`.

### `web/` — React web client (`@am/web`)

Vite + React 18 + TypeScript SPA. Structure:

- `src/client.ts` — app singletons: one `ApiClient`, one `ChatStore`, a `toast`
  helper; wires `visibilitychange` → `store.setForeground`, and
  `onUnauthorized` → `/login`; closes a ringing call's Web Push notification
  once the call is answered or declined (see **DM calls**).
- `src/push.ts` — Web Push lifecycle (service worker registers eagerly;
  `pushManager.subscribe()` is gated behind a real user gesture — the "Stay in
  the loop" modal or the Settings toggle).
- `src/audio/recorder.ts` — WebAudio PCM capture → `encodeWav` for dictation.
- `src/call-platform.ts` — the browser's WebRTC behind the shared
  `CallPlatform`; `components/CallView.tsx` (ringing card, call view,
  floating mini window) and `components/CallEntry.tsx` (call cards in the
  thread).
- `src/screens/` — `LoginScreen`, `ChatScreen`.
- `src/components/` — `Rail` (DM list + bottom-left account trigger), `Stream`
  (message list), `Topbar`, `Composer` (+ `DictationBar`), `MessageBubble`,
  `SettingsModal`, `Modals`, `Overlays`, `Avatar`, `icons`.
- `src/hooks.ts`, `src/format-ui.tsx` — `useSyncExternalStore` bindings and
  React-side formatting glue.
- `e2e/` — Playwright suite (49 tests across 8 specs) + `start-server.sh`,
  which builds an isolated server on :8795 against its own database in
  `$E2E_RUN_DIR` (default `/tmp/am-e2e`; it persists between runs), seeded by
  `web/e2e/seed` (a tiny Go program).

`vite.config.ts` proxies `/api`, `/ws`, `/uploads`, `/avatars`, `/static`,
`/fonts` to `localhost:8765` so `npm run dev` works against a local Go server.
`npm run build` = `tsc --noEmit && vite build` → `web/dist`. When
`web/dist/index.html` exists, the Go server serves the SPA for `/` and `/login`
(and mounts `/assets`); otherwise it serves the legacy static shells — so a
fresh clone still runs without a Node toolchain.

### `mobile/` — Expo React Native client

Expo + React Native app (Expo Router, file-based routes in `app/`) sharing
`@alexmessages/shared` via a `file:../shared` dependency. Targets iOS and
Android from one codebase, plus `expo start --web`.

- `app/` — routes: `index` (splash/bootstrap), `login`, `chat` (DM list — the
  home tab; two-pane on wide screens), `conversation` (a single DM thread —
  pushed as a separate route on narrow screens, embedded as the right pane on
  wide ones), plus `settings`, `new-chat`, `profile` as **modals**.
  `settings/` is its own nested native stack (`_layout` + `index`, `profile`,
  `account`, `notifications`, `data`, `admin`), so sections push with the
  platform transition and the native header owns back/Done; the grouped-list
  primitives live in `src/components/SettingsList.tsx`.
- `src/session.tsx` — the mobile twin of `web/src/client.ts`: one `ApiClient` +
  one `ChatStore` created once the server URL and bearer token are known.
  Auth token in `expo-secure-store` (AsyncStorage fallback on web); server URL
  from `EXPO_PUBLIC_AM_SERVER` or `app.json → extra.defaultServerUrl`;
  `AppState` drives `store.setForeground`. On Android dev builds, `localhost`
  server URLs are remapped to `10.0.2.2` (the emulator's host alias).
- `src/notify.ts` — local notifications (the managed-Expo analogue of the
  legacy Android `MessagingService`): the shared socket stays connected while
  the process lives, `store.onNotify` posts a per-conversation notification,
  and taps route back to that conversation.
- `src/audio.ts` — `useVoiceRecorder` (expo-audio) for dictation + voice notes.
- `src/call/` — DM calls: `CallHost` (opens the `app/call.tsx` full-screen
  route, call sounds, the floating tile), `CallScreen`, `platform.native.ts`
  (react-native-webrtc behind the shared `CallPlatform`), `CallVideo`
  (`RTCView`), `callAudio.native.ts` (react-native-incall-manager),
  `system.ios.ts` / `system.android.ts` (the phone's call handling, below);
  `.ts` twins are the Expo-web stubs. `src/media.ts` stops and blocks in-app
  media during a call.
- `src/theme.ts` — the shared "paper + sage" design tokens (background
  `#F6F4EE`, paper `#FAF8F1`, sage `#4F7A5E`, deep sage `#355040`, incoming
  bubble `#E9E8E0`…), the `type` scale, the `720` wide-layout breakpoint, and
  `glassSupported` (iOS ≥ 26 with the Liquid Glass API present).
- `src/components/` — `Glass` (glass primitives + `EdgeFade`), `NativeMenu`
  (context / popover menus), `DMList` (rows wrapped in `SwipeRow`:
  iMessage-style swipe right → Unread/Read + Pin, left → Delete, full swipe
  fires the outermost action), `SwipeRow`,
  `ConversationView`, `MessageList`, `Bubble`, `Composer` (+ `DictationBar`),
  `Avatar`, `Icon`, `Sheet`, `ActionSheet`, `attachments`, `ImageViewer`,
  `Toasts`. On iOS, images open in the native viewer from `modules/native-menu`
  instead; `ImageViewer` is the Android/web fallback.
- `modules/native-menu/` — local Expo module (Swift): the UIKit context-menu
  view and glass menu button behind `NativeMenu` on iOS, and the UIKit image
  viewer behind `previewImage`.
- `modules/system-calls/` — local Expo module (Swift): CallKit, or
  LiveCommunicationKit in mainland China, behind `src/call/system.ios.ts`.
- `modules/call-ringer/` — local Expo module (Kotlin): the Android ring
  service that rings calls while the app is closed, behind
  `src/call/system.android.ts`.

Platform styling is deliberate: **liquid glass on iOS 26+** and **flat
Material styling on Android** — same layout and the same sage palette. All
floating chrome goes through `src/components/Glass.tsx`: `GlassSurface` /
`GlassIconButton` render `expo-glass-effect` `GlassView` on iOS 26+, a
translucent paper chip on older iOS, and an elevated surface on Android.
Glass stays light — matching the system (even Apple's Photos keeps light
glass over a near-black photo); the material itself keeps content legible.
The scroll-edge treatment is `EdgeBlur` (a `CABackdropLayer` + private
`variableBlur` CAFilter — private API, see `mobile/AGENTS.md`) on iOS 26+ and
`EdgeFade` (a gradient) elsewhere. The design is
iOS-native: the DM list has glass account/compose buttons, a large title,
search, and pinned threads as a large-avatar grid; the conversation runs edge
to edge under a glass header capsule (avatar · name · presence) and a floating
glass composer (round "+" beside the input pill). Voice messages morph the
composer in place like Messages: tapping the waveform swells the pill over
the "+" (both glass shapes share a `GlassContainer`, so they melt together)
into a recorder (live waveform · timer · stop); stop peels an X back out and
turns the pill into a review player (play · waveform · duration · transcribe
→ composer · send). Geometry is Reanimated in `Composer`; contents and the
recorder live in `DictationBar` (`VoicePanel`). Bubbles are iMessage-style
runs (own = sage, right; peer = warm grey, left; no avatars in a DM) with
centered timestamps at hour-long pauses; Settings is an inset-grouped list
whose Profile/Account/Notifications/Data/Admin pages are native stack pushes
inside the sheet. Voice messages (`voice-message.<ext>`, see
`isVoiceMessage` in `shared/src/format.ts`) render as a play · waveform ·
time bubble on every client; other audio uploads stay a named audio card.
Playback must go through `enablePlayback()` (`src/audio.ts`) — expo-audio's
partial `setAudioModeAsync` resets `playsInSilentMode`, and the iOS silent
switch then mutes everything.
Type sizes come from `theme.type` (iOS Dynamic Type defaults). Wide layouts
(iPad, tablets, desktop web) switch `chat` to a two-pane rail + conversation.

- `e2e/` — Maestro YAML flows: `chat`, `dictation`, `attach`, `new-chat`,
  `settings`, `dm-actions`, `reply-edit`, `tour` (8 flows; `subflows/login.yaml`
  is the shared sign-in).

**Expo caveat:** this project pins Expo SDK ~57 — read the exact versioned
docs at https://docs.expo.dev/versions/v57.0.0/ before writing code that
touches Expo APIs; the platform has changed.

## Backend additions for the TypeScript clients

- **SPA serving** (`internal/webapp/pages.go`, `webapp.go`) — when
  `web/dist/index.html` exists it is loaded once at startup into `s.spaHTML`
  and served for `/` and `/login`; `web/dist/assets/` mounts at `/assets`.
  Otherwise the legacy `index.html`/`login.html` shells are served — the
  pre-React frontend is the zero-dependency fallback.
- **Bearer auth** (`auth_routes.go`, `ws.go`, `httpx.SessionToken`) —
  `/api/login` returns `session_token`. REST routes accept
  `Authorization: Bearer <token>` in addition to the `am_session` cookie;
  only the `/ws` upgrade (including the call listener on `/ws?mode=calls`)
  also accepts `?token=`. Close code **4401** still signals unauthenticated.
- **Voice dictation** (`transcribe.go`) — `POST /api/transcribe` accepts a
  multipart audio clip (≤ 20 MB), maps it to an OpenRouter `input_audio`
  format, and calls the chat-completions endpoint with the
  `OPENROUTER_API_KEY` (server-side only). wav/mp3 → Voxtral; other
  containers → Gemini. 60 s upstream timeout. Golden/unit-tested in
  `transcribe_test.go`.

## DM calls (1:1 voice and video)

Two users in a DM can call each other with voice or video from the web client
and from both mobile apps (iOS and Android). The Expo **web** build has no
calls. The server only signals; audio and video flow **peer to peer over
WebRTC**. On iOS a call rings and runs in the system call UI (CallKit, or
LiveCommunicationKit in mainland China). On Android a call also rings while
the app is closed, through a foreground service that holds its own small
socket to the server (no push: the target phones have no Google Play).

**Server** (`internal/webapp/calls.go`, `internal/webapp/call_listener.go`,
`internal/webapp/ice.go`, `internal/db/calls.go`):

- The server owns the call state machine: ringing → active → ended. An
  unanswered call rings for 45 s (`callRingTimeout`), then ends as
  `no_answer`. A user is in at most one call across all of their devices.
  Calling someone who is already in a call ends at once as `busy`; calling
  someone with no open socket, no call listener (below), and no Web Push
  subscription ends at once as `unavailable`. Two users who dial each other
  at the same moment are joined into one call.
- A call binds exactly two sockets — the device that placed it and the device
  that answered — and only those two exchange signaling. Answering on one
  device stops the ringing on the callee's other devices and call listeners
  (`call_ended` with reason `answered_elsewhere`).
- When a bound socket drops without a close frame or with any close code
  other than 1001, the call survives for 30 s (`callResumeGrace`): the
  reconnecting client sends `call_resume` and receives the signals held for
  it (at most 256); the peer-to-peer media keeps flowing meanwhile. A bound
  socket that closes with code 1001 (Going Away: browsers send it when the
  page is closed or reloaded) ends the call at once, as `completed` if it was
  answered and as `cancelled` if it was still ringing, because that page's
  media is gone (`callSocketGone`, tested by `TestCallEndsWhenPageUnloads`).
- Ringing also goes out as a Web Push (`kind: "call"`, tag `call-<id>`,
  `require_interaction` true: it stays on screen until something replaces or
  closes it) when the callee has a Web Push subscription and none of the
  callee's `/ws` sockets is visible. A socket counts as visible from the
  moment it connects until it sends `visibility {visible: false}`, and again
  after `visibility {visible: true}`. The shared `ChatStore` sends that frame
  from `setForeground` whenever the app moves between foreground and
  background, and sends `visible: false` right after each `init` while the
  app is in the background. The legacy `static/app.js` never sends it, so its
  sockets always count as visible. A call listener never counts as visible.
  After the ringing push the callee gets exactly one more push under the same
  tag: `missed_call` ("Missed video call" / "Missed voice call") when the
  call ends unanswered without a decline, or `call_over` when the call is
  answered on any of the callee's devices (sent at the moment of the answer:
  "Answered video call") or declined ("Declined video call");
  `call_over` has `require_interaction` false. The service worker
  (`static/sw.js`) handles `call_over` by closing the `call-<id>`
  notification and showing a silent, non-sticky notification with that body
  under the same tag. While a tab of the app is visible and focused, nothing
  stays on screen: a Chromium-based browser (one that lists the `Chromium`
  brand in `navigator.userAgentData.brands`) shows nothing, which Chromium
  allows while a tab of the site is focused; every other browser shows the
  notification and closes it at once, because Safari revokes a push
  subscription after 3 pushes that show no notification, focused tab or not.
  While a web tab of the callee is open,
  that tab also closes the ringing notification itself once the call is
  answered or declined, on this device or another (`web/src/client.ts`), so
  it does not wait for the push service.
- **Call listener** (`call_listener.go`): a second, small socket on
  `/ws?mode=calls` (the same path and auth as `/ws`, so a proxy's `/ws` rule
  covers it). The Android ring service holds it for as long as the service
  runs (while the app is open too), so a call rings while the app is
  closed. It carries only ringing. The server sends `call_incoming {call}` when a call
  rings for its user (and on connect, for a call that is already ringing),
  `call_ended {call_id, reason}` when that call stops, and `ping` every 30 s
  (`callListenerPing`: the phone sleeps between packets, so the server sends
  the heartbeat and the phone answers `pong`; 30 s also stays inside nginx's
  default 60 s `proxy_read_timeout`, which closes a proxied WebSocket that
  gets no data from the server). The client sends
  `call_decline {call_id}` (the notification's Decline; it declines only a
  call that is still ringing for that user, never one already answered) and
  `ping`. A listener silent for 150 s is dropped (`callListenerTimeout`); its
  session is re-checked every 5 s (`callListenerCheck`), and a revoked
  session gets close 4401. A listener counts toward reaching the callee but
  not toward being online, and it is never bound to a call: Answer opens the
  app, whose own `/ws` socket accepts.
- Every call that ends writes **one `type="call"` message** into the DM
  thread and one row in the `calls` table (`status`: completed, declined,
  failed, cancelled, no_answer, unavailable, busy; plus `media`,
  `answered_at`, and the duration). The message body is a plain-text summary
  for clients that don't render calls; history and live payloads carry the
  record as `call`. A missed call counts as unread for the callee; completed,
  declined, and failed calls don't.
- Logging out (`POST /api/logout`, which ends every session of the user)
  and deleting your own account (`POST /api/me/delete`) first end the call
  you are on or ringing for, as a hang-up would (`endUserCall`): `completed` if
  it was answered, `cancelled` if you were calling, `declined` if you were
  being called. The peer gets `call_ended` at once, and the thread entry is
  written while both users still exist. When the admin panel (a separate
  process) deletes a user during a call, the call is still logged when it
  ends: the deleted user's column (`caller_id` or `callee_id`) is stored as
  `NULL` and sent as `null`, and when the deleted user was the caller, the
  thread entry's `user_id` and `author` are `null` too.
- ICE servers (`ice.go`) come from the environment: `STUN_URLS` (default:
  Google's public STUN servers), `TURN_URLS` (empty by default: no TURN), and
  either `TURN_SECRET` (coturn's `static-auth-secret`: each call gets a
  time-limited TURN REST credential minted per user) or static
  `TURN_USERNAME` / `TURN_CREDENTIAL`. Without TURN, a call between two
  devices behind strict NATs (some mobile carriers, corporate networks)
  cannot connect its media — see `DEPLOY.md`.

WS protocol — client → server: `call_start {peer_id, media, client_call_id,
device}`, `call_accept {call_id, device}`, `call_hangup {call_id, reason?}`
(`reason: "failed"` marks an answered call whose media never connected),
`call_signal {call_id, payload}`, `call_resume {call_id, device}`. Server →
client: `call_outgoing {client_call_id, call}`, `call_incoming {call}`,
`call_ringing {call_id}`, `call_accepted {client_call_id, call}`,
`call_signal {call_id, payload}`, `call_resumed {call}`,
`call_ended {call_id, client_call_id?, reason}`,
`call_error {client_call_id, error}` (`invalid` or `busy_self`). Signal
payloads: `offer` / `answer` (SDP), `candidate`, `state {mic, camera}`, and
`restart` (the callee asks the caller for an ICE restart). The client also
sends `visibility {visible}` on `/ws` (not a call message: every TS client
sends it), which decides whether ringing also goes out as Web Push (see the
Web Push bullet above). The call listener's smaller protocol is in the
**Call listener** bullet above.

**Shared engine** (`shared/src/call.ts`): `CallManager`, owned by `ChatStore`
as `store.calls`, with its session in `store.state.call`. One implementation
serves both clients through a small `CallPlatform` adapter
(`createPeerConnection`, `getUserMedia`, `createStream`, optional
`releaseStream`). The caller always sends the offer, so offers never collide.
The caller creates sendrecv audio and video transceivers up front, so mute,
camera on/off, and the front/back camera switch are all `replaceTrack` — no
renegotiation. A video call without a usable camera goes ahead with the
camera off; a call without a microphone ends with a message saying why. ICE
"disconnected" for 4 s triggers an ICE restart; media that stays lost for
30 s ends the call (`lost`); an answered call whose media doesn't connect
within 30 s ends as `failed`. An ended session stays in `store.state.call`
for 2 s so the UI can say why it ended. A device that hangs up its outgoing
call before the server has answered its `call_start` sends `call_hangup` for
whichever call the server then names: a late `call_outgoing`, or a
`call_accepted` that joined it to the peer's crossed call (the server logs
that call as `completed`, with a duration of about zero). If the peer's
crossed call is ringing on this device at that moment, it stops ringing at
once (outcome `hung_up`).

**Web** (`web/src/call-platform.ts`, `web/src/components/CallView.tsx`,
`web/src/components/CallEntry.tsx`): voice and video call buttons in the DM
topbar and in the peer profile sheet (a call closes the sheet, as Message
does; the sheet's two call buttons are disabled while a call is live); an
incoming-call card (Decline / Accept); a call view with the
peer's video (their avatar while their camera is off), the self view, and
mute / camera / switch camera (only on devices with more than one camera) /
end. On a phone, whose cameras report a facing mode, Switch camera flips
between the front and back camera; on a desktop, whose webcams report no
facing mode, it opens the next camera by `deviceId`, wrapping around.
Turning the camera off and on keeps the chosen camera, and each new call
starts on the browser's default camera. The self view is mirrored unless its
camera reports facing mode `environment`. The call view minimizes to a
floating window so the chat stays usable; that window sits below every
dialog (Settings, Delete account, the profile sheet, New chat, the push
prompt), while the full-screen call view and the incoming-call card sit
above them. The page plays the ringtone (`sound/RingTone.mp3`) and a
ringback while the peer's phone rings, and shows the "Leave site?" prompt
only while a call is live when the page unloads. Log out hangs up the call
before it ends the session, so the peer sees the call end at once and no
prompt shows; Delete account hangs up only after the deletion succeeds (a
wrong password keeps the call); a session that expires (close 4401) ends the
call on this page before it goes to `/login`. The server also ends the call
at `POST /api/logout`, so the peer sees it end at once even when the page's
socket is down and its hang-up never leaves. Finished calls render as call
cards in the thread with a
"Call again" / "Call back" button.

**Mobile** (`mobile/src/call/`, `mobile/app/call.tsx`, `mobile/src/media.ts`,
`mobile/src/components/CallEntry.tsx`, `mobile/modules/system-calls/`,
`mobile/modules/call-ringer/`):

- Media is `react-native-webrtc`. `src/call/platform.native.ts` adapts it to
  `CallPlatform`; its `stop()` only disables a track, so the adapter also
  releases the native tracks and streams (streams before their tracks).
  `src/call/platform.ts` is the Expo-web stub (`null`: no calls).
- Sounds and audio routing are `react-native-incall-manager`
  (`src/call/callAudio.native.ts`): the ringtone with vibration (on iOS it
  obeys the ring/silent switch), the in-call audio session (voice calls on
  the earpiece with the proximity sensor, video calls on the loudspeaker,
  screen kept on), the ringback, and the speaker toggle. On iOS, while the
  system call UI holds a call, `CallAudio.swift` does this job instead (see
  the iOS bullet below). The two sound files
  reach the native bundles through the `expo-asset` plugin in
  `mobile/app.json` and must keep their names, which InCallManager looks up:
  `mobile/assets/sounds/incallmanager_ringtone.mp3` (a copy of
  `sound/RingTone.mp3`) and `incallmanager_ringback.mp3` (440 + 480 Hz, 2 s
  on, 4 s off).
- `src/call/CallHost.tsx` wraps the root Stack. It pushes the `/call` route
  (a `fullScreenModal` that no swipe dismisses) when a call starts, runs the
  call sounds, removes the incoming-call notification once the call stops
  ringing, and — while the call screen is minimized with its chevron — floats
  a tile (the peer's video or avatar) that reopens it. On iOS, while a modal
  sheet (profile, settings, new chat) is open, the tile lives in a
  react-native-screens `FullWindowOverlay` (`OverSheet`), so it stays above
  the sheet; on every other route it stays in the root view, under context
  menus, the image viewer, and the closing call screen. An `Alert` always
  covers the tile (React Native shows it in a window of its own). Two iOS
  side effects, NOT verified on a device: while a sheet is open, the tile
  also draws above anything presented over that sheet (the Settings →
  Profile photo menu and the photo picker), and after a minimize onto a
  sheet it shows at once over the call screen while that screen slides
  away. Android draws the tile above the sheets without an overlay. The DM
  header's call button opens a menu (Video Call / Voice Call): the native
  pull-down on iOS 26, a Material dropdown anchored under the button on Android
  (`GlassMenuButton` with `dropdown`), the action sheet on older iOS. During
  a call it shows a green "Return to call" button instead. The contact page
  (`app/profile.tsx`) has Voice Call and Video Call buttons beside Message:
  the call screen opens over the contact page, ending the call returns to
  it, and the two buttons are disabled while a call is live. The call
  screen opens and closes only
  while the app is not in the background (`useAppVisible`); a call that ends
  in the background closes it when the app comes back. On iOS a modal
  dismissed in the background never finishes its transition, and the app
  came back to a black screen.
- The call screen pads its controls with `useSafeAreaInsets()`, not a
  `SafeAreaView` (which has no insets on its first frames, and none at all
  in an iOS full-screen modal) — see `mobile/AGENTS.md`.
- In-app media yields to calls (`src/media.ts`): starting a call stops any
  playing voice message or video and any recording, and nothing else in the
  app plays or records until the call ends ("Not available during a call").
  Reason: expo-audio and expo-video reconfigure and deactivate the shared iOS
  `AVAudioSession` underneath the call.
- `src/call/system.ios.ts`, `system.android.ts`, and `system.ts` (Expo web)
  connect calls to the phone's own call handling. All three export the same
  seven functions, which `session.tsx`, `CallHost`, and `app/call.tsx` import
  from `./system` (Metro picks the platform file).
- **iOS: the system call UI** — `modules/system-calls/` (a local Expo module
  in Swift) behind `src/call/system.ios.ts`, which mirrors `store.calls` into
  the system and the system's buttons back into `store.calls`.
  `SystemCalls.kind` is chosen once per app launch from the device's region
  setting: `callkit`, unless the region is mainland China
  (`Locale.current.region` = `CN`), where the App Store doesn't allow CallKit
  (MIIT, 2018) and LiveCommunicationKit (iOS 17.4+) takes its place
  (`livecommunication`). iOS before 17.4 in China gets `none`, and the app
  rings by itself as before. Every call, incoming and outgoing, is reported to
  the system. The system rings an incoming call with its own incoming-call
  screen (on the lock screen too), shows every call in the status bar and
  Control Center, and its answer, end, and mute buttons come back as
  `onAnswer` / `onEnd` / `onMute`. One call at a time (`maximumCallGroups` 1), `.generic` handles,
  and calls stay out of the Phone app's Recents
  (`includesCallsInRecents = false`). The system owns the audio session while
  it holds a call: `CallAudio.swift` keeps WebRTC's audio unit off (manual
  audio) until the system activates the session, and does InCallManager's
  job for that call (ringback, earpiece / loudspeaker, proximity sensor,
  screen kept on). A call the system refuses (`ERR_REFUSED`) falls back to the
  app's own ringing and audio; a call that a Focus, Do Not Disturb, or the
  block list filtered (`ERR_FILTERED`) does not ring at all. `app.json` adds
  the `voip` background mode. Without VoIP push the system hears of a call
  only while the app runs (see **Not built**). In the iOS Simulator `kind` is
  always `none` (`#if targetEnvironment(simulator)`, as Signal does): the
  Simulator has no system call UI, so iOS ends every call reported to
  CallKit or LiveCommunicationKit there within seconds (`callservicesd`
  logs "Disconnecting call because there wont be a UI to host the call"),
  which the app would record as declined (incoming) or cancelled
  (outgoing). **Status (2026-10-21): the module builds, and in the iOS 27
  Simulator both providers took calls (CallKit; LiveCommunicationKit with
  the region set to China) until iOS ended them as above. The system call
  UI itself (its ringing, the lock screen, answer / end / mute, the audio
  session handoff) is NOT tested yet: that needs a real iPhone.**
- **Android: the ring service** — `modules/call-ringer/` (a local Expo module
  in Kotlin) behind `src/call/system.android.ts`. Phones without Google Play
  services get no push, so a foreground service (`RingService`, type
  `specialUse`) holds the call listener socket (`RingSocket`, OkHttp,
  `/ws?mode=calls`) while the app is in the background, swiped away, or
  closed. It shows a silent "Ready for calls" notification (channel
  `am_call_service`; Android lists it under Silent). A ringing call posts a
  `CallStyle` notification (channel `am_incoming_calls`: the app's ringtone,
  repeating, with vibration; Decline and Answer; a full-screen intent that
  opens the call screen over the lock screen, where leaving the call screen
  asks for the unlock). Decline sends `call_decline` over the listener
  without opening the app. Answer opens the app, which answers once the call
  reaches its own `/ws` (it waits up to 30 s, which covers a cold start).
  While the app is on screen, the app rings instead: it claims each call it
  rings (`CallRinger.claim`), and the service holds its notification back
  until the app releases the call. Turning the screen off while the app
  shows a ringing call silences that call (the service posts its
  notification without sound), as the power button does for phone calls. A
  call missed while the app is off screen posts "Missed voice call" /
  "Missed video call" (channel
  `am_missed_calls`) in the conversation's notification slot (tag = the
  channel, id 0: the slot expo-notifications uses for that conversation's
  message notification), so a conversation shows one notification.
  The service keeps running after the app is swiped away. Android restarts
  it about 1 s after its process is killed (`START_STICKY`), and
  `RingReceiver` restarts it after a reboot (`BOOT_COMPLETED`) and after an
  app update (`MY_PACKAGE_REPLACED`); for those restarts the session token is
  stored sealed with an AES-GCM key that stays in the Android Keystore
  (`RingStore`). A 5-minute alarm (`setAndAllowWhileIdle`) reconnects a
  socket that is down or has been silent for 150 s, and a change of the
  default network reconnects it at once; reconnects back off from 1 s to
  60 s, plus jitter. The service runs only
  while the user is signed in, **Ring when the app is closed** is on
  (Settings → Notifications, Android only, default on, stored as
  `ring_when_closed`), and notifications are allowed
  (`CallRinger.notificationsAllowed()`: the app's notifications are on and
  its "Incoming calls" category isn't blocked). Without notifications no
  call could show, so the service stops itself and the phone stops counting
  as reachable. With the setting on, the app asks for the notification
  permission at sign-in and at app start (Android stops showing the prompt
  after two denials); the Settings page then shows **Allow notifications**,
  **Allow full-screen calls** (Android 14+ lets the user deny them), and
  **Allow background use** (the battery-optimization exemption) for each
  one that is missing. Logout, a close 4401 on the listener, and an app
  start that ends on the login screen (no stored token, or the startup
  `GET /api/me` fails) stop the service and clear its stored session. Any
  failure of that startup `GET /api/me`, a network error included, discards
  the stored token, so opening the app offline signs the user out and stops
  the service until the next sign-in. With the setting off, Android rings as
  before: by itself while the app's process runs, and with a local
  notification while the app is in the background.
- Which notification rings a call while the app is in the background: on
  iOS, the system call UI (a local notification only for a call the system
  refused and didn't filter); on Android, the ring service's call
  notification, or, with the service off, a local notification on channel
  `calls` (maximum importance).

**Not built** (known limits):

1. No TURN server is deployed by default (see `DEPLOY.md`).
2. No VoIP push. iOS: PushKit needs a paid Apple Developer account (the
   project has none), so an iPhone rings only while the app's process runs
   with its socket connected — in practice while the app is open, because
   iOS suspends a backgrounded app within seconds. Android: no FCM (the
   target phones have no Google Play services); the ring service replaces
   it.
3. A phone maker's battery saver can stop the ring service for good
   (Android itself restarts a killed service after about 1 s). Calls then
   ring only after the app is opened again, which starts the service.
   **Allow background use** (Settings → Notifications) asks for the
   battery-optimization exemption, which makes this less likely.
4. No microphone or camera foreground service during a call: Android stops
   giving a backgrounded app microphone and camera input, so the peer hears
   and sees nothing from an Android phone while the app is in the
   background. The ring service is a `specialUse` service, which doesn't
   lift that rule.
5. No Bluetooth audio routing on Android (the app doesn't request
   `BLUETOOTH_CONNECT`).
6. Live calls are held in memory: a server restart ends every call in
   progress.
7. Group calls are Alex Meet's job (below), not DM calls'.

## OTA updates (Phases 1–2 of `OTAUpdatePlan.md`: server and tools)

**Status (2026-10-25): OTA updates do NOT work.** The server side and the
tools below are in local commits only (not pushed, not deployed). The keys
do not exist (Phase 3), and no app build contains expo-updates (Phases
4–6), so `otapublish ota` stops at its runtime step and `otapublish native`
cannot build a release. The tools have run only in tests, against a
temporary directory standing in for the Pi. The operations (the tools'
commands, emergency commands, kill switch, publishing, audit, restore,
keys, leaf renewal) are in `DEPLOY.md` → **OTA updates of the mobile app**.

- **Update store** `data/ota/` (`OTA_DIR` overrides it; `ota.DirFromEnv`).
  Layout and file formats: `internal/ota/store.go` and `DEPLOY.md`. The
  server only reads it; `bin/otactl` is its only writer.
  Without `data/ota/FORMAT` the store counts as absent and every manifest
  request gets 204. Deploys never touch it: `deploy/remote_deploy.sh`
  refuses a release that contains `data/`.
- **`internal/ota`**: store reads (channel pointers in state
  `update | rollback | halted | test_unsigned`, registered runtimes,
  `revoked.txt`, the signed `manifest`/`rollback` bytes with their `.sig`
  and `.leaf` files),
  `Store.Select` (the manifest logic of plan D2: the beta pointer only when
  `Expo-Extra-Params` has `am_channel="beta"` and a beta pointer exists; a
  revoked id blocks only a pointer in state `update`), the multipart writer
  (part headers are written in lowercase, because iOS reads
  `expo-signature` case-sensitively), RFC 8941 parsing (`expo-signature`,
  `Expo-Extra-Params`, `Expo-Recent-Failed-Update-IDs`), and
  certificate-chain verification with expo-updates' rules (`VerifyChain`,
  `VerifySignature`). `update.go` holds the checks that `otactl` and
  `otapublish` share: the strict parser of the manifests `otapublish`
  writes, the `rollBackToEmbedded` directive, `info.json`, and
  `VerifyUpdateFiles` (an update's files and both signatures against the
  root). A beta pointer in state `test_unsigned` (`until`, `previous`)
  serves the unsigned manifest `test/<id>/manifest.json` until `until`
  (`otactl test-unsigned`, plan step 6.4; `Store.SelectAt`).
- **`internal/ota/sign`** signs manifests and issues certificates. Only
  `cmd/otapublish` and tests import it. `TestServerBinariesNeverImportSigning`
  fails if `cmd/server`, `cmd/admin`, `cmd/meet` or `cmd/otactl` depends on
  it, on the test fixtures in `internal/ota/otatest`, or on `filippo.io/age`;
  `TestNoPrivateKeysTracked` fails on a tracked PEM private key or a tracked
  `.key`, `.p12`, `.pfx`, `.jks` or `.keystore` file (both in
  `internal/ota/sign/sign_test.go`).
- **Routes** (`internal/webapp/ota_routes.go`, no login):
  `GET /api/ota/manifest` (every answer carries `expo-protocol-version: 1`,
  `expo-sfv-version: 0` and `Cache-Control: private, max-age=0, no-store`;
  400 for bad headers; 204 when `OTA_DISABLED=1`, when there is no store, or
  when nothing applies; otherwise a multipart `manifest` or `directive` part
  plus a `certificate_chain` part holding the leaf named in the `.leaf`
  file), `GET`/`HEAD /ota/assets/<sha256-b64url>.<ext>`,
  `GET /api/app/latest`, `GET /app` (the install page), and
  `GET`/`HEAD /app/<android|ios>/AlexMessages-<version>-<build>.<apk|ipa>`.
  The server never sends `expo-manifest-filters` or
  `expo-server-defined-headers`. Its 400 and 404 answers carry
  `Cache-Control: no-store`, because Cloudflare caches 404s by extension.
- **Install census** (`internal/ota/census`, `data/ota/census.db`, a SQLite
  file of its own): every manifest request with `EAS-Client-ID` and
  `Expo-Embedded-Update-ID` updates that install's row. A new row needs an
  embedded id registered in `runtimes/` and fewer than 5,000 rows. A new
  failed update id or fatal error emits the debug event `ota.client_failure`
  (at most 30 per hour). Every field is an unauthenticated claim. `cmd/admin`
  serves it at `GET /api/ota/installs`. `POST /api/me/delete` and the admin
  user delete remove the rows whose `am_uid` is that user, and
  `GET /api/me/export` includes them as `ota_installs`. The maintenance
  sweep of `cmd/server` deletes rows unseen for 90 days.
- **`X-AM-Client`** (`internal/webapp/client_info.go`), sent by app builds
  from build 2 on: `v=1;platform=android;version=1.2.0;build=2;runtime=<fp>;update=<uuid|embedded>;channel=<production|beta>`
  (the Android ring service sends `v=1;platform=android;build=2;component=ring`).
  REST calls send it as a header; the `/ws` upgrade sends it as the
  `client=` query parameter. On an authenticated request it is stored on
  the session (`sessions.client`, `sessions.client_seen_at`): when it
  changed, else at most once per 10 minutes, and never the ring service's
  value. A missing or malformed value is never gated and stores nothing, so
  the web client and the builds before 2 are unaffected.
- **Native-build policy** `data/ota/native/policy.json`: per platform
  `min_build`, `hard_gate` and `latest {version, build, url, sha256, size,
  notes}`. With a valid `X-AM-Client`, the WS `init` carries
  `client_policy {state: ok | update_available | update_required, latest}`
  (the soft gate). Only while `hard_gate` is true for the platform and the
  build is below `min_build` (the hard gate): REST calls under `/api/`
  answer 426 (except `/api/app/`, `/api/ota/`, `/api/debug/report` and
  `/api/logout`), and `/ws` and the call listener close with **4426** before
  the 4401 check. A policy file that cannot be read or is invalid gates
  nobody.
- **`cmd/otactl`** (cross-compiled into `bin/` by `deploy.yml`; runs on the
  Pi): the only writer of `data/ota/`. It holds no key: `ingest` verifies an
  upload in `incoming/<publishId>/` completely (chains, asset hashes, both
  signatures of every update, write-once bytes) before it moves anything;
  `activate` / `promote` / `halt` / `rollback-embedded` / `restore-pointer`
  / `test-unsigned` switch pointers; `revoke`, `register-runtime`, `policy`,
  `repair-asset`; read-only `status`, `census`, `runtimes`, `doctor`,
  `inventory`, `version`; `emergency` (the restricted key's forced command:
  one word of `halt`, `rollback-embedded`, `status`). It finds the store
  without the shell's help: `--ota`, else `$OTA_DIR`, else `../data/ota`
  next to its executable when that sits in `bin/` (the Pi), else
  `./data/ota`; so `OTA_DIR` must stay unset on the Pi (SSH sessions never
  read the units' environment file). Every changing command holds
  `data/ota/.lock` and appends a JSON line to `data/ota/log/actions.log`.
  `promote` reads `alexmessage.db` read-only (an admin's app session must
  report the update; no `ota.crash`/`ota.error` report may name it).
- **`cmd/otapublish`** (runs on the publisher's Mac; never deployed; the only
  binary that imports `internal/ota/sign` and `filippo.io/age`): `keys init-root |
  issue-leaf | upload-certs | show`, `ota` (D6: 18 steps; `--dry-run` runs
  steps 1–12; `--fixture` replaces steps with prepared data;
  `--profile drill` uses throwaway keys and a local store), `promote`,
  `republish`, `resign-active`, `status`, `native` (D10: unsigned Gradle
  release build, unsigned `xcodebuild archive` packed into an IPA,
  `zipalign` + `apksigner` with the release keystore, the artifact checks
  in `artifacts.go` that read the APK's binary XML/resources (`axml.go`)
  and the IPA's plists (`plist.go`), `build.json` in
  `<archive>/builds/native-N-<UTC>/`, `--no-upload`, `--upload-only`),
  `audit [--record]` and `restore-pi`. Every keyed command and every
  publish runs in two stages: stage 1 checks the commit out into a clean
  git worktree, builds otapublish there with the commit stamped in, and runs
  it with `--in-worktree`. The archive (`~/AlexMessagesReleases`,
  `$AM_RELEASES_DIR`) is authoritative for the write-once files; the Pi for
  the pointers, `revoked.txt` and `policy.json` (layout in `DEPLOY.md`).
  **Claude never runs a keyed command (`keys …`, `ota` without
  `--dry-run`, `republish`, `resign-active`, `native`) through its own
  command tool**: Claude starts it in the user's Terminal panel, where the
  user types the passphrase or password. Claude may run `ota --dry-run`,
  `status` and `audit` itself.
- **`.github/workflows/mobile-checks.yml`** (never run on GitHub yet): on
  every push and pull request to `main`/`react`, the two guard tests above,
  `shared` typecheck + Vitest and `mobile` `tsc --noEmit`, each after
  `npm ci --ignore-scripts`. No secrets.

## Alex Meet (cmd/meet, internal/meet)

The former 1:1 "AlexMessage Call" was replaced wholesale by **Alex Meet**, a
Google Meet-style meeting app (audio + video + screen share, 4–5 people in
practice, 9 tiles per grid page). One UI, two interchangeable media backends
chosen per meeting from a lobby dropdown:

- **`mesh` — Standard WebRTC ("Default experience").** Full-mesh
  RTCPeerConnections between participants; the server only relays opaque
  SDP/ICE (`signal` messages). The client uses the MDN perfect-negotiation
  pattern (newcomer = polite peer) and creates audio+video transceivers
  up-front so mute/device-switch/screen-share are all `replaceTrack` — no
  renegotiation. Screen shares request system/tab audio
  (`getDisplayMedia({audio: true})`); when the browser grants it, the capture
  is mixed with the mic via WebAudio (`state.shareMix`) into the existing
  audio sender — still no renegotiation, mic mute keeps working
  (`enabled=false` contributes silence to the mix). Google STUN only, no TURN.
- **`volc` — VolcEngine RTC ("Better performance in China").** Media flows
  through the VolcEngine Web SDK (vendored at
  `static/vendor/volc-rtc-4.68.5.min.js`, UMD global `VERTC`); the server
  mints per-participant **AccessTokens** (`internal/meet/volctoken.go`,
  ported from the reference implementation in volcengine/VolcEngineRTC —
  little-endian packing, HMAC-SHA256, golden-tested in `volctoken_test.go`).
  Credentials come from `VOLC_RTC_APP_ID` / `VOLC_RTC_APP_KEY` (required at
  runtime — no defaults are committed; the app key is an HMAC secret). The
  `volc` backend can't mint join tokens when they're unset, but `mesh` is
  unaffected. VolcEngine user ids are `p<pid>` so streams map back to roster
  entries. Screen shares capture with `startScreenCapture({enableAudio: true})`
  and publish `AUDIO_AND_VIDEO` (falling back to `VIDEO`). Gotcha:
  `isAutoSubscribeVideo` covers only main (camera/mic) streams — remote screen
  streams must be explicitly `subscribeScreen`d in `onUserPublishScreen`, or
  viewers get a black tile.

Either way, every participant stays on the meet server's `/ws` **control
plane**: it owns the roster, AV state fan-out (`peer_state`), and host powers.
Protocol: client sends `join {code, guest_name?, client_id?}` / `leave` /
`signal {to, payload}` / `state {muted, cam_on, sharing}` / `host_mute {pid}` /
`host_transfer {pid}` / `host_kick {pid}` / `set_guests {allowed}` / `ping`;
server sends `hello` (`me` is `null` for anonymous connections) / `joined`
(roster + `host_pid` + `allow_guests` + `volc` join payload when applicable) /
`peer_joined` / `peer_left` / `signal {from}` / `peer_state` / `host_changed` /
`force_mute` / `kicked` / `replaced` / `guests_changed {allowed}` / `error` /
`pong`.

Rooms (`internal/meet/state.go`) are in-memory only, keyed by
`xxx-xxxx-xxx` codes (no i/l/o). The first joiner is host; when the host
leaves, the longest-present participant inherits (`host_changed`). Empty rooms
survive a 5-minute grace (refresh-proof) and never-joined rooms an hour, then
are pruned lazily. **One slot per identity**: a `join` whose registered user id
(or per-tab `client_id`) already occupies the room evicts the prior instance —
the displaced socket gets `replaced` and the rejoiner reclaims host — so an
abnormal disconnect + rejoin can't leave a ghost (or a stuck host) behind. A
**heartbeat** (server read deadline `pongWait`=75s, refreshed per frame; the
client pings every 20s) reclaims a silently-dead socket even without a rejoin;
`conn.send` carries a `writeWait` deadline so a half-open socket can't wedge a
fan-out. The client treats a dropped control socket as recoverable: it keeps
local capture and retries `connectWS`+`join` for ~12s behind a "Reconnecting…"
banner (mesh rebuilds peers, volc rejoins the room with the fresh token) before
falling back to a "Connection lost" screen.
**Guest access**: each room has a host-toggled `allowGuests` flag (the switch
lives in the People panel; `set_guests` over WS). When on, non-registered
visitors get the meeting page instead of the login redirect and join by just
entering a name; guests are synthetic `meetUser`s with a negative id,
`username "guest"`, and `guest: true`. The host can also kick anyone
(`host_kick` → `kicked` to the target, who sees a "removed" screen).

Routes: `GET /` (lobby) and `GET /m/:code` (meeting page; anonymous users are
redirected through `/login?next=…` unless the room allows guests; a request
whose `am_session` cookie no longer resolves to a session counts as anonymous,
and `GET /`, `GET /m/:code`, `GET /login`, and every route that needs a
signed-in user clear that cookie, so a browser whose session was revoked opens
a guest-enabled meeting at the guest gate),
`POST /api/meetings {mode}` → `{code}`, `GET /api/meetings/:code` →
`{mode, participants, allow_guests}` (deliberately public so the page can pick
gate vs. login before auth), plus login/logout/me and the debug-console
ingest `POST /api/debug/report`. The meet server mounts
`/static`, `/fonts`, `/sound`, and `/avatars` (so meeting tiles can show
profile photos; `meetUser` = `{id, username, display_name, avatar, guest?}`,
still no bio). Alex Meet does not write meeting history (the `calls` table logs
DM calls — see **DM calls**).

Frontend (`meet.html`, single file): lobby → pre-join gate → meeting. The gate
shows a live selfie preview plus microphone/speaker/camera dropdowns (and the
name field for guests); in mesh mode the preview stream is adopted as the call
media on join, in volc mode it's held through the join handshake and released
only just before the SDK opens the same devices (so the camera isn't toggled
off mid-join and the already-granted permission isn't re-prompted). Device
constraints fall back from `{deviceId: exact}` to the system default if a
selected device has vanished, and mic capture requests auto-gain so quiet
sources stay audible. Meeting UI: the **grid "group view" is the default** (paged at 9
tiles; `bestGridSize` picks the row/column split that maximizes 16:9 tile area
for the current window ratio, so wide windows lean on columns and portrait
phones stack one column; relaid out on resize). Clicking a tile switches to
the focus view (clicked tile big, the rest in a 16:9 stack); the stack's
placement follows the **window ratio**, not its width — landscape windows
(including a sideways phone) keep it in a scrollable right column so the main
tile isn't squeezed, portrait windows drop it below (`meet-body.stack-bottom`,
toggled from `layout()`). Clicking the main view returns to the grid. A
starting remote screen share auto-focuses the sharer and falls back to the grid
when it ends. The focused tile carries two overlay controls (Lucide icons, only
shown on the tile in `.main-view`): **top-left fullscreen** (Fullscreen API on
the tile element) and **bottom-left fit/fill** — video **scale-to-fill (cover)
is the default**, the toggle restores letterboxing (`fit`), and the choice is
persisted (`meet_fill`) and inherited in fullscreen. Mesh drives it with
`object-fit` on `.main-view .tile video` (`.main-view.fit` → contain); volc
re-applies the focused camera's `renderMode` (`RENDER_MODE_HIDDEN` vs `_FIT`).
A shared screen is never cropped (stays `contain`/`FIT`). The active speaker's
tile gets a light-green
stroke — mesh meters tracks with WebAudio analysers (RMS threshold + 700 ms
hold so background noise doesn't flicker it), volc uses
`enableAudioPropertiesReport`/`linearVolume`. Bottom bar (stacks vertically
and centers on narrow screens): mic split button (chevron menu carries an
**output-volume slider** (0–200%, persisted `meet_volume`; volc amplifies via
`setPlaybackVolume`, mesh caps at the element's 1.0 and leans on mic auto-gain)
plus the microphone *and* speaker device lists — output switching is `setSinkId`
on the remote tile videos / `setAudioPlaybackDevice` on volc), camera split
button, screen share,
**streaming-quality menu** (Auto/Low 360p/Standard 720p/High 1080p/Premium 4K;
per-user, applied to *their* outgoing stream: capture constraints + per-sender
bitrate caps in mesh; `setVideoEncoderConfig` + `setScreenEncoderConfig` +
audio profile in volc — `maxKbps` is mandatory there, the SDK rejects configs
without it and keeps its 640×480/600 Kbps default, so even "Auto" passes an
explicit 720p/2000 Kbps profile), grid toggle, and the red leave pill.
Note: volc media quality is bounded by the network path to VolcEngine's relay,
not by these configs — behind a UDP-blocking proxy/VPN the SDK falls back to
ICE-TCP through the tunnel (~260 ms RTT) and congestion control caps the send
rate around 0.5–1 Mbps regardless of the tier, so 4K will look blocky. Direct
UDP to a nearby volc edge is required for the high tiers to mean anything. Top-left copy-link
button + code chip; top-right people panel with host mute / make-host / kick
actions and the allow-guests switch. All icons are embedded Lucide SVGs. Tiles
never get destroyed on layout changes — they move between main/stack/grid
containers and an off-screen "park" so media keeps playing; in mesh mode remote
audio plays through each remote's own tile `<video>` element (one media clock ⇒
lip-sync — there is **no** separate audio pool; only the self tile is muted).
Gotcha: WebKit pauses a `<video>` whose element is re-inserted
in the DOM (Chrome doesn't), so tile moves go through `placeTile`/`placeTiles`
— no-op when already in position, `moveBefore()` where supported, otherwise
`insertBefore` + `play()` resume (plus a post-layout sweep and a pause
listener on mesh tile videos).

## Debug console (admin panel)

A centralized, real-time debug console lives in the **admin panel**. Because the
three binaries are separate processes that share only the SQLite file, the
shared `debug_events` table (a capped ring buffer; `db/debug.go`) is the channel
between them:

- **Ingestion** — both client-facing servers expose `POST /api/debug/report`
  (built from `debuglog.ReportHandler`). The clients batch real-time actions
  (`Debug.info/warn/error/debug(event, message, ctx)` in `static/app.js`,
  `meet.html`, and `shared/src/debug.ts` for the TS clients) and stream them
  there; server code records its own status with `debuglog.Emit(app, level,
  event, message, ctx)` (e.g. ws connect/disconnect, meeting create/join/
  leave). The actor's identity is resolved server-side from the session —
  never trusted from the body.
- **Abuse prevention** — per-IP token bucket, 64 KB body cap, 50-event batch
  cap, per-field length limits, level whitelist. Over-budget events are dropped
  but the endpoint always returns `200 {"ok":true}`.
- **Reading** — the admin panel (`adminapp/debug.go`) serves `GET /api/debug/events`
  (filtered snapshot), `GET /api/debug/stream` (SSE live tail; polls the table
  on a 1s tick since the writers are other processes), and `POST /api/debug/clear`.
  The admin UI filters by **app, level, user, session, and free-text** search.

## Run / develop

Requires Go 1.25+. Run each binary **from the repo root** (paths to HTML,
`static/`, `fonts/`, `sound/`, `web/dist`, and `data/` are resolved relative to
the working directory).

```bash
# Main app (port 80 in prod needs sudo; 8765 locally)
ADMIN_PASSWORD=changeme PORT=8765 go run ./cmd/server
ADMIN_PASSWORD=changeme PORT=80   go run ./cmd/server   # prod

# Admin panel (default 127.0.0.1:8001)
ADMIN_PASSWORD=changeme go run ./cmd/admin

# Alex Meet (default 127.0.0.1:8002)
ADMIN_PASSWORD=changeme go run ./cmd/meet
```

Build standalone binaries with `go build -o bin/server ./cmd/server` (etc.).

Environment variables (same semantics as the Python version):
- `ADMIN_USERNAME` (default `admin`) / `ADMIN_PASSWORD` — seed admin. If
  `ADMIN_PASSWORD` is unset on first run, a random one is generated and printed
  to stderr once. All three processes call `auth.BootstrapAdmin()`; the
  first-run insert race is caught and made idempotent.
- `VAPID_SUBJECT` (default `mailto:admin@alexanderjia.com`) — Web Push contact URI.
- `HOST` / `PORT` (user app), `ADMIN_HOST` / `ADMIN_PORT`, `CALL_HOST` / `CALL_PORT`.
- `VOLC_RTC_APP_ID` / `VOLC_RTC_APP_KEY` — VolcEngine RTC credentials for Alex
  Meet's `volc` backend. Required at runtime (no defaults are committed — the
  app key is an HMAC signing secret); read in `internal/meet/routes.go`.
- `WS_ALLOWED_ORIGINS` — comma-separated exact origins (for example
  `https://app.example.com`) that may open a WebSocket in addition to
  same-origin connections. A connection counts as same-origin when its
  `Origin` has the request's `Host` and the request's scheme; the server
  takes the scheme to be `https` only for a TLS connection to the Go server
  itself or a request forwarded from loopback with `X-Forwarded-Proto: https`.
  The variable is therefore needed only behind a proxy or dev server that
  rewrites the `Host` header, or behind a TLS-terminating proxy that does not
  connect from loopback with `X-Forwarded-Proto: https`
  (`web/vite.config.ts` keeps `Host`, so `npm run dev` on :5173 needs no
  value). In those setups, also list the origin of the server URL the mobile
  apps use (for example `https://chat.example.com`): React Native's WebSocket
  sends that origin as `Origin` on iOS and Android. Otherwise leave it unset.
- `STUN_URLS`, `TURN_URLS`, `TURN_SECRET`, `TURN_USERNAME`, `TURN_CREDENTIAL`
  — ICE servers for DM calls (`internal/webapp/ice.go`; see **DM calls** and
  `DEPLOY.md`). Unset = Google's public STUN and no TURN.
- `OPENROUTER_API_KEY` — required for `POST /api/transcribe` to reach a real
  model (absent in tests). `OPENROUTER_TRANSCRIBE_MODEL` and
  `OPENROUTER_BASE_URL` override the model and endpoint.
- `OTA_DIR` — the OTA update store (default `data/ota`); read by
  `cmd/server` and `cmd/admin` (the census), so set both to the same value.
  Leave it unset on the Pi: `bin/otactl` over SSH never reads the units'
  environment file and finds the store next to its own executable.
  `OTA_DISABLED=1` — the OTA kill switch: `/api/ota/manifest` answers 204 to
  every request (see **OTA updates**).
- `AM_RELEASES_DIR` — `otapublish`'s release archive on the Mac (default
  `~/AlexMessagesReleases`; `--archive` overrides both).

`go vet ./...` and `go build ./...` should both stay clean.

Note: `getUserMedia`/screen capture require a secure context — `localhost` is
fine for development, but production Alex Meet (and web dictation capture)
must be served over HTTPS.

### TypeScript toolchain

```bash
# shared core — typecheck + unit tests
cd shared && npm install && npm run typecheck && npm test

# web client — dev server (proxies to :8765), build, tests
cd web && npm install && npm run dev          # vite on :5173
cd web && npm run build                       # tsc --noEmit && vite build → web/dist
cd web && npm run test:e2e                    # playwright (spins its own server on :8795)

# mobile client — dev server, native builds, typecheck
cd mobile && npm install && npm start                       # Metro / Expo dev
cd mobile && npm run ios                                    # build + boot iOS sim
cd mobile && npm run android                                # build + boot Android emulator
cd mobile && npx tsc --noEmit                               # typecheck
cd mobile && npx maestro test e2e/                          # E2E on a booted device
```

## Testing

- **Go unit tests** — live next to the code (`internal/db/db_test.go`,
  `internal/db/debug_test.go`, `internal/db/calls_test.go`,
  `internal/db/review_test.go`, `internal/httpx/httpx_test.go`,
  `internal/push/push_test.go`, `internal/adminapp/review_test.go`,
  `internal/webapp/linkpreview_test.go`, `internal/webapp/uploads_test.go`,
  `internal/webapp/transcribe_test.go`, `internal/webapp/calls_test.go`,
  `internal/webapp/call_listener_test.go`, `internal/webapp/review_test.go`,
  `internal/meet/volctoken_test.go`, `internal/meet/routes_test.go`,
  `internal/debuglog/debuglog_test.go`, the OTA tests
  `internal/ota/{sfv,verify,store,manifest,client,update}_test.go`,
  `internal/ota/census/census_test.go`, `internal/ota/sign/sign_test.go`,
  `internal/webapp/ota_routes_test.go`, `internal/db/session_client_test.go`,
  `internal/adminapp/ota_test.go`, `cmd/otactl/{otactl,gate}_test.go`,
  `cmd/otapublish/*_test.go`): DB layer (DM state, read receipts,
  attachment dims, paging, debug filters/prune), link-preview SSRF guard,
  image-dimension extraction, transcribe format/model mapping + upstream
  handling, VolcEngine token wire format, debug ingest rate-limiter; the DM
  call state machine and signaling (glare, resume grace, page unload, account
  deletion, call Web Push payloads and their order) and the call listener
  socket; a call logged after a participant was deleted; a stalled push
  service timing out; and the security-review regressions: upload
  authorization, active-content handling, directory and traversal requests,
  the multipart size cap, Ogg served inline as `audio/ogg`; Secure session
  cookies and WS origin checks (also behind a TLS proxy); bearer password
  change (this device's token kept, every other session dropped) and bearer
  logout (every session and every push subscription dropped); an idle socket
  closed after its session is revoked; the `/ws` ping heartbeat; an `init`
  whose contacts, DM-state, thread-history, or unread-count read fails
  closing with 1011 and leaving no presence; a message that fails to
  save never broadcast; debug reports attributed to the bearer user; atomic
  last-admin protection (a disabled admin can be deleted); admin font and
  icon assets and no directory listings; same-second paging and read
  cutoffs; concurrent DM-state upserts; WAL and the message sequence
  surviving a purge or a migration; `BEGIN IMMEDIATE` waiting out another
  writer; orphan upload cleanup; a revoked cookie reaching Alex Meet's guest
  gate. OTA: SFV parsing; the certificate-chain rules of expo-updates; the
  store formats and atomic writes; every manifest answer (each 204 path,
  `OTA_DISABLED`, the directive, 400s, the exact multipart bytes and a
  signature that verifies against the root); asset and download headers,
  Range and HEAD; census limits; the `X-AM-Client` record and both gates
  (REST 426, WS 4426, `client_policy` in `init`); census rows covered by
  account deletion and export; the session-column migration; the import
  guard that keeps `internal/ota/sign` and age out of the binaries deployed
  to the Pi, and the private-key guard. The OTA tools: every `otactl`
  command against a temporary store (ingest verification, pointer
  switching, emergency, the promote gate on a temporary database), and every
  `otapublish` command against a fake Pi (a temporary directory; fake `ssh`,
  `gh`, `npm`, `npx`, `gradlew`, `pod`, `xcodebuild`, `zipalign` and
  `apksigner` scripts; the real `otactl`; the real webapp engine over
  `httptest`; throwaway keys and injected passphrases), and the APK and IPA
  readers against real `aapt2`, `apksigner` and `plutil` output
  (`cmd/otapublish/testdata/native`).
  `go test ./...` — DB tests point the package-level path vars at a temp
  dir.
- **deploy** — `python3 deploy/remote_deploy_test.py` runs
  `deploy/remote_deploy.sh` against scratch directories with fake `sudo`,
  `curl` and `sleep`: the React build is replaced whole, a failed health
  check or a missing React build restores the old release, `data/` is
  byte-identical after a deploy and after a rollback, a release that
  contains `data/` is refused, and `bin/otactl` is installed by rename.
- **shared** — `cd shared && npm test` (Vitest, 92 tests in 6 files): store
  reducers (optimistic send → delivered → read, edits, replies, unread),
  socket reconnect/queue, api error mapping, format helpers, WAV encoding,
  and the call engine (`call.test.ts`, against a fake `CallPlatform`).
- **web** — `cd web && npm run test:e2e` (Playwright, 49 tests across 8
  specs): `start-server.sh` builds an isolated `cmd/server` on :8795 against
  its own SQLite database in `$E2E_RUN_DIR` (default `/tmp/am-e2e`), seeded by
  `web/e2e/seed`, so the suite never touches dev data. That database persists
  between runs: the seed adds each e2e user only when missing, adds the two
  seed messages only when their thread is empty, and resets the seeded
  threads' pin and read state.
  Chromium runs with fake mic/camera flags so the dictation tests exercise the
  real WebAudio capture. Two tests in `calls.spec.ts` launch a browser of
  their own: one with two fake cameras, and one on full Chromium (channel
  `chromium`), because the default headless shell denies notifications. Both
  come from `npx playwright install chromium`.
- **mobile** — `cd mobile && npx maestro test e2e/` against a booted simulator/
  emulator (8 flows). See **Mobile dev notes** below — the emulator setup
  materially affects reliability.

For manual end-to-end checks, run the binaries and exercise the flow by hand:
register → admin approve → login → DM (over `/ws`) → edit → upload → contacts
→ logout → admin delete. For Alex Meet: lobby → new meeting (each backend) →
second participant via the link → mute/camera/screen share → host mute +
transfer → leave. The user app and admin panel must both be running (they
share `data/alexmessage.db`); the meet server is independent. A fresh `data/`
is created on first run.

## Mobile dev notes

Real-world gotchas hit while bringing the RN app up on simulators/emulators:

- **`JAVA_HOME` for Maestro** — the system `JAVA_HOME` may point at a JDK that
  doesn't exist. Use Android Studio's bundled JBR:
  `export JAVA_HOME="/Applications/Android Studio.app/Contents/jbr/Contents/Home"`.
- **Android emulator → host server** — `localhost` inside an emulator is the
  emulator itself. The session layer remaps `localhost` dev URLs to
  `10.0.2.2` automatically, but keep it in mind for any new networking.
- **Target a specific device** — `npx maestro test --udid emulator-5556 …`
  when more than one device is booted.
- **Emulator animations must stay ON** — the "Reduce animations" / zeroed
  `animator_duration_scale` settings trigger a Reanimated dev-mode LogBox
  toast that overlays the composer and eats taps (and would annoy users).
  Re-enable via `adb shell settings put global window_animation_scale 1` +
  `transition_animation_scale 1` + `animator_duration_scale 1`.
- **Soft keyboard** — AVDs ship `hw.keyboard=yes` + `show_ime_with_hard_keyboard=0`,
  which suppresses the IME. `adb shell settings put secure show_ime_with_hard_keyboard 1`
  makes typing/focus behave like a real phone.
- **`hideKeyboard` on Android ≈ back-nav** — when no keyboard is open, Maestro's
  `hideKeyboard` pops the route. Gate it behind `visible: "return"` (the iOS
  key) in flows.
- **Long-press timing** — React Native's default `delayLongPress` (500 ms) is
  what Maestro's injected taps expect; don't shorten it.
- **Safe areas on Android are real** — modal screens must use
  `SafeAreaView edges={["top","bottom"]}`; without it the header renders under
  the status bar (edge-to-edge) and the close control is unreachable.
- **Uploads and Expo's fetch** — Expo SDK 57 replaces the global `fetch` with
  `expo/fetch`, which can't send React Native `{uri, name, type}` FormData
  parts (every upload failed instantly). `ApiClient` takes a `readFile` hook
  (mobile passes `expo-file-system`'s `File#bytes`) and attaches `bytes()` to
  native parts, which `expo/fetch` accepts. Keep it when adding upload calls.
- **Menus are native on iOS** — `src/components/NativeMenu.tsx` renders the
  UIKit views of the local Expo module `mobile/modules/native-menu`: a
  `UIContextMenuInteraction` on the React Native view for iMessage-style
  long-press menus (every iOS), and a `.glass()` `UIButton` whose menu morphs
  out of it for the composer "+" (iOS 26). `TapMenu` stays an `@expo/ui`
  SwiftUI `Menu`. Android (and the "+" on older iOS) fall back to the shared
  bottom-sheet `ActionSheet` (Android `Alert.alert` caps at 3 buttons). The
  exception is the DM header's call button on Android: `GlassMenuButton`
  with `dropdown` opens a Jetpack Compose `DropdownMenu` anchored to it
  (`@expo/ui/jetpack-compose` through `src/components/JetpackCompose.tsx`,
  which is a stub on iOS and web), with leading icons from
  `mobile/assets/icons/*.xml` (Android vector drawables). Don't
  host list rows or bubbles in SwiftUI — per-row hosting views re-render on
  every scroll frame (see `mobile/AGENTS.md`).
- **Keyboard on iOS** — `react-native-keyboard-controller` (provider mounted
  on iOS only) moves the composer and message list with the keyboard (final
  height set in the keyboard's own animation block; drags follow the finger).
  A downward drag on the list or the composer dismisses the keyboard
  interactively, as in Messages. Back waits for the keyboard to start hiding
  before it pops (see `mobile/AGENTS.md`). Android keeps `KeyboardAvoidingView`.
- **Message list** — an inverted FlatList; iOS 26 scroll-edge effects are
  hidden per scroll view with `NoScrollEdgeEffects`
  (`src/components/ScrollEdge.tsx`), see `mobile/AGENTS.md`.
- **Status bar** — each Stack screen sets `statusBarStyle`
  (`app/_layout.tsx`). Don't add expo-status-bar's `<StatusBar>`: Info.plist
  turns on view-controller-based status bar appearance (the image viewer
  hides the status bar), and React Native's StatusBar module throws under it
  (a red box on iOS).
- **`@expo/ui` stays linked on Android** — expo-router's Android code imports
  it; excluding it from Android autolinking crashed the app at launch
  ("Cannot find native module 'ExpoUI'").
- **Testing calls** — calls in the iOS Simulator ring in the app's own call
  screen, never in CallKit or LiveCommunicationKit (see **iOS: the system
  call UI**); test those on a real iPhone. To get LiveCommunicationKit
  outside China, launch the app with the argument `-AppleLocale en_CN` (in
  Xcode: the scheme's Arguments Passed On Launch). The iOS Simulator has no
  camera: its self view stays black and the peer gets no video from it; the
  Android emulator's virtual camera works. In a call the Simulator sends silence: the peer receives
  the audio packets, but every sample is zero. Safari's `getUserMedia` in
  the same Simulator captures only silence too, while an expo-audio
  recording there captures sound (seen on the iOS 27 Simulator). A VPN app
  on the Mac that takes the default route (seen with Quantumult X) stops the
  Simulator's call media: its WebRTC offers only the tunnel's address, the
  tunnel drops that UDP, and its TCP to a TURN server on the LAN never
  connected. Calls then ring and are answered, and end as "Couldn't
  connect". The Android emulator's calls connected with the same VPN on.
  With the VPN off, Simulator calls connect, and the Simulator receives the
  peer's video and audio. Check that an iPhone's own voice reaches the peer
  on a real iPhone.
- **Testing the Android ring service** — rebuild the app (`npm run android`)
  after any change under `modules/call-ringer`: Fast Refresh pushes the new
  JS into the old native build, which then fails with "undefined is not a
  function" at the new native function. Emulator commands that were used:
  kill the app's process as Android would
  (`adb shell run-as com.alexanderjia.messages kill -9 <pid>`; the service
  is back about 1 s later); force deep Doze (`adb shell dumpsys battery
  unplug`, then `adb shell dumpsys deviceidle force-idle`; undo with
  `adb shell dumpsys deviceidle unforce` and `adb shell dumpsys battery
  reset`); list the app's notifications (`adb shell dumpsys notification
  --noredact | grep alexanderjia`: id 7101 is "Ready for calls", id 7102 is
  a ringing call with tag = the call id, id 0 is a conversation's slot with
  tag = the channel); and check that the service runs (`adb shell dumpsys
  activity services com.alexanderjia.messages`). After a reboot, Android
  delivered `BOOT_COMPLETED` to the app about 40 s after boot. The default
  AVD has no screen lock, so the call screen over a locked keyguard was not
  exercised there.

## Architecture notes specific to the Go port

### Concurrency

The Python servers ran on a single-threaded asyncio event loop, so shared state
needed no locks. Go has real concurrency (one goroutine per WebSocket read loop,
plus broadcast goroutines), so:

- **Per-connection write mutex.** Gorilla WebSocket connections are not safe for
  concurrent writes. `runtime.Client` (main app) and the meet server's `conn`
  each carry a `sync.Mutex` that serializes every send, so two fan-outs never
  interleave a frame on the same socket.
- **Presence is mutex-guarded.** `runtime.Presence` protects its maps with its
  own mutex.
- **Alex Meet state** uses a single mutex (`meetMu`) guarding the room
  registry and all room/participant fields; handlers mutate under the lock,
  snapshot recipients, then send after releasing it.

### Database

`database/sql` + `modernc.org/sqlite` (pure Go, no cgo). The connection pool with
per-statement implicit transactions reproduces the Python "connection-per-call in
autocommit mode" pattern. The DSN sets `foreign_keys`, `busy_timeout` (5 s),
and `journal_mode(WAL)` on every pooled connection, plus `_txlock=immediate`:
every explicit transaction (`pool.Begin()`) starts with `BEGIN IMMEDIATE`,
which takes the write lock up front and waits out `busy_timeout` while
another process writes. (A deferred `BEGIN` fails at once with "database is
locked" when it upgrades from read to write, without waiting.) In WAL mode
SQLite keeps `data/alexmessage.db-wal` and `data/alexmessage.db-shm` next to
the database file.

### JSON wire shapes

Responses are byte-compatible with FastAPI where it matters — and the TS
clients mirror them exactly (`shared/src/types.ts`):

- Errors are `{"detail": "<message>"}` with the original status code (the
  clients read `detail`).
- The live WS `message` payload includes an `author` object; the history/`init`
  message shape (`db.HistoryMessage`) deliberately omits it — matching the two
  distinct Python dict shapes. Nullable fields (`user_id`, `author`, `reply_to`,
  `edited_at`) serialize as JSON `null`, not omitted.
- The three different public-user views are preserved: `runtime.PublicUser`
  (`{id, username, display_name, bio, avatar}`), `db.UserToPublic` (admin: adds
  `status`, `is_admin`, `created_at`), and the meet server's `meetUser`
  (`{id, username, display_name, avatar}`, no bio).

### Web Push / VAPID

`internal/push` keeps the on-disk format identical to Python: the private key is a
PKCS8 PEM at `data/vapid_private.pem` and the public key is mirrored as base64url
in `data/vapid_public.txt`. On startup it loads the PEM and re-derives the public
key so the served key can't drift. Encryption/signing is delegated to
[webpush-go](https://github.com/SherClockHolmes/webpush-go) (the analogue of
`pywebpush`), which wants the keys as base64url; `push.deriveKeys` converts the
EC key into those forms. Subscriptions returning 404/410 are pruned automatically.
Each send gives up after 30 s (`pushHTTPClient`). A push service that never
answers therefore delays the pushes queued behind it (the same user's next
subscription, and the same call's next push) by at most 30 s per stalled
send, instead of blocking them forever.

### WebSocket auth (close 4401)

For both `/ws` endpoints, the credential is read before the upgrade — the
`am_session` cookie, `Authorization: Bearer <token>`, or `?token=` — but the
handshake is completed regardless so the server can send `close(4401)`; the
clients listen for code 4401 to route to `/login`. A connection that sends an
`Origin` header must be same-origin (the origin's host and scheme match the
request's) or match an exact origin from comma-separated `WS_ALLOWED_ORIGINS`
(`httpx.WebSocketOrigin`); a connection without an `Origin` header is
accepted. The React Native app's sockets send an `Origin` built from the
server URL (React Native's WebSocket adds it on iOS and on Android), so they
pass as same-origin when the request reaches the server with the server URL's
host and scheme: directly, or through a reverse proxy that keeps the `Host`
header and, for an https server URL, connects from loopback with
`X-Forwarded-Proto: https` (see `DEPLOY.md`). Only the Android ring service's
call listener (OkHttp) sends no `Origin`. Main-app sockets and registered Meet sockets recheck the
session on every frame and every 5 seconds (the call listener every 5
seconds), and close with 4401 once it is revoked, idle sockets included. The
main-app `/ws` and the Meet `/ws` cap a frame at 128 KB (the call listener at
4 KB). The main-app `/ws` puts a 5-second
deadline on each JSON frame it writes and sends a WebSocket ping control
frame every 25 s (`wsPingInterval`; a ping write may take up to 10 s,
`wsWriteWait`, because it waits for a frame already being written); every data frame or pong from the client refreshes a
75-second read deadline (`wsReadTimeout`), so a socket that sends neither for
75 s is dropped. Browsers and React Native answer pings by themselves, so the
legacy `static/app.js`, which sends no keepalive, stays connected. If any
database read behind `init` fails (contacts, users, DM state, thread
partners, thread history, unread counts), the server closes the socket with
1011 (`init failed`) instead of sending a partial `init`, and the client
reconnects. HTTPS cookies carry Secure, including requests forwarded by
loopback nginx with `X-Forwarded-Proto: https`; direct localhost HTTP remains
usable. Before the 4401 check, the main-app `/ws` and the call listener close
with **4426** a mobile build that the OTA hard gate refuses (its
`X-AM-Client` build is below `min_build` while `hard_gate` is on; see **OTA
updates**). The gate is off by default, and a socket without `X-AM-Client`
is never gated.

### Identity, DMs, per-user DM state, lazy history

A DM thread between users *a* and *b* lives at synthetic channel `dm:<min>:<max>`
(`db.DMChannelID` / `db.ParseDMChannel`); these are the only channels that exist.
`dm_state` rows carry `pinned` / `last_read_at` / `force_unread` / `cleared_at`
per `(user_id, channel)`. Each thread ships only the most recent 50 messages on
connect with a `history_has_more` flag; older messages load via
`GET /api/history/{channel}?before_id=<oldest-message-id>`. The legacy
`before=<seconds>` cursor remains accepted for old clients. Messages carry a
monotonic `position` separate from their display timestamp; DM state stores
read/clear positions, and read receipts include `last_read_position`. The
persisted `client_id` prevents duplicate messages on Retry. The DB schema
migrates added columns (`users.avatar`, `attachments.width/height`,
`dm_state.force_unread`, `dm_state.last_read_position`,
`dm_state.cleared_position`, `messages.edited_at`, `messages.position`,
`messages.client_id`, `calls.channel`, `calls.media`, `calls.message_id`,
`sessions.client`, `sessions.client_seen_at`) on
startup via `ensureColumn`, tolerant of the three binaries racing the same
ALTER. A one-time migration, recorded as `message_positions_v1` in the
`schema_migrations` table, back-fills `messages.position` and the DM
read/clear positions from the old timestamps. The one-row `message_clock`
table hands out positions across the three processes, and the
`messages_legacy_position` trigger gives a position to a message that an
older binary (after a rollback) inserts without one.

## Data / runtime state

Lives under `data/` (gitignored), created on first run:
- `data/alexmessage.db` — SQLite (users, sessions, messages, attachments, contacts,
  calls, push_subscriptions, dm_state, message_clock, schema_migrations,
  debug_events). WAL mode keeps `data/alexmessage.db-wal` and
  `data/alexmessage.db-shm` next to it; the `-wal` file can hold committed
  writes, so a copy of the database must include it
  (`deploy/remote_deploy.sh` copies the `-wal` and `-shm` files, when they
  exist, with the database while the binaries are stopped).
- `data/uploads/<user_id>/<token>_<filename>` — per-user uploads; account deletion
  removes the directory. `GET`/`HEAD /uploads/<user_id>/<file>` needs a
  signed-in user (401 without one) who is the uploader or a participant of
  the DM whose message references that exact file (404 for anyone else).
  Only sniffed inert media is served inline (PNG, JPEG, GIF, WebP, MP4, WebM,
  MP3, WAV, and Ogg as `audio/ogg`); every other file is served as an
  `application/octet-stream` attachment download. Every response carries
  `nosniff` and a `sandbox` CSP. The maintenance sweep deletes upload files
  older than 24 hours that no attachment references, so an unsent draft
  survives for 24 hours.
- `data/avatars/<user_id>_<token>.<ext>` — profile photos; replaced on change,
  removed on account deletion.
- `data/vapid_private.pem` / `data/vapid_public.txt` — VAPID key pair.
- `data/ota/` — the OTA update store (`OTA_DIR` overrides it), including the
  install census `data/ota/census.db`. It does not exist until `bin/otactl
  init` creates it (on the Pi: release day, Phase 6 of `OTAUpdatePlan.md`);
  without it the manifest endpoint answers 204. Deploys never write into
  `data/`.

Alex Meet rooms are in-memory only — a meet-server restart ends all meetings.

## Endpoints

See the route files under `internal/webapp/` (user app),
`internal/adminapp/` (admin; `adminapp.go` + `debug.go`), and
`internal/meet/routes.go` (Alex Meet). The main-app WebSocket protocol: client
sends `message` (with an optional `client_id` optimistic-send nonce)/`edit`/
`switch`/`open_dm`/`visibility {visible}` (whether this device's screen shows
the app; a ringing call also goes out as Web Push when no `/ws` socket of the
callee is visible, see **DM calls**)/`ping`; server sends `init`/`message`
(echoes `client_id` back to the sender)/`message_edited`/`presence`/
`profile_update`/`dm_opened`/`dm_read`/`pong`. DM calls add `call_start`/`call_accept`/`call_hangup`/
`call_signal`/`call_resume` (client) and `call_outgoing`/`call_incoming`/
`call_ringing`/`call_accepted`/`call_signal`/`call_resumed`/`call_ended`/
`call_error` (server) — see **DM calls**. `/ws?mode=calls` is the call
listener socket (ringing only; see **DM calls**). A mobile build from build 2
on adds `client=<X-AM-Client value>` to the `/ws` upgrade; its `init` then
carries `client_policy`, and the OTA hard gate closes it with 4426 (see **OTA
updates**). The Alex Meet control-plane protocol is documented above.

OTA routes (no login): `GET /api/ota/manifest`, `GET`/`HEAD /ota/assets/…`,
`GET /api/app/latest`, `GET /app`, `GET`/`HEAD /app/<android|ios>/…`
(`internal/webapp/ota_routes.go`); admin: `GET /api/ota/installs`.

Notable additions for the TS clients: `POST /api/transcribe` (dictation proxy
to OpenRouter) and the `session_token` field on `POST /api/login` (bearer auth
for native).

Debug console (admin): `GET /api/debug/events` (filtered snapshot),
`GET /api/debug/stream` (SSE live tail), `POST /api/debug/clear`. Ingestion:
`POST /api/debug/report` on both the user app and the meet server. See the
**Debug console** section above.

See also `DEPLOY.md` (build/run/deploy for humans and agents) and
`Investigation.md` (the problem/threat audit from the FreshRefactor pass).
