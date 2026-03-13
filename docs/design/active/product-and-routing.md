# Product and Routing Design

## Product Direction

The product is intentionally API-first with a lightweight web app:

- Primary audience: developers and teams integrating AI into apps
- Secondary audience: direct end users using chat UI
- Positioning: broader AI inference platform first
- Market wedge: Bangladesh-native payment rails, prepaid credits, and transparent local monetization
- Platform emphasis: compatibility, routing, billing controls, and operator visibility

## User Experience Principles

1. Compatibility-first
   - OpenAI-like endpoint contracts to minimize switching friction

2. Transparent charging
   - charge per request in credits
   - expose usage and balance clearly

3. Safe fallback behavior
   - when preferred provider fails, fallback to viable alternative before hard failure

4. Operational observability
   - provider health visibility for operators
   - public-safe status for users/integrators

## Pricing and Credit Design

- Base rate: `1 BDT = 100 AI Credits`
- Refund policy baseline: `100 AI Credits = 0.9 BDT`
- Campaign bonuses are allowed (promo multipliers)
- Promo credits are non-refundable

Rationale:
- keeps unit-economics tunable
- preserves compliance posture vs direct stored-cash semantics
- enables transparent and deterministic accounting

## API-Level Design

Core endpoints:
- `/v1/chat/completions`
- `/v1/responses`
- `/v1/images/generations`
- `/v1/models`

Billing/ops endpoints:
- `/v1/credits/balance`
- `/v1/usage`
- `/v1/payments/intents`
- `/v1/payments/webhook`
- `/v1/providers/status`
- `/v1/providers/status/internal`
- `/v1/providers/metrics`
- `/v1/providers/metrics/internal`
- `/v1/providers/metrics/internal/prometheus`

## Provider Strategy (Current)

Provider roles:
- Ollama: local/private baseline inference
- Groq: hosted high-speed inference
- Mock: deterministic fallback for continuity/testing

Default routing:
- `fast-chat`: Ollama preferred for cost/control
- `smart-reasoning`: Groq preferred for quality/performance

Fallback strategy:
- degrade gracefully on timeout, failure, or disabled provider
- preserve API continuity over hard downtime

## Public vs Internal Status Design

Public endpoint (`/v1/providers/status`):
- safe by default
- no provider internals or sensitive diagnostics

Internal endpoint (`/v1/providers/status/internal`):
- includes detailed health reason strings
- requires admin token

This split keeps observability high without leaking operational details.

## Known Gaps

1. Image route still mock-backed
2. No durable provider/model catalog or intelligence layer yet
3. No dynamic runtime provider config updates yet
4. Provider-specific quota/cost controls can be extended further
5. Analytics and admin tooling are still limited for a broader platform story
