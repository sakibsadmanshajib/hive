-- 20260902_02_usage_window_limits.sql
--
-- Issue #1725: make the two usage windows configurable, and make an
-- unconfigured window mean one unambiguous thing.
--
-- rolling_five_hour_limit and weekly_limit have existed since
-- 20260331_04_api_key_usage_and_limits.sql and have never had a writer. Every
-- row in both tables therefore carries the declared default of 0, which the
-- edge limiter reads as "this window is off". That is the whole reason nothing
-- is enforced anywhere today: 62 accounts, 287 keys, zero configured windows,
-- zero counters in Redis.
--
-- Two things change here, and only these two.
--
-- 1. NULL becomes the representation of "not configured", and 0 becomes
--    unrepresentable. Today a single column holds two meanings that a reader
--    cannot tell apart: an operator who types 0 meaning "allow nothing" gets
--    "no limit at all", which is the most dangerous possible misreading on an
--    authorization gate. After this migration the CHECK refuses 0 outright, so
--    a stored value is either absent (unlimited, by the owner's standing
--    directive of 2026-08-30 that Hive imposes no default rate limit) or a
--    real positive allowance. The Go and JSON layers keep reading zero as
--    unset, deliberately: control-plane projects these rows into a Redis
--    snapshot cache with a sixty second TTL, so a new edge-api reads snapshots
--    an older control-plane wrote for up to a minute after every deploy, and
--    if zero started meaning "refuse everything" at the edge, each deploy
--    would take chat and the API down for that minute. Removing the ambiguity
--    at the writer costs nothing and cannot produce that window.
--
-- 2. account_rate_policies gains weekly_anchor_at, the instant the account's
--    week rolls over. The weekly window is ANCHORED, not rolling (owner ruling
--    D-069, issue #1684): the allowance restores in full at the anchor rather
--    than leaking back one day at a time. Defaulting to now() at insert gives
--    every account its own anchor, which is also what keeps every account's
--    reset from landing on one instant every week.
--
-- NO BACKFILL of limits, for the reason 20260830_02_no_default_rate_limits.sql
-- gives at length: a limit exists only where somebody configured one, and this
-- migration must not become the thing that quietly configures 62 of them. The
-- 0 to NULL rewrite below is not a backfill of policy; it is the same "not
-- configured" state written in the representation that can no longer be
-- confused with a deliberate zero, and it is safe precisely because no writer
-- has ever set these columns to anything else.
--
-- One transaction with SET LOCAL, this repo's house style. lock_timeout
-- matters here for the same reason it did in 20260830_02: both tables are read
-- on the authentication snapshot path, ALTER TABLE takes ACCESS EXCLUSIVE, and
-- an untimed ALTER queued behind one long read stalls authentication for every
-- principal until it is granted.
BEGIN;

SET LOCAL lock_timeout = '5s';

ALTER TABLE public.account_rate_policies ALTER COLUMN rolling_five_hour_limit DROP NOT NULL;
ALTER TABLE public.account_rate_policies ALTER COLUMN rolling_five_hour_limit DROP DEFAULT;
ALTER TABLE public.account_rate_policies ALTER COLUMN weekly_limit DROP NOT NULL;
ALTER TABLE public.account_rate_policies ALTER COLUMN weekly_limit DROP DEFAULT;

ALTER TABLE public.api_key_rate_policies ALTER COLUMN rolling_five_hour_limit DROP NOT NULL;
ALTER TABLE public.api_key_rate_policies ALTER COLUMN rolling_five_hour_limit DROP DEFAULT;
ALTER TABLE public.api_key_rate_policies ALTER COLUMN weekly_limit DROP NOT NULL;
ALTER TABLE public.api_key_rate_policies ALTER COLUMN weekly_limit DROP DEFAULT;

UPDATE public.account_rate_policies
   SET rolling_five_hour_limit = NULL
 WHERE rolling_five_hour_limit = 0;
UPDATE public.account_rate_policies
   SET weekly_limit = NULL
 WHERE weekly_limit = 0;
UPDATE public.api_key_rate_policies
   SET rolling_five_hour_limit = NULL
 WHERE rolling_five_hour_limit = 0;
UPDATE public.api_key_rate_policies
   SET weekly_limit = NULL
 WHERE weekly_limit = 0;

ALTER TABLE public.account_rate_policies
  ADD CONSTRAINT account_rate_policies_five_hour_positive
  CHECK (rolling_five_hour_limit IS NULL OR rolling_five_hour_limit > 0);
ALTER TABLE public.account_rate_policies
  ADD CONSTRAINT account_rate_policies_weekly_positive
  CHECK (weekly_limit IS NULL OR weekly_limit > 0);
ALTER TABLE public.api_key_rate_policies
  ADD CONSTRAINT api_key_rate_policies_five_hour_positive
  CHECK (rolling_five_hour_limit IS NULL OR rolling_five_hour_limit > 0);
ALTER TABLE public.api_key_rate_policies
  ADD CONSTRAINT api_key_rate_policies_weekly_positive
  CHECK (weekly_limit IS NULL OR weekly_limit > 0);

ALTER TABLE public.account_rate_policies
  ADD COLUMN IF NOT EXISTS weekly_anchor_at timestamptz NOT NULL DEFAULT now();

COMMENT ON COLUMN public.account_rate_policies.rolling_five_hour_limit IS
  'Session allowance over a sliding five hour window, in weighted usage score. NULL means no session limit is configured; 0 is rejected by CHECK so the two can never be confused (issue #1725).';
COMMENT ON COLUMN public.account_rate_policies.weekly_limit IS
  'Weekly allowance, restored in full at weekly_anchor_at (D-069). NULL means no weekly limit is configured.';
COMMENT ON COLUMN public.account_rate_policies.weekly_anchor_at IS
  'The instant this account''s weekly allowance resets, repeating every seven days. Assigned per account so resets are spread rather than synchronised.';

COMMIT;
