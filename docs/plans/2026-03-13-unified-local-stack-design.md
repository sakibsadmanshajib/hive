# Unified Local Stack Design

## Goal

Define a single-command local development workflow for Hive that always starts the full real stack while preserving hot reload for API and web development.

## Problem

Hive currently has a fragmented local development story:

- `docker compose up` starts the Hive app stack but not Supabase
- `npx supabase start` starts the Supabase local stack separately
- `pnpm dev` starts local app dev servers but does not match the full stack model
- contributors must manually copy Supabase keys into `.env`
- documentation currently spreads this across multiple partial explanations

This creates onboarding friction and makes the “correct” daily workflow harder to understand than it should be.

## Recommendation

Adopt a repo-owned one-command workflow centered on a hot-reload full stack:

- keep `docker-compose.yml` as the production-like baseline
- add a development override compose file for watch-mode app services
- add root `pnpm` scripts that orchestrate Supabase CLI plus Docker Compose together
- standardize docs so `README.md` is the canonical entry point

## Architecture

### 1. Two Docker-managed systems, one repo-owned workflow

Hive local development uses two distinct runtimes:

- **Hive app stack**
  - `api`
  - `web`
  - `redis`
  - `ollama`
  - `langfuse`
  - `langfuse-db`
  - managed by Docker Compose

- **Supabase local stack**
  - `auth`
  - `db`
  - `kong`
  - `rest`
  - `studio`
  - and related Supabase services
  - managed by `npx supabase start`

Both are Docker-based, but they are controlled by different tools. The docs should say “Supabase is not defined in the Hive Compose file,” not “Supabase is not in Docker.”

### 2. Canonical daily command

Add a root script such as:

- `pnpm stack:dev`

That command should:

1. start or verify the Supabase local stack
2. read `API_URL`, `ANON_KEY`, and `SERVICE_ROLE_KEY` from `npx supabase status -o env`
3. inject those values into the environment used for the Hive stack
4. start Docker Compose with a dev override for hot reload

This becomes the default day-to-day developer entry point.

### 3. Development compose override

Keep the current `docker-compose.yml` for production-like behavior.

Add a dev override file that changes only the app services:

- `api`
  - mount source code
  - run `pnpm --filter @hive/api dev`
- `web`
  - mount source code
  - run `pnpm --filter @hive/web dev -- --hostname 0.0.0.0 --port 3000`

Infra containers stay containerized:

- Redis
- Ollama
- Langfuse
- Langfuse DB

This preserves:

- hot reload
- real service boundaries
- a single stack startup story

### 4. Lifecycle commands

Standardize root scripts around one workflow:

- `pnpm stack:dev` — start full local dev stack with hot reload
- `pnpm stack:down` — stop Hive Compose stack
- `pnpm stack:reset` — optional teardown/reset flow for stale local state
- keep `pnpm build`, `pnpm test`, and existing package-level scripts

## Documentation Model

### README.md

Make this the canonical entry point for:

- first-time setup
- daily development
- Docker vs Supabase explanation
- verification and troubleshooting entry points

### CONTRIBUTING.md

Keep this focused on:

- workflow expectations
- verification expectations
- PR hygiene
- branch/commit/PR scope guidance

### docs/README.md and runbook indexes

Point to the canonical setup flow instead of restating it. Reserve runbooks for:

- smoke testing
- ops troubleshooting
- maintainer workflows

## PR Hygiene

PR titles should be scoped and Conventional-Commit-style where practical, even when a branch spans a docs-heavy umbrella effort.

For PR `#52`, the title should explicitly cover both the audit/docs scope and the runtime/bootstrap fix rather than read like a generic session summary.

Recommended shape:

- `docs(audit): deepen platform review and standardize local auth bootstrap`

## Risks

- Hot reload inside Docker can be slower or more brittle on some host filesystems.
- Next.js dev-in-container may need polling-related tuning if file watching is unreliable.
- Over-automating env sync can obscure where Supabase keys are coming from unless the script output is explicit.

## Mitigations

- keep the production-like compose file untouched
- isolate watch-mode behavior in a dev override
- make the stack script print the resolved Supabase endpoints it is using
- document fallback troubleshooting if file watching is slow

## Expected Outcome

After this change, contributors should be able to:

1. clone the repo
2. run one command
3. get the full local stack with real service boundaries and hot reload
4. follow one canonical getting-started path in the docs
