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
#
# Since issue #1510 the stub is also TARGET aware. The installer now touches two
# units: the launcher service and the health timer, and it restarts that timer
# on every single run by design, because a timer holds no session state. A stub
# that ignored the target would let those timer restarts satisfy every "did the
# launcher restart" assertion below, and case B, the whole reason this file
# exists, would pass on a script that killed the daemon every deploy.
cat > "$tmp/bin/systemctl" <<'SH'
#!/bin/sh
echo "systemctl $*" >> "$STATE/calls"
verb=""
target=""
prop=""
prev=""
for arg in "$@"; do
  case "$arg" in
    --user|--quiet|--no-pager|--value|--lines=*|-p) prev="$arg"; continue ;;
    *.service|*.timer) target="$arg"; prev="$arg"; continue ;;
  esac
  if [ "$prev" = "-p" ]; then prop="$arg"; prev="$arg"; continue; fi
  [ -z "$verb" ] && verb="$arg"
  prev="$arg"
done

teardown() {
  rm -f "$STATE/active" "$STATE/session" "$STATE/socket_path_marker"
  # Stopping a transient unit also collects it (CollectMode=inactive-or-failed),
  # which is precisely what unshadows the on-disk unit file and lets the
  # installer's `enable` succeed on the line after. Modelling that here is what
  # makes case L below able to fail.
  rm -f "$STATE/transient"
  [ -n "${SOCKET_UNDER_TEST:-}" ] && rm -f "$SOCKET_UNDER_TEST"
}

# Starts the fake daemon: a new incarnation id (so the test can prove the
# process was NOT replaced) plus a real AF_UNIX inode, since the installer waits
# on `[ -S "$SOCKET_PATH" ]`.
startup() {
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
}

case "$verb" in
  # Neither reloading the manager nor enabling a unit touches a running
  # process, which is exactly why the installer is allowed to do both on the
  # skip path. Modelled as no-ops that mutate no daemon state, so a script that
  # smuggled a restart in behind one of them would still fail case B.
  daemon-reload) : ;;
  # `disable --now` stops as well as unlinking, and the normalize guard relies
  # on that, so the teardown half is modelled here exactly as it is for stop.
  # A `disable` arm that only unlinked would let the guard look like it had
  # taken the launcher down when it had not.
  disable)
    case "$target" in
      *.timer) rm -f "$STATE/timer_enabled" ;;
      *) rm -f "$STATE/enabled" ;;
    esac
    case " $* " in
      *" --now "*)
        case "$target" in
          *.timer) rm -f "$STATE/timer_active" ;;
          *) teardown ;;
        esac
        ;;
    esac
    ;;
  enable)
    # `enable` on a name a transient unit still holds is what systemd refuses,
    # so refuse it here too rather than let the installer's guard go untested.
    if [ -f "$STATE/transient" ] && [ "${target%.timer}" = "$target" ]; then
      echo "stub systemctl: unit $target is transient or generated" >&2
      exit 1
    fi
    # A box where enable reports success and does not take. Rare, but it is
    # precisely the state the installer used to paper over by PRINTING
    # is-enabled inside an echo instead of requiring it, so it has to be
    # expressible here or the assertion that replaced that echo has no guard.
    if [ -f "$STATE/enable_broken" ]; then exit 0; fi
    # Per target, not one shared marker. A single marker meant the health
    # timer's own enable satisfied every "was the launcher enabled" assertion,
    # so case M's check could not fail no matter what the installer did.
    case "$target" in
      *.timer) : > "$STATE/timer_enabled" ;;
      *) : > "$STATE/enabled" ;;
    esac
    ;;
  is-enabled)
    if [ -f "$STATE/transient" ]; then echo transient; exit 0; fi
    case "$target" in
      *.timer) [ -f "$STATE/timer_enabled" ] && { echo enabled; exit 0; } ;;
      *) [ -f "$STATE/enabled" ] && { echo enabled; exit 0; } ;;
    esac
    echo disabled; exit 1
    ;;
  # `restart` is destructive here too, and modelling only its teardown half is
  # deliberate. With no `restart` arm the stub used to fall through to `exit 0`
  # having changed nothing: the session marker survived, `is-active` still
  # reported active, every assertion passed green, and the real unit had just
  # killed every in-flight sandbox. A stub that also re-created `active` would
  # mask the kill again, so `stop` does not.
  stop|restart|start)
    case "$target" in
      # The health timer holds no sandbox session, so start and restart only
      # flip its own liveness marker and never touch the daemon.
      *.timer)
        case "$verb" in
          stop) rm -f "$STATE/timer_active" ;;
          start|restart) : > "$STATE/timer_active" ;;
        esac
        ;;
      *)
        case "$verb" in
          stop) teardown ;;
          start) startup ;;
          restart) teardown; startup ;;
        esac
        ;;
    esac
    ;;
  # Prints the state as well as setting the exit code, because the real one
  # does and the installer's supervision assertion reads the printed word. A
  # stub that only set an exit code made that assertion compare against an
  # empty string, which is a check that can only ever fail.
  #
  # Timer and launcher are tracked separately: they are independent units, and
  # a stub that answered for the launcher when asked about the timer would let
  # a script that never started the timer pass the assertion.
  is-active)
    # --quiet means print nothing and answer with the exit code only, and the
    # installer relies on that. A stub that printed anyway put stray output in
    # every run.
    quiet=0
    case " $* " in *" --quiet "*) quiet=1 ;; esac
    marker="$STATE/active"
    case "$target" in *.timer) marker="$STATE/timer_active" ;; esac
    if [ -f "$marker" ]; then
      [ "$quiet" = 1 ] || echo active
    else
      [ "$quiet" = 1 ] || echo inactive
      exit 3
    fi
    ;;
  show)
    case "$prop" in
      MainPID) [ -f "$STATE/active" ] && cat "$STATE/active" || echo 0 ;;
      Transient) [ -f "$STATE/transient" ] && echo yes || echo no ;;
      *) echo "stub systemctl: unhandled show property '$prop' in: $*" >&2; exit 64 ;;
    esac
    ;;
  status) [ -f "$STATE/active" ] || exit 3 ;;
  # The general form of the same bug: any verb this stub has not been taught is
  # a loud failure rather than a silent success, so the next one to be added to
  # the installer cannot quietly pass through unmodelled.
  *) echo "stub systemctl: unhandled verb '$verb' in: $*" >&2; exit 64 ;;
