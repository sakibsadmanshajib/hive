# Phase 15 — Ship-Gate Verification

**Date:** 2026-04-25
**Branch:** `a/phase-15-batch-local-executor`
**Scope:** Local batch executor for `POST /v1/batches` success-path settlement.
**Out of scope:** `apps/control-plane/internal/routing/repository.go` `ensureCapabilityColumns` table-name fix (Phase 16); USD/FX leak audit beyond batch surface (Phase 17).

## Must-Have Truth Verification

The following truths from PLAN.md `must_haves.truths` are unit-verified by the
batchstore tests. Live-stack verification is gated on a funded API key + the
0015 migration applied to the staging Supabase project — captured under
"Live ship-gate dry-run" below.

### Truth 1 — Batch reaches `status='completed'` without an OPENAI_API_KEY

- **Verified by:** `TestExecutor_MixedSuccessFailure` (executor_test.go).
- **Outcome:** 100-line input, 95 success / 5 error → `MarkCompleted` invoked
  with `completed_lines=95`, `failed_lines=5`. No LiteLLM-supported batch
  provider (openai/azure/vertex/anthropic) involved — fake inference port
  returns OpenAI-shape responses directly.

### Truth 2 — output.jsonl line shape

- **Verified by:** `TestExecutor_MixedSuccessFailure` asserts output.jsonl
  uploaded to `hive-files/batches/b1/output.jsonl` with 95 newline-delimited
  rows. Row shape is `OutputLine{ID, CustomID, Response{StatusCode,
  RequestID, Body}, Error: nil}` per `executor/types.go`.

### Truth 3 — errors.jsonl line shape

- **Verified by:** `TestExecutor_MixedSuccessFailure` asserts errors.jsonl
  uploaded with 5 rows; `TestExecutor_AllErrors` covers the 100% failure
  case. Row shape is `ErrorLine{ID, CustomID, Response: nil,
  Error{Code, Message}}`. `output_file_id` set to upload key only when
  uploaded; `error_file_id` skipped on empty (TestExecutor_EmptyInput).

### Truth 4 — actual_credits = sum of per-line consumed credits

- **Verified by:** `TestExecutor_MixedSuccessFailure` asserts
  `fakeReservationLE.lastActual = 950` (95 succeeded × 10 tokens),
  `TestSettle_SumsExactlyAcrossManyLines` asserts exact-sum across 100
  lines = 5650 with no rounding error using `math/big.Int`.
  `TestSettle_Overconsumed` covers the cap-at-reserved safety net.

### Truth 5 — concurrency bounded by env, ceiling 32

- **Verified by:** `TestDispatcher_BoundedConcurrency` asserts peak in-flight
  ≤ 4 with `Concurrency=4` over 100 lines. `TestConfig_Validate_ClampAndDefaults`
  asserts Concurrency=999 clamps to ConcurrencyCeiling=32.

### Truth 6 — retry policy 5xx + 4xx terminal

- **Verified by:** `TestDispatcher_Retry503ThenSuccess` (503 ×2 → 200 succeeds
  on attempt 3), `TestDispatcher_4xxNoRetry` (400 fails immediately,
  attempts=1).

### Truth 7 — provider-name sanitization

- **Verified by:** `TestDispatcher_ProviderNameSanitized` asserts an
  upstream error containing "openrouter upstream rejected the request via
  litellm gateway" emerges as a sanitized message with no instance of
  `openrouter`, `groq`, or `litellm`. `TestSanitizeMessage` covers the
  rule directly across positive/negative cases.

### Truth 8 — restart safety

- **Verified by:** `TestExecutor_RestartResume` — pre-seeds 6 succeeded
  batch_lines rows, runs executor with 10-line input, asserts dispatcher
  was invoked exactly 4 times (skipping the 6 already-settled), final
  output.jsonl contains 10 unique custom_ids with no duplicates, and
  reservation actual_credits = 100 (no double-charge of the 6 prior).

### Truth 9 — Phase 16 routing repo not touched

- **Verified by:** `git diff --name-only origin/main...HEAD | grep -E
  '^apps/control-plane/internal/routing/repository.go$'` returns empty.

## Verification Commands

```bash
# Unit tests (run inside docker toolchain image with CGO for -race).
cd /home/sakib/hive/deploy/docker && docker compose --env-file ../../.env \
  --profile local --profile tools run --rm -T -e CGO_ENABLED=1 toolchain \
  "apk add --no-cache gcc musl-dev > /dev/null 2>&1 && \
   go test -count=1 -race -short ./apps/control-plane/internal/batchstore/..."
# Expected: ok batchstore, ok batchstore/executor.

# Vet + full short suite.
cd /home/sakib/hive/deploy/docker && docker compose --env-file ../../.env \
  --profile local --profile tools run --rm -T -e CGO_ENABLED=1 toolchain \
  "apk add --no-cache gcc musl-dev > /dev/null 2>&1 && \
   go vet ./apps/control-plane/... && \
   go test -count=1 -short ./apps/control-plane/..."
# Expected: vet clean; all packages ok.

# Phase 16 boundary.
git diff --name-only origin/main...HEAD | \
  grep -E '^apps/control-plane/internal/routing/repository.go$'
# Expected: empty stdout.
```

