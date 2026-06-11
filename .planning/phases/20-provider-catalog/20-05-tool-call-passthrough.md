---
phase: 20-provider-catalog
plan: 05
type: execute
wave: 3
depends_on: [20-01, 20-02]
size: L
branch: b/phase-20-provider-catalog
milestone: v1.1
track: A
files_modified:
  - apps/edge-api/internal/inference/chat_completions.go
  - apps/edge-api/internal/inference/errors.go
  - apps/edge-api/internal/routing/selector.go
  - packages/openai-contract/support-matrix.md
  - packages/sdk-tests/tool_call_test.js        (new or existing test file)
autonomous: true
---

# Plan 20-05 — Capability-Based Tool-Call Passthrough (Issue #118)

## Objective

Allow `tools`, `tool_choice`, and `response_format` to pass through the edge-api to any model alias that has at least one tool-capable backing route (as indicated by `provider_capabilities`). Return 400 (`unsupported_parameter`) only when the alias has zero capable routes. This unblocks function-calling SDK use cases currently 400-rejected at the inference layer.

---

## Context (Verified Facts)

- **Rejection point:** `apps/edge-api/internal/inference/errors.go` `writeUnsupportedParamError`, called from the guard in `chat_completions.go`.
- **Capability columns:** `provider_capabilities` table with tool/vision flags added in Phase 16. These columns are already available.
- **Routing selection:** `routing.SelectionInput` carries `AllowedProviders` and `AllowedAliases` plus capability flags (Phase 10 baseline). The selector must be told to restrict to tool-capable routes.
- **Current behaviour:** Any request carrying `tools` or `tool_choice` hits `writeUnsupportedParamError` unconditionally.
- **Target behaviour (MVP gate):** Gate 400 on alias capability. If at least one route for the alias has `tools_supported = true` in `provider_capabilities`, pass through the parameter and constrain routing to capable routes only.

---

## Tasks

### Task 1: Extend SelectionInput with capability constraint

**File:** `apps/edge-api/internal/routing/selector.go`

Read the existing file before editing. Add to `SelectionInput`:

```go
// RequireToolCapable, when true, restricts route selection to routes where
// provider_capabilities.tools_supported = true.
RequireToolCapable bool
```

Update the route selection logic: when `RequireToolCapable` is true, filter `AllowedProviders` to those with `tools_supported = true` in `provider_capabilities`. If filtering produces an empty set, return a typed error `ErrNoCapableRoute` (new sentinel).

---

### Task 2: Capability check in chat_completions.go

**File:** `apps/edge-api/internal/inference/chat_completions.go`

Read the existing file before editing. Locate the guard that calls `writeUnsupportedParamError` for `tools`/`tool_choice`/`response_format`. Replace the unconditional rejection with:

```
1. Check whether the request carries tools/tool_choice/response_format.
2. If yes: query whether the alias has >= 1 tool-capable route.
   a. Build SelectionInput with RequireToolCapable=true.
   b. Call selector.Select(...).
   c. If selector returns ErrNoCapableRoute: call writeUnsupportedParamError (400) — same as today.
   d. If selector succeeds: proceed with the capable-route selection; pass tools/tool_choice/response_format downstream unchanged.
3. If no tools params: existing flow unchanged.
```

The capability query should reuse the existing DB/cache access pattern already present in `chat_completions.go` for routing. Do not add a new DB round-trip if the capability data is already loaded as part of route selection.

---

### Task 3: Update errors.go error message

**File:** `apps/edge-api/internal/inference/errors.go`

Read the existing file before editing. Update `writeUnsupportedParamError` to include the specific unsupported parameter name in the error response body so SDK callers know which field caused the rejection:

```json
{
  "error": {
    "message": "Model does not support parameter: tools. Choose an alias with tool-calling capability.",
    "type": "invalid_request_error",
    "code": "unsupported_parameter",
    "param": "tools"
  }
}
```

The `param` field is already part of the OpenAI error schema. Pass the parameter name from the call site.

---

### Task 4: Update openai-contract support matrix

**File:** `packages/openai-contract/support-matrix.md`

Add a row or update the existing `tools`/`tool_choice` row to document:

- Status: `conditional` (supported when alias has tool-capable route).
- Edge behaviour: passes through to LiteLLM; 400 returned only on incapable alias.
- Related: `provider_capabilities.tools_supported` column (Phase 16).

---

### Task 5: SDK integration tests

**File:** `packages/sdk-tests/tool_call_test.js` (create if absent)

Using the OpenAI JS SDK (already present in `packages/sdk-tests` per Phase 10 baseline):

Test case 1 (positive): Send a chat completion request with `tools` array to an alias known to have a tool-capable route. Assert HTTP 200 and a response with `finish_reason: "tool_calls"` or `"stop"` (provider-dependent). Mark as `skip` if no tool-capable provider key is configured in env (`SKIP_TOOL_TESTS=1`).

Test case 2 (negative): Send the same request to an alias explicitly configured with zero tool-capable routes. Assert HTTP 400 with `code: "unsupported_parameter"` and `param: "tools"`.

Test case 3: Send `response_format: {type: "json_object"}` to a tool-capable alias. Assert 200.

---

### Task 6: Routing unit tests

**File:** `apps/edge-api/internal/routing/selector_test.go` (existing file — extend)

Read the existing file before editing. Add:

1. `RequireToolCapable=true` with a provider that has `tools_supported=true` returns that provider.
2. `RequireToolCapable=true` with all providers having `tools_supported=false` returns `ErrNoCapableRoute`.
3. `RequireToolCapable=false` (default) with mixed capability returns any provider (existing behaviour preserved).

---

## TDD Notes

Start with the selector unit tests (pure logic, no I/O). Then write the `chat_completions.go` integration test using an in-process handler. SDK tests run last and require a live stack.

The `ErrNoCapableRoute` sentinel should be declared in `selector.go` and tested in isolation before wiring into `chat_completions.go`.

---

## Acceptance Criteria

- [ ] `SelectionInput.RequireToolCapable = true` restricts to `tools_supported = true` routes; returns `ErrNoCapableRoute` on empty result.
- [ ] Request with `tools` to a tool-capable alias returns 200; tools + tool_choice + response_format passed downstream unchanged.
- [ ] Request with `tools` to an incapable alias returns 400 with `code: "unsupported_parameter"` and `param: "tools"`.
- [ ] `writeUnsupportedParamError` includes `param` field in JSON body.
- [ ] `packages/openai-contract/support-matrix.md` updated with `conditional` status for tools/tool_choice/response_format.
- [ ] SDK test case 1 passes on a tool-capable alias (or marked skip if no provider key configured).
- [ ] SDK test case 2 (negative) passes unconditionally.
- [ ] Selector unit tests (3 cases) pass.
- [ ] `go vet ./apps/edge-api/...` clean.
- [ ] Existing `chat_completions.go` tests (no tools params) still pass — zero regressions.
