## Goal

Implement issue `#8` by adding durable API key lifecycle auditability, per-key nicknames and expirations, authenticated key-management endpoints, and a developer-page UI that shows current key state and lifecycle history.

## Assumptions

- The preferred plan-writer helper at `.agent/skills/superpowers-workflow/scripts/write_artifact.py` is unavailable in this repository, so this plan is written directly to `docs/plans/`.
- The accepted design uses the existing `api_keys` table for current state and a new append-only `api_key_events` table for immutable lifecycle history.
- API key management remains user-scoped and session-authenticated; this issue does not add admin cross-user key operations.
- Raw API keys must continue to be returned only at creation time and never persisted in plaintext.
- Expiration is optional per key and should invalidate the key automatically without requiring a background scheduler in this issue.

## Plan

### Step 1

**Files:** `apps/api/src/runtime/supabase-api-key-store.ts`, `apps/api/src/runtime/services.ts`, `apps/api/src/routes/`, `apps/web/src/app/developer/page.tsx`, `apps/web/src/features/billing/components/usage-cards.tsx`, `packages/openapi/openapi.yaml`, `apps/api/supabase/migrations/`

**Change:** Re-read the API-key schema, service flow, route registration, developer-page behavior, and OpenAPI coverage to pin down the exact drift between the intended user-management surface and the currently registered API routes before changing behavior.

**Verify:** `rg -n "api_keys|users/api-keys|users/me|revoke|developer" apps/api/src apps/web/src packages/openapi/openapi.yaml apps/api/supabase/migrations`

### Step 2

**Files:** `apps/api/test/domain/supabase-api-key-store.test.ts`, `apps/api/test/domain/persistent-user-service.test.ts`, `apps/api/test/routes/`, `apps/web/src/app/developer/page.tsx` or adjacent web test files if present

**Change:** Add failing tests that describe the new behavior: nickname and expiration persistence, expired-key rejection, audit-event creation on create/revoke/expiry observation, authenticated list/revoke routes, and developer-page rendering of enriched key metadata.

**Verify:** `pnpm --filter @hive/api exec vitest run apps/api/test/domain/supabase-api-key-store.test.ts apps/api/test/domain/persistent-user-service.test.ts`

### Step 3

**Files:** `apps/api/supabase/migrations/<new-migration>.sql`, `apps/api/supabase/README.md`

**Change:** Add a Supabase migration that extends `public.api_keys` with a stable id, nickname, and expiration timestamp, and creates the new `public.api_key_events` table plus the required indexes and RLS/service-role policies.

**Verify:** `sed -n '1,260p' apps/api/supabase/migrations/<new-migration>.sql`

### Step 4

**Files:** `apps/api/src/domain/types.ts`, `apps/api/src/runtime/supabase-api-key-store.ts`

**Change:** Extend the API-key domain types and Supabase store to read/write the new metadata, reject expired keys during resolution, list derived status fields, and append immutable lifecycle events for create, revoke, and first expired observation.

**Verify:** `pnpm --filter @hive/api exec vitest run apps/api/test/domain/supabase-api-key-store.test.ts apps/api/test/domain/api-key-service.test.ts`

### Step 5

**Files:** `apps/api/src/runtime/services.ts`, `apps/api/test/domain/persistent-user-service.test.ts`

**Change:** Update `PersistentUserService` to create keys with nickname/expiration, return enriched key summaries and event history, and revoke keys by stable key id instead of requiring raw key material for management operations.

**Verify:** `pnpm --filter @hive/api exec vitest run apps/api/test/domain/persistent-user-service.test.ts`

### Step 6

**Files:** `apps/api/src/routes/index.ts`, new or existing user-management route files under `apps/api/src/routes/`, `apps/api/test/routes/`

**Change:** Formalize and register the authenticated user/API-key management routes for `GET /v1/users/me`, `GET /v1/users/api-keys`, `POST /v1/users/api-keys`, and `POST /v1/users/api-keys/:id/revoke`, including request validation and non-secret response payloads.

