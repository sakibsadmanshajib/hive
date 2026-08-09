# Hive testing standard

What counts as a real test in this repository, and what does not.

This document exists because twelve shipped defects were each concealed by
something that read as coverage inside a green run. Every shape catalogued
below is a real instance from this repository, not a hypothetical. If you are
writing or reviewing a test, the question is never "does it pass". It is
"what would have to break for this to fail, and is that the thing I care
about".

## The one non negotiable rule

**Every test must be watched failing against the broken state before it is
trusted, and the pull request must say what was broken to prove it.**

A test that has only ever been observed passing is an unvalidated claim. It
may be asserting nothing. It may be asserting something true of both the
working and the broken product. You cannot tell by reading it, and neither
can your reviewer, because every shape in this document reads as correct.

The proof is cheap. Delete the guard, invert the condition, unmount the
route, neuter the handler, whatever the test claims to protect. Run the
suite. Watch it go red. Restore. Watch it go green. Then write down both
observations.

A pull request adding or changing a test carries a table:

| Break | Result |
| --- | --- |
| Removed the `gate.Require(FeatureRAG)` wrapper | RED, 2 subtests, 200 instead of 403 |
| Swapped Cowork for RAG on `/v1/agent/tasks` | RED, 4 subtests, both directions |

"Tests pass" is not a test plan. "I broke X and the suite went red, here is
the output" is.

If a break cannot be performed, say so and say why. An honest "I could not
red prove this one, because the failure needs a provider we do not have" is
worth more than a clean looking table that was never executed. A reviewer can
act on the first. The second is how several of the defects below shipped.

## What counts as proof of an effect

The owner's standing instruction: "Every control surface, anything that can
be clicked, dropped down, changed, or swapped, should be tested live. It has
to be tested, everything."

Rendering is not functioning. A control that renders, is enabled, has the
right label, and does nothing at all passes every assertion written about its
appearance. For a control to count as covered it must be activated for real
and must produce something observable:

| Control | What counts as proof |
| --- | --- |
| toggle, checkbox, switch | flip it, **reload the page**, read the new value back from the server, then restore it and check the restore persisted too |
| text field | the value is accepted **and** the field is bound to a named form field, or the edit itself moves the page |
| select | changing the value fires a request, navigates, changes the render, or submits as a named form field |
| internal link | navigation happens and the destination does not answer 4xx or 5xx |
| external link | the destination is reachable |
| button, tab, menu item, anything else | a request, a navigation, a download, a popup, a native dialog, or a change to the rendered output |
| disabled control | a reason the user can see, via `title`, `aria-describedby`, or a marked hint |

A request into an endpoint that answers 404, 405, 410 or 5xx is **not**
proof. That is the exact shape of the console's api-keys, spend-alerts,
budget and checkout defects fixed in #768: a perfectly observable request
into a route nobody had mounted.

Persistence is the part that is almost always missing. Before PR #808, no
spec in this repository called `page.reload()`, so no setting anywhere had a
test that it survived one. A toggle that flips in the browser and saves
nothing looks correct until the next page load.

## How coverage is counted

Coverage is **controls with a proven observable effect, over controls
enumerated from the rendered DOM**.

The denominator is enumerated by walking the live page, not by listing
controls in a file. A hand maintained list only ever covers what somebody
remembered to add to it, and it silently stops growing when the UI does. The
enumerator collects links, buttons, inputs, selects, textareas, `summary`,
anything focusable, anything carrying an interactive ARIA role, anything with
an inline handler, and any element whose computed cursor is `pointer` and
which is not already inside another control. Activating something that
reveals a menu or a tab panel re-enumerates and queues whatever appeared.

Do not invent a denominator. If a surface has no enumeration gate yet, report
the figure for what exists and say the rest is unmeasured. An unmeasured
surface reported as 100 percent is worse than no number.

A control that is deliberately inert, or whose effect genuinely cannot be
reached from a live run, goes in that surface's registry with a route, a
control key, a justification, and an owner. A `covered_elsewhere` entry must
name a spec file that exists and a marker string that file actually contains.
Empty justifications, missing owners, and entries matching nothing all fail
the gate. Deliberate inertness is a recorded decision, never a silent skip.

## Getting a live session

There is exactly one sanctioned way to sign an automated run into a deployed
environment. It is documented at `docs/live-test-auth.md` and implemented at
`apps/web-console/tests/e2e/support/live-auth.mjs`. It mints a session the
way a magic link login does, using `admin/generate_link` followed by
`POST /auth/v1/verify`, so it needs no knowledge of any password and modifies
no account. Its proof of non mutation is recorded at
`docs/proof/live-auth-helper-2026-08-08/`: the password hash is byte
identical before and after a full mint.

