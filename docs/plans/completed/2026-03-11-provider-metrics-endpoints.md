## Goal
Expose provider-level latency, request, error, and health metrics through new public-safe and admin-protected API endpoints using an in-process open-source metrics library, while preserving existing provider routing and provider status security boundaries.

## Assumptions
- The first cut is API-only; no web changes are required.
- Metrics are provider-level only, not split by model or endpoint.
- `prom-client` will be added to `apps/api` as the metrics library.
- Metrics remain in-memory per API instance and reset on restart.
- Existing `/v1/providers/status` routes remain unchanged.
- Internal metrics routes use the existing `x-admin-token` protection model.

## Plan
1. Files: `apps/api/package.json`, `apps/api/src/providers/registry.ts`, `apps/api/test/providers/provider-registry.test.ts`
Change: Add `prom-client` to the API package, then add a failing provider-registry test that asserts successful and failed provider attempts update provider-level request, error, and latency metrics through a new metrics seam.
Verify: `pnpm --filter @hive/api test apps/api/test/providers/provider-registry.test.ts`

2. Files: `apps/api/src/providers/types.ts`, `apps/api/src/providers/registry.ts`, `apps/api/src/providers/provider-metrics.ts`, `apps/api/test/providers/provider-status.test.ts`
Change: Introduce a focused provider metrics module and types for provider-level summaries, histograms/counters, and health/circuit snapshots; wire `ProviderRegistry.chat()` and registry status access into that module without changing fallback behavior.
Verify: `pnpm --filter @hive/api test apps/api/test/providers/provider-registry.test.ts apps/api/test/providers/provider-status.test.ts`

3. Files: `apps/api/src/runtime/services.ts`, `apps/api/test/domain/runtime-services.test.ts`
Change: Expose metrics read methods from the runtime AI service so routes can request public summaries, internal summaries, and Prometheus-formatted output without reaching directly into low-level provider internals.
Verify: `pnpm --filter @hive/api test apps/api/test/domain/runtime-services.test.ts`

4. Files: `apps/api/src/routes/providers-metrics.ts`, `apps/api/src/routes/index.ts`, `apps/api/test/routes/providers-metrics-route.test.ts`
Change: Add `GET /v1/providers/metrics` with a sanitized provider-level JSON payload and tests that lock the public contract to safe fields only.
Verify: `pnpm --filter @hive/api test apps/api/test/routes/providers-metrics-route.test.ts`

5. Files: `apps/api/src/routes/providers-metrics.ts`, `apps/api/test/routes/providers-metrics-route.test.ts`, `apps/api/test/routes/rbac-settings-enforcement.test.ts`
Change: Add `GET /v1/providers/metrics/internal` and `GET /v1/providers/metrics/internal/prometheus` behind the admin token, and add tests for unauthorized `401` behavior plus internal-only diagnostic fields.
Verify: `pnpm --filter @hive/api test apps/api/test/routes/providers-metrics-route.test.ts apps/api/test/routes/rbac-settings-enforcement.test.ts`

6. Files: `docs/runbooks/active/provider-circuit-breaker.md`, `docs/architecture/system-architecture.md`, `CHANGELOG.md`
Change: Document the new metrics endpoints, clarify that metrics are in-memory and restart-scoped, and note the operator usage path for public-safe versus internal diagnostics.
Verify: `rg -n "/v1/providers/metrics|in-memory|prometheus" docs/runbooks/active/provider-circuit-breaker.md docs/architecture/system-architecture.md CHANGELOG.md`

7. Files: `apps/api/src/providers/provider-metrics.ts`, `apps/api/src/routes/providers-metrics.ts`, `apps/api/test/providers/provider-registry.test.ts`, `apps/api/test/routes/providers-metrics-route.test.ts`
Change: Run the targeted provider and route suites together and fix any contract drift or instrumentation edge cases found during combined verification.
Verify: `pnpm --filter @hive/api test apps/api/test/providers/provider-registry.test.ts apps/api/test/providers/provider-status.test.ts apps/api/test/domain/runtime-services.test.ts apps/api/test/routes/providers-metrics-route.test.ts apps/api/test/routes/rbac-settings-enforcement.test.ts`

8. Files: `apps/api/package.json`, `apps/api/src/providers/provider-metrics.ts`, `apps/api/src/providers/registry.ts`, `apps/api/src/routes/providers-metrics.ts`, `apps/api/src/runtime/services.ts`, `docs/runbooks/active/provider-circuit-breaker.md`, `docs/architecture/system-architecture.md`, `CHANGELOG.md`
Change: Run the full API verification required for an API feature after targeted checks pass.
Verify: `pnpm --filter @hive/api test && pnpm --filter @hive/api build`

## Risks & mitigations
- In-memory metrics reset on API restart: document this explicitly in runbooks and architecture notes.
- Health checks on every metrics read may add latency: keep metrics endpoints operator-oriented and add short-lived caching later only if needed.
- Prometheus histogram summaries may be awkward to expose in JSON: return simple derived aggregates publicly and reserve raw exposition for the internal Prometheus endpoint.
- Instrumentation can accidentally affect provider execution flow: keep metrics updates isolated and non-fatal, with tests covering fallback behavior.

## Rollback plan
- Remove the new provider metrics routes from route registration.
- Remove the provider metrics module and registry instrumentation hooks.
- Remove the `prom-client` dependency from `apps/api/package.json`.
- Re-run `pnpm --filter @hive/api test && pnpm --filter @hive/api build` to confirm the API returns to the prior provider-status-only behavior.
