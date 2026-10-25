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
#    binary logs at start ("server built from commit <sha>"). otactl manages
#    the OTA update store data/ota on the Pi (see OTA updates below).
mkdir -p bin
commit=$(git rev-parse HEAD)
for c in server admin meet otactl; do
  GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.commit=$commit" -o bin/$c ./cmd/$c
done

# 2. Bundle binaries + assets (NOT data/ — it lives on the server).
tar czf release.tgz --exclude static/test-speech.wav bin/ web/dist/ static/ fonts/ sound/ \
    index.html login.html admin.html meet.html meet_login.html

# 3. Ship + restart through the relay (ssh -p 2222 alexander@35.226.215.209;
#    `picloud` in ~/.ssh/config). The script backs up bin/, web/dist/, and the database,
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

> **A bad mobile-app update (OTA):** see **OTA updates of the mobile app →
> Emergency: stop or roll back a bad update** at the end of this file. OTA
> updates do NOT work yet.

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
| `OTA_DIR`                             | `data/ota`                   | server, admin |
| `OTA_DISABLED`                        | *(unset)*; `1` = every OTA manifest request gets `204` | server |

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
- **Updated mobile clients are required for protected attachments**: publish
  native builds containing the bearer media headers and the updated iOS
  `ImagePreview` module before deploying the attachment authorization change.
  Older mobile builds request media anonymously and will receive 401.

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
  contains `data/`, so a deploy or a rollback leaves the database and the OTA
  update store `data/ota/` unchanged. The workflow also cross-compiles
  `bin/otactl` (a local change on `react`, not pushed; no deploy has shipped
  `bin/otactl` yet). The script installs it by rename (`install -m 755` to
  `bin/otactl.tmp`, then `mv` over `bin/otactl`), so an `otactl` that runs
  during the deploy keeps its file.

The deploy job only runs when the repo variable **`DEPLOY_ENABLED` is `true`**.

`.github/workflows/mobile-checks.yml` (a local commit on `react`, not pushed;
it has never run on GitHub) runs on every push and pull request to `main` or
`react`: the two OTA guard tests (`TestServerBinariesNeverImportSigning`:
no binary deployed to the Pi imports the signing code or age;
`TestNoPrivateKeysTracked`: no tracked file holds a PEM private key or ends
in `.key`, `.p12`, `.pfx`, `.jks` or `.keystore`), then
`cd shared && npm ci --ignore-scripts && npm run typecheck && npm test` and
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

## OTA updates of the mobile app

Design, reasons and the full build plan: `OTAUpdatePlan.md`.

**Status (2026-10-25): OTA updates do NOT work yet.**

Done (in local commits on `react`; NOT pushed, NOT deployed):
- `cmd/server` serves the update endpoints, the `/app` install page, the
  native-build policy and the install census described below.
- `cmd/admin` serves the census at `GET /api/ota/installs`.
- `remote_deploy.sh` refuses a release that contains `data/` and installs
  `bin/otactl` by rename.
- The tools are built (Phase 2 of the plan): `cmd/otactl` (runs on the Pi)
  and `cmd/otapublish` (runs on your Mac), described under **The tools**
  below. Their tests run every command against a temporary directory that
  stands in for the Pi; no tool has run against the real Pi.
- `deploy.yml` cross-compiles `bin/otactl`; `mobile-checks.yml` exists
  (see **Continuous deployment**). Neither workflow has run with these
  changes.

NOT done:
- The keys do not exist yet (Phase 3). **Every `otapublish` command that
  needs a key fails until then:** `keys issue-leaf`, `keys show`, `ota`
  without `--dry-run`, `republish`, `resign-active` and `native`.
- No app build contains expo-updates yet (Phase 4). Until then
  `otapublish ota` stops at step 7 of its pipeline (the runtimes:
  `expo-updates` is not installed), and `otapublish native` cannot build a
  release (the release commit, `mobile/certs/ota-root-2026.pem`,
  `mobile/release/android-signer.sha256` and
  `mobile/plugins/withReleaseSigning.js` do not exist yet). Phones get OTA
  updates only from build 2 on (Phases 4–6).
- Production runs none of this code. `bin/otactl` is not on the Pi and
  `data/ota/` does not exist there: **every `bin/otactl` command in this
  section fails on the Pi until release day (Phase 6)** deploys it and
  `bin/otactl init` creates the store. Until then the manifest endpoint
  answers `204` (no update) to every request and `/app` says that no build
  is published.

