# Hive

## What This Is

Hive is an AI inference platform providing OpenAI-compatible API endpoints with multi-provider routing, credit-based billing, and a lightweight web chat workspace. It targets developers who want a drop-in OpenAI replacement with transparent provider routing, and end-users in Bangladesh who benefit from local payment rails (Bkash, SSLCommerz). The platform is API-first — the web workspace is a secondary product surface.

## Core Value

Developers can use Hive as a drop-in OpenAI-compatible API with transparent multi-provider routing and prepaid credit billing.

## History

### v0.1.0 (Released 2026-02-24)

Foundation release establishing the core inference platform:

- **API surface:** `/v1/chat/completions`, `/v1/responses`, `/v1/images/generations`, `/v1/models` — OpenAI-compatible request/response format
- **Provider ecosystem:** OpenRouter (primary), Groq (fast inference), OpenAI, Gemini, Anthropic — with circuit breaker, timeout/retry, and fallback chains
- **Web workspace:** Guest-first chat (no login for free models), model picker, developer panel for API keys, settings/billing dashboard
- **Billing:** Prepaid credits (1 BDT = 100 AI Credits), Bkash + SSLCommerz payment webhooks, refund policy (100 credits = 0.9 BDT within 30 days)
- **Auth:** Supabase Auth (email, OAuth, MFA), guest sessions via server-trusted cookie + internal token, API key auth for programmatic access
- **Infrastructure:** TypeScript monorepo (Fastify API + Next.js web), Supabase for persistence, self-hosted Langfuse v2 for observability, Docker Compose dev stack

### Post-v0.1.0 (March 2026)

