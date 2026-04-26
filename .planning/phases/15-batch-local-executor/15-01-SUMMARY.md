---
phase: 15
plan: 01
subsystem: batchstore
tags: [batch, executor, settlement, openrouter, groq, jsonl]
requires: [storage:S3, asynq:redis, accounting:reservation]
provides: [batch:success-path, batch:execute task type, batch_lines table]
affects: [submitter, worker, queue, filestore.Service]
tech_stack:
  added:
    - "math/big — per-line credit summation"
    - "bufio.Scanner with 4MB buffer — JSONL streaming"
    - "asynq batch:execute task type"
  patterns:
    - "small ports + adapters (BatchStore, LineStore, ReservationPort, StoragePort, InferencePort)"
    - "math/big for financial summation (per CLAUDE.md FX rule)"
    - "exponential-backoff retry with 4xx terminal short-circuit"
    - "thread-safe append-only JSONL writers (mutex-guarded)"
    - "pgx upsert-on-conflict for per-line idempotency"
key_files:
  created:
    - apps/control-plane/internal/batchstore/executor/types.go
    - apps/control-plane/internal/batchstore/executor/jsonl.go
    - apps/control-plane/internal/batchstore/executor/jsonl_test.go
    - apps/control-plane/internal/batchstore/executor/dispatcher.go
    - apps/control-plane/internal/batchstore/executor/dispatcher_test.go
    - apps/control-plane/internal/batchstore/executor/executor.go
    - apps/control-plane/internal/batchstore/executor/executor_test.go
    - apps/control-plane/internal/batchstore/executor/settle.go
    - apps/control-plane/internal/batchstore/executor/settle_test.go
    - apps/control-plane/internal/batchstore/local_inference.go
    - apps/control-plane/internal/batchstore/local_executor_adapters.go
    - apps/control-plane/internal/batchstore/local_executor_test.go
    - supabase/migrations/0015_batch_local_executor.sql
    - .planning/phases/15-batch-local-executor/DECISIONS.md
    - .planning/phases/15-batch-local-executor/15-VERIFICATION.md
  modified:
    - apps/control-plane/internal/batchstore/types.go
    - apps/control-plane/internal/batchstore/queue.go
    - apps/control-plane/internal/batchstore/submitter.go
    - apps/control-plane/internal/batchstore/worker.go
    - apps/control-plane/internal/filestore/repository.go
    - apps/control-plane/internal/filestore/service.go
    - apps/control-plane/cmd/server/main.go
    - apps/control-plane/internal/platform/config/config.go
    - .env.example
    - deploy/litellm/config.yaml
decisions:
  - "Q1: LiteLLM-direct HTTP (not in-process import) — Go internal/ rule forbids cross-module access"
  - "Q2: Control-plane in-process — reuses DB pool, Redis queue, storage, accounting"
  - "Q3: Concurrency default 8, ceiling 32 — empirical OpenRouter/Groq parallel-request tolerance"
metrics:
  completed_at: 2026-04-25
  duration_minutes: ~120
  tasks: 3
  go_loc_added: ~1750 (executor 600 + adapters 400 + tests 750)
  migration_loc: 32
---

# Phase 15 Plan 01: Batch Local Executor Summary

Implements local batch executor inside `apps/control-plane/internal/batchstore/executor/` so v1.0's `POST /v1/batches` success-path ships without requiring a LiteLLM-supported batch upstream provider — the executor reads input JSONL line-by-line from Supabase Storage, fans out via bounded concurrency to LiteLLM's `/v1/chat/completions` endpoint, composes OpenAI-shape `output.jsonl` + `errors.jsonl`, and settles per-line credits through the existing reservation primitive.

## Outcome

