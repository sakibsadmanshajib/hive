# Hive Requirement Matrix (active)

**Created:** 2026-04-25 (Phase 11).
**Supersedes:** archived `.planning/milestones/v1.0-REQUIREMENTS.md` for **live** status.
The archive remains the v1.0 ship-gate snapshot (frozen 2026-04-21).

This file is the active source of truth for v1.0 + v1.1 requirement status. Each
row's `Evidence` column either resolves to an on-disk evidence file under
`.planning/phases/.../evidence/` (Satisfied / Partial) or names the planned
phase target (Pending). The validator
`scripts/verify-requirements-matrix.sh` enforces that every link of the first
form points at an existing file with required frontmatter.

---

## v1.0 Requirements (shipped 2026-04-21)

### Compatibility & Contract

| ID | Phase | Status | Evidence |
|----|-------|--------|----------|
| COMP-01 | 01 | Satisfied | Phase 01 (archive — pre-Phase-11 evidence in `milestones/v1.0-REQUIREMENTS.md`) |
| COMP-02 | 01 | Satisfied | Phase 01 (archive — pre-Phase-11 evidence in `milestones/v1.0-REQUIREMENTS.md`) |
| COMP-03 | 01 | Satisfied | Phase 01 (archive — pre-Phase-11 evidence in `milestones/v1.0-REQUIREMENTS.md`) |

### Inference Surface

| ID | Phase | Status | Evidence |
|----|-------|--------|----------|
| API-01 | 06 | Satisfied | [evidence/API-01.md](phases/11-verification-cleanup/evidence/API-01.md) |
| API-02 | 06 | Satisfied | [evidence/API-02.md](phases/11-verification-cleanup/evidence/API-02.md) |
| API-03 | 06 | Satisfied | [evidence/API-03.md](phases/11-verification-cleanup/evidence/API-03.md) |
| API-04 | 06 | Satisfied | [evidence/API-04.md](phases/11-verification-cleanup/evidence/API-04.md) |
| API-05 | 10 | Satisfied | Phase 10 (archive — `phases/10-routing-storage-critical-fixes/10-UAT.md` Test 7) |
| API-06 | 10 | Satisfied | Phase 10 (archive — `phases/10-routing-storage-critical-fixes/10-UAT.md` Test 8) |
| API-07 | 10 | Partial | Phase 10 (archive — `phases/10-routing-storage-critical-fixes/KNOWN-ISSUE-batch-upstream.md`); success-path Phase 12 (planned) |
| API-08 | 01 | Satisfied | Phase 01 (archive) |

### Model Catalog & Routing

| ID | Phase | Status | Evidence |
|----|-------|--------|----------|
| ROUT-01 | 04 | Satisfied | Phase 04 (archive) |
| ROUT-02 | 10 | Satisfied | Phase 10 (archive — `phases/10-routing-storage-critical-fixes/10-VERIFICATION.md`) |
| ROUT-03 | 04 | Satisfied | Phase 04 (archive) |

### API Keys & Attribution (v1.0 subset)

| ID | Phase | Status | Evidence |
|----|-------|--------|----------|
| KEY-04 | 10 | Partial | Phase 10 (archive — edge-level reservation attribution verified; success-path attribution unexercisable until API-07 success-path lands) |

### Authentication & Accounts (Phase 02 — recovered v1.0 satisfied)

The archived v1.0 matrix listed AUTH-01 / AUTH-02 as "Pending — Deferred v1.1".
Audit on 2026-04-25 (Phase 11 Task 1) confirmed Phase 02 shipped the underlying
code paths (Supabase auth migrations, web-console `/auth/{sign-up,sign-in,forgot-password,reset-password,callback}`
routes, `middleware.ts` session gate, control-plane account/membership
provisioning). Status corrected to **Satisfied** with evidence files below.
AUTH-03 + AUTH-04 remain Pending and route to a future phase — out of scope for
Phase 11.

| ID | Phase | Status | Evidence |
|----|-------|--------|----------|
| AUTH-01 | 02 | Satisfied | [evidence/AUTH-01.md](phases/11-verification-cleanup/evidence/AUTH-01.md) |
| AUTH-02 | 02 | Satisfied | [evidence/AUTH-02.md](phases/11-verification-cleanup/evidence/AUTH-02.md) |
| AUTH-03 | TBD | Pending | Phase TBD (planned) |
| AUTH-04 | TBD | Pending | Phase TBD (planned) |

