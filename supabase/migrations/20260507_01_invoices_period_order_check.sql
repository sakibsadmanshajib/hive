-- Phase 14 follow-up — enforce invoice period ordering at schema level.
-- Original 20260428_01 omitted CHECK (period_end > period_start), so an
-- invalid range row could be inserted. Forward-only migration adds the
-- constraint with NOT VALID + VALIDATE so it does not block existing rows
-- (Phase 14 deployed against an empty `invoices` table, but defensive).

ALTER TABLE public.invoices
    ADD CONSTRAINT invoices_period_order_check
    CHECK (period_end > period_start) NOT VALID;

ALTER TABLE public.invoices
    VALIDATE CONSTRAINT invoices_period_order_check;
