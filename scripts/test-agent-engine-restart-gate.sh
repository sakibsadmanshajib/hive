#!/usr/bin/env bash
# Regression guard for scripts/install-agent-engine-host.sh's restart decision
# (issue #921).
#
# The defect this guards against: the installer used to stop and restart the
# agent-engine launcher unit on EVERY deploy, unconditionally. The unit runs
# with systemd's default KillMode=control-group and the sandbox is a foreground
# `apptainer run` child of the launcher process, so that stop kills the running
# agent, not merely the launcher's in-memory session registry. Control-plane
# then polls a session the new launcher never heard of, gets a 404, maps it onto
# agenttask.ErrEngineSessionGone, and since PR #914 that is terminal on first
# detection. Every in-flight Cowork task therefore died within one poll interval
# (15s) of any merge to main, and on 2026-08-29 that was ten restarts in eight
# hours for deploys triggered by paths that cannot change the launcher at all.
#
# So the case that actually matters here is case B below: a second install with
# unchanged artifacts must leave the running daemon, and the session it is
# holding, alive. `stop` is modelled as destructive exactly as the real
# KillMode=control-group is: the stub deletes the session marker. A test that
# only asserted the daemon comes back up would pass against the broken script.
#
# No apptainer, no docker daemon, no systemd and no real launcher needed:
# `apptainer`, `docker`, `systemctl`, `systemd-run` and `curl` are all stubbed
# on PATH, the same trick scripts/test-deploy-disk-gate.sh and
# scripts/test-set-compose-project-name.sh use.
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
installer="$repo_root/scripts/install-agent-engine-host.sh"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/bin" "$tmp/state"
runtime="$tmp/runtime"
mkdir -p "$runtime"

# $STATE holds the fake daemon's observable state:
#   active   - present while the unit is "running"
#   session  - one in-flight sandbox session; deleted by `systemctl stop`,
#              which is what KillMode=control-group does to the apptainer child
#   calls    - one line per stubbed command, the assertion surface
export STATE="$tmp/state"

# A non-empty SIF so the installer skips its artifact-download branch.
printf 'not a real sif' > "$runtime/agent-engine.sif"

# --- stubs ------------------------------------------------------------------

cat > "$tmp/bin/apptainer" <<'SH'
#!/bin/sh
echo "apptainer $*" >> "$STATE/calls"
SH

# Emulates the one docker invocation the installer makes: a `go build` writing
# to whatever `-o` names, inside the host directory bind-mounted at /out. The
# "compiled" bytes are $FAKE_BINARY, so a test can model a source change.
cat > "$tmp/bin/docker" <<'SH'
#!/bin/sh
echo "docker $*" >> "$STATE/calls"
out_host=""
out_path=""
prev=""
for arg in "$@"; do
  case "$prev" in
    -v) case "$arg" in *:/out) out_host="${arg%:/out}" ;; esac ;;
    -o) out_path="$arg" ;;
  esac
  prev="$arg"
done
[ -n "$out_host" ] || { echo "stub docker: no :/out bind mount in $*" >&2; exit 1; }
[ -n "$out_path" ] || { echo "stub docker: no -o in $*" >&2; exit 1; }
target="$out_host${out_path#/out}"
printf '%s' "${FAKE_BINARY:-baseline}" > "$target"
chmod 0755 "$target"
SH

# `stop` is deliberately destructive: it clears the active marker, unlinks the
# socket, AND deletes the in-flight session, because that is what stopping a
# KillMode=control-group unit does to the apptainer child holding the session.
cat > "$tmp/bin/systemctl" <<'SH'
#!/bin/sh
echo "systemctl $*" >> "$STATE/calls"
verb=""
for arg in "$@"; do
  case "$arg" in --user|--quiet|--no-pager|--lines=*|*.service) continue ;; esac
  case "$arg" in -p) continue ;; MainPID|--value) continue ;; esac
  [ -z "$verb" ] && verb="$arg"
