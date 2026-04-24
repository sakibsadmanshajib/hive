# Known Issue: Batch Success-Path Blocked By Upstream Provider Capability

**Phase:** 10-routing-storage-critical-fixes
**Severity:** non-blocking
**Filed:** 2026-04-21
**Surface:** `POST /v1/batches`, terminal settlement on `status=completed`
**Scope:** success-path only — failure-path terminal settlement works end-to-end

## Summary

OpenAI-style batch endpoint (`/v1/batches`) cannot complete a successful run-to-terminal flow because the only LLM providers currently wired into LiteLLM (`openrouter`, `groq`) do not support upstream batch file uploads. Failure-path terminal settlement is fully functional: reservation releases cleanly, batch row marked `failed`, attribution preserved, no overcharge.

## Reproduction

1. Fresh stack boot, funded API key with balance ≥ 1000 credits.
2. Upload JSONL: `POST /v1/files` with `purpose=batch` → returns `file-id`.
3. Create batch: `POST /v1/batches` with `input_file_id`, `endpoint`, `completion_window` → HTTP 500 `"Failed to create batch"`.
4. DB row: `public.batches.status = 'failed'`, `upstream_batch_id = NULL`, `actual_credits = 0`.
5. Reservation released: `public.credit_reservations.status = 'released'`, `released_credits = reserved_credits`.

## Root Cause

Control-plane `batchstore.Submitter` (apps/control-plane/internal/batchstore/submitter.go) correctly:

1. Downloads input JSONL from Supabase Storage.
2. Selects a batch-capable route via `routing.SelectRoute(need_batch=true)`.
3. Rewrites JSONL `body.model` to the LiteLLM route id.
4. Uploads rewritten file to LiteLLM `POST /v1/files` with `custom_llm_provider=<provider>`.
5. On success, creates upstream batch + enqueues `batch:poll` task.
6. On failure, releases reservation with reason `batch_submission_failed` and marks batch failed.

Step 4 fails for `openrouter`:

```
HTTP 400
{"error":{"message":"litellm.BadRequestError: LiteLLM doesn't support openrouter for 'create_file'. Only ['openai', 'azure', 'vertex_ai', 'manus', 'anthropic'] are supported.","type":null,"param":null,"code":"400"}}
```

LiteLLM's managed file dispatcher exposes only the providers listed above because those are the only upstream APIs with native OpenAI-shaped batch endpoints. OpenRouter and Groq do not expose a batch API of their own — they are per-request proxies. `deploy/litellm/config.yaml` includes `files_settings` for openrouter, but LiteLLM's own capability check rejects openrouter before invoking the upstream.

## Evidence

- Direct LiteLLM probe (bypassing our code):
  `curl -X POST http://localhost:4000/v1/files -H "Authorization: Bearer $LITELLM_MASTER_KEY" -F purpose=batch -F custom_llm_provider=openrouter -F "file=@sample.jsonl"` → HTTP 400 with supported-providers error above.
- Live failure-path terminal settlement (this is the passing side):
  - `batches` row `batch-7baa4b46-854b-46d4-9afd-a2cddf4ec7b1`: status=failed, api_key_id preserved, model_alias=hive-default, estimated=1000, actual=0.
  - `credit_reservations` row `a43cc385-d1b8-47a6-bc0c-0de2ca81fcce`: status=released, reserved=1000, consumed=0, released=1000.
- Unit tests (success + failure paths, mock LiteLLM):
  `go test ./apps/control-plane/internal/batchstore -run Submitter -count=1` → PASS.

## What Works Today

- Batch persist + reservation + attribution.
- Failure-path terminal settlement and reservation release.
- Provider-blind error surfacing (customer does not see provider name).
- Unit coverage for both success and failure submitter paths.

## What Does Not Work

- Success-path completion: no batch can reach `status=completed` because LiteLLM refuses the upload step for openrouter/groq.
- Live success-path accounting verification (`actual_credits > 0`, ledger entries from upstream batch request counts).

## Unblocking Options

**Option A — Supported provider.** Add a provider LiteLLM supports for batch:
1. Set `OPENAI_API_KEY` (or Anthropic / Azure / Vertex) in `.env`.
2. Add a `route-openai-batch` model in `deploy/litellm/config.yaml`.
3. Add a route row with `batch=true` capability in `public.route_capabilities`.
4. `routing.SelectRoute(need_batch=true)` will pick that route; submitter works unchanged.
Cost: real USD per batch. Scope: config + one migration row.

**Option B — Local batch executor.** Implement control-plane worker that processes JSONL locally instead of delegating to upstream batch API:
1. On `POST /v1/batches`, persist row as today.
2. Worker reads JSONL line-by-line, dispatches each line to `/v1/chat/completions` via LiteLLM (chat completions do work on openrouter).
3. Compose output file (`output.jsonl`) + error file (`errors.jsonl`) in Supabase Storage.
4. Settle reservation from sum of per-request actual usage.
Cost: no upstream fee change. Scope: ~1 day of work, new file composition + settlement path, concurrency + retry policy needed.

## Recommendation

**Option B for v1.** Matches Bangladesh market reality (OpenRouter + Groq free/cheap tiers, no OpenAI dependency), keeps provider-blind contract intact, makes batch a real feature instead of a thin wrapper over upstream. Defer to a new phase.

## References

- Phase 10 UAT: `.planning/phases/10-routing-storage-critical-fixes/10-UAT.md` Test 10
- Submitter: `apps/control-plane/internal/batchstore/submitter.go`
- Unit tests: `apps/control-plane/internal/batchstore/submitter_test.go`
- LiteLLM config: `deploy/litellm/config.yaml`
