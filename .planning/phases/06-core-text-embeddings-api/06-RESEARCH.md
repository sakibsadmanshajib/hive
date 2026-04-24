# Phase 6: Core Text & Embeddings API - Research

**Researched:** 2026-04-02
**Domain:** OpenAI-compatible inference endpoints (text generation, streaming, embeddings, reasoning)
**Confidence:** HIGH

## Summary

Phase 6 delivers the core inference value path: `POST /v1/responses`, `POST /v1/chat/completions`, `POST /v1/completions`, and `POST /v1/embeddings`. These are the endpoints that make Hive a drop-in OpenAI replacement. The edge API shell, authorization middleware, routing selector, reservation/finalization accounting, and usage event primitives already exist from Phases 1-5. Phase 6 plugs endpoint handlers into the existing `apps/edge-api` shell, dispatches requests through the control-plane routing selector to LiteLLM, and normalizes upstream responses into exact OpenAI-shaped output including SSE streaming, reasoning fields, structured outputs, tool calling, and usage accounting.

The architecture is a thin edge proxy pattern: the edge API validates the request, authorizes via hot-path snapshot, selects a route via control-plane, forwards the request to LiteLLM (which handles provider translation), then normalizes the response back to the caller. For streaming, the edge must relay SSE chunks in real-time while accumulating usage for finalization. LiteLLM already returns OpenAI-shaped responses for chat/completions and embeddings, but Hive must own the normalization layer for: (a) provider-blind error sanitization, (b) reasoning field translation, (c) Responses API event format (which LiteLLM does not natively produce), and (d) usage-bearing terminal chunks and reservation lifecycle hooks.

**Primary recommendation:** Build a request orchestrator in `apps/edge-api/internal/inference/` that handles the full lifecycle (parse -> authorize -> route -> reserve -> dispatch -> normalize -> finalize) for all four endpoint families, with per-endpoint adapters for request/response shape differences.

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions
- The initial Phase 6 rollout includes function and tool calling on supported text endpoints; this is not deferred to a later hardening pass.
- Structured outputs are first-class from the start. JSON-mode and JSON-schema-shaped behavior should be supported anywhere the OpenAI endpoint contract exposes them.
- Hive should support the broader OpenAI text request surface up front rather than shipping an artificially narrowed "plain text only" subset.
- Capability mismatches must hard-fail with OpenAI-style errors. Hive should not silently degrade a request to plain text or weaker behavior when the requested contract cannot be honored.
- Model capability tags and public capability communication should stay authoritative enough that users can tell which aliases support which behaviors, while remaining OpenAI-compliant and provider-blind.
- Hive should mirror OpenAI behavior per endpoint family instead of inventing a Hive-specific convergence layer across `responses`, `chat/completions`, and `completions`.
- Where OpenAI exposes meaningful differences between `responses`, `chat/completions`, and legacy `completions`, Hive should preserve those differences instead of flattening them.
- On supported routes, Hive should aim for the richest OpenAI-style streaming behavior from day one, including usage-bearing terminal chunks and lifecycle-style events where the endpoint contract supports them.
- Reasoning-visible behavior must mirror OpenAI semantics rather than leaking provider-native reasoning traces or extra vendor-specific fields.
- Requests that ask for reasoning controls or reasoning-visible output on unsupported aliases should follow OpenAI-compatible strict behavior, not Hive-defined fallback behavior.
- Interrupted and failed stream termination should replicate OpenAI's terminal behavior as closely as possible, even when upstream providers are messy internally.
- `/v1/embeddings` should follow the OpenAI request surface rather than a reduced Hive-specific subset, including the common input shapes and options OpenAI exposes.
- Options such as `dimensions` and `encoding_format` should be supported wherever the OpenAI contract allows them, and should hard-fail in OpenAI style when a selected alias cannot honor them.
- Stored retrieval, update, delete, and input-item listing flows for `responses` and `chat.completions` remain out of scope for this phase even though create/inference flows are in scope.

### Claude's Discretion
- Downstream agents can choose the exact internal translation layer, adapter shims, reservation/finalization orchestration, and SSE normalization internals as long as the public behavior stays OpenAI-compatible and provider-blind.
- Downstream agents can choose the exact catalog/tag schema and customer-facing capability wording as long as the resulting public surface remains compatible, explicit about supported behaviors, and faithful to the underlying alias capability matrix.
- Downstream agents can decide how much behavior is passed through versus translated per provider, provided the user-facing contract still matches OpenAI semantics and unsupported combinations fail explicitly.

