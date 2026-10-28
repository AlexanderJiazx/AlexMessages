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
frp relay — see **Production** below. Pushing to `main` or `react`
runs tests. It also deploys when `DEPLOY_ENABLED=true` and the deployment
credentials are configured. To deploy by hand after distributing updated
mobile binaries:

```bash
# 0. From the repo root. Verify before shipping.
go build ./... && go vet ./... && go test ./...
python3 deploy/remote_deploy_test.py
(cd shared && npm ci && npm run typecheck && npm test)
(cd web && npm ci && npm run build && npx playwright install chromium && npm run test:e2e)

# 1. Cross-compile static linux/arm64 binaries. main.commit is the commit each
#    binary logs at start ("server built from commit <sha>").
mkdir -p bin
commit=$(git rev-parse HEAD)
for c in server admin meet; do
  GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.commit=$commit" -o bin/$c ./cmd/$c
done

# 2. Bundle binaries + assets (NOT data/ — it lives on the server). On a Mac,
#    COPYFILE_DISABLE=1 and --no-xattrs keep macOS file metadata out of it.
COPYFILE_DISABLE=1 tar --no-xattrs -czf release.tgz --exclude static/test-speech.wav \
    bin/ web/dist/ static/ fonts/ sound/ \
    index.html login.html admin.html meet.html meet_login.html

# 3. Ship + restart through the relay (ssh -p 2222 alexander@35.226.215.209;
#    `picloud` in ~/.ssh/config). The script backs up bin/, web/dist/, and the database,
#    restarts the units, health-checks them, and rolls back on failure.
scp -P 2222 release.tgz alexander@35.226.215.209:/home/alexander/alexmessage/
ssh -p 2222 alexander@35.226.215.209 \
    "APP_DIR=/home/alexander/alexmessage HEALTH_PORTS='6001 6002 6003' bash -s" \
    < deploy/remote_deploy.sh
rm release.tgz   # the deploy script deletes the Pi's copy; git does not ignore this one
```

> **Stale-cache gotcha:** asset URLs are stable (`/static/app.js`) and the
> server sends no `Cache-Control` on them, so browsers (Safari especially) keep
> serving the old frontend after a deploy. Tell testers to hard-refresh, or add
> cache-busting before relying on a frontend change being live.

> **A bad mobile-app update (OTA):** see **Mobile app updates → Undo an
> update** at the end of this file.

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
| `WS_ALLOWED_ORIGINS`                  | *(empty; same-origin only)* | server, meet |
| `STUN_URLS`                           | Google's public STUN         | server    |
| `TURN_URLS` / `TURN_SECRET`           | *(empty; no TURN)*           | server    |
| `TURN_USERNAME` / `TURN_CREDENTIAL`   | *(empty)*                    | server    |

The seed admin is created only if no admin exists yet; the bootstrap is
idempotent and race-safe across the three processes.

## DM calls: STUN and TURN

DM voice and video calls send their media peer to peer (WebRTC). For every
call the server hands both devices a list of ICE servers
(`internal/webapp/ice.go`):

| Variable | Default | Meaning |
|----------|---------|---------|
| `STUN_URLS` | `stun:stun.l.google.com:19302,stun:stun1.l.google.com:19302` | comma-separated `stun:` URLs |
| `TURN_URLS` | *(empty: no TURN)* | comma-separated `turn:` / `turns:` URLs |
| `TURN_SECRET` | *(empty)* | coturn's `static-auth-secret`: each call gets a time-limited credential (TURN REST API), minted per user |
| `TURN_USERNAME` / `TURN_CREDENTIAL` | *(empty)* | static TURN credentials, used only when `TURN_SECRET` is unset |

With STUN only, a call's media connects when at least one of the two devices
can be reached directly, which covers most home Wi-Fi. It does **not**
connect between two devices that are both behind symmetric NATs (common on
mobile carriers and corporate networks): the call rings and is answered,
then ends after 30 s, and both the call screen and the call card in the thread
say "Couldn't connect". A TURN relay fixes that.

Devices that run a VPN or proxy app which takes the default route (for
example Quantumult X) need TURN **over TCP or TLS**: such an app often drops
the UDP that peer-to-peer media uses, and the call then fails the same way
even on one Wi-Fi network. Keep the `?transport=tcp` and `turns:` URLs in
`TURN_URLS` below. A `turns:` listener on TCP 443 also gets through networks
that allow nothing else. Seen in development: the iOS Simulator on a Mac
running Quantumult X offered only the tunnel's address, its UDP was dropped,
and its TCP to a TURN server on the LAN never connected. A TURN server on a
public host reached through such a tunnel was not tested.

TURN needs a host with a public IP address and open UDP ports. The Pi has no
public address (the frp relay forwards only TCP 80, 443, and 2222), so the
TURN server must run elsewhere. A minimal coturn configuration on a public
host (replace `<turn-host>` and `<secret>`):

