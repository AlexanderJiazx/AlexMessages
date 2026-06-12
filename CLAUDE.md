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
| `cmd/admin`       | `127.0.0.1:8001`    | Admin control panel |
| `cmd/voicecall`   | `127.0.0.1:8002`    | **Alex Meet** — multi-party meetings |

## Alex Meet (cmd/voicecall, internal/voicecall)

The former 1:1 "AlexMessage Call" was replaced wholesale by **Alex Meet**, a
Google Meet-style meeting app (audio + video + screen share, 4–5 people in
practice, 9 tiles per grid page). One UI, two interchangeable media backends
chosen per meeting from a lobby dropdown:

- **`mesh` — Standard WebRTC ("Default experience").** Full-mesh
  RTCPeerConnections between participants; the server only relays opaque
  SDP/ICE (`signal` messages). The client uses the MDN perfect-negotiation
  pattern (newcomer = polite peer) and creates audio+video transceivers
  up-front so mute/device-switch/screen-share are all `replaceTrack` — no
  renegotiation. Google STUN only, no TURN.
- **`volc` — VolcEngine RTC ("Better performance in China").** Media flows
  through the VolcEngine Web SDK (vendored at
  `static/vendor/volc-rtc-4.68.5.min.js`, UMD global `VERTC`); the server
  mints per-participant **AccessTokens** (`internal/voicecall/volctoken.go`,
  ported from the reference implementation in volcengine/VolcEngineRTC —
  little-endian packing, HMAC-SHA256, golden-tested in `volctoken_test.go`).
  Credentials come from `VOLC_RTC_APP_ID` / `VOLC_RTC_APP_KEY` (defaults are
  baked in). VolcEngine user ids are `p<pid>` so streams map back to roster
  entries.

Either way, every participant stays on the meet server's `/ws` **control
plane**: it owns the roster, AV state fan-out (`peer_state`), and host powers.
Protocol: client sends `join {code}` / `leave` / `signal {to, payload}` /
`state {muted, cam_on, sharing}` / `host_mute {pid}` / `host_transfer {pid}` /
`ping`; server sends `hello` / `joined` (roster + `host_pid` + `volc` join
payload when applicable) / `peer_joined` / `peer_left` / `signal {from}` /
`peer_state` / `host_changed` / `force_mute` / `error` / `pong`.

Rooms (`internal/voicecall/state.go`) are in-memory only, keyed by
`xxx-xxxx-xxx` codes (no i/l/o). The first joiner is host; when the host
leaves, the longest-present participant inherits (`host_changed`). Empty rooms
survive a 5-minute grace (refresh-proof) and never-joined rooms an hour, then
are pruned lazily. The same account in two tabs is two participants.

Routes: `GET /` (lobby) and `GET /m/:code` (meeting page, redirects through
`/login?next=…`), `POST /api/meetings {mode}` → `{code}`,
`GET /api/meetings/:code` → `{mode, participants}`, plus login/logout/me.
The meet server mounts `/static`, `/fonts`, `/sound`, and `/avatars` (so
meeting tiles can show profile photos; `vcUser` = `{id, username,
display_name, avatar}`, still no bio). The legacy 1:1 `calls` table remains in
the DB but Alex Meet does not write meeting history.

Frontend (`meet.html`, single file): lobby → pre-join gate (mic/cam toggles;
the click is the user gesture autoplay policies want) → meeting. Meeting UI:
rounded main view; right-hand vertical preview stack (click a tile to pin,
click the main view to unpin); grid "group view" paged at 9 tiles; bottom bar
with mic/cam split buttons (chevron opens an input-device picker), screen
share, grid toggle on the left and the red leave pill on the right; top-left
copy-link button + code chip; top-right people panel with host mute /
make-host actions. All icons are embedded Lucide SVGs. Tiles never get
destroyed on layout changes — they move between main/stack/grid containers and
an off-screen "park" so media keeps playing; in mesh mode remote audio plays
through a fixed hidden audio pool so tile juggling can't interrupt it.

## Layout

```
cmd/
  server/main.go      entrypoint: bootstrap + serve user app
  admin/main.go       entrypoint: bootstrap + serve admin panel
  voicecall/main.go   entrypoint: bootstrap + serve Alex Meet
internal/
  db/        SQLite schema + every query helper (was db.py)
  auth/      scrypt hashing, session tokens, admin bootstrap (was auth.py)
  push/      VAPID bootstrap + Web Push delivery (was push.py)
  runtime/   main-app shared state: presence, visibility, broadcasts
  httpx/     tiny shared HTTP helpers: {"detail": …} errors + session cookies
  webapp/    the user app: engine + one file per route module
  adminapp/  the admin panel (was admin.py)
  voicecall/ Alex Meet (state.go, ws.go, routes.go, volctoken.go)
```

The webapp route files: `pages.go`, `auth_routes.go`, `me.go`, `users.go`,
`uploads.go`, `push_routes.go`, `dm_state.go`, `history.go`, `ws.go`, plus
`avatar.go` (profile photos) and `linkpreview.go` (link previews).

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
ADMIN_PASSWORD=changeme go run ./cmd/voicecall
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
  Meet's `volc` backend (defaults baked into `internal/voicecall/routes.go`).

`go vet ./...` and `go build ./...` should both stay clean.

Note: `getUserMedia`/screen capture require a secure context — `localhost` is
fine for development, but production Alex Meet must be served over HTTPS.

### Testing

Unit tests live next to the code (`internal/db/db_test.go`,
`internal/webapp/linkpreview_test.go`, `internal/webapp/uploads_test.go`,
`internal/voicecall/volctoken_test.go`) and cover the DB layer (DM state, read
receipts, attachment dimensions, paging), the link-preview parser/SSRF guard,
image-dimension extraction, and the VolcEngine AccessToken wire format
(golden + round-trip). Run with `go test ./...`. The DB tests point the
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
  `status`, `is_admin`, `created_at`), and the meet server's `vcUser`
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
`internal/adminapp/adminapp.go` (admin), and `internal/voicecall/routes.go`
(Alex Meet). The main-app WebSocket protocol: client sends
`message`/`edit`/`switch`/`open_dm`/`ping`; server sends `init`/`message`/
`message_edited`/`presence`/`profile_update`/`dm_opened`/`dm_read`. The Alex
Meet control-plane protocol is documented above.
