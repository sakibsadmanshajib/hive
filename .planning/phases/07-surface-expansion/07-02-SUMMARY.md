---
phase: 07-surface-expansion
plan: 02
subsystem: api-endpoints
tags: [images, responses, openai-compliance, provider-pipeline]
dependency_graph:
  requires: [07-01]
  provides: [compliant-images-endpoint, compliant-responses-endpoint]
  affects: [routes, schemas, runtime-ai-service]
tech_stack:
  added: []
  patterns: [input-to-chat-translation, response-object-mapping]
key_files:
  created: []
  modified:
    - apps/api/src/schemas/responses.ts
    - apps/api/src/runtime/services.ts
    - apps/api/src/routes/responses.ts
    - apps/api/src/domain/ai-service.ts
    - apps/api/test/routes/typebox-validation.test.ts
decisions:
  - Responses endpoint translates input+instructions to ProviderChatMessage[] and routes through registry.chat()
  - Response usage maps prompt_tokens->input_tokens and completion_tokens->output_tokens per OpenAI Responses API spec
metrics:
  duration: 707s
  completed: "2026-03-19T01:58:25Z"
  tasks_completed: 2
  tasks_total: 2
requirements: [SURF-02, SURF-03]
---

# Phase 7 Plan 2: Images & Responses Compliance Summary

Expanded responses endpoint to accept full CreateResponse schema and return compliant Response objects with proper usage naming, while confirming images endpoint already uses real provider calls with correct response shape.

## Task Results

| Task | Name | Commit | Key Changes |
|------|------|--------|-------------|
| 1 | Fix images/generations provider call and response shape | c7aa353 (07-01) | Already implemented in 07-01; generateImage on base client, quality/style forwarding, no object field |
| 2 | Expand responses schema and service for full CreateResponse/Response compliance | 0e5b6eb | New schema with 9 fields, chat translation, compliant Response object with correct usage naming |

## Deviations from Plan

### Task 1: Already Implemented

Task 1 changes (generateImage on OpenAICompatibleProviderClient, quality/style fields, revised_prompt support, removal of object:"list" from images) were already fully implemented by the 07-01 plan (commit c7aa353). No additional code changes were needed. The acceptance criteria were verified as already met.

### Auto-fixed Issues

**1. [Rule 1 - Bug] Validation test used old schema shape**
- **Found during:** Task 2
- **Issue:** typebox-validation.test.ts sent `{ input: "hello world" }` without required `model` field
- **Fix:** Updated test payloads to include `model: "gpt-4o"` matching new required field
- **Files modified:** apps/api/test/routes/typebox-validation.test.ts
- **Commit:** 0e5b6eb

## Decisions Made

1. **Input-to-chat translation pattern**: Responses endpoint builds ProviderChatMessage[] from input (string or array) with optional system instructions, then dispatches through existing registry.chat() pipeline
2. **Usage field naming**: Response object uses `input_tokens`/`output_tokens`/`total_tokens` (mapped from chat completion's `prompt_tokens`/`completion_tokens`)

## Verification

- TypeScript compiles clean (no errors in source files)
- All 273 tests pass (61 test files)
- Images response has no `object` field in registry or runtime service
- Responses schema contains all required fields: model, input, instructions, temperature, max_output_tokens, tools, tool_choice, text, user
- Response object uses correct usage naming: input_tokens, output_tokens, total_tokens