---

## v1.1 Requirements — Deferred from v1.0

These were scoped to v1.0 originally but reassigned to v1.1 phases. Status
remains **Pending** until the target phase produces an evidence file.

### Billing & Payments

| ID | Phase | Status | Evidence |
|----|-------|--------|----------|
| BILL-01 | 11 | Pending | Phase 11 (planned — formal verification artifact deferred to ship-gate audit) |
| BILL-02 | 11 | Pending | Phase 11 (planned — formal verification artifact deferred to ship-gate audit) |
| BILL-03 | 13 | Pending | Phase 13 (planned) |
| BILL-04 | 11 | Pending | Phase 11 (planned — math shipped Phase 08; formal artifact deferred) |
| BILL-05 | 14 | Pending | Phase 14 (planned) |
| BILL-06 | 14 | Pending | Phase 14 (planned) |
| BILL-07 | 13 | Pending | Phase 13 (planned) |

### API Keys & Rate Limits

| ID | Phase | Status | Evidence |
|----|-------|--------|----------|
| KEY-01 | 13 | Pending | Phase 13 (planned) |
| KEY-02 | 12 | Pending | Phase 12 (planned) |
| KEY-03 | 13 | Pending | Phase 13 (planned) |
| KEY-05-01 | 12 | Satisfied | [12-VERIFICATION.md](phases/12-key05-rate-limiting/12-VERIFICATION.md) — RPM bucket per-key + tier scope wired in `apps/edge-api/internal/authz/ratelimit.go` (`CheckWithTier`) |
| KEY-05-02 | 12 | Satisfied | [12-VERIFICATION.md](phases/12-key05-rate-limiting/12-VERIFICATION.md) — TPM bucket per-key + tier scope wired in `apps/edge-api/internal/authz/ratelimit.go` (`CheckWithTier`) |
| KEY-05-03 | 12 | Satisfied | [12-VERIFICATION.md](phases/12-key05-rate-limiting/12-VERIFICATION.md) — `X-RateLimit-Limit/Remaining/Reset` emitted by `apps/edge-api/internal/authz/authorizer.go` `rateLimitHeaders` |
| KEY-05-04 | 12 | Satisfied | [12-VERIFICATION.md](phases/12-key05-rate-limiting/12-VERIFICATION.md) — 429 + `Retry-After` emitted by existing authorizer rejection path |
| KEY-05-05 | 12 | Satisfied | [12-VERIFICATION.md](phases/12-key05-rate-limiting/12-VERIFICATION.md) — `TierResolver` in `apps/edge-api/internal/authz/tier.go` reads JWT claim `hive_tier` w/ env defaults; Phase 20 swap point preserved |
| KEY-05-06 | 12 | Partial | [12-VERIFICATION.md](phases/12-key05-rate-limiting/12-VERIFICATION.md) — Prometheus alert `deploy/prometheus/alerts/rate-limit.yml` validated by `promtool check rules`. Counter `rate_limit_exceeded_total` emission deferred to follow-up commit before Phase 13; rules are inert until then. |
| KEY-05-07 | 12 | Satisfied | [12-VERIFICATION.md](phases/12-key05-rate-limiting/12-VERIFICATION.md) — owner-gated `/console/api-keys/[id]/limits` page + `RateLimitForm` w/ vitest unit tests |

### Developer Console

| ID | Phase | Status | Evidence |
|----|-------|--------|----------|
| CONS-01 | 13 | Pending | Phase 13 (planned) |
| CONS-02 | 13 | Pending | Phase 13 (planned) |
| CONS-03 | 11 | Pending | Phase 11 (planned — chart UIs shipped Phase 09; live-data verification deferred) |

### Console Integration (Phase 13)