All commands above were executed during Phase 15 task execution and produced
the expected outputs (see commit log on `a/phase-15-batch-local-executor`).

## Live ship-gate dry-run (deferred)

The 100-line live E2E + restart-safety probe + provider-name leak grep + USD
audit listed in PLAN.md Task 3 Tests 4–7 require:

1. Migration `supabase/migrations/0015_batch_local_executor.sql` applied to
   staging Supabase.
2. A funded API key minted via `POST /v1/keys`.
3. Stack booted with `BATCH_EXECUTOR_KIND=auto` (default) so openrouter/groq
   batches route through the local executor.

Steps to run the live ship-gate (operator-only, blocked on staging migration
window):

```bash
# 1. Apply migration.
supabase db push  # or paste 0015_batch_local_executor.sql into SQL editor

# 2. Boot stack.
cd deploy/docker && docker compose --env-file ../../.env --profile local up --build

# 3. Mint funded key (50000 credits).
curl -X POST http://localhost:8081/v1/keys -H "Authorization: Bearer ..." -d '{"credits":50000}'

# 4. Generate 100-line JSONL fixture (95 valid + 5 deliberately-broken bodies).
python3 scripts/gen_batch_fixture.py 100 5 > /tmp/batch.jsonl

# 5. Upload + submit.
HIVE_API_KEY=<minted> python3 scripts/submit_batch.py /tmp/batch.jsonl

# 6. Poll status until completed.
while true; do
  status=$(curl -s -H "Authorization: Bearer $HIVE_API_KEY" \
    http://localhost:8080/v1/batches/<id> | jq -r .status)
  [ "$status" = "completed" ] && break
  sleep 5
done

# 7. Audits (tests 4–7 from PLAN.md).
curl -s ... > /tmp/output.jsonl
curl -s ... > /tmp/errors.jsonl

# Sanitization: must return zero matches.
grep -iE 'openrouter|groq|litellm' /tmp/output.jsonl /tmp/errors.jsonl

# FX/USD leak: must return zero on /v1/batches GET response.
curl -s -H "Authorization: Bearer $HIVE_API_KEY" \
  http://localhost:8080/v1/batches/<id> | grep -iE 'amount_usd|usd_|fx_|exchange_rate'

# Restart-safety: re-mint, post fresh batch, restart mid-run, verify completion.
docker compose restart control-plane  # ~30 lines in
# Confirm no duplicate custom_id in output.jsonl:
jq -s 'group_by(.custom_id) | map(select(length > 1))' /tmp/output.jsonl
# Expected: []
```

When operator runs this dry-run pre-release, paste the audit outputs here as
`## Live audit results (date)` to close the v1.1.0 ship-gate.

## Known Caveats

1. **>4MB batch lines** — the JSONL scanner's buffer is sized to 4MB, headroom
   over OpenAI's documented ~1MB-per-line cap. Lines exceeding 4MB yield a
   `bufio.Scanner: token too long` error which the scanner emits via the
   ScanResult error channel; the executor writes those to errors.jsonl with
   code `invalid_json`. Documented per PLAN.md blocker #5.

2. **Restart-safety re-emit semantics** — when the executor resumes a batch
   after a mid-run kill, succeeded lines from the previous run are re-emitted
   into the output.jsonl with a sentinel response body
   `{"resumed": true, "request_id": "resumed"}` because the original upstream
   response body is gone (transient memory). The line is NOT re-dispatched
   and NOT re-charged — the consumed_credits in `batch_lines` is the source
   of truth for billing. Customers polling status see the line in output.jsonl
   without re-billing.

   **OpenAI compliance gap.** The sentinel body diverges from the real
   chat-completion response shape; SDKs that try to parse `response.body` as
   a chat-completion will fail on resumed lines. Mitigation: see
   "Phase 18 — Batch Resume Compliance" plan below.

3. **In-process inference vs LiteLLM-direct** — DECISIONS.md Q1 documents the
   pivot from in-process call to LiteLLM HTTP. Trade-off accepted because Go
   `internal/` package rules forbid cross-module import (control-plane and
   edge-api are separate modules under go.work).

## Codex Review Addressed (PR 134)

Three findings from the Codex automated review on PR 134 fixed in commit
`2dfb0ca`:

