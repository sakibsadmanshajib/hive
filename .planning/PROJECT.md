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

## Two Product Tiers

Hive operates two fundamentally different product surfaces with separate rate limits, separate analytics pipelines, and separate billing treatment.

### Tier 1: API (B2B / Developer)
**Who:** Developers building apps, hobbyists, tools like Claude Code / OpenCode / OpenClaw systems
**Interface:** `/v1/*` OpenAI-compatible endpoints, Bearer token auth, standard API keys
**Model:** Pay-per-token, prepaid credits, programmatic access
**Rate limits:** Separate from web — API clients get their own quota buckets
**Analytics:** Tracked separately from web usage — API business metrics are distinct from consumer metrics
**Commitment:** Drop-in OpenAI replacement. If it works with the OpenAI SDK, it works with Hive.

### Tier 2: Web (Consumer)
**Who:** Everyone else — individuals, students, professionals wanting a ChatGPT-like experience with more power and local accessibility
**Interface:** Web app (OSS frontend, see #72), WhatsApp, Messenger, phone/SMS
**Capabilities (target):**
- Text chat with all models (more model choice than ChatGPT)
- Image generation
- Video generation
- RAG / Projects (Retrieval-Augmented Generation over user documents, Recursive Language Model chains)
- Voice input and full voice conversation
- **Phone:** Register a phone number with your account → call or text Hive's number to chat (charged per call/SMS; Bangladesh rates)
- **WhatsApp:** Same registered phone number, lower charge than SMS/calls; supports text, voice messages, and video calls via WhatsApp
- **Messenger:** Facebook OAuth login → chat via Messenger; uses Facebook account identity
**Rate limits:** Separate from API — web consumers have their own quota, different throttles
**Analytics:** Separate pipeline from API analytics — consumer product metrics tracked independently
**Billing:** Per-credit (same credit system), but channel-specific pricing (SMS > WhatsApp > web)

## Context

- **Provider strategy:** OpenRouter is the primary routing layer for all providers. Ollama and mock providers have been removed. Free models on OpenRouter serve as the guest/basic tier.
- **Two-tier architecture:** API tier is OpenAI-compatible (strict), web tier is proprietary (prevents reverse-engineering). They share the same backend inference infrastructure but have independent rate limiting, analytics, and billing treatment.
- **OpenAI schema reference:** Full OpenAPI spec stored at `docs/reference/openai-openapi.yml` for compliance validation.
- **Bangladesh market:** Local payment rails (Bkash, SSLCommerz), phone/WhatsApp integration, and BDT credit conversion are first-class features — not afterthoughts. SMS/calls use local carrier rates; WhatsApp is cheaper due to internet-based delivery.

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
| BDT-denominated credit wallet (not USD) | Reduces user confusion, aligns with local payments, avoids presenting balance as foreign-currency account | — Pending |
| Separate logging posture: API vs web | Public API: minimal retention, no raw prompt storage by default. Web app: full conversation analytics, tool traces. Separates enterprise sales posture from product learning | — Pending |
| Two-front-door system architecture | Public OpenAI-compatible API + separate web app backend protocol feeding one internal core. Preserves commercial flexibility, separates privacy postures, reduces abuse risk | — Pending |
| API gateway first, web app second | Payment friction is the strongest validated pain point; backend billing/routing/abuse controls must stabilize before investing in richer consumer UX | — Pending |

## Planned Milestone: Web Frontend Revamp

**Goal:** Replace the custom `apps/web` Next.js frontend with an adopted or forked open-source LLM chat UI. Hive's API-first strategy (OpenAI compatibility) makes it a clean integration target for any OpenAI-compatible frontend.

**Status:** Evaluation in progress — see GitHub issue #72
**Blocks:** #49 (Web IA), #71 (Anonymous chat gate), #73 (Chat titles), #63/#64 (Guest proxy hardening)
**API-side work that can proceed independently:** #50 (/v1/users/settings endpoint), #71 API enforcement

Evaluation criteria: Supabase Auth integration, OpenAI-compatible API backend, credit/billing display, MIT/Apache 2.0 license, Next.js preferred.

## Planned Milestone: Payment & Finance Hardening

**Goal:** Harden the local payment rails, credit ledger, and billing operations to production-grade reliability — the operational foundation that makes the Bangladesh market thesis real.

**Depends on:** OpenAI API Compliance (v1) — billing engine and abuse controls must be stable first

**Scope:**
- bKash and SSLCommerz integration hardened to production (idempotency, reconciliation, webhook verification)
- Order ledger: payment success → wallet credit issuance as atomic operation with audit trail
- Tax invoice format for Bangladesh VAT compliance; reverse-charge treatment documented for imported SaaS spend
- Refund policy enforcement and credit expiry lifecycle
- Upstream vendor reconciliation and margin reporting (provider cost vs credit consumed)
- Customer support ops: escalation playbooks, admin wallet adjustment tooling, refund tooling
- Abuse controls: spend caps, org quotas, anomaly detection on credit consumption

**Key constraints:**
- BDT-denominated credit wallet (not stored USD) — confirmed decision
- Do not expose internal cost-plus-margin to end users; publish a clean tariff instead
- Any BD→foreign-company service flow must be documented for VAT review before scale

**Reference:** `docs/reference/2026-03-17-bangladesh-ai-gateway-strategy.md` §5

## Planned Milestone: Consumer Web Platform

**Goal:** Build out the Tier 2 consumer product — a full-featured AI assistant exceeding ChatGPT's capabilities, accessible via web, WhatsApp, Messenger, phone, and SMS.

**Depends on:** Web Frontend Revamp (need the base UI first)

**Architecture note (from Bangladesh strategy memo):** The web app must use a custom backend-for-frontend protocol — NOT the public OpenAI-compatible API shape. This enables separate analytics, separate abuse controls, project semantics, and wallet state management. Frontend must never be the source of truth for pricing, routing, or entitlement.

**Feature clusters (each will become its own phase/milestone):**

| Cluster | Features | Issues |
|---------|----------|--------|
| Separate tier limits | Independent rate limits + analytics for API vs Web | #74 |
| Multimedia generation | Video generation endpoint + UI | #75 |
| RAG / Projects | Document upload, project contexts, retrieval-augmented generation | #76 |
| Voice | Voice input, voice conversation (speech-to-speech) | #77 |
| Phone / SMS | Register phone number → call or text Hive to chat (Bangladesh carrier rates) | #78 |
| WhatsApp | Same registered number, text + voice + video calls, lower cost than SMS | #79 |
| Messenger | Facebook OAuth login, chat via Messenger | #80 |

**Channel billing model:**
- Web: standard credit rate
- WhatsApp: lower rate (internet delivery, cheaper in Bangladesh)
- SMS: separate charge (carrier-billed)
- Calls (voice/video): separate charge (carrier-billed)

**Regulatory rollout order:** API → web → messaging channels → voice notes → voice calling (BTRC licensing required for regulated telephony; text-based channels are a safer expansion path)

## Planned Milestone: Vertical Products & Efficiency

**Goal:** Add high-value, measurable Bangladesh-market workflow products and drive down cost of serving them.

**Depends on:** Consumer Web Platform (need stable web surface and validated user base first)

**Scope:**
- One or two vertical workflows with measurable ROI (e.g. business copilot / customer support tools)
- Bangla UX work: prompt defaults, message guidance, mixed-language and transliterated-Bangla support
- Benchmark self-hosted multilingual embedding model for Bangla/English/mixed inputs
- Batch-processing offers for business clients (non-real-time enterprise workloads)
- Caching and deduplication of repeated prompts/tool responses where safe
- Selective self-hosting of open models for embeddings, summarization, classification

**Key principle:** Add one or two high-value workflows with measurable ROI rather than a broad shallow feature list. Owned inference infrastructure follows validated demand — do not evaluate GPU/accelerator commitments before clear volume thresholds.

**Reference:** `docs/reference/2026-03-17-bangladesh-ai-gateway-strategy.md` §11–§12

---

## Current Milestone: OpenAI API Compliance (v1)

**Goal:** Transform Hive's `/v1/*` endpoints into a fully OpenAI-SDK-compatible API surface — a true drop-in replacement verifiable with the official `openai` npm SDK.

**Full roadmap:** `.planning/ROADMAP.md` | **Requirements:** `.planning/REQUIREMENTS.md`

| Phase | Name | Requirements | Status |
|-------|------|-------------|--------|
| 1 | Error Format Standardization | FOUND-01 | Complete |
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
*Last updated: 2026-03-17 — Phase 1 marked complete; added Payment & Finance Hardening and Vertical Products & Efficiency milestones; added key decisions from Bangladesh AI Gateway strategy memo*
