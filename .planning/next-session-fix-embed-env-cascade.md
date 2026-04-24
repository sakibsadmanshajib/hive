# Next Session: Fix embedding model env fallback cascade

## Scope

Trivial bug fix PR. Two SDK test files. No backend changes.

## Bug

`packages/sdk-tests/js/tests/embeddings/embeddings.test.ts:5-7`:
```ts
const EMBEDDING_MODEL =
  process.env.HIVE_EMBEDDING_MODEL ?? process.env.HIVE_TEST_MODEL ?? "hive-embedding-default";
```

Python equivalent in `packages/sdk-tests/python/tests/test_embeddings.py` (check + fix same pattern).

When caller exports `HIVE_TEST_MODEL=hive-default` (chat alias) for chat tests, the embed test picks that up as its model — requests `/embeddings` with `hive-default` → upstream returns `400 capability_mismatch` because `hive-default` is a chat alias, not an embedding alias.

## Repro

```bash
export HIVE_API_KEY=<any valid key>
export HIVE_BASE_URL=https://api-hive.scubed.co/v1
export HIVE_TEST_MODEL=hive-default            # intentional — matches chat tests
# HIVE_EMBEDDING_MODEL NOT set
cd packages/sdk-tests/js && npm test -- tests/embeddings
# FAIL: 400 No route supports the requested capabilities for model 'hive-default'
```

## Why CI missed it

`deploy/docker/docker-compose.yml` sdk-tests-* services set only `HIVE_BASE_URL` + `HIVE_API_KEY`. `HIVE_TEST_MODEL` never exported → fallback cascade lands on `"hive-embedding-default"` default → tests pass. Any dev running locally with a mixed env trips the cascade.

## Fix

Drop the `HIVE_TEST_MODEL` fallback. Embedding tests must use embed-specific env var or the embed default:

**JS**:
```ts
const EMBEDDING_MODEL = process.env.HIVE_EMBEDDING_MODEL ?? "hive-embedding-default";
```

**Python** (`test_embeddings.py`):
```py
EMBEDDING_MODEL = os.getenv("HIVE_EMBEDDING_MODEL", "hive-embedding-default")
```

Check `conftest.py` for similar cascade and strip.

## Step-by-step

1. Branch: `fix/sdk-tests-embed-env-cascade`
2. `grep -rn 'HIVE_TEST_MODEL' packages/sdk-tests/` — find all cascade sites
3. Edit JS + Python embed test files: remove `HIVE_TEST_MODEL` from fallback chain
4. Verify chat tests still use `HIVE_TEST_MODEL` — do NOT remove it from those
5. Local test: repro command above must now pass
6. PR title: `fix(sdk-tests): decouple embedding model env from chat test env`
7. PR body: document the cascade + why it's wrong

## Constraints

- Single-concern PR. Do not combine with fixture path fix (that's a separate prompt).
- Do not touch chat-completions / responses / completions tests
- NEVER push directly to main

## Out of scope

- Adding a separate embedding alias beyond `hive-embedding-default`
- Changing anything in `apps/edge-api` or `apps/control-plane`