### Deferred Ideas (OUT OF SCOPE)
None -- discussion stayed within phase scope.
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| API-01 | Developer can call `responses`, `chat/completions`, and `completions` with OpenAI-compatible request and response shapes. | Full OpenAI request/response schema documented below; edge handler architecture; routing integration via existing `SelectRoute`; LiteLLM dispatch for provider translation; tool calling and structured output support. |
| API-02 | Developer can stream supported text-generation endpoints with OpenAI-compatible SSE event ordering, chunk formats, and terminal events. | Chat completions chunk schema (`chat.completion.chunk`); Responses API lifecycle events; `stream_options.include_usage` terminal chunk; `data: [DONE]` sentinel; interrupted stream handling. |
| API-03 | Developer can call `embeddings` with OpenAI-compatible request and response behavior. | Embeddings request/response schema; `dimensions` and `encoding_format` parameters; batch input support; LiteLLM `/embeddings` pass-through. |
| API-04 | Developer can use reasoning or thinking-related request parameters, and Hive returns translated reasoning outputs and usage details when upstream support exists. | Reasoning token accounting (`completion_tokens_details.reasoning_tokens`); `reasoning_effort` parameter; Responses API reasoning items with `encrypted_content` and summary; capability-gated hard-fail for unsupported aliases. |
</phase_requirements>

## Standard Stack

### Core (already in project)
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| Go | 1.24 | Edge API and control-plane | Already established in project; excellent for streaming proxy, concurrency, low-latency HTTP handling. |
| LiteLLM Proxy | main-stable (Docker) | Provider translation layer | Already deployed; translates OpenRouter/Groq into OpenAI-shaped responses; handles chat/completions, completions, and embeddings natively. |
| Redis (go-redis/v9) | v9.18.0 | Rate limiting, ephemeral state | Already in edge-api dependencies for hot-path enforcement. |
| net/http (stdlib) | Go 1.24 | HTTP server, SSE streaming | Standard library; already used for all edge-api handlers. |

### Supporting (new for Phase 6)
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `encoding/json` (stdlib) | Go 1.24 | Request/response serialization | All endpoint handlers. |
| `bufio.Scanner` (stdlib) | Go 1.24 | SSE line-by-line stream parsing | Consuming upstream SSE from LiteLLM and relaying to client. |
| `net/http/httputil` (stdlib) | Go 1.24 | Reverse proxy utilities (optional) | If needed for non-streaming pass-through; likely custom for streaming. |
| `github.com/google/uuid` | (already dep) | Request ID generation | Generating unique request IDs for accounting. |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Custom SSE relay | httputil.ReverseProxy | ReverseProxy does not support streaming SSE chunk-by-chunk relay with per-chunk inspection; custom relay is required for usage extraction and normalization. |
| ogen-generated types | Hand-written Go structs | ogen generates from OpenAPI but adds significant complexity; for 4 endpoints, hand-written structs validated against upstream openapi.yaml are simpler and more maintainable. |
| Direct provider calls | LiteLLM proxy | Direct calls would require reimplementing all provider translation; LiteLLM already handles OpenRouter, Groq, and 100+ providers. |

## Architecture Patterns

### Recommended Project Structure
```
apps/edge-api/internal/
  inference/
    handler.go          # HTTP handler routing to per-endpoint dispatchers
    chat_completions.go # POST /v1/chat/completions request/response logic
    completions.go      # POST /v1/completions request/response logic
    responses.go        # POST /v1/responses request/response + event translation
    embeddings.go       # POST /v1/embeddings request/response logic
    orchestrator.go     # Shared lifecycle: authorize -> route -> reserve -> dispatch -> finalize
    stream.go           # SSE relay, chunk normalization, usage extraction
    stream_responses.go # Responses API event translation (lifecycle events)
    types.go            # OpenAI-compatible request/response structs
    types_stream.go     # Streaming chunk and event types
    reasoning.go        # Reasoning field translation and capability gating
    errors.go           # Inference-specific OpenAI error helpers
    litellm_client.go   # HTTP client for LiteLLM proxy dispatch
```

### Pattern 1: Request Lifecycle Orchestrator
**What:** Every inference request follows the same lifecycle: Parse Request -> Authorize (hot-path) -> Select Route (control-plane) -> Create Reservation (accounting) -> Dispatch to LiteLLM -> Normalize Response -> Finalize/Release Reservation -> Return Response.
**When to use:** All four endpoint families.
**Example:**
```go
// Simplified orchestrator flow
func (o *Orchestrator) Execute(ctx context.Context, req InferenceRequest) error {
    // 1. Authorize via existing authorizer
    snapshot, ok := authorizeAliasRequest(w, r, o.authorizer, req.Model, estimatedCredits, 0, 0)
    if !ok { return nil } // error already written

    // 2. Select route via control-plane
    route, err := o.routingClient.SelectRoute(ctx, routingRequest(req))
    if err != nil { return writeRoutingError(w, req.Model, err) }

    // 3. Create reservation
    reservation, err := o.accountingClient.CreateReservation(ctx, reservationInput(snapshot, req, route))
    if err != nil { return writeReservationError(w, err) }

    // 4. Dispatch to LiteLLM (streaming or non-streaming)
    if req.Stream {
        return o.dispatchStreaming(ctx, w, req, route, reservation)
    }
    return o.dispatchSync(ctx, w, req, route, reservation)
}
```

