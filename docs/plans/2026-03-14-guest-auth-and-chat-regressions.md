## Goal
Fix the current guest/auth chat regressions on the Docker-local stack so guest mode renders as guest, guest-session linking succeeds after sign-in, authenticated `guest-free` chat no longer fails with a 500, and the current chat-state persistence behavior is explicit and covered by tests.

## Assumptions
- Assume the "Unknown user" label and logout button in guest mode are UI gating bugs caused by rendering authenticated account chrome during guest sessions.
- Assume the guest-session link failure and authenticated `guest-free` 500 share the same backend root cause: Supabase-authenticated users can reach Hive before the required `user_profiles` row exists, so downstream inserts into `guest_user_links` and `usage_events` fail foreign-key checks.
- Assume "guest chat is being persisted" refers to incorrect guest/auth chat-state carryover in the web app, not a request for new durable server-side chat history.
- Assume "logged-in chat is not being persisted either" does not mean a regression in an existing persistence layer; this repository currently has no durable chat conversation store, so the implementation should only fix verified state-leakage bugs and make the current non-persistent behavior explicit.

## Plan
1. Files: `apps/web/test/chat-guest-mode.test.tsx`, `apps/web/test/chat-auth-gate.test.tsx`
   Change: Add failing web tests that prove guest mode must not render authenticated profile chrome (`Signed in`, `Unknown user`, `Log out`) and that guest/auth transitions do not keep incorrect guest/auth chat-state visible.
   Verify: `pnpm --filter @hive/web exec vitest run apps/web/test/chat-guest-mode.test.tsx apps/web/test/chat-auth-gate.test.tsx`

2. Files: `apps/web/src/app/page.tsx`, `apps/web/src/features/chat/components/chat-workspace-shell.tsx`, `apps/web/src/features/account/components/profile-menu.tsx`
   Change: Pass guest-mode state into the workspace shell and render guest-safe top-bar actions instead of the authenticated profile menu during guest sessions; if the new tests prove state leakage, reset or isolate the affected client chat state at the guest/auth boundary with the smallest possible UI change.
   Verify: `pnpm --filter @hive/web exec vitest run apps/web/test/chat-guest-mode.test.tsx apps/web/test/chat-auth-gate.test.tsx`

3. Files: `apps/api/test/domain/supabase-auth-service.test.ts`, `apps/api/test/routes/guest-attribution-route.test.ts`, `apps/api/test/domain/runtime-chat-billing.test.ts`, `apps/api/test/routes/chat-completions-route.test.ts`
   Change: Add failing API tests that prove session-authenticated users are bootstrapped before guest-link and usage writes, and that authenticated `guest-free` requests return success without billing or foreign-key 500s.
   Verify: `pnpm --filter @hive/api exec vitest run apps/api/test/domain/supabase-auth-service.test.ts apps/api/test/routes/guest-attribution-route.test.ts apps/api/test/domain/runtime-chat-billing.test.ts apps/api/test/routes/chat-completions-route.test.ts`

4. Files: `apps/api/src/runtime/supabase-auth-service.ts`, `apps/api/src/runtime/supabase-user-store.ts`, `apps/api/src/runtime/services.ts`, `apps/api/src/domain/types.ts`
   Change: Enrich session-principal resolution with the auth user's profile fields and idempotently ensure the required local user/profile record exists before guest-link and usage persistence paths run; keep the public OpenAI-compatible API contract unchanged.
   Gap: Authenticated web chat still shares runtime endpoints with the public API in this slice, so execution remains on the public OpenAI-compatible path while analytics/reporting separate `web` traffic as a temporary known gap.
   Verify: `pnpm --filter @hive/api exec vitest run apps/api/test/domain/supabase-auth-service.test.ts apps/api/test/routes/guest-attribution-route.test.ts apps/api/test/domain/runtime-chat-billing.test.ts apps/api/test/routes/chat-completions-route.test.ts`

5. Files: `apps/web/test/guest-session-link-route.test.ts`, `apps/web/e2e/smoke-auth-chat-billing.spec.ts`, `apps/api/test/routes/guest-chat-route.test.ts`
   Change: Add or tighten regression coverage around guest-session link handoff and the real guest/auth chat flow so the Docker-local smoke suite catches guest-mode chrome regressions, broken guest linking, and authenticated `guest-free` failures.
   Verify: `pnpm --filter @hive/web exec vitest run apps/web/test/guest-session-link-route.test.ts` and `docker compose up --build -d` and `pnpm --filter @hive/web test:e2e -- e2e/smoke-auth-chat-billing.spec.ts`

6. Files: `CHANGELOG.md`, `README.md`, `docs/runbooks/active/web-e2e-smoke.md`
   Change: Document the guest/auth regression fixes, note the current non-goal around durable chat-history persistence, and keep the Docker-local verification/runbook steps aligned with the new behavior.
   Verify: `pnpm --filter @hive/api test` and `pnpm --filter @hive/api build` and `pnpm --filter @hive/web test` and `pnpm --filter @hive/web build`

## Risks & mitigations
- Risk: Bootstrapping user-profile rows inside session-auth flows can accidentally change other authenticated paths.
  Mitigation: Keep the bootstrap idempotent, narrow it to session-authenticated users, and cover the affected routes/services with targeted tests before running the full API suite.
- Risk: The "persistence" wording may hide a broader product request for durable logged-in chat history.
  Mitigation: Limit this change to verified regressions and document that durable conversation history is not currently implemented in this repository.
- Risk: Web guest/auth fixes can pass unit tests but still fail in the real Docker-local stack because of cookie, origin, or guest-link timing behavior.
  Mitigation: Rebuild the Docker stack and run the smoke spec after the targeted tests pass.

## Rollback plan
- Revert the guest-mode UI gating change if it causes authenticated navigation regressions while keeping the new failing tests as a guardrail.
- Revert the session-profile bootstrap wiring independently if it introduces broader auth issues, then restore the previous behavior and keep the new failing API tests to preserve the root-cause evidence.
- If smoke uncovers a broader product mismatch around conversation persistence, stop before adding schema or new storage and split that work into a separate planned task.
