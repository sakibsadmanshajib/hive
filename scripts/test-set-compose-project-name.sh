#!/usr/bin/env bash
# test-set-compose-project-name.sh -- proves the --check collision grep uses
# WHOLE-LINE matching, not substring matching, in both directions.
#
# The bug: line 81 used `grep -v -F "$compose_dir"` (no -x) to remove this
# worktree's own working_dir from the docker-reported collision list.
# Without -x, grep -v -F removes any line that merely CONTAINS compose_dir as
# a substring, not just a line EQUAL to it. A genuinely different working
# directory whose path happens to start with (or otherwise contain)
# compose_dir as a substring -- e.g. a nested worktree checked out one level
# below this one -- gets silently treated as "not a collision" and the
# --check call wrongly reports "ok" and exits 0.
#
# Two scenarios, both against a fake `docker` on PATH (no real daemon
# needed):
#   1. a genuinely colliding directory that contains compose_dir as a
#      substring must still be REPORTED (exit non-zero).
#   2. this worktree's own compose_dir, reported back verbatim by docker
#      (the ordinary, no-collision case), must still be treated as a
#      self-match and NOT reported (exit zero) -- proving the -x fix did not
#      turn whole-line matching into a stricter check that now misses the
#      legitimate self-match it is supposed to filter out.
#
# Run: bash scripts/test-set-compose-project-name.sh
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT="$HERE/set-compose-project-name.sh"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# Shared setup: one fixture git repo, one fake docker stub whose `ps`
# output is controlled by $FAKE_DOCKER_PS_LINE at run time.
fixture_repo="$work/repo-under-test"
mkdir -p "$fixture_repo"
git -C "$fixture_repo" init -q
compose_dir="$fixture_repo/deploy/docker"

fake_bin="$work/bin"
mkdir -p "$fake_bin"
cat > "$fake_bin/docker" <<'DOCKER_EOF'
#!/usr/bin/env bash
if [[ "$1" == "ps" ]]; then
  echo "$FAKE_DOCKER_PS_LINE"
  exit 0
fi
exit 0
DOCKER_EOF
chmod +x "$fake_bin/docker"

run_check() {
  local ps_line="$1"
  set +e
  output="$(cd "$fixture_repo" && FAKE_DOCKER_PS_LINE="$ps_line" PATH="$fake_bin:$PATH" bash "$SCRIPT" --check 2>&1)"
  status=$?
  set -e
}

# Scenario 1: a genuinely different directory that CONTAINS compose_dir as a
# substring (a nested worktree one level under this one). Must be reported
# as a real collision.
colliding_dir="$compose_dir/nested-worktree"
run_check "$colliding_dir"

echo "--- scenario 1 (substring collision): script output ---"
echo "$output"
echo "--- exit status: $status ---"

if [[ $status -eq 0 ]]; then
  echo "FAIL scenario 1: expected --check to exit non-zero (a real collision was injected), got 0" >&2
  exit 1
fi
if ! grep -q "$colliding_dir" <<<"$output"; then
  echo "FAIL scenario 1: expected the colliding directory to be named in the output" >&2
  exit 1
fi
echo "scenario 1 PASS"
echo

# Scenario 2: docker reports back this worktree's own compose_dir verbatim
# (the ordinary case: this worktree's containers, not a collision). Must
# still exit 0 and report "ok" -- proves -x did not turn the legitimate
# self-match into a false positive.
run_check "$compose_dir"

echo "--- scenario 2 (exact self-match, no collision): script output ---"
echo "$output"
echo "--- exit status: $status ---"

if [[ $status -ne 0 ]]; then
  echo "FAIL scenario 2: expected --check to exit 0 for this worktree's own compose_dir (no real collision), got $status" >&2
  exit 1
fi
if ! grep -q "^ok:" <<<"$output"; then
  echo "FAIL scenario 2: expected an 'ok:' line for the no-collision case" >&2
  exit 1
fi
echo "scenario 2 PASS"
echo

echo "test-set-compose-project-name: PASS"
