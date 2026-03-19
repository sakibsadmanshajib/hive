# Integration Check Report — Milestone v1.0

**Date:** 2026-03-19
**Phases verified:** 01 through 09
**Total requirements:** 21

---

## Wiring Summary

**Connected:** 18 exports/integrations properly wired
**Orphaned:** 1 export created but unused in source
**Missing:** 0 expected connections not found

## API Coverage

**Consumed:** All /v1/* routes registered and reachable via v1-plugin
**Orphaned:** 0 routes with no registration

## Auth Protection

**Protected:** 5 of 5 AI endpoint handlers call requireV1ApiPrincipal (chat-completions, embeddings, images-generations, responses, plus models route uses bearer via v1-plugin scope)
**Unprotected:** 0 sensitive handlers missing auth

## E2E Flows

**Complete:** 6 flows work end-to-end
**Broken:** 0 flows have hard breaks
**Partial:** 1 (CHAT-05 usage chunk is pass-through only — see findings)

---

## Detailed Findings

### Orphaned Exports

**`apps/api/src/types/openai.d.ts` (27,559 lines)**
- From: Phase 2 (FOUND-07)
- Reason: Generated from OpenAI OpenAPI spec but zero imports found in source (`grep "from.*types/openai"` returns nothing). The file exists and was generated correctly; TypeBox schemas are used instead for runtime validation. The generated types are available for IDE/type-checking use but are not actively consumed by any runtime code path.
- Impact: Low — TypeBox schemas cover validation. Types are ambient and could be used by any future code. FOUND-07 requirement was to generate them, which was done.

### Missing Connections

None — all implemented exports are wired to consumers.

### Broken Flows

None — all wired flows traced end-to-end without hard breaks.

### Partial Flows

**CHAT-05: stream_options.include_usage**
- Status: PARTIAL PASS
- What works: `stream_options` including `include_usage` is accepted by the TypeBox schema, destructured into `...params`, and forwarded to the upstream provider via `registry.chatStream()`. The SSE response body is proxied raw from the provider, so any usage chunk the upstream emits is delivered to the client.
- What is untested end-to-end: The compliance tests in `chat-completions-streaming-compliance.test.ts` validate SSE chunk shape with static fixtures; they do not drive a live provider to confirm the upstream actually emits a usage chunk when `include_usage: true` is sent. This is an integration test gap, not a broken flow.
- Risk: Low — param forwarding is verified by CHAT-02 tests; the SSE pass-through means upstream behavior governs usage chunk emission.

### Unprotected Routes

None.

---

## Cross-Phase Wiring Verification

### Phase 1 → downstream (sendApiError, v1Plugin)

- `sendApiError`: imported and called in auth.ts, chat-completions.ts, images-generations.ts, responses.ts, embeddings.ts, v1-stubs.ts. CONNECTED.
- `v1Plugin`: registered in index.ts (via registerRoutes). CONNECTED.
- `STATUS_TO_TYPE` / `OpenAIErrorType`: used internally in api-error.ts. CONNECTED (self-contained).

### Phase 2 → downstream (TypeBox schemas, TypeBoxTypeProvider)

- `ChatCompletionsBodySchema`: wired into chat-completions.ts route opts. CONNECTED.
- `ImagesGenerationsBodySchema`: wired into images-generations.ts. CONNECTED.
- `ResponsesBodySchema`: wired into responses.ts. CONNECTED.
- `ModelsParamsSchema`: wired into models.ts for GET /v1/models/:model. CONNECTED.
- `EmbeddingsBodySchema`: wired into embeddings.ts. CONNECTED.
- `TypeBoxTypeProvider`: propagated through v1Plugin and all route function signatures. CONNECTED.
- `openai.d.ts` generated types: generated and present, zero source imports. ORPHANED (see above).

### Phase 3 → downstream (requireV1ApiPrincipal, Content-Type hook)

- `requireV1ApiPrincipal`: imported and called in chat-completions.ts, embeddings.ts, images-generations.ts, responses.ts. CONNECTED.
- `onSend` Content-Type hook: in v1-plugin.ts line 47, skips text/event-stream. CONNECTED.
- `createTestApp` helper: used in models-route.test.ts, v1-auth-compliance.test.ts. CONNECTED.

### Phase 4 → downstream (serializeModel, deriveOwnedBy, findById)

- `serializeModel` / `deriveOwnedBy`: used in models.ts list and retrieve handlers. CONNECTED.
- `findById` (ModelService): called in models.ts retrieve handler and runtime/services.ts for model resolution. CONNECTED.

### Phase 5 → downstream (chatCompletions body-based signature)

- `RuntimeAiService.chatCompletions(userId, body, ctx)`: called from chat-completions.ts non-streaming branch. CONNECTED.
- `guestChatCompletions` body-based: called from guest-chat.ts. CONNECTED.
- Provider pipeline (types/client/registry) param spreading: confirmed in openai-compatible-client.ts line 97, 154. CONNECTED.

### Phase 6 → downstream (chatCompletionsStream, chatStream)

- `RuntimeAiService.chatCompletionsStream()`: called from chat-completions.ts stream branch (line 22). CONNECTED.
- `ProviderRegistry.chatStream()`: called from runtime/services.ts line 755. CONNECTED.
- `Readable.fromWeb()` SSE piping: wired in chat-completions.ts. CONNECTED.
- `stream_options` forwarded: destructured into `...params` in services.ts line 751, forwarded at line 761. CONNECTED.

### Phase 7 → downstream (embeddings, images, responses pipelines)

- `RuntimeAiService.embeddings()`: called from embeddings.ts route. CONNECTED.
- `ProviderRegistry.embeddings()`: called from runtime/services.ts. CONNECTED.
- `registerEmbeddingsRoute`: imported and called in v1-plugin.ts line 8/58. CONNECTED.
- Images route: `generateImage` on provider client wired through registry. CONNECTED.
- Responses `input-to-chat` translation: confirmed in runtime/services.ts. CONNECTED.

### Phase 8 → downstream (x-request-id, AI headers, resolveModelAlias)

- `x-request-id` onRequest hook: in v1-plugin.ts line 19-21, fires on all /v1/* requests. CONNECTED.
- AI headers on non-streaming chat: set explicitly in chat-completions.ts lines 73-76. CONNECTED.
- AI headers on streaming chat: set explicitly in chat-completions.ts lines 38-41. CONNECTED.
- AI headers on embeddings: forwarded via `Object.entries(result.headers)` loop, line 43-44. CONNECTED.
- AI headers on images: forwarded via `Object.entries(result.headers)` loop, lines 44-53. CONNECTED.
- AI headers on responses: forwarded via `Object.entries(result.headers)` loop, lines 33-35. CONNECTED.
- `resolveModelAlias`: imported in model-service.ts, called in `findById` line 180. CONNECTED.

### Phase 9 → downstream (stub routes)

- `registerV1StubRoutes`: imported and called in v1-plugin.ts line 11/61. CONNECTED.
- GitHub issues #81-#87: created externally, no code wiring required. COMPLETE.

---

## Requirements Integration Map

| Requirement | Integration Path | Status | Issue |
|-------------|-----------------|--------|-------|
| FOUND-01 | Phase 1: sendApiError → all v1 routes + v1Plugin error/notFound handlers | WIRED | — |
| FOUND-02 | Phase 3: requireV1ApiPrincipal (bearer-only) → chat, images, responses, embeddings routes | WIRED | — |
| FOUND-03 | Phase 4: serializeModel → GET /v1/models list handler in models.ts | WIRED | — |
| FOUND-04 | Phase 4: findById + sendApiError 404 → GET /v1/models/:model retrieve handler | WIRED | — |
| FOUND-05 | Phase 3: onSend hook in v1Plugin → all /v1/* non-SSE responses get application/json; charset=utf-8 | WIRED | — |
| FOUND-06 | Phase 2: TypeBoxTypeProvider on Fastify instance → TypeBox schemas on all 5 POST/GET v1 routes | WIRED | — |
| FOUND-07 | Phase 2: openai.d.ts generated from spec (27K lines) | PARTIAL | Generated and present; zero source imports. Meets "generated" criterion but types are not consumed. |
| CHAT-01 | Phase 5: RuntimeAiService.chatCompletions → OpenAI-compliant response shape with all required fields | WIRED | — |
| CHAT-02 | Phase 5: body ...params spread → openai-compatible-client params forwarding | WIRED | — |
| CHAT-03 | Phase 5: usage object mapped from rawResponse in RuntimeAiService | WIRED | — |
| CHAT-04 | Phase 6: chatStream() → Readable.fromWeb() SSE proxy → client | WIRED | — |
| CHAT-05 | Phase 6: stream_options in ...params → forwarded to upstream; SSE pass-through delivers usage chunk | WIRED | stream_options forwarding confirmed; upstream usage emission untested end-to-end (no live provider test) |
| SURF-01 | Phase 7: RuntimeAiService.embeddings() → POST /v1/embeddings route → provider registry | WIRED | — |
| SURF-02 | Phase 7: generateImage on provider client → images route, quality/style forwarded, no spurious object field | WIRED | — |
| SURF-03 | Phase 7: input-to-chat translation + input_tokens/output_tokens usage naming in responses route | WIRED | — |
| DIFF-01 | Phase 8: AI headers (x-model-routed, x-provider-used, x-provider-model, x-actual-credits) on all 4 AI routes | WIRED | — |
| DIFF-02 | Phase 8: x-actual-credits value set on all AI service methods and forwarded through result.headers | WIRED | — |
| DIFF-03 | Phase 8: resolveModelAlias in model-service.findById → alias resolution before model lookup | WIRED | — |
| DIFF-04 | Phase 8: onRequest hook in v1Plugin sets x-request-id UUID before handler runs | WIRED | — |
| OPS-01 | Phase 9: registerV1StubRoutes → v1-plugin.ts line 61, 24 stub routes return 404 + OpenAI format | WIRED | — |
| OPS-02 | Phase 9: GitHub issues #81-#87 created for all 7 deferred endpoint groups | COMPLETE | No code wiring; external artifact only |

**Requirements with no cross-phase wiring (self-contained):**
- OPS-02: Fulfilled by GitHub issue creation only; no cross-phase code integration path.
- FOUND-07 (partial): Generated types file has no downstream consumers in source code. Requirement is "generate from spec" — that criterion is met. Consumption is a quality-of-life benefit, not a stated requirement.

---

## Summary

All 21 requirements have their implementation artifacts in place. 19 of 21 are fully wired with confirmed import/call chains. The two notes:

1. **FOUND-07** — `openai.d.ts` exists (27K lines, generated correctly) but is not imported by any runtime source file. TypeBox schemas serve the validation role instead. The requirement was generation, which is done.
2. **CHAT-05** — `stream_options.include_usage` is schema-accepted and forwarded through param spreading to the upstream provider. The raw SSE response is proxied, so upstream usage chunks reach the client. The gap is absence of a live-provider integration test confirming the upstream actually emits the usage chunk — this is a test coverage gap, not a wiring break.

No broken E2E flows. No unprotected sensitive routes. No orphaned API routes.
