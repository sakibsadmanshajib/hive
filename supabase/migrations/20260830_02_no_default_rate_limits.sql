-- 20260830_02_no_default_rate_limits.sql
--
-- Owner directive, 2026-08-30: Hive imposes NO default rate limit anywhere. A
-- rate limit exists only where someone explicitly configured one.
--
-- 20260331_04_api_key_usage_and_limits.sql declared both rate policy tables
-- with `requests_per_minute integer not null default 60` and
-- `tokens_per_minute integer not null default 120000`. Nothing in this repo
-- ever passes those columns on insert, so every account and every key in the
-- database is carrying a per minute cap that no operator and no customer ever
-- chose, and the edge limiter enforces it
-- (apps/edge-api/internal/authz/ratelimit.go checkScope).
--
-- Zero means "no limit at this layer" throughout that limiter, which is an
-- existing documented convention, not a new one: checkScope only opens a Redis
-- window when the limit is strictly positive, so a zeroed row costs nothing at
-- request time and cannot be denied by a degraded limiter either.
--
-- The console can still set a limit. UpdateLimits validates against
-- RateLimitRPMMax / RateLimitTPMMax and writes an explicit value; only the
-- default changes.
--
-- NO BACKFILL, deliberately, and this is the load-bearing decision in the file.
--
-- An earlier draft cleared every row holding the exact old default pair (60 and
-- 120000 together), on the theory that such a row was written by an insert that
-- named no limit. Checking the writers rather than assuming shows that theory
-- is false. The ONLY statement in the tree that writes either table is
-- UpdateLimits in apps/control-plane/internal/apikeys/repository.go, and it
-- always passes an explicit RPM and TPM from the console request; no migration
-- inserts into either table, and no other Go path does. account_rate_policies
-- has no writer at all.
--
-- So every row that exists in either table today was typed by an operator, and
-- a value-matched backfill would erase a genuinely explicit limit, which the
-- directive forbids. An account or key with no row at all was already the
-- common case and is handled by defaultRatePolicy, zeroed in the same change.
--
-- The column default is still dropped, because it is a live trap rather than a
-- dead one: any future insert that omits these columns would silently acquire
-- a 60 and 120000 cap, which is exactly the class of default this directive
-- removes.

-- One transaction with SET LOCAL, which is this repo's house style (see
-- 20260824_02_free_pool_router.sql:161-163 and siblings) and is also what makes
-- the timeout cover all four statements rather than three of them.
--
-- Why a timeout at all, given these are instant: ALTER TABLE ... SET DEFAULT is
-- a catalog-only change, but it still takes ACCESS EXCLUSIVE, and both of these
-- tables are read on the authentication snapshot path. Untimed, the ALTER
-- queues behind any one long-running read, and every subsequent read of that
-- table then queues behind the ALTER, so the symptom is not a slow migration,
-- it is authentication stalling for every principal until the lock is granted.
-- Failing fast and being re-run is strictly better.
--
-- SET LOCAL rather than a bare SET: a bare SET leaks the value into the rest of
-- the session, and it takes effect only from the statement that follows it, so
-- an ALTER placed above it runs untimed. Inside BEGIN both problems disappear,
-- and the four ALTERs land or roll back together.
BEGIN;

SET LOCAL lock_timeout = '5s';

ALTER TABLE public.account_rate_policies ALTER COLUMN requests_per_minute SET DEFAULT 0;
ALTER TABLE public.account_rate_policies ALTER COLUMN tokens_per_minute SET DEFAULT 0;
ALTER TABLE public.api_key_rate_policies ALTER COLUMN requests_per_minute SET DEFAULT 0;
ALTER TABLE public.api_key_rate_policies ALTER COLUMN tokens_per_minute SET DEFAULT 0;

COMMIT;
