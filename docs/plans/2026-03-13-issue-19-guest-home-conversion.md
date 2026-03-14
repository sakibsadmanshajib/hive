## Goal

Complete the remaining issue `#19` product flow on `/` by showing paid chat models as locked for guests, opening a dismissible combined auth modal when a locked model is chosen, and unlocking paid models in place after authentication.

## Assumptions

- Guest free-chat, guest session attribution, and analytics/reporting separation are already in place.
- Direct API auth behavior and guest backend enforcement remain unchanged in this slice.
- Authenticated web/runtime separation is out of scope here and remains tracked in GitHub issue `#57`.
- The preferred plan writer helper at `.agent/skills/superpowers-workflow/scripts/write_artifact.py` is unavailable in this workspace, so this plan is written directly.

## Plan

1. Files: [apps/web/test/chat-guest-mode.test.tsx](/home/sakib/hive/apps/web/test/chat-guest-mode.test.tsx), [apps/web/test/chat-auth-gate.test.tsx](/home/sakib/hive/apps/web/test/chat-auth-gate.test.tsx)
   Change: add failing web tests for guest-visible locked paid models, locked-model click opening an auth modal, and dismissing the modal without losing guest chat capability.
   Verify: `pnpm --filter @hive/web exec vitest run test/chat-guest-mode.test.tsx test/chat-auth-gate.test.tsx`

2. Files: [apps/web/src/features/chat/use-chat-session.ts](/home/sakib/hive/apps/web/src/features/chat/use-chat-session.ts)
   Change: replace the bare `string[]` model option state with richer chat model metadata and derive guest locked-state behavior from `costType`.
   Verify: `pnpm --filter @hive/web exec vitest run test/chat-guest-mode.test.tsx`

3. Files: [apps/web/src/features/chat/components/message-composer.tsx](/home/sakib/hive/apps/web/src/features/chat/components/message-composer.tsx)
   Change: update the model picker UI to render locked paid models with `Locked` and `Requires account and credits`, and route guest clicks on locked models into an auth-modal open action instead of a model switch.
   Verify: `pnpm --filter @hive/web exec vitest run test/chat-guest-mode.test.tsx test/chat-polish.test.tsx`

4. Files: [apps/web/src/app/auth/page.tsx](/home/sakib/hive/apps/web/src/app/auth/page.tsx), new files under `/home/sakib/hive/apps/web/src/features/auth/components/`
   Change: extract the current combined auth form logic into reusable modal-friendly components without breaking the existing `/auth` page.
   Verify: `pnpm --filter @hive/web exec vitest run test/auth-page.test.tsx`

5. Files: [apps/web/src/app/page.tsx](/home/sakib/hive/apps/web/src/app/page.tsx), new auth modal component files under `/home/sakib/hive/apps/web/src/features/auth/components/`
   Change: mount a dismissible combined auth modal on `/`, open it from locked-model selection, and close it after successful auth while preserving the active guest conversation.
   Verify: `pnpm --filter @hive/web exec vitest run test/chat-guest-mode.test.tsx test/chat-auth-gate.test.tsx test/supabase-auth-sync.test.tsx`

6. Files: [apps/web/src/features/chat/use-chat-session.ts](/home/sakib/hive/apps/web/src/features/chat/use-chat-session.ts), [apps/web/test/chat-guest-mode.test.tsx](/home/sakib/hive/apps/web/test/chat-guest-mode.test.tsx)
   Change: refresh model availability after auth success so paid models unlock in place without navigating away from `/`.
   Verify: `pnpm --filter @hive/web exec vitest run test/chat-guest-mode.test.tsx test/supabase-auth-sync.test.tsx`

7. Files: [README.md](/home/sakib/hive/README.md), [CHANGELOG.md](/home/sakib/hive/CHANGELOG.md), [docs/architecture/system-architecture.md](/home/sakib/hive/docs/architecture/system-architecture.md)
   Change: document the locked-model guest upsell behavior, combined auth modal on `/`, and the fact that this completes the remaining issue `#19` conversion UX without implementing issue `#57`.
   Verify: `pnpm --filter @hive/web test`

8. Files: existing touched web files plus any new auth modal components
   Change: run the required verification for the completed slice and fix any regressions before implementation closeout.
   Verify: `pnpm --filter @hive/web test`
   Verify: `pnpm --filter @hive/api test`
   Verify: `pnpm --filter @hive/api build`
   Verify: `NEXT_PUBLIC_API_BASE_URL=http://127.0.0.1:8080 NEXT_PUBLIC_SUPABASE_URL=http://127.0.0.1:54321 NEXT_PUBLIC_SUPABASE_ANON_KEY=test-supabase-anon-key WEB_INTERNAL_GUEST_TOKEN=test-web-token pnpm --filter @hive/web build`

## Risks & mitigations

- Risk: modal auth reimplements logic and drifts from `/auth`.
  Mitigation: extract shared auth form behavior instead of cloning it.
- Risk: locked-model UI accidentally hides or changes the current free model.
  Mitigation: keep locked clicks side-effect free except for opening the modal, and cover that in tests.
- Risk: auth success updates the session but the model list does not refresh quickly enough.
  Mitigation: derive model options from auth state and add a post-auth unlock test.
- Risk: this slice accidentally starts implementing issue `#57`.
  Mitigation: keep authenticated chat submission on the existing route and limit changes to web UX/state only.

## Rollback plan

- Revert the new auth modal component and locked-model UI changes in the web app.
- Restore guest model filtering to the current free-only picker behavior.
- Keep the already-shipped guest session, guest chat, and analytics/reporting work intact.
- Re-run `pnpm --filter @hive/web test` and the production web build after rollback to confirm the previous guest-first home state is restored.
