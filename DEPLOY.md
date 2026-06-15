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
| `VOLC_RTC_APP_ID` / `VOLC_RTC_APP_KEY`| *(required; no default)*     | meet      |

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

## Continuous deployment (GitHub Actions)

`.github/workflows/deploy.yml` automates the agent TL;DR above. On every push
to **`FreshRefactor`** (and on manual *Run workflow*), it:

1. **Tests** — `go build ./... && go vet ./... && go test ./...`.
2. **Builds** — cross-compiles the three static `linux/amd64` binaries
   (`CGO_ENABLED=0`, `-trimpath -ldflags='-s -w'`).
3. **Ships** — `scp`s `release.tgz` (binaries + `static/ fonts/ sound/` + the
   five HTML files; **never `data/`**) to the host.
4. **Restarts & verifies** — runs `deploy/remote_deploy.sh` on the host, which
   backs up the old binaries, extracts the release, `systemctl restart`s the
   three units, health-checks each port, and **rolls the binaries back** if any
   unit fails to answer.

The deploy job only runs when the repo variable **`DEPLOY_ENABLED` is `true`**,
so until you finish the one-time setup the workflow just runs tests.

### One-time setup

**1. Create a dedicated deploy key** (no passphrase — CI can't type one):

```bash
ssh-keygen -t ed25519 -N '' -C 'alexmessages-deploy' -f ~/.ssh/alexmessages_deploy
```

Add the **public** half to the host's authorized keys:

```bash
ssh-copy-id -i ~/.ssh/alexmessages_deploy.pub me@35.226.215.209
# or append ~/.ssh/alexmessages_deploy.pub to me@HOST:~/.ssh/authorized_keys
```

**2. Allow passwordless restart** on the host (the deploy user can't type a
sudo password over non-interactive SSH). As root, `visudo -f /etc/sudoers.d/alexmessage-deploy`:

```
me ALL=(root) NOPASSWD: /usr/bin/systemctl restart alexmessage alexmessage-admin alexmessage-voice, /usr/bin/systemctl restart alexmessage, /usr/bin/systemctl restart alexmessage-admin, /usr/bin/systemctl restart alexmessage-voice
```

(`which systemctl` → adjust the path if it isn't `/usr/bin/systemctl`.)

**3. Add the GitHub repo secrets** (Settings → Secrets and variables → Actions
→ *Secrets*):

| Secret | Value |
|--------|-------|
| `DEPLOY_SSH_KEY` | contents of the **private** key `~/.ssh/alexmessages_deploy` |
| `DEPLOY_KNOWN_HOSTS` | output of `ssh-keyscan 35.226.215.209` (pins the host key) |
| `DEPLOY_HOST` | `35.226.215.209` |
| `DEPLOY_USER` | `me` |
| `DEPLOY_PATH` | `/home/me/alexmessage` |

**4. Add the repo variables** (same screen → *Variables*):

| Variable | Value | Notes |
|----------|-------|-------|
| `DEPLOY_ENABLED` | `true` | Master switch; leave unset to keep deploys off |
| `HEALTH_PORTS` | `8765 9090 9091` | Must match the units' `PORT` / `ADMIN_PORT` / `CALL_PORT` |

> Production ports differ from the dev defaults: per the units above, `server`
> is `8765`, `admin` is `9090`, `meet` is `9091`. Set `HEALTH_PORTS` to whatever
> your units actually bind, or the post-deploy check will roll back a healthy
> release.

**5. Confirm the host is ready** — the app dir exists, the `VOLC_RTC_APP_ID` /
`VOLC_RTC_APP_KEY` (and `ADMIN_PASSWORD`) live in the systemd units, and
`alexmessage-voice`'s `ExecStart` points at `bin/meet` (rename note above).

Then flip `DEPLOY_ENABLED=true` and push (or *Run workflow*). Watch it under the
repo's **Actions** tab.

> **Secret hygiene:** the private key only ever lives in the `DEPLOY_SSH_KEY`
> secret and the runner's ephemeral disk — never commit it. Copy it into the
> secret without it landing in your shell history, e.g. `pbcopy < ~/.ssh/alexmessages_deploy`
> then paste into the GitHub UI.

## Health check after a deploy

```bash
# Each /api/me returns 401 when unauthenticated — that 401 means "up".
for p in 8765 8001 8002; do
  curl -s -o /dev/null -w "port $p -> %{http_code}\n" http://127.0.0.1:$p/api/me
done
```

Then sign into the admin panel and watch the **Debug console** — live client and
server events confirm the whole stack is wired up end to end.
