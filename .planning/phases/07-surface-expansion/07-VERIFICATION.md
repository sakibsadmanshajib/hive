---
phase: 07-surface-expansion
verified: 2026-03-18T00:00:00Z
status: passed
score: 9/9 must-haves verified
re_verification: false
---

# Phase 7: Surface Expansion Verification Report

**Phase Goal:** Embeddings, image generation, and responses endpoints are fully schema-compliant
**Verified:** 2026-03-18
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | POST /v1/embeddings returns object:'list', data[].embedding, data[].index, model, usage | VERIFIED | registry.ts L302 builds compliant body; embeddings-compliance.test.ts validates |
| 2 | POST /v1/embeddings with unknown model returns 400 | VERIFIED | services.ts embeddings() checks capability !== "embedding" |
| 3 | POST /v1/embeddings with insufficient credits returns 402 | VERIFIED | services.ts embeddings() calls credits.consume() with early return |
| 4 | Credit/usage tracking records the embeddings request | VERIFIED | services.ts embeddings() calls usage.add() with endpoint "/v1/embeddings" |
| 5 | POST /v1/images/generations returns { created, data[] } without 'object' field | VERIFIED | services.ts L965-972 builds body with only `created` + `data`; images-compliance.test.ts L41 asserts `"object" in IMAGES_FIXTURE === false` |
| 6 | POST /v1/images/generations makes a real provider call | VERIFIED | openai-compatible-client.ts generateImage() uses fetchWithRetry to `/images/generations`; example.invalid only in mock-client.ts |
| 7 | POST /v1/responses accepts full CreateResponse fields | VERIFIED | schemas/responses.ts has model, input, instructions, temperature, max_output_tokens, tools, tool_choice, text, user |
| 8 | POST /v1/responses returns compliant Response object | VERIFIED | services.ts L875-895: resp_ ID, object:"response", created_at, status:"completed", output[].content[].type:"output_text", input_tokens/output_tokens/total_tokens |
| 9 | POST /v1/responses translates input+instructions to chat messages via registry.chat() | VERIFIED | services.ts responses() builds messages array, calls this.providerRegistry.chat() |

**Score:** 9/9 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `apps/api/src/schemas/embeddings.ts` | TypeBox EmbeddingsBodySchema | VERIFIED | Contains EmbeddingsBodySchema with additionalProperties:false |
| `apps/api/src/routes/embeddings.ts` | POST /v1/embeddings route | VERIFIED | Exports registerEmbeddingsRoute, uses requireV1ApiPrincipal, sendApiError |
| `apps/api/src/providers/types.ts` | ProviderEmbeddingsRequest/Response | VERIFIED | Both types exported; embeddings?() on ProviderClient interface |
| `apps/api/src/providers/openai-compatible-client.ts` | embeddings() method | VERIFIED | async embeddings() at L236 posts to /embeddings |
| `apps/api/src/providers/registry.ts` | embeddings() dispatch | VERIFIED | async embeddings() at L263; returns object:"list" compliant body |
| `apps/api/src/domain/types.ts` | capability includes "embedding" | VERIFIED | capability: "chat" \| "image" \| "embedding" |
| `apps/api/src/schemas/responses.ts` | Expanded ResponsesBodySchema | VERIFIED | instructions, temperature, max_output_tokens, tools present |
| `apps/api/src/routes/__tests__/embeddings-compliance.test.ts` | SURF-01 compliance tests | VERIFIED | describe("SURF-01:...), validates object:"list", data[].object:"embedding", prompt_tokens/total_tokens |
| `apps/api/src/routes/__tests__/images-compliance.test.ts` | SURF-02 compliance tests | VERIFIED | describe("SURF-02:..."), asserts "object" not in fixture, created is integer |
| `apps/api/src/routes/__tests__/responses-compliance.test.ts` | SURF-03 compliance tests | VERIFIED | describe("SURF-03:..."), resp_/msg_ IDs, output_text, input_tokens/output_tokens |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| routes/embeddings.ts | domain/ai-service (services.ts) | services.ai.embeddings() | VERIFIED | L25: `await services.ai.embeddings(` |
| services.ts embeddings() | providers/registry.ts | registry.embeddings() | VERIFIED | services.ts calls this.providerRegistry.embeddings() |
| providers/registry.ts | openai-compatible-client.ts | client.embeddings() | VERIFIED | registry.ts L276 checks client.embeddings, L-: calls client.embeddings(request) |
| routes/v1-plugin.ts | routes/embeddings.ts | registerEmbeddingsRoute() | VERIFIED | v1-plugin.ts L7 import + L51 call |
| routes/responses.ts | services.ts responses() | services.ai.responses(userId, request.body, ...) | VERIFIED | responses.ts L25: passes request.body |
| services.ts responses() | registry.ts chat() | registry.chat() | VERIFIED | services.ts builds messages array and calls this.providerRegistry.chat() |
| registry.ts imageGeneration() | openai-compatible-client.ts | client.generateImage() | VERIFIED | registry.ts L243: `await client.generateImage({...})` |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|----------|
| SURF-01 | 07-01-PLAN, 07-03-PLAN | POST /v1/embeddings endpoint with CreateEmbeddingResponse shape | SATISFIED | Full pipeline: schema → route → service → registry → provider client. Compliance test passes. |
| SURF-02 | 07-02-PLAN, 07-03-PLAN | POST /v1/images/generations schema-compliant with created + data[], no object field | SATISFIED | Real fetchWithRetry call in openai-compatible-client; registry/service build {created, data[]} without object field. Compliance test asserts absence. |
| SURF-03 | 07-02-PLAN, 07-03-PLAN | POST /v1/responses full CreateResponse/Response schema compliance | SATISFIED | Expanded schema, resp_* IDs, input_tokens/output_tokens naming, output_text type, chat translation. Compliance test validates all fields. |

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| apps/api/src/providers/mock-client.ts | 29 | example.invalid URL | INFO | Test mock only — not in production path; acceptable |

No blocker anti-patterns found. The `example.invalid` URL is isolated to the mock client used in tests, not in the production `openai-compatible-client.ts`.

### Human Verification Required

None required for automated checks. The following items could optionally be confirmed with a live call:

1. **Live embeddings call** — POST /v1/embeddings with a valid API key and model should return a real embedding array from the provider. Cannot verify number[] contents statically.
2. **Live images call** — POST /v1/images/generations should return an actual image URL. The `created` timestamp from provider is forwarded correctly.

These are confirmatory, not blocking — all schema shapes and wiring are verified in code.

### Gaps Summary

No gaps. All must-haves across plans 07-01, 07-02, and 07-03 are satisfied:

- The embeddings pipeline is complete end-to-end with correct OpenAI response shape
- Images endpoint makes real provider calls and returns {created, data[]} without an object field
- Responses endpoint accepts full CreateResponse schema, translates to chat internally, and returns a compliant Response object with correct id prefix, field names, and usage naming (input_tokens/output_tokens, not prompt_tokens/completion_tokens)
- All three SURF compliance test files exist and validate the critical shape requirements

---

_Verified: 2026-03-18_
_Verifier: Claude (gsd-verifier)_
