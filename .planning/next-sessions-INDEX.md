# Next-session prompt index

Originally captured 2026-04-24 after staging deploy + SDK replay
verification. Updated after the 2026-04-24 follow-up session shipped
PRs #94 / #95 / #96 / #97 / #98 / #99 (six PRs merged).

## Status

| File | Title | Priority | Status |
|------|-------|----------|--------|
| [next-session-ui-styling.md](./next-session-ui-styling.md) | web-console UI framework + styling | P0 | **Pending** — needs framework pick + designqc loop |
| [next-session-post-deploy-sdk-replay.md](./next-session-post-deploy-sdk-replay.md) | Post-deploy SDK replay against staging URL | P1 | **Done** — PR #96 merged |
| [next-session-fix-js-fixture-path.md](./next-session-fix-js-fixture-path.md) | Fix JS fixture path + dual-branch mask | P2 | **Done** — PR #95 merged |
| [next-session-fix-embed-env-cascade.md](./next-session-fix-embed-env-cascade.md) | Drop `HIVE_TEST_MODEL` from embed fallback | P2 | **Done** — PR #95 merged |
| [next-session-flaky-usage-tokens.md](./next-session-flaky-usage-tokens.md) | Investigate `usage.*_tokens=0` | P1 | **Done** — investigation PR #97 + clamp fix PR #99 (chat + legacy completions only) |
| [next-session-visual-regression-coverage.md](./next-session-visual-regression-coverage.md) | Playwright visual regression in CI | P2 | **Pending** — blocked on UI styling |
| [next-session-clamp-responses-streaming.md](./next-session-clamp-responses-streaming.md) | Extend usage clamp to responses + streaming | P1 | **New** — follow-up to #99, billing-critical |
| [next-session-litellm-route-pinning.md](./next-session-litellm-route-pinning.md) | Pin LiteLLM backing providers for `hive-default` | P2 | **New** — follow-up to #97 variance findings |

## Suggested order (next 3 sessions)

1. **Extend clamp to responses + streaming** (P1) — closes the remaining
   billing-leak surface left after #99. Small, isolated PR.
2. **Pin LiteLLM `hive-default` providers** (P2) — preventive fix that
   makes the clamp rarely fire. Independent of #1.
3. **UI styling** (P0) — large, iterative; user-input gate on framework
   choice (default recommendation: Tailwind v4 + shadcn/ui).
4. **Visual regression coverage** (P2) — only after UI styling has a
   stable baseline.

## What landed in the 2026-04-24 follow-up session

- PR #94 — docs(planning): captured the original 6 prompts + session memory
  (this index)
- PR #95 — fix(sdk-tests): fixture path + embed env cascade (P2 batch)
- PR #96 — ci(deploy): post-deploy SDK replay (P1)
- PR #97 — docs(debug): root-cause flaky `usage.completion_tokens=0`
  (P1, with measured 5.9% flake rate from a 17-sample staging burst)
- PR #98 — ci(deploy-staging): hourly cron with diff-gate (replaces 6h
  cron; only deploys when watched paths changed since last successful run)
- PR #99 — fix(edge-api): clamp upstream `completion_tokens=0` on
  non-empty output (chat + legacy completions; reasoning tokens preserved;
  12-test suite)

## Why this decomposition

- **Reviewable diffs** — P2 trivia (fixture path, env cascade) is 3-line
  changes; batching with a framework swap would make them invisible in
  review.
- **Revert safety** — UI styling may need iteration; visual-regression
  gate committed alongside would block styling PRs until baselines settle.
- **Billing risk isolation** — `usage.tokens=0` got its own investigation
  doc (#97) before any code change (#99); next clamp extension keeps the
  pattern (responses + streaming as a single follow-up PR).
- **Blind-spot traceability** — each prompt documents *why CI missed it*
  so the fix closes the class of bug, not just the instance.

## Source audit

Original bugs + gaps surfaced during staging SDK replay 2026-04-24
~14:00 EDT after merging `chore/single-level-subdomains`. Follow-up
prompts (`clamp-responses-streaming`, `litellm-route-pinning`) added
2026-04-24 after the burst captured 5.9% flake rate against
`api-hive.scubed.co/v1/chat/completions`.
