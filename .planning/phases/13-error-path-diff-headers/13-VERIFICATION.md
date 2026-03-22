---
phase: 13-error-path-diff-headers
verified: "2026-03-22T05:30:29Z"
status: gaps_found
score: "4/5 must-haves verified"
gaps:
  - truth: "Pre-handler validation errors on /v1/* routes preserve x-model-routed, x-provider-used, x-provider-model, and x-actual-credits"
    status: failed
    reason: "TypeBox/Ajv validation errors are emitted from v1Plugin.setErrorHandler() before any route handler calls setNoDispatchDiffHeaders(), so the DIFF headers are absent."
    artifacts:
      - path: "apps/api/src/routes/v1-plugin.ts"
        issue: "setErrorHandler() and setNotFoundHandler() send JSON errors without seeding the no-dispatch DIFF headers"
      - path: "apps/api/test/routes/typebox-validation.test.ts"
        issue: "Validation tests cover 400 responses but assert only the OpenAI error body fields, not the DIFF headers"
    missing:
      - "Seed static no-dispatch DIFF headers for plugin-level validation/pre-handler errors before sending the error response"
      - "Add regression coverage for validation-error responses on in-scope /v1 routes to prove DIFF-01 end-to-end"
---

# Phase 13: Error-Path DIFF Headers Verification Report

**Phase Goal:** Close the remaining DIFF header contract gaps so all `/v1/*` error and stub responses preserve the differentiator headers
**Verified:** 2026-03-22T05:30:29Z
**Status:** gaps_found
**Re-verification:** No - initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
| --- | --- | --- | --- |
| 1 | Representative auth and service-error responses for `POST /v1/chat/completions`, `/v1/embeddings`, `/v1/images/generations`, and `/v1/responses` include `x-model-routed`, `x-provider-used`, `x-provider-model`, and `x-actual-credits` | ✓ VERIFIED | Fresh `pnpm --filter @hive/api exec vitest run test/routes/v1-error-diff-headers.test.ts test/routes/v1-stubs.test.ts` passed 18/18 tests; matrix assertions live in `apps/api/test/routes/v1-error-diff-headers.test.ts` lines 90-129. |
| 2 | Unsupported `/v1/*` stub routes return the same four DIFF headers on 404 responses | ✓ VERIFIED | Fresh focused `vitest` run passed; stub assertions are in `apps/api/test/routes/v1-stubs.test.ts` lines 35-66. |
| 3 | The four DIFF headers are seeded before `requireV1ApiPrincipal()` or `sendApiError()` can terminate the representative route/stub responses | ✓ VERIFIED | `setNoDispatchDiffHeaders()` is defined in `apps/api/src/routes/diff-headers.ts` lines 3-7 and called before auth/error exits in `chat-completions.ts:17`, `embeddings.ts:16`, `images-generations.ts:16`, `responses.ts:16`, and `v1-stubs.ts:7`. |
| 4 | The full API suite and Docker-container API build pass after the DIFF-header route changes | ✓ VERIFIED | Fresh `pnpm --filter @hive/api test` passed 69 files / 361 tests; fresh `docker compose exec api sh -c "cd /app && pnpm --filter @hive/api build"` exited 0. |
| 5 | Pre-handler validation errors on known `/v1/*` routes also preserve the four DIFF headers so `DIFF-01` is fully closed | ✗ FAILED | A live probe against the built `v1Plugin` returned `400` for invalid `POST /v1/chat/completions` input with only `x-request-id`; `x-model-routed`, `x-provider-used`, `x-provider-model`, and `x-actual-credits` were absent. `apps/api/src/routes/v1-plugin.ts` lines 23-45 handle these errors without DIFF-header seeding. |

**Score:** 4/5 truths verified

Truths 1-4 are the phase-plan must-haves. Truth 5 was added during requirement cross-reference because `DIFF-01` in `.planning/REQUIREMENTS.md` is broader than the representative matrix in `13-PLAN.md`.

### Required Artifacts

| Artifact | Expected | Status | Details |
| --- | --- | --- | --- |
| `apps/api/src/routes/diff-headers.ts` | Shared no-dispatch DIFF-header helper | ✓ VERIFIED | Exists and sets the four static headers at lines 3-7; imported by representative routes and stub handler. |
| `apps/api/src/routes/chat-completions.ts` | Pre-auth/error DIFF-header seeding for chat route | ✓ VERIFIED | Calls helper at line 17 before auth at line 19; success paths overwrite with routed headers at lines 37-44 and 75-80. |
| `apps/api/src/routes/embeddings.ts` | Pre-auth/error DIFF-header seeding for embeddings route | ✓ VERIFIED | Calls helper at line 16 before auth, rate-limit, and service-error exits. |
| `apps/api/src/routes/images-generations.ts` | Pre-auth/prompt/error DIFF-header seeding while preserving richer service headers | ✓ VERIFIED | Calls helper at line 16; preserves service-supplied error headers at lines 46-52. |
| `apps/api/src/routes/responses.ts` | Pre-auth/error DIFF-header seeding for responses route | ✓ VERIFIED | Calls helper at line 16 before auth, rate-limit, and service-error exits. |
| `apps/api/src/routes/v1-stubs.ts` | Stub 404 responses with static DIFF headers | ✓ VERIFIED | Stub handler seeds headers at line 7 before `sendApiError()` at line 8. |
| `apps/api/test/routes/v1-error-diff-headers.test.ts` | Live route-level DIFF-header regressions | ✓ VERIFIED | Uses `createTestApp()` / `createMockServices()` and `app.inject()` to exercise the real `v1Plugin` surface. |
| `apps/api/test/routes/v1-stubs.test.ts` | Stub-route DIFF-header assertions | ✓ VERIFIED | Asserts all four headers for stub matrix and parameterized route coverage. |
| `apps/api/src/routes/v1-plugin.ts` | Plugin-level DIFF-header preservation for pre-handler errors | ✗ FAILED | `setErrorHandler()` and `setNotFoundHandler()` at lines 23-45 emit error bodies without calling `setNoDispatchDiffHeaders()` or equivalent. |
| `apps/api/test/routes/typebox-validation.test.ts` | Regression coverage that locks DIFF headers on validation failures | ⚠️ PARTIAL | Exercises 400 validation failures at lines 43-79 and 152-178, but checks only error-body shape. |

