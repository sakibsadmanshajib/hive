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
#   BOX=hive-demo DEST=/home/sakib/hive-backups scripts/pull-box-backups.sh
#
# Requires: rsync, and the ssh host alias $BOX (default hive-demo) in
# ~/.ssh/config on this machine. Do not add one for this script's sake.
set -euo pipefail

BOX="${BOX:-hive-demo}"
BACKUP_ROOT_ON_BOX="${BACKUP_ROOT_ON_BOX:-/home/sakib/hive-backups}"
DEST="${DEST:-$HOME/hive-backups/$BOX}"

mkdir -p "$DEST/daily"
rsync -a "$BOX:$BACKUP_ROOT_ON_BOX/daily/" "$DEST/daily/"

echo "verifying checksums of pulled artifacts:"
STATUS=0
for d in "$DEST"/daily/*/; do
  [[ -f "$d/SHA256SUMS" ]] || continue
  (cd "$d" && sha256sum -c SHA256SUMS --quiet) || STATUS=1
done
if [[ "$STATUS" == "0" ]]; then
  echo "OK: all pulled days verified"
else
  echo "FAIL: checksum mismatch on at least one day" >&2
fi
exit "$STATUS"
