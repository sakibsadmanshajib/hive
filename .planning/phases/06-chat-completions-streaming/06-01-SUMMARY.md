---
phase: 06-chat-completions-streaming
plan: 01
subsystem: api
tags: [sse, streaming, openrouter, fastify, readable-stream]

requires:
  - phase: 05-chat-completions-non-streaming
    provides: "chatCompletions() service method, route handler, provider pipeline"
provides:
  - "chatStream() on ProviderClient interface and OpenAICompatibleProviderClient"
  - "chatStream() dispatch on ProviderRegistry with circuit breaker"
  - "chatCompletionsStream() on RuntimeAiService with credit pre-charge and refund"
  - "SSE pass-through route branch for stream:true requests"
affects: [06-chat-completions-streaming, 07-streaming-tests]

tech-stack:
  added: []
  patterns: ["Readable.fromWeb() for SSE proxy piping", "fire-and-forget usage tracking for streams"]

key-files:
  created: []
  modified:
    - apps/api/src/providers/types.ts
    - apps/api/src/providers/openai-compatible-client.ts
    - apps/api/src/providers/registry.ts
    - apps/api/src/runtime/services.ts
    - apps/api/src/routes/chat-completions.ts
    - apps/api/test/routes/chat-completions-route.test.ts

key-decisions:
  - "No retry for streaming requests (maxRetries:0) -- retrying mid-stream leaks connections"
  - "Fire-and-forget usage tracking for streams -- exact token counts from final chunk deferred"
  - "No fallback chain for streaming -- single provider dispatch with circuit breaker"

patterns-established:
  - "SSE proxy: Readable.fromWeb() + content-type set before reply.send()"
  - "Client disconnect cleanup: request.raw.on('close') destroys upstream stream"

requirements-completed: [CHAT-04, CHAT-05]

duration: 2min
completed: 2026-03-18
---

# Phase 06 Plan 01: Streaming Pipeline Summary

**SSE streaming pipeline wired end-to-end: provider chatStream() with no-retry, registry dispatch, service with credit pre-charge, and route piping via Readable.fromWeb()**

## Performance

- **Duration:** 2 min
- **Started:** 2026-03-18T23:43:46Z
- **Completed:** 2026-03-18T23:46:16Z
- **Tasks:** 2
- **Files modified:** 6

## Accomplishments
- Full streaming pipeline from route to OpenRouter: route -> service -> registry -> provider
- OpenRouter SSE response body piped directly to client without intermediate buffering
- Pre-stream errors (unknown model, insufficient credits, provider failure) return standard sendApiError()
- Client disconnect aborts upstream connection to prevent leaked resources

## Task Commits

Each task was committed atomically:

1. **Task 1: Add chatStream() to provider client, registry, and types** - `c666755` (feat)
2. **Task 2: Add chatCompletionsStream() to RuntimeAiService and wire the route** - `47cc03a` (feat)

## Files Created/Modified
- `apps/api/src/providers/types.ts` - Added optional chatStream method to ProviderClient interface
- `apps/api/src/providers/openai-compatible-client.ts` - chatStream() with stream:true, maxRetries:0, timeoutMs:120s
- `apps/api/src/providers/registry.ts` - ProviderStreamExecutionResult type and chatStream() dispatch
- `apps/api/src/runtime/services.ts` - chatCompletionsStream() with model resolution, credits, refund
- `apps/api/src/routes/chat-completions.ts` - SSE branch replacing stream guard, Readable.fromWeb() piping
- `apps/api/test/routes/chat-completions-route.test.ts` - Updated stream tests for new streaming behavior

## Decisions Made
- No retry for streaming (maxRetries:0) -- retrying mid-stream is impossible and leaks connections
- Fire-and-forget usage tracking -- exact token counts from the final SSE chunk are a future enhancement
- No fallback chain for streaming -- single provider dispatch with circuit breaker support

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Updated stream guard test to match new streaming behavior**
- **Found during:** Task 2 (route wiring)
- **Issue:** Existing test expected 400 for stream:true, now streaming works
- **Fix:** Replaced with two tests: SSE piping success and stream error path
- **Files modified:** apps/api/test/routes/chat-completions-route.test.ts
- **Verification:** All 6 route tests pass
- **Committed in:** 47cc03a (Task 2 commit)

---

**Total deviations:** 1 auto-fixed (1 bug)
**Impact on plan:** Test update was necessary consequence of removing the stream guard. No scope creep.

## Issues Encountered
None

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- Streaming pipeline ready for chunk format validation and usage telemetry tests
- SSE headers set before reply.send() enabling v1-plugin onSend hook compatibility

---
*Phase: 06-chat-completions-streaming*
*Completed: 2026-03-18*