**Rotating the shared demo password is forbidden.** Not as a fallback, not
once, not behind a flag. The account is shared mutable state and the
control-plane resolves every bearer against GoTrue on each request, so
changing the password invalidates every session every other run currently
holds. On 2026-08-08 a scratch script named `demo_login.py` did exactly that
and broke three agents working concurrently. That script no longer exists.
Do not reconstruct its shape: find the account, overwrite its password with a
value you just generated, sign in with that value. Supplying an existing
password you were given is fine. Inventing one by rotating a shared account
is not.

The `POST` form of `/auth/v1/verify` is used deliberately. The `GET` form
answers with a redirect carrying the session in the URL fragment, which is
far easier to leak into a log, a trace, or a screenshot.

Any proof capture of a flow whose URL carries a credential, meaning
invitation accept, password reset, magic link, or OAuth callback, must have
that value redacted in both the captured text and the screenshot pixels
before commit. `npm run lint:proof-tokens` catches the text half in CI. It
cannot inspect image pixels, so masking the screenshot is on whoever captured
it. PR #578 leaked four live invitation tokens publicly this way.

## The fourteen camouflage shapes

Each of these hid a real defect in this repository. Read them as a review
checklist: for any test you are about to approve, ask which of these fourteen
it might be.

### 1. Skipped on a variable that is absent from CI

The test is correct. It never runs, and a skip is not a failure, so the run
is green.

```go
func newRLSTestPool(t *testing.T) *pgxpool.Pool {
    dsn := os.Getenv("HIVE_TEST_DB_URL")
    if dsn == "" {
        t.Skip("HIVE_TEST_DB_URL not set")   // green forever if nothing sets it
    }
```

