# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed
- Temporarily short-circuited web lint/test/build in the monorepo CI workflow and disabled the dedicated web smoke workflow while the web overhaul is in progress; API checks remain active.

### Added
- **Real Local OpenAI SDK Verification Report:** Added a runbook-style report covering the 2026-03-22 Docker-local verification run with real Supabase auth, Hive API-key issuance, local payment funding, official OpenAI SDK requests, and persisted billing evidence.
- **Persisted Chat History:** Guest and authenticated chat conversations are now stored server-side and survive reloads and devices.
  - New Supabase tables: `chat_sessions` and `chat_messages` with ownership for both user and guest; guest sessions are claimed for the user on guest-link.
  - API: authenticated `GET/POST /v1/chat/sessions` and `GET /v1/chat/sessions/:id`, `POST /v1/chat/sessions/:id/messages`; internal guest session endpoints under `WEB_INTERNAL_GUEST_TOKEN` for list/create/get/send and claim-on-link in `/v1/internal/guest/link`.
  - Web: same-origin proxy routes at `/api/chat/guest/sessions` and `/api/chat/guest/sessions/[id]` and `.../messages`; sidebar and transcript hydrate from server and new messages are sent through the session API.
- **Provider-Agnostic Zero-Cost Chat Routing:** Added an internal provider-offer catalog so public models can stay stable while routing across provider-specific offers.
    - `guest-free` is now a provider-backed zero-cost chat model instead of a mock-only path.
    - Zero-cost offers can come from Ollama, OpenRouter, OpenAI, Groq, Gemini, Anthropic, or future providers without changing the public model id.
    - Zero-cost routing now fails closed instead of silently falling through to paid providers.
- **Hosted Chat Provider Readiness:** Added first-class OpenRouter, Gemini, and Anthropic provider wiring alongside the existing OpenAI and Groq paths.
    - OpenRouter, OpenAI, Groq, and Gemini share an internal OpenAI-compatible chat transport.
    - Anthropic uses a native Messages adapter internally while the public API remains OpenAI-compatible.
    - Provider status and startup readiness now cover the expanded hosted-provider set.
- **Traffic Analytics Channel Split:** Usage reporting now distinguishes the OpenAI-compatible API business from the web chat business.
    - `/v1/usage` now includes channel breakdowns (`api` vs `web`) and per-key attribution buckets where applicable.
    - Added admin-only `GET /v1/analytics/internal` for channel, API-key, guest-session, and conversion reporting.
    - This change separates analytics/reporting first; the deeper authenticated web runtime split from the public API path remains tracked separately in GitHub issue `#57`.
- **Guest-First Web Chat Path:** Added a guest chat path for the web app via a Next.js server-side route and an internal token-protected API endpoint, while keeping `/v1/chat/completions` authenticated for public API clients.
    - The web/app-to-API handoff now requires a server-only `WEB_INTERNAL_GUEST_TOKEN`; there is no built-in default token.
    - The web proxy now rejects non-same-origin requests and forwards the client IP to preserve guest rate limiting through the proxy boundary.
- **Guest Session Attribution:** Added durable guest-session and conversion-attribution plumbing across the web and API runtimes.
    - The web app now issues a signed `httpOnly` guest cookie plus a mirrored browser-visible guest session object through `/api/guest-session`.
    - Guest attribution is persisted in dedicated Supabase tables: `guest_sessions`, `guest_usage_events`, and `guest_user_links`.
    - Authenticated Supabase sessions now link the validated `guestId` to the user through an internal handoff so later signup/payment conversion analysis can join anonymous usage to authenticated activity.
- **Usage Analytics and Support Snapshot:** Enriched `/v1/usage` with windowed request/credit analytics and added admin-only `GET /v1/support/users/{userId}` for single-user troubleshooting.
    - Usage summaries now include daily trend, model split, and endpoint split.
    - Developer Panel now shows the usage analytics summary instead of only a raw event count.
    - Support snapshot remains protected by `x-admin-token` and preserves the existing public/internal diagnostics boundary.
- **Real Image Provider Path:** Added an OpenAI-backed adapter for `/v1/images/generations` with provider-registry routing, startup readiness checks, and OpenAI-shaped request/response handling.
- **Inference API Key Bearer Compatibility:** Inference routes now accept `Authorization: Bearer <api-key>` in addition to `x-api-key`, improving drop-in compatibility with OpenAI-style clients and SDKs.
- **Platform Audit Refresh:** Added `docs/audits/2026-03-13-platform-audit.md` to capture the current implementation baseline, backlog drift, and next-step platform priorities.
- **Maintainer Issue Lifecycle Runbook:** Added a dedicated runbook for issue intake, triage state transitions, planning expectations, PR linkage, verification evidence, and closeout workflow.
- **API Key Lifecycle Management:** Added stable API key ids, nicknames, optional expiration, revoke-by-id management, and immutable lifecycle audit events for create/revoke/expiry visibility.
    - Session-authenticated management routes: `/v1/users/me`, `/v1/users/api-keys`, and `/v1/users/api-keys/{id}/revoke`.
    - Developer Panel now shows managed API keys, one-time raw key reveal on creation, and recent lifecycle activity.
