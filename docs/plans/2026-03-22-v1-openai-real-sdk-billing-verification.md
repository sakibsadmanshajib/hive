## Goal

Refresh the v1 OpenAI API Compliance milestone around live public-API evidence: run a real Docker-local verification loop with the OpenAI Node SDK against Hive's local API using a disposable local user and Hive-issued API key, prove request/response/error compatibility plus API-key metering and billing behavior, and document the results in repo artifacts that can support milestone closure.

## Assumptions

- `.env` contains a usable `OPENROUTER_API_KEY`, and the configured OpenRouter free model is callable from this machine.
- The maintainer approves starting the local Supabase and Docker stack and making real upstream OpenRouter calls from the local dev environment.
- A disposable local Supabase auth user, API key, payment intent, and credit balance may be created on this machine for verification.
- If live verification exposes product gaps, we can patch code, tests, and docs in the same task after approval instead of stopping at an audit note.
- The final documentation should distinguish OpenAI-compatible behavior from Hive-specific ownership, billing, and differentiator behavior.

## Plan

1. **Stack bootstrap and provider readiness**
   - **Files**: `tools/dev/stack-dev.sh`, `tools/dev/stack-dev-latest.sh`, `docker-compose.yml`, `docker-compose.dev.yml`, `.env`, `.planning/ROADMAP.md`, `.planning/v1.0-MILESTONE-AUDIT.md`
   - **Change**: Start the approved Docker-local stack (local Supabase plus Docker `api`/`web` services), confirm the API and web origins, and capture provider-readiness evidence for the configured OpenRouter free model so the milestone report is anchored to a real running environment.
   - **Verify**: `pnpm stack:dev`; `docker compose ps`; `curl -s http://127.0.0.1:8080/health`; `curl -s http://127.0.0.1:8080/v1/providers/status`

2. **Create a disposable local user and issue a Hive API key**
   - **Files**: `apps/api/src/routes/users.ts`, `apps/api/src/routes/auth.ts`, `apps/api/src/runtime/services.ts`, `apps/api/src/runtime/supabase-user-store.ts`, `apps/api/src/runtime/supabase-api-key-store.ts`, `supabase/migrations/20260223000001_auth_user_tables.sql`, `supabase/migrations/20260315000100_auth_user_sync_trigger.sql`
   - **Change**: Create a disposable local auth user through the local Supabase auth surface, sign in to obtain a real session token, call Hive's `/v1/users/me` and `/v1/users/api-keys` routes, and create a Hive API key through Hive's own route so the later SDK call uses the same public ownership path the product exposes.
   - **Verify**: `curl` to local Supabase auth signup or admin-user endpoints; `curl -s http://127.0.0.1:8080/v1/users/me -H "Authorization: Bearer <session-token>"`; `curl -s http://127.0.0.1:8080/v1/users/api-keys -H "Authorization: Bearer <session-token>"`; `curl -s -X POST http://127.0.0.1:8080/v1/users/api-keys -H "Authorization: Bearer <session-token>" -H "content-type: application/json" --data '{"nickname":"sdk-local-test","scopes":["chat","usage","billing"]}'`

3. **Fund the same user through the local billing path**
   - **Files**: `apps/api/src/routes/payment-intents.ts`, `apps/api/src/routes/payment-demo-confirm.ts`, `apps/api/src/runtime/services.ts`, `apps/api/src/runtime/supabase-billing-store.ts`, `apps/api/src/routes/credits-balance.ts`, `supabase/migrations/20260223000003_billing_tables.sql`, `supabase/migrations/20260223000004_billing_rpcs.sql`
   - **Change**: Fund the disposable user through the local payment-intent plus demo-confirm path so the credit balance is created by the same billing flow the API owns; fall back to a controlled service-role top-up only if the demo path is blocked by local bootstrap issues.
   - **Verify**: `curl -s -X POST http://127.0.0.1:8080/v1/payments/intents -H "Authorization: Bearer <session-token>" -H "content-type: application/json" --data '{"bdt_amount":50,"provider":"bkash"}'`; `curl -s -X POST http://127.0.0.1:8080/v1/payments/demo/confirm -H "Authorization: Bearer <session-token>" -H "content-type: application/json" --data '{"intent_id":"<intent-id>"}'`; `curl -s http://127.0.0.1:8080/v1/credits/balance -H "Authorization: Bearer <hive-api-key>"`

4. **Run a real OpenAI SDK chat call against Hive's public API**
   - **Files**: `apps/api/test/openai-sdk-regression.test.ts`, `apps/api/src/routes/models.ts`, `apps/api/src/routes/chat-completions.ts`, `apps/api/src/routes/usage.ts`, `packages/openapi/openapi.yaml`
   - **Change**: Execute a real OpenAI Node SDK script against `http://127.0.0.1:8080` using the Hive API key, first proving `models.list()` and `models.retrieve()` and then sending at least one real `chat.completions.create()` request against the configured OpenRouter free chat model; capture the SDK result shape, headers, and any parsing behavior from the actual client.
   - **Verify**: `node` or `tsx` script using the `openai` SDK with `baseURL=http://127.0.0.1:8080/v1`; `curl -si http://127.0.0.1:8080/v1/models -H "Authorization: Bearer <hive-api-key>"`; compare SDK output with OpenAI-style response fields and expected DIFF headers

