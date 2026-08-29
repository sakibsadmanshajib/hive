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

# The docker stub records every invocation, because two of the guarantees below
# are about calls that must or must not happen: the reclaim must not run on a
# healthy box, and it must run before the thresholds are compared. $FAKE_DOCKER_FAIL
# makes `builder prune` fail the way a real one can (daemon busy, context
# cancelled, the reclaim outliving its timeout) while leaving the rest of docker
# working, which is the case that must not be allowed to skip the check.
cat > "$tmp/bin/docker" <<'SH'
#!/bin/sh
echo "$*" >> "$DOCKER_LOG"
case "$1 $2" in
  "builder prune")
    if [ -n "${FAKE_DOCKER_FAIL:-}" ]; then
      echo "stub docker: builder prune failed" >&2
      exit 1
    fi
    ;;
esac
echo "stub docker $*"
SH
chmod +x "$tmp/bin/docker"
export DOCKER_LOG="$tmp/docker.log"
: > "$DOCKER_LOG"

# $FAKE_FREE_GB is what the stubbed df reports, in the two-line shape
# `df -BG --output=avail` produces. Two special values: an empty string models
# a df that exits 0 but prints nothing usable, and "EXPLODE" models df failing
# outright (no such mount, permission denied, a df build without --output).
# The second is the one worth guarding: under `set -euo pipefail` a df inside
# the pipeline would abort the script with no annotation at all.
#
# $FAKE_FREE_SEQ_FILE is how a reclaim is modelled: the guard reads free space,
# reclaims, then reads again, so the two reads have to be able to differ. Each
# call consumes one line. A fixed $FAKE_FREE_GB cannot express "the prune freed
# nothing" and "the prune freed 12 GB" as different runs, and a test that cannot
# tell those apart cannot fail when the second read is dropped.
cat > "$tmp/bin/df" <<'SH'
#!/bin/sh
if [ -n "${FAKE_FREE_SEQ_FILE:-}" ] && [ -s "${FAKE_FREE_SEQ_FILE}" ]; then
  value=$(head -1 "${FAKE_FREE_SEQ_FILE}")
  sed -i '1d' "${FAKE_FREE_SEQ_FILE}"
else
  value="${FAKE_FREE_GB:-}"
fi
if [ "$value" = "EXPLODE" ]; then
  echo "df: unrecognized option '--output=avail'" >&2
  exit 1
fi
echo "Avail"
echo "$value"
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
  : > "$DOCKER_LOG"

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

# ------------------------------------------------------------------
# Build cache reclaim (issue #1419).
#
# The guard reclaims unused build cache when it finds itself below the warn
# line, then re-reads free space and applies the thresholds to the result. Four
# properties have to hold, and each of these cases fails if one is dropped.
# ------------------------------------------------------------------

# A healthy box is never pruned. Without this the guard would throw away the
# layer cache of a box that had no problem, which costs every subsequent build
# and launders the trend the warn line exists to show.
: > "$DOCKER_LOG"
FAKE_FREE_GB=40G GITHUB_STEP_SUMMARY=/dev/null bash "$gate" >/dev/null 2>&1 || true
if grep -q 'builder prune' "$DOCKER_LOG"; then
  fail "[healthy-box-is-not-pruned] a box above the warn line was pruned anyway" "$(cat "$DOCKER_LOG")"
else
  echo "ok   [healthy-box-is-not-pruned]"
fi

# $2 is a space-separated sequence of what successive df calls report: the
# first is the pre-reclaim reading, the second the post-reclaim one.
run_seq() {
  local label="$1" sequence="$2" want_status="$3" want_match="$4" reject_match="$5"
  shift 5
  local out status summary seq_file
  summary="$tmp/summary.$label"
  seq_file="$tmp/seq.$label"
  : > "$summary"
  : > "$DOCKER_LOG"
  printf '%s\n' $sequence > "$seq_file"

  set +e
  out=$(env "$@" FAKE_FREE_SEQ_FILE="$seq_file" GITHUB_STEP_SUMMARY="$summary" bash "$gate" 2>&1)
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
  if ! grep -q 'builder prune' "$DOCKER_LOG"; then
    fail "[$label] the guard never reclaimed build cache" "$(cat "$DOCKER_LOG")"; return
  fi
  echo "ok   [$label]"
}

# The motivating case: 14G free is below the 15G floor and is exactly where the
# box sat when it refused three consecutive deploys, and `docker builder prune
# -f` took it to 23G on the day. It must proceed, and it must say out loud that
# it only proceeded because of a reclaim.
run_seq prune-rescues     "14G 23G" 0 "reclaimed build cache" "::error::"
# Between the floor and the warn line after the reclaim: proceed, still warn.
run_seq prune-partial     "14G 20G" 0 "::warning::"           "::error::"
# A box whose problem is not build cache: the reclaim frees nothing, the pre
# and post numbers are the same, and the floor still refuses. This is the case
# that proves pruning before the check cannot mask a real disk problem.
run_seq prune-not-enough  "14G 14G" 1 "::error::"             ""
# THE load-bearing case. A failing reclaim must not be able to skip, soften or
# short-circuit the check that follows it. The guard still refuses.
run_seq prune-fails       "14G 14G" 1 "::error::"             "" FAKE_DOCKER_FAIL=1

# Both numbers have to reach the log. A reclaim that reported only its result
# would hide how close the box was, which is the signal an operator needs to
# tell "build cache accumulated again" from "something else is eating the disk".
printf '14G\n23G\n' > "$tmp/seq.numbers"
out=$(FAKE_FREE_SEQ_FILE="$tmp/seq.numbers" GITHUB_STEP_SUMMARY="$tmp/summary.numbers" bash "$gate" 2>&1 || true)
for wanted in "14G" "23G"; do
  if printf '%s' "$out" | grep -qF -- "$wanted"; then
    echo "ok   [reclaim reports $wanted]"
  else
    fail "[reclaim-reports-both-numbers] '$wanted' never appears" "$out"
  fi
done

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
