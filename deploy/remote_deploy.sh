#!/usr/bin/env bash
#
# Runs ON the production host (the Raspberry Pi "picloud") — piped in over SSH
# by .github/workflows/deploy.yml (`ssh … bash -s < deploy/remote_deploy.sh`).
# The same command works by hand; see DEPLOY.md → "Deploy by hand".
#
# Inputs (exported before `bash -s`):
#   APP_DIR       app root, already holds the freshly copied release.tgz
#   HEALTH_PORTS  space-separated localhost ports to probe after the restart
#                 (must match the systemd units' ADMIN_PORT/PORT/CALL_PORT)
#
# It backs up the current binaries and the SQLite database, extracts the new
# release over the old tree, restarts the three systemd units, and smoke-tests
# each port. If any unit fails to answer, it restores the previous binaries
# and exits non-zero so the GitHub Actions run is marked failed.
#
# The database is copied while the units are stopped: the Pi has no sqlite3
# for an online `.backup`, and a plain copy of a live WAL database can be torn.
# The stop → copy → start window is a few seconds, about what a restart costs.
#
# Prerequisite: the SSH user can run `sudo -n systemctl stop|start|restart`
# for the three units without a password.

set -euo pipefail

APP_DIR="${APP_DIR:?APP_DIR not set}"
HEALTH_PORTS="${HEALTH_PORTS:-6001 6002 6003}"
UNITS=(alexmessage alexmessage-admin alexmessage-voice)
KEEP_DB_BACKUPS=5

cd "$APP_DIR"

if [[ ! -f release.tgz ]]; then
  echo "::error::release.tgz not found in $APP_DIR" >&2
  exit 1
fi

echo ">> Backing up current binaries"
rm -rf bin.prev
if [[ -d bin ]]; then cp -a bin bin.prev; fi

echo ">> Stopping: ${UNITS[*]}"
sudo -n systemctl stop "${UNITS[@]}"

# From here on, make sure the services come back even if a step fails.
started=0
on_exit() {
  if [[ "$started" != "1" ]]; then
    echo "::warning::Deploy aborted before start — restarting the previous release"
    if [[ -d bin.prev ]]; then rm -rf bin; mv bin.prev bin; fi
    sudo -n systemctl start "${UNITS[@]}" || true
  fi
}
trap on_exit EXIT

if [[ -f data/alexmessage.db ]]; then
  ts="$(date +%Y%m%d-%H%M%S)"
  echo ">> Backing up database → backups/alexmessage-$ts.db"
  mkdir -p backups
  cp -a data/alexmessage.db "backups/alexmessage-$ts.db"
  for x in wal shm; do
    if [[ -f "data/alexmessage.db-$x" ]]; then
      cp -a "data/alexmessage.db-$x" "backups/alexmessage-$ts.db-$x"
    fi
  done
  # Keep the newest few.
  ls -1t backups/alexmessage-*.db | tail -n +$((KEEP_DB_BACKUPS + 1)) |
    while read -r old; do rm -f "$old" "$old-wal" "$old-shm"; done || true
fi

echo ">> Extracting release"
tar xzf release.tgz
rm -f release.tgz

echo ">> Starting: ${UNITS[*]}"
sudo -n systemctl start "${UNITS[@]}"
started=1

rollback() {
  echo "::warning::Deploy failed — restoring previous binaries"
  if [[ -d bin.prev ]]; then
    rm -rf bin
    mv bin.prev bin
    sudo -n systemctl restart "${UNITS[@]}" || true
  fi
}

echo ">> Letting services settle"
sleep 3

echo ">> Health check on ports: $HEALTH_PORTS"
healthy=1
for p in $HEALTH_PORTS; do
  # Any HTTP status means the listener is up (/api/me answers 401 when
  # unauthenticated); only a connection failure (000) counts as down.
  code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 "http://127.0.0.1:$p/api/me" || echo 000)"
  if [[ "$code" == "000" ]]; then
    echo "::error::port $p did not respond" >&2
    healthy=0
  else
    echo "   port $p -> $code (up)"
  fi
done

if [[ "$healthy" != "1" ]]; then
  rollback
  exit 1
fi

rm -rf bin.prev
echo ">> Deploy OK"
