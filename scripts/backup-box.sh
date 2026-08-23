#!/usr/bin/env bash
# backup-box.sh -- encrypted, unattended backups of the demo box's four
# production data stores, taken from a RUNNING stack (no stop/restart ever).
#
# Stores covered
# --------------
#   1. Postgres (identities, tenants, api keys, credit ledger): streamed
#      pg_dump --format=custom out of the supabase-db container.
#   2. Open WebUI relational state: webui.db snapshotted with SQLite's online
#      backup API inside the open-webui container (copying webui.db while its
#      WAL is active is NOT safe, which is why this is not a plain cp).
#   3. Open WebUI uploads directory (/data/uploads).
#   4. Supabase Storage object bytes (buckets hive-files and hive-images,
#      including their nested stub/stub layout under /var/lib/storage).
#
# Why Alertmanager directly
# -------------------------
# The monitoring profile already runs Prometheus plus Alertmanager with working
# email routing (PR #998). The backup script runs on the HOST, outside the
# compose network where Prometheus scrapes, so a scraped metric would need a
# new component (node_exporter is not deployed here). Posting alerts straight
# to Alertmanager's v2 API on the published host port reuses the existing
# transport end to end: same routing tree, same receiver, same email. A firing
# alert resolves itself through resolve_timeout once a later successful run
# stops posting it, so the success path sends nothing.
#
# Failure semantics are loud on both halves that can die:
#   * a failed run posts HiveBoxBackupFailed immediately;
#   * a missed run (dead timer, dead scheduler) is caught because every entry
#     point calls --check first: any run older than STALE_AFTER seconds makes
#     the next check post HiveBoxBackupStale. An hourly cron watchdog runs
#     --check so the death of the systemd timer itself still surfaces within
#     an hour.
#
# Secrets
# -------
# No secret value lives in this script or in the repository. The encryption
# passphrase is read from PASSPHRASE_FILE (chmod 600, outside any git
# checkout). Artifacts leave the box only in encrypted form.
#
# Usage
# -----
#   backup-box.sh            take one full backup set
#   backup-box.sh --check    staleness watchdog only, no backup taken
#
# Environment (all defaulted, none required)
# ------------------------------------------
#   BACKUP_ROOT       /home/sakib/hive-backups
#   DB_CONTAINER      hive-supabase-db-1        OWUI_CONTAINER hive-open-webui-1
#   STORAGE_CONTAINER hive-supabase-storage-1
#   PGUSER            postgres                  PGDATABASE     postgres
#   KEEP_DAILY        14                        STALE_AFTER    93600 (26h)
#   ALERTMANAGER_URL  http://localhost:9093
#   PASSPHRASE_FILE   $BACKUP_ROOT/etc/passphrase
set -euo pipefail

BACKUP_ROOT="${BACKUP_ROOT:-/home/sakib/hive-backups}"
DB_CONTAINER="${DB_CONTAINER:-hive-supabase-db-1}"
OWUI_CONTAINER="${OWUI_CONTAINER:-hive-open-webui-1}"
STORAGE_CONTAINER="${STORAGE_CONTAINER:-hive-supabase-storage-1}"
PGUSER="${PGUSER:-postgres}"
PGDATABASE="${PGDATABASE:-postgres}"
KEEP_DAILY="${KEEP_DAILY:-14}"
STALE_AFTER="${STALE_AFTER:-93600}"   # 26 hours: one missed daily slot of slack
ALERTMANAGER_URL="${ALERTMANAGER_URL:-http://localhost:9093}"
PASSPHRASE_FILE="${PASSPHRASE_FILE:-$BACKUP_ROOT/etc/passphrase}"

MODE="run"
if [[ "${1:-}" == "--check" ]]; then MODE="check"; fi

TS="$(date -u +%Y%m%dT%H%M%SZ)"
LOG_DIR="$BACKUP_ROOT/logs"
mkdir -p "$LOG_DIR" "$BACKUP_ROOT/status" "$BACKUP_ROOT/tmp"
LOG_FILE="$LOG_DIR/$TS.log"

