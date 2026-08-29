#!/usr/bin/env bash
# Regression guard for deploy-demo-box.yml's "Refuse to build without disk
# headroom" step (issue #1098).
#
# That step is the only disk guard in the deploy job that a job timeout cannot
# skip, so its three branches have to stay correct: silent above the warn line,
# warn-but-proceed between the lines, and hard fail below the floor. Getting a
# comparison backwards would either wave a doomed deploy through or block every
# healthy one, and neither shows up until it happens on the live box.
#
# No docker daemon and no real filesystem needed: `df` and `docker` are stubbed
# on PATH, the same trick scripts/test-set-compose-project-name.sh uses. The
# step body is read out of the workflow file itself rather than copied here, so
# this test cannot silently drift away from the thing it guards.
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
workflow="$repo_root/.github/workflows/deploy-demo-box.yml"
step_name="Refuse to build without disk headroom"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# Pull the step's `run:` body straight out of the workflow.
python3 - "$workflow" "$step_name" > "$tmp/gate.sh" <<'PY'
import sys, yaml
workflow, step_name = sys.argv[1], sys.argv[2]
with open(workflow) as fh:
    doc = yaml.safe_load(fh)
steps = doc["jobs"]["deploy"]["steps"]
if steps[0].get("name") != step_name:
    sys.exit(
        f"expected {step_name!r} to be the FIRST step of the deploy job "
        f"(a later position is skippable by a job timeout), found "
        f"{steps[0].get('name')!r}"
    )
sys.stdout.write(steps[0]["run"])
PY

mkdir -p "$tmp/bin"
cat > "$tmp/bin/docker" <<'SH'
#!/bin/sh
echo "stub docker $*"
SH
chmod +x "$tmp/bin/docker"

# $FAKE_FREE_GB is what the stubbed df reports, in the two-line shape
# `df -BG --output=avail` produces. An empty value models an unreadable mount.
cat > "$tmp/bin/df" <<'SH'
#!/bin/sh
echo "Avail"
echo "${FAKE_FREE_GB}"
SH
chmod +x "$tmp/bin/df"

export PATH="$tmp/bin:$PATH"

failures=0

run_case() {
  local label="$1" free="$2" want_status="$3" want_match="$4" reject_match="$5"
  local out status summary
  summary="$tmp/summary.$label"
  : > "$summary"

  set +e
  out=$(FAKE_FREE_GB="$free" GITHUB_STEP_SUMMARY="$summary" bash "$tmp/gate.sh" 2>&1)
  status=$?
  set -e

  if [ "$status" != "$want_status" ]; then
    echo "FAIL [$label] exit $status, wanted $want_status"
    echo "$out" | sed 's/^/       /'
    failures=$((failures + 1))
    return
  fi
  if [ -n "$want_match" ] && ! printf '%s' "$out" | grep -qF -- "$want_match"; then
    echo "FAIL [$label] output missing '$want_match'"
    echo "$out" | sed 's/^/       /'
    failures=$((failures + 1))
    return
  fi
  if [ -n "$reject_match" ] && printf '%s' "$out" | grep -qF -- "$reject_match"; then
    echo "FAIL [$label] output should not contain '$reject_match'"
    echo "$out" | sed 's/^/       /'
    failures=$((failures + 1))
    return
  fi
  echo "ok   [$label]"
}

#        label            free  status  must contain    must not contain
run_case healthy          30G   0       ""              "::warning::"
run_case at-warn-line     20G   0       ""              "::warning::"
run_case just-below-warn  19G   0       "::warning::"   "::error::"
run_case at-floor         10G   0       "::warning::"   "::error::"
run_case below-floor      9G    1       "::error::"     ""
run_case near-empty       1G    1       "::error::"     ""
run_case unreadable       ""    1       "::error::"     ""

# The failure message has to name the two things that must NOT be run to
# reclaim, because a full-disk deploy failure is exactly when someone reaches
# for the biggest hammer available.
out=$(FAKE_FREE_GB=1G GITHUB_STEP_SUMMARY="$tmp/summary.hint" bash "$tmp/gate.sh" 2>&1 || true)
for forbidden in "docker system prune -a" "docker image prune -a" "volumes"; do
  if ! printf '%s' "$out" | grep -qF -- "$forbidden"; then
    echo "FAIL [hint] failure message never mentions '$forbidden'"
    failures=$((failures + 1))
  else
    echo "ok   [hint: warns off '$forbidden']"
  fi
done

if [ "$failures" != "0" ]; then
  echo "$failures check(s) failed"
  exit 1
fi
echo "all checks passed"