### What the server serves

All on `https://messages.alexanderjia.com`, without login:

| URL | What | Cache-Control |
|-----|------|---------------|
| `GET /api/ota/manifest` | Expo Updates protocol v1 manifest endpoint; this URL is baked into every app build from build 2 on | `private, max-age=0, no-store` |
| `GET, HEAD /ota/assets/<sha256-b64url>.<ext>` | update files (the JS bundle as `.js`) | `public, max-age=31536000, immutable` |
| `GET /api/app/latest` | native-build policy of both platforms (`data/ota/native/policy.json`) | `no-cache` |
| `GET /app` | install page: download links, SHA-256 sums, Android and iPhone steps | `no-cache` |
| `GET, HEAD /app/android/AlexMessages-<version>-<build>.apk` | APK | `public, max-age=31536000, immutable` |
| `GET, HEAD /app/ios/AlexMessages-<version>-<build>.ipa` | unsigned IPA (install it through 万能签) | `public, max-age=31536000, immutable` |

- 400 and 404 answers carry `Cache-Control: no-store`, so Cloudflare (which
  caches by file extension, 404 answers included) never keeps a 404 for a
  file that is uploaded later.
- File answers carry `X-Content-Type-Options: nosniff`,
  `Content-Security-Policy: sandbox` and `Content-Disposition: attachment`.
- The manifest endpoint answers `400` for a missing or invalid
  `expo-protocol-version`, `expo-platform` or `expo-runtime-version`, and
  `500` when a file in `data/ota/` is damaged or cannot be read (debug-console
  event `ota.store_error`, at most once per 10 minutes). A missing
  `data/ota/` or a missing `data/ota/FORMAT` gives `204`, not `500`.
- The server holds no signing key. It serves the bytes that `otapublish`
  signed on the Mac.

**The install census:** every manifest request from an install whose
embedded update id belongs to a registered native build updates that
install's row in `data/ota/census.db` (at most 5,000 rows; the maintenance
sweep deletes rows unseen for 90 days). Every field is an unauthenticated
claim of the phone. Read it at `https://admin.alexanderjia.com/api/ota/installs`
(admin login; JSON), or with `bin/otactl census` over SSH (once release day
has put `bin/otactl` on the Pi).
Deleting an account deletes the rows whose `am_uid` is that account.
`backups/` does not hold the census; phones refill it at their next check.

**Which commit runs:** each binary logs
`<server|admin|meet> built from commit <sha>` when it starts. With the
systemd default (output to the journal):
`sudo journalctl -u alexmessage | grep 'built from commit' | tail -n 1`.
`bin/otactl version` prints the commit of `bin/server`, `bin/admin`,
`bin/meet` and itself (it reads `vcs.revision`: `-trimpath` leaves
`-ldflags` out of the build info).

### Storage: `data/ota/`

`/home/alexander/alexmessage/data/ota/` on the Pi. `bin/otactl init`
creates it; after that only `otactl` writes into it. Server deploys and
server rollbacks never touch it.

**Leave `OTA_DIR` unset on the Pi.** The server units read it from their
environment file, but `bin/otactl` over SSH (and the emergency key's forced
command) never sees that file: it finds the store as `../data/ota` next to
its own executable (`~/alexmessage/bin/otactl` → `~/alexmessage/data/ota`).
`--ota <dir>` or `OTA_DIR` in the same shell overrides that, for local
testing. With `OTA_DIR` set in the environment file, the server would read
one store and `otactl` would write another.

