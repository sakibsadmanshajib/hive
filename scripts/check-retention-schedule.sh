#!/usr/bin/env bash
# check-retention-schedule.sh -- Report whether the nightly metering retention
# job is actually scheduled. Reports, never blocks.
#
# Why this exists (issue #645)
# ---------------------------
# 20260729_02_metering_shadow_verdicts_retention.sql sets the purge job up in
# two guarded blocks: one creates the pg_cron extension when
# pg_available_extensions lists it, wrapped in an exception handler, and a later
# one calls cron.schedule only when pg_extension shows pg_cron installed. Both
# guards are correct, because nothing else in that migration depends on pg_cron
# and aborting the deploy over it would be wrong.
#
# The problem is what those guards leave behind. Each degrades to a RAISE
# NOTICE, so a host where the extension cannot be created gets no nightly
# retention at all, and the only evidence is a line in a job log nobody reads
# after the run goes green. metering_shadow_verdicts then grows without bound
# and the first real symptom is disk pressure.
#
# That is the same defect class this repo spent a day removing: a guard that
# correctly avoids aborting also converts a hard failure into an invisible one.
# The fix is not to remove the guard, it is to make the absence visible. This
# script is the "make it visible" half.
#
# Exit status
# -----------
# Always 0. Absence is reported, never fatal. On a host without pg_cron the
# extension genuinely cannot be created, and blocking every deploy for that
# would be the mistake the guards already avoid. The signal is a GitHub
# ::warning:: annotation, which surfaces on the workflow run summary rather than
# only in log text.
#
# This is deliberately NOT a gate. It does not weaken one either: the migration
# step that runs before it still uses ON_ERROR_STOP=1 and still fails the deploy
# on a real migration error.
#
# Connection settings come from libpq environment variables (PGHOST, PGPORT,
# PGUSER, PGDATABASE, PGPASSWORD), same as apply-migrations.sh. No DSN is built
# here, because DSN parameters are per driver and psql is a libpq client.

set -euo pipefail

JOB_NAME='metering-shadow-verdicts-purge'

psql_q() { psql --no-psqlrc -qtAX -v ON_ERROR_STOP=1 "$@"; }

# pg_extension has to be checked separately and first: when pg_cron is not
# installed the cron schema does not exist either, and a single query
# mentioning cron.job would fail to parse rather than returning a verdict,
# no matter how the CASE is arranged.
if ! extension_present="$(psql_q -c "SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_cron')")"; then
  echo "::warning::Could not determine whether nightly metering retention is scheduled: the pg_cron extension query failed. Retention status is UNKNOWN. Check public.metering_shadow_verdicts growth manually."
  exit 0
fi

if [ "$extension_present" != "t" ]; then
  echo "::warning::Nightly metering retention is NOT scheduled: the pg_cron extension is not installed on this database, so 20260729_02 skipped cron.schedule. public.metering_shadow_verdicts will grow without bound. Either install pg_cron (it must also be in shared_preload_libraries, see issue #615) or invoke CALL public.purge_metering_shadow_verdicts() from an external scheduler."
  exit 0
fi

# Reached only when pg_cron is installed, so cron.job is guaranteed to resolve.
# active is checked separately from existence because a disabled job looks
# present in every naive check while scheduling exactly nothing.
if ! state="$(psql_q -c "
  SELECT CASE
    WHEN count(*) = 0 THEN 'SCHEDULE_MISSING'
    WHEN count(*) FILTER (WHERE active) = 0 THEN 'SCHEDULE_INACTIVE'
    ELSE 'OK'
  END
  FROM cron.job
  WHERE jobname = '$JOB_NAME'")"; then
  echo "::warning::Could not determine whether nightly metering retention is scheduled: pg_cron is installed but the cron.job query failed. Retention status is UNKNOWN."
  exit 0
fi

case "$state" in
  SCHEDULE_MISSING)
    echo "::warning::Nightly metering retention is NOT scheduled: pg_cron is installed but no cron job named '$JOB_NAME' exists. 20260729_02 either has not run on this database or its cron.schedule call did not take effect. public.metering_shadow_verdicts will grow without bound."
    ;;
  SCHEDULE_INACTIVE)
    echo "::warning::Nightly metering retention is DISABLED: the cron job '$JOB_NAME' exists but is marked inactive, so it will never fire. public.metering_shadow_verdicts will grow without bound. Re-enable it with UPDATE cron.job SET active = true WHERE jobname = '$JOB_NAME'."
    ;;
  OK)
    schedule="$(psql_q -c "SELECT schedule FROM cron.job WHERE jobname = '$JOB_NAME' AND active LIMIT 1")"
    echo "::notice::Nightly metering retention is scheduled and active ('$JOB_NAME', $schedule)."
    ;;
  *)
    echo "::warning::Unexpected retention schedule state '$state' for '$JOB_NAME'. Retention status is UNKNOWN."
    ;;
esac

exit 0
