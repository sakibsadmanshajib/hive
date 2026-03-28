---
phase: 13-error-path-diff-headers
verified: "2026-03-22T07:25:41Z"
status: passed
score: "5/5 must-haves verified"
re_verification:
  previous_status: gaps_found
  previous_score: "4/5"
  gaps_closed:
    - "Pre-handler validation errors on /v1/* routes preserve x-model-routed, x-provider-used, x-provider-model, and x-actual-credits"
  gaps_remaining: []
  regressions: []
---

# Phase 13: Error-Path DIFF Headers Verification Report

**Phase Goal:** Close the remaining DIFF header contract gaps so all `/v1/*` error and stub responses preserve the differentiator headers
**Verified:** 2026-03-22T07:25:41Z
**Status:** passed
**Re-verification:** Yes - after gap closure

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
| --- | --- | --- | --- |
| 1 | Representative auth and service-error responses for `POST /v1/chat/completions`, `/v1/embeddings`, `/v1/images/generations`, and `/v1/responses` include `x-model-routed`, `x-provider-used`, `x-provider-model`, and `x-actual-credits` | ✓ VERIFIED | Fresh `pnpm --filter @hive/api exec vitest run test/routes/typebox-validation.test.ts test/routes/v1-error-diff-headers.test.ts test/routes/v1-stubs.test.ts` passed 29/29 tests; `apps/api/test/routes/v1-error-diff-headers.test.ts` still asserts the four headers for invalid-auth and `502` service-error cases. |
| 2 | Unsupported `/v1/*` stub routes return the same four DIFF headers on `404` responses | ✓ VERIFIED | The same focused `vitest` run passed `apps/api/test/routes/v1-stubs.test.ts` 10/10 tests, including static-header assertions for stub matrix and parameterized routes. |
| 3 | The four DIFF headers are seeded before `requireV1ApiPrincipal()` or `sendApiError()` can terminate the representative route and stub responses | ✓ VERIFIED | `setNoDispatchDiffHeaders()` remains the shared helper in `apps/api/src/routes/diff-headers.ts:3-7`; it is still called before auth/error exits in `chat-completions.ts:17`, `embeddings.ts:16`, `images-generations.ts:16`, `responses.ts:16`, and `v1-stubs.ts:7`. |
| 4 | The full API suite and Docker-container API build pass after the DIFF-header route and plugin changes | ✓ VERIFIED | Fresh `pnpm --filter @hive/api test` passed 69 files / 363 tests, and fresh `docker compose exec api sh -c "cd /app && pnpm --filter @hive/api build"` exited `0`. |
| 5 | Plugin-generated pre-dispatch `/v1/*` errors preserve the four DIFF headers so `DIFF-01` is fully closed | ✓ VERIFIED | `apps/api/src/routes/v1-plugin.ts:24-47` now seeds `setNoDispatchDiffHeaders(reply)` in both `setErrorHandler()` and `setNotFoundHandler()`. Fresh `apps/api/test/routes/typebox-validation.test.ts` passed 11/11 tests with DIFF-header assertions on chat, embeddings, images, responses, and nested-message `400` validation failures, and a direct runtime probe of `GET /v1/not-a-real-route` returned `404` with all four headers plus the unchanged OpenAI-style error body. |

**Score:** 5/5 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
| --- | --- | --- | --- |
| `apps/api/src/routes/diff-headers.ts` | Shared helper that seeds static no-dispatch DIFF headers | ✓ VERIFIED | Exists and sets `x-model-routed`, `x-provider-used`, `x-provider-model`, and `x-actual-credits` to the expected empty-string / `"0"` values at lines 3-7. |
| `apps/api/src/routes/chat-completions.ts` | Pre-dispatch DIFF-header seeding for chat auth/service error paths | ✓ VERIFIED | Imports the helper at line 8 and calls it at line 17 before auth at line 19; success paths still overwrite with routed headers at lines 37-44 and 75-80. |
| `apps/api/src/routes/embeddings.ts` | Pre-dispatch DIFF-header seeding for embeddings error paths | ✓ VERIFIED | Imports the helper at line 7 and calls it at line 16 before auth at line 18 and before `sendApiError()` branches at lines 25 and 43. |
| `apps/api/src/routes/images-generations.ts` | Pre-dispatch DIFF-header seeding while preserving richer service headers | ✓ VERIFIED | Imports the helper at line 7 and calls it at line 16 before auth, rate-limit, and prompt-validation exits; service-error header forwarding remains intact at lines 47-52. |
| `apps/api/src/routes/responses.ts` | Pre-dispatch DIFF-header seeding for responses error paths | ✓ VERIFIED | Imports the helper at line 7 and calls it at line 16 before auth at line 18 and service-error exit at line 33. |
| `apps/api/src/routes/v1-stubs.ts` | Stub `404` responses with static DIFF headers | ✓ VERIFIED | `stubHandler()` calls `setNoDispatchDiffHeaders(reply)` at line 7 immediately before `sendApiError()` at line 8. |
| `apps/api/src/routes/v1-plugin.ts` | Plugin-level DIFF-header preservation for validation, pre-handler, and unknown-route errors | ✓ VERIFIED | Imports the helper at line 12, seeds it in `setErrorHandler()` at line 27, and seeds it in `setNotFoundHandler()` at line 39; direct runtime probe confirmed scoped unknown-route behavior matches the contract. |
| `apps/api/test/routes/v1-error-diff-headers.test.ts` | Live route-level DIFF-header regressions for auth and service errors | ✓ VERIFIED | Continues to use the real `v1Plugin` test harness and passed 8/8 focused assertions. |
| `apps/api/test/routes/v1-stubs.test.ts` | Stub-route assertions for the static DIFF-header contract | ✓ VERIFIED | Passed 10/10 focused assertions with header checks on both matrix and parameterized routes. |
| `apps/api/test/routes/typebox-validation.test.ts` | Live plugin-level regressions that lock DIFF headers on `400` validation failures | ✓ VERIFIED | Defines `expectNoDispatchHeaders()` at lines 28-32 and asserts it across chat, embeddings, images, responses, and nested-message validation failures at lines 50-100 and 184-211. |