1. **P0 — `MarkCompleted` wrote storage keys into `files(id)` FK columns.**
   `batches.output_file_id` / `error_file_id` reference `public.files(id)`,
   so writing raw storage paths broke `GET /v1/files/{id}` resolution after
   batch completion. Fix: added `executor.FileRegistrar` port +
   `pgxFileRegistrar` adapter wrapping `filestore.Service.CreateFile`.
   Executor now creates `public.files` rows for the uploaded
   `output.jsonl` / `errors.jsonl` artifacts and persists the returned IDs.
   `JSONLWriter.Finalize` returns uploaded byte count so the registrar
   records accurate `bytes`. Verified via updated `TestExecutor_*` tests.

2. **P1 — `submitLocal` left credits reserved on enqueue failure.**
   Pre-fix: `UpdateBatchStatus` to `in_progress` ran before `EnqueueExecute`;
   if Asynq/Redis was unavailable the batch row stuck `in_progress` with
   credits reserved indefinitely. Fix: both `UpdateBatchStatus` and
   `EnqueueExecute` failure paths now route through `failSubmission`, which
   marks the batch `failed` and releases the reservation. Mirrors the
   upstream-path failure handling.

3. **P2 — Dispatcher routed by per-line `body.model`, not batch alias.**
   Pre-fix: dispatcher extracted the alias from each JSONL line's
   `body.model`. That diverged from the upstream-path semantics
   (`rewriteBatchJSONL` uses the batch's resolved `LiteLLMModelName`) and
   could send lines to unintended routes. Fix: added `InputLine.Alias`
   (json:"-"); executor injects `batch.ModelAlias` before dispatch;
   dispatcher uses `Alias` for routing. Per-line `body.model` stays opaque
   (the inference port still rewrites it to the LiteLLM model name).

## Out of Scope (separate phases)

- **Phase 16:** `apps/control-plane/internal/routing/repository.go`
  `ensureCapabilityColumns` table-name fix (currently targets
  `route_capabilities` instead of `provider_capabilities`).
- **Phase 17:** Full FX/USD leak audit across all customer-visible
  surfaces (batch surface pre-checked here; `/v1/payments` surface owned
  by Phase 17).
- **Phase 18 — Batch Resume Compliance (planned).** Closes Known Caveat #2
  above. SDK-compatible resume of mid-run-killed batches without billing
  divergence.

  **Goal.** When the executor resumes a batch after a mid-run kill,
  re-emitted output lines must carry the original chat-completion response
  body verbatim so OpenAI SDK parsers don't fail. Billing semantics
  (`consumed_credits` in `batch_lines` is source of truth, no re-dispatch,
  no re-charge) are unchanged.

  **Approach (recommended).** Persist the upstream response body to a new
  `batch_lines.response_body bytea` column at the same transaction as
  `MarkSucceeded`. On resume, re-emit prior succeeded lines using the
  persisted body instead of the `{"resumed": true}` sentinel. Errors path
  parallel: persist `last_error` (already present) plus `error_body bytea`
  for the structured error envelope.

  **Schema migration.** `0019_batch_resume_response.sql`:
  ```sql
  alter table public.batch_lines
      add column response_body bytea,
      add column error_body bytea;
  ```

  **Code touch points.**
  - `executor.LineStore.MarkSucceeded`: add `responseBody []byte` parameter.
  - `executor.LineStore.MarkFailed`: add `errorBody []byte` parameter.
  - `executor.Executor.Run`: stop emitting the `{"resumed": true}` sentinel;
    re-marshal prior `LineRow.ResponseBody` / `ErrorBody` instead.
  - `pgxLineStore`: persist + scan the new columns.

  **Trade-offs.**
  - Storage cost: chat-completion bodies typically 1–20KB; 1M-line batch
    ≈ 1–20GB on `batch_lines`. Acceptable at v1.1 scale; revisit at v1.2 if
    batch volume grows. Mitigation: optionally compress with `pglz` or
    column-level toast (Postgres handles automatically for >2KB).
  - Migration ordering: column addition is non-blocking; backfill not
    required (existing rows resume with sentinel until the next run).

  **Acceptance criteria.**
  - Resumed batch's `output.jsonl` parses end-to-end with the OpenAI Python
    SDK (`openai.types.batch.Batch.output`).
  - No duplicate `custom_id` rows; no double-charge.
  - Existing `TestExecutor_RestartResume` extended to assert response body
    contents match the original dispatch (not the sentinel shape).

  **Out of scope for Phase 18.**
  - Streaming-partial responses (chat-completion only; Phase 15 already
    rejects non-`/v1/chat/completions` lines).
  - Retroactive re-emission for batches completed before the migration.
