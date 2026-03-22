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

## Maintainer Override

- Explicit maintainer instructions in the current session override `AGENTS.md` defaults globally.
- If the maintainer explicitly asks to persist an override or workflow preference, update `AGENTS.md` so the preference applies in future sessions as well.
- If execution or verification uncovers a durable repo-specific lesson that is likely to matter again, update `AGENTS.md` before completion so the lesson persists for future sessions.
- This override rule does not permit committing secrets, tokens, or credentials, leaking protected internal data, or otherwise bypassing hard safety constraints around sensitive information.
- Current persisted maintainer preference: worktrees are optional in this repository and should not be treated as mandatory for normal task execution.
- Current persisted maintainer preference: use the Docker-local stack as the default development and verification environment for Hive; do not rely on standalone local app servers or alternate local ports as a normal workflow.
- Current persisted maintainer preference: run builds only inside Docker containers (`docker compose exec api/web ... build`); do not run local `pnpm --filter @hive/api build` or `pnpm --filter @hive/web build`.
- Current persisted maintainer preference: hands-free execution—the agent runs all commands (build, test, git, Docker, etc.); the maintainer only approves or rejects. Do not ask the maintainer to run commands; run them and report results.
- Current persisted maintainer preference: **always commit** when work is done or at a verifiable checkpoint; do not leave the working tree with uncommitted changes as the normal outcome. Commit so that the commit id reflects what is running in containers and can be reproduced.

## Agent Skills Reference

Custom skills are located in the following paths. Read the `SKILL.md` in each skill folder for full instructions before using.

| Skill | Path | Purpose |
|-------|------|---------|
| Superpowers | `.agents/skills/using-superpowers/` (and others) | Obra Superpowers planning/execution framework |
| GitHub API | `.agents/skills/gh-api/SKILL.md` | `gh api` patterns for PR reviews, comments, and issue management |
| GitHub Review Reading | `.agents/skills/gh-reading-reviews/SKILL.md` | Read PR review comments, review summaries, and PR conversation comments |
| GitHub Review Replies | `.agents/skills/gh-responding-to-reviews/SKILL.md` | Reply inside inline review threads with the correct endpoint |
| GitHub PR Editing | `.agents/skills/gh-editing-prs/SKILL.md` | Inspect and edit PR metadata, including REST fallbacks for `gh pr edit` failures |
| Docker-Local Smoke Validation | `.agents/skills/docker-local-smoke-validation/SKILL.md` | Verify Hive web auth/chat/billing changes against the rebuilt Docker-local stack on the standard origin |

## Commands First (Run These Often)

Install dependencies:

```bash
pnpm install
```

API tests:

```bash
pnpm --filter @hive/api test
```

API build (run inside Docker only; do not run local builds):

```bash
docker compose exec api sh -c "cd /app && pnpm --filter @hive/api build"
```

Web build (run inside Docker only; do not run local builds):

```bash
docker compose exec web sh -c "cd /app && pnpm --filter @hive/web build"
```

Requires the stack to be up (`docker compose up -d` or `docker compose up --build -d`). If web changes touch browser auth/bootstrap, `NEXT_PUBLIC_*` env usage, or smoke flows, also verify the production bundle with the required public envs set (same exec, with env vars passed into the container):

```bash
docker compose exec -e NEXT_PUBLIC_API_BASE_URL=http://127.0.0.1:8080 -e NEXT_PUBLIC_SUPABASE_URL=http://127.0.0.1:54321 -e NEXT_PUBLIC_SUPABASE_ANON_KEY=test-supabase-anon-key web sh -c "cd /app && pnpm --filter @hive/web build"
```

Unit tests are not sufficient evidence for those changes; they can miss prerender-time and browser-bundle env failures.
When auth/session behavior changes, also verify that protected routes do not redirect during initial client hydration before local auth state is loaded.

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

## Superpowers Workflow Gate (Default)

Use the Obra Superpowers framework for all development tasks by default unless the maintainer explicitly directs a different workflow.

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

### Persist (default)

Write the plan to `docs/plans/YYYY-MM-DD-<task-name>.md`. Reuse an existing tracked plan when continuing that same task; otherwise create a new dated plan file under `docs/plans/`.

Preferred writer command when available:

```bash
python .agent/skills/superpowers-workflow/scripts/write_artifact.py --path docs/plans/YYYY-MM-DD-<task-name>.md
```

