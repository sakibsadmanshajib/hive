# Real OpenAI SDK Local Verification

## Purpose

Document the 2026-03-22 Docker-local verification run that exercised Hive's public OpenAI-compatible API with:

- a real local Supabase-authenticated user
- a real Hive-issued API key created through `/v1/users/api-keys`
- a real local payment intent plus demo confirm flow
- the official `openai` Node SDK against `http://127.0.0.1:8080/v1`

This report is local-environment evidence for API contract, billing, and persistence behavior. It is not a claim that any free upstream model is part of the production public API contract.

## Local Verification Scope

- Stack: `pnpm stack:dev` with Docker-local `api` and `web`
- Public API base URL: `http://127.0.0.1:8080/v1`
- Local Supabase URL: `http://127.0.0.1:54321`
- Verification-only embedding model gate: `OPENROUTER_FREE_EMBEDDING_MODEL=nvidia/llama-nemotron-embed-vl-1b-v2:free`
- Commit used for the local verification-model fix: `0e96ab8`

The embedding-model env is enabled only in [`docker-compose.dev.yml`](/home/sakib/hive/docker-compose.dev.yml). Base Compose and production-like configs do not enable it by default.

## User And API Key Bootstrap

Disposable local-user flow on 2026-03-22:

1. Create a local Supabase user through `POST /auth/v1/signup`
2. Use the Supabase bearer token against `GET /v1/users/me`
3. Create a Hive API key through `POST /v1/users/api-keys`
4. Fund the same user through:
   - `POST /v1/payments/intents`
   - `POST /v1/payments/demo/confirm`

Observed local results:

- User bootstrap succeeded through the real auth path
- API key creation succeeded through Hive's own session-authenticated route
- Payment intent `intent_anssilirql` credited `5000` local test credits from `50 BDT`
- API key id persisted as `42a42fde-289e-4264-a8bc-cfcb32fae0e5`

## Public API Results

### Models

- `GET /v1/models` returned `16` model entries
- The catalog included:
  - `openrouter/auto`
  - `text-embedding-3-small`
  - `nvidia/llama-nemotron-embed-vl-1b-v2:free`
- `client.models.retrieve("nvidia/llama-nemotron-embed-vl-1b-v2:free")` returned:
  - `id: nvidia/llama-nemotron-embed-vl-1b-v2:free`
  - `owned_by: nvidia`
  - `object: model`

### Auth Error Compatibility

Official SDK invalid-key probe:

- `client.models.list()` with `sk-bad-key`
- Result:
  - HTTP `401`
  - `type: authentication_error`
  - `code: invalid_api_key`
  - message matched the OpenAI-style invalid-key path

### Chat Completion

Raw HTTP probe:

- Request model: `openrouter/auto`
- Status: `200`
- Headers:
  - `x-model-routed: openrouter/auto`
  - `x-provider-used: openrouter`
  - `x-provider-model: nvidia/nemotron-3-nano-30b-a3b:free`
  - `x-actual-credits: 8`
- Body returned a standard `chat.completion`

Official SDK probe:

- `client.chat.completions.create({ model: "openrouter/auto", ... })`
- Result:
  - `object: chat.completion`
  - assistant content: `hive`
  - `finish_reason: stop`
  - usage returned normally
  - observed provider-routed response model: `nvidia/nemotron-3-super-120b-a12b-20230311:free`

The public request model stayed stable as `openrouter/auto` while the upstream provider model varied between successful calls. That is expected and matches Hive's DIFF-header design.

### Embeddings

Requested verification model:

- `nvidia/llama-nemotron-embed-vl-1b-v2:free`

Post-fix behavior:

- Hive no longer rejects the model locally as unknown
- The model is exposed in `/v1/models`
- `POST /v1/embeddings` reaches provider dispatch instead of failing at catalog lookup

Observed live outcome on 2026-03-22:

- Raw HTTP request for `nvidia/llama-nemotron-embed-vl-1b-v2:free` returned HTTP `502`
- Official SDK `client.embeddings.create(...)` for the same model returned HTTP `502`
- The returned Hive error payload remained OpenAI-shaped
- The upstream cause was OpenRouter key exhaustion for embeddings on the local key

Control probe:

- `text-embedding-3-small` also returned the same upstream `502` key-limit condition

Conclusion:

- The remaining failure is upstream-account state, not Hive's local model exposure, auth, or request routing
- On this machine and key, live embeddings success could not be proven because OpenRouter rejected the provider call after Hive had already accepted and routed it correctly

## Billing And Persistence Evidence

Local funding and request attribution were verified through both public routes and raw Supabase rows.

### Balance And Usage API

- Pre-funding balance: `0`
- Post-funding balance: `5000`
- Post-verification balance: `4984`
- `/v1/usage` summary after the run:
  - `totalRequests: 2`
  - `totalCredits: 16`
  - `byModel: openrouter/auto -> 2 requests / 16 credits`
  - `byEndpoint: /v1/chat/completions -> 2 requests / 16 credits`
  - `byChannel: api -> 2 requests / 16 credits`
  - `byApiKey: 42a42fde-289e-4264-a8bc-cfcb32fae0e5 -> 2 requests / 16 credits`

No successful embeddings usage rows were created because both live embeddings probes failed upstream.

### Supabase Table Evidence

`credit_accounts`

- `available_credits: 4984`
- `purchased_credits: 5000`
- `promo_credits: 0`

`credit_ledger`

- one `credit` row for `5000` credits with `reference_type = payment` and `reference_id = intent_anssilirql`
- two `debit` rows for `8` credits each with `reference_type = usage`

`usage_events`

- two rows
- both at `/v1/chat/completions`
- both with `model = openrouter/auto`
- both with `channel = api`
- both attributed to API key id `42a42fde-289e-4264-a8bc-cfcb32fae0e5`

`api_keys`

- one active row for nickname `sdk-local-test`
- scopes: `chat`, `usage`, `billing`
- prefix stored as `sk_live_`

`payment_intents`

- `intent_anssilirql`
- provider `bkash`
- `bdt_amount: 50`
- `status: credited`
- `minted_credits: 5000`

`payment_events`

- one verified event for the credited local payment intent

## Verification Commands

```bash
pnpm stack:dev
docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d --force-recreate api web
curl -s http://127.0.0.1:8080/health
curl -s http://127.0.0.1:8080/v1/providers/status
docker compose exec api sh -lc "cd /app && pnpm --filter @hive/api test"
docker compose exec api sh -lc "cd /app && pnpm --filter @hive/api build"
docker compose exec api sh -lc "cd /app && pnpm --filter @hive/api exec vitest run test/openai-sdk-regression.test.ts -t 'embeddings.create'"
```

## Takeaways

- Hive's local public API works with the official OpenAI Node SDK for models, invalid-key auth errors, and chat completions
- Hive-issued API keys own the metering and billing path correctly; the live run proved API-key attribution through `/v1/usage` and Supabase persistence
- The verification-only NVIDIA embedding model is now a dev-only opt-in surface, not a production default
- The remaining live embeddings blocker on 2026-03-22 is the current OpenRouter key's upstream limit, not Hive's local routing or contract layer
