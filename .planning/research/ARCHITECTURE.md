# Architecture Research

**Domain:** OpenAI-compatible multi-provider AI API and developer control plane
**Researched:** 2026-03-28
**Confidence:** HIGH

## Standard Architecture

### System Overview

```text
┌──────────────────────────────────────────────────────────────────────────┐
│                            Client / SDK Layer                           │
├──────────────────────────────────────────────────────────────────────────┤
│  OpenAI JS SDK  OpenAI Python SDK  OpenAI Java SDK  Hive Web Console    │
└───────────────────────────────┬──────────────────────────────────────────┘
                                │
┌──────────────────────────────────────────────────────────────────────────┐
│                         Edge / Compatibility Layer                       │
├──────────────────────────────────────────────────────────────────────────┤
│  Go API Edge  │  Auth / Key Gate  │  Rate Limit  │  SSE / Stream Shaper │
└───────────────┴──────────────┬────┴───────┬──────┴──────────────┬───────┘
                               │            │                     │
┌──────────────────────────────▼────────────▼─────────────────────▼───────┐
│                         Control / Policy Layer                           │
├──────────────────────────────────────────────────────────────────────────┤
│  Model Catalog  │  Capability Matrix  │  Credit Reservation / Finalize  │
│  Payments / FX  │  Tax / Invoice Prep │  Usage Event Normalization       │
└───────────────┬───────────────────────────────────────────────┬──────────┘
                │                                               │
┌───────────────▼────────────────────────────┐  ┌───────────────▼──────────┐
│            Provider Adapter Layer          │  │      Async Worker Layer   │
├────────────────────────────────────────────┤  ├───────────────────────────┤
│ LiteLLM Proxy │ Hive compatibility shims   │  │ Reconciliation / alerts   │
│ OpenRouter    │ Groq │ future providers    │  │ webhooks / invoice jobs   │
└───────────────┬────────────────────────────┘  └───────────────┬──────────┘
                │                                               │
┌───────────────▼────────────────────────────┐  ┌───────────────▼──────────┐
│               Data Plane Stores            │  │      Observability Plane  │
├────────────────────────────────────────────┤  ├───────────────────────────┤
│ PostgreSQL │ Redis │ Object Storage        │  │ Prometheus │ Grafana      │
│ (Optional ClickHouse for analytics later)  │  │ structured metadata logs  │
└────────────────────────────────────────────┘  └───────────────────────────┘
```

### Component Responsibilities

| Component | Responsibility | Typical Implementation |
|-----------|----------------|------------------------|
| Go API Edge | Own the public OpenAI-compatible contract and streaming behavior | Generated handlers from OpenAPI + thin custom business logic |
| Control plane | Apply auth, model policy, pricing, credits, and key controls | Go services/modules backed by Postgres and Redis |
| Provider adapter | Translate internal model alias requests into upstream provider calls | LiteLLM plus Hive-owned shims where strict compatibility requires them |
| Payments service | Handle Stripe/bKash/SSLCommerz intents, callbacks, FX snapshots, and reconciliations | Background jobs + webhook endpoints + canonical payment model |
| Usage pipeline | Normalize usage into durable, queryable billing facts | Outbox/event workers writing to Postgres, with optional analytics sink |
| Web console | Customer-facing billing and developer management UI | Next.js app with Supabase auth |

## Recommended Project Structure

```text
platform/
├── apps/
│   ├── edge-api/            # Public OpenAI-compatible Go server
│   ├── control-plane/       # Billing, catalog, key, and payment APIs
│   ├── worker/              # Reconciliation, alerts, webhook processing
│   └── web-console/         # Next.js developer UI
├── packages/
│   ├── openai-contract/     # Imported OpenAI spec + overlays + generated types
│   ├── domain/              # Shared business types (ledger, catalog, usage)
│   ├── events/              # Usage/payment event schemas
│   └── sdk-tests/           # Drop-in compatibility regression suites
├── deploy/
│   ├── docker/              # Compose, dev images, LiteLLM config
│   └── k8s/                 # Future production manifests
├── docs/
│   ├── architecture/        # Design docs and provider matrices
│   └── runbooks/            # Billing, incidents, payments, migrations
└── tooling/
    ├── codegen/             # OpenAPI import / overlay / generation scripts
    └── migrations/          # DB and data backfill tooling
```

### Structure Rationale

- **`apps/edge-api/`:** keeps the hot request path isolated so it can scale independently from payment and admin traffic.
- **`apps/control-plane/`:** separates slower administrative and billing workflows from latency-sensitive inference requests.
- **`packages/openai-contract/`:** makes compatibility a first-class artifact instead of scattering schemas across handlers.
- **`packages/sdk-tests/`:** keeps SDK compatibility verification in the repo, not in tribal knowledge.

## Architectural Patterns

### Pattern 1: Contract-First Compatibility

**What:** Import the OpenAI OpenAPI spec, maintain Hive overlays for supported/unsupported behavior, and generate the public contract layer.
**When to use:** For every public endpoint Hive claims to support.
**Trade-offs:** More generation plumbing up front, much less long-term API drift.

**Example:**
```go
// Generated request/response types from the imported OpenAI contract
func (s *Server) CreateChatCompletion(ctx context.Context, req *api.CreateChatCompletionRequest) (*api.CreateChatCompletionResponse, error) {
    return s.chatService.Handle(ctx, req)
}
```