```
data/ota/
  FORMAT                                    "1"; without it the server treats the store as absent (204 to every phone)
  certs/ota-root-2026.pem, leaf-*.pem       public certificates only
  assets/<sha256-b64url>.<ext>              write-once, never deleted
  updates/<id>/manifest.json, rollback.json the exact signed bytes
              manifest.sig, rollback.sig    the expo-signature value
              manifest.leaf, rollback.leaf  lowercase hex SHA-256 of the DER leaf certificate that signed it
              info.json                     {"platform","runtime","label","commit","created_at","notes","critical"}
  runtimes/<platform>/<runtime>.json        {"platform","runtime","builds":[{"build","version","embedded_update_id",
                                              "commit_time","commit","registered_at"}]}
  channels/<production|beta>/<platform>/<runtime>.json
                                            {"state":"update"|"rollback"|"halted","update":"<id>","since":"<RFC 3339>"}
                                            a beta pointer in state "test_unsigned" also has "until" and "previous"
  test/<id>/manifest.json, info.json        the unsigned manifest of otactl test-unsigned (plan step 6.4)
  revoked.txt                               one update id per line; "#" starts a comment
  native/policy.json                        the minimum-client gate (below)
  native/android/*.apk, native/ios/*.ipa
  census.db                                 the install census
  incoming/<publishId>/                     uploads that otactl ingest has not moved in yet
  log/actions.log                           one JSON line per otactl command that changed the store
  .lock                                     held by every otactl command that changes the store
```

An id in `revoked.txt` is never served as an update. A pointer in state
`rollback` still serves the rollback directive of a revoked update: that
directive is how phones leave it.

### Emergency: stop or roll back a bad update

From `OTAUpdatePlan.md`, D9. **These commands fail today:** `bin/otactl`
is built, but no deploy has put it on the Pi, and production has no OTA
pointers. They work from release day (Phase 6) on.

No key is needed. Use your normal SSH key from the Mac, or the emergency key
(`~/.ssh/am-emergency`, plan step 3.4) that the Pi restricts to
`bin/otactl emergency`, so a phone with an SSH app never carries a
root-equivalent key. Each command prints every pointer it changed.

1. **Stop:** `ssh -p 2222 alexander@35.226.215.209 'cd ~/alexmessage && bin/otactl halt --all'`
   sets every pointer (every channel, platform and runtime) to `halted`.
   Targeted: `bin/otactl halt --label <label>` halts every pointer (production
   and beta, both platforms) that references that label's updates. Phones
   that have not downloaded the bad update stop getting it at once. With the
   emergency key: `ssh -p 2222 -i <emergency key> alexander@35.226.215.209 halt`.
2. **Roll back:** `ssh -p 2222 alexander@35.226.215.209 'cd ~/alexmessage && bin/otactl rollback-embedded --label <label>'`.
   Every pointer that references the label's updates (production and beta,
   both platforms) switches to that update's pre-signed `rollBackToEmbedded`
   directive (its `commitTime` is the bad update's `createdAt` + 1 s, so it
   applies to that update and to anything older). The bad ids go into
   `revoked.txt`, and the endpoint never serves them again. With the
   emergency key: `… rollback-embedded` (acts on every pointer that is in
   state `update` or `halted`). Both forms print the native builds in the
   census and wait until you type `yes` and press Enter.
3. Phones apply the rollback after they have downloaded it: at the next
   resume after 15 minutes, at Restart to Update, or at the second cold start
   after the directive is live. `bin/otactl status` counts the installs still
   reporting the bad id.
4. If the bad update broke `UpdateHost` itself, tell the affected users:
   "Open the app and keep it open for 30 seconds on Wi-Fi or mobile data.
   Close it completely (iPhone: swipe it away; Android: Settings → Apps →
   Alex Messages → Force stop). Open it again." The first open downloads the
   rollback; the second open runs it. `bin/otactl status` shows the phone on
   its embedded id after the second open.

**Fix forward, at the Mac:**

5. `go run ./cmd/otapublish republish <last good label>` re-signs the last
   good content from the Mac archive as a new id with a new `createdAt`.
   Assets are reused by hash, so phones download only the manifest. Or
   publish a fix normally.
6. Do this promptly. Any new update replaces the rollback pointer, because
   its `createdAt` is later than the directive's `commitTime`, and it also
   moves every phone past the bad update, which ends the replay exposure
   (plan D4).

**Rule:** `rollback-embedded` is allowed only while the server still works
with the embedded JS of every runtime in the census, because that is the code
phones go back to. `otactl rollback-embedded` prints the native builds the
census shows and asks for `yes`.

### Kill switch, if `data/ota` itself is damaged

This one needs no `otactl` and works once the OTA server code is deployed:
add `OTA_DISABLED=1` to `/etc/alexmessage/alexmessage.env` and run
`sudo systemctl restart alexmessage`. The manifest endpoint then answers
`204` to every request, so every phone keeps the update it runs. **The
restart ends every live call** (calls are held in memory). To undo it, delete
the line and restart again.

