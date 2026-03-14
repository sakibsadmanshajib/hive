---
name: docker-local-smoke-validation
description: Use when verifying Hive web auth, chat, billing, or settings changes that need Playwright smoke coverage against the rebuilt Docker-local stack.
---

# Docker-Local Smoke Validation

## Overview

Hive web development and smoke verification should run against the rebuilt Docker-local stack by default. Standalone local app servers and alternate ports are not the normal workflow because the web-to-API path is origin-sensitive.

## When to Use

- Web changes touch auth, chat, billing, settings, guest flow, or browser-to-API integration
- A Playwright smoke result needs to reflect the current working tree, not an older running container
- You are about to reach for a standalone local server or alternate port to verify behavior

## Required Flow

1. Rebuild the local stack from the current working tree:
   - `docker compose down`
   - `docker compose up --build -d`
   - `docker compose ps`
2. Verify readiness on standard ports:
   - `curl -s http://127.0.0.1:8080/health`
   - `curl -sI http://127.0.0.1:3000/auth`
3. Read the live local Supabase anon key:
   - `npx supabase status -o env`
4. Run smoke against the default origin:
   - `E2E_SUPABASE_ANON_KEY="<ANON_KEY>" pnpm --filter @hive/web test:e2e -- e2e/smoke-auth-chat-billing.spec.ts`

## Rules

- Do not treat smoke results from a stale existing container as validation for the current diff.
- Do not switch to standalone local app servers or alternate ports as a normal workaround; rebuild Docker and verify there instead.
- If smoke fails because the expected product behavior changed, update the smoke spec in the same change.

## Common Mistakes

- Running Playwright against an old Docker container and assuming it reflects the current working tree
- Using a sidecar `next start` port that bypasses normal Docker/CORS assumptions
- Forgetting `E2E_SUPABASE_ANON_KEY` and misclassifying fixture setup failures as app regressions
