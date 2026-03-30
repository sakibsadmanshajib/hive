# Phase 3: Credits Ledger & Usage Accounting - Research

**Researched:** 2026-03-30
**Domain:** Workspace-level prepaid credits, immutable ledger entries, reservation/finalization flows, and privacy-safe usage accounting
**Confidence:** HIGH

## Summary

Phase 3 is the money-correctness phase. Phase 2 already established a real `apps/control-plane` service, workspace accounts, memberships, core profiles, billing profiles, Docker wiring, and Supabase-backed pgx repositories. That means Phase 3 should not invent a new service boundary. It should extend the existing control-plane/domain approach with ledger, reservation, and usage-accounting packages that attach to the current workspace account model.

The safest shape is:

1. Keep the authoritative balance model in Supabase Postgres as immutable credit ledger entries plus durable reservation records.
2. Treat the workspace account as the only spendable wallet, while preserving attribution dimensions for user, API key, service account, model alias, endpoint, and customer metadata tags.
3. Model billable work at the request-attempt level, not only by customer-visible request ID, so retries, resumed streams, and partial upstream execution remain reconcilable.
4. Use Redis only for hot-path idempotency, short-lived reservation coordination, and streaming attempt state. Redis must not become the source of truth for balances.
5. Store structured usage and billing metadata only. Do not persist prompts, completions, or transcript bodies at rest.

**Primary recommendation:** add Phase 3 as a control-plane-led accounting foundation made of new Supabase migrations, Go packages for `ledger`, `usage`, and `accounting` or `reservations`, plus Redis-backed idempotency and reservation helpers. Use append-only financial events, explicit reservation status transitions, and customer-favoring reconciliation defaults for ambiguous failures or interrupted streams.

<user_constraints>

## User Constraints (from CONTEXT.md)

### Locked Decisions

- The only real spendable balance is the workspace-level wallet.
- Users, teams, service accounts, and API keys may gain budgets or limits later, but they do not get true sub-wallets in Phase 3.
- The ledger and usage model must preserve attribution for future reporting and policy by user, team, service account, API key, model, and metadata dimensions.
- Dispatch authorization should reserve against a strong estimate plus safety margin instead of a worst-case ceiling or a fully loose settle-later posture.
- Workspace-specific limit enforcement must support strict blocking and a small temporary overage mode.
- The default workspace setting should be strict blocking when a more specific budget or limit fails.
- Streaming or long-running requests should start with an initial estimate and expand the reservation during execution when policy still allows.
- If Hive has no confirmed upstream usage, the reservation should be fully released or refunded.
- If usage is ambiguous after interruption or failure, settlement should default in the customer's favor unless a terminal provider usage record exists.
- Retries may become a new billable attempt once upstream execution has actually started.
- Broken or interrupted streams should stay reconcilable rather than assuming the full reserved amount was consumed.
- Durable usage events should store rich operational metadata rather than only minimal balance facts.
- Required attribution dimensions include workspace, user, team, API key or service account, model, endpoint, and customer-defined metadata tags.
- Customer-defined reporting metadata should be stored with limits and redaction rules rather than dropped entirely.
- Future reporting should support row-level usage events with token counts, Hive Credit cost, timestamp, request ID, and room for more non-transcript fields over time.
- Internal accounting records should preserve provider, provider cost, provider metadata, and error details for reconciliation and support, while keeping provider identity hidden from customer-facing reporting by default.

### Claude's Discretion

- Exact table layout for ledger entries, reservations, and usage events.
- Exact event envelope shape and redaction policy for internal versus customer-visible metadata.
- Exact service/package names and whether reservation/finalization logic lives in one package or several cooperating packages.
- Exact overage-policy field names, as long as strict-vs-overage behavior is preserved.

### Deferred Ideas (OUT OF SCOPE)

- True per-user, per-team, or per-service-account wallets.
- Full API-key lifecycle, budgets, and rate limits as customer-facing features.
- Payment rails, checkout, invoices, and FX-backed top-ups.
- Customer-facing console analytics and ledger-history UX.

</user_constraints>

<phase_requirements>

## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| BILL-01 | Customer has a prepaid Hive Credit balance backed by an immutable ledger of purchases, reservations, charges, refunds, and adjustments. | Use append-only ledger entries in Supabase Postgres, with derived balances and durable reservation state. Never use a mutable balance counter as the sole source of truth. |
| BILL-02 | Hive reserves credits before execution and finalizes or refunds usage accurately after success, failure, cancellation, retry, or interrupted stream completion. | Model request attempts explicitly, reserve before dispatch, expand only under policy, finalize from terminal usage, and keep ambiguous states reconcilable through reservation status plus reconciliation paths. |
| PRIV-01 | Customer can use the API without Hive storing prompt or response bodies at rest by default. | Usage events must store metadata, token counts, statuses, timings, IDs, and error details only. Any debugging or reporting design must avoid transcript retention. |

</phase_requirements>

## Standard Stack

### Core

| Technology | Version / Variant | Purpose | Why It Fits Phase 3 |
|------------|-------------------|---------|---------------------|
| Hosted Supabase Postgres | Existing project primary DB | Authoritative ledger, reservation, usage-event, and reconciliation state | Already locked in project constraints and already used by the control-plane via pgx. |
| Go | `1.24` in current repo modules | Accounting and control-plane business logic | Matches the repo today; Phase 3 should extend the current control-plane instead of introducing a new runtime. |
| `pgx/v5` | Existing dependency in `apps/control-plane` | Transactional repository access | Fits money-critical direct SQL flows better than generated REST layers. |
| Redis | `8.x` class from project research | Hot-path idempotency, reservation coordination, ephemeral streaming state | Matches the architecture research and keeps short-lived counters and locks off the primary ledger DB. |
| Docker Compose | Existing repo workflow | Local development and tests | Preserves the Docker-only workflow already used by the repo. |

### Supporting

| Library / Tool | Purpose | When to Use |
|----------------|---------|-------------|
| Standard `net/http` plus control-plane packages | Internal HTTP/admin surfaces if Phase 3 needs them | Reuse the existing control-plane router style instead of adding a heavy framework. |
| SQL migrations under `supabase/migrations/` | Ledger and usage schema evolution | Keep financial and privacy-critical schema changes explicit and reviewable. |
| `go test` with service/repository tests | Deterministic correctness testing | Use for ledger math, idempotency behavior, retry semantics, and redaction rules. |
| Redis-backed test containers or fakes | Reservation/idempotency verification | Use once reservation expansion and interrupted-stream flows are implemented. |

## Architecture Patterns

### Pattern 1: Workspace-Owned Immutable Credit Ledger

**What:** The workspace account created in Phase 2 remains the only spendable wallet. Financial events are append-only rows that describe grants, manual adjustments, reservation holds, releases, usage charges, refunds, and later payment postings.

**Why:** The current repo already resolves all business context around `account_id`. Reusing that model avoids a second billing ownership graph and keeps Phase 5 budgets and Phase 9 analytics aligned with the same account identity.

**Recommended data shape:**

- `credit_ledger_entries`
  - immutable row per financial event
  - keyed by `account_id`, event type, amount in Hive Credits, currency metadata, related reservation or attempt ID, and created timestamps
- `credit_reservations`
  - durable record of the open or settled reservation lifecycle
  - status examples: `pending`, `active`, `expanding`, `finalized`, `released`, `needs_reconciliation`
- optional balance view or repository query
  - computes available balance from settled ledger totals minus active reservations

**Important rule:** a mutable cached balance is allowed as a read optimization, but not as the only audit surface.

### Pattern 2: Request-Attempt Accounting, Not Only Request ID

**What:** Separate the customer request identity from the billable execution attempt identity. A retry that actually reached upstream execution may become its own attempt and its own reservation/finalization path.

**Why:** The phase context explicitly says retries may become a new billable attempt after upstream execution starts. A single `request_id` alone is not enough to reconstruct financial truth across retries, stream reconnects, or partial upstream execution.

**Recommended identifiers:**

- `request_id` for cross-system correlation
- `attempt_id` for each billable execution attempt
- `idempotency_key` scoped by operation type such as `reserve`, `finalize`, `refund`, or `adjust`
- `provider_usage_ref` or equivalent nullable field for late-arriving terminal usage

### Pattern 3: Privacy-Safe Usage Events with Dual Visibility

**What:** Persist durable usage events that contain metering, attribution, cost-allocation, and support metadata without storing prompts or completions.

**Why:** Phase 3 must let Hive reconstruct usage and billing facts while honoring the no-transcript-storage constraint from project requirements and context.

**Recommended event fields:**