log()  { printf '%s %s\n' "$(date -u +%FT%TZ)" "$*" | tee -a "$LOG_FILE"; }
warn() { log "WARN: $*"; }
die()  { log "ERROR: $*"; exit 1; }

# ---------------------------------------------------------------------------
# Alertmanager direct post. Fixed literal strings only in labels and text, so
# nothing user-controlled and nothing sensitive can reach the email body.
# ---------------------------------------------------------------------------
post_alert() {
  local alertname="$1" description="$2" startsAt body
  startsAt="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  body=$(printf '{"labels":{"alertname":"%s","severity":"critical","job":"box-backup","instance":"hive-demo"},"annotations":{"description":"%s"}}' \
    "$alertname" "$description")
  # Delivery failure must not fail the backup itself on the success path, and
  # must not mask the underlying failure on the failure path. Log it either way.
  curl -fsS -m 10 -XPOST "$ALERTMANAGER_URL/api/v2/alerts" \
    -H 'Content-Type: application/json' -d "$body" \
    >>"$LOG_FILE" 2>&1 || warn "alertmanager post failed for $alertname"
}

fail_run() {
  log "backup FAILED at $1" || true
  echo "FAILED $1 $(date -u +%FT%TZ)" >>"$BACKUP_ROOT/status/STATUS.txt"
  post_alert "HiveBoxBackupFailed" "Backup run failed on the demo box at step $1. See /home/sakib/hive-backups/logs on the box."
  exit 1
}

# Watchdog mode: fresh success means silence, anything else is loud.
if [[ "$MODE" == "check" ]]; then
  EPOCH_FILE="$BACKUP_ROOT/status/last-success-epoch"
  NOW="$(date +%s)"
  LAST="0"
  [[ -f "$EPOCH_FILE" ]] && LAST="$(cat "$EPOCH_FILE")"
  AGE=$(( NOW - LAST ))
  if (( AGE > STALE_AFTER )); then
    post_alert "HiveBoxBackupStale" "Last successful demo box backup is older than 26 hours (age ${AGE}s). A scheduled run did not happen."
    die "stale: last success ${AGE}s ago exceeds threshold ${STALE_AFTER}s"
  fi
  log "check ok: last success ${AGE}s ago"
  exit 0
fi

# ------------------------------- full run ----------------------------------
# Serialize concurrent invocations (systemd timer and cron watchdog overlap).
exec 9>"$BACKUP_ROOT/lock"
flock -n 9 || { log "another backup run holds the lock, exiting"; exit 0; }

[[ -r "$PASSPHRASE_FILE" ]] || fail_run "passphrase_missing"
[[ "$(stat -c %a "$PASSPHRASE_FILE")" == "600" ]] \
  || die "PASSPHRASE_FILE must be chmod 600, refusing to run"

WORK="$BACKUP_ROOT/tmp/run-$TS"
mkdir -p "$WORK"
PUBLISH="$BACKUP_ROOT/daily/$(date -u +%F)"

# Run the staleness check first so a half-dead schedule still alerts even when
# this particular invocation succeeds.
"$0" --check || true

cleanup() {
  rm -rf "$WORK"
}
trap cleanup EXIT

log "== backup run start ($TS) =="

# 1. Database -------------------------------------------------------------
# Custom format keeps pg_restore usable and compresses; the stream crosses
# docker exec stdout only, it never touches the network in clear text.
DB_DUMP="$WORK/db.pgdump"
docker exec "$DB_CONTAINER" pg_dump -U "$PGUSER" -d "$PGDATABASE" -Fc > "$DB_DUMP" \
  || fail_run "db_dump"
[[ "$(head -c 5 "$DB_DUMP")" == "PGDMP" ]] || fail_run "db_dump_verify"
log "db dump ok ($(du -h "$DB_DUMP" | cut -f1))"

