# Issue #9 Startup Provider Model Readiness Checks Design

## Goal

Add startup provider model readiness checks that detect missing or unusable configured models without consuming chat tokens, while keeping the API available and relying on existing runtime fallback behavior.

## Context

- Issue `#9` targets provider availability hardening for public beta readiness.
- The API currently constructs provider clients and the `ProviderRegistry` in `apps/api/src/runtime/services.ts`, but it does not perform any boot-time verification that configured provider models are actually available.
- Provider status today is reachability-oriented: Ollama checks `/api/tags`, Groq checks `/models`, and internal status exposes provider diagnostic detail while the public status endpoint remains sanitized.
- Runtime request routing already supports fallback across providers, so startup verification should improve operator visibility without turning transient readiness issues into a full service outage.

## Decision

Implement zero-token, provider-specific model readiness checks inside each provider adapter and run them once during runtime service startup. Persist the resulting readiness snapshot in the `ProviderRegistry` so internal status can expose it after startup.

Startup readiness failures should never crash the API. Instead, startup logs should warn when an enabled provider is unreachable or its configured model is unavailable, and normal request-time fallback should continue to handle live traffic.

## Rejected Alternatives

### Fail startup when a primary provider is unready

This would make boot behavior brittle and turn partial provider degradation into a full API outage, which is misaligned with the current fallback-first runtime design.

### Put readiness logic in `ProviderRegistry`

This would avoid changing the provider interface, but it would leak provider-specific API knowledge out of the adapters and make additional providers harder to implement cleanly.

### Use a minimal chat request to verify readiness

This would prove end-to-end request execution, but it consumes hosted-provider tokens and violates the explicit requirement to avoid wasting tokens during readiness checks.

## Architecture

### Provider adapter contract

Extend the provider client interface with a zero-token readiness method that verifies whether a specific configured model is available for use.

- Ollama should inspect `/api/tags` and confirm the configured model name is present in the installed model list.
- Groq should inspect `/models` and confirm the configured model id is returned by the account-visible model catalog.
- Mock should report ready whenever enabled because it is the deterministic fallback provider.

### Startup execution

Run a one-time readiness sweep immediately after constructing the provider registry in `apps/api/src/runtime/services.ts`.

The sweep should:

- inspect the configured provider-to-model map
- skip disabled providers cleanly
- record a structured readiness result for each provider
- emit warnings for enabled-but-unready providers
- avoid mutating circuit-breaker state or request metrics

### Status behavior

Persist the latest startup readiness snapshot in the registry and surface it through internal provider status.

- Internal status detail should distinguish cases such as `startup model ready`, `startup model missing`, `startup unreachable`, and `disabled by config`.
- Public status must remain sanitized and must not expose model ids, raw diagnostics, or startup-specific operator detail.

## Scope Boundaries

In scope:

- zero-token startup verification for configured provider models
- persisted startup readiness snapshot in the provider registry
- internal status visibility and startup warning logs
- tests, docs, and changelog updates tied to the new behavior

Out of scope:

- blocking API startup on readiness failures
- periodic background rechecks after startup
- token-consuming end-to-end chat probes
- new public endpoints or UI for readiness detail

## Error Handling

The readiness implementation should differentiate:

1. provider disabled by configuration
2. provider unreachable during startup verification
3. provider reachable but configured model unavailable
4. unexpected readiness-check failure

These outcomes should be reflected in internal diagnostics and startup logs, but should not change public response contracts or fallback ordering.

## Testing Strategy

- Add provider-adapter tests for zero-token readiness parsing and failure cases.
- Add provider-registry tests for readiness snapshot persistence and internal-status detail enrichment.
- Add runtime-service tests for degraded startup behavior and warning logging when enabled providers fail readiness.
- Confirm public provider-status sanitization remains unchanged.

## Operational Impact

- Operators gain immediate startup warnings when configured models are missing or misconfigured.
- Internal provider status becomes more actionable without widening the public diagnostic surface.
- No new long-running scheduler or background worker is introduced.

## Verification

- Targeted provider and runtime tests for readiness behavior
- Full API test suite
- API build

If implementation remains API-only, no web build is required beyond policy checks for touched scopes.
