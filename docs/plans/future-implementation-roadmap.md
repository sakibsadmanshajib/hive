# Future Implementation Roadmap

This roadmap is the next-stage implementation guide after the current stable provider-testing baseline.

## Phase 1 - Provider Hardening (Immediate)

Goals:
- make Ollama/Groq behavior production predictable
- improve observability and failure diagnosis

Tasks:
0. Keep the existing provider adapter pattern as the canonical inference integration style (`ProviderClient` + concrete adapters + registry orchestration).
1. Add provider latency/error counters and expose aggregated metrics endpoint.
2. Add startup checks to verify configured provider models exist.
3. Add per-provider timeout/retry controls via env vars.
4. Add circuit-breaker behavior for repeated provider failures.
5. Add integration tests for real provider adapters using mock HTTP server contracts.

## Phase 2 - Data and Billing Maturity

Goals:
- strengthen financial correctness and auditability

Tasks:
1. Introduce migration tooling (Prisma/Drizzle/Kysely migrations) replacing ad hoc table bootstrap.
2. Add explicit ledger transaction table for reservation/capture/refund events.
3. Add campaign engine persistence and admin management endpoints.
4. Add refundable-balance endpoint with policy decomposition (purchased/promo/consumed/expired).
5. Add reconciliation scheduler and drift alerting.

## Phase 3 - Auth and Tenant Controls

Goals:
- move from dev-key model to operator-grade access control

Tasks:
1. Persist API keys with hash-at-rest and metadata.
2. Add key scopes, key rotation, key revocation audit trail.
3. Add organization/team entities and per-org budgets.
4. Add admin role model and policy enforcement middleware.
5. Add authenticated admin dashboard endpoints.

## Phase 4 - Product Surface Expansion

Goals:
- improve usability and market readiness

Tasks:
1. Replace mock image pipeline with actual image provider integration.
2. Add file ingestion pipeline with parser abstraction.
3. Add better usage analytics (daily trend, model split, provider split).
4. Add top-up/refund request UI with status timeline.
5. Add internal support tooling endpoints.

## Phase 5 - Release and Operations Readiness

Goals:
- make deployment and incident response operationally reliable

Tasks:
1. Add CI pipeline for lint, typecheck, test, build, and container build.
2. Add staging/prod environment templates.
3. Add SLOs and alerting (availability, latency, provider failure rate, payment drift).
4. Add backup and restore process for Postgres.
5. Add incident runbook playbooks for payment and provider outages.

## Recommended Execution Order

1. Phase 1 and 2 in parallel tracks (separate owners)
2. Phase 3 after baseline observability stabilizes
3. Phase 4 after billing and auth are durable
4. Phase 5 continuously, but final hardening before public launch
