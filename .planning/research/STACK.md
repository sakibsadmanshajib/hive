# Stack Research

**Domain:** OpenAI-compatible multi-provider AI API and developer billing platform
**Researched:** 2026-03-28
**Confidence:** HIGH

## Recommended Stack

### Core Technologies

| Technology | Version | Purpose | Why Recommended |
|------------|---------|---------|-----------------|
| Go | 1.26.1 | Edge API, control plane, workers | Best delivery/performance balance for a custom gateway that must preserve streaming behavior, concurrency, and low overhead without hand-writing unsafe systems code. |
| LiteLLM Proxy | 1.81.3-stable | Provider translation, routing, fallback, spend hooks | Already translates many providers into OpenAI-shaped endpoints, supports OpenRouter, budgets, virtual keys, routing, and reduces the amount of provider-specific code Hive must own. |
| PostgreSQL | 18.3 | Source of truth for ledger, catalog, keys, billing state, payments, invoices | Strong transactional semantics are essential for prepaid credits, reconciliation, idempotency, and auditability. |
| Redis | 8.4 | Hot-path rate limiting, idempotency, reservation cache, ephemeral streaming state | Fast counters and TTL-backed state fit request gating and short-lived reservation workflows. |
| Supabase (self-hosted) | Current Docker Compose bundle | Authentication, user/session management, admin primitives | Fastest path to secure auth/account management with minimal custom code; official self-hosting guidance recommends Docker-based deployment. |
| Next.js | 16.1 | Developer console frontend | Current Active LTS web framework with React 19.2 support, mature auth integration patterns, and strong DX for billing and developer tooling. |

### Supporting Libraries

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `ogen` | 1.15.1 | Generate high-performance Go server/client types from OpenAPI | Use to generate the public contract layer from the OpenAI OpenAPI spec instead of hand-writing 100+ endpoint shapes. |
| `oapi-codegen` | 2.5.1 | Secondary OpenAPI codegen/fallback toolchain | Use when specific endpoints or overlays are easier to maintain with a simpler codegen path than `ogen`. |
| OpenAI OpenAPI spec | 2.3.0 | Canonical public contract baseline | Use as the compatibility source of truth for request/response schemas and regression tests. |
| OpenMeter | 1.0.0-beta.225 | Usage metering and entitlements | Optional for quotas, prepaid entitlements, and pricing enforcement once Hive outgrows simple in-house counters. Keep behind a feature flag because it is still beta. |
| ClickHouse | 25.12 | Optional high-cardinality usage analytics store | Add only after Postgres-backed reporting becomes a bottleneck for per-key/per-model/hourly analytics at scale. |
| Stripe Billing / Tax / Credits | Current public docs + previews | Checkout, invoices, tax monitoring, credit grants | Use for card payments, invoices, and tax workflows, but keep Hive's prepaid ledger authoritative because some credit features remain preview/subscription-oriented. |

### Development Tools

| Tool | Purpose | Notes |
|------|---------|-------|
| Docker Compose | Local orchestration | Matches Supabase and LiteLLM self-hosting patterns and keeps parity between local and production packaging. |
| Prometheus + Grafana | Health/latency/error metrics | Use for infra and service-level metrics; do not store prompt bodies. |
| OpenAI SDK compatibility harness | Regression testing | Use the official OpenAI SDKs plus OpenAI's gpt-oss verification guidance to catch API-shape drift early. |
| Playwright | Billing/admin UI smoke tests | Use for the developer console and payment/auth critical paths. |

## Installation

```bash
# Go toolchain
brew install go
# or download from https://go.dev/dl/

# Contract generation
go install github.com/ogen-go/ogen/cmd/ogen@v1.15.1
go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.5.1

# Provider adapter
docker pull ghcr.io/berriai/litellm:v1.81.3-stable

# Optional metering / analytics
docker pull ghcr.io/openmeterio/openmeter:1.0.0-beta.225
docker pull clickhouse/clickhouse-server:25.12
```

## Alternatives Considered