### The tools: `bin/otactl` (Pi) and `cmd/otapublish` (Mac)

**`bin/otactl`** runs on the Pi. It holds no key and signs nothing: it
verifies what `otapublish` signed on the Mac, moves uploads into the store
and switches the pointers. It is the only program that writes into
`data/ota/`. Every command that changes the store holds `data/ota/.lock` and
appends one line to `data/ota/log/actions.log`. Every command except `init`
and `version` exits with status 3 while the store has no `FORMAT` file (before
`bin/otactl init`); `otapublish` reads only that status as "no update store
yet", and any other failure as an error. Run it over SSH:
`ssh -p 2222 alexander@35.226.215.209 'cd ~/alexmessage && bin/otactl <command>'`.

| Command | What it does |
|---------|--------------|
| `init` | creates `data/ota/` (writes `FORMAT` last); safe to run again |
| `version [--json]` | the commit of `otactl`, `bin/server`, `bin/admin` and `bin/meet` |
| `status [--json]` | pointers, revoked ids and a census summary |
| `census [--json]` | every install in the census, and every approved user's app sessions |
| `runtimes [--json]` | the registered native builds per runtime |
| `doctor [--json]` | checks the store and exits 1 on any error: a certificate that does not chain to the root; a pointer whose update is missing, damaged, for another platform or runtime, or signed by a leaf that expires within 30 days; an invalid `policy.json`; an APK or IPA named in the policy whose SHA-256 or size differs from the policy; an unreadable census. It warns about a missing root certificate, unregistered runtimes, pointers at revoked updates, `test_unsigned` pointers, leftover uploads in `incoming/` and less than 1 GB free |
| `inventory [--json]` | every stored file with its SHA-256, for `otapublish audit` |
| `halt (--all \| --label <label>)` | stop serving (see **Emergency**) |
| `rollback-embedded (--label <label> \| --all-active) [--yes]` | send phones back to their built-in JS and revoke the updates (see **Emergency**) |
| `revoke <id>… [--note <text>]` | never activate or serve these updates again |
| `policy [--platform <p> [--min-build <n>] [--hard-gate=<bool>] [--latest-build <n>] [--notes <text>]] [--yes]` | show or change `native/policy.json` (below) |
| `test-unsigned --channel beta [--minutes 10] [--platform <p>]`, `test-unsigned --clear` | plan step 6.4: beta phones get an unsigned copy of their built-in update, which they must refuse |
| `repair-asset --asset <name> < file` | replaces `assets/<name>` only with bytes that hash to `<name>` |
| `emergency` | the emergency key's forced command: one word, `halt`, `rollback-embedded` or `status` |
| `ingest`, `activate`, `promote`, `restore-pointer`, `register-runtime` | `otapublish` runs these over SSH; you never need to |

**`cmd/otapublish`** runs only on your Mac, from the repo root, as
`go run ./cmd/otapublish <command>`. It is never deployed, and it is the only
program that ever holds a signing key. Every command that touches a key, and
every publish, checks the commit out into a clean git worktree, builds
`otapublish` there and runs that build, so an edited checkout never touches a
key.

**Who runs what:** Claude never runs these commands through its own
command tool: every `keys` command, `ota` without `--dry-run`, `republish`,
`resign-active` and `native`. Claude starts them in your Terminal panel, and
you type the passphrase or password there when one is asked. Claude runs
`ota --dry-run`, `status` and `audit` itself. `audit --record` and
`restore-pi` wait for you to type `yes`.