# 2. Open WebUI relational state (SQLite online snapshot) -----------------
# The snapshot is written to the container's own /tmp (not the data volume),
# integrity-checked, then streamed out. Never copied while WAL is hot.
OWUI_SNAP="$WORK/webui.db"
docker exec "$OWUI_CONTAINER" python3 -c '
import os, sqlite3, sys, shutil
src = sqlite3.connect("file:/data/webui.db?mode=ro", uri=True)
dst_path = "/tmp/backup-webui-snapshot.db"
dst = sqlite3.connect(dst_path)
with dst:
    src.backup(dst)
dst.close(); src.close()
check = sqlite3.connect(dst_path)
result = check.execute("PRAGMA integrity_check").fetchone()[0]
check.close()
if result != "ok":
    sys.exit("integrity_check said: " + result)
with open(dst_path, "rb") as f:
    shutil.copyfileobj(f, sys.stdout.buffer)
os.remove("/tmp/backup-webui-snapshot.db")
' > "$OWUI_SNAP" 2>>"$LOG_FILE" || fail_run "owui_snapshot"
log "webui.db snapshot ok ($(du -h "$OWUI_SNAP" | cut -f1))"

# 3. Open WebUI uploads ----------------------------------------------------
UPLOADS_TGZ="$WORK/uploads.tgz"
docker exec "$OWUI_CONTAINER" tar czf - -C /data uploads > "$UPLOADS_TGZ" \
  || fail_run "owui_uploads"
log "uploads tar ok ($(du -h "$UPLOADS_TGZ" | cut -f1))"

# 4. Supabase Storage object bytes ----------------------------------------
STORAGE_TGZ="$WORK/storage.tgz"
docker exec "$STORAGE_CONTAINER" tar czf - -C / var/lib/storage > "$STORAGE_TGZ" \
  || fail_run "storage_tar"
log "storage tar ok ($(du -h "$STORAGE_TGZ" | cut -f1))"

# 5. Encrypt every artifact -----------------------------------------------
for f in "$DB_DUMP" "$OWUI_SNAP" "$UPLOADS_TGZ" "$STORAGE_TGZ"; do
  openssl enc -aes-256-cbc -pbkdf2 -iter 600000 -salt \
    -in "$f" -out "$f.enc" -pass file:"$PASSPHRASE_FILE" || fail_run "encrypt"
done
log "encryption ok"

# 6. Publish the day directory atomically enough ---------------------------
mkdir -p "$PUBLISH"
for f in "$WORK"/*.enc; do
  base="$(basename "$f" .enc)"
  # Write-then-rename so a reader (or the off-box pull) never sees a torn file.
  cp "$f" "$PUBLISH/$base.enc.partial" && mv -f "$PUBLISH/$base.enc.partial" "$PUBLISH/$base.enc"
done
(
  cd "$PUBLISH"
  # Regenerate checksums over exactly today's four artifacts.
  ls | grep '\.enc$' | sort | xargs sha256sum > SHA256SUMS
  {
    echo "created $(date -u +%FT%TZ)"
    ls -l --block-size=K *.enc | awk '{print $5, $9}'
  } > MANIFEST.txt
)
log "published to $PUBLISH"

# 7. Retention --------------------------------------------------------------
cd "$BACKUP_ROOT/daily"
ls -1 | sort -r | tail -n +$(( KEEP_DAILY + 1 )) | while read -r old; do
  rm -rf "$old"
  log "retention: removed $old"
done

# 8. Status + heartbeat ------------------------------------------------------
NOW_EPOCH="$(date +%s)"
echo "$NOW_EPOCH" > "$BACKUP_ROOT/status/last-success-epoch"
{
  echo "OK $(date -u +%FT%TZ) day=$(date -u +%F) files=$(ls "$PUBLISH" | grep -c '\.enc$')"
} >> "$BACKUP_ROOT/status/STATUS.txt"

log "== backup run complete =="

# Success posts nothing to Alertmanager: resolve_timeout ends any earlier
# firing alert on its own once the failures stop being posted.
exit 0