### Key Link Verification

| From | To | Via | Status | Details |
| --- | --- | --- | --- | --- |
| `apps/api/src/routes/diff-headers.ts` | `apps/api/src/routes/chat-completions.ts` | Route-level static DIFF-header seeding before auth/service-error exits | WIRED | Imported at `chat-completions.ts:8` and called at `chat-completions.ts:17` before auth at line 19. |
| `apps/api/src/routes/diff-headers.ts` | `apps/api/src/routes/embeddings.ts` | Route-level static DIFF-header seeding before auth/service-error exits | WIRED | Imported at `embeddings.ts:7` and called at `embeddings.ts:16` before auth at line 18. |
| `apps/api/src/routes/diff-headers.ts` | `apps/api/src/routes/images-generations.ts` | Route-level static DIFF-header seeding before auth/validation/service-error exits | WIRED | Imported at `images-generations.ts:7` and called at `images-generations.ts:16` before prompt validation and service-error handling. |
| `apps/api/src/routes/diff-headers.ts` | `apps/api/src/routes/responses.ts` | Route-level static DIFF-header seeding before auth/service-error exits | WIRED | Imported at `responses.ts:7` and called at `responses.ts:16` before auth at line 18. |
| `apps/api/src/routes/diff-headers.ts` | `apps/api/src/routes/v1-stubs.ts` | Stub `404` DIFF-header seeding | WIRED | Imported at `v1-stubs.ts:3` and called at `v1-stubs.ts:7` before `sendApiError()` at line 8. |
| `apps/api/src/routes/v1-plugin.ts` | `apps/api/src/routes/diff-headers.ts` | Shared no-dispatch helper reused before plugin-generated error responses | WIRED | Imports the helper at `v1-plugin.ts:12` and calls it at lines 27 and 39. |
| `apps/api/test/routes/typebox-validation.test.ts` | `apps/api/src/routes/v1-plugin.ts` | Real `app.register(v1Plugin, { services: mockServices })` validation probes | WIRED | The test registers the real plugin at lines 44, 113, and 178, then asserts the header contract on live `400` responses. |
| `apps/api/test/routes/v1-error-diff-headers.test.ts` | `apps/api/test/helpers/test-app.ts` | Real `v1Plugin` wiring with controllable mock services | WIRED | The test imports `createMockServices` / `createTestApp` and passed 8/8 live route-level assertions on the registered plugin surface. |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
| --- | --- | --- | --- | --- |
| `DIFF-01` | `13-PLAN.md`, `13-02-PLAN.md` | All `/v1/*` endpoints include `x-model-routed`, `x-provider-used`, `x-provider-model`, and `x-actual-credits` response headers | ✓ SATISFIED | `.planning/REQUIREMENTS.md` defines `DIFF-01` at line 36 and maps it to Phase 13 at line 111. Focused DIFF-header regressions passed 29/29 tests, full API verification passed 363/363 tests, the Docker-container API build passed, and the direct `GET /v1/not-a-real-route` probe returned `404` with all four DIFF headers plus the expected OpenAI-style body. |

Orphaned requirements for Phase 13: none. `DIFF-01` is the only requirement mapped to Phase 13 in `.planning/REQUIREMENTS.md`, and it is declared in both `13-PLAN.md` and `13-02-PLAN.md`.

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
| --- | --- | --- | --- | --- |
| None | - | No `TODO` / `FIXME` / placeholder / empty implementation / `console.log` matches in phase-touched files | ℹ️ Info | The phase files are substantive and the shared formatter boundary remains intact; `apps/api/src/routes/api-error.ts` still has no DIFF-header seeding logic. |

### Human Verification Required

None. Automated focused regressions, the full API suite, the Docker-container API build, direct runtime probing of the plugin not-found path, and static wiring checks were sufficient to determine status.

### Gaps Summary

The prior blocker is closed. `v1Plugin` now seeds `setNoDispatchDiffHeaders(reply)` for Fastify validation errors and scoped unknown-route responses, the validation suite locks those `400` paths across the in-scope POST `/v1/*` routes, the earlier representative route and stub regressions remain green, and the full API suite plus required Docker-only API build both pass.

`apps/api/src/routes/api-error.ts` remains free of DIFF-header logic, so the fix stays correctly scoped to the `/v1/*` route and plugin layers. Phase 13 now achieves the goal behind `DIFF-01`: `/v1/*` error and stub responses preserve the differentiator headers, including the previously missing plugin-generated validation path.

---

_Verified: 2026-03-22T07:25:41Z_
_Verifier: Claude (gsd-verifier)_
