# Next-session prompt index

Living index of unfinished session prompts. Consumed prompts are
archived (file deleted) — see the **Shipped** ledger below for which PR
landed each one.

## Pending

| File | Title | Priority | Notes |
|------|-------|----------|-------|
| [next-session-ui-styling.md](./next-session-ui-styling.md) | web-console UI framework + styling | P0 | Needs framework pick + designqc loop. Default rec: Tailwind v4 + shadcn/ui. |
| [next-session-visual-regression-coverage.md](./next-session-visual-regression-coverage.md) | Playwright visual regression in CI | P2 | Blocked on UI styling — baselines only stabilize after the framework swap. |

## Suggested order

1. **UI styling** (P0) — user-input gate on framework choice; biggest
   user-visible improvement.
2. **Visual regression coverage** (P2) — only after UI styling lands a
   stable baseline.

## Shipped (2026-04-24 follow-up session)

| PR  | Title | Source prompt |
|-----|-------|---------------|
| #94 | docs(planning): captured the original 6 prompts + session memory | (this index) |
| #95 | fix(sdk-tests): JS fixture path + embed env cascade | `next-session-fix-js-fixture-path.md`, `next-session-fix-embed-env-cascade.md` |
| #96 | ci(deploy): post-deploy SDK replay against staging URL | `next-session-post-deploy-sdk-replay.md` |
| #97 | docs(debug): root-cause flaky `usage.completion_tokens=0` (5.9% flake) | `next-session-flaky-usage-tokens.md` |
| #98 | ci(deploy-staging): hourly cron with diff-gate vs last successful run | (in-session decision, no prompt) |
| #99 | fix(edge-api): clamp upstream `completion_tokens=0` on non-empty output | `next-session-flaky-usage-tokens.md` (code half) |
| #101 | fix(edge-api): extend usage clamp to responses + streaming paths | `next-session-clamp-responses-streaming.md` |
| #102 | chore(litellm): pin OpenRouter backing providers | `next-session-litellm-route-pinning.md` |

## Why this decomposition

- **Reviewable diffs** — P2 trivia stays in its own PR instead of being
  hidden inside a framework swap.
- **Revert safety** — UI styling needs iteration; a visual-regression
  gate committed alongside would block styling PRs until baselines
  settle.
- **Billing risk isolation** — `usage.tokens=0` got an investigation
  doc (#97) before any code (#99 → #101); same pattern for any future
  billing-affecting change.
- **Blind-spot traceability** — each prompt documents *why CI missed it*
  so the fix closes the class of bug, not just the instance.
