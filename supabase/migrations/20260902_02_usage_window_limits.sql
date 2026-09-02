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
-- ============================================================================
-- THIS IS STAGE ONE OF TWO. STAGE TWO IS AT THE BOTTOM OF THIS COMMENT AND HAS
-- NOT BEEN WRITTEN YET. Do not fold it into this file.
-- ============================================================================
--
-- Why the split. deploy-demo-box.yml runs the `migrate` job to completion and
-- only then starts the `deploy` job, which builds the images. For the whole
-- duration of that build the binary serving traffic is the PREVIOUS one, and
-- that binary scans rolling_five_hour_limit and weekly_limit straight into
-- int64 with no COALESCE. A migration that NULLs those columns therefore makes
-- every ResolveSnapshot on the running gateway fail with "cannot scan NULL
-- into *int64"; with a sixty second snapshot TTL that is a total auth outage
-- on the live box for the length of an image build. The first draft of this
-- file did exactly that to all 62 account rows and all 287 key rows.
--
-- So stage one changes only what the currently-running binary can still read,
-- and stage two, on the deploy AFTER the new binaries are serving, performs the
-- rewrite. This is the same class as the incident that produced the revert in
-- d74013592: the repository is not the running system.
--
-- Three things change here, and only these three.
--
-- 1. The four window columns become nullable, and lose their zero default, so
--    NULL can start being the representation of "not configured". Existing
--    zeros are LEFT IN PLACE: the new code reads zero as unset exactly as the
--    old code does, so nothing behaves differently, and the old binary keeps
--    reading a non-NULL value out of every row that exists when it applies.
--
--    Dropping the default does leave one narrow window: public.UpdateLimits
--    inserts an api_key_rate_policies row without naming these columns, so a
--    row CREATED between this migration and the new image coming up holds NULL
--    and the old binary cannot scan it. That is one key, only if somebody edits
--    a key's RPM or TPM during an image build, and it heals the moment the new
--    image is live. The default cannot stay, because the CHECK below rejects
--    the 0 it would insert. Accepting a defect that needs a console edit inside
--    a two minute window is not the same order as NULLing 349 live rows.
--
-- 2. The CHECK arrives NOT VALID. It constrains every future write -- an
--    operator typing 0 to mean "allow nothing" is refused rather than silently
--    given "no limit at all", which is the most dangerous possible misreading
--    on an authorization gate -- while leaving the existing zeros unexamined
--    until stage two rewrites them. NOT VALID is what makes stage one and stage
--    two separable at all.
--
-- 3. account_rate_policies gains weekly_anchor_at, the instant the account's
--    week rolls over. The weekly window is ANCHORED, not rolling (owner ruling
--    D-069, issue #1684): the allowance restores in full at the anchor rather
--    than leaking back one day at a time. A new column the old binary does not
--    select is invisible to it.
--
--    The backfill is explicit and NOT left to the column default. now() is
--    STABLE, not VOLATILE, so PostgreSQL takes the fast ADD COLUMN path and
--    stores ONE attmissingval for the whole table: every existing account would
--    have received the identical anchor and every account's weekly reset would
--    have landed on the same instant every week, which is precisely the
--    synchronised reset this column exists to avoid. random() is VOLATILE and
--    is evaluated per row, so the UPDATE below is what actually spreads them.
--
-- NO BACKFILL of limits, for the reason 20260830_02_no_default_rate_limits.sql
-- gives at length: a limit exists only where somebody configured one, and this
-- migration must not become the thing that quietly configures 62 of them.
--
-- One transaction with SET LOCAL, this repo's house style. lock_timeout
-- matters here for the same reason it did in 20260830_02: both tables are read
-- on the authentication snapshot path, ALTER TABLE takes ACCESS EXCLUSIVE, and
-- an untimed ALTER queued behind one long read stalls authentication for every
-- principal until it is granted.
--
-- ----------------------------------------------------------------------------
-- STAGE TWO, to be written as its own migration and merged only after the
-- binaries carrying this PR are the ones serving production. Tracked so it
-- cannot be lost. Verbatim:
--
--   BEGIN;
--   SET LOCAL lock_timeout = '5s';
--   ALTER TABLE public.account_rate_policies ALTER COLUMN rolling_five_hour_limit DROP DEFAULT;
--   ALTER TABLE public.account_rate_policies ALTER COLUMN weekly_limit DROP DEFAULT;
--   UPDATE public.account_rate_policies SET rolling_five_hour_limit = NULL WHERE rolling_five_hour_limit = 0;
--   UPDATE public.account_rate_policies SET weekly_limit = NULL WHERE weekly_limit = 0;
--   UPDATE public.api_key_rate_policies SET rolling_five_hour_limit = NULL WHERE rolling_five_hour_limit = 0;
--   UPDATE public.api_key_rate_policies SET weekly_limit = NULL WHERE weekly_limit = 0;
--   ALTER TABLE public.account_rate_policies VALIDATE CONSTRAINT account_rate_policies_five_hour_positive;
--   ALTER TABLE public.account_rate_policies VALIDATE CONSTRAINT account_rate_policies_weekly_positive;
--   ALTER TABLE public.api_key_rate_policies VALIDATE CONSTRAINT api_key_rate_policies_five_hour_positive;
--   ALTER TABLE public.api_key_rate_policies VALIDATE CONSTRAINT api_key_rate_policies_weekly_positive;
--   COMMIT;
--
-- (The two DROP DEFAULTs are on account_rate_policies only; the api_key ones
-- are dropped in stage one below, because that is the table whose insert path
-- would otherwise trip the CHECK.)
-- ----------------------------------------------------------------------------
BEGIN;

SET LOCAL lock_timeout = '5s';

ALTER TABLE public.account_rate_policies ALTER COLUMN rolling_five_hour_limit DROP NOT NULL;
ALTER TABLE public.account_rate_policies ALTER COLUMN weekly_limit DROP NOT NULL;

ALTER TABLE public.api_key_rate_policies ALTER COLUMN rolling_five_hour_limit DROP NOT NULL;
ALTER TABLE public.api_key_rate_policies ALTER COLUMN rolling_five_hour_limit DROP DEFAULT;
ALTER TABLE public.api_key_rate_policies ALTER COLUMN weekly_limit DROP NOT NULL;
ALTER TABLE public.api_key_rate_policies ALTER COLUMN weekly_limit DROP DEFAULT;

-- NOT VALID: constrains every future write, leaves the existing zeros for
-- stage two. See the header.
ALTER TABLE public.account_rate_policies
  ADD CONSTRAINT account_rate_policies_five_hour_positive
  CHECK (rolling_five_hour_limit IS NULL OR rolling_five_hour_limit > 0) NOT VALID;
ALTER TABLE public.account_rate_policies
  ADD CONSTRAINT account_rate_policies_weekly_positive
  CHECK (weekly_limit IS NULL OR weekly_limit > 0) NOT VALID;
ALTER TABLE public.api_key_rate_policies
  ADD CONSTRAINT api_key_rate_policies_five_hour_positive
  CHECK (rolling_five_hour_limit IS NULL OR rolling_five_hour_limit > 0) NOT VALID;
ALTER TABLE public.api_key_rate_policies
  ADD CONSTRAINT api_key_rate_policies_weekly_positive
  CHECK (weekly_limit IS NULL OR weekly_limit > 0) NOT VALID;

ALTER TABLE public.account_rate_policies
  ADD COLUMN IF NOT EXISTS weekly_anchor_at timestamptz NOT NULL DEFAULT now();

-- Spread the existing rows' anchors. See point 3 in the header: the column
-- default alone gives every one of them the same instant.
UPDATE public.account_rate_policies
   SET weekly_anchor_at = now() - (random() * interval '7 days');

COMMENT ON COLUMN public.account_rate_policies.rolling_five_hour_limit IS
  'Session allowance over a sliding five hour window, in weighted usage score. NULL means no session limit is configured; 0 is rejected by CHECK so the two can never be confused (issue #1725). Rows predating this migration may still hold 0, which every reader treats as unset, until the stage two rewrite.';
COMMENT ON COLUMN public.account_rate_policies.weekly_limit IS
  'Weekly allowance, restored in full at weekly_anchor_at (D-069). NULL means no weekly limit is configured.';
COMMENT ON COLUMN public.account_rate_policies.weekly_anchor_at IS
  'The instant this account''s weekly allowance resets, repeating every seven days. Assigned per account so resets are spread rather than synchronised. Never in the future: the writer rejects one, because the limiter and the console derive the weekly bucket key from it and a future anchor puts them on different grids.';

COMMIT;