5. **Prove metering and billing attribution on the same real request**
   - **Files**: `apps/api/src/runtime/services.ts`, `apps/api/src/runtime/supabase-billing-store.ts`, `apps/api/src/routes/credits-balance.ts`, `apps/api/src/routes/usage.ts`, `supabase/migrations/20260314000300_usage_reporting_channels.sql`
   - **Change**: Validate metering and billing on the same API-key path by comparing pre/post balances, `/v1/usage` output, and persisted records (`credit_accounts`, `credit_ledger`, `usage_events`, `api_keys`, and relevant payment rows) so we can prove the API request is attributed to Hive's API key id and charged the intended amount.
   - **Verify**: `curl -s http://127.0.0.1:8080/v1/credits/balance -H "Authorization: Bearer <hive-api-key>"` before and after the SDK call; `curl -s http://127.0.0.1:8080/v1/usage -H "Authorization: Bearer <hive-api-key>"`; service-role SQL or REST reads against `credit_accounts`, `credit_ledger`, `usage_events`, `api_keys`, `payment_intents`, and `payment_events` for the disposable user

6. **Refresh milestone artifacts and add a durable real-integration report**
   - **Files**: `.planning/phases/11-real-openai-sdk-regression-tests-ci-style-e2e/11-VERIFICATION.md`, `.planning/v1.0-MILESTONE-AUDIT.md`, `.planning/PROJECT.md`, `docs/runbooks/active/api-key-lifecycle.md`, `docs/runbooks/active/credit-ledger-audit.md`, `CHANGELOG.md`
   - **Change**: Update the stale milestone artifacts and add durable documentation covering the real local SDK flow, the exact endpoints exercised, how the Hive-issued API key owns the request for metering, which parts are OpenAI-compatible, which parts are intentionally Hive-specific, and what real-integration evidence was captured.
   - **Verify**: `rg -n "real OpenAI SDK|OpenRouter|api_key_id|x-actual-credits|usage_events|368/368" .planning docs CHANGELOG.md`

7. **Close any live gaps and checkpoint the result**
   - **Files**: Any touched source, tests, planning artifacts, or docs from steps 1-6
   - **Change**: If live verification finds request, response, error-shape, SDK-compatibility, or billing-attribution gaps, implement the minimal code and regression changes needed to close them, then refresh the report and milestone artifacts so they match the verified behavior.
   - **Verify**: `pnpm --filter @hive/api test`; `docker compose exec api sh -c "cd /app && pnpm --filter @hive/api build"`; rerun the real SDK and balance/usage checks from steps 4-5; `git status --short`

## Risks & mitigations

- **Risk**: The local environment cannot start because Supabase CLI or Docker bootstrap is missing or blocked.
  - **Mitigation**: Verify bootstrap prerequisites first; if `npx supabase` must be downloaded or Docker needs approval, request escalation immediately and document the exact prerequisite gap in the plan execution notes.
- **Risk**: The configured OpenRouter free model changes or the upstream key is invalid, which would make a "real integration" failure ambiguous.
  - **Mitigation**: Capture provider-status output and the exact configured free model before the SDK call; if the upstream is unavailable, separate bootstrap or provider failure from Hive API contract failure in the report.
- **Risk**: Creating a user or key directly in the database would bypass the product surface and weaken the evidence.
  - **Mitigation**: Prefer the real Supabase-auth plus `/v1/users/api-keys` route; only use controlled service-role intervention for credit seeding if the payment-confirm path is the blocker, and call that out explicitly in the report.
- **Risk**: Billing evidence looks correct at the API boundary but the persisted attribution is missing `api_key_id`, wrong `channel`, or mismatched ledger rows.
  - **Mitigation**: Require both API-level evidence (`/v1/credits/balance`, `/v1/usage`, headers) and storage-level evidence (`usage_events`, `credit_ledger`, `payment_intents`, `api_keys`) before declaring the milestone aligned.
- **Risk**: Existing milestone docs stay internally inconsistent even if the live stack passes.
  - **Mitigation**: Refresh the stale Phase 11 verification artifact, milestone audit, and `PROJECT.md` in the same change so the planning surface matches the live verification result.

## Rollback plan

- If stack bootstrap or verification changes create unwanted churn, stop the local stack with `docker compose down` and `npx supabase stop`, then inspect `git status` before reverting files.
- Revert only the planning and documentation artifacts first if the code remains valid: `git restore .planning docs CHANGELOG.md`.
- If source changes were required and need to be undone, revert the specific files or the checkpoint commit with `git revert <commit>` so the previous milestone state remains recoverable.
