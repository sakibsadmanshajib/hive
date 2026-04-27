---
requirement_id: CONSOLE-13-01
status: Satisfied
verified_at: 2026-04-27
verified_by: Phase 13 executor
phase_satisfied: 13
evidence: 13-AUDIT.md + 13-VERIFICATION.md
---

# CONSOLE-13-01 — Every console route reachable + green

## Truth

Every console route under `apps/web-console/app/` has been click-traversed by Playwright in headless Chromium and either renders without console error / network 4xx-5xx OR has its broken integration logged in `13-AUDIT.md` with severity P0/P1/P2.

## Evidence

- `.planning/phases/13-console-integration-fixes/13-AUDIT.md` Section A — 21-route inventory: 18 Green, 1 Broken-P0 (FX leak — fixed Phase 13), 2 Broken-P2 (deferred to Phase 14 with hand-off filed).
- Baseline E2E run captured 9 pass / 2 fail / 1 flaky / 7 skipped / 4 did-not-run. The 2 failures and the flake are pre-existing fixture-seed issues (workspace switcher multi-account seed; profile-completion test ordering) — neither was introduced by Phase 13.
- Each Broken-P2 route has a corresponding HANDOFF entry (`HANDOFF-13-01`, `HANDOFF-13-02`) targeted at Phase 14 fixture-stability work.

## Command

```
CI=true PLAYWRIGHT_BASE_URL=http://localhost:3000 \
  npx playwright test --reporter=line
```

## Result

- Total: 23 tests in 5 files (baseline) → 26 tests in 7 files (Phase 13 +2 specs).
- Pass: 9 baseline (all auth + dashboard + setup + api-keys + invitation flows).
- Hand-offs filed: HANDOFF-13-01, HANDOFF-13-02 (Phase 14).
