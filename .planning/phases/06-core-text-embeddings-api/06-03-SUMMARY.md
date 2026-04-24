---
phase: 06-core-text-embeddings-api
plan: "03"
subsystem: edge-api/inference
tags: [streaming, sse, responses-api, reasoning, chat-completions, completions]
dependency_graph:
  requires: ["06-02"]
  provides: ["streaming-chat-completions", "streaming-completions", "responses-api", "reasoning-gating"]
  affects: ["edge-api inference handler"]
tech_stack:
  added: []
  patterns:
    - SSE relay with bufio.Scanner and http.Flusher
    - UsageAccumulator struct for streaming token extraction
    - responsesEventTranslator state machine for Responses API event sequence
    - Defer-based reservation cleanup for client disconnects
key_files:
  created:
    - apps/edge-api/internal/inference/stream.go
    - apps/edge-api/internal/inference/reasoning.go
    - apps/edge-api/internal/inference/stream_test.go
    - apps/edge-api/internal/inference/responses.go
    - apps/edge-api/internal/inference/stream_responses.go
    - apps/edge-api/internal/inference/responses_test.go
  modified:
    - apps/edge-api/internal/inference/chat_completions.go
    - apps/edge-api/internal/inference/completions.go
    - apps/edge-api/internal/inference/orchestrator.go
    - apps/edge-api/internal/inference/handler.go
    - apps/edge-api/internal/inference/handler_test.go
    - apps/edge-api/internal/inference/types.go
decisions:
  - "SelectRouteResult has no SupportsReasoning field; reasoning capability is inferred from whether NeedReasoning=true was satisfied by the routing service. The validateReasoningCapability function uses the needFlags.NeedReasoning bool as a proxy."
  - "Terminal usage chunk synthesis triggers only when includeUsage=true and upstream sent no usage chunk, preventing double-emission."
  - "Responses API streaming does not emit data: [DONE] — it ends with event: response.completed, matching OpenAI SDK expectations."
  - "handler_test.go stale 501 tests updated to reflect live endpoint behavior (400 for missing model, 401 for streaming no-auth)."
metrics:
  duration: 10min
  completed: "2026-04-09"
  tasks_completed: 2
  files_created: 6
  files_modified: 6
---

# Phase 06 Plan 03: SSE Streaming, Responses API, and Reasoning Gating Summary

SSE relay with UsageAccumulator and client-disconnect cleanup, Responses API event translation state machine (response.created through response.completed), reasoning capability gating, and removal of all streaming/responses placeholder stubs.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | SSE streaming relay, reasoning gating, streaming for chat/completions + completions | 1e74cd7 | stream.go, reasoning.go, stream_test.go, chat_completions.go, completions.go, orchestrator.go |
| 2 | Responses API handler with event translation and full inference wiring | 9c187ad | responses.go, stream_responses.go, responses_test.go, types.go, handler.go, handler_test.go |

## What Was Built

### Task 1: SSE Streaming Relay

**`stream.go`** — `executeStreaming` method on `*Orchestrator`:
- Full lifecycle: authorize → route → validate reasoning → start attempt → reserve → dispatch → relay SSE → finalize
- `UsageAccumulator` struct with `Accumulate(ChatCompletionChunk)` and `ToUsageResponse()` methods
- `bufio.Scanner` relay rewrites `model` field to alias ID in every chunk
- Terminal usage chunk synthesis when `includeUsage=true` but upstream omits it
- Defer-based `client_disconnect` reservation release with `recordInterruptedEvent`

**`reasoning.go`** — capability gating:
- `validateReasoningCapability(w, model, reasoningEffort, routeSupportsReasoning)` — writes 400 `unsupported_parameter` if effort set but route incapable
- `validateResponsesReasoningCapability(w, model, reasoning, routeSupportsReasoning)` — same for Responses API `reasoning` field
- `normalizeReasoningUsage(usage)` — zero-initializes `CompletionTokensDetails` and `PromptTokensDetails` to prevent nil panics

**`chat_completions.go` / `completions.go`** — removed `not_implemented` streaming stubs, now call `executeStreaming`.

