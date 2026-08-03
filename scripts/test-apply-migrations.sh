#!/usr/bin/env bash
# test-apply-migrations.sh -- prove scripts/apply-migrations.sh against a real
# throwaway Postgres, one scratch database per scenario.
#
# What it pins down
# -----------------
# The baseline in scripts/migration-baseline.conf lists migrations that already
# ran on ONE database, recorded so they are not run again. It used to be seeded
# whenever public.hive_schema_migrations was empty, and an empty ledger is also
# exactly what a brand new database has, so a fresh install recorded every listed
# migration as applied without executing it and still reported success (issue
# #676). These scenarios hold the three behaviours apart:
#
#   1. empty database    the FULL chain runs. Proven by counting the migrations
#                        actually EXECUTED against the number of files in
#                        supabase/migrations, never by the exit code.
#   2. populated ledger  unchanged from before: the baseline is inert, only files
#                        missing from the ledger run, and the schema is not
#                        probed at all.
#   3. ambiguous state   refuse to proceed, apply nothing, record nothing, and
#                        say exactly what is needed.
#
# Plus the case the baseline exists for (a database that predates the ledger:
# seed it, then apply only the remainder) and the override guards (an operator
# flag that contradicts the schema is rejected rather than obeyed, and a run that
# cannot probe refuses to guess).
#
# How to run
# ----------
# Docker only, no host toolchain. supabase/postgres is the image the demo box
# cutover uses, and matters here: a plain postgres or pgvector image has no
# supabase_auth_admin role, whose absence makes 20260516_07 fail outright, and
# its ambient extension set is what makes the fresh-vs-preexisting decision
# ignore extensions.
#
#   docker network create hivemig
#   docker run -d --name migdb --network hivemig -e POSTGRES_PASSWORD=postgres \
#     supabase/postgres:17.6.1.136
#   # any image with psql, bash, coreutils and python3, e.g.
#   #   FROM postgres:17-alpine
#   #   RUN apk add --no-cache bash python3 coreutils
#   docker run --rm --network hivemig -v "$PWD:/workspace" -w /workspace \
#     -e PGHOST=migdb -e PGUSER=postgres -e PGPASSWORD=postgres \
#     -e PGDATABASE=postgres <that image> bash scripts/test-apply-migrations.sh
#
# PGDATABASE is used only as the maintenance database for CREATE and DROP
# DATABASE; every scenario runs in its own hive_migtest_* database.
#
# The auth schema, and why a real fresh install needs GoTrue first
# ---------------------------------------------------------------
# 13 migrations foreign-key to auth.users and 10 call auth.uid() or auth.jwt().
# GoTrue owns all of those on a real Supabase project.
# deploy/supabase/init/00-extensions.sql creates the auth SCHEMA and the anon,
# authenticated and service_role roles but not the auth objects themselves, so
# on a bare self-hosted database the chain fails immediately, at
# 20260328_01_identity_foundation.sql, on a missing auth.users. That ordering
# constraint belongs to the bootstrap, not to this script, so the scratch
# databases below get the same stand-in objects GoTrue would have created and
# the applier is tested against the state it will really see.
set -euo pipefail
export LC_COLLATE=C

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
migrations_dir="$repo_root/supabase/migrations"
applier="$repo_root/scripts/apply-migrations.sh"
init_sql="$repo_root/deploy/supabase/init/00-extensions.sql"
maintenance_db="${PGDATABASE:-postgres}"

file_count="$(find "$migrations_dir" -maxdepth 1 -name '*.sql' | wc -l | tr -d ' ')"
# The baseline count comes from the applier's own parser (--check reads the file
# and touches no database), not from a grep of the conf file here. One convention
# across all three readers of that file: an applied entry appears exactly ONCE and
# surrounding whitespace is insignificant, so a raw count and a de-duplicated count
# are the same number, and a duplicated or differently spaced entry is one named
# error instead of three disagreeing totals. See scripts/apply-migrations.sh.
#
# Its output is echoed on failure rather than piped straight into an extractor,
# because the applier reports on stdout: piping would swallow the very message
# that says what is wrong with the file.
if ! baseline_check="$("$applier" --check 2>&1)"; then
  echo "$baseline_check"
  echo "FAILED: the baseline file does not parse, so no scenario below would mean anything"
  exit 1
fi
baseline_count="${baseline_check##*applied=}"
baseline_count="${baseline_count%% *}"
remainder=$((file_count - baseline_count))

failures=0

scenario() { echo; echo "=== $1"; }
ok()  { echo "  ok   $*"; }
bad() { echo "  FAIL $*"; failures=$((failures + 1)); }
check() { # check <description> <expected> <actual>
  if [ "$2" = "$3" ]; then ok "$1 = $2"; else bad "$1: expected '$2', got '$3'"; fi
}
nonzero() {
  if [ "$status" -ne 0 ]; then ok "exit $status (non-zero)"; else bad "expected a non-zero exit, got 0"; fi
}
contains() {
  if grep -qF -- "$1" "$log"; then ok "output mentions '$1'"; else bad "output does not mention '$1'"; fi
}
lacks() {
  if grep -qF -- "$1" "$log"; then bad "output unexpectedly mentions '$1'"; else ok "output does not mention '$1'"; fi
}