| Command | What it does |
|---------|--------------|
| `keys init-root --id root-2026 --out /Volumes/<stick> --cert-out mobile/certs/ota-root-2026.pem` | the root key (plan step 3.1); `--out` must be a folder under `/Volumes/` on another disk than your home folder (the USB stick) |
| `keys issue-leaf --root /Volumes/<stick>/ota-root-2026.key.age --out ~/.alexmessages/keys/ [--no-upload]` | a new signing leaf, certified by the root; then, unless `--no-upload`, asks the Pi over SSH whether it has an update store and copies the certificates there if it has one (before `bin/otactl init`, plan step 6.3, it prints "Not uploaded" and succeeds) |
| `keys upload-certs` | copies the root and every unexpired leaf certificate to the Pi's `data/ota/certs/` |
| `keys show (--root <key file> \| --leaf <key file>)` | decrypts a key file and prints its fingerprint and certificate |
| `ota --notes <text> [--channel beta\|production] [--critical] [--from <ref>] [--platform <p>]` | publishes the JS of the pushed commit as a signed update; the channel defaults to `beta` |
| `ota --dry-run --notes <text> [--allow-local] [--fixture <file>]` | steps 1–12 of the pipeline: no key, nothing uploaded; `--allow-local` accepts a commit that is not on origin; `--fixture` replaces steps with prepared data (`cmd/otapublish/testdata/dry-run.json`, for tests and for checking the tool before Phase 4) |
| `promote <label> [--force]` | points production at the beta updates of a label |
| `republish <label> [--channel production\|beta] [--notes <text>]` | re-signs archived content as a new update; the channel defaults to `production` |
| `resign-active` | re-signs with the newest leaf (see **Renew the leaf certificate**) |
| `status` | the Pi's pointers, revoked ids and census summary |
| `native --notes <text> [--no-upload [--allow-local]]`, `native --upload-only <artifact dir>` | builds, signs, checks and publishes a native build (see **Publish**) |
| `audit [--record]` | compares the Pi with the Mac archive (see **Audit**) |
| `restore-pi` | rebuilds the Pi's store from the Mac archive (see **Restore after a lost SD card**) |

Flags every command takes: `--archive <dir>` (default `$AM_RELEASES_DIR`,
else `~/AlexMessagesReleases`), `--keys-dir <dir>` (default
`~/.alexmessages/keys`), `--pi <user@host>` and `--pi-port <n>` (default
`alexander@35.226.215.209` and `2222`), `--pi-dir <dir>` (a local update
store instead of the Pi, for dry runs and the drill; with `--pi-data` and
`--otactl`), `--public-url <url>` (with `--pi-dir` only: the server that
phones talk to) and `--profile drill` (Phase 5: throwaway keys in
`~/.alexmessages/drill-keys`, the archive `<archive>/drill`, a local store
through `--pi-dir`, no git tags).

**The Mac archive** is authoritative for the files that never change once
written (certificates, assets, updates, runtimes, native builds). The Pi is
authoritative for the pointers, `revoked.txt` and `policy.json`.

```
~/AlexMessagesReleases/
  ota/                                   the Mac's copy of data/ota
    certs/, assets/, updates/<id>/       every certificate, asset and signed update otapublish made
    runtimes/<platform>/<runtime>.json   the registered builds
    native/<platform>/<file>             every APK and IPA that otapublish native uploaded
    native/policy.json                   the policy after the last native upload or audit --record
    pointers.log                         one JSON line per pointer change otapublish made or audit --record recorded
    revoked.txt                          the revoked ids that audit --record recorded
  builds/native-<N>-<UTC time>/          otapublish native's artifact directory: build.json, the APK and the IPA
  drill/                                 the drill's archive (the same layout)
```

### Publish (works only after Phases 3–6)

From section 6 of `OTAUpdatePlan.md`. Claude starts every command that needs
a key in your Terminal panel, and you type the passphrase there.

**Ship a JS fix:**
1. If the fix needs a server change, deploy the server first, by hand: set
   the repo variable `DEPLOY_ENABLED` to `true`, run Actions → Deploy → Run
   workflow, and set `DEPLOY_ENABLED` back to `false` when the run ends
   (pushes never deploy). The server must keep working for phones still on
   older updates.
2. Commit and push.
3. `go run ./cmd/otapublish ota --notes "Fixes the send button on iPad"`
   (you type the leaf passphrase). It publishes to beta and prints the label.
4. On your phone: Settings → Software Update → Check for Updates → Restart
   to Update; try the fix.
5. On each beta phone (iOS and Android): Check for Updates once more, so the
   server sees the running update.
6. `go run ./cmd/otapublish promote <label>`.
7. Next day: `bin/otactl census` (or the admin page) shows every phone on the
   label and no failure.

**Ship a native change:**
1. Claude makes the release commit `release: native N` (`app.json`: version
   `1.N.0`, `ios.buildNumber` `"N"`, `android.versionCode` N) and pushes
   after your go-ahead.