### Pattern 2: SSE Relay with Usage Extraction
**What:** For streaming requests, the edge reads SSE lines from LiteLLM, inspects each chunk for usage data, relays to the client in real-time, and finalizes the reservation when the stream ends.
**When to use:** `stream=true` on chat/completions, completions; `stream=true` on responses.
**Example:**
```go
func (o *Orchestrator) relaySSE(ctx context.Context, w http.ResponseWriter, upstream io.ReadCloser, reservation Reservation) error {
    flusher, ok := w.(http.Flusher)
    if !ok { return errors.New("streaming not supported") }

    w.Header().Set("Content-Type", "text/event-stream")
    w.Header().Set("Cache-Control", "no-cache")
    w.Header().Set("Connection", "keep-alive")
    w.WriteHeader(http.StatusOK)

    scanner := bufio.NewScanner(upstream)
    var totalUsage UsageAccumulator

    for scanner.Scan() {
        line := scanner.Text()
        if line == "data: [DONE]" {
            fmt.Fprintf(w, "data: [DONE]\n\n")
            flusher.Flush()
            break
        }
        if strings.HasPrefix(line, "data: ") {
            chunk := line[6:]
            // Extract usage from terminal chunk if present
            totalUsage.Accumulate(chunk)
            // Sanitize provider-blind fields
            sanitized := sanitizeChunk(chunk)
            fmt.Fprintf(w, "data: %s\n\n", sanitized)
            flusher.Flush()
        } else {
            // Relay event: lines and blank lines as-is
            fmt.Fprintf(w, "%s\n", line)
            if line == "" { flusher.Flush() }
        }
    }

    // Finalize reservation with accumulated usage
    o.finalizeReservation(ctx, reservation, totalUsage)
    return nil
}
```

### Pattern 3: Responses API Event Translation
**What:** LiteLLM returns chat-completions-style streaming. For the `/v1/responses` endpoint, Hive must translate into Responses API lifecycle events: `response.created`, `response.output_item.added`, `response.content_part.added`, `response.output_text.delta`, `response.content_part.done`, `response.output_item.done`, `response.completed`.
**When to use:** `POST /v1/responses` with `stream=true`.
**Key insight:** The Responses API uses `event:` + `data:` SSE format (named events), while chat/completions uses only `data:` lines. The edge must generate the richer event envelope.

### Pattern 4: Capability-Gated Hard Failure
**What:** Before dispatching, check the route's capability flags against request features. If the request asks for streaming, reasoning, tool calling, structured output, or embeddings dimensions that the selected route cannot honor, return an OpenAI-style error immediately.
**When to use:** Every request, before dispatch.
**Example:**
```go
func validateCapabilities(route SelectionResult, req InferenceRequest) *OpenAIError {
    if req.Stream && !route.SupportsStreaming {
        return unsupportedParamError("stream", req.Model)
    }
    if req.HasReasoningParams() && !route.SupportsReasoning {
        return unsupportedParamError("reasoning_effort", req.Model)
    }
    if req.HasTools() && !route.SupportsToolCalling {
        return unsupportedParamError("tools", req.Model)
    }
    return nil
}
```

### Anti-Patterns to Avoid
- **Flattening endpoint families:** Do NOT create a single unified request type that merges `responses`, `chat/completions`, and `completions` into one shape. Each endpoint has distinct request/response schemas and must be handled separately.
- **Silently dropping unsupported parameters:** Do NOT ignore parameters like `reasoning_effort`, `response_format`, or `tools` when the route cannot honor them. Hard-fail with OpenAI-style errors.
- **Storing request/response bodies:** Do NOT log or persist prompt or completion content. Only structured metadata, token counts, and usage events are stored (per PRIV-01).
- **Trusting LiteLLM response shapes blindly:** Always validate and normalize. LiteLLM may include provider-specific fields, non-standard error formats, or missing usage data that must be sanitized before returning to the caller.

## OpenAI API Schema Reference

### POST /v1/chat/completions

**Request (key fields):**
| Field | Type | Required | Notes |
|-------|------|----------|-------|
| `model` | string | Yes | Hive alias ID |
| `messages` | array | Yes | Array of message objects (system/developer/user/assistant/tool/function roles) |
| `stream` | boolean | No | Default false; enables SSE streaming |
| `stream_options` | object | No | `{"include_usage": true}` for usage in terminal chunk |
| `temperature` | number | No | 0-2 |
| `top_p` | number | No | 0-1 |
| `n` | integer | No | Number of choices (default 1) |
| `max_tokens` / `max_completion_tokens` | integer | No | Output length limit |
| `stop` | string/array | No | Stop sequences |
| `presence_penalty` / `frequency_penalty` | number | No | -2.0 to 2.0 |
| `tools` | array | No | Tool/function definitions |
| `tool_choice` | string/object | No | `auto`, `none`, `required`, or specific tool |
| `response_format` | object | No | `{"type": "json_object"}` or `{"type": "json_schema", "json_schema": {...}}` |
| `reasoning_effort` | string | No | `low`, `medium`, `high` (reasoning models only) |
| `logprobs` | boolean | No | Return log probabilities |
| `seed` | integer | No | Deterministic sampling |
| `user` | string | No | End-user identifier |

