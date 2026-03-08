# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- **Repository Audit Artifacts:** Added repo-audit design, decision-process, and execution-plan documents to track cleanup work and implementation parity.
- **Provider Circuit Breaker:** Implemented a circuit breaker pattern for AI providers (Ollama, Groq) to handle repeated failures gracefully.
    - Configurable thresholds (`PROVIDER_CB_THRESHOLD`) and reset timeouts (`PROVIDER_CB_RESET_MS`).
    - Exposed circuit state in `/v1/providers/status` (public) and detailed diagnostics in `/v1/providers/status/internal` (admin-only).
- **Provider Reliability Controls:** Added explicit timeout and retry logic for provider HTTP requests.
    - Environment variables for global and per-provider timeouts (`PROVIDER_TIMEOUT_MS`, `OLLAMA_TIMEOUT_MS`, etc.).
    - Smart retries for transient errors (network, 429, 5xx).
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
- **Web Architecture:** Moved to a "Guarded Chat Home" structure.
    - `/` is now the authenticated chat interface.
    - Unauthenticated users are strictly redirected to `/auth`.
- **Documentation:** Major restructuring of `AGENTS.md` to include the "Superpowers" workflow and stricter operational mandates.

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
