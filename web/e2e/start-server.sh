#!/usr/bin/env bash
# Launch an isolated Alex Messages instance for the Playwright suite:
# a scratch dir with a fresh SQLite database plus symlinks to the real
# frontend assets, a seeded set of e2e users, and the server on :8795.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
RUN="${E2E_RUN_DIR:-/tmp/am-e2e}"
PORT="${E2E_PORT:-8795}"

rm -rf "$RUN"
mkdir -p "$RUN/bin" "$RUN/data"
ln -sfn "$ROOT/index.html" "$ROOT/login.html" "$ROOT/static" "$ROOT/fonts" "$ROOT/sound" "$RUN/"
mkdir -p "$RUN/web"
ln -sfn "$ROOT/web/dist" "$RUN/web/dist"

cd "$ROOT"
go build -o "$RUN/bin/seed" ./web/e2e/seed
go build -o "$RUN/bin/server" ./cmd/server

cd "$RUN"
./bin/seed

# Fake Matrix homeserver for the bridge e2e tests; the app runs bridged.
HS_PORT="${E2E_HS_PORT:-8796}"
node "$ROOT/web/e2e/fake-hs.mjs" &
HS_PID=$!
trap 'kill $HS_PID 2>/dev/null || true' EXIT
sleep 0.3

MATRIX_HOMESERVER_URL="http://127.0.0.1:$HS_PORT" \
MATRIX_SERVER_NAME="e2e.test" \
MATRIX_AS_TOKEN="e2e-as-token" \
MATRIX_HS_TOKEN="e2e-hs-token" \
PORT="$PORT" exec ./bin/server
