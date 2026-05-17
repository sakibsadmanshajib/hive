---
phase: 18
slug: rbac-matrix
status: closed
nyquist_compliant: true
wave_0_complete: true
created: 2026-05-14
closed: 2026-05-14
---

# Phase 18 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.
> Sourced from `18-RESEARCH.md` §11 — Validation Architecture.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework (Go)** | `go test` (stdlib, table-driven, `-race`) |
| **Framework (TS unit)** | Vitest (existing `npm run test:unit`) |
| **Framework (E2E)** | Playwright (existing `npx playwright test`) |
| **Framework (CI lint)** | Node ripgrep wrapper, mirrors `packages/openai-contract/scripts/lint-no-customer-usd.mjs` |
| **Go config** | `go.work` workspace; Docker toolchain only |
| **TS config** | `apps/web-console/vitest.config.ts`, `apps/web-console/playwright.config.ts` |
| **Quick run (Go authz only)** | `cd deploy/docker && docker compose --profile tools run toolchain bash -c "cd /workspace && go test ./apps/control-plane/internal/authz/... -count=1 -short -race"` |
| **Quick run (TS gates)** | `cd deploy/docker && docker compose run web-console npx vitest run lib/viewer-gates.test.ts` |
| **Full Go suite** | `cd deploy/docker && docker compose --profile tools run toolchain bash -c "cd /workspace && go test ./apps/control-plane/... -count=1 -short"` |
| **Full TS suite** | `cd deploy/docker && docker compose run web-console npm run test:unit` |
| **Playwright spec** | `cd apps/web-console && npx playwright test tests/e2e/rbac-unverified.spec.ts` |
| **CI lint** | `node packages/openai-contract/scripts/lint-no-bare-role-check.mjs` |
| **Estimated runtime (quick)** | ~8s Go authz pkg / ~4s TS gates |
| **Estimated runtime (full)** | ~90s Go control-plane / ~30s TS unit / ~45s Playwright spec |

---

## Sampling Rate

- **After every task commit:** Run both quick commands above (Go authz + TS gates).
- **After every plan wave:** Full Go suite + full TS suite + CI lint.
- **Before `/gsd:verify-work`:** Full Go + full TS + Playwright + CI lint all green.
- **Max feedback latency:** 15 seconds (quick-run upper bound).

---

## Per-Task Verification Map

Task IDs are placeholders pending planner output (will be `18-XX-YY` shape).
Mapping is by requirement → verification, not by task — the planner assigns the
task IDs in a later step.

| Requirement | Test Type | Automated Command | Wave 0? | Status |
|-------------|-----------|-------------------|---------|--------|
| RBAC-18-01 — authz package + matrix table | Go unit | `go test ./apps/control-plane/internal/authz/... -count=1` | ❌ W0 (new pkg) | ✅ |
| RBAC-18-02 — `Policy.Can` decision over (role, verified, perm) | Go unit (table-driven) | same as above; matrix in `policy_test.go` | ❌ W0 | ✅ |
| RBAC-18-03 — backend handler migration (8 modules) | Go integration | full Go control-plane suite | ✅ exists (per-module `http_test.go`) | ✅ |
| RBAC-18-04 — viewer-response wire flip (`permissions: []`) | Go integration | `go test ./apps/control-plane/internal/accounts/... -run TestViewer -count=1` | ✅ exists (`service_test.go`) | ✅ |
| RBAC-18-05 — TS codegen (`permissions.generated.ts`) | CI drift check | `make gen-permissions && git diff --exit-code apps/web-console/lib/control-plane/permissions.generated.ts` | ❌ W0 (new emitter `cmd/gen-permissions/main.go`) | ✅ |
| RBAC-18-06 — web-console `can()` helper + consumer migration | Vitest | `vitest run lib/viewer-gates.test.ts` | ✅ exists | ✅ |
| RBAC-18-07 — `lint-no-bare-role-check` CI step | Lint script | `node packages/openai-contract/scripts/lint-no-bare-role-check.mjs` | ❌ W0 (new script) | ✅ |
| RBAC-18-08 — Go integration matrix (role × verified × module) | Go integration | full Go control-plane suite + new `authz_integration_test.go` | ❌ W0 (new test file) | ✅ |
| RBAC-18-09 — vitest matrix-parity (FE decisions == Go decisions) | Vitest | `vitest run lib/permissions.parity.test.ts` | ❌ W0 (new test file) | ✅ |
| RBAC-18-10 — Playwright unverified flow on /billing + /keys | Playwright | `npx playwright test tests/e2e/rbac-unverified.spec.ts` | ❌ W0 (new spec) | ✅ |
| RBAC-18-11 — STATE.md ship-gate flip + todo resolution | Manual | `grep 'rbac_matrix: true' .planning/STATE.md` + `ls .planning/todos/done/ \| grep design-rbac-authorization` | n/a | ✅ |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

New artefacts the executor must create before downstream waves can run their
verification commands:

- [x] `apps/control-plane/internal/authz/permissions.go` — typed `Permission` const + registry with `RequiresVerified` flag.
- [x] `apps/control-plane/internal/authz/policy.go` — `Actor` struct, `Policy.Can(actor, perm) bool`, `RequirePermission(perm)` middleware.
- [x] `apps/control-plane/internal/authz/policy_test.go` — table-driven matrix test (the spec).
- [x] `apps/control-plane/cmd/gen-permissions/main.go` — TS emitter.
- [x] `apps/web-console/lib/control-plane/permissions.generated.ts` — codegen output (committed).
- [x] `apps/web-console/tests/unit/permissions.parity.test.ts` — vitest parity test consuming an independent `EXPECTED` table.
- [x] `apps/web-console/tests/e2e/rbac-unverified.spec.ts` — Playwright spec.
- [x] `apps/control-plane/internal/authz/authz_integration_test.go` — handler-level role × verified × module coverage (covered by per-module `http_test.go` + matrix test).
- [x] `packages/openai-contract/scripts/lint-no-bare-role-check.mjs` — ripgrep wrapper, allowlist `internal/authz/`, `platform/role*.go`, matrix tests.
- [x] `.github/workflows/*.yml` — `make gen-permissions && git diff --exit-code` + `node packages/openai-contract/scripts/lint-no-bare-role-check.mjs` wired as required CI steps.
- [x] Makefile target `gen-permissions` that wraps the emitter.

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Sidebar nav re-renders correctly when viewer transitions verified→owner | RBAC-18-06 | Visual interaction-state — not asserted by vitest | Sign in as unverified test user; verify email; reload `/console`; assert "API Keys" / "Members" links appear without page nav |
| `rbac_matrix` ship-gate flip persisted | RBAC-18-11 | Single-line edit in STATE.md, asserted by grep but reviewer must read context | `git diff .planning/STATE.md` shows `rbac_matrix: true` with surrounding comment removed |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies declared in PLAN.md.
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify.
- [ ] Wave 0 covers all MISSING references above.
- [ ] No watch-mode flags in CI invocations.
- [ ] Feedback latency < 15s on quick run.
- [ ] `nyquist_compliant: true` set in frontmatter only after planner confirms every requirement maps to a task.

**Approval:** pending
