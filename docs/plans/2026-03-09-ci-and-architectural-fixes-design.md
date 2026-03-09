# CI and Architectural Fixes Design

## Status
**Active** - Validated and ready for implementation planning. Designed to address CodeRabbit architectural reviews and CI failures from PR #36.

## Context
PR #36 received functional approval but surfaced several architectural concerns regarding database TOCTOU (Time-Of-Check to Time-Of-Use) races, as well as two CI failures (GitGuardian flagging Langfuse placeholder keys, and ESLint flagging unused variables).

## Goals
1. Fix all failing CI jobs on `chore/audit-session`.
2. Eliminate TOCTOU race conditions in API key revocation.
3. Eliminate TOCTOU race conditions and partial failure drift in payment intent claims and credit top-ups.
4. Eliminate TOCTOU race conditions and partial failure drift between the `credit_ledger` and `credit_accounts`.
5. Add missing test coverage flagged by review.

## Architecture & Approach

### 1. Database Atomicity (RPCs)
Supabase JS does not support interactive transactions. To ensure ACID compliance without distributed locking, we will shift critical transition logic into PostgreSQL RPCs.
- `claim_payment_intent(p_intent_id, p_provider, p_provider_txn_id)`: Atomically verifies the intent status, marks it as `credited`, inserts into `payment_events`, credits the `credit_accounts` table, and logs the top-up in `credit_ledger`. Returns the updated intent or an error status.
- `consume_credits(p_user_id, p_credits, p_reference_id)`: Atomically subtracts from `credit_accounts` and writes a debit to `credit_ledger`.

### 2. API Key Revocation
Refactor `supabase-api-key-store.ts` to revoke in a single query:
`UPDATE api_keys SET revoked = true, revoked_at = now() WHERE key_hash = ? AND user_id = ? AND revoked = false RETURNING *`

### 3. CI Failures
- **GitGuardian**: Change mock keys in `.env.example` to `your-langfuse-public-key-here` (or similar non-entropic strings).
- **ESLint**: Remove or prefix unused variables `hashPassword` and `verifyPassword` in `services.ts`.

### 4. Test Coverage
Add explicit tests covering the Supabase mappings for:
- `PersistentUsageService.add` and `.list`
- `PersistentUserService.me`, `.validateApiKey`, and `.revokeApiKey`
- `createRuntimeServices` dependency injection wiring

## Constraints & Trade-offs
- **Trade-off**: Shifting logic into PostgreSQL RPCs fragments business logic between TypeScript and SQL.
- **Mitigation**: We only use RPCs where strict ACID distributed isolation is required (billing/payments). All other logic remains in TypeScript.