2. `go run ./cmd/otapublish native --notes "…"`. It refuses unless N is
   the next build number (the highest build registered on the Pi + 1; 2
   when none is registered). It builds both platforms from a clean worktree
   of the pushed commit (`expo prebuild`, the unsigned Gradle release build,
   `pod install` and an unsigned `xcodebuild archive` packed into an IPA),
   then `apksigner` asks for the keystore password (you type it). It runs
   every artifact check, saves the artifact directory
   `~/AlexMessagesReleases/builds/native-N-<UTC time>/` (`build.json`, the
   APK and the IPA), uploads both files, registers the runtimes, updates
   `policy.json`, checks both downloads through `/api/app/latest`, and tags
   `native-N` only after the upload succeeded.
3. To build without uploading (plan step 5.3):
   `go run ./cmd/otapublish native --notes "…" --no-upload` (add
   `--allow-local` for a commit that is not pushed). It prints the artifact
   directory. Once the commit is on origin,
   `go run ./cmd/otapublish native --upload-only <artifact directory>`
   checks the files again and uploads them (no password: they are already
   signed).

**Backport a JS fix to phones on an older runtime:**
`git switch -c release/native-N native-N`, cherry-pick the fix, push
`release/native-N` (deploy.yml does not run on that branch), then
`otapublish ota --from release/native-N`.

**Upgrade Expo SDK, React Native or expo-updates:** always a native release
(a new runtime on both platforms). Before it ships, re-run drill scenarios a,
b, d, e, n, o and the rollback part of c (plan D12) on the new versions, and
check that `fingerprint.config.js` still parses
(`npx @expo/fingerprint fingerprint:generate` lists the skips).

**Numbering:** native build N, one counter for both platforms; version
`1.N.0`; OTA label `YYYYMMDD.k`; git tags `native-N` and `ota-<label>`.

### Audit: compare the Pi with the Mac archive

`go run ./cmd/otapublish audit` reads `bin/otactl inventory` from the Pi and
compares it with the archive. It prints every difference as
`DIFFERENCE: …`, history (an expired leaf, an update whose signature no
longer verifies) as `note: …`, and exits 1 when anything differs. It changes
nothing.

- **Before you install an APK or IPA from `/app`, run `audit`.** The
  download is not authenticated (plan D10); a file on the Pi whose SHA-256
  differs from the archive is reported with "do NOT install this file from
  /app".
- After a command run on the Pi itself (`halt`, `rollback-embedded`,
  `revoke`, `policy`, `test-unsigned`), `audit` reports the pointers,
  revoked ids or policy it changed. If you made those changes,
  `go run ./cmd/otapublish audit --record` (type `yes`) stores them as the
  Mac's record (`pointers.log`, `ota/revoked.txt`, `ota/native/policy.json`
  in the archive), so a restore rebuilds them. It never records a loss and
  never changes the write-once files.

### Restore after a lost SD card

1. You: reinstall the Pi and deploy the server (the deploy puts
   `bin/otactl` there).
2. You or Claude: `ssh -p 2222 alexander@35.226.215.209 'cd ~/alexmessage && bin/otactl init'`.
3. `go run ./cmd/otapublish restore-pi` (type `yes` after it prints its
   plan). It uploads every write-once file the Pi lacks, registers the
   archived native builds, revokes the recorded ids, writes the policy if
   the Pi has none, writes every recorded pointer the Pi lacks, and runs
   `otactl doctor`. It changes nothing the Pi already has, and it refuses
   when the Pi holds a write-once file with other bytes than the archive.
   An update signed by an expired leaf cannot be restored (its signatures
   no longer verify): restore-pi skips it and the pointers that need it,
   and says so; publish or republish instead.
4. `go run ./cmd/otapublish audit` must print "No differences".

restore-pi restores what the Mac recorded: a halt, rollback or revoke made
on the Pi after the last `audit --record` is not restored.

### Keys: where they live

None of these exists yet (Phase 3 creates them). The Pi, the GCP relay,
Cloudflare and GitHub never hold a private key.

