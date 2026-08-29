---
name: Prove Test Is Load-Bearing
description: Use when writing or reviewing a regression test or any pre-merge gate for a bug fix, especially on a money, accounting, or serialization path. A test that never actually fails against the unfixed code is not a regression guard, it's decoration, and this repo has shipped that exact shape twice on the same table. Also covers asserting on serialized wire bytes rather than the in-memory struct, and confirming a gate's scope actually includes the file you changed.
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

## Assert on the wire, not on the struct

For anything that crosses an HTTP or SSE boundary, assert on the serialized
bytes. Never on the in-memory value the handler built.

Concretely, that means one of:

- `httptest.ResponseRecorder` and then reading `rec.Body.String()` or
  `rec.Body.Bytes()`.
- The literal SSE frame text, `event:` line and `data:` payload together.
- `json.Unmarshal` into `map[string]any` and asserting on the decoded keys,
  which fails when a key is missing rather than silently zero-valuing it the
  way unmarshalling into the same typed struct would.

A struct-level assertion is structurally blind to every one of the following,
any of which ships a wrong body while the struct-level test stays green:

- A missing or misspelled `json:"..."` tag, so the field ships under a
  different name or not at all.
- A custom `MarshalJSON` that drops, renames, or reshapes the field.
- An `omitempty` that erases a legitimate zero. A required-by-spec field
  serialized as absent is not the same as a field serialized as `0`, and only
  the bytes can tell them apart.
- Any downstream layer that rewrites the body after the struct is built.

That last one is not hypothetical here. This repo has three layers that rewrite
a request or response body after the caller's struct exists, and a struct
assertion upstream of any of them cannot see what actually goes out:

- `injectMemoryBlock` (`apps/edge-api/internal/chat/memory.go`), which splices a
  recall system message into a raw request body.
- The Anthropic request translator
  (`apps/edge-api/internal/anthropic/translate_request.go`), which converts the
  Anthropic wire shape into the internal OpenAI shape.
- `buildMessagesFromInput` (`apps/edge-api/internal/inference/responses.go`),
  which converts a Responses-API `Input` plus `Instructions` into a chat
  messages array.

### The worked example (issue #1329, PR #1334)

`StreamUsage` (`apps/edge-api/internal/anthropic/types.go`) carried `omitempty`
on every field. Every `message_start` this gateway relays is emitted before any
usage-bearing upstream frame arrives, so both counts were zero, so both
vanished, so the frame went out as:

```
"usage":{}
```

An object with no required member at all. A typed client validating the
Anthropic Usage model gets a parse failure rather than a zero. No assertion on
the `StreamUsage` value could have seen it: the struct was correct, the
serialization was not. The guard that catches it reads the emitted SSE frame
text and asserts the `input_tokens` and `output_tokens` keys are present
(`apps/edge-api/internal/anthropic/usage_wire_test.go`).

## The gate's scope is not the gate's name

A gate can report green because it never examined the thing you changed. Before
trusting any pre-merge check, confirm its **scope** covers the file you touched.
Passing is not the question; having looked is.

Two shapes of this, both from real merges here:

- **A scope narrower than its name.** `scripts/test-owui-hive-frontend.sh`
  compiles Svelte components with `node owui-hive-svelte-compile-check.mjs
  lib/hive`, so its coverage stops at that one directory. A component living
  anywhere else in the vendored tree is never compiled. Three simultaneous
  mutations, including a hard parse error, left the suite reporting
  `16 passed / 208 passed / 13/13 components compiled` and exit 0 (PR #1298).
  The counts were all true. None of them counted the broken file.
- **A gate nothing ever runs.** `.claude/hooks/hooks.selfcheck.js` existed with
  real cases and no caller for its whole life, which is how a secrets scanner
  blind to every `MultiEdit` payload survived (issues #1333, #1339). PR #1337
  wired it into `ci.yml`. An unexecuted test file is not a weaker gate than a
  passing one, it is no gate. This has now happened at least twice here: the
  `make test-scripts` target had the same problem, existing with no workflow
  invoking it, which is what let the OWUI shim seed ship without a
  `tenant_billing_accounts` mapping (issue #717, `ci.yml` around line 785).

The check to actually run, phrased so it cannot be answered by "it passed":

1. Name the file you changed.
2. Read the gate's invocation, not its title, and find the path, glob, or
   directory argument it is given.
3. Confirm your file is inside that argument's reach. If it is not, the green
   result says nothing about your change, and you need either a wider gate or a
   different one.

A useful negative control: break the file you changed on purpose and rerun the
gate. If it still passes, the gate never saw it. That is the same discipline as
step 2 above, applied to the gate instead of the test.

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