Pass the full markdown plan as stdin to the command.

If the command is unavailable, write the plan file in `docs/plans/` directly and explicitly state that the helper command was unavailable.

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

Repo lesson:

- Some route-level unit tests use lightweight mock replies where `reply.header()` is not chainable; shared route helpers should set headers without relying on Fastify-style chaining unless the tests are updated in the same change.

For provider/routing changes, verify headers remain correct:

- `x-model-routed`
- `x-provider-used`
- `x-provider-model`
- `x-actual-credits`

Provider readiness lesson that must persist:

- Startup provider model readiness checks must use zero-token metadata endpoints such as Ollama `/api/tags` and Groq `/models`; do not spend chat tokens just to verify configured model availability.

For ops/status changes:

- `/v1/providers/status` must not include internal `detail`.
- `/v1/providers/status/internal` must return `401` without valid admin token.
- `/v1/providers/metrics` must remain public-safe and omit provider diagnostic detail plus raw circuit-breaker failure internals.
- `/v1/providers/metrics/internal` and `/v1/providers/metrics/internal/prometheus` must return `401` without a valid admin token.
- Provider metrics are currently in-memory per API instance; if behavior or docs touch them, explicitly preserve or document restart-reset semantics.

## Git Workflow and Worktrees (Default)

- Worktrees are optional in this repository. Use them when isolation is useful, but they are not required for normal task execution.
- Never run two independent tasks in the same working tree at the same time.
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

Default: if you create or switch into a task worktree for the first time, run `pnpm install --frozen-lockfile` in that worktree before other project commands. If you stay in the primary working tree, use the existing install unless dependency changes require a refresh.

After merge:

```bash
git worktree remove ../.worktrees/hive-<task-slug>
git worktree prune
```

## Docker Compose Lifecycle

Use this lifecycle to avoid stale containers and bad local verification.

### Run latest code without rebuilding images

To test code changes in Docker **without** rebuilding api/web images (saves several minutes):

1. Use the dev override so api and web run from **mounted source** and dev servers (hot reload).
2. From repo root, with Supabase already running (`npx supabase start`):

```bash
docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d api web
```

No `--build` is needed: the dev override mounts `./` into the containers and overrides the command to `pnpm ... dev`, so containers use your current working tree. Restart after code changes is only needed if the dev server doesn’t pick them up.

For full dev stack (Supabase env + dev override) in one go, use:

```bash
pnpm stack:dev
```

To run latest code **without rebuilding** api/web images (e.g. after editing code; typically &lt;30s):

```bash
pnpm stack:latest
```

This uses the same dev override (mounted source + dev servers) but skips `--build`. Use `stack:dev` when bringing the stack up from cold or when dependencies change; use `stack:latest` when you only need api/web to pick up code changes.

### Full rebuild lifecycle

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

### Commit behaviour

- **Always commit** when a task or verifiable checkpoint is complete. Do not leave the working tree with a long list of uncommitted changes as the default outcome.
- Commit so that (1) the repo has a clear revision for what was done, (2) the commit id can be used to verify what is running in containers, and (3) the maintainer can reproduce or roll back from a known commit.
- Use `git commit --no-gpg-sign -m "..."` when the maintainer has requested no GPG signing. Prefer Conventional Commit style for the message.
- Before reporting "done" or running verification that depends on container state, commit first so the commit id matches the code you are testing.

### Getting latest code in api and web containers

We **do not** rely on rebuilding images to get the latest code during normal development. Use one of these approaches:

1. **Mounted source (recommended for daily work)**  
   Run api and web with the **dev override** so the host working tree is mounted into the containers and dev servers (e.g. Next.js, ts-node or similar) serve the current files:
   - `pnpm stack:dev` — full dev stack with mounted source and dev servers.
   - `pnpm stack:latest` — same but skip image build; use after dependencies are already installed.
   Containers then run whatever is on the host (committed or not). To have a **meaningful commit id** printed on startup, **commit first**, then start or restart so `api git-commit:` / `web git-commit:` in logs match `git rev-parse HEAD`.

