#!/usr/bin/env bash
# test-set-compose-project-name.sh -- proves the --check collision grep uses
# WHOLE-LINE matching, not substring matching.
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
# This fixture never touches a real docker daemon: it puts a fake `docker`
# executable on PATH that returns one canned working_dir line, a real,
# different directory that happens to contain compose_dir as a substring.
#
# Run: bash scripts/test-set-compose-project-name.sh
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT="$HERE/set-compose-project-name.sh"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

fixture_repo="$work/repo-under-test"
mkdir -p "$fixture_repo"
git -C "$fixture_repo" init -q
compose_dir="$fixture_repo/deploy/docker"

# A genuinely different directory that CONTAINS compose_dir as a substring
# (a nested worktree one level under this one). Must be reported as a real
# collision.
colliding_dir="$compose_dir/nested-worktree"

fake_bin="$work/bin"
mkdir -p "$fake_bin"
cat > "$fake_bin/docker" <<EOF
#!/usr/bin/env bash
if [[ "\$1" == "ps" ]]; then
  echo "$colliding_dir"
  exit 0
fi
exit 0
EOF
chmod +x "$fake_bin/docker"

set +e
output="$(cd "$fixture_repo" && PATH="$fake_bin:$PATH" bash "$SCRIPT" --check 2>&1)"
status=$?
set -e

echo "--- script output ---"
echo "$output"
echo "--- exit status: $status ---"

if [[ $status -eq 0 ]]; then
  echo "FAIL: expected --check to exit non-zero (a real collision was injected), got 0" >&2
  exit 1
fi
if ! grep -q "$colliding_dir" <<<"$output"; then
  echo "FAIL: expected the colliding directory to be named in the output" >&2
  exit 1
fi

echo "test-set-compose-project-name: PASS"