esac
exit 0
SH

# crontab is STUBBED, and that is not optional tidiness. The installer installs
# a cron entry for the staleness watchdog, and this suite runs on developer
# machines and CI runners that have a real crontab. Without this stub the first
# run of this file rewrote the developer's own user crontab to point at a
# $TMPDIR path that is deleted seconds later, which is both a live side effect
# on the host and a broken cron entry left behind. Measured, not theoretical:
# it happened on the first run.
#
# It is a full model rather than a no-op, so the cron half of the install is
# actually asserted below instead of silently skipped.
cat > "$tmp/bin/crontab" <<'SH'
#!/bin/sh
echo "crontab $*" >> "$STATE/calls"
case "${1:-}" in
  -l)
    [ -f "$STATE/crontab" ] || { echo "no crontab for stub" >&2; exit 1; }
    cat "$STATE/crontab"
    ;;
  -r) rm -f "$STATE/crontab" ;;
  -)
    # A crontab that reports success and does not take. Rare in the wild, and
    # the only way to express "the watchdog ended up absent" against an
    # installer that writes it on every run, which is exactly the state the
    # assertion has to catch.
    if [ -f "$STATE/cron_readonly" ]; then cat > /dev/null; exit 0; fi
    cat > "$STATE/crontab"
    ;;
  *)  echo "stub crontab: unhandled argument '${1:-}'" >&2; exit 64 ;;
esac
exit 0
SH

# The launcher must NOT go back to a transient `systemd-run` unit (issue
# #1510): transient units live in tmpfs, so a reboot erases the definition and
# nothing starts the launcher again, and CollectMode=inactive-or-failed deletes
# the unit rather than leaving a failed one behind to notice. A stub that
# quietly worked would let that regression land, so this one refuses.
cat > "$tmp/bin/systemd-run" <<'SH'
#!/bin/sh
echo "systemd-run $*" >> "$STATE/calls"
echo "stub systemd-run: the launcher must run under an enabled unit file, not a transient systemd-run unit (issue #1510)" >&2
exit 64
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
    XDG_CONFIG_HOME="$tmp/config" \
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

