# PiCloud Migration Note

Date: 2026-06-25

Target host: `picloud` (`10.50.130.148`)

## Summary

Alex Messages, Alex Platform Admin panel, and Alex Meet were migrated to the new
Pi server and run from `/home/alexander/alexmessage`.

Requested ports:

| Port | Service |
| ---- | ------- |
| 6001 | Alex Platform Admin Panel |
| 6002 | Alex Messages |
| 6003 | Alex Meet |

## Data And Environment

- Migrated `/home/me/alexmessage/data` from the old production server to
  `/home/alexander/alexmessage/data` on the Pi.
- The old production units were stopped during the `data/` tar stream so the
  SQLite database, server-side sessions, uploads, avatars, and VAPID files were
  copied consistently, then restarted afterward.
- Migrated runtime secrets into `/etc/alexmessage/alexmessage.env` on the Pi:
  `ADMIN_PASSWORD`, `VOLC_RTC_APP_ID`, and `VOLC_RTC_APP_KEY`.
- Secret values were not recorded in this note.

## Coolify Port Move

Coolify's realtime container was already occupying host ports `6001` and
`6002`. With explicit approval, its external host bindings were moved to:

| Old host port | New host port | Container port |
| ------------- | ------------- | -------------- |
| 6001 | 16001 | 6001 |
| 6002 | 16002 | 6002 |

The internal container ports were left unchanged.

Backups were created before editing:

- `/data/coolify/source/.env.alexmessages-port-backup-20260624203842`
- `/data/coolify/source/docker-compose.prod.yml.alexmessages-port-backup-20260624203842`

Applied Coolify changes:

- Set `SOKETI_PORT=16001` in `/data/coolify/source/.env`.
- Changed the `docker-compose.prod.yml` host mapping from `6002:6002` to
  `16002:6002`.
- Restarted only the `soketi` service via Docker Compose.

## Coolify Main Port

With explicit approval, Coolify's main UI host port was moved to `6000`.

Backups and installer artifacts:

- `/data/coolify/source/.env.alexmessages-app-port-backup-20260624204614`
- `/data/coolify/source/.env-20260624-204629`
- `/data/coolify/source/docker-compose.prod.yml.alexmessages-post-install-port-backup-20260624204854`
- `/data/coolify/source/upgrade-2026-06-24-20-46-38.log`

Applied Coolify changes:

- Set `APP_PORT=6000` in `/data/coolify/source/.env`.
- Ran the requested installer command from `/data/coolify/source`:

  ```bash
  curl -fsSL https://cdn.coollabs.io/coolify/install.sh | sudo bash
  ```

- The installer refreshed `docker-compose.prod.yml`, which reverted the
  hard-coded realtime host mapping to `6002:6002`. Because Alex Messages was
  already correctly bound to `6002`, `coolify-realtime` failed to start during
  that installer pass. The approved realtime mapping was then reapplied:
  `16002:6002`, followed by `docker compose up -d`.

Final Coolify host ports:

| Host port | Service |
| --------- | ------- |
| 6000 | Coolify main UI |
| 16001 | Coolify realtime -> container `6001` |
| 16002 | Coolify realtime -> container `6002` |

## Systemd Units

Installed and enabled:

- `alexmessage.service` -> `0.0.0.0:6002`
- `alexmessage-admin.service` -> `0.0.0.0:6001`
- `alexmessage-voice.service` -> `0.0.0.0:6003`

All three services use:

- `WorkingDirectory=/home/alexander/alexmessage`
- `EnvironmentFile=-/etc/alexmessage/alexmessage.env`

## Verification

Local Pi health checks:

- `http://127.0.0.1:6000/api/health` -> `200`
- `http://127.0.0.1:6001/api/me` -> `401`
- `http://127.0.0.1:6002/api/me` -> `401`
- `http://127.0.0.1:6003/api/me` -> `401`
- `http://127.0.0.1:16001/ready` -> `200`
- `http://127.0.0.1:16002/ready` -> `200`

LAN checks:

- `http://10.50.130.148:6000/api/health` -> `200`
- `http://10.50.130.148:6001/api/me` -> `401`
- `http://10.50.130.148:6002/api/me` -> `401`
- `http://10.50.130.148:6003/api/me` -> `401`
- `http://10.50.130.148:16001/ready` -> `200`
- `http://10.50.130.148:16002/ready` -> `200`
