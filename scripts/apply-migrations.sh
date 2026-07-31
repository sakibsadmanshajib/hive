#!/usr/bin/env bash
# apply-migrations.sh -- Apply pending supabase/migrations/*.sql files to a
# target Postgres, recording each one in public.hive_schema_migrations so the
# next run skips it.
#
# Why this exists
# ---------------
# Migrations in this repo were applied by hand (SQL editor or a one-off psql)
# while code shipped automatically through deploy-demo-box. Code and schema
# therefore drifted apart with nothing watching. Two concrete failures:
#
#   * 20260731_01_tenant_billing_account_backfill_v2.sql merged in PR #624 and
#     was not applied, so PR #620's fail-closed tenant check answered HTTP 403
#     to every API-key call on the demo box: the mapping rows it reads did not
#     exist.
#   * 20260730_01_usage_events_event_type_extend.sql was not applied either, so
#     the usage_events event_type CHECK kept its old value list and every
#     'interrupted' / 'upstream_error' insert was rejected and then discarded by
#     the caller (apps/edge-api/internal/inference/stream.go).
#
# Both files were correct. Neither ever ran. This script is the missing half.
#
# Usage
# -----
#   scripts/apply-migrations.sh              apply pending migrations
#   scripts/apply-migrations.sh --dry-run    list what would be applied, touch nothing
#   scripts/apply-migrations.sh --check      validate the baseline file only, no database
#
# Connection
# ----------
# Connection settings come from libpq environment variables (PGHOST, PGPORT,
# PGUSER, PGDATABASE, PGPASSWORD) that the caller exports. This script never
# builds or parses a DSN string. That is deliberate: DSN parameters are
# per-driver, and mixing pgx-only parameters such as pool_max_conns or
# default_query_exec_mode into a libpq connection string makes libpq reject the
# whole value (it has already crash-looped a container and taken the live chat
# surface down for ~50 minutes). psql is a libpq client, so libpq variables are
# the only thing passed here.
#
# Point PGPORT at the pooler's transaction-mode port (6543) rather than session
# mode (5432). Session-mode clients are 1:1 with server connections and capped
# at the project's pool_size of 15, shared with the live demo stack and every CI
# job; a migration run has no need of session state and should not consume one
# of those slots.
#
# Transactions
# ------------
# Each migration file is handed to psql as-is, with no wrapping
# --single-transaction. Some files depend on that: 20260730_01 splits ADD
# CONSTRAINT ... NOT VALID from VALIDATE CONSTRAINT precisely so the validation
# scan runs under SHARE UPDATE EXCLUSIVE instead of inheriting ACCESS EXCLUSIVE
# from the same transaction, which would block reads and writes on the hot
# usage_events table for the length of the scan. Wrapping every file in one
# transaction would silently undo that. Files that want atomicity carry their
# own BEGIN/COMMIT.
#
# The consequence is that a file failing partway leaves its own completed
# statements in place and no ledger row. That is the honest outcome: the run
# stops, the exit code is non-zero, and an operator fixes it. It is not papered
# over.
#
# Extensions
# ----------
# This script adds no CREATE EXTENSION of its own. That matters because
# CREATE EXTENSION IF NOT EXISTS does not degrade gracefully when the extension
# is missing: it raises and aborts, which has already broken CI once by killing
# the schema bootstrap. Migration files that need an extension carry their own
# pg_available_extensions / pg_extension guard (see 20260729_02), and if such a
# guard is insufficient on a given host the run fails loudly here rather than
# being swallowed.

set -euo pipefail

# Filename order is the apply order, so collation must not depend on the
# runner's locale. C collation also matches the plain supabase/migrations/*.sql
# glob that CI's schema bootstrap already applies files in.
export LC_COLLATE=C

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
migrations_dir="$repo_root/supabase/migrations"
baseline_file="$repo_root/scripts/migration-baseline.conf"

mode="apply"
case "${1:-}" in
  --dry-run) mode="dry-run" ;;
  --check)   mode="check" ;;
  "")        ;;
  *) echo "unknown argument: $1" >&2; exit 2 ;;
esac

all_files=()
for path in "$migrations_dir"/*.sql; do
  [ -e "$path" ] || continue
  all_files+=("${path##*/}")
done
if [ "${#all_files[@]}" -eq 0 ]; then
  echo "::error::no migration files found in $migrations_dir"
  exit 1
fi

# ---------------------------------------------------------------------------
# Baseline
# ---------------------------------------------------------------------------
# The ledger is new, so the first run has to be told what history already
# happened. It cannot be derived: drift here is per-file, not a clean prefix.
# Verified read-only against the live database on 2026-07-31, everything
# through 20260731_01 is applied EXCEPT 20260729_02, whose purge procedure and
# retention_config table are both absent while later migrations are present.
# So the baseline is a marker plus an explicit list of holes below it.
baseline_marker=""
catch_up=()
while IFS= read -r line; do
  line="${line%%#*}"
  line="$(printf '%s' "$line" | tr -d '[:space:]')"
  [ -z "$line" ] && continue
  key="${line%%=*}"
  value="${line#*=}"
  case "$key" in
    baseline) baseline_marker="$value" ;;
    catch_up) catch_up+=("$value") ;;
    *) echo "::error::unknown key in $baseline_file: $key"; exit 1 ;;
  esac