- workspace and actor attribution: `account_id`, `user_id`, `api_key_id`, `service_account_id`, `team_id`
- request metadata: `request_id`, `attempt_id`, endpoint, model alias, customer metadata tags
- metering: input tokens, output tokens, cache tokens where available, total Hive Credits charged
- operational details: status, error code, latency buckets, started and ended timestamps
- internal-only support fields: provider mapping, provider cost, provider request ID, reconciler notes

**Do not store:** prompt text, completion text, raw uploaded body payloads, or full transcript chunks.

### Pattern 4: Redis-Assisted Hot Path, Postgres Authoritative Truth

**What:** Keep balance truth and reservation durability in Postgres, while using Redis for fast idempotency windows, reservation-expansion coordination, and short-lived stream or retry state.

**Why:** The global architecture research already recommends Redis for hot-path counters and reservations. The current repo does not yet have Redis wired into Docker Compose, so Phase 3 should add it intentionally rather than quietly depending on in-memory process state.

**Recommended split:**

- Postgres: authoritative ledger entries, reservations, usage events, reconciliation queue or outbox
- Redis: idempotency cache, optimistic reservation tokens, stream heartbeat or timeout markers
- Go services: policy evaluation and transactional orchestration

### Pattern 5: Reconciliation-Friendly Settlement

**What:** Finalization should support late or partial provider truth instead of assuming the reserve amount was fully consumed.

**Why:** The context explicitly requires customer-favoring settlement for ambiguous interrupted flows. That means reservation state must remain reopenable or reconcilable when usage is uncertain.

**Recommended lifecycle:**

1. Create reservation from estimated cost plus safety margin.
2. Dispatch upstream only if policy and available balance allow.
3. Record usage observations during execution without storing transcript bodies.
4. Finalize from terminal usage when available.
5. If usage is missing or ambiguous, release or partially refund and mark the attempt for reconciliation if later evidence can arrive.

## Common Pitfalls

### Pitfall 1: Storing only a mutable balance field

That makes support disputes, refunds, and reconciliation nearly impossible. The balance must be derivable from immutable events.

### Pitfall 2: Treating reservations as purely in-memory or Redis-only

Redis is appropriate for coordination, but not for the durable financial record. A process crash cannot erase the knowledge that credits were reserved or later released.

### Pitfall 3: Using request ID as the only financial identity

Retries, resumed streams, and duplicate client sends can all share a request context but differ financially. Attempt-level tracking is mandatory.

### Pitfall 4: Logging prompt or completion bodies "just for debugging"

That directly violates `PRIV-01` and the project privacy stance. Supportability has to come from metadata, IDs, token counts, and structured errors.

### Pitfall 5: Charging the full reserve on ambiguous interruptions

The context is explicit that ambiguous cases should favor the customer unless terminal upstream usage is known.

### Pitfall 6: Letting future budgets reshape the core wallet model

Per-key or per-team budgets should become policy overlays on top of the shared workspace wallet, not a reason to fork the balance model into sub-wallets.

## Open Questions

These are design questions to settle during planning, not blockers to begin planning:

1. Should reservation expansion be represented as multiple immutable hold entries, or one reservation row plus immutable adjustment events?
   - Recommendation: use a durable reservation row plus immutable ledger or reservation-event records for each expansion or release transition.
2. Should the first Phase 3 implementation expose internal HTTP endpoints for balance or ledger inspection, or keep the surface package-local until Phase 8 and Phase 9 need customer-facing APIs?
   - Recommendation: keep public/customer surfaces minimal and prioritize internal service correctness first.
3. What exact Redis contract should Phase 3 own before Phase 5 and Phase 6 consume it?
   - Recommendation: start with idempotency and reservation coordination only; rate limits and key-specific hot-path controls belong later.

## Validation Architecture

### Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | `go test` for control-plane domain, service, and repository packages |
| **Config file** | none beyond Go module and Docker Compose services |
| **Quick run command** | `docker compose -f deploy/docker/docker-compose.yml run --rm control-plane go test ./internal/ledger/... ./internal/usage/... ./internal/accounting/... -count=1` |
| **Full suite command** | `docker compose -f deploy/docker/docker-compose.yml config --services | grep -qx redis && docker compose -f deploy/docker/docker-compose.yml run --rm control-plane go test ./internal/accounts/... ./internal/profiles/... ./internal/ledger/... ./internal/usage/... ./internal/accounting/... -count=1` |
| **Estimated runtime** | ~90 seconds once Phase 3 packages exist |

