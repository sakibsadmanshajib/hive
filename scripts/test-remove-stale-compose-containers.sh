#!/usr/bin/env bash
# Regression guard for scripts/remove-stale-compose-containers.sh (issue #967).
#
# That script runs `docker rm -f` against the live demo box on every deploy, on
# a host with no off-box backup of any production data store, so its selection
# has to stay exactly right. The two ways it can go wrong are opposite and both
# silent: select nothing, and the profile-gated `next dev` console the script
# exists to evict goes on serving an unauthenticated LAN surface for another
# month; select too much, and a deploy tears down live services. Neither shows
# up until it happens on the box.
#
# No docker daemon involved: `docker` is stubbed on PATH, the same trick
# scripts/test-deploy-disk-gate.sh and scripts/test-set-compose-project-name.sh
# use. The stub records every `rm` it is asked to perform, because what this
# guard actually asserts is the set of containers removed, not the exit code.
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
script="$repo_root/scripts/remove-stale-compose-containers.sh"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/bin"

cat >"$tmp/bin/docker" <<'STUB'
#!/usr/bin/env bash
# $FAKE_ACTIVE is what `compose config --services` prints, $FAKE_RUNNING what
# `ps` prints. $FAKE_CONFIG_FAIL makes the compose call exit non-zero the way a
# real one can when the env file is missing or a flag is rejected.
set -euo pipefail
case "$1" in
  compose)
    # `compose ... ps --quiet` resolves the project name; `compose ... config
    # --services` lists the active set. Both arrive as `docker compose <flags>
    # <verb> ...`, so the verb is found by scanning rather than by position.
    for arg in "$@"; do
      case "$arg" in
        config)
          if [ -n "${FAKE_CONFIG_FAIL:-}" ]; then
            echo "compose: error" >&2
            exit 1
          fi
          printf '%s' "${FAKE_ACTIVE:-}"
          exit 0
          ;;
        ps)
          printf '%s' "${FAKE_PROJECT_CID-cid-control-plane}"
          exit 0
          ;;
      esac
    done
    echo "unexpected compose invocation: $*" >&2
    exit 1
    ;;
  inspect)
    printf '%s' "${FAKE_PROJECT-hive}"
    ;;
  ps)
    # The real filter list must contain the oneoff exclusion, or a developer's
    # in-flight `compose run` container is killed mid-command.
    case "$*" in
      *com.docker.compose.oneoff=False*) ;;
      *) echo "docker ps was called without the oneoff exclusion: $*" >&2; exit 1 ;;
    esac
    printf '%s' "${FAKE_RUNNING:-}"
    ;;
  rm)
    # `rm -f <name>`: record the name, and record any volume flag, since a `-v`
    # would make this script capable of destroying a data volume. The volume
    # log is deliberately a DIFFERENT file from $FAKE_RM_LOG, which each case
    # truncates: asserting on the truncated one at the end would be a check
    # that reads an empty file and can never go red.
    shift
    for arg in "$@"; do
      case "$arg" in
        -v|--volumes) echo "SENTINEL-PROVES-THIS-CHECK-WORKS" >>"$FAKE_VOLUME_LOG" ;;
        -f|--force) ;;
        *) echo "$arg" >>"$FAKE_RM_LOG" ;;
      esac
    done
    ;;
  *)
    echo "unexpected docker invocation: $*" >&2
    exit 1
    ;;
esac
STUB
chmod +x "$tmp/bin/docker"

# Never truncated, unlike the per-case rm log, so case 8 reads every removal
# this run performed rather than only the last one.
export FAKE_VOLUME_LOG="$tmp/volume-flag.log"
: >"$FAKE_VOLUME_LOG"

fail() {
  echo "FAIL: $1" >&2
  exit 1
}

# Runs the script with the stub on PATH and the given fixture, echoing the
# newline-separated list of containers it removed.
run_case() {
  local active="$1" running="$2"
  : >"$tmp/rm.log"
  PATH="$tmp/bin:$PATH" \
  FAKE_ACTIVE="$active" FAKE_RUNNING="$running" FAKE_RM_LOG="$tmp/rm.log" \
  HIVE_COMPOSE_FLAGS="--profile local" \
    bash "$script" >"$tmp/out.txt" 2>"$tmp/err.txt"
  cat "$tmp/rm.log"
}

