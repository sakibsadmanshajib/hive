---
phase: 3
slug: credits-ledger-usage-accounting
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-03-30
---

# Phase 3 — Validation Strategy

> Draft Nyquist validation contract derived from `03-RESEARCH.md` before plan execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | `go test` for control-plane domain, service, and repository packages |
| **Config file** | none beyond Go module and Docker Compose services |
| **Quick run command** | `docker compose -f deploy/docker/docker-compose.yml run --rm control-plane go test ./internal/ledger/... ./internal/usage/... ./internal/accounting/... -count=1` |
| **Full suite command** | `docker compose -f deploy/docker/docker-compose.yml config --services | grep -qx redis && docker compose -f deploy/docker/docker-compose.yml run --rm control-plane go test ./internal/accounts/... ./internal/profiles/... ./internal/ledger/... ./internal/usage/... ./internal/accounting/... -count=1` |
| **Estimated runtime** | ~90 seconds once Phase 3 packages exist |

---

## Sampling Rate

- **After every task commit:** Run the quick Phase 3 package suite.
- **After every plan wave:** Run the full suite including existing `accounts` and `profiles` packages.
- **Before `$gsd-verify-work`:** Full suite must be green.
- **Max feedback latency:** 90 seconds.

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 03-01-01 | 01 | 1 | BILL-01 | unit + repository | `docker compose -f deploy/docker/docker-compose.yml run --rm control-plane go test ./internal/ledger/... -run TestBalanceCalculation -count=1` | ❌ W0 | ⬜ pending |
| 03-01-02 | 01 | 1 | BILL-01 | unit | `docker compose -f deploy/docker/docker-compose.yml run --rm control-plane go test ./internal/accounting/... -run TestIdempotentMutations -count=1` | ❌ W0 | ⬜ pending |
| 03-02-01 | 02 | 2 | PRIV-01 | unit + repository | `docker compose -f deploy/docker/docker-compose.yml run --rm control-plane go test ./internal/usage/... -run TestUsageEventRedaction -count=1` | ❌ W0 | ⬜ pending |
| 03-02-02 | 02 | 2 | BILL-01, BILL-02 | unit | `docker compose -f deploy/docker/docker-compose.yml run --rm control-plane go test ./internal/usage/... -run TestUsageEventAttribution -count=1` | ❌ W0 | ⬜ pending |
| 03-03-01 | 03 | 3 | BILL-02 | unit | `docker compose -f deploy/docker/docker-compose.yml run --rm control-plane go test ./internal/accounting/... -run TestInterruptedStreamSettlement -count=1` | ❌ W0 | ⬜ pending |
| 03-03-02 | 03 | 3 | BILL-02 | unit | `docker compose -f deploy/docker/docker-compose.yml run --rm control-plane go test ./internal/accounting/... -run TestReservationExpansionPolicy -count=1` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `.env.example` — add Redis environment keys required by Phase 3 services.
- [ ] `deploy/docker/docker-compose.yml` — add a `redis` service for Docker-only Phase 3 development and tests.
- [ ] `apps/control-plane/internal/ledger/` — create the immutable ledger package and its tests.
- [ ] `apps/control-plane/internal/usage/` — create the privacy-safe usage package and its tests.
- [ ] `apps/control-plane/internal/accounting/` or `internal/reservations/` — create the reserve/finalize/refund orchestration package and its tests.
- [ ] `supabase/migrations/*credits*` — add the ledger and reservation schema migration.
- [ ] `supabase/migrations/*usage*` — add the usage-event and reconciliation schema migration.

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Support investigation can explain a failed or interrupted request without transcript storage | PRIV-01 | Requires judgment about operational sufficiency, not only field presence | Trigger a simulated failed attempt, inspect the stored usage and ledger artifacts, and confirm the record explains actor, model, endpoint, timing, error code, and cost outcome without any prompt or completion body. |
| Customer-favoring settlement is understandable in a disputed interrupted-stream scenario | BILL-02 | Requires scenario review rather than only assertions | Run a simulated interrupted stream with no terminal provider usage, inspect the reservation and ledger records, and confirm the outcome is a release or limited charge plus a reconciliation marker rather than a full reserve debit. |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies.
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify.
- [ ] Wave 0 covers all missing validation references.
- [ ] No watch-mode flags.
- [ ] Feedback latency < 90s.
- [ ] `nyquist_compliant: true` set in frontmatter.

**Approval:** pending