Found repeatedly. `CATALOG_TEST_DB_URL`, `LITELLM_TEST_DB_URL` and
`PROVIDERS_TEST_DB_URL` appeared nowhere under `.github/`, so a query against
a column that does not exist on `provider_routes` shipped and never failed a
single run (#701, #708). Twelve control-plane packages carrying
`HIVE_TEST_DB_URL` gates were absent from the RLS step's explicit package
list, so they skipped in the plain step, where the variable is exported after
the step that needed it, and were never invoked in the live step (#659,
#797).

**Instead:** the gate must be a hard failure, not a skip, when the suite is
supposed to run. Pair every env gated suite with a workflow line that names
it, and treat "the variable is set but the package is not in the list" as the
same bug.

### 2. An inverted assertion, demanding behaviour that was removed

The requirement changed. The test still enforces the old one, so it either
fails forever or, worse, passes because it is asserting the absence of
something nobody emits any more.

The BD no FX and no USD display rule was a regulatory constraint enforced by
dedicated guard specs and lint. The rule was revoked (#800), and the tests
demanding USD absence became assertions about a requirement that no longer
exists. A test outliving its requirement is not harmless: it blocks the
correct change and teaches readers a rule that is no longer true.

**Instead:** when a requirement is revoked, the tests enforcing it are part
of the change, and their deletion is reviewed as carefully as their addition.
When a requirement is added, grep for a test asserting the opposite first.

### 3. An assertion that cannot distinguish success from failure

The assertion is true of every possible state, including the broken one.

```ts
// console-workspace-admin.spec.ts, before PR #808
await expect(toggle).toHaveAttribute("aria-checked", /true|false/);
```

Unanchored, and `true` and `false` are the only two values the attribute can
hold. It was true of a toggle nobody had ever clicked, and the spec never
clicked one.

```ts
// viewer-gates.test.ts, issue #795
const expectedSet = new Set<string>(actor.permissions);
const viewer = { permissions: actor.permissions };
expect(can(viewer, perm)).toBe(expectedSet.has(perm));
```

That is `Set.has(x) === Set.has(x)`. Fifty five cases, none of which can fail
for any input, in a file presented as a cross language RBAC parity matrix.

**Instead:** write the assertion, then ask what value would make it fail. If
you cannot name one, the assertion is decoration. `toBeAttached()` for a read
only claim is equally true of an enabled field; assert `toBeDisabled()` plus
the visible read only notice.

### 4. Checking the wrong quantity

The test checks a lookup table, a config value, or a request shape, where the
requirement is about the magnitude of a real outcome.

A price table's contents are not a charge. Asserting that the catalog holds
the right number per million tokens says nothing about what the ledger
actually debited, and this is where a 262x overcharge lived: every unit test
about pricing agreed with itself while the amount written to the ledger was
wrong. #743 is the same family from the other direction, a fallback that
crosses an alias boundary so a request is billed at one price and served by
another model. No test of the price table can see that.

**Instead:** on a money path, assert the magnitude of the recorded charge for
a known input, read back from the ledger. The question is never "is the price
right in the table", it is "what did this account get billed".

### 5. A weaker duplicate of the predicate, reassuring while the real path rejects

The test re-implements the rule instead of calling it, so it proves the copy
agrees with itself. Or a preflight validates with a looser predicate than the
runtime, prints a reassuring OK, and the real path then rejects.

`permissions.parity.test.ts` is aware of the tautology trap in shape 3 and
adds an `EXPECTED` table, with a comment explaining that it is defined
separately so drift surfaces as a failure. But `EXPECTED` and
`MATRIX[].granted` are two hardcoded literals in the same file, and a
separate test asserts they agree with each other. The 55 parity cases still
reduce to feeding `can()` a list and asserting membership in that list. No Go
artifact is read at test time (#795).

**Instead:** the test must consume the real artifact, generated or exported
from the authority, not a hand copy of it living beside the assertions. If
the two sides cannot share an artifact, generate one in CI and fail on drift.

### 6. Success indistinguishable from failure

The marker the test keys on is present in both outcomes.

A Copy button appears on an assistant message bubble. It also appears on the
error bubble. A spec that waits for the Copy button to confirm a completion
arrived passes just as well when the model call failed and the UI rendered an
error.

The counter example worth copying is
`apps/web-console/tests/e2e/console-workspace-admin.spec.ts:55-97`, which
names the exact strings that appear on both the 403 wall and the success
page, then asserts the one line only the wall produces.

**Instead:** identify a marker unique to the success state, and assert the
absence of the failure state's own unique marker as well. Two assertions, not
one.

### 7. A locator pinned to the broken state

The selector describes the product as it is while broken, so the test passes
today and fails the moment the product is fixed.

This appears when a spec is written against a surface with a known defect and
the author encodes the defect into the locator, for example matching the
upstream vendor's label on a white labelled surface (#773, #784). The suite
then defends the bug.

**Instead:** locators describe the requirement, not the current render. If
the correct label does not exist yet, the test should be failing, and the
honest move is a failing test tracked to an issue rather than a passing test
pinned to the wrong string.

### 8. A 403 that came from the edge, not from the gate under test

The authorization test asserts a status code. The status code was synthesized
by a bot filter, a proxy, or an edge rule, and the gate under test was never
reached. It would pass with the gate deleted.

`03-tenant-isolation.spec.ts` and `06-no-tenant-block.spec.ts` avoid this by
asserting a specific error code in the response body rather than a bare
status, which distinguishes an application refusal from an edge refusal
(#797).

**Instead:** assert the application's own error envelope, not just the
status. And pair every denial with a positive control on the same route: the
same request, authorized, must succeed. A gate that denies everyone passes a
denial only suite.

### 9. A fix keyed on a value the upstream also writes

The patch or the assertion is keyed on a version, a hash, a class name, or a
config value that the upstream project controls. It silently stops matching
at the next upgrade, and nothing reports that it stopped.

`scripts/test_owui_ui_surfaces.py` closes this by checking the rewrite table
against verbatim bytes read from the pinned image digest, so an upgrade that
moves the bytes fails the test instead of quietly disabling the patch.

**Instead:** pin the upstream artifact by digest, read the real bytes from
it, and make the drift a failure. If you must key on an upstream value, add a
test that the key still matches something.

### 10. A check a neighbouring line also satisfies

The assertion looks for a filename or a string that appears in more than one
place, so neutering the line that matters still passes.

A Dockerfile test that greps for a patch script's filename is satisfied by
the `COPY` line that puts the file in the image, whether or not the `RUN`
line that executes it survives.

`scripts/test_caddy_owui_blocklist.py:161-195` closes this by reading the
Dockerfile as folded logical instructions and asserting the `RUN` that
executes the patch, explicitly because a filename substring is satisfied by
the `COPY` alone.

**Instead:** assert the instruction, not the token. Ask which other line in
the file would also satisfy this grep.

### 11. A patch step that succeeds on zero matches

`sed`, `patch`, a codemod, or a rewrite rule that matches nothing exits 0.
The build is green and the change was never applied.

**Instead:** the patch step counts its matches and fails on zero, and a test
exercises that zero match failure path so the counting itself is proven.
`scripts/test_owui_ui_surfaces.py` does exactly this.

### 12. The gate is tested, its attachment to the route is not

The middleware has thorough tests. Every one of them constructs the
middleware and calls it directly. That proves the middleware works. It proves
nothing about whether it is on the route, which is the only thing that was
broken.

All three of the RAG gate, the Cowork gate and the platform admin gate were
applied inline inside `main()`, where no test could reach the wiring.
Deleting any of them left 25 edge-api packages and 52 control-plane packages
green (#793). Separately, `authz.RequireOwnTenant` had six passing tests and
zero production callers.

**Instead:** extract route registration into a named function, drive it
through a real mux in a table test, assert an outcome per path and per
caller, and pair every denial with a positive control so a gate that denied
everyone cannot pass as a success.
`TestRegisterMediaFileBatchRoutesAppliesVoiceGateToAudioOnly` at
`apps/edge-api/cmd/server/main_test.go:422` is the model.

### 13. A guard whose deletion leaves the suite green

The general case of shape 12, and the single most useful audit you can run.
Pick any protection. Delete it. Run everything. If the suite is still green,
that protection is undefended regardless of how many tests mention it.

Confirmed on the credit grants money path: the platform admin gate on
`/v1/admin/credit-grants` could be deleted and the whole control-plane suite
still exited 0 across 52 packages (#793). Confirmed again on the workspace
switcher: deleting the `onChange` handler that makes it do anything at all
left all 423 web-console unit tests passing, because the only test on the
component read its source text for the `"use client"` directive (#794).

**Instead:** this is the red proof from the top of this document, applied as
an audit rather than as a step in writing one test. Run it against anything
that guards money, authorization, or data loss.

### 14. A spec file that no workflow runs

Nothing skips. There is no variable to blame. The file simply is never passed
to any runner, and an uninvoked file emits no signal at all.

`web-e2e` invokes Playwright by explicit file path and names four of the
thirty three spec files in the tree. The rest are collected by a project's
`testDir`, so `npx playwright test` locally runs the lot, which is why this is
invisible to anyone working on the specs. **Eighteen of thirty three spec
files have never run in CI**, including `billing-fx-zero-leak.spec.ts`,
written to enforce what was at the time a regulatory constraint (#813).

**Instead:** run the directory, not a file list. Where a file list is
unavoidable, add a guard that fails when a spec exists that no workflow runs,
with an allowlist whose every entry names a justification, an owner and a
tracking issue. `apps/web-console/scripts/verify-spec-wiring.mjs` is that
guard.

#### Resolve wiring by asking the runner, never by matching filenames

The first version of that guard matched spec filenames against the text of
`.github/`. It was wrong in **both** directions at once, which is worth
recording because the mistake is inviting and it looks like it works:

- **False positives.** `openai-sdk.spec.ts` and `performance/ttfb.spec.ts`
  were counted as wired because a workflow **comment** mentions them. A
  comment runs nothing.
- **False negatives.** The nine `owui/NN-*.spec.ts` and the two
  `owui/performance/*.spec.ts` were counted as dark, when `owui-nightly.yml`
  runs all eleven through `npm run e2e:owui` and `e2e:owui:perf`. Those
  select by `--project`, so no filename ever appears in the workflow.

It reported 6 wired and 27 dark. The truth is 15 and 18. A guard that
miscounts in both directions is worse than no guard, because it manufactures
confidence in a number nobody checked.

The root cause is structural: workflows select tests by project and by
config, so any filename based detection is measuring something the runner does
not use. This is camouflage shape 5, a weaker duplicate of the real predicate,
appearing inside the tooling built to catch camouflage.

The corrected guard runs `playwright test --list --reporter=json` for every
invocation a workflow actually makes, and unions the collected files. Three
details are load bearing:

1. `spec.file` in that report is relative to `config.rootDir`, which differs
   per config. Resolve against it or the owui specs silently fail to match.
2. The owui config chooses its `testMatch` from credential environment
   variables and collects **zero** files without them. The guard supplies
   placeholders, because the question is whether the workflow runs the spec
   when it has its secrets.
3. An invocation that collects zero files is a **failure**, not an empty
   result. That is the phase-19 `testMatch` bug, and treating it as "no specs
   here" is how a broken selector reports success.

A collection guard certifies that files are found, not that they run. Phase
19 is dark twice over: no workflow runs the project, `ci.yml` only collects
it, and `E2E_TENANT_B_ID`, `E2E_EXPIRED_JWT` and `E2E_ORPHAN_JWT` appear in
zero workflow files, so even if it were invoked, four of its specs would
`test.skip` on their first line. Both halves have to be closed.