- **Repository Audit Artifacts:** Added repo-audit design, decision-process, and execution-plan documents to track cleanup work and implementation parity.
- **OSS Governance Policy Set:** Added root contributor and governance documents: `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `SECURITY.md`, `SUPPORT.md`, `GOVERNANCE.md`, and `.github/CODEOWNERS`.
- **GitHub Contribution Intake and Metadata Sync:** Added GitHub Issue Forms, a structured PR template, declarative label/milestone metadata under `tools/github/`, and a maintainer triage runbook for repository issue operations.
- **Payment Reconciliation Scheduler:** Added recent billing drift detection plus an opt-in API-side scheduler for payment reconciliation alerts.
    - Configurable enablement, interval, and lookback window via `PAYMENT_RECONCILIATION_*` env vars.
    - Drift detection covers missing credited intents, missing verified events, credited amount mismatches, and missing payment ledger evidence.
    - Clean intervals stay silent; logs are emitted only for drift or reconciliation failures.
- **Provider Circuit Breaker:** Implemented a circuit breaker pattern for AI providers (Ollama, Groq) to handle repeated failures gracefully.
    - Configurable thresholds (`PROVIDER_CB_THRESHOLD`) and reset timeouts (`PROVIDER_CB_RESET_MS`).
    - Exposed circuit state in `/v1/providers/status` (public) and detailed diagnostics in `/v1/providers/status/internal` (admin-only).
- **Provider Metrics Endpoints:** Added provider-level metrics for request volume, errors, latency, and health snapshots.
    - Public-safe JSON at `/v1/providers/metrics`.
    - Admin-only JSON at `/v1/providers/metrics/internal`.
    - Admin-only Prometheus exposition at `/v1/providers/metrics/internal/prometheus`.
    - Metrics are in-memory per API instance and reset on restart.
- **Provider Reliability Controls:** Added explicit timeout and retry logic for provider HTTP requests.
    - Environment variables for global and per-provider timeouts (`PROVIDER_TIMEOUT_MS`, `OLLAMA_TIMEOUT_MS`, etc.).
    - Smart retries for transient errors (network, 429, 5xx).
- **Startup Provider Model Readiness Checks:** Added zero-token startup verification for configured Ollama and Groq models.
    - Reuses provider metadata endpoints instead of chat probes, so readiness checks do not consume request tokens.
    - Persists startup readiness detail into the internal provider status surface while keeping the public status endpoint sanitized.
    - Logs warnings for enabled-but-unready providers without blocking API startup.
- **Web E2E Smoke Tests:** Added Playwright end-to-end tests for the critical Auth -> Chat -> Billing flow.
- **CI/CD Optimizations:**
    - "Smart" smoke workflow that reuses Playwright binaries and pnpm cache.
    - Cost-optimized CI quality gates that only run relevant checks based on changed scopes (API vs. Web).
    - Automated PR cleanup workflow.

### Removed
- **Earlier Incomplete OpenRouter Spike:** Removed the earlier half-wired OpenRouter/CostCalculator experiment that did not fit the final billing model.
- **Legacy Python MVP:** Removed deprecated root `app/` and `tests/` Python implementation paths after documenting TypeScript migration mapping.
- **Duplicate OpenAPI Contract:** Removed `openapi/openapi.yaml`; `packages/openapi/openapi.yaml` is now the sole in-repo OpenAPI source.

### Changed
- **Public Model Routing:** Chat model selection now routes public virtual model ids through internal provider offers instead of binding public models directly to concrete providers.
- **Provider Configuration Surface:** `.env.example` and runtime env parsing now include OpenRouter, OpenAI chat, Groq free-model, Gemini, and Anthropic configuration for provider-backed chat.
- **Chat Home Access Model:** `/` now renders guest chat by default instead of redirecting unauthenticated users to `/auth`. Guest users are limited to free models and are prompted to sign in for paid capabilities.
- **Guest Conversion UX On `/`:** Paid chat models now remain visible to guests as locked options instead of disappearing from the picker.
    - Choosing a locked paid model opens a dismissible combined auth modal directly on `/`.
    - Dismissing the modal preserves the active guest conversation and free-model flow.
    - Successful modal auth unlocks paid models in place without navigating away from `/`.
    - This completes issue `#19`'s guest-home conversion UX while leaving the deeper authenticated web runtime split tracked separately in GitHub issue `#57`.
