# Future Implementation Roadmap

This roadmap reflects Hive after the provider-hardening and OSS-governance wave that shipped through late February and early March 2026.

## Already Delivered Baseline

The following platform primitives are already in place and should no longer be treated as future roadmap work:

- provider routing with fallback
- provider timeout and retry controls
- provider circuit breaker
- public/internal provider status split
- public/internal provider metrics split
- startup provider model readiness checks
- payment reconciliation scheduler
- API key lifecycle management
- web auth -> chat -> billing smoke coverage
- GitHub issue templates, metadata sync, and maintainer lifecycle docs

## Phase 1 - Platform Positioning And Backlog Quality

Goals:
- clarify Hive as an inference platform rather than a narrow local gateway
- ensure docs, roadmap, and GitHub backlog describe the real product

Tasks:
1. Align top-level docs and architecture docs to the broader inference-platform narrative.
2. Retire or re-triage stale strategic issues and oversized implementation placeholders.
3. Create missing issues for genuine platform gaps with clean acceptance criteria.
4. Keep roadmap, runbooks, and changelog synchronized with backlog changes.

## Phase 2 - Provider Breadth And Provider Intelligence

Goals:
- make Hive meaningfully stronger as a multi-provider inference platform
- improve provider discovery and routing decisions

Tasks:
1. Decide the OpenRouter strategy cleanly:
   - metadata intelligence first
   - runtime provider later only if billing and governance stay controlled
2. Add a provider/model catalog layer with normalized metadata and provenance.
3. Expand provider breadth beyond the current Ollama/Groq baseline.
4. Add explicit provider cost-governance and routing-policy inputs.
5. Improve `/v1/models` to expose richer model capability and pricing context where appropriate.

## Phase 3 - Product Surface Expansion

Goals:
- close the most obvious gaps between current platform claims and user-visible capability

Tasks:
1. Replace the mock image pipeline with a real image provider integration.
2. Add file ingestion with parser abstraction and safe limits.
3. Expand usage analytics:
   - daily trend
   - model split
   - provider split
   - credit-spend visibility
4. Add support/admin tooling surfaces for debugging users, keys, and billing flows.
5. Improve developer-facing controls around keys, quotas, and environment clarity.

## Phase 4 - Billing, Access Tiers, And Commercial Controls

Goals:
- improve monetization flexibility without weakening billing correctness

Tasks:
1. Reframe and implement the free-tier / zero-cost access-control track.
2. Add refundable-balance decomposition and clearer credit accounting surfaces.
3. Add campaign/promo management with explicit lifecycle rules.
4. Add stronger abuse controls and tier-aware limits.
5. Add cost and margin visibility for provider-backed traffic.

## Phase 5 - Team And Operator Maturity

Goals:
- move from single-user/operator assumptions toward platform operations

Tasks:
1. Add organization/team entities and per-org budgets.
2. Add stronger admin role models and support permissions.
3. Add operator-facing deployment templates for staging and production.
4. Add SLOs and incident runbooks for provider, billing, and auth failures.
5. Add backup and restore guidance for Supabase/Postgres-dependent operations.

## Recommended Execution Order

1. Phase 1 immediately, because narrative and backlog quality now limit execution more than missing fundamentals do.
2. Phase 2 next, because provider breadth and provider intelligence most directly strengthen Hive's inference-platform identity.
3. Phase 3 and Phase 4 in parallel only after provider strategy is clearer.
4. Phase 5 continuously, with deeper operator maturity before any broader launch.

## Planning Notes

- Bangladesh-native payments should remain a strategic wedge and monetization advantage, not the sole framing for product direction.
- Avoid reopening broad provider integrations until metadata, billing, and acceptance criteria are disciplined enough to prevent scope sprawl.
- Prefer small, evidence-backed GitHub issues over umbrella issues that mix strategy, implementation detail, and speculative market claims.
