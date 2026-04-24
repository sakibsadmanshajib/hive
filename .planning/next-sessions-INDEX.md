# Next-session prompt index

Session prompts captured 2026-04-24 after staging deploy + SDK replay verification. Each prompt is self-contained and maps to **one PR** unless noted.

| File | Title | Type | Priority | Est. session size |
|------|-------|------|----------|-------------------|
| [next-session-ui-styling.md](./next-session-ui-styling.md) | web-console UI framework + styling | regression (zero CSS) | P0 | Large (framework pick + all pages) |
| [next-session-post-deploy-sdk-replay.md](./next-session-post-deploy-sdk-replay.md) | Post-deploy SDK replay against staging URL | coverage gap | P1 | Small (1 workflow file) |
| [next-session-fix-js-fixture-path.md](./next-session-fix-js-fixture-path.md) | Fix JS fixture path off-by-one + dual-branch mask | bug | P2 | Trivial (1 test file + 1 Dockerfile line) |
| [next-session-fix-embed-env-cascade.md](./next-session-fix-embed-env-cascade.md) | Drop `HIVE_TEST_MODEL` from embedding fallback chain | bug | P2 | Trivial (2 test files) |
| [next-session-flaky-usage-tokens.md](./next-session-flaky-usage-tokens.md) | Investigate `usage.*_tokens=0` intermittent | bug (billing-critical) | P1 | Medium (investigation → fix) |
| [next-session-visual-regression-coverage.md](./next-session-visual-regression-coverage.md) | Playwright visual regression + designqc in CI | coverage gap | P2 | Medium (blocked on UI styling) |

## Suggested order

1. **UI styling** (P0, biggest user-visible fix)
2. **Post-deploy SDK replay** (P1, cheap insurance against env drift) — can run in parallel with #1
3. **Flaky usage tokens** (P1, billing-critical)
4. **Embed env cascade fix + JS fixture path fix** (P2, batch as one "sdk-tests housekeeping" afternoon)
5. **Visual regression coverage** (P2, blocked on #1 having a stable baseline)

## Why this decomposition

Each bug/gap got one prompt instead of lumping into one big cleanup PR because:

- **Reviewable diffs**: P2 trivia (fixture path, env cascade) is 3-line changes; batching with a framework swap makes them invisible in review
- **Revert safety**: UI styling may need iteration; a visual-regression gate committed alongside would block styling PRs until baselines settle — hence the explicit "blocked on" note
- **Billing risk isolation**: `usage.tokens=0` can affect credit attribution; it gets its own investigation doc before any code changes
- **Blind-spot traceability**: each prompt documents *why CI missed it* so the fix closes the class of bug, not just the instance

## Source audit

Bugs + gaps surfaced during staging SDK replay (2026-04-24 ~14:00 EDT) after merging chore/single-level-subdomains. Full runbook: staging endpoints healthy (api-hive, cp-hive), 4 model aliases live, Java 100%, Python 14/14, JS 25/1-fail/1-skip. See session memory for full log.