# The stubs log full argv, so match the real shapes: `systemctl --user stop
# hive-agent-engine.service`. Matching "systemctl stop" would never fire and
# every stop assertion below would pass vacuously.
#
# Both are pinned to the LAUNCHER service specifically. The installer restarts
# hive-agent-engine-health.timer on every run by design, so an unanchored match
# would report "started" on a run that only touched the watchdog, and case B
# would go green on a script that killed the daemon every deploy.
launcher_unit='hive-agent-engine\.service'
stopped() { grep -qE "^systemctl .* (stop|restart) $launcher_unit" "$STATE/calls"; }
started() { grep -qE "^systemctl .* (start|restart) $launcher_unit" "$STATE/calls"; }
enabled() { grep -qE "^systemctl .* enable $launcher_unit" "$STATE/calls"; }
incarnation(){ cat "$STATE/active" 2>/dev/null || echo none; }
unit_file="$tmp/config/systemd/user/hive-agent-engine.service"
health_service="$tmp/config/systemd/user/hive-agent-engine-health.service"
health_timer="$tmp/config/systemd/user/hive-agent-engine-health.timer"

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

# --- case I: an unreadable installed artifact restarts, it does not abort ----
#
# `fingerprint_of` reads the installed artifacts, and sha256sum exits 1 on a
# file it cannot read. Under `set -o pipefail` that aborts the installer before
# the restart-and-repair branch, and since the permission does not change by
# itself, every later deploy fails identically until someone fixes it by hand
# on the box. Restarting repairs it instead, because `mv -f` over the installed
# path needs write permission on the directory rather than on the file.
before_i=$failures
if [ "$(id -u)" = 0 ]; then
  echo "skip [I] unreadable-artifact case needs a non-root uid, running as root"
else
  pid_before=$(incarnation)
  chmod 000 "$runtime/bin/agent-engine"
  run_installer "I unreadable-artifact" rebuilt-different-bytes openai/some-other-alias
  if ! started; then
    fail "[I] an unreadable installed binary did not restart the daemon" "$(cat "$STATE/calls")"
  fi
  if [ "$(incarnation)" = "$pid_before" ]; then
    fail "[I] the daemon process was not replaced despite an unreadable installed binary"
  fi
  if [ ! -r "$runtime/bin/agent-engine" ]; then
    fail "[I] the unreadable binary was not replaced by a readable one"
  elif [ "$(cat "$runtime/bin/agent-engine")" != "rebuilt-different-bytes" ]; then
    fail "[I] the unreadable binary was not overwritten by the freshly built one"
  fi
  [ $failures -eq "$before_i" ] && echo "ok   [I] an unreadable installed artifact restarts instead of aborting"
fi

# --- case J: the launcher must be supervised, not transient (issue #1510) ----
#
# The launcher used to be started by `systemd-run --user --collect
# --property=Restart=on-failure`, which is four holes at once: the unit lives
# in tmpfs so a reboot erases it and nothing starts the launcher again,
# CollectMode deletes the unit instead of leaving a failed one to notice,
# on-failure does not cover a clean exit or SIGTERM, and the default start
# limit gives up after five attempts in ten seconds. `systemd-run` is stubbed
# to fail outright, so a regression to it aborts the installer; these
# assertions cover the properties that a merely-present unit file would not.
before_j=$failures
if [ ! -f "$unit_file" ]; then
  fail "[J] no launcher unit file was installed at $unit_file"
else
  grep -q '^Restart=always$'             "$unit_file" || fail "[J] the unit does not set Restart=always"
  grep -q '^StartLimitIntervalSec=0$'    "$unit_file" || fail "[J] the unit does not disable the start limit"
  grep -q '^WantedBy=default.target$'    "$unit_file" || fail "[J] the unit is not wanted by default.target, so it will not start at boot"
  grep -q "^ExecStart=$runtime/bin/run-engine.sh$" "$unit_file" \
    || fail "[J] the unit's ExecStart was not rendered to the installed entry script" "$(grep '^ExecStart=' "$unit_file" || true)"
  grep -q '@' "$unit_file" && fail "[J] an unsubstituted placeholder survived into the installed unit" "$(grep '@' "$unit_file")"
  # Apptainer's rootless launch goes through a setuid helper, so this line
  # being copied across from hive-box-backup.service would break every agent
  # task while leaving the unit itself perfectly healthy.
  grep -q '^NoNewPrivileges=' "$unit_file" \
    && fail "[J] the launcher unit sets NoNewPrivileges, which breaks apptainer's setuid starter"