q()     { PGDATABASE="$1" psql --no-psqlrc -qtAX -v ON_ERROR_STOP=1 -c "$2"; }
maint() { PGDATABASE="$maintenance_db" psql --no-psqlrc -qtAX -v ON_ERROR_STOP=1 -c "$1" >/dev/null; }

ledger_rows()    { q "$1" "SELECT count(*) FROM public.hive_schema_migrations"; }
ledger_rows_of() { q "$1" "SELECT count(*) FROM public.hive_schema_migrations WHERE source='$2'"; }
# Tables a migration would have created. The ledger lives in public too and is
# created by the applier rather than by a migration, so it does not count.
public_tables() {
  q "$1" "SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
          WHERE n.nspname='public' AND c.relkind='r'
            AND c.relname <> 'hive_schema_migrations'"
}

new_db() { # a scratch database holding only what Supabase itself provides
  maint "DROP DATABASE IF EXISTS $1 WITH (FORCE)"
  maint "CREATE DATABASE $1"
  PGDATABASE="$1" psql --no-psqlrc -qX -v ON_ERROR_STOP=1 -f "$init_sql" >/dev/null
  # The GoTrue-owned objects. Real GoTrue creates far more; these are the ones
  # supabase/migrations actually references.
  PGDATABASE="$1" psql --no-psqlrc -qX -v ON_ERROR_STOP=1 >/dev/null <<'SQL'
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='auditor_ro') THEN
    CREATE ROLE auditor_ro NOLOGIN;
  END IF;
END $$;
CREATE TABLE IF NOT EXISTS auth.users (
  id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  email              TEXT,
  raw_user_meta_data JSONB NOT NULL DEFAULT '{}'::jsonb
);
CREATE OR REPLACE FUNCTION auth.uid() RETURNS uuid LANGUAGE sql STABLE AS $$
  SELECT NULLIF(current_setting('request.jwt.claim.sub', true), '')::uuid $$;
CREATE OR REPLACE FUNCTION auth.jwt() RETURNS jsonb LANGUAGE sql STABLE AS $$
  SELECT NULLIF(current_setting('request.jwt.claims', true), '')::jsonb $$;
SQL
}

log=""
status=0
# run_applier [VAR=value ...] <db> [args...]; combined output in $log, exit in $status
#
# Leading VAR=value words are the environment for THIS run only, and they are
# handed to env(1) rather than written as a prefix assignment on the call. A
# prefix assignment before a shell FUNCTION is not scoped the way one before an
# external command is: it is an assignment in the calling shell. Measured, in
# posix mode (which is how bash behaves when invoked as sh), bash 4.4 and 5.0
# leave the variable set after the function returns and bash 5.1 and later do
# not. A leak there would carry HIVE_MIGRATION_BASELINE or a PATH shim from one
# scenario into every later one, and these scenarios are the whole evidence for
# the fix, so they must not be able to influence each other on any shell.
run_applier() {
  local envs=()
  while [ "$#" -gt 0 ] && [ "${1#*=}" != "$1" ]; do envs+=("$1"); shift; done
  local db="$1"; shift
  log="$(mktemp)"
  status=0
  env ${envs[@]+"${envs[@]}"} PGDATABASE="$db" "$applier" "$@" >"$log" 2>&1 || status=$?
}

echo "migration files: $file_count, baseline entries: $baseline_count, remainder: $remainder"

# ---------------------------------------------------------------------------
db_fresh=hive_migtest_fresh
new_db "$db_fresh"

scenario "an override that contradicts a fresh schema is rejected"
run_applier HIVE_MIGRATION_BASELINE=seed "$db_fresh"
nonzero
contains "contradicts the schema"
check "ledger rows" 0 "$(ledger_rows "$db_fresh")"
check "public tables" 0 "$(public_tables "$db_fresh")"

scenario "an empty database applies the FULL chain"
run_applier "$db_fresh"
check "exit status" 0 "$status"
contains "baseline_state=fresh"
contains "the baseline is NOT seeded"
# THE BOUND: migrations EXECUTED, counted in the ledger and in the run's own
# report, both equal to the number of files on disk. Not the exit code.
check "executed per ledger" "$file_count" "$(ledger_rows_of "$db_fresh" applied)"
check "recorded as baseline without running" 0 "$(ledger_rows_of "$db_fresh" baseline)"
check "ledger rows" "$file_count" "$(ledger_rows "$db_fresh")"
check "reported applied count" "applied $file_count migration(s)" "$(tail -1 "$log")"
check "the schema really exists" t \
  "$(q "$db_fresh" "SELECT to_regclass('public.tenants') IS NOT NULL
                      AND to_regclass('public.api_keys') IS NOT NULL
                      AND to_regclass('public.rag_documents') IS NOT NULL")"