### Key Link Verification

| From | To | Via | Status | Details |
| --- | --- | --- | --- | --- |
| `apps/api/src/routes/diff-headers.ts` | `apps/api/src/routes/chat-completions.ts` | Route-level static DIFF-header seeding before auth/service error exits | WIRED | Imported at line 8 and called at line 17 before auth at line 19. |
| `apps/api/src/routes/diff-headers.ts` | `apps/api/src/routes/embeddings.ts` | Route-level static DIFF-header seeding before auth/service error exits | WIRED | Imported at line 7 and called at line 16 before auth at line 18. |
| `apps/api/src/routes/diff-headers.ts` | `apps/api/src/routes/images-generations.ts` | Route-level static DIFF-header seeding before auth/validation/service error exits | WIRED | Imported at line 7 and called at line 16 before auth, prompt validation, and service-error handling. |
| `apps/api/src/routes/diff-headers.ts` | `apps/api/src/routes/responses.ts` | Route-level static DIFF-header seeding before auth/service error exits | WIRED | Imported at line 7 and called at line 16 before auth and service-error handling. |
| `apps/api/src/routes/diff-headers.ts` | `apps/api/src/routes/v1-stubs.ts` | Stub 404 DIFF-header seeding | WIRED | Imported at line 3 and called at line 7 before `sendApiError()` at line 8. |
| `apps/api/test/routes/v1-error-diff-headers.test.ts` | `apps/api/test/helpers/test-app.ts` | Real `v1Plugin` wiring with controllable mock services | WIRED | Test imports helper at lines 3-4; `createTestApp()` registers `v1Plugin` at `apps/api/test/helpers/test-app.ts` lines 289-299. |
| `apps/api/src/routes/v1-plugin.ts` | `apps/api/src/routes/diff-headers.ts` | Plugin-level validation/pre-handler error DIFF-header preservation | NOT_WIRED | `setErrorHandler()` / `setNotFoundHandler()` at lines 23-45 do not call `setNoDispatchDiffHeaders()` or otherwise set the four headers. |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
| --- | --- | --- | --- | --- |
| `DIFF-01` | `13-PLAN.md` | All `/v1/*` endpoints include `x-model-routed`, `x-provider-used`, `x-provider-model`, and `x-actual-credits` response headers | ✗ BLOCKED | `.planning/REQUIREMENTS.md` defines `DIFF-01` at line 36 and maps it to Phase 13 at line 111. Representative auth/service/stub coverage now passes, but a live validation-error probe on `POST /v1/chat/completions` returned `400` with only `x-request-id`, proving the full requirement is not yet met. |

Orphaned requirements for Phase 13: none. `DIFF-01` is the only requirement mapped to Phase 13 in `.planning/REQUIREMENTS.md`, and it is declared in `13-PLAN.md`.

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
| --- | --- | --- | --- | --- |
| None | - | No `TODO` / `FIXME` / placeholder / empty implementation matches in phase-touched files | ℹ️ Info | The phase files are substantive; the blocker is a real wiring gap in plugin-level error handling, not a placeholder stub. |

### Human Verification Required

None. Automated route-level tests, a direct runtime probe, the full API suite, and the Docker-container build were sufficient to determine status.

### Gaps Summary

Phase 13 implemented the planned route-layer and stub-layer DIFF-header seeding correctly. The shared helper exists, the representative AI routes seed headers before auth and service-error exits, stub 404s carry the static no-dispatch headers, the focused regressions pass, the full API suite passes, and the Docker-container API build passes.

The phase goal is still not achieved because `DIFF-01` is broader than the plan's representative matrix. Fastify validation failures happen before the route handlers run, so they bypass `setNoDispatchDiffHeaders()`. `apps/api/src/routes/v1-plugin.ts` currently formats those errors without seeding the DIFF headers, and the existing validation tests do not assert the header contract. Until plugin-handled validation errors preserve the four headers and that path is locked by regression coverage, Phase 13 cannot be considered complete.

---

_Verified: 2026-03-22T05:30:29Z_
_Verifier: Claude (gsd-verifier)_