fi
if ! enabled; then
  fail "[J] the launcher unit was never enabled, so it will not come back after a reboot" "$(cat "$STATE/calls")"
fi
[ $failures -eq "$before_j" ] && echo "ok   [J] the launcher runs under an enabled, always-restarting unit file"

# --- case K: the launcher being down must be loud without a person ----------
#
# Before this, the only way to learn the launcher was gone was to submit a task
# and watch it fail. The probe and its timer are installed unconditionally, and
# deliberately outside the fingerprint, so changing the watchdog never restarts
# the thing it watches.
before_k=$failures
[ -x "$runtime/bin/agent-engine-health-probe.sh" ] \
  || fail "[K] the health probe was not installed executable at $runtime/bin/agent-engine-health-probe.sh"
[ -f "$health_service" ] || fail "[K] no health probe service was installed at $health_service"
[ -f "$health_timer" ]   || fail "[K] no health probe timer was installed at $health_timer"
grep -q "^ExecStart=$runtime/bin/agent-engine-health-probe.sh$" "$health_service" 2>/dev/null \
  || fail "[K] the health service does not run the installed probe copy" "$(grep '^ExecStart=' "$health_service" 2>/dev/null || true)"
# The two Environment= lines, which nothing asserted. They carry @RUNTIME_DIR@
# and @UNIT_NAME@, and an unsubstituted RUNTIME_DIR would point every probe run
# at the wrong socket path and report the launcher down forever.
grep -q "^Environment=RUNTIME_DIR=$runtime$" "$health_service" 2>/dev/null \
  || fail "[K] the health service's RUNTIME_DIR was not rendered" "$(grep '^Environment=' "$health_service" 2>/dev/null || true)"
grep -q "^Environment=UNIT_NAME=hive-agent-engine$" "$health_service" 2>/dev/null \
  || fail "[K] the health service's UNIT_NAME was not rendered" "$(grep '^Environment=' "$health_service" 2>/dev/null || true)"
# Every rendered unit, not just the launcher, must be free of placeholders.
for rendered in "$unit_file" "$health_service" "$health_timer"; do
  if grep -qE '@[A-Z][A-Z_]*@' "$rendered" 2>/dev/null; then
    fail "[K] an unsubstituted placeholder survived into $rendered" "$(grep -oE '@[A-Z][A-Z_]*@' "$rendered" | sort -u | tr '\n' ' ')"
  fi
done
grep -qE '^systemctl .* enable hive-agent-engine-health\.timer' "$STATE/calls" \
  || fail "[K] the health timer was never enabled" "$(cat "$STATE/calls")"
grep -qE '^systemctl .* restart hive-agent-engine-health\.timer' "$STATE/calls" \
  || fail "[K] the health timer was never started" "$(cat "$STATE/calls")"
[ $failures -eq "$before_k" ] && echo "ok   [K] the health probe, its service and its timer are installed and enabled"

# --- case L: a changed unit definition must restart --------------------------
#
# The other direction of case B. A restart policy that is edited but never
# applied is worse than none, because the repository then states a guarantee
# the box does not provide. Only the fingerprint covering the unit file makes
# this fire.
before_l=$failures
pid_before=$(incarnation)
printf 'Restart=no\n' >> "$unit_file"
run_installer "L unit-drift" rebuilt-different-bytes openai/some-other-alias
if ! stopped; then
  fail "[L] a drifted unit file was reported unchanged and skipped" "$(cat "$STATE/calls")"
fi
if [ "$(incarnation)" = "$pid_before" ]; then
  fail "[L] the daemon was not replaced despite a drifted unit file"
fi
if grep -q '^Restart=no$' "$unit_file"; then
  fail "[L] the drifted unit file was not re-rendered"
fi
[ $failures -eq "$before_l" ] && echo "ok   [L] a changed unit definition restarts and is re-rendered"

