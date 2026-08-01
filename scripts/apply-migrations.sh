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
  --dry-run)           mode="dry-run" ;;
  --check)             mode="check" ;;
  --print-baseline-sql) mode="print-baseline-sql" ;;
  "")                  ;;
  *) echo "unknown argument: $1" >&2; exit 2 ;;
esac

# The ledger DDL lives here rather than inline at each use so the SQL printed
# for review by --print-baseline-sql cannot drift from the SQL actually executed.
ledger_ddl() {
  cat <<'SQL'
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
}

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
# happened. It cannot be derived from filenames, because the drift is
# INTERLEAVED rather than a prefix: 20260516_01 through _07 are applied while
# _08 and _09 are not, and 20260518_02 is applied while _01 and _04 are not.
# A marker plus exceptions cannot express that shape, so the baseline file is
# an explicit list of the files proven applied.
#
# The safety rule: anything NOT listed is treated as not applied. Unknown never
# resolves to applied. A non-idempotent migration re-running aborts loudly and
# is fixable from one job log; a migration skipped silently leaves the ledger
# claiming a fully migrated database that is not one. Loud beats silent.
baseline_applied=()
while IFS= read -r line; do
  line="${line%%#*}"
  line="$(printf '%s' "$line" | tr -d '[:space:]')"
  [ -z "$line" ] && continue
  key="${line%%=*}"
  value="${line#*=}"
  case "$key" in
    applied) baseline_applied+=("$value") ;;
    *) echo "::error::unknown key in $baseline_file: $key"; exit 1 ;;
  esac
done < "$baseline_file"

if [ "${#baseline_applied[@]}" -eq 0 ]; then
  echo "::error::$baseline_file lists no applied migrations"
  exit 1
fi

contains() {
  local needle="$1"; shift
  local item
  for item in "$@"; do [ "$item" = "$needle" ] && return 0; done
  return 1
}

# Every name in the baseline must be a real migration file. A typo would
# otherwise mark nothing as applied, silently re-running a migration, or mask a
# rename.
for name in "${baseline_applied[@]}"; do
  if ! contains "$name" "${all_files[@]}"; then
    echo "::error::baseline lists a file that does not exist: $name"
    exit 1
  fi
done

if [ "$mode" = "check" ]; then
  echo "baseline file OK: applied=${#baseline_applied[@]} pending=$(( ${#all_files[@]} - ${#baseline_applied[@]} )) migrations=${#all_files[@]}"
  exit 0
fi

# Emit the one-time reconciliation as reviewable SQL, with no database
# connection. This is the same statement set the first `apply` run executes; it
# is exposed separately so the reconciliation can be read and approved as SQL
# rather than only being observable as a side effect of a deploy job.
if [ "$mode" = "print-baseline-sql" ]; then
  cat <<EOF
-- Generated by scripts/apply-migrations.sh --print-baseline-sql
-- One-time reconciliation of public.hive_schema_migrations against history that
-- was applied by hand. Records files at or before
-- proven applied in scripts/migration-baseline.conf, recording each as already
-- applied WITHOUT executing it. Every migration not listed there stays pending
-- and is executed by the run, because unknown never resolves to applied.
--
-- Reviewable and runnable standalone, but running it by hand is not required:
-- the first apply run performs exactly these statements itself.
EOF
  ledger_ddl
  echo
  for name in "${baseline_applied[@]}"; do
    sha="$(sha256sum "$migrations_dir/$name" | cut -d' ' -f1)"
    echo "INSERT INTO public.hive_schema_migrations (filename, sha256, source)"
    echo "VALUES ('$name', '$sha', 'baseline')"
    echo "ON CONFLICT (filename) DO NOTHING;"
  done
  exit 0
fi

# ---------------------------------------------------------------------------
# Ledger
# ---------------------------------------------------------------------------
psql_q() { psql --no-psqlrc -qtAX -v ON_ERROR_STOP=1 "$@"; }

if [ "$mode" = "apply" ]; then
  ledger_ddl | psql_q

  # Seed the baseline exactly once, on the run that finds the ledger empty.
  if [ "$(psql_q -c 'SELECT count(*) FROM public.hive_schema_migrations')" = "0" ]; then
    echo "ledger is empty: seeding ${#baseline_applied[@]} proven-applied migrations"
    for name in "${baseline_applied[@]}"; do
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
    echo "(ledger does not exist yet; treating the baseline as the applied set)"
    applied=("${baseline_applied[@]}")
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
