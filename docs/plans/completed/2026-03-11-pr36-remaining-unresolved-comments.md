# PR #36 Remaining Unresolved Comments Plan

## Goal
Implement the last substantive unresolved PR #36 review item by keeping the custom web auth session synchronized with live Supabase session/token refreshes so session-auth API calls do not start using stale bearer tokens.

## Assumptions
- The unresolved code issue still in scope is the auth token refresh/session synchronization gap called out on `apps/web/src/app/auth/page.tsx`.
- Other currently unresolved review threads were either already addressed in earlier commits or are metadata/process issues rather than new implementation work.
- The preferred outcome is to preserve the existing custom `AUTH_STORAGE_KEY` session shape while making it react to Supabase auth state changes instead of only login/signup writes.
- The current branch should remain the working branch; no worktree is required unless the maintainer later asks for isolation.

## Plan
1. Files: `apps/web/src/features/auth/auth-session.ts`, `apps/web/src/lib/supabase-client.ts`, `apps/web/src/app/auth/page.tsx`
   Change: Design a single source of truth for browser auth session synchronization by adding a small auth-session sync hook/helper around Supabase `onAuthStateChange()` / `getSession()` that updates the custom storage entry whenever the Supabase session changes or refreshes.
   Verify: `rg -n "writeAuthSession|readAuthSession|onAuthStateChange|getSession|AUTH_STORAGE_KEY" apps/web/src`

2. Files: `apps/web/src/app/auth/page.tsx`, `apps/web/src/features/account/components/profile-menu.tsx`, `apps/web/src/features/chat/use-chat-session.ts`, `apps/web/src/app/developer/page.tsx`, `apps/web/src/app/settings/page.tsx`
   Change: Replace one-time token reads that can go stale with subscription-driven or fresh session reads so API callers derive `accessToken` from synchronized auth state instead of cached `useState` initializers.
   Verify: `pnpm --filter @hive/web exec vitest run test/auth-page.test.tsx test/profile-menu.test.tsx test/chat-auth-gate.test.tsx test/review-feedback-pages.test.tsx`

3. Files: `apps/web/test/auth-session.test.ts`, `apps/web/test/auth-page.test.tsx`, `apps/web/test/review-feedback-pages.test.tsx`, optional new targeted test file under `apps/web/test/`
   Change: Add regression coverage for token refresh/session sync behavior, including updating the stored access token after a simulated Supabase auth-state change and ensuring session-dependent pages use the refreshed token.
   Verify: `pnpm --filter @hive/web exec vitest run test/auth-session.test.ts test/auth-page.test.tsx test/review-feedback-pages.test.tsx`

4. Files: `apps/web/src/lib/api.ts`, `apps/web/src/lib/supabase-client.ts`, `apps/web/test/public-env-lazy.test.ts`, affected web auth/session files above
   Change: Re-run and, if needed, adjust the lazy public-env access pattern so the token-refresh fix does not reintroduce prerender/build-time failures.
   Verify: `NEXT_PUBLIC_API_BASE_URL=http://127.0.0.1:8080 NEXT_PUBLIC_SUPABASE_URL=http://127.0.0.1:54321 NEXT_PUBLIC_SUPABASE_ANON_KEY=test-supabase-anon-key pnpm --filter @hive/web build`

5. Files: `apps/web/e2e/fixtures/auth.ts`, `apps/web/e2e/smoke-auth-chat-billing.spec.ts`, affected web auth/session files above
   Change: Verify the end-to-end guarded auth/chat/settings flow still works with the synchronized session handling and that seeded sessions remain compatible with the custom auth storage contract.
   Verify: `E2E_BASE_URL=http://127.0.0.1:3001 E2E_API_BASE_URL=http://127.0.0.1:8080 E2E_ALLOW_DEV_TOKEN_FALLBACK=true pnpm --filter @hive/web test:e2e -- e2e/smoke-auth-chat-billing.spec.ts`

## Risks & mitigations
- Risk: Adding an auth-state listener could create duplicate listeners or race conditions across pages.
  Mitigation: Centralize the synchronization logic in one helper/hook and ensure it returns an unsubscribe cleanup.
- Risk: Switching away from cached `useState` token reads could trigger extra renders or inconsistent initial page state.
  Mitigation: Keep the session shape small, initialize from the current stored session, and only update when the token or profile fields actually change.
- Risk: The fix could reintroduce production-build failures if env validation happens too early.
  Mitigation: Preserve the existing lazy env access pattern and verify with a full env-configured production web build before claiming completion.

## Rollback plan
- Revert the auth-session synchronization commit if it causes navigation churn, duplicate listeners, or broken login/logout behavior.
- Restore the previous session-read behavior while keeping the separate env/workflow fixes intact if the regression is isolated to the sync implementation.
- Re-run `pnpm --filter @hive/web exec vitest run` and the env-configured `pnpm --filter @hive/web build` after rollback to confirm the branch returns to the current verified baseline.
