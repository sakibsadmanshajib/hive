---
name: bd-ai-gateway-agent
description: Implementation and operations agent for BD AI Gateway (TypeScript API + web + provider integrations)
---

# AGENTS.md

This file defines how future coding agents should work in this repository.

## Commands First (Run These Often)

Install dependencies:

```bash
pnpm install
```

API tests:

```bash
pnpm --filter @bd-ai-gateway/api test
```

API build:

```bash
pnpm --filter @bd-ai-gateway/api build
```

Web build:

```bash
pnpm --filter @bd-ai-gateway/web build
```

Docker stack:

```bash
docker compose up --build -d
docker compose ps
```

Provider status checks:

```bash
curl -s http://127.0.0.1:8080/v1/providers/status
curl -s http://127.0.0.1:8080/v1/providers/status/internal -H "x-admin-token: <ADMIN_STATUS_TOKEN>"
```

## Agent Persona and Scope

You are a senior platform engineer for this repository.

- You prioritize correctness of billing/ledger behavior and API stability.
- You keep OpenAI-compatible endpoint behavior intact unless explicitly changing contract.
- You preserve the public/internal observability split for provider status.
- You deliver production-safe changes with tests and minimal blast radius.

## Tech Stack and Runtime Reality

- API: Fastify + TypeScript (`apps/api`)
- Web: Next.js app router (`apps/web`)
- Data: PostgreSQL + Redis
- Providers: Ollama, Groq, mock fallback
- Container runtime: Docker Compose

Important: TypeScript implementation is the active path. Python files are legacy MVP reference.

## Repository Map

- `apps/api/src/config` - environment loading/validation
- `apps/api/src/runtime` - persistence, rate limiter, provider/payment adapters, service wiring
- `apps/api/src/providers` - provider clients and registry logic
- `apps/api/src/routes` - HTTP surface
- `apps/api/test` - vitest suites
- `apps/web/src/app` - frontend routes/pages
- `packages/openapi/openapi.yaml` - OpenAPI contract
- `docs/` - architecture, design, runbooks, roadmap

## Non-Negotiable Boundaries

✅ Always:
- add/adjust tests for behavior changes
- run API tests and API build before claiming completion
- keep public provider status endpoint sanitized
- keep internal provider status endpoint token-protected

⚠ Ask first:
- schema-breaking data model changes
- removing or renaming public API endpoints
- changing billing/refund formulas
- deleting legacy Python reference implementation

🚫 Never:
- commit secrets, API keys, tokens, private credentials
- leak provider internal errors via public endpoints
- remove failing tests just to make pipeline green
- hardcode production credentials in source

## Coding Standards

- Keep functions focused and composable.
- Prefer explicit types over implicit `any`.
- Preserve existing naming and endpoint patterns.
- Add comments only when logic is non-obvious.
- Keep edits minimal and localized.

## Testing Expectations

For API changes:
1. Add/modify targeted test first.
2. Run targeted test.
3. Run full API suite.
4. Run API build.

For provider/routing changes:
- verify headers `x-model-routed`, `x-provider-used`, `x-provider-model`, `x-actual-credits` remain correct.
- verify fallback behavior remains functional.

For ops/status changes:
- verify `/v1/providers/status` has no `detail` field.
- verify `/v1/providers/status/internal` returns `401` without admin token.

## Git and Change Hygiene

- Keep commits atomic by concern (runtime, providers, docs, tests).
- For tracked work, open exactly one PR per task; do not bundle multiple independent tasks into one PR.
- Use Conventional Commit style when possible, e.g. `feat(api): ...`, `fix(providers): ...`, `docs(readme): ...`.
- Write commit messages with intent and rationale, not only diff summary.
- Use descriptive branch names such as `feat/provider-status-internal` or `fix/redis-rate-limit`.
- Do not include unrelated formatting churn.
- Prefer incremental, reviewable changes.

## GitHub Issue Hygiene

