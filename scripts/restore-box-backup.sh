#!/usr/bin/env bash
# restore-box-backup.sh -- prove a backup is restorable, into a THROWAWAY.
#
# Decrypts the newest (or a given) day's database dump, restores it into a
# throwaway Postgres container built from the SAME image production runs,
# compares row counts for the money and identity tables against the live
# database, prints a table-by-table verdict, and destroys the throwaway. It
# never writes to production: every write lands in its own throwaway container.
#
# The REAL restore onto production stays deliberately manual; see
# docs/runbooks/box-backup-restore.md for that procedure.
#
# Usage (on the box):
#   restore-box-backup.sh              verify the newest published day
#   restore-box-backup.sh 2026-08-23   verify a specific day
#   restore-box-backup.sh --keep       keep the throwaway container around
#
# Environment: same variables as backup-box.sh plus VERIFY_IMAGE (default
# hive-supabase-db:pg16-cron, the exact production DB image).
set -euo pipefail

BACKUP_ROOT="${BACKUP_ROOT:-/tmp/hive-backups}"   # overridden on box to /home/sakib/hive-backups
DB_CONTAINER="${DB_CONTAINER:-hive-supabase-db-1}"
PGUSER="${PGUSER:-postgres}"
VERIFY_IMAGE="${VERIFY_IMAGE:-hive-supabase-db:pg16-cron}"
PASSPHRASE_FILE="${PASSPHRASE_FILE:-$BACKUP_ROOT/etc/passphrase}"

DAY="latest"
KEEP=0
for arg in "$@"; do
  case "$arg" in
    --keep) KEEP=1 ;;
    *) DAY="$arg" ;;
  esac
done

[[ -r "$PASSPHRASE_FILE" ]] || { echo "FATAL: passphrase not readable"; exit 1; }

if [[ "$DAY" == "latest" && -d "$BACKUP_ROOT/daily" ]]; then
  DAY="$(ls -1 "$BACKUP_ROOT/daily" | sort | tail -1)"
fi
DIR="$BACKUP_ROOT/daily/$DAY"
[[ -f "$DIR/db.pgdump.enc" ]] || { echo "FATAL: no db artifact under $DIR"; exit 1; }
echo "== verifying $DAY =="

WORK="$(mktemp -d /tmp/restore-verify.XXXXXX)"
CNAME="hive-bk-verify-$$"
cleanup() {
  rm -rf "$WORK"
  if [[ "$KEEP" == "0" ]]; then docker rm -f "$CNAME" >/dev/null 2>&1 || true; fi
}
trap cleanup EXIT

# 1. Decrypt into a private temp dir on the box. Never crosses the network in
#    the clear, never lands inside any git checkout.
chmod 700 "$WORK"
openssl enc -d -aes-256-cbc -pbkdf2 -iter 600000 -salt \
  -in "$DIR/db.pgdump.enc" -out "$WORK/db.pgdump" \
  -pass file:"$PASSPHRASE_FILE"

# 2. Throwaway postgres from the production image: isolated network namespace
#    (--network none), no published ports, random name, minutes of life.
docker run -d --name "$CNAME" \
  -e POSTGRES_HOST_AUTH_METHOD=trust \
  --network none \
  --restart no \
  "$VERIFY_IMAGE" >/dev/null
for _ in $(seq 1 60); do
  docker exec "$CNAME" pg_isready -U postgres >/dev/null 2>&1 && break
  sleep 1
done
docker exec "$CNAME" pg_isready -U postgres >/dev/null

# 3. Restore. Extension DDL that needs shared-preload libraries the vanilla
#    entrypoint does not set (pg_cron) fails here WITHOUT failing the data;
#    pg_restore continues past individual object errors by default, and the
#    row-count comparison below decides pass or fail, not pg_restore's exit code.
echo "-- restoring (extension DDL errors are expected here, data errors are not)"
docker exec -i "$CNAME" pg_restore -U postgres -d postgres --no-owner --role=postgres \
  < "$WORK/db.pgdump" || true

# 4. Compare counts. Live side is read-only SELECT count(*).
TABLES=(public.credit_ledger_entries public.tenants public.api_keys auth.users auth.identities storage.objects)
FAIL=0
printf '%-32s %12s %12s\n' TABLE LIVE RESTORED
for t in "${TABLES[@]}"; do
  LIVE="$(docker exec "$DB_CONTAINER" psql -U "$PGUSER" -Atc "SELECT count(*) FROM $t")"
  GOT="$(docker exec "$CNAME" psql -U "$PGUSER" -Atc "SELECT count(*) FROM $t" 2>/dev/null || echo ERR)"
  VERDICT=ok
  [[ "$LIVE" == "$GOT" ]] || { VERDICT=MISMATCH; FAIL=1; }
  printf '%-32s %12s %12s  %s\n' "$t" "$LIVE" "$GOT" "$VERDICT"
done

echo ""
if [[ "$FAIL" == "0" ]]; then
  echo "PASS: all compared tables match between live and restored throwaway."
else
  echo "FAIL: at least one table count differs. Keep with --keep and inspect."
fi

if [[ "$KEEP" == "1" ]]; then
  echo "throwaway container kept as $CNAME"
fi
exit "$FAIL"