**Response object (`chat.completion`):**
```json
{
  "id": "chatcmpl-...",
  "object": "chat.completion",
  "created": 1694268190,
  "model": "hive-gpt-4o",
  "system_fingerprint": "fp_44709d6fcb",
  "choices": [{
    "index": 0,
    "message": {
      "role": "assistant",
      "content": "Hello!",
      "tool_calls": null,
      "refusal": null
    },
    "logprobs": null,
    "finish_reason": "stop"
  }],
  "usage": {
    "prompt_tokens": 9,
    "completion_tokens": 12,
    "total_tokens": 21,
    "completion_tokens_details": {
      "reasoning_tokens": 0,
      "accepted_prediction_tokens": 0,
      "rejected_prediction_tokens": 0
    },
    "prompt_tokens_details": {
      "cached_tokens": 0
    }
  }
}
```

**Streaming chunk (`chat.completion.chunk`):**
```json
{
  "id": "chatcmpl-...",
  "object": "chat.completion.chunk",
  "created": 1694268190,
  "model": "hive-gpt-4o",
  "system_fingerprint": "fp_44709d6fcb",
  "choices": [{
    "index": 0,
    "delta": {"role": "assistant", "content": "Hello"},
    "logprobs": null,
    "finish_reason": null
  }],
  "usage": null
}
```

**Terminal usage chunk (when `stream_options.include_usage` is true):**
- `choices` is an empty array `[]`
- `usage` contains the full token counts for the entire request
- Followed by `data: [DONE]`

### POST /v1/completions (Legacy)

**Request (key fields):**
| Field | Type | Required | Notes |
|-------|------|----------|-------|
| `model` | string | Yes | Hive alias ID |
| `prompt` | string/array | Yes | Input text or token array |
| `stream` | boolean | No | Default false |
| `max_tokens` | integer | No | Output length limit (default 16) |
| `temperature` | number | No | 0-2 |
| `top_p` | number | No | 0-1 |
| `n` | integer | No | Number of completions |
| `stop` | string/array | No | Stop sequences |
| `suffix` | string | No | Text after completion |
| `echo` | boolean | No | Echo prompt in output |
| `logprobs` | integer | No | Number of logprobs to return |
| `seed` | integer | No | Deterministic sampling |
| `user` | string | No | End-user identifier |

**Response object (`text_completion`):**
```json
{
  "id": "cmpl-...",
  "object": "text_completion",
  "created": 1694268190,
  "model": "hive-gpt-3.5-turbo-instruct",
  "choices": [{
    "text": "This is a test",
    "index": 0,
    "logprobs": null,
    "finish_reason": "stop"
  }],
  "usage": {
    "prompt_tokens": 5,
    "completion_tokens": 7,
    "total_tokens": 12
  }
}
```

### POST /v1/responses

**Request (key fields):**
| Field | Type | Required | Notes |
|-------|------|----------|-------|
| `model` | string | Yes | Hive alias ID |
| `input` | string/array | Yes | String or array of message/item objects |
| `stream` | boolean | No | Default false; enables lifecycle event streaming |
| `instructions` | string | No | System-level instructions |
| `text` | object | No | `{"format": {"type": "json_schema", "name": "...", "schema": {...}, "strict": true}}` |
| `tools` | array | No | Tool definitions (different schema from chat/completions) |
| `tool_choice` | string/object | No | Tool selection strategy |
| `temperature` | number | No | 0-2 |
| `top_p` | number | No | 0-1 |
| `max_output_tokens` | integer | No | Output length limit |
| `reasoning` | object | No | `{"effort": "low"|"medium"|"high", "summary": "auto"|"concise"|"detailed"}` |
| `store` | boolean | No | Whether to store (Hive: always false per PRIV-01) |
| `user` | string | No | End-user identifier |
| `previous_response_id` | string | No | For conversation continuity |

**Response object:**
```json
{
  "id": "resp_...",
  "object": "response",
  "created_at": 1694268190,
  "model": "hive-gpt-4o",
  "status": "completed",
  "output": [{
    "type": "message",
    "id": "msg_...",
    "status": "completed",
    "role": "assistant",
    "content": [{
      "type": "output_text",
      "text": "Hello!",
      "annotations": []
    }]
  }],
  "usage": {
    "input_tokens": 9,
    "output_tokens": 12,
    "total_tokens": 21,
    "output_tokens_details": {
      "reasoning_tokens": 0
    },
    "input_tokens_details": {
      "cached_tokens": 0
    }
  },
  "text": {"format": {"type": "text"}},
  "reasoning": null,
  "metadata": {},
  "temperature": 1.0,
  "top_p": 1.0,
  "max_output_tokens": null,
  "truncation": "disabled",
  "tool_choice": "auto",
  "tools": [],
  "incomplete_details": null,
  "error": null
}
```

