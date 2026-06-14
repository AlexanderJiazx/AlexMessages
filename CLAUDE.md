# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

**Alex Messages (Go edition)** — an internet-hosted messaging app, originally a
faithful Go/Gin port of the Python/FastAPI project at `~/Code/ChatRoom`
(FastAPI → [Gin](https://github.com/gin-gonic/gin)).

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
  the client renders an inline editor (Save/Cancel) and an "(edited)" tag.
- **Image dimensions** — `/api/upload` decodes images and returns
  `width`/`height`, persisted on `attachments`, so the client reserves a
  correctly sized loading placeholder.
- **Link previews** — `GET /api/link-preview?url=` fetches OpenGraph metadata
  server-side (SSRF-guarded dialer, in-memory TTL cache) for preview cards.
- **UI refresh** — message bubbles (own messages right-aligned), sidebar rows
  show avatar + name + last-message preview, settings/account in a floating
  sheet, compose icon in the rail header starts a new chat. The composer is a
  long pill (iMessage-style) with a circular arrow-up send button and image
  thumbnails for pending attachments; the recipient's online status is a chip
  under their name in the topbar (single source of truth, no duplicate).
- **Branding** — user-visible name is **"Alex Messages"** (title, rail header,
  manifest, service-worker notifications, login/admin pages). The Go module
  remains `alexmessage`.

The frontend is HTML / CSS / vanilla JS under `static/`, plus `index.html`,
`login.html`, `admin.html`, `meet.html`, `meet_login.html`, and the `fonts/`
and `sound/` asset trees.

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
  Credentials come from `VOLC_RTC_APP_ID` / `VOLC_RTC_APP_KEY` (defaults are
  baked in). VolcEngine user ids are `p<pid>` so streams map back to roster
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

## Layout

```
cmd/
  server/main.go   entrypoint: bootstrap + serve user app
  admin/main.go    entrypoint: bootstrap + serve admin panel
  meet/main.go     entrypoint: bootstrap + serve Alex Meet
internal/
  db/        SQLite schema + every query helper; debug.go (ring buffer) + maintenance.go (sweeps)
  auth/      scrypt hashing, session tokens, admin bootstrap
  push/      VAPID bootstrap + Web Push delivery
  runtime/   main-app shared state: presence, visibility, broadcasts
  httpx/     tiny shared HTTP helpers: {"detail": …} errors, cookies, graceful Serve
  debuglog/  debug-console ingestion: rate-limited /api/debug/report + server-side Emit
  webapp/    the user app: engine + one file per route module
  adminapp/  the admin panel + adminapp/debug.go (debug-console read API)
  meet/      Alex Meet (state.go, ws.go, routes.go, volctoken.go)
```

The webapp route files: `pages.go`, `auth_routes.go`, `me.go`, `users.go`,
`uploads.go`, `push_routes.go`, `dm_state.go`, `history.go`, `ws.go`, plus
`avatar.go` (profile photos) and `linkpreview.go` (link previews).

## Debug console (admin panel)

A centralized, real-time debug console lives in the **admin panel**. Because the
three binaries are separate processes that share only the SQLite file, the
shared `debug_events` table (a capped ring buffer; `db/debug.go`) is the channel
between them:

- **Ingestion** — both client-facing servers expose `POST /api/debug/report`
  (built from `debuglog.ReportHandler`). The browser clients batch real-time
  actions (`Debug.info/warn/error/debug(event, message, ctx)` in `static/app.js`
  and `meet.html`) and stream them there; server code records its own status
  with `debuglog.Emit(app, level, event, message, ctx)` (e.g. ws connect/
  disconnect, meeting create/join/leave). The actor's identity is resolved
  server-side from the session cookie — never trusted from the body.
- **Abuse prevention** — per-IP token bucket, 64 KB body cap, 50-event batch
  cap, per-field length limits, level whitelist. Over-budget events are dropped
  but the endpoint always returns `200 {"ok":true}`.
- **Reading** — the admin panel (`adminapp/debug.go`) serves `GET /api/debug/events`
  (filtered snapshot), `GET /api/debug/stream` (SSE live tail; polls the table
  on a 1s tick since the writers are other processes), and `POST /api/debug/clear`.
  The admin UI filters by **app, level, user, session, and free-text** search.

## Run / develop

Requires Go 1.25+. Run each binary **from the repo root** (paths to HTML, `static/`,
`fonts/`, `sound/`, and `data/` are resolved relative to the working directory).

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
  Meet's `volc` backend (defaults baked into `internal/meet/routes.go`).

`go vet ./...` and `go build ./...` should both stay clean.

Note: `getUserMedia`/screen capture require a secure context — `localhost` is
fine for development, but production Alex Meet must be served over HTTPS.

### Testing

Unit tests live next to the code (`internal/db/db_test.go`,
`internal/db/debug_test.go`, `internal/webapp/linkpreview_test.go`,
`internal/webapp/uploads_test.go`, `internal/meet/volctoken_test.go`,
`internal/debuglog/debuglog_test.go`) and cover the DB layer (DM state, read
receipts, attachment dimensions, paging, debug-event filters/prune), the
link-preview parser/SSRF guard, image-dimension extraction, the VolcEngine
AccessToken wire format (golden + round-trip), and the debug ingest
rate-limiter/sanitizers. Run with `go test ./...`. The DB tests point the
package-level path vars at a temp dir.

For end-to-end checks, run the binaries and exercise the flow by hand:
register → admin approve → login → DM message (over `/ws`) → edit it → upload
→ contacts → logout → admin delete. For Alex Meet: lobby → new meeting (each
backend) → second participant via the link → mute/camera/screen share → host
mute + transfer → leave. The user app and admin panel must both be running
(they share `data/alexmessage.db`); the meet server is independent. A fresh
`data/` is created on first run.

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

Responses are byte-compatible with FastAPI where it matters:
- Errors are `{"detail": "<message>"}` with the original status code (the frontend
  reads `data.detail`).
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

For both `/ws` endpoints, the cookie is read before the upgrade, but the handshake
is completed regardless so the server can send `close(4401)` — the client listens
for code 4401 to redirect to `/login`. WS origin is not enforced (matching
Starlette).

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
sends `message`/`edit`/`switch`/`open_dm`/`ping`; server sends `init`/`message`/
`message_edited`/`presence`/`profile_update`/`dm_opened`/`dm_read`. The Alex
Meet control-plane protocol is documented above.

Debug console (admin): `GET /api/debug/events` (filtered snapshot),
`GET /api/debug/stream` (SSE live tail), `POST /api/debug/clear`. Ingestion:
`POST /api/debug/report` on both the user app and the meet server. See the
**Debug console** section above.

See also `DEPLOY.md` (build/run/deploy for humans and agents) and
`Investigation.md` (the problem/threat audit from the FreshRefactor pass).