- **Guest Web Runtime:** Guest chat bootstrap now requires a durable guest session before the first anonymous chat request, and guest usage is recorded under `guestId` rather than a synthetic user id.
- **Model Catalog Metadata:** API model responses now include `capability` and `costType`, and the underlying model catalog now carries structured pricing metadata in addition to fixed per-request credits.
- **Image Routing:** `image-basic` now routes to the hosted OpenAI image adapter with `mock` fallback instead of returning a placeholder-only mock image URL.
- **OpenAPI Image Contract:** Expanded `/v1/images/generations` in `packages/openapi/openapi.yaml` to document model, size, count, response format, and bearer/api-key auth compatibility.
- **Product Positioning Docs:** Reframed top-level product and architecture docs around Hive as a broader AI inference platform, with Bangladesh-native payments positioned as one strategic wedge rather than the entire product definition.
- **Future Roadmap:** Rewrote the active roadmap so already-shipped hardening work is treated as delivered baseline and remaining work is organized around provider breadth, analytics, commercial controls, and operator maturity.
- **Docker Documentation:** Clarified why Docker Compose is used locally, why `api` and `web` are separate containers, and how that differs from running `pnpm dev` directly.
- **Local Auth Bootstrap Docs:** Clarified that local Supabase generates real `ANON_KEY` and `SERVICE_ROLE_KEY` values, and those keys must be copied into `.env` before starting Docker `api` and `web`.
- **Local Development Workflow:** Standardized contributor onboarding around `pnpm stack:dev` as the canonical full-stack hot-reload entry point, and clarified that Supabase runs as Docker containers under the Supabase CLI rather than inside Hive's Compose file.
- **Bootstrap Workflow:** Split first-time local setup into `pnpm bootstrap:local` and kept `pnpm stack:dev` as the daily startup command; bootstrap now provisions the default local Ollama model as well as Supabase schema state.
- **PR Hygiene Docs:** Tightened guidance so pull request titles are expected to be scoped and Conventional-Commit-style where practical.
- **Web Architecture:** Evolved from a guarded home to a guest-first chat home.
    - `/` is now the primary chat interface for both guests and authenticated users.
    - Unauthenticated users stay on `/` in guest mode and use the separate web guest chat route.
- **Documentation:** Major restructuring of `AGENTS.md` to include the "Superpowers" workflow and stricter operational mandates.
- **Git Workflow Policy:** Worktrees are now optional in this repository; they remain available for isolation but are no longer required by default.
- **Browser Runtime Configuration:** Web runtime now requires explicit `NEXT_PUBLIC_API_BASE_URL`, `NEXT_PUBLIC_SUPABASE_URL`, and `NEXT_PUBLIC_SUPABASE_ANON_KEY` instead of implicit localhost or placeholder fallbacks.
- **Web Smoke Auth Fixtures:** Playwright smoke auth now prefers real Supabase signup using `E2E_SUPABASE_ANON_KEY` and only uses synthetic token fallback when `E2E_ALLOW_DEV_TOKEN_FALLBACK=true` is explicitly enabled.
- **API Key Metadata:** Persistent API key records now expose only `keyPrefix` metadata instead of a plaintext-looking `key` field.