# --- case M: the one-time migration off the old transient unit --------------
#
# What every existing box looks like on the first deploy after this lands: a
# transient unit from `systemd-run` still holds the name, and no unit file has
# ever been installed. systemd refuses `enable` while a transient unit shadows
# the on-disk file, so an installer that called it unguarded would abort the
# deploy on precisely the run that was supposed to fix the box. It has to skip
# the enable, stop the transient unit, and only then enable and start the real
# one.
before_m=$failures
pid_before=$(incarnation)
rm -f "$unit_file"
rm -f "$STATE/enabled"
touch "$STATE/transient"
run_installer "M transient-migration" rebuilt-different-bytes openai/some-other-alias
if ! stopped; then
  fail "[M] the transient unit was left in place" "$(cat "$STATE/calls")"
fi
if [ "$(incarnation)" = "$pid_before" ]; then
  fail "[M] the daemon was not replaced despite having no installed unit file"
fi
if ! enabled; then
  fail "[M] the launcher was never enabled after the transient unit was replaced" "$(cat "$STATE/calls")"
fi
if [ ! -f "$STATE/enabled" ]; then
  fail "[M] the enable call did not take effect"
fi
[ $failures -eq "$before_m" ] && echo "ok   [M] a box still on the transient unit migrates without aborting"

# --- case N: and the skip still holds once supervised ------------------------
#
# Cases J to M all left the daemon restarted. The property case B guards has to
# survive every one of those additions, so re-assert it as the last word rather
# than trusting that B still covers a script three features larger.
before_n=$failures
pid_before=$(incarnation)
printf 'task-in-flight' > "$STATE/session"
run_installer "N unchanged-after-supervision" rebuilt-different-bytes openai/some-other-alias
if [ ! -f "$STATE/session" ]; then
  fail "[N] the in-flight session was killed by an install that changed nothing" "$(cat "$STATE/calls")"
fi
if stopped || started; then
  fail "[N] a supervised daemon running identical artifacts was still restarted" "$(cat "$STATE/calls")"
fi
if [ "$(incarnation)" != "$pid_before" ]; then
  fail "[N] the daemon process was replaced on an unchanged install"
fi
[ $failures -eq "$before_n" ] && echo "ok   [N] the skip survives the supervision changes"

# --- cases O and P: the migration is safe from the OTHER direction too -----
#
# The direction cases A to N do not cover, and the one that actually broke the
# demo box on 2026-08-29. `systemd-run --unit=X` refuses while a fragment file
# for X exists, and `systemctl stop` does not unload a fragment, so a checkout
# that predates the unit file cannot deploy onto a box that has one. Every
# deploy from main failed at the launcher step and the launcher sat inactive
# with no process, which is both agent capabilities down. The kill and recover
# tests could never have caught it: this is coexistence, not resilience.
#
# scripts/normalize-agent-engine-unit.sh runs before the installer in
# deploy-demo-box.yml and hands the name back when, and only when, this
# checkout cannot manage a unit file.
normalizer="$repo_root/scripts/normalize-agent-engine-unit.sh"
run_normalizer() {
  local repo="$1"
  set +e
  out=$(env REPO_DIR="$repo" RUNTIME_DIR="$runtime" XDG_CONFIG_HOME="$tmp/config" \
    bash "$normalizer" 2>&1)
  status=$?
  set -e
}

# --- case O: on a checkout that ships the template, it must touch nothing ---
#
# This runs on every deploy of main forever. If it ever deleted a unit there,
# it would take the supervision off the box on the very deploy that installed
# it.
before_o=$failures
run_installer "O pre-normalize" rebuilt-different-bytes openai/some-other-alias
pid_before=$(incarnation)
run_normalizer "$repo_root"
[ "$status" = 0 ] || fail "[O] the normalizer failed on a checkout that ships the template" "$out"
[ -f "$unit_file" ] || fail "[O] the normalizer deleted the unit on a checkout that owns it" "$out"
[ -f "$health_timer" ] || fail "[O] the normalizer deleted the health timer on a checkout that owns it"
[ "$(incarnation)" = "$pid_before" ] || fail "[O] the normalizer stopped the daemon on a checkout that owns it"
[ -f "$runtime/engine.fingerprint" ] || fail "[O] the normalizer cleared the fingerprint on a checkout that owns it"
[ $failures -eq "$before_o" ] && echo "ok   [O] the normalizer is a no-op on a checkout that ships the unit template"

