## Goal

Implement issue `#7` by adding an API-side payment reconciliation service, an opt-in in-process scheduler, and drift-alert logging for recent billing mismatches, then document the operator workflow and verify the billing hardening behavior with tests and build checks.

## Assumptions

- The preferred plan-writer helper at `.agent/skills/superpowers-workflow/scripts/write_artifact.py` is unavailable in this repository, so this plan is written directly to `docs/plans/`.
- The current Supabase billing tables and `SupabaseBillingStore` remain the source of truth for payment intents, payment events, and credited ledger state.
- First-pass alerting will use structured API logs rather than external notification integrations.
- The initial scheduler only needs single-process overlap protection; distributed multi-instance coordination is out of scope for issue `#7`.

## Plan

### Step 1

**Files:** `apps/api/src/runtime/supabase-billing-store.ts`, `apps/api/src/runtime/services.ts`, `apps/api/src/config/env.ts`, `apps/api/test/domain/payment-service.test.ts`, `apps/api/test/domain/runtime-services.test.ts`, `docs/runbooks/active/payments-reconciliation.md`

**Change:** Re-read the existing billing store, runtime wiring, env configuration, payment tests, and reconciliation runbook to confirm the exact store/query seams, conversion formula, and operator expectations that the new reconciliation workflow must preserve.

**Verify:** `sed -n '1,320p' apps/api/src/runtime/supabase-billing-store.ts && sed -n '1,220p' apps/api/src/runtime/services.ts && sed -n '1,240p' docs/runbooks/active/payments-reconciliation.md`

### Step 2

**Files:** `apps/api/test/domain/payment-reconciliation.test.ts`

**Change:** Add failing tests for the reconciliation classifier covering the three initial drift classes: verified event without credited intent, credited intent without verified event, and credited intent with mismatched minted credits.

**Verify:** `pnpm --filter @hive/api exec vitest run test/domain/payment-reconciliation.test.ts`

### Step 3

**Files:** `apps/api/src/domain/types.ts`, `apps/api/src/runtime/supabase-billing-store.ts`, `apps/api/src/runtime/payment-reconciliation.ts`

**Change:** Add the reconciliation result and finding types plus a reconciliation module that reads recent payment reconciliation data from the billing store and returns a structured summary and detailed findings.

**Verify:** `pnpm --filter @hive/api exec vitest run test/domain/payment-reconciliation.test.ts`

### Step 4

**Files:** `apps/api/test/domain/payment-reconciliation.test.ts`, `apps/api/src/runtime/supabase-billing-store.ts`, `apps/api/src/runtime/payment-reconciliation.ts`

**Change:** Expand the billing store with a recent reconciliation-read method that returns the minimum intent/event fields needed for drift classification, then make the tests pass with the minimal implementation.

**Verify:** `pnpm --filter @hive/api exec vitest run test/domain/payment-reconciliation.test.ts`

### Step 5

**Files:** `apps/api/test/domain/payment-reconciliation-scheduler.test.ts`, `apps/api/test/domain/runtime-services.test.ts`

**Change:** Add failing tests for scheduler behavior, including disabled-by-default wiring, enabled interval execution, and overlap prevention when a prior reconciliation run is still in flight.

**Verify:** `pnpm --filter @hive/api exec vitest run test/domain/payment-reconciliation-scheduler.test.ts test/domain/runtime-services.test.ts`

### Step 6

**Files:** `apps/api/src/config/env.ts`, `apps/api/src/runtime/payment-reconciliation-scheduler.ts`, `apps/api/src/runtime/services.ts`

**Change:** Add opt-in env settings for reconciliation scheduling and lookback windows, implement a small scheduler wrapper around the reconciliation service, and wire it into runtime service creation so it starts only when enabled.

**Verify:** `pnpm --filter @hive/api exec vitest run test/domain/payment-reconciliation-scheduler.test.ts test/domain/runtime-services.test.ts`

### Step 7

**Files:** `apps/api/test/domain/payment-reconciliation-scheduler.test.ts`, `apps/api/src/runtime/payment-reconciliation-scheduler.ts`

**Change:** Implement structured drift and failure logging semantics in the scheduler and confirm the scheduler does not emit noisy success logs on every clean interval.

**Verify:** `pnpm --filter @hive/api exec vitest run test/domain/payment-reconciliation-scheduler.test.ts`

### Step 8

**Files:** `docs/runbooks/active/payments-reconciliation.md`, `docs/runbooks/active/credit-ledger-audit.md`, `README.md`, `docs/README.md`, `CHANGELOG.md`, `AGENTS.md`

**Change:** Document the automated reconciliation scheduler, new env flags, drift classes, alert behavior, and remaining manual resolution workflow; update AGENTS only if implementation/verification exposes a durable repo-specific lesson worth persisting.

**Verify:** `rg -n "reconciliation|drift|alert|PAYMENT_RECONCILIATION|scheduler" docs/runbooks/active/payments-reconciliation.md docs/runbooks/active/credit-ledger-audit.md README.md docs/README.md CHANGELOG.md AGENTS.md`

### Step 9

**Files:** `apps/api/src/config/env.ts`, `apps/api/src/domain/types.ts`, `apps/api/src/runtime/payment-reconciliation.ts`, `apps/api/src/runtime/payment-reconciliation-scheduler.ts`, `apps/api/src/runtime/services.ts`, `apps/api/src/runtime/supabase-billing-store.ts`, `apps/api/test/domain/payment-reconciliation.test.ts`, `apps/api/test/domain/payment-reconciliation-scheduler.test.ts`, `apps/api/test/domain/runtime-services.test.ts`, `docs/runbooks/active/payments-reconciliation.md`, `docs/runbooks/active/credit-ledger-audit.md`, `README.md`, `docs/README.md`, `CHANGELOG.md`, `AGENTS.md`

**Change:** Run final verification for the API billing-hardening change and confirm the exact commands that support the completion claim.

**Verify:** `pnpm --filter @hive/api test && pnpm --filter @hive/api build`

## Risks & mitigations

- Risk: reconciliation queries infer credited state incorrectly from incomplete billing data.
  Mitigation: keep the first implementation limited to fields already persisted in `payment_intents`, `payment_events`, and payment-credit ledger entries, and test each drift class explicitly.
- Risk: scheduler overlap or repeated intervals produce duplicate noisy alerts.
  Mitigation: use a process-local in-flight guard and log only drift/failure conditions.
- Risk: enabling the scheduler in future multi-instance deployments causes duplicate scans.
  Mitigation: document that the first pass is single-instance oriented and keep the reconciliation core separable so a leased/distributed scheduler can replace the wrapper later.
- Risk: the repo’s current billing-store abstraction lacks read APIs needed for reconciliation.
  Mitigation: add the smallest read-only store method necessary for recent reconciliation snapshots instead of broadening unrelated interfaces.

## Rollback plan

- Revert the reconciliation service, scheduler wiring, env configuration, tests, and documentation together if the design needs rework.
- If only the scheduler behavior is problematic, disable it with env configuration while retaining the reconciliation core and tests.
- Because this change only adds detection and logging, rollback does not require data migration or ledger mutation reversal.