### Task 2: Responses API

**`types.go`** — added: `ResponsesRequest`, `ResponseObject`, `ResponseOutputItem`, `ResponseContentPart`, `ResponsesUsage`, `OutputTokensDetails`, `InputTokensDetails`

**`responses.go`** — `handleResponses`:
- Parses `ResponsesRequest`, validates `model` and `input`
- `translateResponsesToChatCompletions`: maps `instructions` → system message, `text.format` → `response_format`, `reasoning.effort` → `reasoning_effort`, `max_output_tokens` → `max_completion_tokens`, tools with Responses→chat format translation
- `normalizeResponsesSync`: wraps `ChatCompletionResponse` into `ResponseObject` with `resp_` prefixed ID, `msg_` prefixed output item IDs, usage field translation (`PromptTokens` → `InputTokens`, etc.)

**`stream_responses.go`** — `responsesEventTranslator` state machine:
- Emits: `response.created` → `response.output_item.added` → `response.content_part.added` → `response.output_text.delta` (per chunk) → `response.content_part.done` → `response.output_item.done` → `response.completed`
- Named SSE events (`event: response.created\ndata: {...}`) not plain `data:` lines
- Does NOT emit `data: [DONE]`
- Same defer-based disconnect cleanup as `executeStreaming`

**`handler.go`** — `/v1/responses` wired to `handleResponses`.

## Tests

- `stream_test.go`: 8 tests — `TestUsageAccumulatorAccumulate`, `TestUsageAccumulatorToUsageResponse`, `TestUsageAccumulatorReasoningTokensInDetails`, `TestStreamRelayRewritesModel`, `TestStreamTerminalUsageChunkSynthesis`, `TestStreamReasoningCapabilityGating`, `TestStreamReasoningCapabilityAllowed`, `TestStreamReasoningNilEffortAllowed`
- `responses_test.go`: 6 tests — `TestResponsesSyncNormalization`, `TestResponsesRequestTranslation`, `TestResponsesToolTranslation`, `TestResponsesReasoningCapabilityGating`, `TestResponsesModelAliasInResponse`, `TestResponsesStreamingLifecycleEvents`
- All 14 new tests + all 15 pre-existing tests pass (`go test ./internal/inference/` — 29 total)

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] `NeedCompletions` field does not exist in `SelectRouteInput`**
- **Found during:** Task 1 build
- **Issue:** `orchestrator.go` and `stream.go` referenced `NeedCompletions` but `SelectRouteInput` only has `NeedChatCompletions` (LiteLLM routes completions through chat-completions-capable routes)
- **Fix:** Removed `NeedCompletions` field from both struct literals
- **Files modified:** orchestrator.go, stream.go
- **Commit:** 1e74cd7

**2. [Rule 1 - Bug] Unused `encoding/json` import in `orchestrator.go`**
- **Found during:** Task 1 build
- **Issue:** Import was left from earlier refactoring, causing compilation failure
- **Fix:** Removed unused import
- **Files modified:** orchestrator.go
- **Commit:** 1e74cd7

**3. [Rule 1 - Bug] Stale placeholder tests in `handler_test.go`**
- **Found during:** Task 2 full test run
- **Issue:** `TestHandler_ResponsesPlaceholder` expected 501 (placeholder), `TestHandler_ChatCompletions_StreamNotImplemented` expected 501 (not-implemented stub). Both endpoints are now live.
- **Fix:** Renamed and updated both tests to verify live behavior (400 for missing model, 401 for no-auth streaming)
- **Files modified:** handler_test.go
- **Commit:** 9c187ad

## Self-Check: PASSED

Files verified:
- apps/edge-api/internal/inference/stream.go — FOUND
- apps/edge-api/internal/inference/reasoning.go — FOUND
- apps/edge-api/internal/inference/responses.go — FOUND
- apps/edge-api/internal/inference/stream_responses.go — FOUND

Commits verified:
- 1e74cd7 — FOUND (Task 1)
- 9c187ad — FOUND (Task 2)

Build: PASSED (`go build ./...`)
Tests: PASSED (`go test ./internal/inference/` — all 29 tests)
