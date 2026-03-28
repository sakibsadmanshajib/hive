# Credit Ledger Audit Runbook

## Goal
Prove every credit mint, debit, and refund is traceable.

See also: [Real OpenAI SDK Local Verification](/home/sakib/hive/docs/runbooks/active/openai-real-sdk-local-verification.md)

## Audit Invariants
- Purchased credit mint must map to a verified payment intent.
- Purchased credit mint must also map to a `credit_ledger` entry with `reference_type = payment` and `reference_id = intent_id`.
- Purchased credit mint conversion must preserve 2-decimal payment amounts exactly at the `1 BDT = 100 credits` rate.
- Every debit must map to a request id.
- Refunded credits must be unused purchased credits inside 30-day window.
- Promo credits are never cash-refundable.

## Weekly Checks
1. Sample 50 usage events and verify request id lineage.
2. Sample 20 refunds and verify policy formula:
   - `refund_bdt = (credits / 100) * 0.9`
3. Confirm no negative available balances.
4. Confirm idempotency for duplicate webhooks.
5. If the payment reconciliation scheduler is enabled, review drift logs and confirm each finding was resolved or triaged.

## Recent Reference

- The 2026-03-22 local real-SDK verification produced one credited payment row for `5000` credits and two `8`-credit usage debits tied to the same Hive API key id, with matching `/v1/usage` aggregates and `credit_ledger` rows.
