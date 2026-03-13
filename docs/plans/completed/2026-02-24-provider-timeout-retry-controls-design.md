# Provider Timeout and Retry Controls Design

**Issue:** [#2 Add provider timeout and retry controls with safe defaults](https://github.com/sakibsadmanshajib/hive/issues/2)

## Context

Provider calls currently use direct `fetch()` in `OllamaProviderClient` and `GroqProviderClient` without configurable timeout or retry behavior. If a provider stalls or returns transient upstream errors, requests can fail quickly or wait indefinitely depending on runtime behavior.

## Goals

- Add explicit timeout controls for provider HTTP calls.
- Add explicit retry controls with safe defaults.
- Keep existing provider routing and fallback behavior intact.
- Avoid leaking internal provider errors through public status endpoints.

## Non-Goals

- No circuit breaker behavior in this change.
- No provider routing map changes.
- No billing, ledger, or credit formula changes.

## Proposed Design

### 1) Environment-configured controls

Add provider transport settings in `apps/api/src/config/env.ts`:

- `PROVIDER_TIMEOUT_MS` (default `4000`)
- `PROVIDER_MAX_RETRIES` (default `1`)
- `OLLAMA_TIMEOUT_MS` (fallback to `PROVIDER_TIMEOUT_MS`)
- `OLLAMA_MAX_RETRIES` (fallback to `PROVIDER_MAX_RETRIES`)
- `GROQ_TIMEOUT_MS` (fallback to `PROVIDER_TIMEOUT_MS`)
- `GROQ_MAX_RETRIES` (fallback to `PROVIDER_MAX_RETRIES`)

These values are parsed as non-negative integers.

### 2) Shared provider HTTP request helper

Add a shared helper in `apps/api/src/providers/http-client.ts` that:

- wraps `fetch` with `AbortController` timeout
- retries on transient conditions only:
  - timeout/abort
  - network-level fetch errors
  - HTTP `429` and `5xx`
- does not retry on permanent/client errors (`4xx` except `429`)

The helper performs at most `1 + maxRetries` attempts.

### 3) Client integration

Update:

- `apps/api/src/providers/ollama-client.ts`
- `apps/api/src/providers/groq-client.ts`

Both clients will call the helper for `chat()` and `status()` requests, preserving their current response parsing and error messages as much as possible.

### 4) Runtime wiring

Update `apps/api/src/runtime/services.ts` to pass timeout/retry config from env into provider client constructors.

## Error Handling

- Final errors keep provider context (`ollama`/`groq`) and HTTP status where available.
- Public `/v1/providers/status` remains sanitized.
- Internal `/v1/providers/status/internal` remains admin-token protected and detailed.

## Testing Strategy

### Unit tests

- Add/extend provider tests to verify:
  - retries on transient failures and eventual success
  - no retry on non-retryable `4xx`
  - failure after retry limit exhaustion
- Extend env tests to verify defaults and override behavior for new settings.

### Regression checks

- Existing provider registry fallback tests remain unchanged and passing.
- Existing provider status route security tests remain unchanged and passing.

## Documentation Impact

Update:

- `README.md` environment variable section for new timeout/retry controls.
- operational docs if needed for provider behavior expectations.