scenario "a populated ledger missing one file applies exactly that file"
q "$db_fresh" "DELETE FROM public.hive_schema_migrations
               WHERE filename='20260717_03_rag_embedding_dim_drop_check.sql'" >/dev/null
run_applier "$db_fresh"
check "exit status" 0 "$status"
lacks "baseline_state="
lacks "seeding"
check "reported applied count" "applied 1 migration(s)" "$(tail -1 "$log")"
check "ledger rows" "$file_count" "$(ledger_rows "$db_fresh")"

scenario "a database that predates the ledger seeds the baseline and applies the rest"
q "$db_fresh" "TRUNCATE public.hive_schema_migrations" >/dev/null
run_applier "$db_fresh" --dry-run
check "exit status" 0 "$status"
contains "baseline_state=preexisting"
check "pending count" "$remainder" "$(grep -c '^  [0-9]' "$log")"
check "still nothing recorded by a dry run" 0 "$(ledger_rows "$db_fresh")"

# ---------------------------------------------------------------------------
scenario "a populated ledger is authoritative and the schema is never probed"
db_ledger=hive_migtest_ledger
new_db "$db_ledger"
# The shape of the live demo database today: every file recorded, so the
# baseline is inert. The ledger DDL comes from the applier itself rather than a
# copy of it, so this cannot drift from the real table definition.
"$applier" --print-baseline-sql | PGDATABASE="$db_ledger" psql --no-psqlrc -qX -v ON_ERROR_STOP=1 >/dev/null
for path in "$migrations_dir"/*.sql; do
  q "$db_ledger" "INSERT INTO public.hive_schema_migrations (filename, sha256, source)
                  VALUES ('${path##*/}', 'x', 'applied') ON CONFLICT (filename) DO NOTHING" >/dev/null
done
run_applier "$db_ledger"
check "exit status" 0 "$status"
lacks "baseline_state="
lacks "seeding"
check "report" "no pending migrations ($file_count files, all recorded)" "$(tail -1 "$log")"
check "ledger rows" "$file_count" "$(ledger_rows "$db_ledger")"
check "nothing executed (public tables)" 0 "$(public_tables "$db_ledger")"

# ---------------------------------------------------------------------------
scenario "a half applied schema with an empty ledger aborts"
db_partial=hive_migtest_partial
new_db "$db_partial"
# One baseline migration applied and no ledger: neither a fresh install nor the
# history the baseline describes.
PGDATABASE="$db_partial" psql --no-psqlrc -qX -v ON_ERROR_STOP=1 \
  -f "$migrations_dir/20260328_01_identity_foundation.sql" >/dev/null
run_applier "$db_partial"
nonzero
contains "baseline_state=ambiguous"
contains "cannot tell whether"
contains "HIVE_MIGRATION_BASELINE=seed"
check "ledger rows" 0 "$(ledger_rows "$db_partial")"
check "nothing further executed" t "$(q "$db_partial" "SELECT to_regclass('public.tenants') IS NULL")"

scenario "an operator override resolves the ambiguous state"
run_applier HIVE_MIGRATION_BASELINE=seed "$db_partial" --dry-run
check "exit status" 0 "$status"
contains "on the operator's instruction"
check "pending count" "$remainder" "$(grep -c '^  [0-9]' "$log")"
check "still nothing recorded" 0 "$(ledger_rows "$db_partial")"

scenario "a run that cannot probe the schema refuses to guess"
# The probe is the only thing that can tell the two empty-ledger cases apart, so
# a run that cannot execute it must stop rather than fall back to seeding.
shim="$(mktemp -d)"
printf '#!/bin/sh\nexit 1\n' >"$shim/python3"
chmod +x "$shim/python3"
run_applier "PATH=$shim:$PATH" "$db_partial"
nonzero
contains "the schema probe failed"
contains "cannot tell whether"
check "still nothing recorded" 0 "$(ledger_rows "$db_partial")"
rm -rf "$shim"

scenario "a bad override value is rejected"
run_applier HIVE_MIGRATION_BASELINE=maybe "$db_partial"
nonzero
contains "must be seed or ignore"
check "still nothing recorded" 0 "$(ledger_rows "$db_partial")"

# ---------------------------------------------------------------------------
echo
for db in "$db_fresh" "$db_ledger" "$db_partial"; do
  maint "DROP DATABASE IF EXISTS $db WITH (FORCE)"
done

if [ "$failures" -ne 0 ]; then
  echo "FAILED: $failures assertion(s)"
  exit 1
fi
echo "PASS: every scenario held"
