-- Issue #1648 — record the USD to BDT rate an invoice was converted at.
--
-- public.invoices.total_bdt_subunits used to be filled with a raw Hive credit
-- count (1 USD = 1,000,000,000 credits) and rendered as paisa (100 per taka),
-- which overstated the customer-visible figure by about five orders of
-- magnitude. The application now converts credits to taka explicitly, and this
-- column records the rate that conversion used so the arithmetic on a stored
-- row stays reproducible from the append-only ledger months later.
--
-- Nullable on purpose. NULL is the discriminator for a row generated before the
-- fix, that is, a row whose amounts are the conflated credit count and which
-- has to be regenerated rather than trusted. Do not backfill it with a rate:
-- that would make an unconverted row look converted.
--
-- numeric, not float. Forward-only migration (project policy).

ALTER TABLE public.invoices
    ADD COLUMN IF NOT EXISTS usd_bdt_rate numeric(18, 6)
        CHECK (usd_bdt_rate IS NULL OR usd_bdt_rate > 0);

COMMENT ON COLUMN public.invoices.usd_bdt_rate IS
    'USD to BDT rate used to convert ledger credits into the taka amounts on this row, normally the account''s own fx_snapshots effective_rate. NULL means the row PREDATES issue #1648 and its amounts are a raw credit count rather than taka; it does not mean the rate is merely unknown. That reading holds only while invoices.GenerateInvoiceForPeriod is the sole writer of this table, which it is today: anything that later inserts a row by another route must set this column or the discriminator stops being sound.';
