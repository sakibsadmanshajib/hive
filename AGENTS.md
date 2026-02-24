---
name: hive-agent
description: Implementation and operations agent for Hive (TypeScript API + web + provider integrations)
---

# AGENTS.md

This file defines how future coding agents should work in this repository.

## Agent Policy Source of Truth

`AGENTS.md` is the canonical instruction set for all coding agents in this repository.

- Treat this file as a replacement for tool-specific agent docs like `CLAUDE.md`, `GEMINI.md`, `CURSOR.md`, or similar files.
- If any other agent instruction file conflicts with this file, follow `AGENTS.md`.
- Keep shared agent policy updates centralized here so all agents operate with the same rules.

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

Web smoke E2E (Playwright):

```bash
pnpm --filter @hive/web exec playwright install chromium
pnpm --filter @hive/web test:e2e -- e2e/smoke-auth-chat-billing.spec.ts
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
- Worktrees are mandatory: because multiple AI agents/tools may touch the repo concurrently, start every new task in a fresh git worktree.
- Never run two independent tasks in the same working tree.
- Start each new task in a fresh git worktree instead of reusing the previous task's working directory.
- Before beginning a new task, prune the previously used worktree if its PR is merged.
- Use Conventional Commit style when possible, e.g. `feat(api): ...`, `fix(providers): ...`, `docs(readme): ...`.
- Write commit messages with intent and rationale, not only diff summary.
- Use descriptive branch names such as `feat/provider-status-internal` or `fix/redis-rate-limit`.
- Do not include unrelated formatting churn.
- Prefer incremental, reviewable changes.

### Worktree Quickstart

Worktree location policy:

- Create task worktrees under a dedicated parent `.worktrees/` directory.
- In this environment, if primary clone is `/home/sakib/hive`, create worktrees at `/home/sakib/.worktrees/hive-<task-slug>`.
- Keep `.worktrees` as a sibling path outside the repository root (not `hive/.worktrees`).

Use this flow for every task branch:

```bash
git fetch origin main
mkdir -p ../.worktrees
git worktree add ../.worktrees/hive-<task-slug> -b <type/task-name> origin/main
git -C ../.worktrees/hive-<task-slug> status
```

After merge, clean up the old worktree:

```bash
git worktree remove ../.worktrees/hive-<task-slug>
git worktree prune
```

## Detailed Usage Behavior

### Docker Compose Lifecycle

Use this exact lifecycle to avoid stale containers and port conflicts during local verification:

1. Stop any existing stack before starting task verification:

```bash
docker compose down
```

2. Start a fresh stack only when the task needs API/web integration behavior:

```bash
docker compose up --build -d
docker compose ps
```

3. Verify readiness before running E2E:
- API: `curl -s http://127.0.0.1:8080/health`
- Web: `curl -sI http://127.0.0.1:3000/auth`

4. Tear down stack when verification is complete or blocked:

```bash
docker compose down
```

Rules:
- Do not assume existing long-running containers represent current branch code.
- Do not debug E2E failures against stale containers; restart stack first.
- If ports are occupied by another project stack, stop that stack before proceeding.

Useful compose operations while debugging:

```bash
docker compose ps
docker compose logs api web
docker compose restart api web
docker compose down -v
```

Use `docker compose down -v` when you need a fully clean state (for example, flaky local E2E caused by stale DB/Redis state).

### Playwright Smoke E2E Expectations

Primary smoke spec: `apps/web/e2e/smoke-auth-chat-billing.spec.ts`

When touching auth/chat/billing/settings navigation or related API integration:

1. Ensure Playwright browser is installed:
   - `pnpm --filter @hive/web exec playwright install chromium`
2. Start stack with Docker Compose and confirm readiness:
   - `curl -s http://127.0.0.1:8080/health`
   - `curl -sI http://127.0.0.1:3000/auth`
3. Run smoke spec:
   - `pnpm --filter @hive/web test:e2e -- e2e/smoke-auth-chat-billing.spec.ts`

Environment used by E2E:
- `E2E_BASE_URL` (default: `http://127.0.0.1:3000`)
- `E2E_API_BASE_URL` (used by API fixtures)

If Playwright fails due to missing Linux libs locally:
- `pnpm --filter @hive/web exec playwright install-deps chromium`

### CI Pipeline Expectations

Primary CI workflow: `.github/workflows/ci.yml`
- Runs on PR/push to `main`.
- Ignores docs-only/markdown-only changes via `paths-ignore`.
- Uses path filtering to run API and web checks only when relevant scopes changed.
- API scope: lint, test, build.
- Web scope: lint, test, build.

Web smoke workflow: `.github/workflows/web-e2e-smoke.yml`
- Runs on PRs touching `apps/web/**`, `apps/api/**`, `docker-compose.yml`, or the workflow file.
- Installs Playwright Chromium, starts Docker stack, waits for readiness, then runs smoke spec.
- Uploads Playwright artifacts on failure.

Post-merge cleanup workflow: `.github/workflows/pr-cleanup.yml`
- Runs when PR is merged.
- Deletes merged source branches when safe and removes `status:in-progress` label.

Before opening/updating a PR, run local checks that match your touched scopes so CI results are predictable.

### Receiving AI/Code Review Feedback

When PR comments arrive:

1. Extract exact comment text and line locations.
2. Verify each comment against current code before changing anything.
3. Apply only technically correct suggestions for this codebase.
4. For suggestions requiring nonexistent endpoints or contracts, choose safe alternatives (for example deterministic test data prefixes) and document rationale in the PR update.
5. Re-run relevant tests/build after fixes.

Rules:
- Do not blindly apply all automated review suggestions.
- Do not ignore failing CI root causes while fixing only style nits.

### Merge Conflict Handling

Before push:

1. Sync latest `main`:

```bash
git fetch origin main
```

2. Integrate `main` into the task branch (`rebase` preferred unless merge commit is intentionally needed):

```bash
git rebase origin/main
```

3. Resolve conflicts file-by-file, preserving current task intent and docs consistency.
4. Re-run verification commands after conflict resolution.
5. Push only after clean status and successful verification evidence.

Rules:
- Never resolve conflicts by dropping behavior-critical tests/docs.
- Never force-push rewritten history without confirming branch ownership and PR impact.

### Push Readiness Checklist

Before `git push`, confirm all of the following:

- `git status` is clean except intended task files.
- Required tests/build commands for touched areas have run successfully.
- Docs and runbooks reflect any behavior or operational changes.
- Public/internal status endpoint boundaries remain intact.

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

- Use the Obra Superpowers framework for all development tasks (planning, implementation, debugging, verification, and branch completion workflows).
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

## Frontend IA Snapshot

Current web information architecture is chat-first:

- `/` is the default chat workspace (primary user entry after auth)
- `/developer` contains API key and usage-centric developer workflows
- `/settings` contains profile, billing/payment, and account settings workflows
- `/billing` is a compatibility route that points to `/settings` and `/developer`
- top-right header actions expose `Developer Panel` and `Settings` as peer-level destinations
