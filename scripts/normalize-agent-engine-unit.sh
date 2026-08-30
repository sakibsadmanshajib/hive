#!/usr/bin/env bash
# Hand the hive-agent-engine.service name back to whichever installer THIS
# checkout actually ships, for issue #1510.
#
# ── The trap this exists to disarm ────────────────────────────────────────
#
# Before #1510 the launcher was started by `systemd-run --user
# --unit=hive-agent-engine`, which creates a TRANSIENT unit. #1510 installs a
# persistent unit file of the same name. Those two cannot coexist, and the
# incompatibility is one directional in a way that is easy to get wrong:
#
#   new installer, box carries a transient unit  -> fine. It stops the
#       transient unit, which also collects it, and then owns the name.
#   old installer, box carries a unit FILE       -> fatal. systemd refuses
#       with "Failed to start transient service unit: Unit
#       hive-agent-engine.service was already loaded or has a fragment file",
#       and `systemctl stop` does not help, because stopping a unit does not
#       unload its fragment.
#
# That second direction took the demo box's deploy down on 2026-08-29: every
# run of deploy-demo-box.yml from a checkout without the unit file failed at
# the launcher step, and the launcher sat inactive with no process at all,
# which is both agent capabilities down. The failure surfaced in a workflow
# nobody was watching rather than in the change that caused it.
#
# ── What this does about it ───────────────────────────────────────────────
#
# It answers one question before the installer runs: does the checkout about
# to run own the unit file, or not? That is decided on a fact about the
# checkout, the presence of the template, rather than by grepping the
# installer for a marker that a refactor would move. If the checkout does not
# ship the template, its installer is the pre-#1510 one, it will reach for
# `systemd-run`, and any unit file on the box is a landmine in its path. So
# take the landmine out and let it have the name.
#
# Deliberately a no-op in the normal direction. On a checkout that ships the
# template, which is every checkout of main once #1510 lands, this prints one
# line and exits.
#
# ── What it does NOT cover, stated rather than implied ───────────────────
#
# A full revert of #1510 reverts this script along with the installer, so
# nothing in that commit can protect the deploy that follows it. If that ever
# happens, the manual clear is:
#
#   systemctl --user disable --now hive-agent-engine-health.timer
#   systemctl --user disable --now hive-agent-engine.service
#   rm -f ~/.config/systemd/user/hive-agent-engine*.service \
#         ~/.config/systemd/user/hive-agent-engine*.timer
#   systemctl --user daemon-reload
#
# What this script does cover is every partial shape, which is the realistic
# one: a revert of the installer alone, a rollback deploy from an older main,
# a branch deploy, and a box that was migrated by hand ahead of the merge.
set -euo pipefail

REPO_DIR="${REPO_DIR:-/home/sakib/hive}"
RUNTIME_DIR="${RUNTIME_DIR:-/home/sakib/agent-runtime}"
UNIT_NAME="hive-agent-engine"
HEALTH_NAME="hive-agent-engine-health"
UNIT_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
TEMPLATE="$REPO_DIR/deploy/systemd-user/$UNIT_NAME.service"

# The same two variables install-agent-engine-host.sh pins, for the same
# reason: a CI step has neither set, and `systemctl --user` cannot reach the
# user manager without them.
export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
export DBUS_SESSION_BUS_ADDRESS="${DBUS_SESSION_BUS_ADDRESS:-unix:path=$XDG_RUNTIME_DIR/bus}"

if [ -f "$TEMPLATE" ]; then
  echo "normalize: this checkout ships $UNIT_NAME.service, so its installer owns the name; nothing to hand back"
  exit 0
fi

if [ ! -f "$UNIT_DIR/$UNIT_NAME.service" ]; then
  echo "normalize: this checkout predates the supervised unit and the box carries no unit file; nothing to hand back"
  exit 0
fi

# Loud, and deliberately so. This deletes a systemd unit and stops a service,
# and a guard that does that quietly is a new way to be surprised. ::warning::
# puts it in the run's annotation summary as well as the log, so it is visible
# to someone scrolling a green deploy rather than only to someone reading it.
echo "::warning::agent-engine unit normalize FIRED: this checkout predates the supervised unit (no $TEMPLATE) but the box carries $UNIT_DIR/$UNIT_NAME.service. Removing the unit files so this checkout's installer can create its transient unit. The launcher is supervised only while a checkout that ships the template is deployed."
echo "normalize: ==================================================================="
echo "normalize: HANDING THE $UNIT_NAME.service NAME BACK TO systemd-run"
echo "normalize: reason: no template at $TEMPLATE, so this checkout's installer"
echo "normalize:         cannot manage a unit file and will call systemd-run,"
echo "normalize:         which refuses while a fragment file of that name exists."
echo "normalize: launcher state before: $(systemctl --user is-active "$UNIT_NAME.service" 2>&1 || true) / $(systemctl --user is-enabled "$UNIT_NAME.service" 2>&1 || true)"
echo "normalize: files found on the box:"
ls -l "$UNIT_DIR/$UNIT_NAME.service" \
      "$UNIT_DIR/$HEALTH_NAME.service" \
      "$UNIT_DIR/$HEALTH_NAME.timer" 2>/dev/null | sed 's/^/normalize:   /' || true

# Order matters. The watchdog goes first, so it cannot alert about a launcher
# that is deliberately down for the next few seconds of this deploy.
systemctl --user disable --now "$HEALTH_NAME.timer" 2>/dev/null || true
systemctl --user disable --now "$UNIT_NAME.service" 2>/dev/null || true
rm -f "$UNIT_DIR/$UNIT_NAME.service" \
      "$UNIT_DIR/$HEALTH_NAME.service" \
      "$UNIT_DIR/$HEALTH_NAME.timer"
systemctl --user daemon-reload
echo "normalize: removed $UNIT_DIR/$UNIT_NAME.service"
echo "normalize: removed $UNIT_DIR/$HEALTH_NAME.service"
echo "normalize: removed $UNIT_DIR/$HEALTH_NAME.timer"
echo "normalize: the five minute health probe is now OFF; nothing watches the launcher until a checkout with the template deploys again"
echo "normalize: ==================================================================="

# The recorded fingerprint was written by the newer installer over four
# artifacts, one of which is the unit file just deleted. Leaving it would have
# the older installer compare its three artifact hash against a note about a
# different set, which is a comparison whose result means nothing. Clear it so
# the next run makes its decision from the artifacts themselves.
rm -f "$RUNTIME_DIR/engine.fingerprint"

echo "normalize: $UNIT_NAME.service is free; this checkout's installer will start the launcher"
