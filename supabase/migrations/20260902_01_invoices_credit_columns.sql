-- Issues #1682 and #1681 — carry the credit quantity on the invoice row, and
-- record which rate source denominated it.
--
-- Two separate problems share these columns.
--
-- #1681: an invoice's consumption quantity is a Hive credit count. Credits are
-- the unit the ledger stores and the unit the product sells; taka is what the
-- customer pays. The row used to keep only the taka figure, so the console and
-- the PDF had nothing to print but a converted number, and a customer could not
-- reconcile the invoice against a balance the rest of the console prints in
-- credits. total_credits stores the quantity itself so no surface has to invert
-- a rate to recover it.
--
-- #1682: rows generated before the #1648 fix hold a raw credit count in
-- total_bdt_subunits, about a hundred thousand times the true taka amount, and
-- their line items hold the same conflation. usd_bdt_rate IS NULL is the
-- discriminator for those rows (see 20260901_01). The repair runs in the
-- application rather than here, because it also has to re-render and re-upload
-- the stored PDF object, which SQL cannot do; this migration only adds the
-- columns the repaired row is written into.
--
-- Both columns are nullable and neither is backfilled. NULL total_credits means
-- the quantity is genuinely unknown for that row, which is true of any invoice
-- generated between the #1648 fix and this change: those rows are correct in
-- taka and must stay byte identical, and inverting their rate to manufacture a
-- credit figure would print a number the ledger never recorded. Surfaces render
-- an absent quantity as absent.
--
-- Forward-only migration (project policy). No UPDATE statement appears in this
-- file on purpose: a money repair that regenerates a customer-facing document
-- belongs where it can fail loudly and be retried, not in a schema step.

ALTER TABLE public.invoices
    ADD COLUMN IF NOT EXISTS total_credits bigint
        CHECK (total_credits IS NULL OR total_credits >= 0);

ALTER TABLE public.invoices
    ADD COLUMN IF NOT EXISTS usd_bdt_rate_source text
        CHECK (usd_bdt_rate_source IS NULL
               OR usd_bdt_rate_source IN ('fx_snapshot', 'env', 'default'));

COMMENT ON COLUMN public.invoices.total_credits IS
    'Hive credit quantity consumed in this period, the unit public.credit_ledger_entries.credits_delta stores (1 USD = 1,000,000,000 credits, decision D-046). This is the invoice''s consumption quantity; total_bdt_subunits is the fiat amount charged for it, converted once at usd_bdt_rate. NULL means the quantity was never recorded for this row, which is true of invoices generated between the issue #1648 fix and issue #1682''s repair. Do not derive it by inverting usd_bdt_rate: that rounds, and it would present a manufactured figure as a ledger reading.';

COMMENT ON COLUMN public.invoices.usd_bdt_rate_source IS
    'Where usd_bdt_rate came from: fx_snapshot (the account''s own public.fx_snapshots row, the rate it bought credits at), env (the HIVE_USD_BDT_RATE operator override), or default (the platform constant). Recorded so an operator can tell an account-specific denomination from a platform fallback without re-running the resolution months later. NULL alongside a NULL rate means the row predates the conversion entirely.';
