#!/usr/bin/env bash
#
# Runs ON the production host — piped in over SSH by
# .github/workflows/deploy.yml (`ssh … bash -s < deploy/remote_deploy.sh`).
#
# Inputs (exported by the workflow before `bash -s`):
#   APP_DIR       app root, already holds the freshly scp'd release.tgz
#   HEALTH_PORTS  space-separated localhost ports to probe after restart
#                 (must match the systemd units' PORT/ADMIN_PORT/CALL_PORT)
#
# It backs up the current binaries, extracts the new release over the old tree,
# restarts the three systemd units, and smoke-tests each port. If any unit
# fails to answer, it restores the previous binaries and exits non-zero so the
# GitHub Actions run is marked failed.
#
# Prerequisite on the host: the deploy user has passwordless sudo for exactly
# `systemctl restart` of the three units (see DEPLOY.md → Continuous deployment).

set -euo pipefail

APP_DIR="${APP_DIR:?APP_DIR not set}"
HEALTH_PORTS="${HEALTH_PORTS:-8765 9090 9091}"
UNITS=(alexmessage alexmessage-admin alexmessage-voice)

cd "$APP_DIR"

if [[ ! -f release.tgz ]]; then
  echo "::error::release.tgz not found in $APP_DIR" >&2
  exit 1
fi

echo ">> Backing up current binaries"
rm -rf bin.prev
[[ -d bin ]] && cp -a bin bin.prev

echo ">> Extracting release"
tar xzf release.tgz
rm -f release.tgz

restart_units() {
  echo ">> Restarting: ${UNITS[*]}"
  sudo -n systemctl restart "${UNITS[@]}"
}

rollback() {
  echo "::warning::Deploy failed — restoring previous binaries"
  if [[ -d bin.prev ]]; then
    rm -rf bin
    mv bin.prev bin
    sudo -n systemctl restart "${UNITS[@]}" || true
  fi
}

restart_units

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