2. **Build inside running containers (verify build, same source)**  
   With the stack up (production-style or dev override with mounted source), run the build **inside** the container so the build environment is Docker, not the host:
   - `docker compose exec api sh -c "cd /app && pnpm --filter @hive/api build"`
   - `docker compose exec web sh -c "cd /app && pnpm --filter @hive/web build"`
   With mounted source, `/app` in the container is your host repo; the container is building the same tree you have locally. With production-style images (no mount), the container builds whatever was baked into the image at `docker compose up --build`.

3. **Production-style images (full rebuild)**  
   To bake the current tree into new images and run them: `docker compose up --build -d api web`. Optionally set `GIT_COMMIT=$(git rev-parse HEAD)` so the image build-arg and container CMD echo that commit. Use when you need to verify the exact production build path or when you are not using mounted source.

### Verifying containers run latest code (commit id)

To confirm api/web are running the code you expect:

1. **Commit your changes** (e.g. `git commit --no-gpg-sign -m "your message"`). Then the commit id reflects the code you are about to run.
2. **With dev override (mounted source):** Start or restart with `pnpm stack:latest` or `pnpm stack:dev`. On startup, api and web each print `api git-commit: <sha>` and `web git-commit: <sha>` from the mounted repo (host’s `git rev-parse HEAD`). Check that the printed commit matches your latest commit.
3. **To verify build inside containers:** Run `docker compose exec api sh -c "cd /app && pnpm --filter @hive/api build"` and the same for `web`. If the build fails, fix and re-run; if it passes, the container can build the current tree.
4. **Production-style (baked image):** Use `GIT_COMMIT=$(git rev-parse HEAD) docker compose up --build -d api web` so the image logs that commit on start.

## Playwright Smoke E2E Expectations

Primary smoke spec: `apps/web/e2e/smoke-auth-chat-billing.spec.ts`

When touching auth/chat/billing/settings routes or related API integration:

1. Install browser:
   - `pnpm --filter @hive/web exec playwright install chromium`
2. Start the Docker-local stack and verify readiness.
3. Run smoke spec:
   - `pnpm --filter @hive/web test:e2e -- e2e/smoke-auth-chat-billing.spec.ts`

E2E environment variables:

- `E2E_BASE_URL` (default `http://127.0.0.1:3000`)
- `E2E_API_BASE_URL` (used by API fixtures)
- `E2E_SUPABASE_ANON_KEY` (required for smoke cases that create real Supabase users programmatically)

Auth/session lessons that must persist:

- Do not clear the custom browser auth session just because `supabase.auth.getSession()` returns `null` on startup; that can erase seeded smoke/dev sessions before Supabase has established any browser session.
- Only let Supabase-driven sign-out clear the mirrored custom auth session after the browser runtime has observed a real Supabase session.
- For protected web routes, wait until client auth-session hydration is ready before redirecting to `/auth`, or valid local sessions can be bounced to the login page during initial render.
- Guest-session bootstrap and guest-to-user link side effects must fail closed on browser/network errors and preserve retryability; do not let background guest-session fetches create unhandled runtime rejections.
- For this repository, use the rebuilt Docker-local stack as the default development and verification environment for web changes; do not use standalone `next start`/alternate-port workflows as a normal substitute for Docker-local verification.
- Smoke verification should run on the standard Docker-local origin `http://127.0.0.1:3000`; alternate verification ports can invalidate web-to-API behavior because the API CORS allowlist is origin-sensitive.
- If a product change intentionally changes the home-route auth model or other smoke-covered behavior, update `apps/web/e2e/smoke-auth-chat-billing.spec.ts` in the same change; a stale smoke spec is a merge blocker, not an acceptable known failure.
- Smoke coverage for guest-first `/` must send at least one real guest chat message, not just render the guest UI, so missing guest-session bootstrap or missing `WEB_INTERNAL_GUEST_TOKEN` is caught before merge.
- Guest web chat must stay behind the web app boundary: browser traffic should hit a Next.js route, the API guest endpoint must require a server-only token, and the web route should reject non-same-origin requests before forwarding.
- When guest chat is proxied through the web app, preserve guest rate limiting by forwarding the caller IP to the internal API route instead of keying all guests to the web server address.
- When the API runs inside Docker, `OLLAMA_BASE_URL` must point at the Docker service hostname such as `http://ollama:11434`; `127.0.0.1` inside the API container points at the wrong process and makes Ollama appear unhealthy.
- If localhost browser behavior does not match the current source tree or recent unit-test results, rebuild the Docker-local `api` and `web` containers from the current working tree before assuming the fix failed; these services run compiled artifacts and can keep serving stale behavior.
- Keep `WEB_INTERNAL_GUEST_TOKEN` fail-closed in base Compose and deployed environments. Only local dev wrappers such as `pnpm stack:dev` and CI smoke should inject the disposable `dev-web-guest-token`.
- The Supabase CLI uses the root `supabase/migrations/` directory as the live schema source of truth for local bootstrap and CI smoke. If smoke-relevant schema changes land only under `apps/api/supabase/migrations/`, the workflow is incomplete.
- OpenAI-compatible contract requirements apply to the API product surface. The web chat product is a separate analytics/reporting pipeline and must be tagged `web`, while API-product traffic must be tagged `api` and should include a stable API key id when available.
- If authenticated web chat still shares runtime endpoints with the public API temporarily, document that as an explicit gap instead of folding web traffic into API-business analytics.