# --- case P: on a checkout without the template, it must free the name ------
#
# Modelled with a repo directory that has no deploy/systemd-user, which is
# exactly what a revert of the installer, a rollback deploy from older main, or
# a branch predating #1510 looks like from the box's point of view.
before_p=$failures
old_checkout="$tmp/old-checkout"
mkdir -p "$old_checkout/deploy/systemd-user"
# The pre-#1510 tree had this directory, holding only the backup units. The
# discriminator has to be the agent-engine template specifically, not the
# directory, or the guard would never fire on a real revert.
: > "$old_checkout/deploy/systemd-user/hive-box-backup.service"
run_normalizer "$old_checkout"
[ "$status" = 0 ] || fail "[P] the normalizer failed on a checkout without the template" "$out"
[ -f "$unit_file" ] && fail "[P] the launcher unit file survived, so systemd-run will still refuse the name" "$out"
[ -f "$health_service" ] && fail "[P] the health service file survived"
[ -f "$health_timer" ] && fail "[P] the health timer file survived"
[ -f "$runtime/engine.fingerprint" ] && fail "[P] the four artifact fingerprint survived, so the older installer would compare against a note about a different artifact set"
[ -f "$STATE/enabled" ] && fail "[P] the unit was left enabled, so a reboot would fight the transient unit for the name"
[ -f "$STATE/active" ] && fail "[P] the launcher was left running, so systemd-run would still find the name taken"
case "$out" in
  *"::warning::"*) : ;;
  *) fail "[P] deleting a systemd unit was not announced loudly" "$out" ;;
esac
[ $failures -eq "$before_p" ] && echo "ok   [P] the normalizer frees the name, loudly, on a checkout that cannot manage a unit file"

# --- case Q: and it stays quiet when there is nothing to hand back ----------
before_q=$failures
run_normalizer "$old_checkout"
[ "$status" = 0 ] || fail "[Q] the normalizer failed on a box with no unit file" "$out"
case "$out" in
  *"::warning::"*) fail "[Q] the normalizer warned about a box it had nothing to do to" "$out" ;;
esac
[ $failures -eq "$before_q" ] && echo "ok   [Q] the normalizer is quiet on a box with no unit file to remove"


# run_installer_variant runs the installer without asserting success, for the
# cases below whose whole point is that it must FAIL.
run_installer_variant() {
  : > "$STATE/calls"
  set +e
  out=$(env \
    FAKE_BINARY="rebuilt-different-bytes" \
    REPO_DIR="$repo_root" \
    RUNTIME_DIR="$runtime" \
    XDG_CONFIG_HOME="$tmp/config" \
    HIVE_AGENT_ENGINE_LLM_MODEL="openai/some-other-alias" \
    HIVE_AGENT_ENGINE_LLM_BASE_URL="https://api-hive.example/v1" \
    HIVE_AGENT_ENGINE_LLM_API_KEY="test-key" \
    CONTROL_PLANE_INTERNAL_TOKEN="test-token" \
    bash "$installer" 2>&1)
  status=$?
  set -e
}

# --- case R: an un-enabled unit is repaired without paying a restart --------
#
# The reason writing the unit, reloading and enabling all happen on every run
# rather than only on the restart path. A box whose unit was disabled by hand,
# or by a normalize run that a later deploy re-installed over, must come back
# to enabled on the next deploy, and must not lose an in-flight Cowork session
# to do it.
before_r=$failures
run_installer "R pre-disable" rebuilt-different-bytes openai/some-other-alias
pid_before=$(incarnation)
printf 'task-in-flight' > "$STATE/session"
rm -f "$STATE/enabled"
run_installer "R re-enable" rebuilt-different-bytes openai/some-other-alias
[ -f "$STATE/enabled" ] || fail "[R] a disabled unit was left disabled by an otherwise unchanged install" "$(cat "$STATE/calls")"
if stopped || started; then
  fail "[R] repairing the enable state cost a restart" "$(cat "$STATE/calls")"
fi
[ -f "$STATE/session" ] || fail "[R] the in-flight session was killed while repairing the enable state"
[ "$(incarnation)" = "$pid_before" ] || fail "[R] the daemon was replaced while repairing the enable state"
[ $failures -eq "$before_r" ] && echo "ok   [R] a disabled unit is re-enabled without restarting the daemon"

