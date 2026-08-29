#!/usr/bin/env bash
# Regression guard for scripts/check-deploy-disk.sh (issue #1098).
#
# That script is the only disk guard in deploy-demo-box.yml that a job timeout
# cannot skip, so its three branches have to stay correct: silent above the
# warn line, warn-but-proceed between the lines, and hard fail below the floor.
# Getting a comparison backwards would either wave a doomed deploy through or
# block every healthy one, and neither shows up until it happens on the live
# box. The floor in particular has to fire ABOVE the observed failure point:
# run 33237021243 timed out mid recreate with 11 GB free, so a floor at or
# below 11 would have permitted exactly the deploy the guard exists to stop.
#
# No docker daemon and no real filesystem needed: `df` and `docker` are stubbed
# on PATH, the same trick scripts/test-set-compose-project-name.sh uses.
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
gate="$repo_root/scripts/check-deploy-disk.sh"
workflow="$repo_root/.github/workflows/deploy-demo-box.yml"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/bin"

cat > "$tmp/bin/docker" <<'SH'
#!/bin/sh
echo "stub docker $*"
SH
chmod +x "$tmp/bin/docker"

# $FAKE_FREE_GB is what the stubbed df reports, in the two-line shape
# `df -BG --output=avail` produces. Two special values: an empty string models
# a df that exits 0 but prints nothing usable, and "EXPLODE" models df failing
# outright (no such mount, permission denied, a df build without --output).
# The second is the one worth guarding: under `set -euo pipefail` a df inside
# the pipeline would abort the script with no annotation at all.
cat > "$tmp/bin/df" <<'SH'
#!/bin/sh
if [ "${FAKE_FREE_GB}" = "EXPLODE" ]; then
  echo "df: unrecognized option '--output=avail'" >&2
  exit 1
fi
echo "Avail"
echo "${FAKE_FREE_GB}"
SH
chmod +x "$tmp/bin/df"

export PATH="$tmp/bin:$PATH"

