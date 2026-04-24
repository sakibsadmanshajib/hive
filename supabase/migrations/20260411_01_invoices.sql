CREATE TABLE payment_invoices (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    payment_intent_id UUID NOT NULL REFERENCES payment_intents(id),
    invoice_number TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'issued',
    credits BIGINT NOT NULL,
    amount_usd BIGINT NOT NULL,
    amount_local BIGINT NOT NULL,
    local_currency TEXT NOT NULL DEFAULT 'USD',
    tax_treatment TEXT NOT NULL DEFAULT 'none',
    rail TEXT NOT NULL,
    line_items JSONB NOT NULL DEFAULT '[]',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_payment_invoices_account ON payment_invoices(account_id, created_at DESC);
CREATE UNIQUE INDEX idx_payment_invoices_intent ON payment_invoices(payment_intent_id);
