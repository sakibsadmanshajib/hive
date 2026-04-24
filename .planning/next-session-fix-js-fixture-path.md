# Next Session: Fix JS fixture path off-by-one + dual-branch masking

## Scope

Trivial bug fix PR. One file. One test re-run.

## Bug

`packages/sdk-tests/js/tests/models/list-models.test.ts:10-17`

```ts
function loadGolden(name: string): unknown {
  // In Docker container, fixtures are at /fixtures/golden/
  // Locally, they are relative to the test file
  const containerPath = resolve("/fixtures/golden", name);
  const localPath = resolve(__dirname, "../../../../fixtures/golden", name);
  const filePath = existsSync(containerPath) ? containerPath : localPath;
  return JSON.parse(readFileSync(filePath, "utf-8"));
}
```

Two problems:

1. **Path off-by-one**: `../../../../fixtures/golden` from `packages/sdk-tests/js/tests/models/` resolves to `packages/fixtures/golden` — that directory does not exist. Actual fixtures at `packages/sdk-tests/fixtures/golden/`. Correct relative: `../../../fixtures/golden` (three levels up, not four).

2. **Dual-branch masking**: `existsSync(containerPath) ? containerPath : localPath` means local branch is never exercised in Docker/CI (where `/fixtures/golden/` exists). The local path bug went undetected for the entire lifetime of this test.

## Why CI missed it

`Dockerfile.sdk-tests-js` copies fixtures to `/fixtures/` inside the container → `containerPath` always wins in CI. Local dev running `npm test` on host hits the broken `localPath`. Bug only surfaces outside Docker.

## Goal

Single resolve path that works in both environments. Preferred: env var override + single default.

## Fix options (pick one)

**Option A — single path relative to package root** (preferred):
```ts
const localPath = resolve(__dirname, "../../../fixtures/golden", name);
```
Keep Docker branch as override for CI. Both branches now correct.

**Option B — env var**:
```ts
const fixturesDir = process.env.HIVE_FIXTURES_DIR ?? resolve(__dirname, "../../../fixtures/golden");
return JSON.parse(readFileSync(resolve(fixturesDir, name), "utf-8"));
```
Docker sets `HIVE_FIXTURES_DIR=/fixtures/golden` in Dockerfile. Simpler, one source of truth.

Recommendation: **Option B**. Fewer branches, testable.

## Step-by-step

1. Branch: `fix/sdk-tests-js-fixture-path`
2. Edit `packages/sdk-tests/js/tests/models/list-models.test.ts` line 10-17
3. Edit `deploy/docker/Dockerfile.sdk-tests-js` — add `ENV HIVE_FIXTURES_DIR=/fixtures/golden`
4. Run local: `cd packages/sdk-tests/js && npm test -- tests/models` — must pass w/o Docker
5. Run Docker: `cd deploy/docker && docker compose --profile test run --rm sdk-tests-js npx vitest run tests/models` — must pass inside
6. PR title: `fix(sdk-tests-js): resolve golden fixture path outside Docker`
7. PR body: explain the off-by-one + dual-branch mask

## Verification

Local + Docker both must pass. Touch no other tests.

## Constraints

- Single-file edit (test file) + 1-line Dockerfile tweak
- No dependency changes
- NEVER push directly to main