done < "$baseline_file"

if [ -z "$baseline_marker" ]; then
  echo "::error::$baseline_file does not set a baseline marker"
  exit 1
fi

contains() {
  local needle="$1"; shift
  local item
  for item in "$@"; do [ "$item" = "$needle" ] && return 0; done
  return 1
}

# Every name in the baseline file must be a real migration file, otherwise a
# typo silently marks nothing (or worse, marks the wrong thing) as applied.
if ! contains "$baseline_marker" "${all_files[@]}"; then
  echo "::error::baseline marker is not a migration file: $baseline_marker"
  exit 1
fi
for name in ${catch_up[@]+"${catch_up[@]}"}; do
  if ! contains "$name" "${all_files[@]}"; then
    echo "::error::catch_up entry is not a migration file: $name"
    exit 1
  fi
  if [[ "$name" > "$baseline_marker" ]]; then
    echo "::error::catch_up entry $name sorts after the baseline marker, so it is already pending and must not be listed"
    exit 1
  fi
done

if [ "$mode" = "check" ]; then
  echo "baseline file OK: marker=$baseline_marker catch_up=${#catch_up[@]} migrations=${#all_files[@]}"
  exit 0
fi

# ---------------------------------------------------------------------------
# Ledger
# ---------------------------------------------------------------------------
psql_q() { psql --no-psqlrc -qtAX -v ON_ERROR_STOP=1 "$@"; }

if [ "$mode" = "apply" ]; then
  psql_q <<'SQL'
CREATE TABLE IF NOT EXISTS public.hive_schema_migrations (
  filename   text PRIMARY KEY,
  sha256     text NOT NULL,
  applied_at timestamptz NOT NULL DEFAULT now(),
  source     text NOT NULL CHECK (source IN ('baseline', 'applied'))
);

COMMENT ON TABLE public.hive_schema_migrations IS
  'Which supabase/migrations/*.sql files have run against this database. '
  'Keyed on filename, not on the leading version prefix: three prefixes are '
  'duplicated in this repo (20260331_02, 20260719_01, 20260726_01), so the '
  'prefix is not unique. source=baseline rows were recorded without being '
  'executed, as history applied by hand before this ledger existed.';

-- No policies: this is deployment metadata and nothing outside a superuser or
-- table-owner connection has any business reading it through PostgREST.
ALTER TABLE public.hive_schema_migrations ENABLE ROW LEVEL SECURITY;
SQL

  # Seed the baseline exactly once, on the run that finds the ledger empty.
  if [ "$(psql_q -c 'SELECT count(*) FROM public.hive_schema_migrations')" = "0" ]; then
    echo "ledger is empty: seeding baseline through $baseline_marker"
    for name in "${all_files[@]}"; do
      [[ "$name" > "$baseline_marker" ]] && break
      contains "$name" ${catch_up[@]+"${catch_up[@]}"} && continue
      sha="$(sha256sum "$migrations_dir/$name" | cut -d' ' -f1)"
      psql_q -c "INSERT INTO public.hive_schema_migrations (filename, sha256, source)
                 VALUES ('$name', '$sha', 'baseline')
                 ON CONFLICT (filename) DO NOTHING"
    done
    echo "baseline seeded: $(psql_q -c 'SELECT count(*) FROM public.hive_schema_migrations') rows"
  fi

  mapfile -t applied < <(psql_q -c 'SELECT filename FROM public.hive_schema_migrations')
else
  # --dry-run must work before the ledger exists, so a missing table reads as
  # "nothing applied yet" rather than an error.
  if [ "$(psql_q -c "SELECT to_regclass('public.hive_schema_migrations') IS NOT NULL")" = "t" ]; then
    mapfile -t applied < <(psql_q -c 'SELECT filename FROM public.hive_schema_migrations')
  else
    echo "(ledger does not exist yet; treating every file after the baseline as pending)"
    applied=()
    for name in "${all_files[@]}"; do
      [[ "$name" > "$baseline_marker" ]] && break
      contains "$name" ${catch_up[@]+"${catch_up[@]}"} && continue
      applied+=("$name")
    done
  fi
fi

# ---------------------------------------------------------------------------
# Apply
# ---------------------------------------------------------------------------
pending=()
for name in "${all_files[@]}"; do
  contains "$name" ${applied[@]+"${applied[@]}"} || pending+=("$name")
done

if [ "${#pending[@]}" -eq 0 ]; then
  echo "no pending migrations (${#all_files[@]} files, all recorded)"
  exit 0
fi

echo "pending migrations (${#pending[@]}):"
printf '  %s\n' "${pending[@]}"

if [ "$mode" = "dry-run" ]; then
  echo "--dry-run: nothing applied"
  exit 0
fi

for name in "${pending[@]}"; do
  echo "::group::applying $name"
  psql --no-psqlrc -X -v ON_ERROR_STOP=1 -f "$migrations_dir/$name"
  sha="$(sha256sum "$migrations_dir/$name" | cut -d' ' -f1)"
  psql_q -c "INSERT INTO public.hive_schema_migrations (filename, sha256, source)
             VALUES ('$name', '$sha', 'applied')"
  echo "::endgroup::"
  echo "applied $name"
done

echo "applied ${#pending[@]} migration(s)"