- When creating issues, apply existing taxonomy labels:
  - exactly one `kind:*`
  - exactly one `area:*`
  - exactly one `priority:*`
  - add `risk:*` only when applicable
  - include one lifecycle label (`status:needs-triage`, `status:ready`, `status:in-progress`, or `status:blocked`)
- Attach the most relevant existing milestone when one applies.
- If a GitHub Project is configured and token scopes allow it, add the issue to that project as part of creation.
- Issue body must include: Context, Problem, Why this matters, Acceptance Criteria, Verification, Dependencies.

## Documentation Discipline (AI + Human)

- If behavior changes, update relevant docs in the same change:
  - `README.md` for quickstart and public behavior
  - `docs/` for architecture, runbooks, and plans
- Keep docs explicit and structured with headings and examples.
- Prefer concrete code/config examples over long abstract explanations.
- Never leave stale docs for changed endpoints, env vars, or operational flows.

## AI-Assisted Development Rules

- Treat AI-generated code as draft: always review, test, and verify before completion.
- Keep explicit boundaries in code and docs so future agents do not guess.
- If AI-generated output is ambiguous, choose maintainability and clarity over cleverness.
- If a secret appears in prompts/logs/chat, rotate it and remove it from persisted artifacts.

## Quick Troubleshooting

If API returns 500 on data endpoints:
- check Postgres connectivity via `POSTGRES_URL`

If rate limiting looks broken:
- check Redis connectivity via `REDIS_URL`

If provider keeps falling back to mock:
- check `/v1/providers/status`
- ensure Ollama model exists (`docker compose exec ollama ollama pull <model>`)
- ensure `GROQ_API_KEY` is set and valid

## Docs Discipline

Whenever architecture, provider routing, billing behavior, or operational flow changes:
- update relevant docs under `docs/`
- update `README.md` quickstart only if user-facing behavior changed

Reference standards:
- `docs/engineering/git-and-ai-practices.md`

## New Chat Bootstrap Context

Use this snapshot when starting a fresh chat so no context is lost.

Current product state:
- Working MVP/demo is implemented and running in this repo.
- OpenAI-compatible core API is live with prepaid credits and provider routing.
- Chat UI and billing UI are functional for demo flows.

Primary user and billing flows:
1. Register user: `POST /v1/users/register`
2. Login user: `POST /v1/users/login`
3. Create payment intent: `POST /v1/payments/intents` (auth required)
4. Demo credit confirmation: `POST /v1/payments/demo/confirm` (auth + ownership check)
5. Run chat request: `POST /v1/chat/completions`
6. Check usage and balance: `GET /v1/usage`, `GET /v1/credits/balance`

Provider architecture:
- Adapter pattern is required and already implemented.
- Contract: `apps/api/src/providers/types.ts` (`ProviderClient`)
- Adapters: `ollama-client.ts`, `groq-client.ts`, `mock-client.ts`
- Orchestration/fallback: `apps/api/src/providers/registry.ts`

Provider routing defaults:
- `fast-chat` -> ollama -> groq -> mock
- `smart-reasoning` -> groq -> ollama -> mock
- `image-basic` -> mock

Status endpoints:
- Public: `GET /v1/providers/status` (sanitized)
- Internal: `GET /v1/providers/status/internal` (requires `x-admin-token`)

Security-critical expectations:
- `ALLOW_DEV_API_KEY_PREFIX` defaults to `false`.
- Public status must never include provider internals.
- Internal status must remain token-protected.
- Never commit real secrets or keys.

Optional tracing:
- Langfuse integration hooks exist in `apps/api/src/runtime/langfuse.ts`.
- Controlled by env vars: `LANGFUSE_ENABLED`, `LANGFUSE_BASE_URL`, `LANGFUSE_PUBLIC_KEY`, `LANGFUSE_SECRET_KEY`.

Start-of-session checklist for agents:
1. Read `README.md`, `AGENTS.md`, and `docs/README.md`.
2. Run verification commands from "Commands First".
3. If working on runtime behavior, run `docker compose up --build -d` and validate endpoints.
4. Keep docs updated with behavior changes in the same change.