API key lifecycle lessons that must persist:

- Keep the `/v1/users/me` developer snapshot backward-compatible with the web developer panel's credit summary unless the web contract is updated in the same change; dropping `credits` from that payload breaks the existing dashboard.
- Validate API key expiration input at the route boundary as a real future ISO timestamp before persisting it; invalid date strings can otherwise cause later list/resolve paths to throw when they normalize timestamps.
- Do not dedupe `expired_observed` audit events only in process memory; expiry audit deduplication must survive API restarts and multi-instance reads by checking persisted event state.

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

- Runs for PR changes under `apps/web/**`, `apps/api/**`, `supabase/**`, `docker-compose.yml`, `.env.example`, and workflow file.
- Uses Node `24` (same as primary CI) so pnpm cache keys can be reused across workflows.
- Restores cached Playwright browsers from `~/.cache/ms-playwright` before install.
- Uses `pnpm install --prefer-offline` to maximize dependency cache reuse.
- Uses workflow-level concurrency cancellation to stop stale in-progress smoke runs on the same branch/PR.
- Installs Playwright Chromium, starts the Supabase CLI stack, resets the local schema from repo migrations, starts the required Docker services together, skips Ollama, waits for readiness, and runs smoke spec.
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

Repo lesson:

- In this environment, remote git actions that rely on repository SSH credentials (especially `git push`, and similar remote-mutating git commands) are user-owned. Agents should stop and ask the user to run those commands unless the maintainer explicitly directs otherwise.

Billing/reconciliation lessons that must persist:

- Do not derive payment credits with floating-point truncation such as `Math.trunc(bdtAmount * 100)`; use a shared decimal-safe conversion helper and keep regression coverage for common 2-decimal amounts such as `19.99 BDT -> 1999 credits`.
- Payment reconciliation must verify payment-ledger evidence in addition to `payment_intents.status` and `minted_credits`; a credited intent without the corresponding `credit_ledger` payment entry is still drift.
- Reconciliation lookback queries must expand to all rows linked to affected `intent_id` values; filtering intents, events, and ledger entries independently by timestamp creates false drift alerts at the lookback boundary.
- The payment reconciliation scheduler is process-local today. Enable it on only one API instance in multi-replica deployments until cross-instance coordination exists.
- Start the payment reconciliation job immediately when the scheduler starts; do not wait a full interval before the first scan after deployment or restart.

Never force-push rewritten history unless explicitly required and safe.

## Documentation Discipline

- If behavior changes, update docs in the same change.
- Update `README.md` for user-facing changes.
- Update `CHANGELOG.md` for all notable changes (Added, Changed, Deprecated, Removed, Fixed, Security).
- Update `docs/` runbooks/architecture/plans for operational or architectural changes.
- Prefer concrete examples (commands, payloads, env vars) over abstract prose.
- Keep docs synchronized with implementation.
- Keep `docs/plans/` root limited to currently in-flight session plans only (for example: the handful of dated plans being actively executed right now). When a plan is no longer the active execution surface, move it out of the root into `docs/plans/completed/` (for finished work) or `docs/plans/active/` (for long-lived tracks), rather than leaving old plans in the root.
- Before creating a new plan under `docs/plans/YYYY-MM-DD-<task-name>.md`, quickly audit the root of `docs/plans/` and relocate any obviously stale plans so that only truly current work remains there.

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
