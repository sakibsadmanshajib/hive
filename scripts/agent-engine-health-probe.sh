#!/usr/bin/env bash
# Health probe for the agent-engine host launcher, for issue #1510.
#
# Why this exists: control-plane hands every agent task launch to a bare host
# process over a Unix socket, and before this probe the only way to learn that
# process was gone was to submit a task and watch it fail. On a demo day that
# is the first time it matters. Cowork and the coding agent are two of the six
# locked demo capabilities, so an unsupervised, unwatched launcher was holding
# up a third of the demo.
#
# Run every five minutes by hive-agent-engine-health.timer. Silent when
# healthy; posts to Alertmanager when not.
#
# Two alerts, and TWO lanes, which is the part that took a correction to get
# right:
#
#   HiveAgentEngineDown       the launcher is not answering right now.
#   HiveAgentEngineProbeStale no probe has SUCCEEDED in longer than it should
#                             have, so a scheduled run did not happen.
#
# The staleness alert only means anything if something still runs when the
# five minute systemd timer does not. A staleness check that lives only inside
# the timed probe cannot fire when the timer stops: the probe never runs, it
# posts nothing, any firing alert auto-resolves through Alertmanager's
# resolve_timeout, and the absence reads as health. That is the exact shape
# this repository has already been bitten by, and an earlier draft of this file
# had it.
#
# So this script has two modes, the same split scripts/backup-box.sh already
# uses on this box for the same reason:
#
#   (no argument)  full probe. Checks the launcher, alerts if it is down,
#                  writes the last-success stamp. Run by
#                  hive-agent-engine-health.timer every five minutes.
#   --check        staleness only. Reads the stamp, alerts if it is old,
#                  touches nothing and probes nothing. Run by a CRON entry,
#                  deliberately a different scheduler from the systemd user
#                  timer above, so the death of that timer, or of the whole
#                  systemd user manager, still surfaces.
#
# What the pair still does NOT cover, stated rather than implied: if cron and
# the user manager are both dead the box is dead, and that is
# external-uptime-probe.yml's job, from outside this box's own network path.
#
# The channel is the one scripts/backup-box.sh already proved end to end:
# Alertmanager's v2 API on the published host port, its existing routing tree
# and the hive-ops email receiver (PR #998). No new component is deployed and
# the alert reaches a mailbox rather than a dashboard nobody opens.
#
# Environment (all optional, all defaulted):
#   RUNTIME_DIR       launcher state dir      (default /home/sakib/agent-runtime)
#   UNIT_NAME         launcher unit base name (default hive-agent-engine)
#   ALERTMANAGER_URL  alertmanager base URL   (default http://localhost:9093)
#   STALE_AFTER       seconds before a missing run is loud (default 900)
#
# Handles no secrets and prints none: everything it touches is a Unix socket
# health endpoint, a timestamp file and a fixed-literal alert body.
set -euo pipefail

MODE=full
case "${1:-}" in
  '') ;;
  --check) MODE=check ;;
  *) echo "usage: $(basename "$0") [--check]" >&2; exit 2 ;;
esac

RUNTIME_DIR="${RUNTIME_DIR:-/home/sakib/agent-runtime}"
UNIT_NAME="${UNIT_NAME:-hive-agent-engine}"
ALERTMANAGER_URL="${ALERTMANAGER_URL:-http://localhost:9093}"
# Three missed five-minute slots. Tight enough that a dead timer surfaces
# inside a quarter of an hour, loose enough that one slow or skipped run does
# not cry wolf. A monitor that cries wolf gets muted within a day, and a muted
# monitor is worse than none because it still reads as coverage.
STALE_AFTER="${STALE_AFTER:-900}"

SOCKET_PATH="$RUNTIME_DIR/run/engine.sock"
STAMP_PATH="$RUNTIME_DIR/health.last-success-epoch"

# ---------------------------------------------------------------------------
# Alertmanager direct post, the same shape scripts/backup-box.sh uses. Fixed
# literal strings only in labels and text, so nothing user-controlled and
# nothing sensitive can reach the email body.
# ---------------------------------------------------------------------------
# Set by post_alert when a POST does not land. The caller uses it to decide
# whether it may advance the last-success stamp, because advancing it after an
# undelivered staleness alert would erase the only record that the alert was
# owed.
alert_delivery_failed=0

