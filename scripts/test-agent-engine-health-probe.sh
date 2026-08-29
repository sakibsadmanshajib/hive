#!/usr/bin/env bash
# Regression guard for scripts/agent-engine-health-probe.sh (issue #1510).
#
# The probe is the only thing that reports the agent-engine launcher being down
# without a person submitting a task and watching it fail, so the ways it can
# be quietly wrong are all the same shape: it stays silent when it should not.
# Three of them are covered here because none of them would show up on the box
# until the day they mattered.
#
#   1. It reports healthy on a launcher that is not serving. Each of the three
#      conditions is asserted separately, because a probe that only checked the
#      unit state would report green on a wedged daemon and a probe that only
#      curled the socket would report green with the unit gone if a stale
#      socket file were left behind.
#   2. It treats its own absence as health. A probe run that never happened
#      posts nothing, any firing alert auto-resolves through Alertmanager's
#      resolve_timeout, and the silence reads as coverage. The staleness arm is
#      what makes an absent run loud, so it is asserted in both directions.
#   3. It advances its own last-success stamp on a run where the launcher was
#      down, which would make the staleness arm above permanently blind.
#
# No systemd, no launcher and no Alertmanager needed: `systemctl` and `curl` are
# stubbed on PATH, the same trick scripts/test-agent-engine-restart-gate.sh
# uses. The curl stub captures every posted alert body so the assertions read
# what would actually have been sent.
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
probe="$repo_root/scripts/agent-engine-health-probe.sh"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/bin" "$tmp/state"
runtime="$tmp/runtime"
mkdir -p "$runtime/run"

export STATE="$tmp/state"
stamp="$runtime/health.last-success-epoch"
posted="$STATE/posted"

cat > "$tmp/bin/systemctl" <<'SH'
#!/bin/sh
# Only ever asked `is-active`. Anything else is a probe that grew a new
# dependency without this stub learning about it, and that must be loud.
case " $* " in
  *" is-active "*) [ -f "$STATE/unit_active" ] || exit 3 ;;
  *) echo "stub systemctl: unexpected call: $*" >&2; exit 64 ;;
esac
exit 0
SH

# Two very different calls arrive here: the /health probe over the Unix socket,
# and the Alertmanager POST. Telling them apart on --unix-socket keeps the
# alert capture from swallowing health probes and vice versa.
cat > "$tmp/bin/curl" <<'SH'
#!/bin/sh
case " $* " in
  *" --unix-socket "*)
    [ -f "$STATE/health_ok" ] || exit 7
    echo '{"status":"ok"}'
    exit 0
    ;;
esac
# Alertmanager. Capture the body so the test can assert on the alertname that
# would really have been sent, not merely that some POST happened.
prev=""
for arg in "$@"; do
  [ "$prev" = "-d" ] && printf '%s\n' "$arg" >> "$STATE/posted"
  prev="$arg"
done
[ -f "$STATE/alertmanager_down" ] && exit 7
exit 0
SH

chmod +x "$tmp/bin/"*
export PATH="$tmp/bin:$PATH"

failures=0
fail() {
  echo "FAIL $1"
  shift
  [ $# -gt 0 ] && printf '%s\n' "$1" | sed 's/^/       /'
  failures=$((failures + 1))
}

out=""
status=0
run_probe() {
  : > "$posted"
  set +e
  out=$(env \
    RUNTIME_DIR="$runtime" \
    UNIT_NAME="hive-agent-engine" \
    ALERTMANAGER_URL="http://localhost:9093" \
    STALE_AFTER=900 \
    bash "$probe" 2>&1)
  status=$?
  set -e
}

alerted() { grep -q "\"alertname\":\"$1\"" "$posted"; }

# A real AF_UNIX inode, not a plain file: the probe asserts `[ -S ... ]`, which
# a `touch`ed regular file would fail, and the "no socket" arm would then be the
# only one this suite ever exercised.
bind_socket() {
  python3 - "$runtime/run/engine.sock" <<'PY'
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

healthy_launcher() { touch "$STATE/unit_active" "$STATE/health_ok"; bind_socket; }

# --- case A: a healthy launcher is silent and stamps itself -----------------

before_a=$failures
healthy_launcher
rm -f "$stamp"
run_probe
[ "$status" = 0 ] || fail "[A] the probe failed against a healthy launcher" "$out"
[ -s "$posted" ] && fail "[A] a healthy launcher posted an alert" "$(cat "$posted")"
[ -f "$stamp" ] || fail "[A] a successful probe did not write its last-success stamp"
# A missing stamp is a first-ever run, not staleness. Alerting on it would fire
# once on every fresh box install for no reason at all.
alerted HiveAgentEngineProbeStale && fail "[A] the first run with no stamp reported itself stale"
[ $failures -eq "$before_a" ] && echo "ok   [A] a healthy launcher is silent and records its success"

# --- case B: the unit not running is loud ------------------------------------

before_b=$failures
rm -f "$STATE/unit_active"
stamp_before=$(cat "$stamp")
run_probe
[ "$status" = 0 ] && fail "[B] the probe exited 0 with the launcher unit not running" "$out"
alerted HiveAgentEngineDown || fail "[B] no HiveAgentEngineDown alert was posted" "$out"
# The whole staleness arm goes blind if a failed run advances the stamp.
[ "$(cat "$stamp")" = "$stamp_before" ] \
  || fail "[B] a failed probe advanced its own last-success stamp"
[ $failures -eq "$before_b" ] && echo "ok   [B] an inactive unit posts HiveAgentEngineDown and does not stamp"

# --- case C: an active unit with no socket is loud --------------------------
#
# Separate from B because it sends an operator somewhere else: the launcher
# process is alive and something removed or never created its socket.
before_c=$failures
touch "$STATE/unit_active"
rm -f "$runtime/run/engine.sock"
run_probe
[ "$status" = 0 ] && fail "[C] the probe exited 0 with no socket present" "$out"
alerted HiveAgentEngineDown || fail "[C] a missing socket posted no alert" "$out"
case "$out" in *"no socket"*) : ;; *) fail "[C] the alert did not name the missing socket" "$out" ;; esac
[ $failures -eq "$before_c" ] && echo "ok   [C] an active unit with no socket posts HiveAgentEngineDown"

