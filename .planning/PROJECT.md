# Hive API Platform

## What This Is

Hive is a developer-focused, OpenAI-compatible AI gateway and billing platform. **v1.0 developer-api-core is a full Go rewrite of the prior implementation** (control-plane + edge-api in Go 1.24), undertaken for efficiency and operational control: lean hot-path latency, predictable memory, precise `math/big` FX, and full source-level control over routing, sanitization, and billing semantics that the prior stack could not guarantee. Routes requests to internally managed upstream providers such as OpenRouter, Groq, and future providers. Drop-in compatible with official OpenAI JavaScript/TypeScript, Python, and Java SDKs for chat/completions, responses, embeddings, images, audio, files, and batches. Hides upstream provider identity, enforces prepaid credit controls, and provides a developer console for billing, usage, tax/profile data, and API key management. **v1.0 shipped 2026-04-21.**

## Core Value

Developers can switch from OpenAI to Hive with only a base URL and API key change, while keeping predictable prepaid billing and provider-agnostic operations — backed by a native Go rewrite of the prior v1.0 stack for efficiency and full operational control.

## Current State

**Shipped:** v1.0 developer-api-core (2026-04-21). Phases 1–10, 49 plans, 580 commits.
**Next:** v1.1 — compliance cleanup, hot-path rate limiting, console integration, invoicing + budget integration. Scope in `.planning/v1.1-DEFERRED-SCOPE.md`.

## Requirements

### Validated (v1.0)

- ✓ **OpenAI contract fidelity** — Official JS/TS, Python, Java SDKs work against Hive with only base URL + API key change for the supported launch subset. Unsupported endpoints return OpenAI-style errors. Swagger/OpenAPI docs generated from support matrix. — v1.0 (Phase 1).
- ✓ **OpenAI-compatible text inference + streaming + reasoning** — chat/completions, completions, responses, embeddings with SSE streaming, terminal events, and reasoning-field normalization. — v1.0 (Phase 6).
- ✓ **OpenAI-compatible media + file workflows** — images generation/edits, audio speech/STT/translation, files, uploads, batches (failure-path settlement verified; success-path deferred to v1.1 pending upstream provider capability). — v1.0 (Phase 7 + Phase 10).
- ✓ **Provider abstraction** — Hive-owned aliases, capability matrix, fallback policy, cache-aware usage attribution, provider-blind errors at both edge and control-plane boundaries. — v1.0 (Phase 4 + Phase 10).
- ✓ **Money-safe prepaid credit ledger** — Immutable Postgres ledger, reservations before dispatch, finalize/refund for success/failure/cancel/retry/interrupted-stream paths. Per-key + per-model attribution (KEY-04 edge-level). — v1.0 (Phases 3, 5, 10).
- ✓ **Multi-rail BDT/USD checkout** — Stripe, bKash, SSLCommerz with reproducible FX snapshots, 3% conversion fee on BDT rails, BD VAT 15% tax math, `math/big` precision, payment-intent state machine. — v1.0 (Phase 8).
- ✓ **Developer console + observability** — Billing, invoices, API key management, analytics with Recharts, model catalog, Prometheus + Grafana + Alertmanager monitoring profile. — v1.0 (Phase 9).
- ✓ **Docker-only developer workflow** — Hot reload, code generation, builds, and tests run entirely in containers. No host-installed Go or Node required. — v1.0 (Phase 1).

### Active (v1.1 target)

