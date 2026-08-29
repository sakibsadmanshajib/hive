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
# Two alerts, and the second one is the point:
#
#   HiveAgentEngineDown       the launcher is not answering right now.
#   HiveAgentEngineProbeStale this probe itself has not succeeded in longer
#                             than it should have, so a run did not happen.
#
# Without the second, a probe that stops running produces silence, any firing
# alert auto-resolves through Alertmanager's resolve_timeout, and the absence
# reads as health. That is the exact shape this repository has already been
# bitten by, so absence is made loud here rather than left implicit.
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
post_alert() {
  local alertname="$1" description="$2" body safe_desc
  # Escape JSON-significant characters so a description that ever gains
  # interpolated content cannot produce malformed JSON. All current call sites
  # are fixed literals; this is the latent foot-gun guard.
  safe_desc="${description//\\/\\\\}"
  safe_desc="${safe_desc//\"/\\\"}"
  safe_desc="${safe_desc//$'\n'/ }"
  # Alertmanager v2 expects an ARRAY of alerts; a bare object is a 400.
  body=$(printf '[{"labels":{"alertname":"%s","severity":"critical","job":"agent-engine","instance":"hive-demo"},"annotations":{"description":"%s"}}]' \
    "$alertname" "$safe_desc")
  # A delivery failure must be visible in the journal but must not mask the
  # underlying condition, so it is reported and swallowed rather than allowed
  # to abort the probe under `set -e`.
  if ! curl -fsS -m 10 -XPOST "$ALERTMANAGER_URL/api/v2/alerts" \
      -H 'Content-Type: application/json' -d "$body" >/dev/null; then
    echo "WARN: alertmanager post failed for $alertname" >&2
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
  age=$(( now - last ))
  if [ "$age" -gt "$STALE_AFTER" ]; then
    post_alert "HiveAgentEngineProbeStale" \
      "The agent-engine launcher health probe on the demo box last succeeded ${age}s ago, over the ${STALE_AFTER}s threshold. A scheduled probe run did not happen, so treat the launcher as unverified rather than healthy. Check: systemctl --user list-timers hive-agent-engine-health.timer"
    echo "stale: last success ${age}s ago exceeds ${STALE_AFTER}s"
  fi
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
printf '%s\n' "$now" > "$STAMP_PATH"
echo "ok: launcher healthy on $SOCKET_PATH"