Closes the v1.0 known-issue "batch success-path blocked by upstream provider capability" (CLAUDE.md Known Issues #4) for the OpenRouter+Groq provider mix Hive ships with. Existing LiteLLM upstream path preserved as `submitUpstream` for future supported providers (openai/azure/vertex/anthropic) — selectable per-batch via `BATCH_EXECUTOR_KIND` env override.

## What Shipped

### Executor primitives (`apps/control-plane/internal/batchstore/executor/`)

- `types.go` — Config (Concurrency/MaxRetries/LineTimeout/Kind) with Validate clamps; OpenAI batch-shape contracts (InputLine, OutputLine, ErrorLine, OutputResponse, ErrorObj); ScanResult/DispatchResult.
- `jsonl.go` — `ScanLines` streams a JSONL reader with a 4MB scanner buffer (headroom over OpenAI's ~1MB-per-line cap); malformed lines yielded as ScanResult errors so the caller routes them to errors.jsonl rather than crashing. `JSONLWriter` is mutex-guarded for concurrent Append from dispatcher goroutines; Finalize uploads to a StoragePort with optional skip-empty for the errors file.
- `dispatcher.go` — bounded worker pool + exp-backoff retry (100ms/400ms/1.6s; 4xx terminal short-circuit; 5xx/timeout/network retry up to MaxRetries); per-line context timeout; provider-name regex sanitization (`openrouter|groq|litellm` → `upstream`); `PeakInFlight` counter for concurrency-cap test instrumentation.
- `executor.go` — `Run(ctx, batchID)` orchestrates: load batch via BatchStore, load prior batch_lines via LineStore (skips already-settled rows for restart-safe resume), stream input via ScanLines, dispatch via Pool, drain results into JSONLWriters, finalize uploads, settle credits, MarkCompleted.
- `settle.go` — `Settle` sums per-line credits via `math/big.Int` (per CLAUDE.md FX rule), caps at reserved with `overconsumed=true` on overrun (no negative balance), idempotent on identical inputs.

### Migration `0015_batch_local_executor.sql`

- `batches.executor_kind` ('upstream'|'local'), `batches.completed_lines`, `batches.failed_lines`, `batches.overconsumed`.
- `route_capabilities.executor_kind` (default 'local' for openrouter/groq).
- `batch_lines` table with `(batch_id, custom_id)` PK + status/attempt/consumed_credits/output_index/error_index/last_error/timestamps for per-line idempotency + restart-safe resume.

### Wiring

- `submitter.go` — `WithLocalExecutor(executeQueue, kind)` + `shouldUseLocalExecutor` branches on provider (openai/azure/vertex/anthropic → upstream; everything else → local) plus env override. `submitLocal` updates the batch row to in_progress with executor_kind=local and enqueues `batch:execute`.
- `queue.go` — `EnqueueExecute` pushes the new task type onto the same Redis batch queue.
- `worker.go` — `HandleBatchExecute` delegates to `executor.Run`; `ctx.Canceled` re-enqueues; terminal failures mark the batch failed via the existing failure path.
- `local_inference.go` — `LiteLLMInferenceClient` implements `executor.InferencePort` via `POST {litellm}/v1/chat/completions`; rewrites model field per route; decodes OpenAI usage for per-line credit attribution.
- `local_executor_adapters.go` — `pgxBatchStore`, `pgxLineStore`, `AccountingReservationAdapter` wrap existing services with zero new ledger code.
- `filestore` — `GetBatchByID` + `GetFileByID` server-side variants (existing `GetBatch` requires accountID scope; the worker resolves the account from the batch row first).
- `main.go` — singleton executor wired at startup; `asynq.NewServeMux` registers `batch:execute` alongside `batch:poll`.
- `.env.example` — `BATCH_EXECUTOR_CONCURRENCY` (default 8, ceiling 32), `BATCH_EXECUTOR_MAX_RETRIES` (default 3, ceiling 5), `BATCH_EXECUTOR_LINE_TIMEOUT_MS` (default 60000, floor 5000, ceiling 300000), `BATCH_EXECUTOR_KIND` (default "auto").

## Decisions Made

- **Q1 LiteLLM-direct over in-process import.** Initial plan had the dispatcher call `apps/edge-api/internal/inference.Orchestrator` in-process. Investigation confirmed Go's `internal/` rule forbids cross-module access (control-plane and edge-api are separate modules under `go.work`). Two fallbacks evaluated: (a) shared `packages/go/inference` factor (~1 day, ~25 call-site refactors), (b) HTTP to LiteLLM `/v1/chat/completions`. Picked (b) because control-plane already holds LiteLLM credentials and rewrites batch model names; reuses LiteLLM's provider routing/retry/sanitization; no module-boundary refactor; no double rate-limiter accounting; ~5–15ms per-line latency cost is acceptable for batch workloads. Sanitization moved to `dispatcher.SanitizeMessage` regex.
- **Q2 control-plane in-process.** Reuses DB pool, Redis queue, storage, accounting service. Throughput target dozens of batches/day → separate process is over-engineering. Concurrency ceiling 32 + per-line timeout bound the runaway-batch risk.
- **Q3 concurrency 8/32.** Empirical OpenRouter/Groq parallel-request tolerance; ceiling enforced in `Config.Validate`.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Toolchain Docker image lacks gcc/musl-dev for `-race`**

- **Found during:** Task 1 verification.
- **Issue:** `go test -race` requires `CGO_ENABLED=1` plus a libc and a C compiler; the alpine-based toolchain image ships with neither.
- **Fix:** Test commands now run `apk add --no-cache gcc musl-dev > /dev/null 2>&1 && go test ...` on each invocation. Documented in 15-VERIFICATION.md "Verification Commands".
- **Files modified:** Verification doc only — not committed to image build to keep the toolchain Dockerfile lean.
- **Commit:** N/A (operator-runbook)

**2. [Rule 3 - Blocking] Module boundary forbids in-process inference import**

- **Found during:** Task 3 main.go wiring.
- **Issue:** `apps/control-plane` cannot import `apps/edge-api/internal/inference` because Go's `internal/` rule forbids cross-module reads, even within the same `go.work`.
- **Fix:** Pivoted Q1 from "in-process call" to "LiteLLM-direct HTTP" — documented in DECISIONS.md.
- **Files modified:** DECISIONS.md, local_inference.go (new).
- **Commit:** d6e9c25 (Task 1) — landed with the pivot resolved before any wiring code was written.

**3. [Rule 3 - Blocking] `filestore.Service.GetBatch/GetFile` require accountID scope**

- **Found during:** Task 3 adapter wiring.
- **Issue:** The worker loads a batch by ID before resolving its owning account, but the existing methods require accountID for tenant isolation.
- **Fix:** Added `GetBatchByID` + `GetFileByID` (server-side, no scope) in repository.go + service.go. Customer-facing handlers continue to use the scoped variants.
- **Files modified:** apps/control-plane/internal/filestore/{repository,service}.go.
- **Commit:** Task 3 commit.

**4. [Rule 3 - Blocking] Toolchain image VCS stamping fails**

- **Found during:** Task 3 build.
- **Issue:** `go build` errored "obtaining VCS status: exit status 128" inside the toolchain container because the workspace mount isn't a git repo from the container's perspective.
- **Fix:** Added `-buildvcs=false` to the verification build invocation; production builds (run from the host or a properly-stamped image) are unaffected.
- **Commit:** N/A (operator-runbook)

### Architectural Changes (asked → none)

No Rule 4 escalations. The Q1 pivot to LiteLLM-direct was a pre-resolved blocker (PLAN.md blocker #2), not a fresh architectural decision.

## Live Ship-Gate Status

The unit-level matrix from PLAN.md `must_haves.truths` is fully verified
(see 15-VERIFICATION.md "Must-Have Truth Verification" section). The 100-line
live E2E + restart-safety probe + sanitization grep + USD audit need:

1. Migration 0015 applied to staging Supabase.
2. Funded API key minted via control-plane.
3. `BATCH_EXECUTOR_KIND=auto` boot.

Operator runbook in 15-VERIFICATION.md "Live ship-gate dry-run". Once run,
audit outputs paste back into that section to close the v1.1.0 ship-gate
item for batch success-path settlement.

## Authentication Gates

None encountered during execution.

## Forward-Carried Blockers

- **Phase 16:** `apps/control-plane/internal/routing/repository.go`
  `ensureCapabilityColumns` table-name fix (currently targets
  `route_capabilities` instead of `provider_capabilities`). Phase 15 explicitly
  did not touch this file (`git diff main...HEAD` empty for that path).
- **Phase 17:** Full FX/USD leak audit across customer-visible surfaces
  (Phase 15 introduces no new leak on the batch surface; Phase 17 owns the
  comprehensive sweep).

## Self-Check: PASSED

- All listed key_files present on disk:
  - executor/{types,jsonl,jsonl_test,dispatcher,dispatcher_test,executor,executor_test,settle,settle_test}.go ✓
  - migration 0015_batch_local_executor.sql ✓
  - local_inference.go, local_executor_adapters.go, local_executor_test.go ✓
  - DECISIONS.md, 15-VERIFICATION.md ✓
- All listed modified files contain Phase 15 changes (Edit tool errored
  on stale reads → state confirmed via rebuild + tests).
- All commits on `a/phase-15-batch-local-executor-clean` reachable from HEAD
  (cherry-picked from interleaved branch onto origin/main).
- All tests green under `-race`:
  - `go test -count=1 -race -short ./apps/control-plane/internal/batchstore/...` → ok batchstore + ok batchstore/executor.
  - `go vet ./apps/control-plane/...` → clean.
  - `go test -count=1 -short ./apps/control-plane/...` → all packages ok.
- Phase 16 boundary clean: `git diff --name-only origin/main...HEAD` does
  not include `apps/control-plane/internal/routing/repository.go`.