### Pattern 2: Reserve-Then-Finalize Ledger

**What:** Before forwarding billable requests, reserve a credit allowance; finalize exact usage after the upstream result is known; release or adjust on failure.
**When to use:** Every inference endpoint and long-running streamed request.
**Trade-offs:** Slightly more orchestration, much better protection against overdraft and reconciliation drift.

**Example:**
```go
reservation := ledger.Reserve(accountID, keyID, plannedCostCeiling)
stream, err := upstream.Start(req)
usage := collectFinalUsage(stream, err)
ledger.Finalize(reservation.ID, usage.ActualCostCredits)
```

### Pattern 3: Outbox-Driven Side Effects

**What:** Write durable ledger or payment state first, then emit side effects through an outbox to workers.
**When to use:** Alerts, invoices, tax evidence updates, analytics fan-out, webhook-driven reconciliation.
**Trade-offs:** More moving parts than inline side effects, but much safer for billing and payments.

## Data Flow

### Request Flow

```text
SDK / Client
    ↓
Go Edge
    ↓
Auth + Key Lookup + Rate Limit
    ↓
Credit Reservation + Model Alias Resolution
    ↓
LiteLLM / Hive Provider Shim
    ↓
Upstream Provider
    ↓
Stream / Response Normalization
    ↓
Usage Finalization + Event Outbox
    ↓
Client
```

### State Management

```text
PostgreSQL (authoritative state)
    ↓
Workers / APIs publish view models
    ↓
Redis caches hot balances, limits, idempotency keys
    ↓
Next.js queries control-plane APIs / React Query cache
```

### Key Data Flows

1. **Inference billing flow:** reserve credits before the upstream call, finalize on terminal usage, emit itemized usage events.
2. **Payment flow:** create canonical payment intent, capture FX/tax snapshot, accept webhook/callback, mint credits only after verified settlement.
3. **Catalog flow:** sync upstream model metadata and provider costs, map to stable Hive aliases and Hive Credit pricing.
4. **Support flow:** investigate issues from structured usage/error metadata without retaining prompt bodies.

## Scaling Considerations

| Scale | Architecture Adjustments |
|-------|--------------------------|
| 0-1k users | Run edge, control plane, workers, Postgres, Redis, LiteLLM in one region; no ClickHouse yet. |
| 1k-100k users | Split edge and worker deployments, add read replicas, isolate payment webhooks, and move analytics-heavy queries off the primary database. |
| 100k+ users | Add ClickHouse or equivalent analytics store, region-local edge pods, dedicated event transport, and finer-grained service boundaries around billing and provider routing. |

### Scaling Priorities

1. **First bottleneck:** request gating and streaming concurrency on the edge — fix with horizontal edge scaling and Redis-backed hot-path checks.
2. **Second bottleneck:** analytics/reporting queries hitting the billing store — fix by pushing normalized usage into an append-only analytics store.

## Anti-Patterns

### Anti-Pattern 1: Full Microservice Explosion on Day One

**What people do:** Split every concern into its own service before traffic exists.
**Why it's wrong:** Raises cost and coordination overhead before there is evidence that the split helps.
**Do this instead:** Start with a modular platform and only split the hot request path, workers, and web console where scaling/value is obvious.

### Anti-Pattern 2: Billing Writes Coupled to Best-Effort Network Calls

**What people do:** Debit customer balances inline without reservation/finalization or reconciliation.
**Why it's wrong:** Streaming interruptions, retries, and provider failures create drift and customer trust issues.
**Do this instead:** Use reservations, finalization, idempotency keys, and reconciliation workers.

## Integration Points

### External Services

| Service | Integration Pattern | Notes |
|---------|---------------------|-------|
| OpenAI spec/docs | Contract source | Import regularly and review diffs before claiming endpoint coverage. |
| LiteLLM | Internal adapter service | Treat as provider abstraction infrastructure, not the public business API. |
| OpenRouter | Upstream provider aggregation | Useful for model breadth and routing, but Hive must normalize differences and hide provider identity. |
| Groq | Upstream low-latency inference | Mostly OpenAI-compatible, but unsupported-field gaps must be filtered or translated. |
| Stripe | Card payments, invoicing, tax monitoring | Strong for global billing workflows; do not rely on preview credit features as the sole ledger. |
| bKash / SSLCommerz | Bangladesh-local payment rails | Keep behind one canonical payment abstraction to avoid business logic branching across gateways. |
| XE | FX reference source | Persist the exact rate and fee inputs used for each BDT credit purchase. |

### Internal Boundaries

| Boundary | Communication | Notes |
|----------|---------------|-------|
| edge-api ↔ control-plane | Internal API / shared domain library | Keep the hot path thin; do not drag admin concerns into the public request loop. |
| control-plane ↔ worker | Outbox + jobs | Needed for idempotent side effects and replayable billing flows. |
| edge-api ↔ LiteLLM | Internal HTTP/gRPC boundary | Keep a capability filter so unsupported provider fields never leak through. |

## Sources

- OpenAI API reference and OpenAPI spec
- LiteLLM official docs and releases
- Groq OpenAI compatibility docs
- OpenRouter API and provider-routing docs
- Stripe billing/tax docs
- Supabase self-hosting docs
- XE Currency Data API help docs

---
*Architecture research for: OpenAI-compatible AI gateway and developer billing platform*
*Researched: 2026-03-28*
