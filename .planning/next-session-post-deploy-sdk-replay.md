# Next Session: Post-deploy SDK replay against staging

## Scope

Add SDK test replay + Playwright console check to `deploy-staging.yml` **after** the current 2-curl smoke, so a post-merge deploy catches env/model/routing drift before calling the deploy green.

Scope: **one PR, one workflow file change, one bash step**. Not a refactor. Not a framework.

## Current state

`.github/workflows/deploy-staging.yml` smoke (lines 131–148) only curls:
- `https://api-hive.scubed.co/health`
- `https://cp-hive.scubed.co/health`

CI `live-integration` job already runs all 3 SDK suites — against an **in-Docker** edge-api, not against the deployed staging URL. So if staging's runtime env (GH repo vars, model bindings) drifts from the docker-compose defaults, nothing catches it.

## Goal

After smoke passes on deploy-staging, run a minimum SDK replay against the real `api-hive.scubed.co` URL using the staging `HIVE_API_KEY` secret:

- `curl` `GET /v1/models` with auth → assert 4 expected aliases present
- vitest `tests/chat-completions/chat-completions.test.ts` only (fast, one round-trip)
- pytest `tests/test_health.py tests/test_models.py tests/test_embeddings.py` only (fast)
- Optional: single Playwright hit of `https://console-hive.scubed.co/auth/sign-in` asserting 200

Full suite adds cost + flakiness (OpenRouter rate limits). Minimal replay catches 80% of drift.

## Env needed in workflow

```yaml
env:
  HIVE_BASE_URL: https://api-hive.scubed.co/v1
  HIVE_API_KEY: ${{ secrets.HIVE_API_KEY }}
  HIVE_TEST_MODEL: hive-default
  HIVE_EMBEDDING_MODEL: hive-embedding-default
```

`HIVE_API_KEY` must exist as a GH secret — confirm before coding. Currently used in `ci.yml` `live-integration` and `web-e2e` jobs, so already provisioned.

## Step-by-step

1. Branch: `feat/deploy-staging-sdk-replay`
2. Read `.github/workflows/deploy-staging.yml` + `ci.yml` live-integration job for env pattern reuse
3. Add new step **after** Smoke test: `SDK replay (minimal)` that checks out repo (if not already), installs node + python, runs `npm ci` + `pip install -e .[dev]` in the two sdk-tests dirs, exports env, runs minimal vitest/pytest subset
4. Local test: `act` or push branch and trigger workflow manually via `workflow_dispatch`
5. On failure inside replay → fail the workflow red (do NOT roll back deploy — just alert)
6. PR title: `ci(deploy): replay minimal SDK suite against staging URL after smoke`
7. PR body: Before/After coverage table, list of specs included, expected runtime

## Constraints

- Runtime budget ≤ 5 min for replay (avoid OpenRouter throttle tripping prod deploy)
- NEVER push directly to main — feature branch + PR
- Do not change `packages/sdk-tests/*` test code in this PR (bug fixes tracked in separate prompts)
- Do not change smoke retry logic (already 12 × 10s)

## Files to touch

- `.github/workflows/deploy-staging.yml` (add step)
- `packages/sdk-tests/js/package.json` — may need a `test:smoke` script (subset)
- `packages/sdk-tests/python/pyproject.toml` — may add pytest marker for smoke subset

## Out of scope

- Full SDK suite post-deploy (cost + flake)
- Java replay (gradle cold-start 60s+, too slow for deploy gate)
- Visual/console screenshot (tracked separately in `next-session-visual-regression-coverage.md`)
