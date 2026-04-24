---
phase: 4
slug: model-catalog-provider-routing
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-03-31
---

# Phase 4 — Validation Strategy

> Draft Nyquist validation contract derived from `04-RESEARCH.md` before plan execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | `go test` for `apps/edge-api` and `apps/control-plane`, plus existing JS SDK model-list regression |
| **Config file** | `deploy/docker/docker-compose.yml` |
| **Quick run command** | `docker compose -f deploy/docker/docker-compose.yml run --rm control-plane go test ./... -count=1` |
| **Full suite command** | `docker compose -f deploy/docker/docker-compose.yml run --rm control-plane go test ./... -count=1 && docker compose -f deploy/docker/docker-compose.yml run --rm sdk-tests-js pnpm test -- --run tests/models/list-models.test.ts` |
| **Estimated runtime** | ~120 seconds once Phase 4 packages and fixtures exist |

---

## Sampling Rate

- **After every task commit:** Run the most specific touched catalog, routing, or usage package test.
- **After every plan wave:** Run the control-plane suite plus the JS SDK `models.list()` regression.
- **Before `$gsd-verify-work`:** Full suite must be green.
- **Max feedback latency:** 120 seconds.

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 04-01-01 | 01 | 1 | ROUT-01 | edge unit + SDK regression | `docker compose -f deploy/docker/docker-compose.yml run --rm sdk-tests-js pnpm test -- --run tests/models/list-models.test.ts` | ✅ partial | ⬜ pending |
| 04-01-02 | 01 | 1 | ROUT-01 | control-plane or edge unit | `docker compose -f deploy/docker/docker-compose.yml run --rm control-plane go test ./... -run TestCatalogProjection -count=1` | ❌ W0 | ⬜ pending |
| 04-02-01 | 02 | 2 | ROUT-02 | service unit | `docker compose -f deploy/docker/docker-compose.yml run --rm control-plane go test ./... -run TestSelectEligibleRoute -count=1` | ❌ W0 | ⬜ pending |
| 04-02-02 | 02 | 2 | ROUT-02 | service unit | `docker compose -f deploy/docker/docker-compose.yml run --rm control-plane go test ./... -run TestFallbackPolicyWidening -count=1` | ❌ W0 | ⬜ pending |
| 04-03-01 | 03 | 3 | ROUT-03 | usage/accounting unit | `docker compose -f deploy/docker/docker-compose.yml run --rm control-plane go test ./... -run TestNormalizeCacheUsage -count=1` | ❌ W0 | ⬜ pending |
| 04-03-02 | 03 | 3 | ROUT-03 | usage/http + edge error tests | `docker compose -f deploy/docker/docker-compose.yml run --rm control-plane go test ./... -run TestProviderBlindUsageResponse -count=1` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `supabase/migrations/*catalog*` — add alias catalog, route candidate, routing policy, and provider capability schema.
- [ ] `apps/control-plane/internal/...` — create catalog and routing packages plus their tests.
- [ ] `apps/edge-api/...` — replace the empty `/v1/models` implementation with alias-backed projection logic and tests.
- [ ] cross-app projection/client layer — add the control-plane-to-edge contract for catalog data.
- [ ] LiteLLM config or adapter wiring in Docker/dev config — add the internal routing target used by Phase 4 and later phases.
- [ ] provider-blindness regression tests — add catalog, usage, and error tests that explicitly reject upstream vendor names and raw provider model IDs.
- [ ] cache normalization tests — add fixtures and tests for read/write cache token mapping.

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Public aliases remain understandable without revealing providers | ROUT-01 | Requires product judgment about naming clarity and catalog usefulness | Review `/v1/models` plus the richer catalog output and confirm a developer can choose a model by alias, price, and capability badges without vendor exposure. |
| Fallback preserves the advertised alias contract | ROUT-02 | Requires scenario review across multiple route candidates | Force a primary-route failure, inspect the internal fallback chain, and confirm the chosen fallback remains inside the alias's allowed behavior and price boundaries. |
| Cache-aware billing is understandable in customer-facing output | ROUT-03 | Requires UX/content review beyond raw field assertions | Review a cache-capable alias in catalog and usage output and confirm read/write token categories explain billing changes without provider terminology. |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies.
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify.
- [ ] Wave 0 covers all missing validation references.
- [ ] No watch-mode flags.
- [ ] Feedback latency < 120s.
- [ ] `nyquist_compliant: true` set in frontmatter.

**Approval:** pending