```ini
# /etc/turnserver.conf
listening-port=3478
tls-listening-port=5349      # turns: also needs cert= and pkey=
realm=<turn-host>
use-auth-secret
static-auth-secret=<secret>
min-port=49160
max-port=49200
no-multicast-peers
# Never relay into private networks.
denied-peer-ip=10.0.0.0-10.255.255.255
denied-peer-ip=172.16.0.0-172.31.255.255
denied-peer-ip=192.168.0.0-192.168.255.255
```

Open TCP+UDP 3478, TCP 5349, and UDP 49160–49200 on that host. Then add to
`/etc/alexmessage/alexmessage.env` on the Pi and restart the `alexmessage`
unit:

```bash
TURN_URLS=turn:<turn-host>:3478?transport=udp,turn:<turn-host>:3478?transport=tcp,turns:<turn-host>:5349
TURN_SECRET=<secret>
```

The secret belongs only in those two files — never in the repo.

**Mobile builds:** calls in the mobile apps need **new native builds**
(`react-native-webrtc`, `react-native-incall-manager`, and the local modules
`mobile/modules/system-calls` (iOS) and `mobile/modules/call-ringer`
(Android) are native code; `cd mobile && npx expo prebuild`, then build). Mobile builds made before
calls existed ignore call events — a caller still sees "Ringing…" while such
a build is connected, until the call times out as unanswered — and show
finished calls as plain-text messages ("Missed video call").

