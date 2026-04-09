# Hive API Platform

## What This Is

Hive API Platform is a developer-focused AI gateway and billing platform that exposes an OpenAI-compatible API surface while routing requests to internally managed upstream providers such as OpenRouter, Groq, and future providers. The product is meant to work as a drop-in backend for official OpenAI SDKs and the user's own agent startup, while hiding upstream provider identity, enforcing prepaid credit controls, and giving customers a web console for billing, usage, tax/profile data, and API key management.

## Core Value

Developers can switch from OpenAI to Hive with only a base URL and API key change, while keeping predictable prepaid billing and provider-agnostic operations.

## Requirements

### Validated

- [x] OpenAI-compatible text inference endpoints (chat/completions, completions, responses, embeddings) with streaming, usage metering, and capability-gated error handling. Validated in Phase 06: core-text-embeddings-api.

### Active

- [ ] Full public OpenAI API mirror except org/admin management endpoints, with drop-in behavior for official SDKs and streaming/error semantics.
- [ ] Internal provider abstraction that maps public model aliases to OpenRouter, Groq, and future providers without exposing provider identity to customers.
- [ ] Prepaid billing ledger with Hive Credits, rate limits, model pricing, itemized usage, spend controls, and future subscription-ready credit mechanics.
- [ ] Customer billing rails for Stripe, bKash, and SSLCommerz, including BDT top-ups anchored to USD/BDT FX plus a 3% conversion fee.
- [ ] Developer web console with hosted Supabase auth and Supabase-managed Postgres for billing, tax/business profile, invoices, spend alerts, usage analytics, API key lifecycle, and model catalog visibility.
- [ ] Account and API-key controls for budgets, expiration, allowed models, per-key usage tracking, and account-tier rate limiting.
- [ ] Privacy-first request handling that avoids storing API message bodies at rest while still capturing health, error, latency, and usage metrics.

### Out of Scope

- ChatGPT-style end-user chat product — defer until the API product is stable and validated.
- RAG projects/workspaces — defer until after the developer API and billing foundation ship.
- Hosted code runner / dev environment — high-complexity future product area, not part of API launch.
- Subscription plans for launch — keep launch commercial model prepaid-only, while designing ledger primitives that can support subscriptions later.

## Context

The compatibility target is broad: as of March 28, 2026, the current OpenAI API spec exposes a large public surface that spans `responses`, `chat/completions`, `completions`, `embeddings`, `images`, `audio`, `files`, `uploads`, `batches`, `vector_stores`, `fine_tuning`, `realtime`, `videos`, and related public product endpoints. The launch product should mirror the public surface except org/admin management endpoints, but it can phase delivery internally as long as official OpenAI SDK compatibility remains the product standard and unsupported gaps return OpenAI-style errors until implemented.

The commercial model is prepaid Hive Credits. One USD maps to 100,000 Hive Credits, and users can top up in increments of 1,000 credits. BDT pricing should derive from XE USD/BDT exchange data plus a 3% conversion fee. Pricing must support per-model catalogs, fiat cost references, payment-method surcharges where applicable, country-aware tax treatment, itemized usage, and detailed spend visibility by account, API key, model, and time window.

The platform must keep provider identity internal. Public model IDs should be Hive-controlled aliases mapped to upstream providers and models. Upstream credentials are managed internally, not by end users. The system should log health, usage, errors, and operational metrics, but should not store prompt or completion bodies at rest for the API product. Reasoning or "thinking" behavior must work where upstream providers support it, and OpenAI-style reasoning-related request/response semantics should be preserved as much as possible.

The launch scope includes a developer web app, not an end-user assistant product. Customers need hosted Supabase-based authentication and a console for credit purchases, invoices, VAT/business details, spend alerts, API key administration, model catalog browsing, and usage investigation. The initial managed backend should use the hosted Supabase project at `https://yimgflllgdsbcibnaxqe.supabase.co` for auth and the primary transactional Postgres database, rather than introducing a separate standalone PostgreSQL service in v1. First-party SDKs for JavaScript/TypeScript, Python, and Java are desirable, but the primary integration promise is that existing official OpenAI SDKs work unchanged except for base URL and credentials.

## Constraints

- **Compatibility**: Public behavior must track the OpenAI API closely enough for drop-in use with official SDKs, including streaming formats, errors, and reasoning-related fields.
- **Privacy**: Do not store request or response bodies from the API product at rest; retain only operational metadata needed for billing, support, and reliability.
- **Provider abstraction**: Public responses must not reveal upstream provider identity unless a future product requirement explicitly changes that policy.
- **Commercial model**: Launch with prepaid credits only, but structure the ledger and catalog for future subscription bundles that still resolve to credits.
- **Payments**: Support Stripe, bKash, and SSLCommerz; BDT support must reflect XE-backed FX conversion plus the platform fee.
- **Performance**: Keep the request-serving path lean and horizontally scalable; avoid unnecessary custom code when a proven OSS component covers the need.
- **Auth & primary DB**: Use the hosted Supabase project for authentication, account identity, and the primary transactional Postgres database in v1.
- **Developer workflow**: Local development, hot reload, code generation, builds, and tests must run inside Docker containers; contributors should not need host-installed Go, Node, or database tooling.
- **Observability**: Capture health, rate-limit, billing, and provider metrics without violating the no-message-storage rule.

## Key Decisions

| Decision | Rationale | Outcome |
|----------|-----------|---------|
| Mirror the full public OpenAI API surface except org/admin endpoints | The product promise is drop-in compatibility, not a partial imitation | — Pending |
| Prioritize official OpenAI SDK compatibility over custom SDK ergonomics | Existing SDK compatibility minimizes migration cost and accelerates launch | — Pending |
| Hide upstream provider identity behind internal Hive model aliases | Provider abstraction is core to customer-facing simplicity and future routing flexibility | — Pending |
| Launch with prepaid credits and no subscriptions | Simplifies initial revenue mechanics while preserving room for credit-based subscriptions later | — Pending |
| Exclude end-user chat, RAG projects, and code execution from launch | Keeps the first product focused on the developer API, billing, and control plane | — Pending |
| Avoid storing API prompts/completions at rest | Privacy and operational simplicity matter more than transcript retention for the launch API | — Pending |
| Use hosted Supabase as the auth and primary relational data platform in v1 | Keeps ops overhead low while still providing managed Postgres, auth, and storage primitives | — Pending |
| Run the entire local developer workflow in Docker containers | Prevents host toolchain drift and keeps onboarding and builds reproducible | — Pending |

---
*Last updated: 2026-04-09 after Phase 06 completion — text inference and embeddings API surface shipped*