### Phase Requirements to Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| BILL-01 | Immutable ledger entries produce correct posted and available balances for grants, holds, charges, releases, refunds, and adjustments | unit + repository | `docker compose -f deploy/docker/docker-compose.yml run --rm control-plane go test ./internal/ledger/... -run TestBalanceCalculation -count=1` | No -- Wave 0 |
| BILL-01 | Ledger mutations are idempotent under duplicate reserve/finalize calls | unit | `docker compose -f deploy/docker/docker-compose.yml run --rm control-plane go test ./internal/accounting/... -run TestIdempotentMutations -count=1` | No -- Wave 0 |
| BILL-02 | Retry and interrupted-stream attempts finalize, release, or reconcile according to terminal usage availability | unit | `docker compose -f deploy/docker/docker-compose.yml run --rm control-plane go test ./internal/accounting/... -run TestInterruptedStreamSettlement -count=1` | No -- Wave 0 |
| BILL-02 | Reservation expansion obeys strict-blocking versus temporary-overage policy | unit | `docker compose -f deploy/docker/docker-compose.yml run --rm control-plane go test ./internal/accounting/... -run TestReservationExpansionPolicy -count=1` | No -- Wave 0 |
| PRIV-01 | Usage events never persist prompt or completion bodies while retaining structured billing metadata | unit + repository | `docker compose -f deploy/docker/docker-compose.yml run --rm control-plane go test ./internal/usage/... -run TestUsageEventRedaction -count=1` | No -- Wave 0 |

### Sampling Rate

- **Per task commit:** run the quick suite for the touched Phase 3 packages
- **Per wave merge:** run the full suite including existing `accounts` and `profiles` packages to catch cross-package regressions
- **Before `$gsd-verify-work`:** full suite must be green
- **Max feedback latency:** 90 seconds

### Wave 0 Gaps

- [ ] `.env.example` — add Redis environment keys required by Phase 3 services
- [ ] `deploy/docker/docker-compose.yml` — add a `redis` service for Docker-only Phase 3 development and tests
- [ ] `apps/control-plane/internal/ledger/` — new package for immutable ledger entries and balance calculation
- [ ] `apps/control-plane/internal/usage/` — new package for privacy-safe usage events
- [ ] `apps/control-plane/internal/accounting/` or `internal/reservations/` — orchestration for reserve/finalize/refund flows
- [ ] `supabase/migrations/*credits*` — migration for ledger and reservation tables
- [ ] `supabase/migrations/*usage*` — migration for usage-event and reconciliation tables

### Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Support investigation can explain a failed or interrupted request without transcript storage | PRIV-01 | Requires judgment about whether metadata is operationally sufficient | Trigger a simulated failed attempt, inspect the stored usage and ledger artifacts, and confirm the event explains actor, model, endpoint, timing, error code, and cost outcome without any prompt or completion body. |
| Customer-favoring settlement is understandable in a disputed interrupted-stream scenario | BILL-02 | Requires scenario review rather than only assertions | Run a simulated interrupted stream with no terminal provider usage, inspect the reservation and ledger records, and confirm the outcome is a release or limited charge plus a reconciliation marker rather than a full reserve debit. |

## Sources

### Primary (HIGH confidence)

- `.planning/phases/03-credits-ledger-usage-accounting/03-CONTEXT.md`
- `.planning/ROADMAP.md`
- `.planning/REQUIREMENTS.md`
- `.planning/PROJECT.md`
- `.planning/STATE.md`
- `.planning/research/SUMMARY.md`
- `.planning/research/ARCHITECTURE.md`
- `.planning/research/STACK.md`
- `.planning/research/PITFALLS.md`
- `.planning/research/FEATURES.md`
- `apps/control-plane/internal/accounts/service.go`
- `apps/control-plane/internal/accounts/repository.go`
- `apps/control-plane/internal/profiles/repository.go`
- `apps/control-plane/internal/platform/http/router.go`
- `deploy/docker/docker-compose.yml`

### Secondary (MEDIUM confidence)

- Existing Phase 1 and Phase 2 planning artifacts that establish repo conventions for packages, tests, and validation structure
- Existing `apps/edge-api` server shape, which shows that request-execution and hot-path billing integration have not yet been implemented

## Metadata

**Confidence breakdown:**
- Ledger architecture: HIGH
- Reservation/finalization model: HIGH
- Privacy-safe usage design: HIGH
- Validation plan: MEDIUM-HIGH

**Research date:** 2026-03-30
**Valid until:** 2026-04-30

---
*Research completed: 2026-03-30*
*Ready for planning: yes*