# --- case S: an enable that does not take must FAIL the deploy -------------
#
# THE guard for the reporting-instead-of-requiring defect. The installer used
# to print `$(systemctl --user is-enabled ...)` inside an echo. Command
# substitution in a word expansion does not trip `set -e`, and echo returns its
# own 0, so a box where enable silently did not take printed
# "supervision: ... is disabled" and the installer exited 0. The deploy went
# green over a launcher that would not survive a reboot, which is this issue's
# own defect reintroduced inside its fix.
before_s=$failures
touch "$STATE/enable_broken"
rm -f "$STATE/enabled"
run_installer_variant
if [ "$status" = 0 ]; then
  fail "[S] the installer reported success on a box where the unit is not enabled" "$out"
fi
case "$out" in
  *"::error::"*"not 'enabled'"*) : ;;
  *) fail "[S] the failure did not name the unit as not enabled" "$out" ;;
esac
rm -f "$STATE/enable_broken"
[ $failures -eq "$before_s" ] && echo "ok   [S] an enable that does not take fails the deploy instead of printing itself"

# --- case T: a transient unit must never reach the skip path silently -------
#
# The state cases A to N do not produce: the unit FILE is installed and its
# fingerprint matches, but a transient unit still holds the name, so the
# launcher that is actually running is not the one the file describes.
# Skipping there would leave the box on an unsupervised transient unit while
# the deploy reported the artifacts up to date, which reads as supervised and
# is not.
before_t=$failures
run_installer "T pre-transient" rebuilt-different-bytes openai/some-other-alias
touch "$STATE/transient"
run_installer_variant
# Either outcome is acceptable and both are loud: restart onto the unit file,
# or fail the deploy. What must not happen is a silent exit 0 that leaves the
# transient unit in place.
if [ "$status" = 0 ] && [ -f "$STATE/transient" ]; then
  fail "[T] the installer exited 0 leaving the launcher on an unsupervised transient unit" "$out"
fi
rm -f "$STATE/transient"
[ $failures -eq "$before_t" ] && echo "ok   [T] a transient unit is never silently accepted by the skip path"


# --- case U: the observer for the observer ---------------------------------
#
# The systemd timer cannot report its own death: if it stops, the probe never
# runs, it posts nothing, the firing alert lapses, and the silence reads as
# health. So the staleness lane runs from cron, a different scheduler under a
# different daemon. An earlier draft of this change had the staleness check
# living only inside the timed probe, which is not a staleness check at all.
before_u=$failures
run_installer "U cron" rebuilt-different-bytes openai/some-other-alias
if [ ! -f "$STATE/crontab" ]; then
  fail "[U] no cron entry was installed, so nothing observes the health timer" "$(cat "$STATE/calls")"
else
  grep -qF -- "--check" "$STATE/crontab" \
    || fail "[U] the cron entry does not run the staleness-only mode" "$(cat "$STATE/crontab")"
  grep -qF "$runtime/bin/agent-engine-health-probe.sh" "$STATE/crontab" \
    || fail "[U] the cron entry does not point at the installed probe copy" "$(cat "$STATE/crontab")"
  # cron runs the command through /bin/sh with almost no environment, so a
  # probe invoked from here falls back to its own compiled-in defaults unless
  # the install pins them. On a box using a non-default RUNTIME_DIR that means
  # reading a stamp path that does not exist, which the probe correctly treats
  # as a first-ever run and therefore never alerts about: a watchdog running
  # every fifteen minutes forever, reporting nothing. This suite always uses a
  # non-default RUNTIME_DIR, so it can see that.
  grep -qF "RUNTIME_DIR=$runtime " "$STATE/crontab" \
    || fail "[U] the cron entry does not pin RUNTIME_DIR, so it would watch the wrong stamp path in silence" "$(cat "$STATE/crontab")"
  grep -qF "UNIT_NAME=hive-agent-engine " "$STATE/crontab" \
    || fail "[U] the cron entry does not pin UNIT_NAME" "$(cat "$STATE/crontab")"
fi
[ $failures -eq "$before_u" ] && echo "ok   [U] a cron staleness watchdog observes the systemd timer"