done
case "$verb" in
  # `restart` is destructive here too, and modelling only its teardown half is
  # deliberate. The installer issues `stop` today, but rewriting that line as
  # `systemctl --user restart` is the obvious simplification and is exactly the
  # regression this file exists to catch. With no `restart` arm the stub used to
  # fall through to `exit 0` having changed nothing: the session marker
  # survived, `is-active` still reported active, every assertion passed green,
  # and the real unit had just killed every in-flight sandbox. A stub that also
  # re-created `active` would mask the kill again, so it does not.
  stop|restart)
    rm -f "$STATE/active" "$STATE/session" "$STATE/socket_path_marker"
    [ -n "${SOCKET_UNDER_TEST:-}" ] && rm -f "$SOCKET_UNDER_TEST"
    ;;
  is-active) [ -f "$STATE/active" ] || exit 3 ;;
  show) [ -f "$STATE/active" ] && cat "$STATE/active" || echo 0 ;;
  status) [ -f "$STATE/active" ] || exit 3 ;;
  # The general form of the same bug: any verb this stub has not been taught is
  # a loud failure rather than a silent success, so the next one to be added to
  # the installer cannot quietly pass through unmodelled.
  *) echo "stub systemctl: unhandled verb '$verb' in: $*" >&2; exit 64 ;;
esac
exit 0
SH

# Starts the fake daemon: a new incarnation id (so the test can prove the
# process was NOT replaced) plus a real AF_UNIX inode, since the installer waits
# on `[ -S "$SOCKET_PATH" ]`.
cat > "$tmp/bin/systemd-run" <<'SH'
#!/bin/sh
echo "systemd-run $*" >> "$STATE/calls"
rm -f "$STATE/wedged"
# A monotonic counter, not a random or time-derived value: two restarts inside
# the same second would collide and the "was the process replaced" assertions
# below would silently pass on a daemon that had in fact been killed.
n=$(cat "$STATE/incarnations" 2>/dev/null || echo 0)
n=$((n + 1))
echo "$n" > "$STATE/incarnations"
echo "$n" > "$STATE/active"
python3 - "$SOCKET_UNDER_TEST" <<'PY'
import socket, sys, os
path = sys.argv[1]
try:
    os.unlink(path)
except FileNotFoundError:
    pass
s = socket.socket(socket.AF_UNIX)
s.bind(path)
PY
exit 0
SH

# The only curl the installer makes once a SIF exists is the /health probe. A
# `wedged` marker models a daemon that is up but not answering; a restart clears
# it, exactly as restarting a wedged process would.
cat > "$tmp/bin/curl" <<'SH'
#!/bin/sh
echo "curl $*" >> "$STATE/calls"
[ -f "$STATE/active" ] || exit 7
[ -f "$STATE/wedged" ] && exit 7
echo '{"status":"ok"}'
SH

chmod +x "$tmp/bin/"*
export PATH="$tmp/bin:$PATH"
export SOCKET_UNDER_TEST="$runtime/run/engine.sock"

# --- harness ----------------------------------------------------------------

