# Deploying Alex Messages + Alex Meet

Two products, three binaries, one shared SQLite database:

| Binary        | Build from      | Default bind     | Serves                          |
|---------------|-----------------|------------------|---------------------------------|
| `server`      | `./cmd/server`  | `0.0.0.0:8765`   | Alex Messages chat app + `/ws`  |
| `admin`       | `./cmd/admin`   | `127.0.0.1:8001` | Admin panel (+ debug console)   |
| `meet`        | `./cmd/meet`    | `127.0.0.1:8002` | Alex Meet meetings + `/ws`      |

All three call `db.InitDB()` against `data/alexmessage.db` (created on first
run), so **run them from the same working directory**. Requires **Go 1.25+**.
`getUserMedia`/screen-share need a secure context, so production **must** be
HTTPS (terminate TLS at the reverse proxy).

---

## TL;DR for agents

```bash
# 0. From the repo root. Verify before shipping.
go build ./... && go vet ./... && go test ./...

# 1. Cross-compile static linux/amd64 binaries.
mkdir -p bin
for c in server admin meet; do
  GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/$c ./cmd/$c
done

# 2. Bundle binaries + assets (NOT data/ — it lives on the server).
tar czf release.tgz bin/ static/ fonts/ sound/ \
    index.html login.html admin.html meet.html meet_login.html

# 3. Ship + restart (production host details: see the GCP section below).
scp release.tgz me@HOST:/home/me/alexmessage/
ssh me@HOST 'cd /home/me/alexmessage && tar xzf release.tgz && \
    sudo systemctl restart alexmessage alexmessage-admin alexmessage-voice'
```

> **Stale-cache gotcha:** asset URLs are stable (`/static/app.js`) and the
> server sends no `Cache-Control` on them, so browsers (Safari especially) keep
> serving the old frontend after a deploy. Tell testers to hard-refresh, or add
> cache-busting before relying on a frontend change being live.

---

## Run locally (development)

```bash
# Each in its own terminal, from the repo root:
ADMIN_PASSWORD=changeme PORT=8765 go run ./cmd/server
ADMIN_PASSWORD=changeme           go run ./cmd/admin   # 127.0.0.1:8001
ADMIN_PASSWORD=changeme           go run ./cmd/meet    # 127.0.0.1:8002
```

Open `http://localhost:8765` (chat), `http://localhost:8001` (admin),
`http://localhost:8002` (meet). `localhost` counts as a secure context, so
camera/mic/screen-share work without TLS in dev.

First run prints the seed admin account to stderr (a random password if
`ADMIN_PASSWORD` is unset).

## Configuration (environment variables)

| Variable                              | Default                      | Used by   |
|---------------------------------------|------------------------------|-----------|
| `ADMIN_USERNAME` / `ADMIN_PASSWORD`   | `admin` / *(random, logged)* | all       |
| `HOST` / `PORT`                       | `0.0.0.0` / `8765`           | server    |
| `ADMIN_HOST` / `ADMIN_PORT`           | `127.0.0.1` / `8001`         | admin     |
| `CALL_HOST` / `CALL_PORT`             | `127.0.0.1` / `8002`         | meet      |
| `VAPID_SUBJECT`                       | `mailto:admin@…`             | server    |
| `VOLC_RTC_APP_ID` / `VOLC_RTC_APP_KEY`| baked-in defaults            | meet      |

The seed admin is created only if no admin exists yet; the bootstrap is
idempotent and race-safe across the three processes.

## Production (GCP reference deployment)

Host: `me@35.226.215.209` (Debian/amd64). App dir `/home/me/alexmessage`.

- **systemd units** (in `/etc/systemd/system/`), env vars set inside each unit:
  - `alexmessage` → `bin/server`, `127.0.0.1:8765`, `messages.alexanderjia.com`
  - `alexmessage-admin` → `bin/admin`, `:9090`, `admin.alexanderjia.com`
  - `alexmessage-voice` → `bin/meet`, `:9091`, `meet.alexanderjia.com`
- **nginx**: one vhost per domain in `sites-available/` (symlinked into
  `sites-enabled/`), certbot/Let's Encrypt TLS. Each vhost reverse-proxies to
  its binary and must pass WebSocket upgrade headers (`Upgrade`/`Connection`)
  through for `/ws` and **not buffer** `text/event-stream` for the admin debug
  stream (the handler already sends `X-Accel-Buffering: no`).
- **Deploy**: the agent TL;DR above. After extracting, `systemctl restart` the
  three units.

> **Binary rename note:** this branch renamed `cmd/voicecall` → `cmd/meet`, so
> the built binary is now `bin/meet` (was `bin/voicecall`). Update the
> `alexmessage-voice` unit's `ExecStart` to point at `bin/meet` on the next
> deploy.

## Health check after a deploy

```bash
# Each /api/me returns 401 when unauthenticated — that 401 means "up".
for p in 8765 8001 8002; do
  curl -s -o /dev/null -w "port $p -> %{http_code}\n" http://127.0.0.1:$p/api/me
done
```

Then sign into the admin panel and watch the **Debug console** — live client and
server events confirm the whole stack is wired up end to end.
