---
name: hive-agent
description: Implementation and operations agent for Hive (TypeScript API + web + provider integrations)
---

# AGENTS.md

This file is the canonical operating policy for coding agents in this repository.

## Agent Policy Source of Truth

- Treat this file as a replacement for agent-specific policy files like `CLAUDE.md`, `GEMINI.md`, `CURSOR.md`, and similar docs.
- If any other agent-instruction file conflicts with this file, follow `AGENTS.md`.
- Keep shared policy updates centralized here so all agents follow the same rules.

## ⛔ Superpowers Prerequisite Gate (Mandatory — Execute First)

**Before doing ANY work**, verify that Superpowers skills are installed locally:

```bash
ls .agents/skills/using-superpowers/SKILL.md
```

- **If the file exists**: proceed normally. You MUST load skills directly from `.agents/skills/` when needed.
- **If the file does NOT exist or the path is broken**: **STOP ALL WORK IMMEDIATELY.** Do not read code, do not plan, do not edit files.

When superpowers is missing, reply with **exactly** this message and nothing else:

> ⛔ **Superpowers skills not found.**
>
> This repository requires [Obra Superpowers](https://github.com/obra/superpowers) to be installed before any agent work can proceed.
>
> **Setup (one-time):**
> ```bash
> git clone https://github.com/obra/superpowers.git ~/.agents/superpowers
> mkdir -p .agents/skills
> cp -r ~/.agents/superpowers/skills/* .agents/skills/
> ```
>
> After setup, re-run your request.

**No exceptions.** Do not attempt to self-install, skip, or work around this gate.

## Agent Skills Reference

Custom skills are located in the following paths. Read the `SKILL.md` in each skill folder for full instructions before using.

| Skill | Path | Purpose |
|-------|------|---------|
| Superpowers | `.agents/skills/using-superpowers/` (and others) | Obra Superpowers planning/execution framework |
| GitHub API | `.agents/skills/gh-api/SKILL.md` | `gh api` patterns for PR reviews, comments, and issue management |

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

## Superpowers Workflow Gate (Mandatory)

Use the Obra Superpowers framework for all development tasks: planning, implementation, debugging, verification, and completion.

Before any code edits, perform a plan gate:

### Task

Plan for the task exactly as provided by the user. If task input is missing, ask for a one-sentence restatement and stop.

### Rules

- Do not edit code during this workflow.
- You may read files for context.
- Plan steps must be small (2-10 minutes each).
- Every plan step must include a verification command.

### Output format (use exactly)

```md
## Goal
## Assumptions
## Plan
(Each step must include: Files, Change, Verify)
## Risks & mitigations
## Rollback plan
```

### Persist (mandatory)

Write the plan to `artifacts/superpowers/plan.md` (Note: the `artifacts` folder is local-only and ignored by git). Create the folder if needed. Confirm by listing `artifacts/superpowers/`.

Preferred writer command when available:

```bash
python .agent/skills/superpowers-workflow/scripts/write_artifact.py --path artifacts/superpowers/plan.md
```

Pass the full markdown plan as stdin to the command.

If the command is unavailable, write `artifacts/superpowers/plan.md` directly and explicitly state that the helper command was unavailable.

### Approval gate

After writing the plan, ask exactly:

`Approve this plan? Reply APPROVED if it looks good.`

If user replies `APPROVED`:

- Do not implement yet.
- Reply exactly: `Plan approved. Run /superpowers-execute-plan to begin implementation.`

## Agent Persona and Scope

You are a senior platform engineer for this repository.

- Prioritize billing and ledger correctness.
- Preserve API stability and OpenAI-compatible behavior unless explicitly changing contract.
- Preserve the public/internal provider status boundary.
- Deliver production-safe changes with minimal blast radius.

## Tech Stack and Project Map

- API: Fastify + TypeScript (`apps/api`)
- Web: Next.js app router (`apps/web`)
- Data: PostgreSQL + Redis
- Providers: Ollama, Groq, mock fallback
- Runtime: Docker Compose

TypeScript is the active implementation path. Python files are legacy MVP reference.

Key paths:

- `apps/api/src/config` - environment loading/validation
- `apps/api/src/runtime` - persistence, rate limiting, adapters, service wiring
- `apps/api/src/providers` - provider clients and routing/fallback logic
- `apps/api/src/routes` - HTTP API surface
- `apps/api/test` - API test suites
- `apps/web/src/app` - web app routes/pages
- `packages/openapi/openapi.yaml` - API contract
- `docs/` - architecture, runbooks, design/plans

## Boundaries

Always:

- Add or adjust tests for behavior changes.
- Run required verification for touched areas.
- Keep public provider status sanitized.
- Keep internal provider status admin-token-protected.

Ask first:

- Schema-breaking data model changes.
- Removing or renaming public API endpoints.
- Billing/refund formula changes.
- Deleting legacy Python reference implementation.

Never:

- Commit secrets, tokens, or credentials.
- Leak provider internals through public endpoints.
- Remove failing tests to force green CI.
- Hardcode production secrets.

## Coding and Testing Standards

- Keep functions focused and composable.
- Prefer explicit types over implicit `any`.
- Preserve existing endpoint/naming patterns.
- Add comments only for non-obvious logic.
- Keep edits minimal and localized.

For API-impacting changes:

1. Add or update targeted tests first.
2. Run targeted tests.
3. Run full API test suite.
4. Run API build.

For provider/routing changes, verify headers remain correct:

- `x-model-routed`
- `x-provider-used`
- `x-provider-model`
- `x-actual-credits`

For ops/status changes:

- `/v1/providers/status` must not include internal `detail`.
- `/v1/providers/status/internal` must return `401` without valid admin token.

## Git Workflow and Worktrees (Mandatory)

- Worktrees are required because multiple AI agents/tools may operate concurrently.
- Never run two independent tasks in the same working tree.
- Keep commits atomic by concern (runtime, providers, docs, tests).
- Open one PR per tracked task.
- Use Conventional Commit style when practical.

Worktree location policy:

- Create task worktrees under a dedicated sibling `.worktrees/` directory.
- In this environment, primary clone is `/home/sakib/hive` and task worktrees should be created at `/home/sakib/.worktrees/hive-<task-slug>`.
- Do not create worktrees inside the main repository directory.

Worktree quickstart:

```bash
git fetch origin main
mkdir -p ../.worktrees
git worktree add ../.worktrees/hive-<task-slug> -b <type/task-name> origin/main
git -C ../.worktrees/hive-<task-slug> status
pnpm --dir ../.worktrees/hive-<task-slug> install --frozen-lockfile
```

Mandatory: after creating or switching into a task worktree for the first time, run `pnpm install --frozen-lockfile` in that worktree before other project commands.

After merge:

```bash
git worktree remove ../.worktrees/hive-<task-slug>
git worktree prune
```

## Docker Compose Lifecycle

Use this lifecycle to avoid stale containers and bad local verification:

1. Stop existing stack:

```bash
docker compose down
```

2. Start fresh stack when integration behavior is relevant:

```bash
docker compose up --build -d
docker compose ps
```

3. Verify readiness:

- API: `curl -s http://127.0.0.1:8080/health`
- Web: `curl -sI http://127.0.0.1:3000/auth`

4. Tear down when done:

```bash
docker compose down
```

Useful debugging commands:

```bash
docker compose ps
docker compose logs api web
docker compose restart api web
docker compose down -v
```

Use `docker compose down -v` when stale DB/Redis state is likely causing flakiness.

## Playwright Smoke E2E Expectations

Primary smoke spec: `apps/web/e2e/smoke-auth-chat-billing.spec.ts`

When touching auth/chat/billing/settings routes or related API integration:

1. Install browser:
   - `pnpm --filter @hive/web exec playwright install chromium`
2. Start stack and verify readiness.
3. Run smoke spec:
   - `pnpm --filter @hive/web test:e2e -- e2e/smoke-auth-chat-billing.spec.ts`

E2E environment variables:

- `E2E_BASE_URL` (default `http://127.0.0.1:3000`)
- `E2E_API_BASE_URL` (used by API fixtures)

If Linux browser dependencies are missing locally:

- `pnpm --filter @hive/web exec playwright install-deps chromium`

## CI Pipeline Expectations

Primary CI: `.github/workflows/ci.yml`

- Runs on PR/push to `main`.
- Ignores docs-only changes with `paths-ignore`.
- Uses path filters to run only needed scopes.
- API scope: lint, test, build.
- Web scope: lint, test, build.

Web smoke workflow: `.github/workflows/web-e2e-smoke.yml`

- Runs for PR changes under `apps/web/**`, `apps/api/**`, `docker-compose.yml`, and workflow file.
- Uses Node `24` (same as primary CI) so pnpm cache keys can be reused across workflows.
- Restores cached Playwright browsers from `~/.cache/ms-playwright` before install.
- Uses `pnpm install --prefer-offline` to maximize dependency cache reuse.
- Uses workflow-level concurrency cancellation to stop stale in-progress smoke runs on the same branch/PR.
- Installs Playwright Chromium, starts Docker stack, waits for readiness, runs smoke spec.
- Uploads Playwright artifacts on failure.

Post-merge cleanup: `.github/workflows/pr-cleanup.yml`

- Runs on merged PR.
- Deletes merged source branch when safe.
- Removes `status:in-progress` label.

Before PR updates, run local checks matching touched scopes.

## Review, Merge, and Push Hygiene

When receiving review feedback:

1. Extract exact feedback and locations.
2. Verify against current code.
3. Apply only technically correct changes.
4. Re-run relevant tests/build.

Before push:

1. `git fetch origin main`
2. `git rebase origin/main` (preferred)
3. Resolve conflicts without dropping behavior-critical tests/docs
4. Re-run verification commands
5. Push only with clean status and verification evidence

Never force-push rewritten history unless explicitly required and safe.

## Documentation Discipline

- If behavior changes, update docs in the same change.
- Update `README.md` for user-facing changes.
- Update `CHANGELOG.md` for all notable changes (Added, Changed, Deprecated, Removed, Fixed, Security).
- Update `docs/` runbooks/architecture/plans for operational or architectural changes.
- Prefer concrete examples (commands, payloads, env vars) over abstract prose.
- Keep docs synchronized with implementation.

Reference: `docs/engineering/git-and-ai-practices.md`

## Quick Troubleshooting

If API returns 500 on data endpoints:

- Check `POSTGRES_URL` and DB connectivity.

If rate limiting is broken:

- Check `REDIS_URL` and Redis connectivity.

If provider falls back to mock unexpectedly:

- Check `/v1/providers/status`
- Ensure Ollama model exists (`docker compose exec ollama ollama pull <model>`)
- Ensure `GROQ_API_KEY` is valid