### Fixed
- **Local Verification Embeddings Routing:** Added a dev-only `OPENROUTER_FREE_EMBEDDING_MODEL` path so Docker-local verification can expose `nvidia/llama-nemotron-embed-vl-1b-v2:free` without changing base/prod defaults, and embeddings now route unmapped model ids to the requested provider model instead of falling back to `openrouter/auto`.
- **`guest-free` Billing Regression:** Authenticated requests to `guest-free` now bypass credit-consumption RPCs instead of failing with `failed to consume credits via rpc: invalid credits amount`.
- **`guest-free` Mock Echo Regression:** Guest and authenticated `guest-free` requests now use the provider-backed zero-cost routing path instead of returning the mock `MVP response` echo.
- **Guest/Auth Persistence Foreign Keys:** Session-authenticated guest linking and authenticated `guest-free` usage writes now ensure the local `user_profiles` row exists before persistence, preventing `guest_user_links_user_id_fkey` and `usage_events_user_id_fkey` `500` failures on the Docker-local stack.
- **Guest Chrome Hydration:** Guest mode on `/` no longer renders authenticated profile chrome such as `Unknown user` and `Log out` during initial auth-session hydration.
- **Guest Model Fail-Closed Behavior:** Guest chat now disables sending when the public model catalog fails or returns only paid chat models, instead of inventing a free model or selecting a locked paid option.
- **Domain Image Model Selection:** The domain image-generation path now honors an explicit requested image model id instead of always billing and logging against the default image model.
- **Paid Inference Billing Guardrails:** `/v1/chat/completions`, `/v1/responses`, and `/v1/images/generations` now refund consumed credits when the provider call fails or usage persistence fails before the response is returned.
- **Responses Route Headers:** `/v1/responses` now emits the same routing and billing headers as the other paid inference routes: `x-model-routed`, `x-provider-used`, `x-provider-model`, and `x-actual-credits`.
- **Guest Session Bootstrap Resilience:** Guest session bootstrap and guest-to-user link side effects now fail closed on network errors instead of surfacing unhandled browser/test rejections.
- **API Browser CORS:** Added explicit Fastify CORS support for current local web origins so browser requests to the API no longer fail preflight by default.
- **Repo Audit Tracking Docs:** Updated the repo-audit plan and decision-process docs to reflect that PR #36 is now partially implemented rather than still fully deferred.
- **Planning Doc Placement:** Documented `docs/plans/` as the canonical location for persisted implementation plans.
- **Smoke Workflow Guidance:** Clarified that web smoke validation must run against the rebuilt Docker-local stack on the standard `http://127.0.0.1:3000` origin, not against standalone local web servers or alternate ports.
- **Smoke Workflow Coverage:** Updated the smoke spec and GitHub smoke workflow to cover the guest-first `/` flow, locked paid models, dismissible auth modal, and in-place unlock after signup.
- **Docker-Local Guest Runtime Defaults:** Wired a local-only `WEB_INTERNAL_GUEST_TOKEN` into the Docker-local `api` and `web` services, fixed the API container to target Ollama via `http://ollama:11434`, and extended smoke coverage so guest chat itself must succeed.
- **Guest Token Hardening:** Base Compose now fails closed for `WEB_INTERNAL_GUEST_TOKEN`; only `pnpm stack:dev` and the GitHub smoke workflow inject the disposable development token, while deployed environments must set an explicit secret.
- **Smoke Trigger Coverage:** The web smoke workflow now also tracks changes under `supabase/**` and `.env.example` because guest bootstrap and auth smoke depend on the live Supabase CLI schema path and Compose env wiring.
- **Smoke Workflow Orchestration:** Updated the GitHub smoke workflow to start the Supabase CLI stack, reset the local schema from repo migrations, pull a small local Ollama model for `guest-free`, and then start the Docker app stack alongside it so guest smoke still sends one real free-model message.
- **Smoke Bootstrap Guidance:** Clarified that `pnpm bootstrap:local` is a fresh-environment setup step, not a routine smoke prerequisite, because it resets local Supabase state.
- **Docker-Local Rebuild Guidance:** Clarified that manual localhost verification must use rebuilt `api` and `web` containers from the current working tree because stale compiled containers can continue serving pre-fix behavior.
- **Plans Index Organization:** Reorganized `docs/plans/` so only in-flight plans remain at the root while completed dated plans move under `docs/plans/completed/`.
- **Provider Metrics Documentation:** Aligned README, runbook, and architecture docs with the new public/internal provider metrics boundary and in-memory reset behavior.
- **Web Auth Session Sync:** Fixed stale browser auth-session behavior by synchronizing the mirrored local auth store with Supabase session refresh events, while preserving seeded smoke/dev sessions until a real Supabase session has been observed.
- **Protected Route Hydration:** Fixed `/`, `/developer`, and `/settings` auth guards so they wait for client auth-session hydration before redirecting to `/auth`, preventing false redirects during production startup.

## [0.1.0] - 2026-02-24

### Added
- **Core Platform:** Initial release of the TypeScript Monorepo (migrated from Python MVP).
- **API:** Fastify-based REST API with OpenAI-compatible endpoints (`/v1/chat/completions`).
- **Web:** Next.js Chat UI with multi-conversation support.
- **Auth:** Comprehensive auth system with Google OAuth, Email/Password, and 2FA enrollment.
- **Billing:**
    - Credit ledger system (1 BDT = 100 Credits).
    - Local payment intent verification (mock/manual flow).
    - Webhook handling for payment providers.
- **AI Routing:**
    - Provider registry supporting Ollama (local), Groq (cloud), and Mock.
    - Fallback logic for model availability.
- **Infrastructure:** Docker Compose stack including Postgres, Redis, and Ollama.

### Deprecated
- **Python MVP:** Legacy Python implementation was deprecated at 0.1.0 and has since been removed in Unreleased cleanup.
