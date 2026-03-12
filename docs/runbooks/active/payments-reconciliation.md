# Payments Reconciliation Runbook

## Goal
Detect and resolve mismatches between provider transactions and credited AI credits.

## Automated Scan

Hive can run an opt-in payment reconciliation scheduler inside the API process.

Environment variables:

- `PAYMENT_RECONCILIATION_ENABLED` — enable the scheduler (`false` by default)
- `PAYMENT_RECONCILIATION_INTERVAL_MS` — run interval in milliseconds (default `3600000`)
- `PAYMENT_RECONCILIATION_LOOKBACK_HOURS` — recent reconciliation window in hours (default `24`)

Behavior:

- The scheduler scans recent payment intents, payment events, and payment credit-ledger entries.
- The scheduler expands the scan to all rows linked to affected `intent_id` values so lookback-boundary matches do not create false drift alerts.
- The job skips overlap inside one API process if a prior run is still in flight.
- The scheduler logs only when drift is found or when the reconciliation job fails. Clean intervals are intentionally silent.

## Daily Procedure
1. Check API logs for `payment reconciliation drift detected` or `payment reconciliation job failed`.
2. Export provider transactions from bKash and SSLCOMMERZ dashboards if drift was reported.
3. Export internal payment intent, webhook event, and payment credit-ledger rows for the affected `intent_id` values.
4. Match by `provider_txn_id`, `intent_id`, and payment ledger `reference_id`.
5. Classify reported drift:
   - verified provider success but payment intent not credited
   - credited intent without verified provider success
   - credited amount mismatch between `bdt_amount * 100`, `payment_intents.minted_credits`, and payment ledger credits
   - credited intent missing payment credit-ledger entry
6. Resolve manually with support and create adjustment ledger entries if needed.

## Safeguards
- Never issue credits from browser redirect status.
- Process webhook events idempotently.
- Require server-side verification before credit minting.
- Treat `payment_intents.status = credited` as insufficient evidence by itself; confirm matching verified event and payment ledger evidence.