failures=0
fail() {
  echo "FAIL $1"
  shift
  [ $# -gt 0 ] && printf '%s\n' "$1" | sed 's/^/       /'
  failures=$((failures + 1))
}

run_case() {
  local label="$1" free="$2" want_status="$3" want_match="$4" reject_match="$5"
  local out status summary
  summary="$tmp/summary.$label"
  : > "$summary"

  set +e
  out=$(FAKE_FREE_GB="$free" GITHUB_STEP_SUMMARY="$summary" bash "$gate" 2>&1)
  status=$?
  set -e

  if [ "$status" != "$want_status" ]; then
    fail "[$label] exit $status, wanted $want_status" "$out"; return
  fi
  if [ -n "$want_match" ] && ! printf '%s' "$out" | grep -qF -- "$want_match"; then
    fail "[$label] output missing '$want_match'" "$out"; return
  fi
  if [ -n "$reject_match" ] && printf '%s' "$out" | grep -qF -- "$reject_match"; then
    fail "[$label] output should not contain '$reject_match'" "$out"; return
  fi
  echo "ok   [$label]"
}

# Defaults are floor 15, warn 25.
#        label            free  status  must contain    must not contain
run_case healthy          40G   0       ""              "::warning::"
run_case at-warn-line     25G   0       ""              "::warning::"
run_case just-below-warn  24G   0       "::warning::"   "::error::"
run_case at-floor         15G   0       "::warning::"   "::error::"
# The motivating incident. 11G free is what the box had when run 33237021243
# timed out mid recreate, so this case must FAIL, not warn.
run_case incident-11g     11G   1       "::error::"     ""
run_case just-below-floor 14G   1       "::error::"     ""
run_case near-empty       1G    1       "::error::"     ""
run_case unparseable      ""    1       "::error::"     ""
run_case df-fails    EXPLODE    1       "::error::"     ""

check() {
  local label="$1"; shift
  if "$@"; then echo "ok   [$label]"; else fail "[$label]"; fi
}

# GITHUB_STEP_SUMMARY is always set by Actions, but `set -u` would turn an
# unset one into a bare abort with no annotation, so the guard must not depend
# on it.
set +e
FAKE_FREE_GB=40G bash "$gate" >/dev/null 2>&1
no_summary_status=$?
set -e
check "no-summary-var" [ "$no_summary_status" = "0" ]

# The floor is overridable, because a floor with no escape hatch can wedge the
# box: below it nothing deploys, including a change meant to shrink the build.
set +e
out=$(FAKE_FREE_GB=11G HIVE_DEPLOY_DISK_FLOOR_GB=5 HIVE_DEPLOY_DISK_WARN_GB=8 \
  GITHUB_STEP_SUMMARY="$tmp/summary.override" bash "$gate" 2>&1)
override_status=$?
set -e
check "floor-overridable" [ "$override_status" = "0" ]

# An empty override (what a push-triggered run passes, since `inputs` is empty
# outside workflow_dispatch) must fall back to the default floor, not to zero.
set +e
FAKE_FREE_GB=11G HIVE_DEPLOY_DISK_FLOOR_GB="" \
  GITHUB_STEP_SUMMARY="$tmp/summary.empty" bash "$gate" >/dev/null 2>&1
empty_override_status=$?
set -e
check "empty-override-uses-default" [ "$empty_override_status" = "1" ]

# Both thresholds come from a human-typed workflow_dispatch input, and a bad
# value does not fail, it silently stops gating: `[ 22 -lt bad ]` exits 2,
# which `if` reads as false, so the floor check is skipped. A warn line below
# the floor is the same hole by another route, since the `>= warn` early exit
# returns 0 before the floor is compared. Both must be refused, and refused
# with an annotation rather than a bare non-zero exit.
bad_case() {
  local label="$1" free="$2"; shift 2
  local out status
  set +e
  out=$(env "$@" FAKE_FREE_GB="$free" GITHUB_STEP_SUMMARY="$tmp/summary.$label" bash "$gate" 2>&1)
  status=$?
  set -e
  if [ "$status" != "1" ]; then
    fail "[$label] exit $status, wanted 1" "$out"; return
  fi
  if ! printf '%s' "$out" | grep -qF -- "::error::"; then
    fail "[$label] rejected without an ::error:: annotation" "$out"; return
  fi
  echo "ok   [$label]"
}

bad_case floor-nonnumeric      22G HIVE_DEPLOY_DISK_FLOOR_GB=bad
bad_case warn-nonnumeric       22G HIVE_DEPLOY_DISK_WARN_GB=bad
bad_case floor-negative        22G HIVE_DEPLOY_DISK_FLOOR_GB=-5
bad_case floor-decimal         22G HIVE_DEPLOY_DISK_FLOOR_GB=7.5
# warn under floor: 10 is >= warn 0, so without the inversion check the script
# would exit 0 here and never compare against the 15 GB floor.
bad_case warn-below-floor      10G HIVE_DEPLOY_DISK_WARN_GB=0

# The failure message has to name the things that must NOT be run to reclaim,
# because a full-disk failure is exactly when someone reaches for the biggest
# hammer available.
out=$(FAKE_FREE_GB=1G GITHUB_STEP_SUMMARY="$tmp/summary.hint" bash "$gate" 2>&1 || true)
for forbidden in "docker system prune -a" "docker image prune -a" "volumes"; do
  if printf '%s' "$out" | grep -qF -- "$forbidden"; then
    echo "ok   [hint: warns off '$forbidden']"
  else
    fail "[hint] failure message never mentions '$forbidden'"
  fi
done

# Both jobs must actually call the guard. This is the part that would regress
# silently: deleting a call site is a one-line diff that nothing else notices,
# and the migrate job is the one where running out of disk is worst (Postgres
# panics and shuts down on ENOSPC, taking out the stack that is serving).
# Count EXECUTABLE call sites, not text matches. The first version of this
# counted every mention of the path and asserted three or more, which the
# comments and the push.paths entry satisfy on their own: deleting both `run:`
# lines still left four matches, so the assertion passed with zero jobs
# protected. Exactly two `run:` lines, and separately exactly one push.paths
# entry, which is what keeps a fix to the guard from merging without ever
# reaching the box.
calls=$(grep -cE '^[[:space:]]*run: scripts/check-deploy-disk\.sh[[:space:]]*$' "$workflow" || true)
paths=$(grep -cE "^[[:space:]]*- 'scripts/check-deploy-disk\.sh'[[:space:]]*$" "$workflow" || true)
check "wired-into-both-jobs" [ "$calls" = "2" ]
check "guard-change-triggers-deploy" [ "$paths" = "1" ]

if [ "$failures" != "0" ]; then
  echo "$failures check(s) failed"
  exit 1
fi
echo "all checks passed"