- [ ] **Regulatory compliance on BD checkout** — Remove `amount_usd` and any FX-exposing field from BD-visible payment responses (Phase 11).
- [ ] **Formal verification of authentication + ledger + privacy requirements** — VERIFICATION.md for Phase 2 (AUTH-01..04) and Phase 3 (BILL-01, BILL-02, PRIV-01); live-verify analytics + monitoring (Phase 11).
- [ ] **Hot-path rate limiting** — Edge proxy enforces account-tier + per-key rate limits with 429 + Retry-After; close KEY-02 + KEY-05 (Phase 12).
- [ ] **Console integration fixes** — Web-console proxy routes for checkout modal + API key create/revoke/rotate; close BILL-03, BILL-07, CONS-01, CONS-02, KEY-01, KEY-03 (Phase 13).
- [ ] **Invoice-row + budget threshold integration** — Payment webhook inserts `payment_invoices` rows; budget thresholds enforced on spend/grant paths with real notifier; close BILL-05 + BILL-06 (Phase 14).
- [ ] **RBAC + verification-aware authorization model** — Replace the current `owner`/`member` plus ad hoc gate booleans with a reusable permission model that can express guest, unverified, member, owner, billing, and API-key access consistently across control-plane handlers and web-console routes.
- [ ] **Batch success-path terminal settlement** — Local batch executor in control-plane (fan-out `/v1/chat/completions`, compose output JSONL, settle from per-request usage). Unblocks API-07 success-path + KEY-04 success-path attribution (upstream OpenRouter/Groq have no native batch API).
- [ ] **`ensureCapabilityColumns` wrong-table fix** — Target `provider_capabilities` not `route_capabilities`. Latent since seed path populates columns; code fix removes dead path.

### Out of Scope

- ChatGPT-style end-user chat product — defer until API product is stable and validated.
- RAG projects/workspaces — defer until after developer API and billing foundation ship.
- Hosted code runner / dev environment — high-complexity future product area, not part of API launch.
- Subscription plans for launch — prepaid-only at launch, ledger primitives support subscriptions later.
- OpenAI org/admin management endpoints — not part of drop-in developer value proposition.
- Storing prompt or completion bodies by default — conflicts with launch privacy requirement.
- Customer-supplied upstream provider keys — Hive manages provider credentials internally.

## Context

v1.0 shipped with:

- **Codebase:** Go 1.24 control-plane + edge-api, Next.js 15 / React 19 / TS 5.8 web-console, 17 Supabase migrations, 580 commits over 58 days.
- **Infrastructure:** Docker Compose-only local stack (edge-api + control-plane + Redis + LiteLLM + web-console + monitoring profile); Supabase hosted Postgres + auth + object storage (buckets: `hive-files`, `hive-images`).
- **LLM routing:** LiteLLM proxy with OpenRouter + Groq upstreams configured; batch success-path blocked pending upstream support or local batch executor.
- **Payment rails:** Stripe, bKash, SSLCommerz — BDT anchored to XE USD/BDT FX + 3% conversion fee (note: REQUIREMENTS.md originally specified 5%; implementation landed on 3%).
- **Observability:** Prometheus metrics on both Go services (custom registries exclude Go runtime), Grafana dashboards across 4 signal categories, Alertmanager with 3 critical alerts.
- **Compatibility target:** Full public OpenAI surface except org/admin. Reasoning, streaming, usage metering, cache-aware token categories supported where upstream provides them.

**Known issues as of v1.0 ship (deferred v1.1):**

- Batch success-path not exercisable with current provider mix.
- `ensureCapabilityColumns` targets wrong table (latent).
- `amount_usd` leaks to BD checkout responses (regulatory).
- Phase 5 rate-limit work incomplete (lifecycle + KEY-04 shipped; full KEY-05 hot-path enforcement carried to Phase 12).

## Constraints

