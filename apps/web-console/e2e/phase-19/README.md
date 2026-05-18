# Phase 19 — Playwright E2E

Two Playwright suites live here:

* `phase-19/0[1-7]-*.spec.ts` — per-push user-flow suite. Runs in CI on
  every push and PR. Asserts audit-log rows for the happy paths.
* `phase-19/owui/` — Open WebUI direct chat + RAG suite. Runs **nightly**
  in CI and on `ci:e2e-owui`-labelled PRs. Not gated on per-push to keep
  the cost envelope predictable.

## Run locally

```bash
pnpm install
pnpm e2e:phase-19         # per-push suite
pnpm e2e:owui             # nightly suite
pnpm e2e:owui:perf        # latency budgets
```

Environment configuration lives in
[`docs/onboarding/phase-19-dev-setup.md`](../../../../docs/onboarding/phase-19-dev-setup.md).
Specs skip when their required `E2E_*` env or `HIVE_TEST_DB_URL` is unset
so the suite is safe to run against a partially provisioned dev stack.