**Streaming events (SSE with named events):**
```
event: response.created
data: {"type":"response.created","response":{...partial response with status:"in_progress"...}}

event: response.output_item.added
data: {"type":"response.output_item.added","output_index":0,"item":{...}}

event: response.content_part.added
data: {"type":"response.content_part.added","output_index":0,"content_index":0,"part":{...}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"Hello"}

event: response.content_part.done
data: {"type":"response.content_part.done","output_index":0,"content_index":0,"part":{...}}

event: response.output_item.done
data: {"type":"response.output_item.done","output_index":0,"item":{...}}

event: response.completed
data: {"type":"response.completed","response":{...full response with status:"completed", usage:{...}...}}
```

### POST /v1/embeddings

**Request:**
| Field | Type | Required | Notes |
|-------|------|----------|-------|
| `model` | string | Yes | Hive embedding alias |
| `input` | string/array | Yes | Text string, array of strings, array of token integers, or array of token arrays |
| `encoding_format` | string | No | `float` (default) or `base64` |
| `dimensions` | integer | No | Output dimensions (text-embedding-3+ only) |
| `user` | string | No | End-user identifier |

**Response object:**
```json
{
  "object": "list",
  "data": [{
    "object": "embedding",
    "embedding": [0.0023, -0.0094, ...],
    "index": 0
  }],
  "model": "hive-text-embedding-3-small",
  "usage": {
    "prompt_tokens": 8,
    "total_tokens": 8
  }
}
```

## Reasoning Field Translation (API-04)

### Chat Completions Reasoning
- **Request:** `reasoning_effort` parameter (`low`, `medium`, `high`) -- only valid for reasoning-capable models.
- **Response:** `usage.completion_tokens_details.reasoning_tokens` contains the count. Reasoning content itself is NOT visible in chat completions responses (reasoning tokens are billed but hidden).

### Responses API Reasoning
- **Request:** `reasoning` object with `effort` and `summary` fields.
- **Response:** Reasoning items appear in the `output` array with `type: "reasoning"`, containing `encrypted_content` (opaque, for multi-turn continuity) and optionally a `summary` array.
- **Streaming:** Reasoning items generate their own lifecycle events (`response.reasoning_summary_text.delta`, etc.).

### Translation Strategy
1. For chat/completions: LiteLLM passes `reasoning_effort` through to supported providers. Hive normalizes the `completion_tokens_details` in the response.
2. For responses: Hive must translate between chat-completions-style LiteLLM output and the Responses API reasoning item format.
3. Hard-fail: If `reasoning_effort` or `reasoning` is set but the route's `SupportsReasoning` is false, return `400 invalid_request_error` with message indicating the model does not support reasoning.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Provider translation (OpenRouter, Groq, etc.) | Custom HTTP clients per provider | LiteLLM Proxy (already deployed) | 100+ provider translations, model mapping, retry logic already implemented. |
| SSE parsing | Regex-based line splitting | `bufio.Scanner` with `\n\n` delimiter awareness | Standard library handles edge cases (incomplete lines, buffering) correctly. |
| Request validation against OpenAI schema | Manual field-by-field checks | Validate against upstream `openapi.yaml` schemas during development; runtime checks for critical fields only | The upstream spec has 100+ fields; exhaustive runtime validation is wasteful but critical fields (model, messages/input/prompt) need checks. |
| UUID generation | Custom ID formats | `github.com/google/uuid` (already a dependency) | Consistent, RFC-compliant IDs for request tracking. |
| Provider-blind error sanitization | Ad-hoc string replacement | Extend existing `errors.WriteProviderBlindUpstreamError` | Already handles provider name scrubbing; extend with inference-specific error codes. |

**Key insight:** LiteLLM does the heavy lifting of provider translation. Hive's Phase 6 code is primarily a normalization and orchestration layer, NOT a provider adapter layer. The complexity is in SSE relay fidelity, Responses API event translation, and correct reservation lifecycle management -- not in talking to upstream providers.

## Common Pitfalls