**Android ring service:** an Android phone with **Ring when the app is
closed** on (the default) keeps a second WebSocket open to the server,
`/ws?mode=calls` (the call listener), for as long as its user stays signed
in. It uses the `/ws` path, so the nginx `/ws` location already proxies it:
no new location is needed. The server pings it every 30 s, which keeps it
inside nginx's default 60 s `proxy_read_timeout`; the app's own `/ws` socket
gets a WebSocket ping every 25 s. Don't set `proxy_read_timeout` below about
35 s on the `/ws` location.

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
  `text/event-stream` for the admin debug stream. Preserve the browser Host
  and HTTPS scheme with `proxy_set_header Host $http_host;` and
  `proxy_set_header X-Forwarded-Proto $scheme;`. The backends trust HTTPS
  forwarding only from loopback. Extra origins must be exact entries in
  `WS_ALLOWED_ORIGINS`. The mobile apps' sockets send an `Origin` built from
  their server URL (React Native's WebSocket adds it on iOS and Android),
  which passes the same-origin check only while nginx keeps the `Host`
  header as above. A socket without an `Origin` header is accepted; only the
  Android ring service's call listener (OkHttp) sends none.
- **systemd units** (`/etc/systemd/system/`), all `User=alexander`,
  `WorkingDirectory=/home/alexander/alexmessage`, secrets in
  `EnvironmentFile=/etc/alexmessage/alexmessage.env` (`ADMIN_PASSWORD`,
  `VOLC_RTC_APP_ID`/`VOLC_RTC_APP_KEY`; add `OPENROUTER_API_KEY` there for
  voice transcription, and the `TURN_*` variables for call relays — see
  **DM calls: STUN and TURN**):

  | Unit | Binary | Port | Domain |
  |------|--------|------|--------|
  | `alexmessage-admin` | `bin/admin` | `6001` | `admin.alexanderjia.com` |
  | `alexmessage` | `bin/server` | `6002` | `messages.alexanderjia.com` |
  | `alexmessage-voice` | `bin/meet` | `6003` | `meet.alexanderjia.com` |

  (Coolify on the same Pi was moved off these ports — its UI is on `6000`, its
  realtime on `16001`/`16002`; see `PiCloudMigration.md` in the app dir.)
- **SSH**: `ssh -p 2222 alexander@35.226.215.209` (the relay forwards raw TCP;
  the host key is the Pi's own). `alexander` has passwordless sudo.
- **Web client**: the `react` branch workflow builds and tests `web/dist`,
  passes that build to the deploy job as `react-web-dist`, and includes it in
  the release. The deploy script replaces the complete directory so stale
  hashed assets cannot survive. Releases missing `web/dist/index.html` abort.
- **Protected attachments need build 2 or later**: since the deploy of
  2026-10-28 the server sends an attachment only to a signed-in user (an
  anonymous request gets 401). Build 2 (version 1.2.0) sends its session
  token with media requests; mobile builds made before the attachment
  authorization change (commit `37e425d`) request media anonymously and get
  401.

## Continuous deployment (GitHub Actions)

`.github/workflows/deploy.yml`:

- **Pull requests** into `main`/`react` run the tests only:
  `go build`/`vet`/`test`, deployment rollback tests, the `shared/` typecheck +
  Vitest suite, and a React web build plus Playwright browser tests.
- **Pushes** to `main` or `react` — including merging a PR — and
  manual *Run workflow* runs then **deploy**: cross-compile the three
  `linux/arm64` binaries, bundle them with `web/dist/ static/ fonts/ sound/` + the five
  HTML shells (**never `data/`**), `scp` the bundle to the Pi through the relay
  (port `2222`), and run `deploy/remote_deploy.sh` there. That script backs up
  `bin/` and `web/dist/`, and copies the database (with its `-wal` and `-shm` files) to `backups/` (keeping the newest 5) while the
  units are stopped, extracts the release, starts the units, health-checks
  ports `6001 6002 6003`, and **restores the binaries and React build** if any fails. A final
  step checks `https://messages.alexanderjia.com/api/me` end to end (relay →
  nginx → app).
- Each binary is built with `-X main.commit=$GITHUB_SHA` and logs
  `<server|admin|meet> built from commit <sha>` when it starts.
- `remote_deploy.sh` never writes into `data/`: it refuses a release that
  contains `data/`, so a deploy or a rollback leaves the database, the
  published app updates (`data/updates/`) and the app downloads
  (`data/app/`) unchanged.

The deploy job only runs when the repo variable **`DEPLOY_ENABLED` is `true`**.

`.github/workflows/mobile-checks.yml` (first pushed on 2026-10-28) runs on
every push and pull request to `main` or
`react`: `cd shared && npm ci --ignore-scripts && npm run typecheck && npm test` and
`cd mobile && npm ci --ignore-scripts && npx --no -- tsc --noEmit`. It uses
no secrets and deploys nothing.

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

## Mobile app updates

The mobile app downloads new JavaScript from the server by itself
(expo-updates, unsigned; CLAUDE.md → **OTA updates**). **Status
(2026-10-28): the server side is deployed, and build 2 is on `/app`.** A
phone gets updates only once build 2 is installed on it (**First release**,
step 3, below). No update is published yet. CLAUDE.md → **OTA updates** has
the test results.

Both scripts run on the Mac, from any directory, and reach the Pi over SSH
(`PI` and `PI_PORT` override `alexander@35.226.215.209`, port `2222`).

### Publish an update (JavaScript and images only)

```bash
mobile/scripts/publish-update.sh
```

It runs `npx expo export` for Android and iOS and copies the result into
`~/alexmessage/data/updates/<app version>/<unix seconds>/` on the Pi. The
server needs no restart. Every phone whose app has that version
(`mobile/app.json` → `expo.version`) downloads the update at its next start,
or when the app comes back after 15 minutes in the background, and runs it
from the start after that, or from the next such return. The app never
reloads during a call.

An update must not need native code that the installed app lacks. After a
native change (a new native module or config plugin, a native setting in
`mobile/app.json`, any change under `mobile/modules/`), publish a new app
build instead (below).

### Undo an update

A phone only takes an update that is newer than the one it runs, so
deleting an update directory on the Pi does not undo it on phones that
already have it. Publish the old code again instead:

```bash
git revert <the bad commit>
mobile/scripts/publish-update.sh
```

### Publish a new app build (native code)

1. Raise the version in `mobile/app.json`: native build N is
   `expo.version` `1.N.0`, `android.versionCode` N and `ios.buildNumber`
   `"N"` (build 2 is 1.2.0). Commit it.
2. Build and upload:

   ```bash
   mobile/scripts/publish-app.sh android
   mobile/scripts/publish-app.sh ios
   ```

   Each runs `npx expo prebuild --clean`, a release build (`gradlew
   assembleRelease`; an unsigned `xcodebuild archive` packed into an IPA)
   and copies `AlexMessages.apk` or `AlexMessages.ipa` into
   `~/alexmessage/data/app/` on the Pi. The APK is signed with the debug
   keystore of Expo's Android template, the same file after every prebuild,
   so it installs over the old app and keeps its data.
3. Everyone opens https://messages.alexanderjia.com/app on the phone and
   installs it. Android: the APK installs over the old app. iPhone: open
   the IPA in 万能签, which signs and installs it.

Updates published before step 1 reach only the old version; the new build
gets the updates published after it.

### What the server serves (no login)

| Route | Answer |
|-------|--------|
| `GET /api/ota/manifest` | the newest update for the phone's platform and app version; `204` when there is none |
| `GET /ota/files/<version>/<unix seconds>/…` | the files of an update, from `data/updates/` |
| `GET /app` | the install page |
| `GET, HEAD /app/AlexMessages.apk`, `/app/AlexMessages.ipa` | the files in `data/app/`, with `Cache-Control: no-store` (Cloudflare caches `.apk` by extension, and the names never change) |

Deploys never touch `data/updates/` or `data/app/`.

### First release

Steps 1 and 2 were done on 2026-10-28 (server commit `3fa5bc3`). Step 3 is
not done yet.

1. Push the `react` branch and deploy the server (see **TL;DR for
   agents**). The server then answers `/api/ota/manifest` (`204` until an
   update is published) and serves `/app`.
2. Run `mobile/scripts/publish-app.sh android` and
   `mobile/scripts/publish-app.sh ios` (build 2, version 1.2.0).
3. Everyone installs build 2 once from https://messages.alexanderjia.com/app.
4. From then on, `mobile/scripts/publish-update.sh` updates their apps.