active_set='control-plane
edge-api
open-webui
supabase-db
web-console-prod'

# 1. The case this script was written for: a profile-gated service still
#    running is removed, and nothing else is touched.
removed=$(run_case "$active_set" 'control-plane=hive-control-plane-1
edge-api=hive-edge-api-1
open-webui=hive-open-webui-1
supabase-db=hive-supabase-db-1
web-console-prod=hive-web-console-prod-1
web-console=hive-web-console-1')
[ "$removed" = "hive-web-console-1" ] \
  || fail "expected only hive-web-console-1 removed, got: $(echo "$removed" | tr '\n' ' ')"

# 2. A healthy box removes nothing. The step must be inert on the common path,
#    or every deploy becomes a restart.
removed=$(run_case "$active_set" 'control-plane=hive-control-plane-1
edge-api=hive-edge-api-1
supabase-db=hive-supabase-db-1')
[ -z "$removed" ] || fail "expected no removals on a clean box, got: $removed"

# 3. Substring names are not confused for one another. `web-console` is a
#    prefix of `web-console-prod`, so a `grep -F` without `-x` would treat the
#    running dev service as active and evict nothing, which is exactly the bug
#    this whole script exists to fix, silently reintroduced.
removed=$(run_case 'web-console-prod' 'web-console=hive-web-console-1
web-console-prod=hive-web-console-prod-1')
[ "$removed" = "hive-web-console-1" ] \
  || fail "substring service name confused the match, removed: $(echo "$removed" | tr '\n' ' ')"

# 4. The mirror of case 3: an active service whose name CONTAINS a stale one
#    must still survive.
removed=$(run_case 'web-console' 'web-console=hive-web-console-1
web-console-prod=hive-web-console-prod-1')
[ "$removed" = "hive-web-console-prod-1" ] \
  || fail "expected only hive-web-console-prod-1 removed, got: $(echo "$removed" | tr '\n' ' ')"

# 5. A container carrying no compose service label is left alone rather than
#    removed on an empty service name.
removed=$(run_case "$active_set" '=some-foreign-container
control-plane=hive-control-plane-1')
[ -z "$removed" ] || fail "an unlabelled container was selected: $removed"

# 6. Fail closed on an empty service list. Without this the script would read
#    "no service is active" as "remove everything".
set +e
PATH="$tmp/bin:$PATH" FAKE_ACTIVE="" FAKE_RUNNING='control-plane=hive-control-plane-1' \
  FAKE_RM_LOG="$tmp/rm.log" HIVE_COMPOSE_FLAGS="--profile local" \
  bash "$script" >/dev/null 2>&1
status=$?
set -e
[ "$status" -ne 0 ] || fail "an empty service list must fail the step, exited 0"

# 7. Fail closed when compose itself errors, rather than proceeding on a
#    partial or empty list.
set +e
PATH="$tmp/bin:$PATH" FAKE_CONFIG_FAIL=1 FAKE_RUNNING='control-plane=hive-control-plane-1' \
  FAKE_RM_LOG="$tmp/rm.log" HIVE_COMPOSE_FLAGS="--profile local" \
  bash "$script" >/dev/null 2>&1
status=$?
set -e
[ "$status" -ne 0 ] || fail "a failing compose config must fail the step, exited 0"

# 8. No volume flag ever reaches `docker rm`, across every removal above. This
#    is the property that keeps a wrong selection survivable on a box with no
#    off-box backup: the container goes, the data does not.
if grep -q "SENTINEL-PROVES-THIS-CHECK-WORKS" "$FAKE_VOLUME_LOG"; then
  fail "docker rm was called with a volume flag"
fi

# 9. --dry-run reports the same selection and removes nothing, so an operator
#    can check the blast radius on a live box before the deploy does it.
: >"$tmp/rm.log"
out=$(PATH="$tmp/bin:$PATH" FAKE_ACTIVE="$active_set" \
  FAKE_RUNNING='web-console=hive-web-console-1
control-plane=hive-control-plane-1' \
  FAKE_RM_LOG="$tmp/rm.log" HIVE_COMPOSE_FLAGS="--profile local" \
  bash "$script" --dry-run)
[ -s "$tmp/rm.log" ] && fail "--dry-run removed a container"
echo "$out" | grep -q "would remove hive-web-console-1" \
  || fail "--dry-run did not name the container it would remove: $out"

# 10. The project name is read off a real container, not assumed to be `hive`.
#     COMPOSE_PROJECT_NAME in the box's untracked .env overrides the compose
#     file's `name:`, and a hardcoded `hive` would filter to nothing, report
#     "removed: 0" and exit green with the stale container still running.
: >"$tmp/rm.log"
PATH="$tmp/bin:$PATH" FAKE_PROJECT="hive-renamed" FAKE_ACTIVE="$active_set" \
  FAKE_RUNNING='web-console=hive-renamed-web-console-1' \
  FAKE_RM_LOG="$tmp/rm.log" HIVE_COMPOSE_FLAGS="--profile local" \
  bash "$script" >"$tmp/out.txt" 2>&1
[ "$(cat "$tmp/rm.log")" = "hive-renamed-web-console-1" ] \
  || fail "a renamed compose project was not handled: $(cat "$tmp/out.txt")"

# 11. Fail closed when the project has no running containers at all, rather
#     than resolving an empty project name and filtering on it.
set +e
PATH="$tmp/bin:$PATH" FAKE_PROJECT_CID="" FAKE_ACTIVE="$active_set" \
  FAKE_RUNNING="" FAKE_RM_LOG="$tmp/rm.log" HIVE_COMPOSE_FLAGS="--profile local" \
  bash "$script" >/dev/null 2>&1
status=$?
set -e
[ "$status" -ne 0 ] || fail "an unresolvable project name must fail the step, exited 0"

# 12. An upper bound on the blast radius. A run that wants to remove more than
#     a handful has almost certainly resolved the active set wrongly, and on a
#     box with no off-box backup it must stop rather than converge in one pass.
: >"$tmp/rm.log"
set +e
PATH="$tmp/bin:$PATH" FAKE_ACTIVE="control-plane" \
  FAKE_RUNNING='a=hive-a-1
b=hive-b-1
c=hive-c-1
d=hive-d-1' \
  FAKE_RM_LOG="$tmp/rm.log" HIVE_COMPOSE_FLAGS="--profile local" \
  bash "$script" >/dev/null 2>&1
status=$?
set -e
[ "$status" -ne 0 ] || fail "four stale containers must trip the bound, exited 0"
[ ! -s "$tmp/rm.log" ] || fail "the bound was tripped but containers were removed anyway"

# 13. The deploy workflow actually calls this script. A guard that tests a
#     script nothing runs is coverage on paper only.
workflow="$repo_root/.github/workflows/deploy-demo-box.yml"
grep -q "scripts/remove-stale-compose-containers.sh" "$workflow" \
  || fail "deploy-demo-box.yml does not call remove-stale-compose-containers.sh"

# 14. It is invoked from the compose directory. HIVE_COMPOSE_FLAGS carries
#     relative `-f docker-compose.yml` and `--env-file ../../.env` paths, so
#     running this from the repo root makes every compose call in it resolve
#     against the wrong directory. Nothing in the stubbed cases above can see
#     that, because a stub does not care where it was called from, and the
#     first draft of this step got it wrong exactly that way.
python3 - "$workflow" <<'PY' || exit 1
import sys, yaml
steps = yaml.safe_load(open(sys.argv[1]))["jobs"]["deploy"]["steps"]
step = next(
    s for s in steps
    if isinstance(s, dict) and "remove-stale-compose-containers.sh" in str(s.get("run", ""))
)
wd = step.get("working-directory", "")
if not wd.endswith("deploy/docker"):
    sys.exit(
        "FAIL: the eviction step runs from %r; HIVE_COMPOSE_FLAGS only resolves "
        "from the compose directory" % wd
    )
PY

# 15. The script is on the deploy workflow's path filter. Without an entry a
#     fix to the selection would merge and never reach the box, which is this
#     repository's documented merged-is-not-deployed trap.
grep -q "'scripts/remove-stale-compose-containers.sh'" "$workflow" \
  || fail "remove-stale-compose-containers.sh is not on deploy-demo-box.yml's path filter"

echo "PASS: remove-stale-compose-containers.sh selection guard, 15 cases"