# --- case U2: installing it twice must not accumulate entries --------------
#
# It runs on every deploy. An append-only install would leave one more copy of
# the line per merge, and cron would run the probe a dozen times a slot.
before_u2=$failures
run_installer "U2 cron-idempotent" rebuilt-different-bytes openai/some-other-alias
occurrences=$(grep -cF -- "--check" "$STATE/crontab" 2>/dev/null || echo 0)
[ "$occurrences" = 1 ] || fail "[U2] the cron entry appears $occurrences times after two installs" "$(cat "$STATE/crontab")"
[ $failures -eq "$before_u2" ] && echo "ok   [U2] reinstalling does not accumulate cron entries"

# --- case U3: an unrelated cron entry must survive -------------------------
#
# The install rewrites the crontab rather than appending to it, which is what
# makes U2 hold. Rewriting someone else's crontab is a much worse failure than
# a duplicated line, so the rewrite has to be surgical.
before_u3=$failures
printf '%s\n' "0 3 * * * /home/sakib/hive-backups/bin/backup-box.sh --check" > "$STATE/crontab"
run_installer "U3 cron-preserve" rebuilt-different-bytes openai/some-other-alias
grep -qF "backup-box.sh --check" "$STATE/crontab" \
  || fail "[U3] installing the watchdog deleted an unrelated cron entry" "$(cat "$STATE/crontab")"
grep -qF -- "agent-engine-health-probe.sh --check" "$STATE/crontab" \
  || fail "[U3] the watchdog entry was not added alongside the existing one" "$(cat "$STATE/crontab")"
[ $failures -eq "$before_u3" ] && echo "ok   [U3] an unrelated cron entry survives the install"

# --- case U4: a missing cron entry must FAIL the deploy --------------------
#
# Same shape as case S. Installing the backstop and then not requiring it would
# make its absence silent, which is the defect this whole change is about.
before_u4=$failures
rm -f "$STATE/crontab"
: > "$STATE/cron_readonly"
run_installer_variant
if [ "$status" = 0 ]; then
  fail "[U4] the installer reported success with no staleness watchdog installed" "$out"
fi
case "$out" in
  *"::error::"*"staleness watchdog"*) : ;;
  *) fail "[U4] the failure did not name the missing staleness watchdog" "$out" ;;
esac
rm -f "$STATE/cron_readonly"
[ $failures -eq "$before_u4" ] && echo "ok   [U4] a missing staleness watchdog fails the deploy"


# --- case V: editing the watchdog must NOT restart the thing it watches -----
#
# The inverse of case L, and the property that protects in-flight Cowork
# sessions. The installer states it deliberately: "The health probe and its two
# units are outside the fingerprint on purpose in the other direction: changing
# the watchdog must never restart the thing it watches." Until this case
# existed that was asserted only by a hand run recorded in the pull request
# body, so folding the health units into the fingerprint (an easy and
# superficially reasonable edit, since the launcher unit was just folded in)
# would have landed green here and started killing every in-flight sandbox on
# any watchdog edit.
before_v=$failures
run_installer "V pre-watchdog-drift" rebuilt-different-bytes openai/some-other-alias
pid_before=$(incarnation)
printf 'task-in-flight' > "$STATE/session"
printf '\n# drift\n' >> "$health_timer"
printf '\n# drift\n' >> "$health_service"
run_installer "V watchdog-drift" rebuilt-different-bytes openai/some-other-alias
if stopped || started; then
  fail "[V] editing the watchdog restarted the thing it watches" "$(cat "$STATE/calls")"
fi
[ "$(incarnation)" = "$pid_before" ] || fail "[V] the daemon was replaced by a watchdog-only change"
[ -f "$STATE/session" ] || fail "[V] an in-flight session was killed by a watchdog-only change"
# And the watchdog edit is still repaired, so this is not passing merely
# because the installer ignored the files.
grep -q '# drift' "$health_timer" && fail "[V] the drifted watchdog unit was not re-rendered"
[ $failures -eq "$before_v" ] && echo "ok   [V] changing the watchdog does not restart the launcher"

if [ "$failures" -ne 0 ]; then
  echo "$failures check(s) failed"
  exit 1
fi
echo "all agent-engine restart-gate checks passed"