- Chat history persistence across guest→authenticated sessions (#62)
- Provider-backed guest-free routing — real OpenRouter free models replace mock provider (#61)
- Guest-first home flow with model picker and auth gates (#58)
- Usage analytics and support snapshot (#56)
- Real image provider integration via OpenRouter (#53)
- Removed Ollama and mock providers — OpenRouter free is the baseline

### Current Direction

Two distinct API surfaces:
1. **Public API (`/v1/*`):** Strict OpenAI-compatible — the sellable product. Auth via Bearer token (API key). Full compliance with OpenAI's schema including telemetry (`usage` fields, streaming metadata).
2. **Web pipeline:** Proprietary routes for guest chat, sessions, billing, analytics. Deliberately non-OpenAI to prevent reverse engineering and unauthorized API inference through the web connection.

## Requirements

### Validated

- ✓ OpenAI-compatible `/v1/chat/completions` with streaming — v0.1.0
- ✓ OpenAI-compatible `/v1/responses` endpoint — v0.1.0
- ✓ OpenAI-compatible `/v1/images/generations` — v0.1.0
- ✓ OpenAI-compatible `/v1/models` listing — v0.1.0
- ✓ Multi-provider routing with circuit breaker and fallback — v0.1.0
- ✓ API key authentication (Bearer token + `x-api-key` header) — v0.1.0
- ✓ Credit-based billing with BDT conversion — v0.1.0
- ✓ Bkash + SSLCommerz payment webhooks — v0.1.0
- ✓ Guest-first web chat with free model access — post-v0.1.0
- ✓ Chat history persistence across guest→user link — post-v0.1.0
- ✓ Provider-backed free models via OpenRouter — post-v0.1.0
- ✓ Supabase Auth with email, OAuth, MFA — v0.1.0
- ✓ Self-hosted Langfuse observability — v0.1.0
- ✓ Provider health/metrics endpoints — v0.1.0

### Active

- [ ] Full OpenAI schema compliance for `/v1/chat/completions` (request/response fields, error format, usage telemetry)
- [ ] Full OpenAI schema compliance for `/v1/models` (object shape, permission fields)
- [ ] Full OpenAI schema compliance for `/v1/images/generations` (all parameters, response format)
- [ ] OpenAI-compatible auth model — canonical Bearer token behavior matching OpenAI SDK expectations
- [ ] Streaming telemetry — `usage` object in final streaming chunk per OpenAI spec
- [ ] OpenAI-compatible error responses (error object shape, status codes, error types)
- [ ] `/v1/embeddings` endpoint (routed to OpenRouter embedding models)
- [ ] OpenRouter metadata capture for token/cost tracking (generation metadata persistence)
- [ ] Separate authenticated web chat from public API pipeline (proprietary web routes)
- [ ] Provider/model catalog layer with normalized metadata
- [ ] Enhanced `/v1/models` with capability/pricing context

### Out of Scope

- `/v1/audio/*` (speech, transcription, translation) — no upstream provider support yet
- `/v1/files` + `/v1/uploads` — defer until file ingestion feature
- `/v1/batches` — defer until demand validated
- `/v1/moderations` — defer until content policy needed
- `/v1/completions` (legacy) — deprecated by OpenAI, not worth implementing
- `/v1/fine_tuning/*` — platform doesn't support fine-tuning
- `/v1/vector_stores` — no vector DB integration planned
- Realtime API (WebSocket) — defer to future milestone
- Web pipeline OpenAI compliance — deliberately proprietary to prevent abuse

## Context

- **Provider strategy:** OpenRouter is the primary routing layer for all providers. Ollama and mock providers have been removed. Free models on OpenRouter serve as the guest/basic tier.
- **Two-surface architecture:** The public API must be OpenAI-compatible for SDK drop-in. The web↔API pipeline is proprietary — different auth mechanism, different request/response shapes, different rate limiting. This prevents users from reverse-engineering the web connection to get free API access.
- **OpenAI schema reference:** Full OpenAPI spec stored at `docs/reference/openai-openapi.yml` for compliance validation.
- **Bangladesh market:** Local payment rails (Bkash, SSLCommerz) are a competitive wedge, not the entire product story. Credit system designed for decimal-safe BDT conversion.

## Constraints

- **Tech stack:** TypeScript monorepo, Fastify API, Next.js web, Supabase persistence — established, no changes planned
- **Provider dependency:** OpenRouter as primary routing layer — Hive's model catalog depends on OpenRouter's model availability
- **Billing:** Per-request credit consumption, no subscription billing yet
- **Deployment:** Docker Compose locally, separate API/web containers in production

## Key Decisions

| Decision | Rationale | Outcome |
|----------|-----------|---------|
| Remove Ollama + mock providers | OpenRouter free covers all use cases, reduces operational complexity | ✓ Good |
| OpenRouter as primary provider | Single integration point for 100+ models, free tier available | — Pending |
| Proprietary web pipeline | Prevent API abuse via web reverse-engineering | — Pending |
| Bearer token auth for public API | Match OpenAI SDK expectations, minimize switching friction | — Pending |
| Supabase for all persistence | Unified auth + data, reduces operational surface | ✓ Good |
| Credit-based billing (not subscription) | Simpler for pay-as-you-go, matches inference economics | ✓ Good |

## Planned Milestone: Web Frontend Revamp

**Goal:** Replace the custom `apps/web` Next.js frontend with an adopted or forked open-source LLM chat UI. Hive's API-first strategy (OpenAI compatibility) makes it a clean integration target for any OpenAI-compatible frontend.

**Status:** Evaluation in progress — see GitHub issue #72
**Blocks:** #49 (Web IA), #71 (Anonymous chat gate), #73 (Chat titles), #63/#64 (Guest proxy hardening)
**API-side work that can proceed independently:** #50 (/v1/users/settings endpoint), #71 API enforcement

Evaluation criteria: Supabase Auth integration, OpenAI-compatible API backend, credit/billing display, MIT/Apache 2.0 license, Next.js preferred.

---

## Current Milestone: OpenAI API Compliance (v1)

**Goal:** Transform Hive's `/v1/*` endpoints into a fully OpenAI-SDK-compatible API surface — a true drop-in replacement verifiable with the official `openai` npm SDK.

**Full roadmap:** `.planning/ROADMAP.md` | **Requirements:** `.planning/REQUIREMENTS.md`

| Phase | Name | Requirements | Status |
|-------|------|-------------|--------|
| 1 | Error Format Standardization | FOUND-01 | In planning |
| 2 | Type Infrastructure | FOUND-06, FOUND-07 | Not started |
| 3 | Auth Compliance | FOUND-02, FOUND-05 | Not started |
| 4 | Models Endpoint | FOUND-03, FOUND-04 | Not started |
| 5 | Chat Completions (Non-Streaming) | CHAT-01, CHAT-02, CHAT-03 | Not started |
| 6 | Chat Completions (Streaming) | CHAT-04, CHAT-05 | Not started |
| 7 | Surface Expansion | SURF-01, SURF-02, SURF-03 | Not started |
| 8 | Differentiators | DIFF-01, DIFF-02, DIFF-03, DIFF-04 | Not started |
| 9 | Operational Hardening | OPS-01, OPS-02 | Not started |

**Execution order:** 1 → 2 → 3 → 4 → 5 → 6 → 7 → 8 → 9 (see ROADMAP.md for dependency graph)

---
*Last updated: 2026-03-17 — current milestone and phase table added*