**Verify:** `pnpm --filter @hive/api exec vitest run apps/api/test/routes`

### Step 7

**Files:** `packages/openapi/openapi.yaml`

**Change:** Document the new session-authenticated API-key management endpoints and payloads, including nickname, expiration, status, and one-time raw key return on creation.

**Verify:** `rg -n "/v1/users/me|/v1/users/api-keys|nickname|expiresAt|revokedAt|status" packages/openapi/openapi.yaml`

### Step 8

**Files:** `apps/web/src/app/developer/page.tsx`, `apps/web/src/features/billing/components/usage-cards.tsx`, any supporting developer-page components added under `apps/web/src/features/developer/`

**Change:** Replace the current “Create API key” flow with a real key-management UI that supports nickname and expiration input, loads the enriched key list/history, shows active/revoked/expired state, and lets the user revoke a specific key.

**Verify:** `pnpm --filter @hive/web build`

### Step 9

**Files:** `README.md`, `docs/README.md`, `docs/architecture/system-architecture.md`, `docs/runbooks/README.md`, `CHANGELOG.md`, and any new or updated runbook under `docs/runbooks/active/`

**Change:** Update docs to reflect the new API-key lifecycle model, developer-page behavior, current/audit data shape, and any operator guidance for investigating revoked or expired keys.

**Verify:** `rg -n "API key|nickname|expiration|expired|revoked|api_key_events|developer" README.md docs/README.md docs/architecture/system-architecture.md docs/runbooks/README.md CHANGELOG.md docs/runbooks/active`

### Step 10

**Files:** `apps/api/src/config/env.ts`, `apps/api/src/domain/types.ts`, `apps/api/src/runtime/supabase-api-key-store.ts`, `apps/api/src/runtime/services.ts`, `apps/api/src/routes/`, `apps/api/test/domain/`, `apps/api/test/routes/`, `apps/web/src/app/developer/page.tsx`, `apps/web/src/features/developer/`, `packages/openapi/openapi.yaml`, `README.md`, `docs/README.md`, `docs/architecture/system-architecture.md`, `docs/runbooks/active/`, `CHANGELOG.md`, `AGENTS.md`

**Change:** Run final verification across touched scopes, review whether implementation revealed any durable repo lesson worth persisting into `AGENTS.md`, and capture exact evidence before claiming completion.

**Verify:** `pnpm --filter @hive/api test && pnpm --filter @hive/api build && pnpm --filter @hive/web build`

## Risks & mitigations

- Risk: adding expiration introduces subtle auth regressions.
  Mitigation: cover expired-key rejection directly in store and auth route tests, and keep expiration checks in the existing lookup path.
- Risk: lifecycle events drift from current key state.
  Mitigation: treat `api_keys` as the source of current validity and `api_key_events` as append-only audit evidence written transactionally with create/revoke operations.
- Risk: the current web/API contract around user-management routes is already incomplete.
  Mitigation: explicitly register and test the key-management routes before relying on the developer page.
- Risk: returning enriched metadata accidentally exposes secret material.
  Mitigation: restrict all read responses to stable id, prefix, nickname, scopes, timestamps, and derived status; return raw key only in create responses.
- Risk: docs and OpenAPI lag the implementation.
  Mitigation: make docs/openapi updates part of the core plan, not a cleanup step.

## Rollback plan

- Revert the API-key lifecycle migration, runtime/service changes, route updates, web UI changes, OpenAPI contract, and docs in one revert if the model needs redesign.
- If the schema must remain but the UI/API shape changes, keep `api_keys` backward-compatible and stop reading from `api_key_events` until a follow-up migration is ready.
- If expiration behavior causes regressions, temporarily treat `expires_at` as display-only by reverting the auth-resolution check while preserving stored metadata for later reintroduction.
