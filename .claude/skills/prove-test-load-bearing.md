---
name: Prove Test Is Load-Bearing
description: Use when writing or reviewing a regression test for a bug fix, especially on a money or accounting path. A test that never actually fails against the unfixed code is not a regression guard, it's decoration — this repo has shipped that exact shape twice on the same table.
---

# Prove Test Is Load-Bearing

## Why this exists

`public.api_key_usage_rollups` shipped two fake-backed "regression guards" in
back-to-back days:

- **PR #1173**: `FinalizeReservation` hardcoded `0` for input/output tokens
  instead of forwarding the real metered counts. The existing test's mock for
  `RecordUsageFinalization` **discarded its own arguments before this PR** —
  it accepted whatever was passed and asserted nothing about the value. No
  test could ever have caught the bug, regardless of how wrong the code was,
  because nothing in the suite ever looked at what got recorded.
- **PR #1204**: cache read/write tokens written as zero into the same table,
  one day later, same underlying shape — a code path with real data available
  in scope that never got threaded into the write it should have driven.

A test that cannot go red is not a regression guard. It is inert code that
looks like one, and it will pass over the next bug in the same shape too.

## The discipline: break it, watch it fail, fix it, watch it pass

Before trusting any new or modified test as a real guard:

1. **Write the test against the fix you intend to make**, asserting the
   actual value, not just "no error" or "was called." For a mock/stub, that
   means asserting on the *captured argument*, not merely that the mock was
   invoked.
2. **Confirm it fails against the unfixed code**, and read the failure
   message. It must name the wrong value (`expected 420, got 0`), not just
   "test panicked" or "undefined is not a function" — a compile error or
   nil-pointer panic proves the test runs, not that it exercises the bug.
3. **Apply the real fix.**
4. **Confirm it passes**, and that the rest of the touched package still
   passes.
5. **Paste both outputs** (RED then GREEN) in the PR body under their own
   headings, verbatim, not paraphrased. This is already this repo's house
   style (see PR #1173's body for the pattern) — the point of this skill is
   to make step 2 actually meaningful, not just present.

Skipping step 2 is exactly how #1173's original mock existed for as long as
it did: the test suite was green the entire time the bug was live.

## The specific anti-pattern to check for

Before trusting an existing test as a guard for the behavior you're touching,
grep the mock/stub it depends on for what it does with its own arguments.

```bash
grep -n "func.*Record\|func.*Finalize" apps/control-plane/internal/accounting/*_test.go
```

If a mock's method body ignores the arguments it was called with (returns a
canned value, appends to a call-count, but never stores or asserts on the
payload), any test relying on it for correctness is decoration. Either the
mock needs to capture and assert on its arguments, or the test needs to hit
the real code path (against a bootstrapped test DB) instead of the mock.

## Exact commands for this repo (Go)

```bash
cd deploy/docker && docker compose --profile tools run toolchain \
  "cd /workspace && go test ./apps/control-plane/internal/accounting/... -run TestFinalizeReservationUsageRollupCarriesMeteredTokens -v -count=1"
```

Run it once against the pre-fix code (`git stash` the fix, or check out the
parent commit) to capture RED, then again after applying the fix for GREEN.
Full package run afterward to confirm no collateral breakage:

```bash
cd deploy/docker && docker compose --profile tools run toolchain \
  "cd /workspace && go test ./apps/control-plane/... -count=1 -short"
```

## Exact commands for this repo (TypeScript)

```bash
cd deploy/docker && docker compose run --build web-console npm run test:unit -- -t "<test name>"
```

Same RED-then-GREEN sequence: stash the fix, confirm the specific test fails
with the wrong value named in the assertion output, restore the fix, confirm
it passes.

## Related

`.wolf/decisions.md` D-051 (standing test rule: new control needs a test,
behavior change needs a test change, removal needs a test removed; 100%
interaction coverage on web-console surfaces).