| Recommended | Alternative | When to Use Alternative |
|-------------|-------------|-------------------------|
| Go 1.26.1 edge/control plane | Rust 1.94.0 | Use Rust only if profiling proves the Go edge is the bottleneck or if a specific hot-path component warrants a dedicated Rust service. |
| LiteLLM as provider adapter | Hand-written provider adapters | Use custom adapters only for endpoints LiteLLM cannot cover cleanly or where exact compatibility requires a Hive-owned translation layer. |
| Internal Postgres credit ledger | Stripe Billing Credits as the sole ledger | Use Stripe credits only as a payment/billing convenience layer; do not make it the sole source of truth for prepaid balances. |
| Postgres first for analytics | ClickHouse from day one | Use ClickHouse once usage/event volumes or customer analytics queries make Postgres too expensive or slow. |
| Single-region modular platform with targeted split services | Immediate full microservice decomposition | Use more services only after traffic patterns prove where independent scaling pays for itself. |

## What NOT to Use

| Avoid | Why | Use Instead |
|-------|-----|-------------|
| Hand-writing the full OpenAI mirror endpoint by endpoint | Too much surface area, high drift risk, and expensive maintenance | Generate the contract from the OpenAI spec and overlay Hive-specific business logic. |
| A pure Python monolith for the hot request path | Faster to start, but weaker fit for low-latency streaming, concurrency control, and long-term cost discipline | Go edge + targeted Python/LiteLLM adapter service. |
| Exposing OpenRouter or Groq model IDs directly | Leaks provider identity and makes future routing changes customer-breaking | Public Hive aliases backed by an internal model catalog. |
| Building sales-tax logic only from hardcoded country tables | Jurisdiction rules and thresholds change often | Stripe Tax for supported payment flows plus a dedicated tax abstraction for non-Stripe rails. |

## Stack Patterns by Variant

**If request volume is still modest (<10M requests/day):**
- Keep usage analytics in Postgres with materialized views.
- Because operational simplicity is worth more than an extra analytics database early.

**If analytics queries become the expensive path:**
- Add ClickHouse as an append-only usage warehouse fed from the ledger/outbox.
- Because billing writes and customer analytics reads have very different performance profiles.

**If provider capability mismatch becomes the main blocker:**
- Keep LiteLLM behind a strict Hive compatibility filter and add Hive-owned shims per endpoint.
- Because OpenAI drop-in behavior matters more than provider-native convenience.

## Version Compatibility

| Package A | Compatible With | Notes |
|-----------|-----------------|-------|
| Go 1.26.1 | `ogen` 1.15.1 | `ogen` is an OpenAPI v3 Go code generator optimized for code-generated routing and validation. |
| Go 1.26.1 | `oapi-codegen` 2.5.1 | Good fallback when a simpler server/client generation path is easier to maintain for a subset of endpoints. |
| Next.js 16.1 | React 19.2 | Current official pairing in Next.js 16.x documentation and release notes. |
| PostgreSQL 18.3 | Supabase self-hosting | Supabase self-hosting is Docker-first; pin the exact upstream bundle during implementation and keep Hive migrations isolated. |

## Sources

- https://go.dev/dl/ — verified current stable Go version
- https://www.postgresql.org/ — verified current PostgreSQL major/minor release line
- https://redis.io/docs/latest/operate/oss_and_stack/install/version-mgmt/ — verified supported Redis Open Source versions
- https://supabase.com/docs/guides/self-hosting — verified Docker-based self-hosting recommendation
- https://nextjs.org/blog/next-16 and https://nextjs.org/docs/app/getting-started/upgrading — verified Next.js 16 / 16.1 status
- https://react.dev/versions and https://react.dev/blog/2025/10/01/react-19-2 — verified current React version
- https://docs.litellm.ai/ and https://github.com/BerriAI/litellm/releases — verified LiteLLM capabilities and current stable release
- https://github.com/ogen-go/ogen/releases and https://github.com/oapi-codegen/oapi-codegen/releases — verified codegen options and current release lines
- https://openmeter.io/docs/billing/entitlements/overview and https://github.com/openmeterio/openmeter/pkgs/container/helm-charts%2Fopenmeter — verified OpenMeter capability and current published beta image
- https://clickhouse.com/blog/clickhouse-release-25-12 — verified current ClickHouse release line
- https://developers.openai.com/api/reference/resources/responses/methods/create/ — verified Responses API contract and streaming semantics
- https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create/ — verified Chat Completions API contract and SSE behavior
- https://developers.openai.com/cookbook/articles/gpt-oss/verifying-implementations/ — verified official API-shape verification guidance

---
*Stack research for: OpenAI-compatible AI gateway and developer billing platform*
*Researched: 2026-03-28*
