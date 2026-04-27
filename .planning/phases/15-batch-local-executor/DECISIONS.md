# Phase 15 — Open Decisions Resolved

**Date:** 2026-04-25
**Phase:** 15 — Batch Success-Path Settlement (Local Executor)
**Branch:** `a/phase-15-batch-local-executor`

## Q1. Loopback HTTP vs in-process call vs LiteLLM-direct

**Decision: LiteLLM-direct (HTTP from control-plane to LiteLLM `POST /v1/chat/completions`).**

**Rationale.** PLAN.md initially proposed an in-process call into
`apps/edge-api/internal/inference.Orchestrator.ChatCompletion`. Investigation
during Task 1 confirmed the architectural blocker noted in the plan's blockers
section #2: control-plane and edge-api are independent Go modules
(`apps/control-plane/go.mod` vs `apps/edge-api/go.mod`), and Go's `internal/`
rule forbids cross-module imports of an `internal/` package. The PLAN's
fallback was a shared `packages/go/inference` factor — a 1-day refactor that
would touch ~25 edge-api call sites and require its own review cycle.

A third option emerged from reading the existing `submitter.go`: control-plane
already holds `litellmBaseURL` + `litellmMasterKey` for the batch-upload path
and already rewrites JSONL bodies to the LiteLLM model name. Calling
`POST {litellmBaseURL}/v1/chat/completions` directly from the dispatcher:

- Uses the same provider routing, sanitization, retry, and capability path
  that edge-api uses (LiteLLM is the unified gateway).
- Reuses control-plane's existing LiteLLM credentials — no new infra.
- Skips the module-boundary refactor entirely.
- Skips loopback HTTP through edge-api (no double rate-limiter accounting,
  no header re-signing).
- Per-line usage comes back in the OpenAI response body's `usage` object
  populated by LiteLLM, the same source edge-api consumes.

**Trade-off accepted.** Sanitization for provider-name leaks now lives in
the executor's response-shaping layer (`dispatcher.sanitizeError`) rather
than reusing edge-api's `inference.errors` package. The sanitization rules
are mechanical (strip provider tokens like `openrouter`, `groq`, `litellm`
from error message strings) and tested in `dispatcher_test.go` Test 7.

**Pre-resolution:** in-process import attempted and rejected by Go's
`internal/` rule (separate modules). LiteLLM-direct adopted on 2026-04-25.

## Q2. Executor in control-plane vs separate worker process

**Decision: control-plane in-process, behind a config flag.** (Unchanged from
PLAN.md.)

**Rationale.** Reuses control-plane's DB pool, Redis queue (Asynq), storage
client (S3-compatible Supabase), accounting service, and reservation
primitive. v1.0 throughput target is dozens of batches/day, not thousands —
a separate process is over-engineering. Horizontal scale path preserved by
the existing Asynq queue (multiple control-plane replicas coordinate via
Redis locks).

**Trade-off accepted.** A runaway batch can compete with control-plane HTTP
traffic for goroutines — mitigated by `BATCH_EXECUTOR_CONCURRENCY` ceiling
(32) and per-line `BATCH_EXECUTOR_LINE_TIMEOUT_MS`. Promoting to a separate
process is a v1.2 task if needed.

## Q3. Concurrency default + ceiling

**Decision: default 8, ceiling 32.** (Unchanged from PLAN.md.)

**Rationale.** OpenRouter and Groq tolerate 8 parallel requests per key on
free/paid tiers (no published hard cap, empirical). Ceiling 32 prevents
accidental misconfiguration from saturating control-plane. Per-line timeout
(60s default) bounds tail latency. Ceiling enforced server-side:
`min(env, 32)` in `ExecutorConfig.Validate`. Per-route concurrency knob is a
v1.2 task.

## Phase 16 boundary

This phase deliberately does NOT touch
`apps/control-plane/internal/routing/repository.go`. The
`ensureCapabilityColumns` table-name fix lands separately in Phase 16. Phase
15 reads from the existing routing path via
`routing.Service.SelectRoute(NeedBatch=true)` and trusts the route's
provider field; the executor_kind decision is taken from the
`BATCH_EXECUTOR_KIND` env var (defaulting to "local" for openrouter/groq
providers) since the route_capabilities `executor_kind` column is added by
this migration and read in Task 3.