# --- case D: a wedged daemon is loud ----------------------------------------
#
# The case a unit-state-only check would report green on: the process is up,
# systemd is satisfied, and it answers nothing.
before_d=$failures
bind_socket
rm -f "$STATE/health_ok"
run_probe
[ "$status" = 0 ] && fail "[D] the probe exited 0 against a daemon that did not answer /health" "$out"
alerted HiveAgentEngineDown || fail "[D] a wedged daemon posted no alert" "$out"
case "$out" in *"/health"*) : ;; *) fail "[D] the alert did not name the health endpoint" "$out" ;; esac
[ $failures -eq "$before_d" ] && echo "ok   [D] a wedged daemon posts HiveAgentEngineDown"

# --- case E: THE GUARD. An absent probe run must read as down ---------------
#
# Without this arm, a probe that stops running produces silence, the firing
# alert resolves itself through resolve_timeout, and the absence reads as
# health. That is the exact failure this repository has already been bitten by,
# so it gets an explicit test rather than a comment.
before_e=$failures
healthy_launcher
printf '%s\n' "$(( $(date +%s) - 1000 ))" > "$stamp"
run_probe
alerted HiveAgentEngineProbeStale \
  || fail "[E] a last success older than the threshold did not post HiveAgentEngineProbeStale" "$out"
# Staleness is about the schedule, not the launcher, so a stale stamp on a
# currently healthy launcher must not also claim the launcher is down.
alerted HiveAgentEngineDown \
  && fail "[E] a stale stamp on a healthy launcher wrongly claimed the launcher was down" "$(cat "$posted")"
[ "$status" = 0 ] || fail "[E] a stale stamp on a healthy launcher should still exit 0" "$out"
[ $failures -eq "$before_e" ] && echo "ok   [E] a probe run that did not happen reads as down, not as silence"

# --- case F: and a fresh stamp does not cry wolf ----------------------------
#
# The other half of E. A monitor that fires on an ordinary run gets muted
# within a day, and a muted monitor is worse than none because it still reads
# as coverage.
before_f=$failures
run_probe
alerted HiveAgentEngineProbeStale && fail "[F] a fresh stamp reported itself stale" "$(cat "$posted")"
[ "$status" = 0 ] || fail "[F] the probe failed on a fresh healthy run" "$out"
[ $failures -eq "$before_f" ] && echo "ok   [F] a fresh stamp stays quiet"

# --- case G: a corrupt stamp must not silently disable the staleness arm ----
#
# `$(( now - garbage ))` is a fatal arithmetic error under `set -e`, which would
# abort the probe before it ever checked the launcher: a truncated or
# half-written stamp would take the whole watchdog offline silently.
before_g=$failures
printf 'not-a-number\n' > "$stamp"
run_probe
alerted HiveAgentEngineProbeStale || fail "[G] a corrupt stamp did not read as stale" "$out"
[ "$status" = 0 ] || fail "[G] a corrupt stamp aborted the probe instead of reading as stale" "$out"
[ $failures -eq "$before_g" ] && echo "ok   [G] a corrupt stamp reads as stale rather than aborting the probe"

# --- case H: a dead Alertmanager must not hide the underlying failure -------
before_h=$failures
rm -f "$STATE/unit_active"
touch "$STATE/alertmanager_down"
run_probe
[ "$status" = 0 ] && fail "[H] an undeliverable alert turned a down launcher into a green probe" "$out"
case "$out" in *"alertmanager post failed"*) : ;; *) fail "[H] the delivery failure was not reported" "$out" ;; esac
rm -f "$STATE/alertmanager_down"
[ $failures -eq "$before_h" ] && echo "ok   [H] a failed alert delivery is reported and does not mask the failure"

if [ "$failures" -ne 0 ]; then
  echo "$failures check(s) failed"
  exit 1
fi
echo "all agent-engine health-probe checks passed"