| ID | Phase | Status | Evidence |
|----|-------|--------|----------|
| CONSOLE-13-01 | 13 | Satisfied | [evidence/CONSOLE-13-01.md](phases/13-console-integration-fixes/evidence/CONSOLE-13-01.md) — every console route reachable; 18/21 Green, 2 Phase-14-deferred (fixture-seed flake), 1 Broken-P0 fixed inline |
| CONSOLE-13-02 | 13 | Satisfied | [evidence/CONSOLE-13-02.md](phases/13-console-integration-fixes/evidence/CONSOLE-13-02.md) — `apps/web-console/lib/control-plane/types.ts` re-export shim over canonical `client.ts` interface set |
| CONSOLE-13-03 | 13 | Satisfied | [evidence/CONSOLE-13-03.md](phases/13-console-integration-fixes/evidence/CONSOLE-13-03.md) — strict-TS clean: `tsc --noEmit` exit 0, zero `as any`/`as unknown`/`<any>`/`<unknown>` matches |
| CONSOLE-13-04 | 13 | Satisfied | [evidence/CONSOLE-13-04.md](phases/13-console-integration-fixes/evidence/CONSOLE-13-04.md) — zero customer-surface FX/USD leak in `apps/web-console/{app,components,lib}` (PHASE-17-OWNER-ONLY annotated remnants only) |
| CONSOLE-13-05 | 13 | Satisfied | [evidence/CONSOLE-13-05.md](phases/13-console-integration-fixes/evidence/CONSOLE-13-05.md) — viewer-gates honoured on owner-only routes; vitest 18 tests cover owner / non-owner role matrix |
| CONSOLE-13-06 | 13 | Satisfied | [evidence/CONSOLE-13-06.md](phases/13-console-integration-fixes/evidence/CONSOLE-13-06.md) — auth flows (sign-in/up/forgot/reset/out/callback) green via `__tests__/auth-routes.test.ts` (12 tests) + `tests/e2e/unauth.spec.ts` (5 tests) |
| CONSOLE-13-07 | 13 | Partial | [evidence/CONSOLE-13-07.md](phases/13-console-integration-fixes/evidence/CONSOLE-13-07.md) — workspace switch + invitation accept code paths exercised; workspace-switcher E2E spec fails on pre-existing fixture-seed race (HANDOFF-13-01 → Phase 14) |
| CONSOLE-13-08 | 13 | Satisfied | [evidence/CONSOLE-13-08.md](phases/13-console-integration-fixes/evidence/CONSOLE-13-08.md) — Playwright spec coverage map + 2 new specs (console-billing BDT-only, console-fx-guard whole-console) |
| CONSOLE-13-09 | 13 | Satisfied | [evidence/CONSOLE-13-09.md](phases/13-console-integration-fixes/evidence/CONSOLE-13-09.md) — `tsc --noEmit` + `npm run build` + `npm run test:unit` all exit 0 |
| CONSOLE-13-10 | 13 | Satisfied | [evidence/CONSOLE-13-10.md](phases/13-console-integration-fixes/evidence/CONSOLE-13-10.md) — 6 hand-offs filed (HANDOFF-13-01..06) targeting Phases 14, 17, 18 |

### Privacy & Operations

| ID | Phase | Status | Evidence |
|----|-------|--------|----------|
| PRIV-01 | 11 | Pending | Phase 11 (planned — policy enforced in code; formal VERIFICATION.md deferred) |
| OPS-01 | 11 | Pending | Phase 11 (planned — Prometheus/Grafana/Alertmanager shipped Phase 09; live-stack verification deferred) |

---

## v1.1 Requirements (in flight)

### Routing & Catalog

| ID | Phase | Status | Evidence |
|----|-------|--------|----------|
| CAP-16-01 | 16 | Satisfied | [evidence/CAP-16-01.md](phases/16-capability-columns-fix/evidence/CAP-16-01.md) |

### Payments / Budget / Grant (Phase 14)

| ID | Phase | Status | Evidence |
|----|-------|--------|----------|
| PAY-14-01 | 14 | Satisfied | [evidence/PAY-14-01.md](phases/14-payments-budget-grant/evidence/PAY-14-01.md) |
| PAY-14-02 | 14 | Satisfied | [evidence/PAY-14-02.md](phases/14-payments-budget-grant/evidence/PAY-14-02.md) |
| PAY-14-03 | 14 | Satisfied | [evidence/PAY-14-03.md](phases/14-payments-budget-grant/evidence/PAY-14-03.md) |
| PAY-14-04 | 14 | Satisfied | [evidence/PAY-14-04.md](phases/14-payments-budget-grant/evidence/PAY-14-04.md) |
| PAY-14-05 | 14 | Satisfied | [evidence/PAY-14-05.md](phases/14-payments-budget-grant/evidence/PAY-14-05.md) |
| PAY-14-06 | 14 | Satisfied | [evidence/PAY-14-06.md](phases/14-payments-budget-grant/evidence/PAY-14-06.md) |
| PAY-14-07 | 14 | Satisfied | [evidence/PAY-14-07.md](phases/14-payments-budget-grant/evidence/PAY-14-07.md) |
| PAY-14-08 | 14 | Satisfied | [evidence/PAY-14-08.md](phases/14-payments-budget-grant/evidence/PAY-14-08.md) |
| PAY-14-09 | 14 | Satisfied | [evidence/PAY-14-09.md](phases/14-payments-budget-grant/evidence/PAY-14-09.md) |
| PAY-14-10 | 14 | Satisfied | [evidence/PAY-14-10.md](phases/14-payments-budget-grant/evidence/PAY-14-10.md) |
| PAY-14-11 | 14 | Satisfied | [evidence/PAY-14-11.md](phases/14-payments-budget-grant/evidence/PAY-14-11.md) |
| PAY-14-12 | 14 | Satisfied | [evidence/PAY-14-12.md](phases/14-payments-budget-grant/evidence/PAY-14-12.md) |

