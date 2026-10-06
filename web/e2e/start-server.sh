#!/usr/bin/env bash
# Launch an isolated Alex Messages instance for the Playwright suite:
# a scratch dir with a fresh SQLite database plus symlinks to the real
# frontend assets, a seeded set of e2e users, and the server on :8795.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
RUN="${E2E_RUN_DIR:-/tmp/am-e2e}"
PORT="${E2E_PORT:-8795}"

mkdir -p "$RUN/bin" "$RUN/data"
ln -sfn "$ROOT/index.html" "$ROOT/login.html" "$ROOT/static" "$ROOT/fonts" "$ROOT/sound" "$RUN/"
mkdir -p "$RUN/web"
ln -sfn "$ROOT/web/dist" "$RUN/web/dist"

cd "$ROOT"
go build -o "$RUN/bin/seed" ./web/e2e/seed
go build -o "$RUN/bin/server" ./cmd/server

cd "$RUN"
./bin/seed
PORT="$PORT" exec ./bin/server