- **Compatibility**: Public behavior must track OpenAI API closely enough for drop-in official SDK use — streaming formats, errors, reasoning-related fields.
- **Privacy**: No storing request/response bodies at rest. Retain only operational metadata for billing, support, reliability.
- **Provider abstraction**: Public responses must not reveal upstream provider identity. Provider-blind sanitization enforced at edge + control-plane boundaries.
- **Commercial model**: Prepaid credits at launch; ledger + catalog structured for future subscription bundles resolving to credits.
- **Payments**: Stripe + bKash + SSLCommerz. BDT uses XE-backed FX snapshot + 3% fee. No FX rate or currency-exchange language visible to BD customers (regulatory).
- **FX precision**: `math/big` for all financial calculations — never float64.
- **Storage backend**: Supabase Storage (S3 protocol) only. `edge-api` and `control-plane` fail fast unless S3 env vars present and `hive-files` + `hive-images` buckets exist.
- **Performance**: Lean request-serving hot path, horizontally scalable. Prefer proven OSS components over custom code.
- **Auth & primary DB**: Hosted Supabase for auth, account identity, primary transactional Postgres in v1.
- **Developer workflow**: Entire local dev loop runs in Docker containers. No host-installed Go, Node, or database tooling.
- **Observability**: Capture health + rate-limit + billing + provider metrics without violating no-message-storage rule.

## Key Decisions

| Decision | Rationale | Outcome |
|----------|-----------|---------|
| Mirror full public OpenAI API surface except org/admin | Product promise is drop-in compatibility, not partial imitation | ✓ Good — SDK smoke tests for JS/Python/Java validate the contract |
| Prioritize official OpenAI SDK compatibility over custom SDK ergonomics | Existing SDK compatibility minimizes migration cost | ✓ Good — v1.0 ships with zero custom Hive SDK; users change only base URL + key |
| Hide upstream provider identity behind Hive model aliases | Provider abstraction core to customer-facing simplicity and routing flexibility | ✓ Good — provider-blind errors enforced; capability matrix lives internally |
| Launch with prepaid credits, no subscriptions | Simplifies initial revenue mechanics while preserving room for credit-based subscriptions | ✓ Good — ledger + reservation + attribution verified end-to-end |
| Exclude end-user chat, RAG projects, code execution from launch | Keeps first product focused on developer API, billing, control plane | ✓ Good — scope held; shipped on target |
| Avoid storing API prompts/completions at rest | Privacy + operational simplicity > transcript retention | ✓ Good — enforced in code; formal VERIFICATION.md deferred to Phase 11 |
| Hosted Supabase as auth + primary relational data + object storage in v1 | Managed Postgres + auth + S3 primitives with low ops overhead | ✓ Good — v1.0 shipped on single Supabase backend; no MinIO, no separate Postgres |
| Run entire local dev workflow in Docker | Prevents host toolchain drift, keeps onboarding + builds reproducible | ✓ Good — 580 commits delivered without host-installed Go or Node |
| `math/big` for all FX calculations | Prevent float64 corruption on financial math | ✓ Good — BDT rails ship without precision bugs |
| Never show FX rates or currency-exchange language to BD customers | Bangladesh regulatory requirement | ⚠️ Revisit v1.1 — `amount_usd` still leaks in BD checkout response (Phase 11) |
| Internal endpoints at `/internal/*` bypass auth middleware | Service-to-service calls avoid duplicating auth layer | ✓ Good — edge-to-control-plane calls work cleanly |
| `io.Pipe` zero-copy multipart forwarding + binary relay for media | No disk writes for TTS/STT/image passthrough | ✓ Good — shipped in Phase 7 |
| Defer formal Nyquist validation, treat live UAT as verification | Workflow preference; live UAT covers test-first discipline | — Ongoing — all 10 v1.0 VALIDATION.md files remain draft; may revisit for v1.1 |
| Local batch executor over upstream batch API dependency | OpenRouter + Groq have no batch API; LiteLLM managed upload only supports openai/azure/vertex_ai/manus/anthropic | — Pending v1.1 design |
| Phase 5 KEY-05 hot-path limiter deferred to Phase 12 | Lifecycle + KEY-04 attribution covers v1.0 integrator needs; hot-path Lua limiter needs dedicated hardening phase | ✓ Good — v1.0 scope held, Phase 12 owns closure |

---

*Last updated: 2026-04-21 after v1.0 milestone completion — developer-api-core shipped.*
