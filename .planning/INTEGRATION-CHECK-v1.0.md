# Integration Check Report - Milestone v1.0

**Date:** 2026-03-22
**Phases verified:** 01 through 13
**Total requirements:** 21

---

## Wiring Summary

**Connected:** 20 requirement integrations confirmed in the current tree
**Partial:** 1 (`FOUND-07`)
**Missing:** 0 expected connections missing
**Process-only:** 1 (`OPS-02`, satisfied through tracked issues rather than cross-phase runtime wiring)

## API Coverage

**Consumed:** All OpenAI-compatible `/v1/*` routes are registered through `v1Plugin`
**Orphaned:** 0 routes with no registration

## Auth Protection

**Protected:** 5 of 5 OpenAI-compatible live endpoint groups require Bearer auth (`/v1/models`, `/v1/chat/completions`, `/v1/embeddings`, `/v1/images/generations`, `/v1/responses`)
**Unprotected:** 0 sensitive endpoint groups missing auth

## E2E Flows

**Complete:** 7 flows work end-to-end on the current tree
**Broken:** 0 flows have hard breaks
**Partial:** 0 flows are partially wired

## Fresh Evidence

- `pnpm --filter @hive/api test` -> `69` files, `368` tests passed
- `pnpm --filter @hive/api exec tsc --noEmit` -> passed

---

## Detailed Findings

### Confirmed Cross-Phase Wiring

- Phase 01 -> Phases 03/04/05/07/09/13: `sendApiError` and `v1Plugin` remain the shared error boundary for route errors, validation errors, stub 404s, and plugin not-found paths.
- Phase 02 -> Phases 04/05/07/10/13: TypeBox schemas and Fastify type-provider wiring remain live for models params, chat, embeddings, images, and responses.
- Phase 03 -> live `/v1/*` routes: `requireV1ApiPrincipal` is consumed by models, chat, embeddings, images, and responses; `v1Plugin` still owns non-stream `Content-Type` plus `x-request-id`.
- Phase 04 + Phase 10 + Phase 11: models serialization, auth, DIFF-header seeding, and SDK regressions remain wired end-to-end.
- Phase 05 + Phase 06 + Phase 11: non-stream chat, streaming chat, and the OpenAI SDK async-iterator path remain wired through the runtime and regression suite.
- Phase 07 + Phase 08 + Phase 12: embeddings route -> runtime -> alias resolution -> provider registry is wired, and the real-runtime SDK embeddings regression closes the former alias blind spot.
- Phase 07 + Phase 08 + Phase 13: success-path DIFF headers plus no-dispatch DIFF headers are unified across routes, stubs, and plugin-generated errors.

### Partial Integration

**FOUND-07**
- `apps/api/src/types/openai.d.ts` is generated and present, but no downstream runtime or test files import it directly.
- Impact: low. The milestone requirement as written is still satisfied because the types are generated and the compile step passes, but later phases do not materially deepen spec-type consumption.

### Non-Blocking Artifact Debt

- `.planning/phases/11-real-openai-sdk-regression-tests-ci-style-e2e/11-VERIFICATION.md` is stale: it still says `human_needed` and references the pre-Phase-12/13 `345/68` snapshot while current evidence is `368/69`.
- `apps/api/src/routes/__tests__/differentiators-headers.test.ts` still validates DIFF headers through the legacy `AiService` fixture rather than the current `RuntimeAiService` route stack.
- `DIFF-04` is centrally wired in `v1Plugin`, but live `x-request-id` assertions remain lighter than the rest of the DIFF-header coverage.

### Closed Prior Gaps

- Phase 12 closed the embeddings alias gap: `text-embedding-3-small` now succeeds on the real runtime path and remains provider-namespaced only internally.
- Phase 13 closed the DIFF-header error-path gap: route, stub, validation, and plugin-generated `/v1/*` error responses now preserve the DIFF headers.
- Phase 10 Nyquist validation is current and compliant; the prior stale-draft claim no longer applies.

---

## Requirements Integration Map

| Requirement | Integration Path | Status | Issue |
|-------------|------------------|--------|-------|
| FOUND-01 | `sendApiError -> v1Plugin -> routes/stubs/plugin errors` | WIRED | - |
| FOUND-02 | `requireV1ApiPrincipal -> models/chat/embeddings/images/responses -> SDK regressions` | WIRED | - |
| FOUND-03 | `ModelService.serializeModel -> GET /v1/models -> SDK list` | WIRED | - |
| FOUND-04 | `findById + sendApiError(404) -> GET /v1/models/:model -> SDK retrieve` | WIRED | - |
| FOUND-05 | `v1Plugin onSend + chat stream branch` | WIRED | - |
| FOUND-06 | `TypeBox schemas -> Fastify validation -> live route tests` | WIRED | - |
| FOUND-07 | `generated openai.d.ts -> downstream consumers` | PARTIAL | Types are generated but not directly imported downstream |
| CHAT-01 | `chat route -> RuntimeAiService.chatCompletions -> SDK create` | WIRED | - |
| CHAT-02 | `request body -> RuntimeAiService -> ProviderRegistry.chat` | WIRED | - |
| CHAT-03 | `provider/raw usage -> response.usage` | WIRED | - |
| CHAT-04 | `chat stream route -> RuntimeAiService.chatCompletionsStream -> SDK async iterator` | WIRED | - |
| CHAT-05 | `stream_options -> provider client stream request` | WIRED | - |
| SURF-01 | `embeddings route -> RuntimeAiService.embeddings -> ProviderRegistry.embeddings` | WIRED | - |
| SURF-02 | `images route -> RuntimeAiService.imageGeneration -> provider client` | WIRED | - |
| SURF-03 | `responses route -> RuntimeAiService.responses` | WIRED | - |
| DIFF-01 | `success headers + no-dispatch helper + plugin error handlers` | WIRED | - |
| DIFF-02 | `RuntimeAiService/provider headers -> x-actual-credits/usage` | WIRED | - |
| DIFF-03 | `resolveModelAlias -> ModelService.findById -> ProviderRegistry` | WIRED | - |
| DIFF-04 | `v1Plugin onRequest -> x-request-id` | WIRED | Live assertions are thinner than the rest of DIFF coverage |
| OPS-01 | `v1Plugin -> v1-stubs -> sendApiError` | WIRED | - |
| OPS-02 | `phase-local GitHub issue tracking artifacts` | PROCESS | Satisfied as documentation/process work, not a cross-phase runtime wiring path |

Requirements with no cross-phase runtime wiring: `OPS-02`.

---

## Summary

Current milestone integration status is `tech_debt`.

There are no broken end-to-end flows and no missing cross-phase connections in the implemented API surface. The remaining issues are:

1. partial downstream adoption of the generated OpenAI spec types (`FOUND-07` quality debt, not a requirement blocker)
2. one stale milestone verification artifact (`11-VERIFICATION.md`)
3. lighter live assertion depth on the `x-request-id` contract than on the rest of the DIFF-header surface