### Pitfall 1: SSE Chunk Buffering Kills Streaming UX
**What goes wrong:** Upstream chunks arrive but the edge buffers them (due to Go's default `http.ResponseWriter` buffering) instead of flushing immediately. The client receives nothing until the entire response is complete.
**Why it happens:** Go's `net/http` response writer buffers by default. You must assert `http.Flusher` and call `Flush()` after every SSE data line.
**How to avoid:** Assert `http.Flusher` at handler start. Flush after every `data:` line and blank line. Set `Content-Type: text/event-stream`, `Cache-Control: no-cache`, `Connection: keep-alive`.
**Warning signs:** Streaming responses arrive all at once instead of incrementally.

### Pitfall 2: Missing Terminal Usage Chunk
**What goes wrong:** `stream_options.include_usage` is set but the final usage chunk is missing or has wrong structure, breaking official SDK token counting.
**Why it happens:** LiteLLM may or may not include the usage chunk depending on the upstream provider. The edge must ensure it always appears when requested.
**How to avoid:** Track accumulated tokens during streaming. If the upstream stream ends without a usage chunk but `include_usage` was requested, synthesize the terminal chunk with `choices: []` and the accumulated `usage` object before `data: [DONE]`.
**Warning signs:** SDK `response.usage` is null after streaming; token counts don't match non-streaming equivalent.

### Pitfall 3: Responses API Event Translation Drift
**What goes wrong:** The Responses API uses lifecycle events (`response.created`, `response.output_item.added`, etc.) that are structurally different from chat-completions chunks. Building a half-baked translation leads to SDK incompatibility.
**Why it happens:** LiteLLM does not natively produce Responses API event streams. The edge must translate chat-completions chunks into the full Responses lifecycle event sequence.
**How to avoid:** Build a state machine that tracks output items and content parts, emitting the correct lifecycle events in order. Test with the official OpenAI Python and JS SDKs using `client.responses.create(stream=True)`.
**Warning signs:** Official SDK streaming helpers throw parsing errors; events arrive out of order; `response.completed` event is missing.

### Pitfall 4: Reservation Leak on Stream Interruption
**What goes wrong:** Client disconnects mid-stream, the reservation is never finalized, and credits remain held indefinitely.
**Why it happens:** No cleanup handler on context cancellation during streaming.
**How to avoid:** Use `context.AfterFunc` or a `defer` that checks whether finalization occurred. On context cancellation, release or finalize with customer-favoring settlement (per Phase 3 decisions).
**Warning signs:** Account balances show persistent holds; reservations stuck in `active` status.

### Pitfall 5: Provider-Specific Fields Leaking in Responses
**What goes wrong:** LiteLLM includes provider-specific fields (e.g., `x_groq`, `openrouter_processing_ms`) in the response JSON that are not part of the OpenAI spec.
**Why it happens:** LiteLLM passes through some provider metadata.
**How to avoid:** Build response normalization that constructs the output object from known fields only, rather than passing through the LiteLLM response verbatim. Use allowlist, not blocklist.
**Warning signs:** SDK strict mode rejects responses; extra unknown fields appear in API output.

### Pitfall 6: Embeddings Dimension Mismatch
**What goes wrong:** Client requests `dimensions: 256` but the selected embedding model doesn't support custom dimensions. Silent truncation or provider error leaks through.
**Why it happens:** Not all embedding models support the `dimensions` parameter (only `text-embedding-3-*` and later).
**How to avoid:** Check `SupportsDimensions` capability on the selected route. Hard-fail with OpenAI-style error if unsupported. The capability matrix should track this per alias.
**Warning signs:** Embedding vectors have unexpected dimensionality; provider errors about unsupported parameters.

## Code Examples

### Edge Handler Registration (in main.go)
```go
// Phase 6 additions to apps/edge-api/cmd/server/main.go
inferenceHandler := inference.NewHandler(
    authorizer,
    inference.NewRoutingClient(resolveControlPlaneBaseURL()),
    inference.NewAccountingClient(resolveControlPlaneBaseURL()),
    inference.NewLiteLLMClient(resolveLiteLLMBaseURL()),
)

mux.Handle("/v1/chat/completions", inferenceHandler)
mux.Handle("/v1/completions", inferenceHandler)
mux.Handle("/v1/responses", inferenceHandler)
mux.Handle("/v1/embeddings", inferenceHandler)
```

### LiteLLM Dispatch (non-streaming)
```go
func (c *LiteLLMClient) ChatCompletion(ctx context.Context, route SelectionResult, body []byte) (*http.Response, error) {
    // Rewrite "model" field to use LiteLLM route handle
    rewritten := rewriteModel(body, route.LiteLLMModelName)

    req, err := http.NewRequestWithContext(ctx, http.MethodPost,
        c.baseURL+"/chat/completions", bytes.NewReader(rewritten))
    if err != nil { return nil, err }

    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("Authorization", "Bearer "+c.masterKey)

    return c.httpClient.Do(req)
}
```

### OpenAI Error Response Helpers
```go
func unsupportedParamError(param, model string) {
    code := "unsupported_parameter"
    apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error",
        fmt.Sprintf("The parameter '%s' is not supported with model '%s'.", param, model),
        &code)
}

func modelNotFoundError(model string) {
    code := "model_not_found"
    apierrors.WriteError(w, http.StatusNotFound, "invalid_request_error",
        fmt.Sprintf("The model '%s' does not exist or you do not have access to it.", model),
        &code)
}
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Chat Completions only | Responses API as primary recommended API | March 2025 | Hive must support both; Responses API has lifecycle events, not just delta chunks. |
| No streaming usage | `stream_options.include_usage` | May 2024 | Terminal chunk with `choices: []` and `usage: {...}` is expected by SDKs. |
| `max_tokens` only | `max_completion_tokens` (preferred) | Late 2024 | Both must be accepted; `max_completion_tokens` excludes reasoning tokens from the limit. |
| Reasoning tokens hidden | `completion_tokens_details.reasoning_tokens` in usage | Early 2025 | Billing must account for reasoning tokens separately. |
| `response_format: json_object` only | `response_format: json_schema` (structured outputs) | August 2024 | Strict schema enforcement; Responses API uses `text.format` instead. |
| Function calling (deprecated) | Tool calling | Late 2023 | Both must be supported; `functions` parameter is legacy but still accepted. |

**Deprecated/outdated:**
- `functions` and `function_call` parameters: Replaced by `tools` and `tool_choice`, but still accepted for backward compatibility. Hive should pass through both.

## LiteLLM Integration Details

### How LiteLLM Fits
LiteLLM runs as a Docker sidecar (`ghcr.io/berriai/litellm:main-stable`) with model groups keyed by private route handles (e.g., `route-openrouter-default`). The edge API sends requests to `http://litellm:4000/chat/completions` (or `/completions`, `/embeddings`) with the `model` field set to the route handle from `SelectionResult.LiteLLMModelName`.

### What LiteLLM Handles
- Provider authentication (OpenRouter API key, Groq API key)
- Request translation to provider-specific formats
- Response translation back to OpenAI-compatible format
- Streaming chunk relay from providers
- Basic retry/timeout behavior

### What Hive Must Own (NOT delegated to LiteLLM)
- Public request validation and OpenAI-compatible error responses
- Authorization (API key, budget, rate limiting)
- Route selection (capability-aware, alias-based)
- Reservation lifecycle (create, expand, finalize, release)
- Response normalization (provider-blind sanitization, field allowlisting)
- Responses API event translation (LiteLLM only knows chat-completions format)
- Usage extraction and accounting event recording
- Streaming terminal chunk synthesis
- Reasoning field normalization across endpoint families

### LiteLLM Base URL
```
LITELLM_BASE_URL=http://litellm:4000
```
Already available in docker-compose network. Edge API needs this as a new environment variable.

## Responses API Translation Layer

The most complex piece of Phase 6 is translating between LiteLLM's chat-completions output and the Responses API contract. The approach:

1. **Non-streaming:** Call LiteLLM `/chat/completions` with translated request (map `input` to `messages`, `instructions` to system message, `text.format` to `response_format`, `reasoning` to `reasoning_effort`). Transform the chat completion response into a Response object.

2. **Streaming:** Call LiteLLM `/chat/completions` with `stream=true, stream_options.include_usage=true`. Build a state machine that:
   - Emits `response.created` on first chunk
   - Emits `response.output_item.added` + `response.content_part.added` on first content delta
   - Translates `delta.content` chunks into `response.output_text.delta` events
   - Translates `delta.tool_calls` into function call argument delta events
   - Emits `response.content_part.done` + `response.output_item.done` on `finish_reason`
   - Emits `response.completed` with full response object (including usage) at end

3. **Tool calling translation:** The Responses API tool schema differs from chat/completions. Hive translates at the boundary: Responses API `tools[].type="function"` uses `name`, `description`, `parameters`, `strict` at top level (not nested under `function`).

## Open Questions

1. **LiteLLM Responses API support**
   - What we know: LiteLLM docs mention `/responses` endpoint support. Some providers may route through it natively.
   - What's unclear: Whether LiteLLM's `/responses` endpoint produces the full lifecycle event stream or just wraps chat/completions.
   - Recommendation: Test LiteLLM's `/responses` endpoint first. If it produces correct lifecycle events, use it directly and skip the translation layer for that endpoint. If not, use the chat-completions translation approach.

2. **Reasoning content from non-OpenAI providers**
   - What we know: OpenAI reasoning tokens are well-defined. OpenRouter may expose reasoning from other providers (Anthropic thinking, DeepSeek reasoning).
   - What's unclear: How LiteLLM normalizes reasoning content from non-OpenAI providers into the `completion_tokens_details.reasoning_tokens` field.
   - Recommendation: For Phase 6, support reasoning tokens in usage accounting and pass through `reasoning_effort` where supported. Do not attempt cross-provider reasoning content translation -- just report token counts accurately.

3. **`store` parameter on Responses API**
   - What we know: OpenAI defaults `store` to true for Responses API. Hive must not store content (PRIV-01).
   - What's unclear: Whether to hard-override `store=false` silently or accept the parameter and ignore it.
   - Recommendation: Accept the parameter but always behave as `store=false` internally. If user sends `store=true`, consider warning in response metadata but do not fail the request.

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go testing + vitest (SDK tests) |
| Config file | `apps/edge-api/cmd/server/main_test.go` (Go), `packages/sdk-tests/js/vitest.config.ts` (JS), `packages/sdk-tests/python/pyproject.toml` (Python) |
| Quick run command | `docker compose run --rm toolchain bash -c "cd apps/edge-api && go test ./..."` |
| Full suite command | `docker compose --profile test up --exit-code-from sdk-tests-js sdk-tests-js && docker compose --profile test up --exit-code-from sdk-tests-py sdk-tests-py` |

### Phase Requirements -> Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| API-01 | chat/completions returns valid response | integration | `docker compose --profile test run sdk-tests-js -- vitest run --grep "chat.completions"` | No -- Wave 0 |
| API-01 | completions returns valid response | integration | `docker compose --profile test run sdk-tests-js -- vitest run --grep "completions"` | No -- Wave 0 |
| API-01 | responses returns valid response | integration | `docker compose --profile test run sdk-tests-js -- vitest run --grep "responses"` | No -- Wave 0 |
| API-02 | streaming chat/completions SSE format | integration | `docker compose --profile test run sdk-tests-js -- vitest run --grep "streaming"` | No -- Wave 0 |
| API-02 | streaming terminal usage chunk present | integration | `docker compose --profile test run sdk-tests-js -- vitest run --grep "usage"` | No -- Wave 0 |
| API-03 | embeddings returns valid response | integration | `docker compose --profile test run sdk-tests-js -- vitest run --grep "embeddings"` | No -- Wave 0 |
| API-04 | reasoning params pass through on capable models | integration | Manual validation against live provider | Manual-only initially |
| API-04 | reasoning params hard-fail on incapable models | unit | `cd apps/edge-api && go test ./internal/inference/ -run TestReasoningCapabilityGate` | No -- Wave 0 |

### Sampling Rate
- **Per task commit:** `cd apps/edge-api && go test ./internal/inference/ -race`
- **Per wave merge:** Full SDK test suite (JS + Python + Java)
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `apps/edge-api/internal/inference/*_test.go` -- unit tests for request parsing, response normalization, capability gating, SSE relay
- [ ] `packages/sdk-tests/js/tests/chat-completions.test.ts` -- SDK integration tests for chat/completions (streaming + non-streaming)
- [ ] `packages/sdk-tests/js/tests/completions.test.ts` -- SDK integration tests for legacy completions
- [ ] `packages/sdk-tests/js/tests/responses.test.ts` -- SDK integration tests for Responses API
- [ ] `packages/sdk-tests/js/tests/embeddings.test.ts` -- SDK integration tests for embeddings
- [ ] `packages/sdk-tests/python/tests/test_chat_completions.py` -- Python SDK integration tests
- [ ] `packages/sdk-tests/python/tests/test_embeddings.py` -- Python SDK integration tests

## Sources

### Primary (HIGH confidence)
- `packages/openai-contract/upstream/openapi.yaml` -- Canonical OpenAI OpenAPI spec (downloaded 2026-03-28 from openai/openai-openapi)
- `packages/openai-contract/matrix/support-matrix.json` -- Phase 6 endpoint classification
- Existing codebase: `apps/edge-api/`, `apps/control-plane/internal/routing/`, `apps/control-plane/internal/accounting/`, `apps/control-plane/internal/usage/`

### Secondary (MEDIUM confidence)
- [OpenAI Chat Completions API Reference](https://platform.openai.com/docs/api-reference/chat/object) -- Response object schema
- [OpenAI Responses API Reference](https://platform.openai.com/docs/api-reference/responses/create) -- Request/response and streaming event schemas
- [OpenAI Embeddings API Reference](https://platform.openai.com/docs/api-reference/embeddings/create) -- Request/response schema
- [OpenAI Streaming Responses Guide](https://developers.openai.com/api/docs/guides/streaming-responses) -- SSE event format and lifecycle
- [OpenAI Reasoning Models Guide](https://developers.openai.com/api/docs/guides/reasoning) -- Reasoning tokens, effort parameter, summary
- [Chat Completions Streaming Events Reference](https://developers.openai.com/api/reference/resources/chat/subresources/completions/streaming-events) -- Terminal usage chunk format
- [LiteLLM Supported Endpoints](https://docs.litellm.ai/docs/supported_endpoints) -- Proxy endpoint coverage
- [LiteLLM Embeddings](https://docs.litellm.ai/docs/embedding/supported_embedding) -- Embedding endpoint support

### Tertiary (LOW confidence)
- [Responses API streaming community guide](https://community.openai.com/t/responses-api-streaming-the-simple-guide-to-events/1363122) -- Community interpretation of event ordering
- [OpenAI Responses API Migration Guide](https://developers.openai.com/api/docs/guides/migrate-to-responses) -- Differences between Chat Completions and Responses

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH -- all libraries are already in the project; no new dependencies needed
- Architecture: HIGH -- request lifecycle pattern well-understood from existing codebase patterns; edge proxy + control-plane + LiteLLM architecture is established
- OpenAI schema fidelity: HIGH -- upstream openapi.yaml is the canonical source and is already in the repo
- Responses API event translation: MEDIUM -- event format is documented but translation from chat-completions is non-trivial; LiteLLM native support level is uncertain
- Reasoning field handling: MEDIUM -- well-defined for OpenAI models but cross-provider behavior via LiteLLM needs runtime validation
- Pitfalls: HIGH -- streaming, reservation lifecycle, and provider-blind sanitization pitfalls are well-known from prior phases

**Research date:** 2026-04-02
**Valid until:** 2026-05-02 (30 days -- OpenAI API surface is stable; Responses API is relatively new but stabilizing)
