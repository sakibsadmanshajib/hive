#!/usr/bin/env bash
# Guard for issue 708: Playwright's testMatch is just a path pattern. A
# broken one collects zero spec files while `playwright test --list` still
# exits 0 with no error or warning (the old regex anchored on "no path
# separator from position 0", which an absolute path can never satisfy, so
# the phase-19 project silently ran nothing). This runs the real collector
# and fails loud if the collected spec-file count drifts from what is on
# disk, instead of a green run that quietly covers nothing.
#
# EXPECTED is derived from disk (count of e2e/phase-19/*.spec.ts), not
# hardcoded: a spec file gaining a second test() is a coverage change, not
# a collection change, and should not fail this guard. Counting DISTINCT
# spec files per project (not test() entries) also means a testMatch that
# leaks e2e/phase-19/owui/** into this project still fails, since the
# collected file count would then exceed the direct-child count on disk.
#
# Usage: ./scripts/verify-phase19-collection.sh   (run from apps/web-console)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${SCRIPT_DIR}/.."

PROJECT="phase-19"
EXPECTED="$(ls -1 e2e/phase-19/*.spec.ts | wc -l | tr -d ' ')"

RAW="$(npx playwright test --project="$PROJECT" --list --reporter=json)"

COLLECTED="$(echo "$RAW" | node -e "
const report = JSON.parse(require('fs').readFileSync(0, 'utf8'));
const project = process.argv[1];
const files = new Set();
const walk = (suite) => {
  for (const child of suite.suites || []) walk(child);
  for (const spec of suite.specs || []) {
    if ((spec.tests || []).some((t) => t.projectName === project)) {
      files.add(spec.file);
    }
  }
};
for (const suite of report.suites || []) walk(suite);
console.log(files.size);
" "$PROJECT")"

if [ "$COLLECTED" -ne "$EXPECTED" ]; then
  echo "phase-19 collection guard FAILED: expected $EXPECTED spec files (e2e/phase-19/*.spec.ts on disk) in project '$PROJECT', collected $COLLECTED." >&2
  echo "testMatch broke (collects 0 or too few) or leaked e2e/phase-19/owui/** in (collects too many)." >&2
  exit 1
fi

echo "phase-19 collection guard: OK, $COLLECTED spec files collected in project '$PROJECT' (matches $EXPECTED on disk)."
