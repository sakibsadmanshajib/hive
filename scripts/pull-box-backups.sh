#!/usr/bin/env bash
# pull-box-backups.sh -- pull the box's encrypted backup artifacts to this
# machine. Runs on the DEV machine, never on the box. Only encrypted artifacts
# ever cross the network; decryption happens only where the passphrase lives.
#
# This is the stopgap off-box copy. A true offsite destination (object store,
# separate physical location) remains an open owner decision, tracked in the
# follow-up issue referenced from docs/runbooks/box-backup-restore.md.
#
# Usage:
#   scripts/pull-box-backups.sh
#   scripts/pull-box-backups.sh --prune     also delete local days the box dropped
#   BOX=hive-demo DEST=/home/sakib/hive-backups scripts/pull-box-backups.sh
#
# Requires: rsync, and the ssh host alias $BOX (default hive-demo) in
# ~/.ssh/config on this machine. Do not add one for this script's sake.
set -euo pipefail

BOX="${BOX:-hive-demo}"
BACKUP_ROOT_ON_BOX="${BACKUP_ROOT_ON_BOX:-/home/sakib/hive-backups}"
DEST="${DEST:-$HOME/hive-backups/$BOX}"

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
done
if [[ "$STATUS" == "0" ]]; then
  echo "OK: all pulled days verified"
else
  echo "FAIL: checksum mismatch on at least one day" >&2
fi
exit "$STATUS"
