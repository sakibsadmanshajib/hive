#!/usr/bin/env bash
# pull-box-backups.sh -- pull the box's encrypted backup artifacts to this
# machine. Runs on the DEV machine, never on the box. Only encrypted artifacts
# ever cross the network; decryption happens only where the passphrase lives.
#
# This is the stopgap off-box copy. A true offsite destination (object store,
# separate physical location) remains an open owner decision, tracked in the
# follow-up issue referenced from docs/runbooks/box-backup-restore.md.
#
# This script is also the ONLY writer of the staleness marker that issue #1491
# added. After a verified pull, and only then, it records the repository
# variable $MARKER_VAR so that a scheduled lane can see how long ago the last
# off-box copy actually happened. Before that marker existed, this script's
# silence was indistinguishable from its success: on 2026-08-29 it had not run
# for six days, the box's own backup signals were all green, and nothing
# anywhere reported that the off-box copy held one day out of seven.
#
# The marker is written after verification, never before. A pull whose
# checksums do not verify leaves the previous marker untouched and exits
# non-zero, because a marker written ahead of its evidence is one more surface
# asserting a state the system does not have, which is the defect, not the fix.
#
# Usage:
#   scripts/pull-box-backups.sh
#   scripts/pull-box-backups.sh --prune     also delete local days the box dropped
#   BOX=hive-demo DEST=/home/sakib/hive-backups scripts/pull-box-backups.sh
#
# Requires: rsync, the gh CLI authenticated against $MARKER_REPO, and the ssh
# host alias $BOX (default hive-demo) in ~/.ssh/config on this machine. Do not
# add one for this script's sake.
set -euo pipefail

BOX="${BOX:-hive-demo}"
BACKUP_ROOT_ON_BOX="${BACKUP_ROOT_ON_BOX:-/home/sakib/hive-backups}"
DEST="${DEST:-$HOME/hive-backups/$BOX}"
# Where the freshness marker is recorded (issue #1491). A GitHub repository
# variable, not a file on the box: nothing that runs on a schedule can read a
# file on the box (the hosted runners have no SSH path to it, and the two
# workflows that do run on the box's self-hosted runner are triggered by
# deploys and labels, never by a clock), whereas every workflow can read a
# repository variable with no credential and no network path of its own.
MARKER_REPO="${MARKER_REPO:-sakibsadmanshajib/hive}"
MARKER_VAR="${MARKER_VAR:-LAST_OFFBOX_BACKUP_PULL}"

PRUNE=0
for arg in "$@"; do
  case "$arg" in
    --prune) PRUNE=1 ;;
    *) echo "unknown argument: $arg" >&2; exit 2 ;;
  esac
done

mkdir -p "$DEST/daily"
if [[ "$PRUNE" == "1" ]]; then
  rsync -a --delete "$BOX:$BACKUP_ROOT_ON_BOX/daily/" "$DEST/daily/"
else
  rsync -a "$BOX:$BACKUP_ROOT_ON_BOX/daily/" "$DEST/daily/"
fi

echo "verifying pulled artifacts:"
STATUS=0
NEWEST_DAY=""
for d in "$DEST"/daily/*/; do
  # A day directory without its full committed set means the pull raced the
  # publisher or a transfer was truncated. That is a LOUD failure, never a
  # silent skip: an incomplete off-box set must not read as protected.
  ENCS=$(ls "$d" 2>/dev/null | grep -c '\.enc$' || true)
  if [[ ! -f "$d/SHA256SUMS" || ! -f "$d/MANIFEST.txt" ]]; then
    echo "FAIL: $d has no SHA256SUMS or MANIFEST.txt (incomplete set)" >&2
    STATUS=1
    continue
  fi
  if [[ "$ENCS" != "4" ]]; then
    echo "FAIL: $d holds $ENCS encrypted artifacts, expected 4 (incomplete set)" >&2
    STATUS=1
    continue
  fi
  (cd "$d" && sha256sum -c SHA256SUMS --quiet) || STATUS=1
  # The day directories glob in sorted order and every name is YYYY-MM-DD, so
  # the last one reached is the newest. Only read below when STATUS is 0, i.e.
  # when every day including this one verified.
  NEWEST_DAY="$(basename "$d")"
done

if [[ "$STATUS" != "0" ]]; then
  echo "FAIL: checksum mismatch on at least one day" >&2
  echo "the freshness marker was NOT updated: the previous one stands, so the" >&2
  echo "scheduled staleness check keeps measuring the last pull that actually" >&2
  echo "verified rather than this one" >&2
  exit 1
fi
echo "OK: all pulled days verified"

# ---------------------------------------------------------------------------
# Record the marker. Everything below this line is downstream of the
# verification gate above, which is the whole point: nothing records a
# successful off-box copy except a successful off-box copy.
#
# A failure here is loud and non-zero even though the artifacts are already
# safely on this machine, because an unrecorded pull is invisible to the
# scheduled check and will read as staleness until someone runs this again.
# Reporting success over that would put the silence straight back.
# ---------------------------------------------------------------------------
if [[ -z "$NEWEST_DAY" ]]; then
  echo "FAIL: no verified day directories under $DEST/daily, nothing to record" >&2
  exit 1
fi

if ! command -v gh >/dev/null 2>&1; then
  echo "FAIL: the artifacts pulled and verified, but the gh CLI is not installed" >&2
  echo "so the freshness marker could not be recorded. Install gh, authenticate it" >&2
  echo "against $MARKER_REPO, and run this script again." >&2
  exit 1
fi

MARKER="pulled_at=$(date -u +%Y-%m-%dT%H:%M:%SZ) newest_day=$NEWEST_DAY"
if ! gh variable set "$MARKER_VAR" --repo "$MARKER_REPO" --body "$MARKER"; then
  echo "FAIL: the artifacts pulled and verified, but recording the freshness marker" >&2
  echo "($MARKER_VAR on $MARKER_REPO) failed. The scheduled staleness check will keep" >&2
  echo "reporting the previous pull until this succeeds." >&2
  exit 1
fi
echo "recorded off-box pull marker $MARKER_VAR: $MARKER"
