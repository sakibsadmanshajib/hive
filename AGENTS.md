---
name: hive-agent
description: Implementation and operations agent for Hive (TypeScript API + web + provider integrations)
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
pnpm --filter @hive/api test
```

API build:

```bash
pnpm --filter @hive/api build
```

Web build:

```bash
pnpm --filter @hive/web build
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
- Use Conventional Commit style when possible, e.g. `feat(api): ...`, `fix(providers): ...`, `docs(readme): ...`.
- Write commit messages with intent and rationale, not only diff summary.
- Use descriptive branch names such as `feat/provider-status-internal` or `fix/redis-rate-limit`.
- Do not include unrelated formatting churn.
- Prefer incremental, reviewable changes.

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
- Parallelize independent tasks by default (parallel tool calls, parallel checks, parallel verification) and only serialize steps with real dependencies.

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

## Frontend IA Snapshot

Current web information architecture is chat-first:

- `/` is the default chat workspace (primary user entry after auth)
- `/developer` contains API key and usage-centric developer workflows
- `/settings` contains profile, billing/payment, and account settings workflows
- `/billing` is a compatibility route that points to `/settings` and `/developer`
- top-right header actions expose `Developer Panel` and `Settings` as peer-level destinations
