CREATE TABLE account_budget_thresholds (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    threshold_credits BIGINT NOT NULL,
    last_notified_at TIMESTAMPTZ,
    alert_dismissed BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_budget_threshold_account UNIQUE (account_id)
);

CREATE INDEX idx_budget_thresholds_account ON account_budget_thresholds(account_id);
