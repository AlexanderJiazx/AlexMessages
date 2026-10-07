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
  (`GET /api/me/export`). Peer profiles use a separate read-only sheet.
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
  REST calls and the `/ws` upgrade with `Authorization: Bearer <token>` /
  `?token=`.
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
and drains in-flight requests on SIGINT/SIGTERM. Each `main` also calls
`db.StartBackgroundMaintenance()`, a 10-minute sweep that prunes expired
sessions and caps the debug ring buffer.

## Repository layout

```
cmd/
  server/main.go   entrypoint: bootstrap + serve user app
  admin/main.go    entrypoint: bootstrap + serve admin panel
  meet/main.go     entrypoint: bootstrap + serve Alex Meet
internal/
  db/        SQLite schema + every query helper; debug.go (ring buffer) + maintenance.go
  auth/      scrypt hashing, session tokens, admin bootstrap
  push/      VAPID bootstrap + Web Push delivery
  runtime/   main-app shared state: presence, visibility, broadcasts
  httpx/     tiny shared HTTP helpers: {"detail": …} errors, cookies, graceful Serve
  debuglog/  debug-console ingestion: rate-limited /api/debug/report + server-side Emit
  webapp/    the user app: engine + one file per route module
  adminapp/  the admin panel + adminapp/debug.go (debug-console read API)
  meet/      Alex Meet (state.go, ws.go, routes.go, volctoken.go)
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
`transcribe.go` (voice dictation proxy), plus `avatar.go` (profile photos) and
`linkpreview.go` (link previews).

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

Run its tests with `cd shared && npm test` (Vitest). `npm run typecheck` =
`tsc --noEmit`.

### `web/` — React web client (`@am/web`)

Vite + React 18 + TypeScript SPA. Structure:

- `src/client.ts` — app singletons: one `ApiClient`, one `ChatStore`, a `toast`
  helper; wires `visibilitychange` → `store.setForeground`, and
  `onUnauthorized` → `/login`.
- `src/push.ts` — Web Push lifecycle (service worker registers eagerly;
  `pushManager.subscribe()` is gated behind a real user gesture — the "Stay in
  the loop" modal or the Settings toggle).
- `src/audio/recorder.ts` — WebAudio PCM capture → `encodeWav` for dictation.
- `src/screens/` — `LoginScreen`, `ChatScreen`.
- `src/components/` — `Rail` (DM list + bottom-left account trigger), `Stream`
  (message list), `Topbar`, `Composer` (+ `DictationBar`), `MessageBubble`,
  `SettingsModal`, `Modals`, `Overlays`, `Avatar`, `icons`.
- `src/hooks.ts`, `src/format-ui.tsx` — `useSyncExternalStore` bindings and
  React-side formatting glue.
- `e2e/` — Playwright suite (41 tests across 7 specs) + `start-server.sh`,
  which builds an isolated server on :8795 against a scratch DB seeded by
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
  `Toasts`.
- `modules/native-menu/` — local Expo module (Swift): the UIKit context-menu
  view and glass menu button behind `NativeMenu` on iOS.

Platform styling is deliberate: **liquid glass on iOS 26+** and **flat
Material styling on Android** — same layout and the same sage palette. All
floating chrome goes through `src/components/Glass.tsx`: `GlassSurface` /
`GlassIconButton` render `expo-glass-effect` `GlassView` on iOS 26+, a
translucent paper chip on older iOS, and an elevated surface on Android;
`EdgeFade` is the scroll-edge fade (a native `experimental_backgroundImage`
gradient) that lets content dissolve under floating bars. The design is
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
- **Bearer auth** (`auth_routes.go`, `ws.go`) — `/api/login` returns
  `session_token`; REST routes and the `/ws` upgrade accept
  `Authorization: Bearer <token>` or `?token=` in addition to the
  `am_session` cookie. Close code **4401** still signals unauthenticated.
- **Voice dictation** (`transcribe.go`) — `POST /api/transcribe` accepts a
  multipart audio clip (≤ 20 MB), maps it to an OpenRouter `input_audio`
  format, and calls the chat-completions endpoint with the
  `OPENROUTER_API_KEY` (server-side only). wav/mp3 → Voxtral; other
  containers → Gemini. 60 s upstream timeout. Golden/unit-tested in
  `transcribe_test.go`.

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
redirected through `/login?next=…` unless the room allows guests),
`POST /api/meetings {mode}` → `{code}`, `GET /api/meetings/:code` →
`{mode, participants, allow_guests}` (deliberately public so the page can pick
gate vs. login before auth), plus login/logout/me and the debug-console
ingest `POST /api/debug/report`. The meet server mounts
`/static`, `/fonts`, `/sound`, and `/avatars` (so meeting tiles can show
profile photos; `meetUser` = `{id, username, display_name, avatar, guest?}`,
still no bio). The legacy 1:1 `calls` table remains in the DB but Alex Meet
does not write meeting history.

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
- `OPENROUTER_API_KEY` — required for `POST /api/transcribe` to reach a real
  model (absent in tests). `OPENROUTER_TRANSCRIBE_MODEL` and
  `OPENROUTER_BASE_URL` override the model and endpoint.

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
  `internal/db/debug_test.go`, `internal/webapp/linkpreview_test.go`,
  `internal/webapp/uploads_test.go`, `internal/webapp/transcribe_test.go`,
  `internal/meet/volctoken_test.go`, `internal/debuglog/debuglog_test.go`):
  DB layer (DM state, read receipts, attachment dims, paging, debug
  filters/prune), link-preview SSRF guard, image-dimension extraction,
  transcribe format/model mapping + upstream handling, VolcEngine token wire
  format, debug ingest rate-limiter. `go test ./...` — DB tests point the
  package-level path vars at a temp dir.
- **shared** — `cd shared && npm test` (Vitest): store reducers (optimistic
  send → delivered → read, edits, replies, unread), socket reconnect/queue,
  api error mapping, format helpers, WAV encoding.
- **web** — `cd web && npm run test:e2e` (Playwright, 41 tests): `start-server.sh`
  builds an isolated `cmd/server` on :8795 against a scratch DB seeded by
  `web/e2e/seed`, so the suite never touches dev data. Chromium runs with fake
  mic/camera flags so the dictation tests exercise the real WebAudio capture.
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
  bottom-sheet `ActionSheet` (Android `Alert.alert` caps at 3 buttons). Don't
  host list rows or bubbles in SwiftUI — per-row hosting views re-render on
  every scroll frame (see `mobile/AGENTS.md`).
- **Keyboard on iOS** — `react-native-keyboard-controller` (provider mounted
  on iOS only) moves the composer and message list with the keyboard frame
  by frame and lets a downward drag on the list or the composer dismiss it
  interactively, as in Messages. Android keeps `KeyboardAvoidingView`.

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
autocommit mode" pattern. `foreign_keys` and `busy_timeout` are set on every
pooled connection via the DSN.

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

### WebSocket auth (close 4401)

For both `/ws` endpoints, the credential is read before the upgrade — the
`am_session` cookie, `Authorization: Bearer <token>`, or `?token=` — but the
handshake is completed regardless so the server can send `close(4401)`; the
clients listen for code 4401 to route to `/login`. WS origin is not enforced
(matching Starlette).

### Identity, DMs, per-user DM state, lazy history

A DM thread between users *a* and *b* lives at synthetic channel `dm:<min>:<max>`
(`db.DMChannelID` / `db.ParseDMChannel`); these are the only channels that exist.
`dm_state` rows carry `pinned` / `last_read_at` / `force_unread` / `cleared_at`
per `(user_id, channel)`. Each thread ships only the most recent 50 messages on
connect with a `history_has_more` flag; older messages load via
`GET /api/history/{channel}?before=`. The DB schema migrates added columns
(`users.avatar`, `attachments.width/height`, `dm_state.force_unread`,
`messages.edited_at`) on startup via `ensureColumn`, tolerant of the three
binaries racing the same ALTER.

## Data / runtime state

Lives under `data/` (gitignored), created on first run:
- `data/alexmessage.db` — SQLite (users, sessions, messages, attachments, contacts,
  calls, push_subscriptions, dm_state).
- `data/uploads/<user_id>/<token>_<filename>` — per-user uploads; account deletion
  removes the directory.
- `data/avatars/<user_id>_<token>.<ext>` — profile photos; replaced on change,
  removed on account deletion.
- `data/vapid_private.pem` / `data/vapid_public.txt` — VAPID key pair.

Alex Meet rooms are in-memory only — a meet-server restart ends all meetings.

## Endpoints

See the route files under `internal/webapp/` (user app),
`internal/adminapp/` (admin; `adminapp.go` + `debug.go`), and
`internal/meet/routes.go` (Alex Meet). The main-app WebSocket protocol: client
sends `message` (with an optional `client_id` optimistic-send nonce)/`edit`/
`switch`/`open_dm`/`ping`; server sends `init`/`message` (echoes `client_id`
back to the sender)/`message_edited`/`presence`/`profile_update`/`dm_opened`/
`dm_read`. The Alex Meet control-plane protocol is documented above.

Notable additions for the TS clients: `POST /api/transcribe` (dictation proxy
to OpenRouter) and the `session_token` field on `POST /api/login` (bearer auth
for native).

Debug console (admin): `GET /api/debug/events` (filtered snapshot),
`GET /api/debug/stream` (SSE live tail), `POST /api/debug/clear`. Ingestion:
`POST /api/debug/report` on both the user app and the meet server. See the
**Debug console** section above.

See also `DEPLOY.md` (build/run/deploy for humans and agents) and
`Investigation.md` (the problem/threat audit from the FreshRefactor pass).