### RBAC Matrix (Phase 18)

| ID | Phase | Status | Evidence |
|----|-------|--------|----------|
| RBAC-18-01 | 18 | Satisfied | [evidence/RBAC-18-01.md](phases/18-rbac-matrix/evidence/RBAC-18-01.md) |
| RBAC-18-02 | 18 | Satisfied | [evidence/RBAC-18-02.md](phases/18-rbac-matrix/evidence/RBAC-18-02.md) |
| RBAC-18-03 | 18 | Satisfied | [evidence/RBAC-18-03.md](phases/18-rbac-matrix/evidence/RBAC-18-03.md) |
| RBAC-18-04 | 18 | Satisfied | [evidence/RBAC-18-04.md](phases/18-rbac-matrix/evidence/RBAC-18-04.md) |
| RBAC-18-05 | 18 | Satisfied | [evidence/RBAC-18-05.md](phases/18-rbac-matrix/evidence/RBAC-18-05.md) |
| RBAC-18-06 | 18 | Satisfied | [evidence/RBAC-18-06.md](phases/18-rbac-matrix/evidence/RBAC-18-06.md) |
| RBAC-18-07 | 18 | Satisfied | [evidence/RBAC-18-07.md](phases/18-rbac-matrix/evidence/RBAC-18-07.md) |
| RBAC-18-08 | 18 | Satisfied | [evidence/RBAC-18-08.md](phases/18-rbac-matrix/evidence/RBAC-18-08.md) |
| RBAC-18-09 | 18 | Satisfied | [evidence/RBAC-18-09.md](phases/18-rbac-matrix/evidence/RBAC-18-09.md) |
| RBAC-18-10 | 18 | Satisfied | [evidence/RBAC-18-10.md](phases/18-rbac-matrix/evidence/RBAC-18-10.md) |
| RBAC-18-11 | 18 | Satisfied | [evidence/RBAC-18-11.md](phases/18-rbac-matrix/evidence/RBAC-18-11.md) |

### Web Search Tool (Phase 26)

| ID | Phase | Status | Evidence |
|----|-------|--------|----------|
| SEARCH-26-01 | 26 | Pending | Phase 26 (planned) — self-hosted SearXNG available only on the internal network |
| SEARCH-26-02 | 26 | Pending | Phase 26 (planned) — `POST /v1/tools/web_search` validates input and normalises results |
| SEARCH-26-03 | 26 | Pending | Phase 26 (planned) — `GET /v1/tools` advertises `web_search` as an OpenAI-compatible function tool |
| SEARCH-26-04 | 26 | Pending | Phase 26 (planned) — guest/unverified users blocked at OWUI and edge-api boundaries |
| SEARCH-26-05 | 26 | Pending | Phase 26 (planned) — verified quota and credited BDT debit paths settle through the prepaid ledger |
| SEARCH-26-06 | 26 | Pending | Phase 26 (planned) — provider-blind errors expose no SearXNG, engine, or internal network detail |
| SEARCH-26-07 | 26 | Pending | Phase 26 (planned) — SDK function-tool roundtrip covered against the real Hive stack |
| SEARCH-26-08 | 26 | Pending | Phase 26 (planned) — EnterpriseEdge packaging includes or explicitly excludes SearXNG |

### FX/USD Zero-Leak (Phase 17)

