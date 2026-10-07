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

Production is a Raspberry Pi (`picloud`, linux/**arm64**) reached through an
frp relay — see **Production** below. Pushing to `FreshRefactor` or `react`
deploys automatically (GitHub Actions); to deploy by hand:

```bash
# 0. From the repo root. Verify before shipping.
go build ./... && go vet ./... && go test ./...

# 1. Cross-compile static linux/arm64 binaries.
mkdir -p bin
for c in server admin meet; do
  GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o bin/$c ./cmd/$c
done

# 2. Bundle binaries + assets (NOT data/ — it lives on the server).
tar czf release.tgz --exclude static/test-speech.wav bin/ static/ fonts/ sound/ \
    index.html login.html admin.html meet.html meet_login.html

# 3. Ship + restart through the relay (ssh -p 2222 alexander@35.226.215.209;
#    `picloud` in ~/.ssh/config). The script backs up bin/ and the database,
#    restarts the units, health-checks them, and rolls back on failure.
scp -P 2222 release.tgz alexander@35.226.215.209:/home/alexander/alexmessage/
ssh -p 2222 alexander@35.226.215.209 \
    "APP_DIR=/home/alexander/alexmessage HEALTH_PORTS='6001 6002 6003' bash -s" \
    < deploy/remote_deploy.sh
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

## Production (Raspberry Pi behind an frp relay)

The app runs on a Raspberry Pi, **`picloud`** (Debian 13, **arm64**), app dir
`/home/alexander/alexmessage` (with `data/` and `backups/`). The Pi has no
public address; the GCP VM at **`35.226.215.209`** is only an **frp relay**:

```
internet ──► 35.226.215.209 (frps :7000)  ◄── frpc on the Pi (/etc/frp/frpc.toml)
               :80  / :443  ──tcp──► Pi nginx 127.0.0.1:8080 / :8443 (PROXY protocol v2)
               :2222        ──tcp──► Pi sshd :22
```

- **nginx on the Pi** terminates TLS (one vhost per domain in
  `/etc/nginx/sites-enabled/`) and reverse-proxies to the binaries. Vhosts must
  pass WebSocket upgrade headers for `/ws` and must **not buffer**
  `text/event-stream` for the admin debug stream.
- **systemd units** (`/etc/systemd/system/`), all `User=alexander`,
  `WorkingDirectory=/home/alexander/alexmessage`, secrets in
  `EnvironmentFile=/etc/alexmessage/alexmessage.env` (`ADMIN_PASSWORD`,
  `VOLC_RTC_APP_ID`/`VOLC_RTC_APP_KEY`; add `OPENROUTER_API_KEY` there for
  voice transcription):

  | Unit | Binary | Port | Domain |
  |------|--------|------|--------|
  | `alexmessage-admin` | `bin/admin` | `6001` | `admin.alexanderjia.com` |
  | `alexmessage` | `bin/server` | `6002` | `messages.alexanderjia.com` |
  | `alexmessage-voice` | `bin/meet` | `6003` | `meet.alexanderjia.com` |

  (Coolify on the same Pi was moved off these ports — its UI is on `6000`, its
  realtime on `16001`/`16002`; see `PiCloudMigration.md` in the app dir.)
- **SSH**: `ssh -p 2222 alexander@35.226.215.209` (the relay forwards raw TCP;
  the host key is the Pi's own). `alexander` has passwordless sudo.
- **Web client**: the release carries no `web/dist`, so the server serves the
  legacy `static/` frontend. Shipping the React web app means building
  `web/dist` and adding it to the bundle.

## Continuous deployment (GitHub Actions)

`.github/workflows/deploy.yml`:

- **Pull requests** into `FreshRefactor`/`react` run the tests only:
  `go build`/`vet`/`test` plus the `shared/` typecheck + Vitest suite.
- **Pushes** to `FreshRefactor` or `react` — including merging a PR — and
  manual *Run workflow* runs then **deploy**: cross-compile the three
  `linux/arm64` binaries, bundle them with `static/ fonts/ sound/` + the five
  HTML shells (**never `data/`**), `scp` the bundle to the Pi through the relay
  (port `2222`), and run `deploy/remote_deploy.sh` there. That script backs up
  `bin/` and copies the database to `backups/` (keeping the newest 5) while the
  units are stopped, extracts the release, starts the units, health-checks
  ports `6001 6002 6003`, and **rolls the binaries back** if any fails. A final
  step checks `https://messages.alexanderjia.com/api/me` end to end (relay →
  nginx → app).

The deploy job only runs when the repo variable **`DEPLOY_ENABLED` is `true`**.

### One-time setup

**1. A dedicated deploy key** (passphrase-less — CI can't type one), authorized
on the Pi:

```bash
ssh-keygen -t ed25519 -N '' -C 'alexmessages-ci-deploy' -f ~/.ssh/alexmessages_deploy
ssh picloud 'cat >> ~/.ssh/authorized_keys' < ~/.ssh/alexmessages_deploy.pub
```

> `alexander` has full passwordless sudo on the Pi, so this key is effectively
> root there. To narrow it, prefix the authorized_keys line with
> `from="<GitHub runner ranges>"`, or move the units to an unprivileged `deploy`
> user whose sudoers entry allows only
> `systemctl stop|start|restart alexmessage alexmessage-admin alexmessage-voice`.

**2. Repo secrets** (Settings → Secrets and variables → Actions → *Secrets*):

| Secret | Value |
|--------|-------|
| `DEPLOY_SSH_KEY` | the **private** key (`pbcopy < ~/.ssh/alexmessages_deploy`, paste) |
| `DEPLOY_KNOWN_HOSTS` | *optional* — the workflow pins the Pi's current ed25519 key; set this (`ssh-keyscan -p 2222 35.226.215.209`) only if the Pi is reinstalled |

**3. Repo variables** (same screen → *Variables*):

| Variable | Value | Notes |
|----------|-------|-------|
| `DEPLOY_ENABLED` | `true` | Master switch; unset = tests only |
| `DEPLOY_HOST` / `DEPLOY_PORT` / `DEPLOY_USER` / `DEPLOY_PATH` | *optional* | Default `35.226.215.209` / `2222` / `alexander` / `/home/alexander/alexmessage` |
| `HEALTH_PORTS` | *optional* | Default `6001 6002 6003` — must match the units |
| `PUBLIC_URL` | *optional* | Default `https://messages.alexanderjia.com` |

Then push (or *Run workflow*) and watch the repo's **Actions** tab.

> **Secret hygiene:** the private key only ever lives in the `DEPLOY_SSH_KEY`
> secret and the runner's ephemeral disk — never commit it.

## Health check after a deploy

```bash
# Each /api/me returns 401 when unauthenticated — that 401 means "up".
for p in 6001 6002 6003; do   # on the Pi (dev defaults: 8765 8001 8002)
  curl -s -o /dev/null -w "port $p -> %{http_code}\n" http://127.0.0.1:$p/api/me
done
```

Then sign into the admin panel and watch the **Debug console** — live client and
server events confirm the whole stack is wired up end to end.
