# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
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
- **OpenRouter & CostCalculator:** Removed inadvertently added/restored implementations to maintain project scope and avoid token-based billing complexity.
- **Legacy Python MVP:** Removed deprecated root `app/` and `tests/` Python implementation paths after documenting TypeScript migration mapping.
- **Duplicate OpenAPI Contract:** Removed `openapi/openapi.yaml`; `packages/openapi/openapi.yaml` is now the sole in-repo OpenAPI source.

### Changed
- **Product Positioning Docs:** Reframed top-level product and architecture docs around Hive as a broader AI inference platform, with Bangladesh-native payments positioned as one strategic wedge rather than the entire product definition.
- **Future Roadmap:** Rewrote the active roadmap so already-shipped hardening work is treated as delivered baseline and remaining work is organized around provider breadth, analytics, commercial controls, and operator maturity.
- **Docker Documentation:** Clarified why Docker Compose is used locally, why `api` and `web` are separate containers, and how that differs from running `pnpm dev` directly.
- **Local Auth Bootstrap Docs:** Clarified that local Supabase generates real `ANON_KEY` and `SERVICE_ROLE_KEY` values, and those keys must be copied into `.env` before starting Docker `api` and `web`.
- **Local Development Workflow:** Standardized contributor onboarding around `pnpm stack:dev` as the canonical full-stack hot-reload entry point, and clarified that Supabase runs as Docker containers under the Supabase CLI rather than inside Hive's Compose file.
- **Bootstrap Workflow:** Split first-time local setup into `pnpm bootstrap:local` and kept `pnpm stack:dev` as the daily startup command; bootstrap now provisions the default local Ollama model as well as Supabase schema state.
- **PR Hygiene Docs:** Tightened guidance so pull request titles are expected to be scoped and Conventional-Commit-style where practical.
- **Web Architecture:** Moved to a "Guarded Chat Home" structure.
    - `/` is now the authenticated chat interface.
    - Unauthenticated users are strictly redirected to `/auth`.
- **Documentation:** Major restructuring of `AGENTS.md` to include the "Superpowers" workflow and stricter operational mandates.
- **Git Workflow Policy:** Worktrees are now optional in this repository; they remain available for isolation but are no longer required by default.
- **Browser Runtime Configuration:** Web runtime now requires explicit `NEXT_PUBLIC_API_BASE_URL`, `NEXT_PUBLIC_SUPABASE_URL`, and `NEXT_PUBLIC_SUPABASE_ANON_KEY` instead of implicit localhost or placeholder fallbacks.
- **Web Smoke Auth Fixtures:** Playwright smoke auth now prefers real Supabase signup using `E2E_SUPABASE_ANON_KEY` and only uses synthetic token fallback when `E2E_ALLOW_DEV_TOKEN_FALLBACK=true` is explicitly enabled.
- **API Key Metadata:** Persistent API key records now expose only `keyPrefix` metadata instead of a plaintext-looking `key` field.

### Fixed
- **API Browser CORS:** Added explicit Fastify CORS support for current local web origins so browser requests to the API no longer fail preflight by default.
- **Repo Audit Tracking Docs:** Updated the repo-audit plan and decision-process docs to reflect that PR #36 is now partially implemented rather than still fully deferred.
- **Planning Doc Placement:** Documented `docs/plans/` as the canonical location for persisted implementation plans.
- **Smoke Workflow Guidance:** Clarified that the web smoke runbook is production-style validation guidance, not a `pnpm stack:dev` workflow.
- **Smoke Bootstrap Guidance:** Clarified that `pnpm bootstrap:local` is a fresh-environment setup step, not a routine smoke prerequisite, because it resets local Supabase state.
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