| ID | Phase | Status | Evidence |
|----|-------|--------|----------|
| FX-17-01 | 17 | Satisfied | [evidence/FX-17-01.md](phases/17-fx-usd-zero-leak/evidence/FX-17-01.md) |
| FX-17-02 | 17 | Satisfied | [evidence/FX-17-02.md](phases/17-fx-usd-zero-leak/evidence/FX-17-02.md) |
| FX-17-03 | 17 | Satisfied | [evidence/FX-17-03.md](phases/17-fx-usd-zero-leak/evidence/FX-17-03.md) |
| FX-17-04 | 17 | Satisfied | [evidence/FX-17-04.md](phases/17-fx-usd-zero-leak/evidence/FX-17-04.md) |
| FX-17-05 | 17 | Satisfied | [evidence/FX-17-05.md](phases/17-fx-usd-zero-leak/evidence/FX-17-05.md) |
| FX-17-06 | 17 | Satisfied | [evidence/FX-17-06.md](phases/17-fx-usd-zero-leak/evidence/FX-17-06.md) |
| FX-17-07 | 17 | Satisfied | [evidence/FX-17-07.md](phases/17-fx-usd-zero-leak/evidence/FX-17-07.md) |
| FX-17-08 | 17 | Satisfied | [evidence/FX-17-08.md](phases/17-fx-usd-zero-leak/evidence/FX-17-08.md) |
| FX-17-09 | 17 | Satisfied | [evidence/FX-17-09.md](phases/17-fx-usd-zero-leak/evidence/FX-17-09.md) |
| FX-17-10 | 17 | Satisfied | [evidence/FX-17-10.md](phases/17-fx-usd-zero-leak/evidence/FX-17-10.md) |

Phase 17 closes the v1.1.0 BD regulatory blocker (deferred `amount-usd-on-bd-checkout`
from Phase 11). Customer surfaces across control-plane HTTP, ledger wire DTOs,
web-console DOM, invoice PDF, and chat-app rendered strings carry zero customer-USD/FX keys.
Internal accounting USD path (DB columns + server→Stripe payload) preserved. Lint
`packages/openai-contract/scripts/lint-no-customer-usd.mjs --all` walks Go + TS +
chat-app sources and is wired into CI as a blocking step. Hand-offs emitted to Phase 18
(HANDOFF-17-01 — RBAC matrix) and Phase 25 (HANDOFF-17-02 — chat-app re-audit
post-Phase-23 i18n bundles).

CAP-16-01 closes the v1.0 latent bug formerly recorded in `CLAUDE.md` Known
Issues §1 (`ensureCapabilityColumns` targeting `route_capabilities` instead
of `provider_capabilities`). The bug was eliminated by Phase 14's
media-columns work (DDL moved to
`supabase/migrations/20260414_01_provider_capabilities_media_columns.sql`,
which targets `public.provider_capabilities`); Phase 16 formally verifies
that closure with a regression-guard test
(`TestRoutingRepositoryDoesNotRunCapabilityDDL`) and an evidence file.

---

## Out of Scope

| Feature | Reason |
|---------|--------|
| End-user chat web application | Launch is strictly a developer API + control-plane product. |
| RAG projects or workspaces | Requires separate retrieval, workspace, content-governance semantics. |
| Hosted code runner or dev environment | Separate isolation + cost model from the API launch. |
| Credit subscriptions at launch | Commercial model is prepaid only for v1. |
| Customer-supplied upstream provider keys | Hive manages provider credentials internally; provider identity hidden. |
| OpenAI org/admin management endpoints | Not part of the drop-in developer value proposition. |
| Storing prompt or completion bodies by default | Conflicts with launch privacy requirement. |

---

## v2 Requirements (Out of v1.0 + v1.1)

- **SDK-01**: First-party branded SDK wrappers for JS/TS, Python, Java.
- **SUBS-01**: Subscription-like credit bundles resolving to Hive Credits.
- **ENT-01**: Org hierarchies, procurement controls, approval workflows.
- **ANAL-01**: Warehouse-backed deep analytics.

---

## Validator

`scripts/verify-requirements-matrix.sh` parses this file, extracts every
`[label](phases/.../evidence/*.md)` link, asserts the file exists with required
frontmatter (`requirement_id`, `status`, `verified_at`, `verified_by`,
`evidence`), and exits non-zero on any miss. Rows whose Evidence column reads
`Phase NN (planned)` or `Phase NN (archive ...)` are skipped — the former is an
intentional pending marker, the latter points back at archived v1.0 evidence
predating Phase 11.

---

*Active matrix established 2026-04-25 by Phase 11 — Compliance, Verification &
Artifact Cleanup. Archive: `.planning/milestones/v1.0-REQUIREMENTS.md`.*
