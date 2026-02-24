# Payments Reconciliation Runbook

## Goal
Detect and resolve mismatches between provider transactions and credited AI credits.

## Daily Procedure
1. Export provider transactions from bKash and SSLCOMMERZ dashboards.
2. Export internal payment intent and webhook event logs.
3. Match by `provider_txn_id` and `intent_id`.
4. Flag three mismatch classes:
   - provider success but no credits
   - credits issued without provider success
   - amount mismatch
5. Resolve manually with support and create adjustment ledger entries.

## Safeguards
- Never issue credits from browser redirect status.
- Process webhook events idempotently.
- Require server-side verification before credit minting.