post_alert() {
  local alertname="$1" description="$2" body safe_desc ends_at
  # Escape JSON-significant characters. NOT a latent foot-gun guard: both call
  # sites below interpolate live values (an age in seconds, a systemd unit
  # state, a socket path), so this is load bearing today. An earlier version of
  # this comment claimed the call sites were fixed literals, which was wrong
  # when it was written.
  safe_desc="${description//\\/\\\\}"
  safe_desc="${safe_desc//\"/\\\"}"
  safe_desc="${safe_desc//$'\n'/ }"

  # An explicit endsAt, rather than leaning on Alertmanager's resolve_timeout.
  #
  # resolve_timeout defaults to five minutes and this probe runs every five
  # minutes, so an alert posted at T expires at almost exactly the moment the
  # next post is due. Any jitter at all, and systemd timers have some by
  # default, puts the expiry first: the alert resolves, the next run re-fires
  # it, and hive-ops gets a resolved mail and a firing mail every five minutes
  # for as long as the launcher stays down. A monitor that mails a flapping
  # pair is muted within a day.
  #
  # Three intervals of headroom, so two consecutive posts can be late or lost
  # before the alert lapses, and the resolution still arrives within fifteen
  # minutes of the launcher actually coming back.
  ends_at="$(date -u -d "@$(( $(date +%s) + 900 ))" +%Y-%m-%dT%H:%M:%SZ)"

  # Alertmanager v2 expects an ARRAY of alerts; a bare object is a 400.
  body=$(printf '[{"labels":{"alertname":"%s","severity":"critical","job":"agent-engine","instance":"hive-demo"},"annotations":{"description":"%s"},"endsAt":"%s"}]' \
    "$alertname" "$safe_desc" "$ends_at")
  # A delivery failure must be visible in the journal but must not mask the
  # underlying condition, so it is reported rather than allowed to abort the
  # script under `set -e`. It is also RECORDED, because a dropped alert that
  # the next run then makes unrepeatable is a signal lost for good.
  if ! curl -fsS -m 10 -XPOST "$ALERTMANAGER_URL/api/v2/alerts" \
      -H 'Content-Type: application/json' -d "$body" >/dev/null; then
    echo "WARN: alertmanager post failed for $alertname" >&2
    alert_delivery_failed=1
  fi
}

now=$(date +%s)

# ---------------------------------------------------------------------------
# Staleness first, so a half-dead schedule still alerts even when the launcher
# itself is currently fine. Checked BEFORE the probe runs, against the stamp
# the PREVIOUS run left, which is what makes a run that never happened
# observable at all.
#
# A missing stamp is first-ever-run, not staleness: alerting on it would fire
# once on every fresh box install for no reason.
# ---------------------------------------------------------------------------
if [ -f "$STAMP_PATH" ]; then
  last=$(cat "$STAMP_PATH" 2>/dev/null || echo 0)
  case "$last" in
    ''|*[!0-9]*) last=0 ;;
  esac
  # 10# forces base ten. Bash arithmetic reads a leading zero as octal, so a
  # truncated or half-written stamp beginning with 0 either evaluates to the
  # wrong number or, on a digit of 8 or 9, is a fatal arithmetic error that
  # would abort this script under `set -e` before it checked the launcher at
  # all. Taking the whole watchdog offline is the one outcome this file exists
  # to prevent, and the digit filter above cannot catch it because "08" is all
  # digits.
  age=$(( now - 10#$last ))
  if [ "$age" -gt "$STALE_AFTER" ]; then
    post_alert "HiveAgentEngineProbeStale" \
      "The agent-engine launcher health probe on the demo box last succeeded ${age}s ago, over the ${STALE_AFTER}s threshold. A scheduled probe run did not happen, so treat the launcher as unverified rather than healthy. Check: systemctl --user list-timers hive-agent-engine-health.timer"
    echo "stale: last success ${age}s ago exceeds ${STALE_AFTER}s"
    stale=1
  fi
fi

# --check is the cron lane, and it stops here. It deliberately does not probe
# the launcher and does not write the stamp: its only job is to notice that the
# systemd timer has stopped producing successes, and a second lane that also
# wrote the stamp would keep the stamp fresh forever and hide exactly the
# condition it exists to find.
if [ "$MODE" = check ]; then
  if [ "${stale:-0}" = 1 ]; then
    exit 1
  fi
  echo "check ok: the timed probe is producing successes"
  exit 0
fi

# ---------------------------------------------------------------------------
# The probe itself. Three independent conditions, reported separately, because
# "the unit is active but the socket is gone" and "the unit is not running at
# all" send an operator to different places.
# ---------------------------------------------------------------------------
reason=""
if ! systemctl --user is-active --quiet "$UNIT_NAME.service"; then
  reason="the $UNIT_NAME.service user unit is not active (state: $(systemctl --user is-active "$UNIT_NAME.service" 2>&1 || true))"
elif [ ! -S "$SOCKET_PATH" ]; then
  reason="the unit is active but there is no socket at $SOCKET_PATH"
elif ! curl -fsS --max-time 10 --unix-socket "$SOCKET_PATH" http://localhost/health >/dev/null; then
  reason="the socket at $SOCKET_PATH exists but did not answer /health within 10s"
fi

if [ -n "$reason" ]; then
  post_alert "HiveAgentEngineDown" \
    "The agent-engine host launcher on the demo box is not serving: ${reason}. Every agent task, Cowork and coding agent both, fails while this is true. Check: systemctl --user status $UNIT_NAME.service and journalctl --user -u $UNIT_NAME -n 100"
  echo "DOWN: $reason"
  exit 1
fi

# Only a successful probe advances the stamp, so the staleness check above
# measures time since the launcher was last known good rather than time since
# this script last ran.
#
# And not even then, if a staleness alert was owed and did not land. Advancing
# the stamp after a dropped POST would make the next run compute a fresh age,
# find nothing to report, and the alert that Alertmanager never received would
# be one nobody ever hears about. Leaving the stamp alone costs one extra
# staleness alert on the next run and keeps the signal repeatable.
if [ "$alert_delivery_failed" = 1 ]; then
  echo "WARN: not advancing the last-success stamp, an alert was owed and did not reach alertmanager" >&2
  exit 1
fi
printf '%s\n' "$now" > "$STAMP_PATH"
echo "ok: launcher healthy on $SOCKET_PATH"
