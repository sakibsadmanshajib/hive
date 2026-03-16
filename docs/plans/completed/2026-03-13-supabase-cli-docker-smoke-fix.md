# Supabase CLI And Docker Smoke Fix

## Status (Superseded in part)

This plan reflects an intermediate direction. The current CI smoke workflow (`.github/workflows/web-e2e-smoke.yml`) runs a small local Ollama model for `guest-free` rather than skipping Ollama entirely. Treat the "skip Ollama" portions below as historical intent; the canonical operational behavior is documented in `docs/runbooks/active/web-e2e-smoke.md`.

## Goal
Align local and CI smoke orchestration around the real Supabase CLI project plus the Docker app stack, make the root `supabase/migrations/` directory the schema source of truth for smoke-critical guest/session tables, and explicitly skip Ollama in the GitHub smoke workflow.

## Assumptions
- The root `supabase/` project is the only Supabase CLI project used by contributors and GitHub Actions.
- `supabase/migrations/` should be the schema source of truth for local bootstrap and CI resets.
- The smoke workflow should validate guest session bootstrap, guest free-model chat, auth, and billing UX without waiting for Ollama.
- Local development should still support Ollama through Docker Compose, but CI smoke should not start or wait on it.
- Public/runtime URLs should not be hardcoded to loopback values inside application code; production-facing URL resolution should come from env or request context, while CI may still inject explicit local test URLs at the workflow boundary.

## Plan
1. Files: `supabase/migrations/`, `apps/api/supabase/migrations/`
   Change: Reconcile the missing guest attribution and usage reporting SQL into the root Supabase CLI migration path so `npx supabase db reset --yes` creates the tables required by guest session bootstrap and analytics.
   Verify: `rg -n "guest_sessions|guest_usage_events|guest_user_links|channel text|api_key_id" supabase/migrations apps/api/supabase/migrations`

2. Files: `.github/workflows/web-e2e-smoke.yml`
   Change: Tighten the smoke workflow so it explicitly starts Supabase CLI, resets the schema, exports live Supabase env, starts only the needed Docker services, and skips Ollama entirely.
   Verify: `sed -n '1,260p' .github/workflows/web-e2e-smoke.yml`

3. Files: `docker-compose.yml`, `.env.example`, `apps/web/src/lib/api.ts`, `apps/web/src/app/api/guest-session/route.ts`, `apps/web/src/app/api/chat/guest/route.ts`, `apps/web/src/app/api/guest-session/link/route.ts`, `apps/web/src/app/api/guest-session/request.ts`
   Change: Keep Docker-local runtime wiring consistent with the documented workflow without hardcoding production-facing loopback URLs in app logic by using env-driven internal service discovery for server-side web routes, requiring the web internal guest token in both services, and preserving loopback alias handling only for local smoke origin validation.
   Verify: `pnpm --filter @hive/web exec vitest run test/public-env-lazy.test.ts test/guest-session-route.test.ts test/guest-chat-route.test.ts test/guest-session-link-route.test.ts`

4. Files: `apps/web/e2e/smoke-auth-chat-billing.spec.ts`
   Change: Ensure the smoke spec covers real guest-session bootstrap and guest free-model chat so the workflow fails if Supabase schema or web-to-API guest routing regresses.
   Verify: `sed -n '1,260p' apps/web/e2e/smoke-auth-chat-billing.spec.ts`

5. Files: `README.md`, `docs/runbooks/active/web-e2e-smoke.md`, `docs/architecture/system-architecture.md`, `AGENTS.md`, `CHANGELOG.md`
   Change: Document that Supabase CLI and Docker Compose work in conjunction for local and CI smoke, that root `supabase/migrations/` is the live CLI schema path, that public/runtime URLs should be env- or request-derived rather than hardcoded, and that Ollama is skipped in CI smoke on purpose.
   Verify: `rg -n "Supabase CLI|Docker|conjunction|Ollama|skip|supabase/migrations|guest-session|WEB_INTERNAL_GUEST_TOKEN" README.md docs/runbooks/active/web-e2e-smoke.md docs/architecture/system-architecture.md AGENTS.md CHANGELOG.md`

6. Files: working tree runtime and workflow paths above
   Change: Run the verification flow against the actual Docker-plus-Supabase stack, then rerun the focused web tests and smoke spec.
   Verify: `pnpm --filter @hive/web exec vitest run test/public-env-lazy.test.ts test/guest-session-route.test.ts test/guest-chat-route.test.ts test/guest-session-link-route.test.ts && docker compose down && npx supabase start && npx supabase db reset --yes && set -a && source <(npx supabase status -o env) && set +a && NEXT_PUBLIC_API_BASE_URL=http://127.0.0.1:8080 NEXT_PUBLIC_SUPABASE_URL=$API_URL NEXT_PUBLIC_SUPABASE_ANON_KEY=$ANON_KEY SUPABASE_SERVICE_ROLE_KEY=$SERVICE_ROLE_KEY WEB_INTERNAL_GUEST_TOKEN=dev-web-guest-token docker compose up --build -d redis langfuse-db langfuse api web --no-deps && pnpm --filter @hive/web test:e2e -- e2e/smoke-auth-chat-billing.spec.ts`

## Risks & mitigations
- Risk: duplicating or moving migrations incorrectly can make the root Supabase history diverge from app-local copies.
  Mitigation: reconcile the SQL carefully and verify the required guest/usage objects exist in the root path before running resets.
- Risk: CI smoke could still depend on implicit localhost/container assumptions.
  Mitigation: keep the internal API base explicit for server-side web routes and preserve the loopback normalization tests.
- Risk: documentation drifts from the real bootstrap path again.
  Mitigation: update the workflow file and the operator-facing docs in the same change, and add AGENTS guidance so the lesson persists.

## Rollback plan
- Revert the workflow and docs changes if the new orchestration proves unstable.
- Revert the migration-path reconciliation if it causes unexpected local schema conflicts, then restore the previous smoke behavior while reworking the Supabase project layout separately.
