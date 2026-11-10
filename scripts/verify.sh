#!/usr/bin/env bash
# Verifies the repository: Go build, vet and race-detector tests, the deploy
# and upload tests, and the TypeScript typechecks and unit tests.
#
#   scripts/verify.sh           # all checks except the Playwright suite
#   scripts/verify.sh --e2e     # also builds the web client and runs Playwright
#
# It installs nothing. The node_modules directories of shared/, web/ and
# mobile/ must exist (run `npm ci` in each). --e2e also needs
# `npx playwright install chromium` once.
set -euo pipefail

e2e=0
if [[ $# -gt 1 ]]; then
  echo "usage: $0 [--e2e]" >&2
  exit 2
elif [[ $# -eq 1 ]]; then
  if [[ "$1" == --e2e ]]; then e2e=1; else echo "usage: $0 [--e2e]" >&2; exit 2; fi
fi

cd "$(dirname "$0")/.."

echo ">> AGENTS.md and CLAUDE.md hold the same text after their first 3 lines"
if ! diff <(tail -n +4 AGENTS.md) <(tail -n +4 CLAUDE.md) > /dev/null; then
  echo "AGENTS.md and CLAUDE.md differ. Make the same edit in both files." >&2
  exit 1
fi
echo ">> go build ./..."
go build ./...
echo ">> go vet ./..."
go vet ./...
echo ">> go test -race -count=1 ./..."
go test -race -count=1 ./...
echo ">> python3 deploy/remote_deploy_test.py"
python3 deploy/remote_deploy_test.py
echo ">> bash mobile/scripts/test-upload.sh"
bash mobile/scripts/test-upload.sh
echo ">> shared: typecheck and tests"
(cd shared && npm run typecheck && npm test)
echo ">> web: tsc --noEmit"
(cd web && npx --no -- tsc --noEmit)
echo ">> mobile: tsc --noEmit"
(cd mobile && npx --no -- tsc --noEmit)
if [[ $e2e -eq 1 ]]; then
  echo ">> web: build and Playwright"
  (cd web && npm run build && npm run test:e2e)
fi
echo ">> All checks passed"
