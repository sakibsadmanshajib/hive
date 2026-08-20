#!/usr/bin/env bash
# ci-throwaway-db.sh -- bring the full Hive schema up on a throwaway Postgres,
# so a CI job that needs a database boots its own instead of sharing the one
# the live demo stack and every other job are already on.
#
# Why
# ---
# CI, the coding agents and live chat traffic all pointed at one hosted
# Supabase project. Its Supavisor session-mode pool is capped at 15 clients, so
# a job that booted a stack could and did take the live chat surface offline,
# and every job that wrote fixture rows grew a shared-fixture surface that
# another job then had to purge. A job that boots its own stack needs a SCHEMA,
# not shared state, and this script produces one.
#
# What it does, in order
#   1. deploy/supabase/init/00-extensions.sql   the roles and schemas the
#      self-hosted stack creates (anon, authenticated, service_role,
#      supabase_admin, hive_app, plus the auth, storage, extensions and
#      graphql_public schemas).
#   2. .github/ci/test-db-bootstrap.sql          the Supabase-managed objects
#      supabase/migrations assumes already exist: auditor_ro,
#      supabase_auth_admin, and the GoTrue-owned auth.users, auth.uid() and
#      auth.jwt(). Thirteen migrations foreign-key to auth.users and ten call
#      auth.uid() or auth.jwt(), so without these the chain dies at
#      20260328_01_identity_foundation.sql. Pass --gotrue when a real GoTrue
#      has already migrated the auth schema, and the stand-ins are skipped.
#   3. scripts/apply-migrations.sh               the real chain, in order, one
#      ledger row per file. The same applier the demo box runs, so a throwaway
#      matches production rather than approximating it.
#
# It then ASSERTS the outcome rather than trusting the exit code, because the
# failure mode that matters here is silent: the applier's baseline describes a
# database that predates the ledger, and a fresh one has to EXECUTE every file
# rather than record it as history it never ran. Checked by counting ledger
# rows with source=applied against the files on disk, which is the bound issue
# #676 exists for.
#
# pg_cron, stated rather than left to be discovered: 20260729_02 wraps its
# CREATE EXTENSION and its cron.schedule call in availability guards and raises
# a NOTICE when the extension is absent, so on an image without pg_cron the
# file applies, reports success, and leaves the nightly retention purge
# unscheduled. Every other object it creates (the config table, the purge
# procedure) is created either way. That is an acceptable difference for a
# database that lives for the length of one CI job and is never there at 21:00
# UTC, but it is NOT acceptable for it to be invisible, so this script says
# which of the two happened, every run, in a line naming the job.
#
# Connection settings come from libpq environment variables (PGHOST, PGPORT,
# PGUSER, PGPASSWORD, PGDATABASE) that the caller exports. No DSN is built or
# parsed here: DSN parameters are per-driver, and mixing them has already
# crash-looped a container once.
#
# Usage
#   PGHOST=127.0.0.1 PGUSER=postgres PGPASSWORD=... PGDATABASE=... \
#     scripts/ci-throwaway-db.sh [--gotrue]

set -euo pipefail

gotrue_owns_auth=0
for arg in "$@"; do
  case "$arg" in
    --gotrue) gotrue_owns_auth=1 ;;
    *) echo "unknown argument: $arg" >&2; exit 2 ;;
  esac
done

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
init_sql="$repo_root/deploy/supabase/init/00-extensions.sql"
stub_sql="$repo_root/.github/ci/test-db-bootstrap.sql"
migrations_dir="$repo_root/supabase/migrations"

q() { psql --no-psqlrc -qtAX -v ON_ERROR_STOP=1 -c "$1"; }

echo "==> Supabase-managed roles and schemas"
psql --no-psqlrc -qX -v ON_ERROR_STOP=1 -f "$init_sql" >/dev/null

if [ "$gotrue_owns_auth" -eq 1 ]; then
  echo "==> auth schema left to GoTrue (--gotrue)"
  # auditor_ro and supabase_auth_admin are referenced by the migrations and are
  # not GoTrue objects, so they are still ours to create.
  psql --no-psqlrc -qX -v ON_ERROR_STOP=1 >/dev/null <<'SQL'
DO $$
DECLARE r text;
BEGIN
  FOREACH r IN ARRAY ARRAY['auditor_ro', 'supabase_auth_admin'] LOOP
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = r) THEN
      EXECUTE format('CREATE ROLE %I NOLOGIN', r);
    END IF;
  END LOOP;
END
$$;
SQL
  if [ "$(q "SELECT to_regclass('auth.users') IS NOT NULL")" != "t" ]; then
    echo "::error::--gotrue was passed but auth.users does not exist. GoTrue has to finish its own migrations before this chain runs, or 20260328_01_identity_foundation.sql fails on the foreign key."
    exit 1
  fi
else
  echo "==> GoTrue stand-in objects"
  psql --no-psqlrc -qX -v ON_ERROR_STOP=1 -f "$stub_sql" >/dev/null
fi

echo "==> migration chain"
"$repo_root/scripts/apply-migrations.sh"

failures=0
fail() { echo "::error::$*"; failures=$((failures + 1)); }

file_count="$(find "$migrations_dir" -maxdepth 1 -name '*.sql' | wc -l | tr -d ' ')"
executed="$(q "SELECT count(*) FROM public.hive_schema_migrations WHERE source='applied'")"
recorded="$(q "SELECT count(*) FROM public.hive_schema_migrations WHERE source='baseline'")"
if [ "$executed" != "$file_count" ]; then
  fail "only $executed of $file_count migrations were executed on this throwaway database"
fi
if [ "$recorded" != "0" ]; then
  fail "$recorded migration(s) were recorded as baseline history without ever running"
fi

for rel in public.tenants public.accounts public.api_keys public.usage_events \
           public.rag_documents public.tenant_billing_accounts; do
  if [ "$(q "SELECT to_regclass('$rel') IS NOT NULL")" != "t" ]; then
    fail "$rel is missing after the migration chain"
  fi
done

# Say out loud which pg_cron branch 20260729_02 took, so an unscheduled purge is
# a stated fact in the log rather than a silent difference from production.
if [ "$(q "SELECT count(*) FROM pg_extension WHERE extname='pg_cron'")" = "0" ]; then
  echo "pg_cron: absent on this image, so 20260729_02 created its config table and purge procedure but scheduled no nightly job. Expected here; a throwaway database does not live long enough to run one."
elif [ "$(q "SELECT count(*) FROM cron.job WHERE jobname='metering-shadow-verdicts-purge'")" = "1" ]; then
  echo "pg_cron: installed, and the metering-shadow-verdicts-purge job is scheduled."
else
  fail "pg_cron is installed but 20260729_02 scheduled no metering-shadow-verdicts-purge job, which is neither of the two branches that file documents"
fi

if [ "$failures" -ne 0 ]; then
  echo "throwaway database bootstrap FAILED with $failures problem(s)"
  exit 1
fi
echo "throwaway database ready: $executed of $file_count migrations executed"
