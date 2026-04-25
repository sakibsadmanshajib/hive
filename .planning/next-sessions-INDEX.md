# Next-session prompt index

Living index of unfinished session prompts. Consumed prompts are
archived (file deleted) — see the **Shipped** ledger below for which PR
landed each one.

## Pending

| File | Title | Priority | Notes |
|------|-------|----------|-------|

_(none — last P0 consumed by branch `feat/web-console-opennext-revamp`)_

## Suggested order

(empty)

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
| #103 | docs(planning): clean up consumed session prompts + sync wolf state | (cleanup pass) |
| TBD | feat(web-console): OpenNext + Upstash + Supabase auth fix + Claude-grade redesign | `next-session-web-console-revamp.md` |

## Why this decomposition

- **Bundling auth fix + runtime swap + redesign** is intentional — the
  three are entangled (the auth bug needs SSR cookie behavior that
  changes between Pages and Workers; the redesign needs authed pages
  reachable so `designqc` can iterate). See the revamp prompt for the
  full rationale.
- **Reviewable diffs** — kept its own PR so reviewers can sequence
  rollback if any one of the three legs misbehaves.
- **Billing risk isolation** — `usage.tokens=0` got an investigation
  doc (#97) before any code (#99 → #101); same pattern for any future
  billing-affecting change.
- **Blind-spot traceability** — each prompt documents *why CI missed it*
  so the fix closes the class of bug, not just the instance.
