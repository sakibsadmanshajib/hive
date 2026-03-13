# Unified Local Stack Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add a single-command hot-reload full-stack workflow for Hive and standardize the related onboarding and PR hygiene docs.

**Architecture:** Keep the existing production-like Docker Compose file as the baseline, add a dev override for watch-mode API/web services, and expose a root orchestration script that starts Supabase CLI, reads local keys, and launches the dev stack consistently. Update docs so `README.md` becomes the canonical getting-started and daily-development guide.

**Tech Stack:** pnpm, bash, Docker Compose, Supabase CLI, Fastify, Next.js, Markdown docs

---

### Task 1: Inspect current stack scripts and compose boundaries

**Files:**
- Modify: `package.json`
- Modify: `README.md`
- Reference: `docker-compose.yml`
- Reference: `apps/api/package.json`
- Reference: `apps/web/package.json`

**Step 1: Confirm current script and compose state**

Run:

```bash
cat package.json
cat apps/api/package.json
cat apps/web/package.json
sed -n '1,260p' docker-compose.yml
```

**Step 2: Define final root script names**

Change:
- plan root script names for stack startup/shutdown/reset
- keep existing `dev`, `build`, `test`, `lint` unless a rename is clearly necessary

**Step 3: Verify the planned command surface is documented before editing**

Run:

```bash
rg -n "stack:dev|stack:down|stack:reset" package.json README.md CONTRIBUTING.md docs || true
```

**Step 4: Commit**

Commit later with related script and docs changes.

### Task 2: Add a repo-owned stack orchestrator

**Files:**
- Create: `tools/dev/stack-dev.sh`
- Modify: `package.json`

**Step 1: Write the script**

Change:
- start `npx supabase start`
- read `API_URL`, `ANON_KEY`, `SERVICE_ROLE_KEY` from `npx supabase status -o env`
- export:
  - `SUPABASE_URL`
  - `NEXT_PUBLIC_SUPABASE_URL`
  - `SUPABASE_SERVICE_ROLE_KEY`
  - `NEXT_PUBLIC_SUPABASE_ANON_KEY`
- start Docker Compose with the dev override

**Step 2: Add root scripts**

Change:
- add `stack:dev`
- add `stack:down`
- add `stack:reset` if kept in scope

**Step 3: Verify script syntax**

Run:

```bash
bash -n tools/dev/stack-dev.sh
cat package.json
```

**Step 4: Commit**

```bash
git add package.json tools/dev/stack-dev.sh
git commit -m "chore(dev): add unified local stack runner"
```

### Task 3: Add a Docker Compose dev override for hot reload

**Files:**
- Create: `docker-compose.dev.yml`
- Modify: `docker-compose.yml` only if strictly necessary

**Step 1: Define watch-mode API service**

Change:
- mount repo source into the API container
- run `pnpm --filter @hive/api dev`
- preserve required env vars and network behavior

**Step 2: Define watch-mode web service**

Change:
- mount repo source into the web container
- run `pnpm --filter @hive/web dev -- --hostname 0.0.0.0 --port 3000`
- preserve required public env vars

**Step 3: Verify compose config renders**

Run:

```bash
docker compose -f docker-compose.yml -f docker-compose.dev.yml config
```

**Step 4: Commit**

```bash
git add docker-compose.dev.yml
git commit -m "chore(dev): add hot-reload compose override"
```

### Task 4: Standardize onboarding and development docs

**Files:**
- Modify: `README.md`
- Modify: `CONTRIBUTING.md`
- Modify: `docs/README.md`
- Modify: `docs/runbooks/README.md`
- Modify: `docs/architecture/system-architecture.md`
- Modify: `docs/runbooks/active/web-e2e-smoke.md`

**Step 1: Rewrite canonical getting-started flow**

Change:
- make `README.md` the canonical first-time setup and daily-development entry
- explain the two Docker-managed systems clearly
- explain when to use the unified stack command

**Step 2: Tighten workflow and PR hygiene docs**

Change:
- keep `CONTRIBUTING.md` focused on workflow and PR expectations
- document scoped PR title guidance

**Step 3: Reduce duplicate setup guidance in doc indexes**

Change:
- point `docs/README.md` and `docs/runbooks/README.md` at the canonical onboarding flow

**Step 4: Verify discoverability**

Run:

```bash
rg -n "stack:dev|Supabase CLI|Hive Compose file|PR title|Getting Started|Daily Development" README.md CONTRIBUTING.md docs/README.md docs/runbooks/README.md docs/architecture/system-architecture.md docs/runbooks/active/web-e2e-smoke.md
```

**Step 5: Commit**

```bash
git add README.md CONTRIBUTING.md docs/README.md docs/runbooks/README.md docs/architecture/system-architecture.md docs/runbooks/active/web-e2e-smoke.md
git commit -m "docs(readme): standardize local development workflow"
```

### Task 5: Update changelog and PR metadata

**Files:**
- Modify: `CHANGELOG.md`
- Live PR: `#52`

**Step 1: Update changelog**

Change:
- record unified local development workflow and docs/process cleanup

**Step 2: Update PR title**

Change:
- rename PR `#52` to the approved umbrella scoped title

**Step 3: Verify**

Run:

```bash
rg -n "local development|Supabase CLI|PR hygiene|stack:dev" CHANGELOG.md
gh pr view 52 --json title
```

**Step 4: Commit**

```bash
git add CHANGELOG.md
git commit -m "docs(changelog): record local workflow standardization"
```

### Task 6: Verify the final workflow

**Files:**
- Verify all touched files

**Step 1: Verify API build**

Run:

```bash
pnpm --filter @hive/api build
```

**Step 2: Verify web production build with real local Supabase key**

Run:

```bash
NEXT_PUBLIC_API_BASE_URL=http://127.0.0.1:8080 \
NEXT_PUBLIC_SUPABASE_URL=http://127.0.0.1:54321 \
NEXT_PUBLIC_SUPABASE_ANON_KEY="$(npx supabase status -o env | sed -n 's/^ANON_KEY=\"\\(.*\\)\"$/\\1/p')" \
pnpm --filter @hive/web build
```

**Step 3: Verify compose rendering**

Run:

```bash
docker compose -f docker-compose.yml -f docker-compose.dev.yml config
```

**Step 4: Inspect final diff and status**

Run:

```bash
git diff --stat
git status --short
```

**Step 5: Commit final fixups if needed**

```bash
git add <touched-files>
git commit -m "docs(dev): finalize unified local stack guidance"
```
