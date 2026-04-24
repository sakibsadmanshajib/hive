# Next Session: Extend usage clamp to responses-API + streaming paths

## Context

PR #99 (merged 2026-04-24) shipped a `clampZeroCompletionUsage` helper that
fixes the billing leak observed during the staging burst (5.9% of chat
completions returned `usage.completion_tokens=0` while carrying real
output text). The clamp is **currently wired only into**:

- `normalizeChatCompletion` — `apps/edge-api/internal/inference/chat_completions.go`
- `normalizeCompletion` — `apps/edge-api/internal/inference/completions.go`

Two more code paths can hit the same upstream-zero-usage bug and are still
unprotected:

1. **Responses API** — `normalizeResponsesSync` in `apps/edge-api/internal/inference/responses.go:247`
2. **Streaming accumulator** — `apps/edge-api/internal/inference/stream.go`,
   especially the path around line 286 (`if includeUsage && !accumulator.HasUsage`)
   and line 308 (`if accumulator.HasUsage`). Streaming usage events are
   accumulated chunk-by-chunk and flushed at the end; the same upstream
   provider that returns `ct=0` non-stream will return `usage` events with
   `completion_tokens=0` on stream too.

Both leaks have the same revenue impact as the chat-completions leak — the
orchestrator records `OutputTokens = usage.CompletionTokens` for any path,
so any zero propagates to the ledger.

## Scope

Single PR, single concern: extend the clamp to the two unprotected paths.

Out of scope:

- Embeddings (`normalizeEmbeddings`) — embedding usage shape is different
  and `completion_tokens` is intentionally 0 there.
- LiteLLM provider pinning to reduce variance (separate prompt:
  `next-session-litellm-route-pinning.md`).
- Cross-request retroactive ledger reprice (business decision, not code).

## Goal

After this session merges, a re-run of the same 50-curl burst against
`api-hive.scubed.co/v1/responses` and `…/v1/chat/completions?stream=true`
must show **zero `ct=0` events on non-empty output** — same guarantee that
non-stream chat completions now have.

## Step-by-step

1. Branch: `fix/edge-api-usage-clamp-responses-streaming`
2. Read `apps/edge-api/internal/inference/responses.go:247` —
   `normalizeResponsesSync(respBody, aliasID, req)`. Note that responses
   output is structured as `output[]` array of items
   (`{type: "message", role, content[]}`, `{type: "reasoning", ...}`,
   tool_call items, etc.) — not a flat `choices[].message.content`. You
   need a helper `responsesOutputTexts(items []ResponsesOutputItem)` that
   walks `output_text` items and returns the user-visible string slice.
3. Wire `clampZeroCompletionUsage(usage, responsesOutputTexts(out), id, alias, EndpointResponses)`
   into `normalizeResponsesSync` after the Unmarshal.
4. Read `apps/edge-api/internal/inference/stream.go` — find the place
   where the `accumulator` produces the final `UsageResponse`. Likely
   around line 308–337. Walk the accumulator's recorded text content
   (the assistant message accumulated across chunks) and pass that into
   `clampZeroCompletionUsage` before the final flush to the orchestrator.
   - For chat-completions streaming: text comes from
     `delta.content` chunks → already accumulated as `accumulator.Content`
     (or similar — verify field name).
   - For responses streaming (`stream_responses.go`): text accumulates in
     `output_text.delta` events.
   - Both eventually call into the orchestrator's `recordCompletedEvent`
     — clamp must fire BEFORE that call.
5. Add unit tests in `usage_clamp_test.go`:
   - `TestNormalizeResponsesSync_ClampsZeroCt` — synthetic body with
     output_text and `usage.output_tokens=0`.
   - `TestStreamAccumulator_ClampsZeroCt` (or co-locate with
     `stream_test.go`) — feed synthetic chunks ending in a
     `usage` chunk with `completion_tokens=0` plus a real assistant
     message in earlier chunks; assert the final usage handed off has
     non-zero ct.
6. Run full edge-api test suite via Docker:
   ```
   cd deploy/docker && docker run --rm -v $(pwd)/../../:/workspace \
     -v hive_gomodcache:/go/pkg/mod -w /workspace hive-toolchain \
     'go test ./apps/edge-api/internal/inference/... -count=1 -short'
   ```
7. Manual verify post-deploy with 30-curl bursts:
   - `POST /v1/responses` with `{"model":"hive-default","input":"ping"}` ×30
   - `POST /v1/chat/completions` with `stream:true` ×30 — collect the
     final `usage` chunk from each stream (or use `stream_options.include_usage:true`
     to ensure usage is emitted)
   - Compare to baseline: zero ct=0 on non-empty content events expected.

## Files likely to touch

- `apps/edge-api/internal/inference/responses.go`
- `apps/edge-api/internal/inference/stream.go`
- `apps/edge-api/internal/inference/stream_responses.go` (if responses
  streaming has its own accumulator)
- `apps/edge-api/internal/inference/usage_clamp.go` (add a
  `responsesOutputTexts` helper next to `chatChoiceTexts` /
  `completionChoiceTexts`)
- `apps/edge-api/internal/inference/usage_clamp_test.go`

## Constraints

- Reasoning tokens (`completion_tokens_details.reasoning_tokens`) must
  remain untouched — clamp only the headline counter.
- Streaming path must NOT clamp on incremental chunks — clamp only at the
  flush, when the full accumulator is known. A premature clamp would
  produce inflated ct (counting the same chunk N times across deltas).
- `stream_options.include_usage = false` callers do not get a usage block
  at all — clamp is a no-op (nothing to clamp).
- NEVER push directly to main — feature branch + PR.

## Why CI will catch regressions

PR #96 added a post-deploy SDK replay step against staging. Once this PR
merges, the next deploy's replay will exercise the responses + streaming
paths through the OpenAI SDK and reveal any clamp bug as a billing test
failure.

## PR title

`fix(edge-api): extend usage clamp to responses + streaming paths`

## Out of scope (future)

- Tokenizer accuracy upgrade — current heuristic is `ceil(byte_len/4)`,
  conservative cl100k approximation. Switch to `tiktoken-go` only if a
  later audit shows >20% under-billing on long replies.
