---
phase: 11-real-openai-sdk-regression-tests-ci-style-e2e
verified: 2026-03-22T22:42:56Z
status: passed
score: 5/5 must-haves verified
re_verification: true
human_verification: []
---

# Phase 11: Real OpenAI SDK Regression Tests — Verification Report

**Phase Goal:** Expand the OpenAI SDK regression suite into a CI-ready verification surface that covers the implemented OpenAI-compatible endpoints with the official `openai` Node SDK.
**Verified:** 2026-03-22
**Status:** passed
**Re-verification:** yes — refreshed against the current tree and Docker-local stack

## Current Evidence

- `docker compose exec api sh -lc "cd /app && pnpm --filter @hive/api test"` -> `69/69` files, `372/372` tests passed
- `docker compose exec api sh -lc "cd /app && pnpm --filter @hive/api build"` -> passed
- `docker compose exec api sh -lc "cd /app && pnpm --filter @hive/api exec vitest run test/openai-sdk-regression.test.ts -t 'embeddings.create'"` -> passed
- 2026-03-22 Docker-local live verification with the official SDK:
  - `models.list()` succeeded
  - `models.retrieve()` succeeded for the local verification embedding model
  - invalid key handling returned HTTP `401` with `authentication_error` / `invalid_api_key`
  - `chat.completions.create()` succeeded against the live public API
  - live embeddings requests cleared Hive routing and auth, but upstream OpenRouter embeddings were blocked by the current key limit in this environment

## Goal Achievement

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | Success-path SDK coverage exists for models, chat, embeddings, images, and responses | VERIFIED | [`test/openai-sdk-regression.test.ts`](/home/sakib/hive/apps/api/test/openai-sdk-regression.test.ts) |
| 2 | Streaming coverage remains present through the SDK surface | VERIFIED | streaming test still present in the Phase 11 regression file |
| 3 | Auth and error-path SDK mapping remains correct | VERIFIED | invalid-key live SDK probe returned `401` / `authentication_error` / `invalid_api_key` |
| 4 | The regression suite passes on the current tree | VERIFIED | `372/372` tests, `69/69` files |
| 5 | The API builds in Docker on the current tree | VERIFIED | `pnpm --filter @hive/api build` passed inside the live API container |

## Live Local SDK Notes

### Verified Success Paths

- `models.list()` returned the public catalog from Hive's live `/v1/models`
- `models.retrieve("nvidia/llama-nemotron-embed-vl-1b-v2:free")` returned a `model` object from the live stack after the local verification-model fix
- `chat.completions.create({ model: "openrouter/auto" })` returned a standard `chat.completion`
- The live chat path also emitted DIFF headers and charged credits through the real Hive API key

### Verified Error Path

- Invalid key probe via the official SDK returned:
  - HTTP `401`
  - `type: authentication_error`
  - `code: invalid_api_key`

### External Environment Blocker

Live embeddings requests on 2026-03-22 were no longer rejected by Hive as unknown models. Both:

- `nvidia/llama-nemotron-embed-vl-1b-v2:free`
- `text-embedding-3-small`

reached provider dispatch and then failed with an upstream OpenRouter key-limit condition. That is an external verification blocker for this machine's current key, not an in-tree Phase 11 regression gap.

## Requirements Coverage

| Requirement | Description | Status | Evidence |
|-------------|-------------|--------|----------|
| CI-01 | Success-path coverage for implemented SDK endpoints | SATISFIED | regression suite remains green |
| CI-02 | Streaming coverage through the SDK path | SATISFIED | streaming regression remains present and green |
| CI-03 | SDK error-path mapping for the implemented surface | SATISFIED | invalid-key live SDK probe plus regression coverage |
| CI-04 | Current regression artifact is live and current | SATISFIED | this report replaces the stale `345/68` snapshot |
| CI-05 | Current tree passes the full API suite and build | SATISFIED | `372/372`, `69/69`, Docker build passed |

## Conclusion

Phase 11 is now current.

- The regression suite is green on the current tree.
- The API builds successfully in Docker.
- Real Docker-local SDK verification confirms the public models/auth/chat paths on Hive's live API.
- Live embeddings requests now clear Hive's local contract layer and fail only at the current upstream OpenRouter key limit.

That upstream limit should be tracked as an environment/provider constraint for live local verification, not as a regression in the Phase 11 implementation.
