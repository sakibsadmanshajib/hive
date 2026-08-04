#!/usr/bin/env bash
# Guard for issue 708: Playwright's testMatch is just a path pattern. A
# broken one collects zero spec files while `playwright test --list` still
# exits 0 with no error or warning (the old /^[^/]+\.spec\.ts$/ regex did
# exactly this -- it can never match an absolute path, so the phase-19
# project silently ran nothing). This runs the real collector and fails
# loud if the count drifts from what is expected, instead of a green run
# that quietly covers nothing.
#
# Usage: ./scripts/verify-phase19-collection.sh   (run from apps/web-console)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${SCRIPT_DIR}/.."

PROJECT="phase-19"
EXPECTED=7

RAW="$(npx playwright test --project="$PROJECT" --list --reporter=json)"

COLLECTED="$(echo "$RAW" | python3 -c "
import json, sys

def walk(suite, project):
    count = 0
    for child in suite.get('suites', []):
        count += walk(child, project)
    for spec in suite.get('specs', []):
        for t in spec.get('tests', []):
            if t.get('projectName') == project:
                count += 1
    return count

report = json.load(sys.stdin)
total = sum(walk(s, '$PROJECT') for s in report.get('suites', []))
print(total)
")"

if [ "$COLLECTED" -ne "$EXPECTED" ]; then
  echo "phase-19 collection guard FAILED: expected $EXPECTED tests in project '$PROJECT', collected $COLLECTED." >&2
  echo "Either testMatch broke (collects 0 or too few), or a spec file was deliberately added or removed -- if the latter, update EXPECTED in this script." >&2
  exit 1
fi

echo "phase-19 collection guard: OK, $COLLECTED tests collected in project '$PROJECT'."
