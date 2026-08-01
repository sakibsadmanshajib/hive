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
# On a database whose ledger is empty, the run first decides whether the
# baseline describes this database at all, by probing the schema. See
# decide_baseline below. A fresh install therefore executes the whole chain
# instead of recording it as history it never ran. HIVE_MIGRATION_BASELINE
# overrides that decision and is honoured only where the probe is inconclusive;
# python3 must be present for the probe to run at all, and a run that cannot
# probe refuses to guess.
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
#
# The baseline is about ONE database: the one whose history it was probed
# against. Whether it is about the database in front of this run is a separate
# question, decided by decide_baseline below from the schema, never from the
# ledger merely being empty.
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

# ---------------------------------------------------------------------------
# Does the baseline describe THIS database?
# ---------------------------------------------------------------------------
# An empty ledger has two opposite meanings and looks identical in both:
#
#   * A database that predates the ledger, whose migrations were applied by hand
#     and recorded nowhere. This is what the baseline was written for, and
#     seeding it is correct: re-running that history would abort on the first
#     non-idempotent statement.
#   * A brand new, empty database. Seeding the baseline here marks every listed
#     migration applied WITHOUT running it, so the schema those files create is
#     never created and the ledger claims otherwise. On a fresh install that is
#     the whole of history silently skipped behind a green deploy.
#
# Reading "the ledger is empty" as the first case was the defect this function
# replaces. The two cases are indistinguishable in the ledger and obvious in the
# schema, so the schema is what gets asked: probe-applied-migrations.py
# --baseline-state reports fresh (not one object any baseline migration creates
# exists), preexisting (every baseline migration's objects are all present), or
# ambiguous (anything else, including a half applied chain or a baseline gone
# stale against this database).
#
# Ambiguous refuses to proceed. There is no safe guess: guessing preexisting
# skips real migrations permanently and still reports success, and guessing
# fresh re-runs history. An operator resolving it from one job log is the cheap
# outcome; a green deploy over missing schema is not.
#
# HIVE_MIGRATION_BASELINE=seed|ignore is belt and braces, deliberately NOT the
# primary guard, because the failure mode of a required flag is someone
# forgetting to pass it. It is honoured only where the probe is inconclusive or
# could not run, and is rejected outright when it contradicts the schema.
baseline_decision=""
decide_baseline() {
  local override="${HIVE_MIGRATION_BASELINE:-}"
  case "$override" in
    ""|seed|ignore) ;;
    *) echo "::error::HIVE_MIGRATION_BASELINE must be seed or ignore, got: $override"; exit 1 ;;
  esac

  local probe="$repo_root/scripts/probe-applied-migrations.py"
  local out="" state="" entries=""
  if ! command -v python3 >/dev/null 2>&1; then
    out="python3 is not installed, so the schema could not be probed"
  elif [ ! -f "$probe" ]; then
    out="$probe is missing, so the schema could not be probed"
  elif ! out="$(python3 "$probe" --baseline-state 2>&1)"; then
    out="the schema probe failed:
$out"
  else
    state="$(printf '%s\n' "$out" | sed -n 's/^baseline_state=//p')"
    entries="$(printf '%s\n' "$out" | sed -n 's/^baseline_entries=//p')"
  fi
  printf '%s\n' "$out"
  [ -n "$state" ] || state="unknown"

  # Both this script and the probe parse migration-baseline.conf. If those two
  # parsers ever disagree the probe would be answering about a different set of
  # files than the one about to be seeded, which is exactly the kind of silent
  # divergence this whole change exists to remove.
  if [ -n "$entries" ] && [ "$entries" != "${#baseline_applied[@]}" ]; then
    echo "::error::the probe read $entries baseline entries and this script read ${#baseline_applied[@]}; the two parsers of $baseline_file disagree, so no decision is trustworthy"
    exit 1
  fi

  case "$state" in
    preexisting)
      if [ "$override" = "ignore" ]; then
        echo "::error::HIVE_MIGRATION_BASELINE=ignore contradicts the schema of this database: every baseline migration's objects are already present, so running the full chain would re-run history. Drop the override, or fix the baseline if that is the thing that is wrong."
        exit 1
      fi
      baseline_decision="seed"
      ;;
    fresh)
      if [ "$override" = "seed" ]; then
        echo "::error::HIVE_MIGRATION_BASELINE=seed contradicts the schema of this database: not one object of any baseline migration exists, so this database is new and seeding would record ${#baseline_applied[@]} migrations as applied without running them. Drop the override."
        exit 1
      fi
      baseline_decision="ignore"
      ;;
    *)
      if [ -n "$override" ]; then
        echo "::warning::baseline state is $state, proceeding with HIVE_MIGRATION_BASELINE=$override on the operator's instruction"
        baseline_decision="$override"
      else
        echo "::error::cannot tell whether $baseline_file describes this database (state: $state), so nothing has been applied and nothing has been recorded."
        cat <<EOF
public.hive_schema_migrations is empty, which means either this database
predates the ledger and already ran the baseline migrations, or it is new and
has run nothing. The schema above matches neither shape, so both answers are
guesses and one of them silently skips migrations. Resolve it explicitly:

  * If the baseline has gone stale against this database, regenerate it and
    commit the result, then re-run:
        scripts/probe-applied-migrations.py --emit-baseline
  * If the schema is half applied, finish or unwind that partial state by hand
    first. The state lines above name the files involved.
  * As a last resort, state the answer yourself and re-run with one of:
        HIVE_MIGRATION_BASELINE=seed     record the baseline as applied without
                                         running it (skips those migrations for
                                         good on this database)
        HIVE_MIGRATION_BASELINE=ignore   ignore the baseline and run every
                                         migration in the chain
EOF
        exit 1
      fi
      ;;
  esac
}

applied=()
if [ "$mode" = "apply" ]; then
  ledger_ddl | psql_q

  # Seed the baseline at most once, on a run that finds the ledger empty, and
  # only against a database whose schema says the baseline belongs to it.
  if [ "$(psql_q -c 'SELECT count(*) FROM public.hive_schema_migrations')" = "0" ]; then
    decide_baseline
    if [ "$baseline_decision" = "seed" ]; then
      echo "ledger is empty and this database predates it: seeding ${#baseline_applied[@]} proven-applied migrations"
      for name in "${baseline_applied[@]}"; do
        sha="$(sha256sum "$migrations_dir/$name" | cut -d' ' -f1)"
        psql_q -c "INSERT INTO public.hive_schema_migrations (filename, sha256, source)
                   VALUES ('$name', '$sha', 'baseline')
                   ON CONFLICT (filename) DO NOTHING"
      done
      echo "baseline seeded: $(psql_q -c 'SELECT count(*) FROM public.hive_schema_migrations') rows"
    else
      echo "ledger is empty and this database has none of the schema the baseline describes: the baseline is NOT seeded, all ${#all_files[@]} migrations are pending"
    fi
  fi

  mapfile -t applied < <(psql_q -c 'SELECT filename FROM public.hive_schema_migrations')
else
  # --dry-run must work before the ledger exists, so a missing table reads as
  # "nothing applied yet" rather than an error. An absent ledger and an empty
  # one ask the same question an apply run asks, and get the same answer here,
  # so a dry run never reports a pending set the apply run would disagree with.
  if [ "$(psql_q -c "SELECT to_regclass('public.hive_schema_migrations') IS NOT NULL")" = "t" ]; then
    mapfile -t applied < <(psql_q -c 'SELECT filename FROM public.hive_schema_migrations')
  else
    echo "(ledger does not exist yet)"
  fi
  if [ "${#applied[@]}" -eq 0 ]; then
    decide_baseline
    if [ "$baseline_decision" = "seed" ]; then
      applied=("${baseline_applied[@]}")
    fi
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