failures=0
fail() {
  echo "FAIL $1"
  shift
  [ $# -gt 0 ] && printf '%s\n' "$1" | sed 's/^/       /'
  failures=$((failures + 1))
}

# run_installer <label> <fake-binary-bytes> <llm-model> -> populates $out/$status
out=""
status=0
run_installer() {
  local label="$1" binary="$2" model="$3"
  : > "$STATE/calls"
  set +e
  out=$(env \
    FAKE_BINARY="$binary" \
    REPO_DIR="$repo_root" \
    RUNTIME_DIR="$runtime" \
    HIVE_AGENT_ENGINE_LLM_MODEL="$model" \
    HIVE_AGENT_ENGINE_LLM_BASE_URL="https://api-hive.example/v1" \
    HIVE_AGENT_ENGINE_LLM_API_KEY="test-key" \
    CONTROL_PLANE_INTERNAL_TOKEN="test-token" \
    bash "$installer" 2>&1)
  status=$?
  set -e
  if [ "$status" != 0 ]; then
    fail "[$label] installer exited $status" "$out"
  fi
}

# The stubs log full argv, so match the real shapes: `systemctl --user stop …`
# and `systemd-run --user --unit=… …`. Matching "systemctl stop" would never
# fire and every stop assertion below would pass vacuously.
stopped() { grep -qE '^systemctl .* (stop|restart) ' "$STATE/calls"; }
started() { grep -q '^systemd-run ' "$STATE/calls"; }
incarnation(){ cat "$STATE/active" 2>/dev/null || echo none; }

# --- case A: a first install starts the daemon -------------------------------

run_installer "A first-install" baseline openai/hive-default
if ! started; then
  fail "[A] first install did not start the unit" "$(cat "$STATE/calls")"
fi
if [ ! -S "$SOCKET_UNDER_TEST" ]; then
  fail "[A] no socket after the first install"
fi
[ $failures -eq 0 ] && echo "ok   [A] first install starts the daemon"

# --- case A2: the build must not be allowed to depend on the git revision ----
#
# `go build` stamps the VCS revision into the binary whenever it can, which
# would make the fingerprint below differ on every deploy from a different
# commit even when not one byte of apps/agent-engine changed, and the skip in
# case B would never fire again. This is a static contract check on the
# installer's own command line, not a measurement of the toolchain: what the
# toolchain actually does with and without the flag was measured by building
# the real binary from two commits that changed no agent-engine source, and is
# recorded in the pull request body because it needs a real Go toolchain, a
# real git repository and several minutes, none of which belong in a lint job.
# What this check is for is the regression the measurement cannot cover: the
# flag being dropped from the command line later.
before_a2=$failures
if ! grep -q -- '-buildvcs=false' "$STATE/calls"; then
  fail "[A2] the installer's go build does not pass -buildvcs=false" "$(grep '^docker ' "$STATE/calls" || true)"
fi
[ $failures -eq "$before_a2" ] && echo "ok   [A2] the build is pinned to the sources with -buildvcs=false"

# --- case A3: the artifacts this script leaves behind must stay narrow -------
#
# The suite asserted no file mode at all before this case existed, so dropping
# either `umask 077` block or any of the four explicit chmods would have gone
# green while leaving the model API key and the internal service token in a
# world readable engine.env on the box. The fingerprint file is here for the
# same reason: it was shipped 0644 while every sibling artifact in the same
# directory was 0600 or 0700.
before_a3=$failures
mode_of() { stat -c%a "$1" 2>/dev/null || echo missing; }
for spec in "$runtime/engine.env:600" "$runtime/bin/run-engine.sh:700" \
            "$runtime/engine.fingerprint:600" "$runtime/run:700"; do
  path="${spec%:*}"
  want="${spec##*:}"
  got=$(mode_of "$path")
  if [ "$got" != "$want" ]; then
    fail "[A3] $path is mode $got, expected $want"
  fi
done
[ $failures -eq "$before_a3" ] && echo "ok   [A3] the installed artifacts are not world readable"

# --- case B: THE GUARD. Unchanged artifacts must not kill an in-flight task ---

pid_before=$(incarnation)
printf 'task-in-flight' > "$STATE/session"

before_b=$failures
run_installer "B unchanged" baseline openai/hive-default
if [ ! -f "$STATE/session" ]; then
  fail "[B] the in-flight session was killed by an install that changed nothing" "$(cat "$STATE/calls")"
fi
if stopped; then
  fail "[B] the installer stopped a healthy daemon running identical artifacts" "$(cat "$STATE/calls")"
fi
if started; then
  fail "[B] the installer restarted a healthy daemon running identical artifacts" "$(cat "$STATE/calls")"
fi
if [ "$(incarnation)" != "$pid_before" ]; then
  fail "[B] the daemon process was replaced ($pid_before -> $(incarnation))"
fi
[ $failures -eq "$before_b" ] && echo "ok   [B] unchanged artifacts leave the daemon and its in-flight session alone"

# --- case C: a real launcher change must still restart -----------------------

before_c=$failures
run_installer "C changed-binary" rebuilt-different-bytes openai/hive-default
if ! stopped; then
  fail "[C] a changed launcher binary did not restart the daemon" "$(cat "$STATE/calls")"
fi
if ! started; then
  fail "[C] a changed launcher binary did not start a new daemon" "$(cat "$STATE/calls")"
fi
if [ "$(incarnation)" = "$pid_before" ]; then
  fail "[C] the daemon process was not replaced despite a changed binary"
fi
[ $failures -eq "$before_c" ] && echo "ok   [C] a changed binary still restarts"

# --- case D: a changed env file must restart ---------------------------------

before_d=$failures
pid_before=$(incarnation)
run_installer "D changed-env" rebuilt-different-bytes openai/some-other-alias
if ! stopped; then
  fail "[D] a changed env file did not restart the daemon" "$(cat "$STATE/calls")"
fi
if [ "$(incarnation)" = "$pid_before" ]; then
  fail "[D] the daemon process was not replaced despite a changed env file"
fi
[ $failures -eq "$before_d" ] && echo "ok   [D] a changed env file still restarts"

# --- case E: unchanged artifacts but an unhealthy daemon must restart --------

before_e=$failures
pid_before=$(incarnation)
touch "$STATE/wedged"
run_installer "E unhealthy" rebuilt-different-bytes openai/some-other-alias
if ! started; then
  fail "[E] an unhealthy daemon was left down because the fingerprint matched" "$(cat "$STATE/calls")"
fi
if [ "$(incarnation)" = "$pid_before" ]; then
  fail "[E] the unhealthy daemon was not replaced"
fi
[ $failures -eq "$before_e" ] && echo "ok   [E] an unhealthy daemon restarts even when the fingerprint matches"

# --- case F: the skip decision must survive a fresh checkout of the same tree -

before_f=$failures
pid_before=$(incarnation)
run_installer "F unchanged-again" rebuilt-different-bytes openai/some-other-alias
if stopped; then
  fail "[F] the second unchanged install after a restart still stopped the daemon" "$(cat "$STATE/calls")"
fi
if [ "$(incarnation)" != "$pid_before" ]; then
  fail "[F] the daemon was replaced on an unchanged install"
fi
[ $failures -eq "$before_f" ] && echo "ok   [F] the skip decision holds on the next unchanged install"

# --- case G: the skip must be decided against the INSTALLED artifacts --------
#
# The decision the previous shape of this script made was "do my freshly staged
# artifacts match the note I wrote at the end of the last successful run", which
# stays true no matter what happened to the installed files in between. So an
# installed binary that drifted, was corrupted, was edited by hand, or was left
# half-written by an earlier run that died between two of the three `mv`s still
# reported "unchanged" and skipped, leaving the box running something nobody
# had verified. Mutating an installed artifact behind the script's back is the
# only way to tell the two questions apart: a script that hashes its staging
# area passes this vacuously, a script that hashes what is installed restarts.
before_g=$failures
pid_before=$(incarnation)
printf 'drifted-behind-the-scripts-back' > "$runtime/bin/agent-engine"
run_installer "G installed-drift" rebuilt-different-bytes openai/some-other-alias
if ! stopped; then
  fail "[G] a drifted installed binary was reported unchanged and skipped" "$(cat "$STATE/calls")"
fi
if ! started; then
  fail "[G] a drifted installed binary did not start a new daemon" "$(cat "$STATE/calls")"
fi
if [ "$(incarnation)" = "$pid_before" ]; then
  fail "[G] the daemon process was not replaced despite a drifted installed binary"
fi
if [ "$(cat "$runtime/bin/agent-engine")" != "rebuilt-different-bytes" ]; then
  fail "[G] the drifted binary was not overwritten by the freshly built one" "$(cat "$runtime/bin/agent-engine")"
fi
[ $failures -eq "$before_g" ] && echo "ok   [G] drift in an installed artifact restarts instead of skipping"

# --- case H: and the same for the installed env file -------------------------
#
# Same defect, different artifact, and worth its own case because this is the
# one that carries the API key and the internal token: a hand-edited or
# truncated engine.env left the daemon running the old credentials while the
# script reported the box up to date.
before_h=$failures
pid_before=$(incarnation)
printf 'HIVE_AGENT_ENGINE_LLM_MODEL=tampered\n' > "$runtime/engine.env"
run_installer "H env-drift" rebuilt-different-bytes openai/some-other-alias
if ! stopped; then
  fail "[H] a drifted installed env file was reported unchanged and skipped" "$(cat "$STATE/calls")"
fi
if ! grep -q 'openai/some-other-alias' "$runtime/engine.env"; then
  fail "[H] the drifted env file was not re-rendered"
fi
if [ "$(incarnation)" = "$pid_before" ]; then
  fail "[H] the daemon process was not replaced despite a drifted env file"
fi
[ $failures -eq "$before_h" ] && echo "ok   [H] drift in the installed env file restarts instead of skipping"

if [ "$failures" -ne 0 ]; then
  echo "$failures check(s) failed"
  exit 1
fi
echo "all agent-engine restart-gate checks passed"