| Key or file | Where |
|-------------|-------|
| OTA root private key (RSA-4096) | `ota-root-2026.key.age`, encrypted with a passphrase (age), on one USB stick, plus one copy in your password manager; never on the Mac's disk |
| Root passphrase | in your head, and on paper stored with the USB stick (not in the password manager that holds the encrypted key) |
| OTA root certificate (public) | `mobile/certs/ota-root-2026.pem` (committed; embedded in every app build from build 2 on), and a copy at `data/ota/certs/ota-root-2026.pem` |
| OTA leaf private key (RSA-3072) | `~/.alexmessages/keys/ota-leaf-<yyyymm>.key.age` on the Mac (a second leaf in the same month: `ota-leaf-<yyyymm>-2.key.age`), encrypted (age) with a different passphrase |
| Leaf passphrase | in your head, and on paper stored with the USB stick |
| OTA leaf certificate (public) | `data/ota/certs/leaf-<yyyymm>.pem` on the Pi (sent with every manifest), `~/.alexmessages/keys/ota-leaf-<yyyymm>.pem` and `~/AlexMessagesReleases/ota/certs/leaf-<yyyymm>.pem` on the Mac |
| Android release keystore (PKCS12, RSA-4096) | `~/.alexmessages/keys/android-release.p12` on the Mac, a copy on the USB stick, and one in your password manager |
| Keystore password | on paper with the USB stick, and in your password manager; never in a file on the Mac |
| Android signer SHA-256 (public) | `mobile/release/android-signer.sha256` (committed) |
| Emergency SSH key (optional) | `~/.ssh/am-emergency` on the device you would use in an emergency; restricted on the Pi to `bin/otactl emergency` |
| Drill keys (throwaway) | `~/.alexmessages/drill-keys/` |
| Archive of everything published | `~/AlexMessagesReleases/` on the Mac (layout under **The tools**) |

### Renew the leaf certificate (every 12 months)

A leaf certificate is valid for 400 days. Renewing it needs no new app build.
These steps work only after Phase 3 (the keys) and release day (Phase 6:
`bin/otactl` and `data/ota` on the Pi).

1. Plug in the USB stick.
2. `go run ./cmd/otapublish keys issue-leaf --root /Volumes/<stick>/ota-root-2026.key.age --out ~/.alexmessages/keys/`
   (you type the root passphrase, then a new leaf passphrase; write the new
   one on paper and keep it with the stick). It also uploads the new leaf
   certificate to `data/ota/certs/` on the Pi.
3. Unplug the stick.
4. `go run ./cmd/otapublish resign-active` (you type the new leaf
   passphrase). It re-signs with the new leaf every update that any pointer
   references (production and beta, every platform and runtime) and the last
   3 updates per platform and runtime, both `manifest.json` and
   `rollback.json`. The signed bodies stay byte-identical; their `.sig` and
   `.leaf` files change.
5. `ssh -p 2222 alexander@35.226.215.209 'cd ~/alexmessage && bin/otactl doctor'`
   checks the leaf of every pointer-referenced manifest and rollback
   directive.

`otactl activate` and `otactl rollback-embedded` refuse a signature whose
leaf expires within 30 days. A lost leaf costs only a new leaf from the root.
A lost root costs a reinstall of your iPhone, and a new Android build
installed through the app's updater.

### The minimum-client gate (`data/ota/native/policy.json`)

`bin/otactl policy` writes it, and `otapublish native` sets `latest` after
it uploads a build. Without the file nothing is
gated and `/api/app/latest` lists no build. Format:

```json
{
  "android": {"min_build": 0, "hard_gate": false,
              "latest": {"version": "1.2.0", "build": 2,
                         "url": "https://messages.alexanderjia.com/app/android/AlexMessages-1.2.0-2.apk",
                         "sha256": "<64 lowercase hex characters>", "size": 51234567, "notes": "…"}},
  "ios":     {"min_build": 0, "hard_gate": false, "latest": null}
}
```

- The gate acts only on app builds that send the `X-AM-Client` header
  (build 2 and later). The web client and the builds before 2 are never
  gated.
- **Soft gate:** the WebSocket `init` of such a build carries
  `client_policy {state: ok | update_available | update_required, latest}`.
- **Hard gate:** only while `hard_gate` is `true` for the platform, and only
  for a build below `min_build`. REST calls under `/api/` answer `426`
  (except `/api/app/`, `/api/ota/`, `/api/debug/report` and `/api/logout`),
  and `/ws` and the call listener close with code `4426`.
- A policy file that cannot be read or is invalid gates nobody (debug-console
  event `ota.store_error`).
