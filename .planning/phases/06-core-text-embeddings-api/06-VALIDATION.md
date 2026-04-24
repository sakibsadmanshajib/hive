---
phase: 6
slug: core-text-embeddings-api
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-04-08
---

# Phase 6 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go testing + vitest (SDK tests) |
| **Config file** | `apps/edge-api/cmd/server/main_test.go` (Go), `packages/sdk-tests/js/vitest.config.ts` (JS), `packages/sdk-tests/python/pyproject.toml` (Python) |
| **Quick run command** | `docker compose run --rm toolchain bash -c "cd apps/edge-api && go test ./..."` |
| **Full suite command** | `docker compose --profile test up --exit-code-from sdk-tests-js sdk-tests-js && docker compose --profile test up --exit-code-from sdk-tests-py sdk-tests-py` |
| **Estimated runtime** | ~60 seconds |

---

## Sampling Rate

- **After every task commit:** `cd apps/edge-api && go test ./internal/inference/ -race`
- **After every plan wave:** Full SDK test suite (JS + Python)
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 60 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 06-01-01 | 01 | 0 | API-01 | unit | `cd apps/edge-api && go test ./internal/inference/ -run TestRequestParsing` | ❌ W0 | ⬜ pending |
| 06-01-02 | 01 | 0 | API-02 | unit | `cd apps/edge-api && go test ./internal/inference/ -run TestSSERelay` | ❌ W0 | ⬜ pending |
| 06-01-03 | 01 | 0 | API-03 | unit | `cd apps/edge-api && go test ./internal/inference/ -run TestEmbeddings` | ❌ W0 | ⬜ pending |
| 06-01-04 | 01 | 0 | API-04 | unit | `cd apps/edge-api && go test ./internal/inference/ -run TestReasoningCapabilityGate` | ❌ W0 | ⬜ pending |
| 06-02-01 | 02 | 1 | API-01 | integration | `docker compose --profile test run sdk-tests-js -- vitest run --grep "chat.completions"` | ❌ W0 | ⬜ pending |
| 06-02-02 | 02 | 1 | API-01 | integration | `docker compose --profile test run sdk-tests-js -- vitest run --grep "completions"` | ❌ W0 | ⬜ pending |
| 06-02-03 | 02 | 1 | API-01 | integration | `docker compose --profile test run sdk-tests-js -- vitest run --grep "responses"` | ❌ W0 | ⬜ pending |
| 06-03-01 | 03 | 1 | API-02 | integration | `docker compose --profile test run sdk-tests-js -- vitest run --grep "streaming"` | ❌ W0 | ⬜ pending |
| 06-03-02 | 03 | 1 | API-02 | integration | `docker compose --profile test run sdk-tests-js -- vitest run --grep "usage"` | ❌ W0 | ⬜ pending |
| 06-04-01 | 04 | 1 | API-03 | integration | `docker compose --profile test run sdk-tests-js -- vitest run --grep "embeddings"` | ❌ W0 | ⬜ pending |
| 06-05-01 | 05 | 2 | API-04 | manual | Manual validation against live provider | N/A | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `apps/edge-api/internal/inference/*_test.go` — unit tests for request parsing, response normalization, capability gating, SSE relay
- [ ] `packages/sdk-tests/js/tests/chat-completions.test.ts` — SDK integration tests for chat/completions (streaming + non-streaming)
- [ ] `packages/sdk-tests/js/tests/completions.test.ts` — SDK integration tests for legacy completions
- [ ] `packages/sdk-tests/js/tests/responses.test.ts` — SDK integration tests for Responses API
- [ ] `packages/sdk-tests/js/tests/embeddings.test.ts` — SDK integration tests for embeddings
- [ ] `packages/sdk-tests/python/tests/test_chat_completions.py` — Python SDK integration tests
- [ ] `packages/sdk-tests/python/tests/test_embeddings.py` — Python SDK integration tests

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Reasoning params pass through on capable models | API-04 | Requires live provider with reasoning model | Send request with `reasoning_effort: "medium"` to a reasoning-capable model, verify response includes `reasoning` field |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 60s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
