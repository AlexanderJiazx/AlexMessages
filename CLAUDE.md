# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

**AlexMessage (Go edition)** — an internet-hosted messaging app, originally a
faithful Go/Gin port of the Python/FastAPI project at `~/Code/ChatRoom`
(FastAPI → [Gin](https://github.com/gin-gonic/gin)).

Since the June 2026 upgrade (`UpgradeJune.md`) the app has **diverged** from the
Python original:
- **Channels were removed entirely.** The app is DM-only: every conversation is
  a `dm:<min>:<max>` thread. No channel catalog, no default channel.
- **Profile photos** — `POST`/`DELETE /api/me/avatar`, files stored at
  `data/avatars/<uid>_<token>.<ext>`, served under `/avatars/`;
  `runtime.PublicUser` carries an `avatar` URL field.
- **Read receipts** — marking a DM read broadcasts a `dm_read` WS event to both
  participants; init/`dm_opened` `dm_state` payloads include `peer_last_read_at`.
- **Image dimensions** — `/api/upload` decodes images and returns
  `width`/`height`, persisted on `attachments`, so the client reserves a
  correctly sized loading placeholder.
- **Link previews** — `GET /api/link-preview?url=` fetches OpenGraph metadata
  server-side (SSRF-guarded dialer, in-memory TTL cache) for preview cards.
- **UI refresh** — message bubbles (own messages right-aligned), sidebar rows
  show avatar + name + last-message preview (attachments as
  `Attachment: image|video|file`), settings/account in a floating sheet (the
  right rail is gone), compose icon in the rail header starts a new chat.

The frontend is HTML / CSS / vanilla JS under `static/`, plus `index.html`,
`login.html`, `admin.html`, `voicecall.html`, `voicecall_login.html`, and the
`fonts/` and `sound/` asset trees.

There are **three independent server binaries** that share one SQLite database,
exactly like the three Python processes:

| Binary            | Mirrors        | Default addr        | Purpose |
|-------------------|----------------|---------------------|---------|
| `cmd/server`      | `server.py`    | `0.0.0.0:8765`      | User-facing chat app + `/ws` |
| `cmd/admin`       | `admin.py`     | `127.0.0.1:8001`    | Admin control panel |
| `cmd/voicecall`   | `voicecall.py` | `127.0.0.1:8002`    | WebRTC signaling for voice calls |

## Layout

```
cmd/
  server/main.go      entrypoint: bootstrap + serve user app
  admin/main.go       entrypoint: bootstrap + serve admin panel
  voicecall/main.go   entrypoint: bootstrap + serve voice-call server
internal/
  db/        SQLite schema + every query helper (was db.py)
  auth/      scrypt hashing, session tokens, admin bootstrap (was auth.py)
  push/      VAPID bootstrap + Web Push delivery (was push.py)
  runtime/   main-app shared state: presence, visibility, broadcasts (was runtime.py)
  httpx/     tiny shared HTTP helpers: {"detail": …} errors + session cookies (was deps.py)
  webapp/    the user app: engine + one file per Python route module
  adminapp/  the admin panel (was admin.py)
  voicecall/ the voice-call server (state.go, ws.go, routes.go) (was voicecall.py)
```

The Python `routes/` modules map one-to-one onto files in `internal/webapp/`:
`pages.go`, `auth_routes.go`, `me.go`, `users.go`, `uploads.go`, `push_routes.go`,
`dm_state.go`, `history.go`, `ws.go`. Post-upgrade additions with no Python
counterpart: `avatar.go` (profile photos) and `linkpreview.go` (link previews).

## Run / develop

Requires Go 1.25+. Run each binary **from the repo root** (paths to HTML, `static/`,
`fonts/`, `sound/`, and `data/` are resolved relative to the working directory).

```bash
# Main app (port 80 in prod needs sudo; 8765 locally)
ADMIN_PASSWORD=changeme PORT=8765 go run ./cmd/server
ADMIN_PASSWORD=changeme PORT=80   go run ./cmd/server   # prod

# Admin panel (default 127.0.0.1:8001)
ADMIN_PASSWORD=changeme go run ./cmd/admin

# Voice-call server (default 127.0.0.1:8002)
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

`go vet ./...` and `go build ./...` should both stay clean.

### Testing

Unit tests live next to the code (`internal/db/db_test.go`,
`internal/webapp/linkpreview_test.go`, `internal/webapp/uploads_test.go`) and
cover the DB layer (DM state, read receipts, attachment dimensions, paging),
the link-preview parser/SSRF guard, and image-dimension extraction. Run with
`go test ./...`. The DB tests point the package-level path vars at a temp dir.

For end-to-end checks, run the binaries and exercise the flow by hand:
register → admin approve → login → DM message (over `/ws`) → upload → contacts
→ logout → admin delete. The user app and admin panel must both be running
(they share `data/alexmessage.db`); the voice-call server is independent. A
fresh `data/` is created on first run.

## Architecture notes specific to the Go port

### Concurrency

The Python servers ran on a single-threaded asyncio event loop, so shared state
needed no locks. Go has real concurrency (one goroutine per WebSocket read loop,
plus broadcast goroutines), so:

- **Per-connection write mutex.** Gorilla WebSocket connections are not safe for
  concurrent writes. `runtime.Client` (main app) and `voicecall.conn` each carry a
  `sync.Mutex` that serializes every send, so two fan-outs never interleave a
  frame on the same socket.
- **Presence is mutex-guarded.** `runtime.Presence` and `voicecall.presenceTracker`
  protect their maps with their own mutex.
- **Voice-call state** keeps the original's single-lock design: `callMu` guards
  `calls`, `userToCall`, and per-`Call` fields, and is held across the sends in
  each handler — exactly as `voicecall.py` `await`-ed under one asyncio lock. The
  only exception is signaling relay, which sends after releasing the lock.

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
  distinct Python dict shapes. Nullable fields (`user_id`, `author`, `reply_to`)
  serialize as JSON `null`, not omitted.
- The three different public-user views are preserved: `runtime.PublicUser`
  (`{id, username, display_name, bio, avatar}`), `db.UserToPublic` (admin: adds
  `status`, `is_admin`, `created_at`), and `voicecall.vcUser`
  (`{id, username, display_name}`, no bio).

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
(`users.avatar`, `attachments.width/height`, `dm_state.force_unread`) on startup
via `ensureColumn`, tolerant of the three binaries racing the same ALTER.

## Data / runtime state

Lives under `data/` (gitignored), created on first run:
- `data/alexmessage.db` — SQLite (users, sessions, messages, attachments, contacts,
  calls, push_subscriptions, dm_state).
- `data/uploads/<user_id>/<token>_<filename>` — per-user uploads; account deletion
  removes the directory.
- `data/avatars/<user_id>_<token>.<ext>` — profile photos; replaced on change,
  removed on account deletion.
- `data/vapid_private.pem` / `data/vapid_public.txt` — VAPID key pair.

## Endpoints

See the route files under `internal/webapp/` (user app),
`internal/adminapp/adminapp.go` (admin), and `internal/voicecall/` (auth +
`/api/calls/recent` + `/ws` signaling). The WebSocket protocol: client sends
`message`/`switch`/`open_dm`/`ping`; server sends `init`/`message`/`presence`/
`profile_update`/`dm_opened`/`dm_read`.
