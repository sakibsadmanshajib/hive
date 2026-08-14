// tools/verify-spec-wiring.test.mjs
// Fixture tests for the pure classifiers in tools/verify-spec-wiring.mjs.
// Dependency-free, node:assert only, matching the style every tools/lint-*.mjs
// self-check already uses.
//
// Case 2 below is the reproducible hole a second CodeRabbit pass found on this
// PR (tools/verify-spec-wiring.mjs review thread, line 178): a job or step `if:`
// that explicitly EXCLUDES pull_request, such as
// `github.event_name != 'pull_request'`, mentions the event name and also
// contains the quoted string `pull_request`, so the guard's own two-line check
// read it as surviving an ordinary pull request and credited its specs as
// pull-request gated. Confirmed RED against the pre-fix guard (see PR
// description / commit history: this file did not exist until the fix
// landed in the same change), GREEN after adding the explicit negation check.
//
// Run: node tools/verify-spec-wiring.test.mjs

import assert from "node:assert/strict";
import { survivesOrdinaryPullRequest } from "./verify-spec-wiring.mjs";

// --- Case 1: no condition at all always survives (an unconditional step). ---
assert.equal(survivesOrdinaryPullRequest(undefined), true, "no condition must survive");
assert.equal(survivesOrdinaryPullRequest(""), true, "empty condition must survive");

// --- Case 2: THE HOLE. A condition that explicitly EXCLUDES pull_request
// must NOT be counted as surviving an ordinary pull request. Before the fix,
// this returned true because the check only looked for the presence of
// `github.event_name` plus the quoted string `pull_request`, not the
// direction of the comparison. ---
assert.equal(
  survivesOrdinaryPullRequest("github.event_name != 'pull_request'"),
  false,
  "a step gated OUT of pull_request must not be credited as pull-request gated",
);
assert.equal(
  survivesOrdinaryPullRequest(`github.event_name != "pull_request_target"`),
  false,
  "the pull_request_target form of the same exclusion must also be caught",
);

// --- Case 3: the equality form (an ordinary positive gate) still survives. ---
assert.equal(
  survivesOrdinaryPullRequest("github.event_name == 'pull_request'"),
  true,
  "an explicit equality check on pull_request must still survive",
);

// --- Case 4: unrelated event names still fail closed (existing behaviour,
// regression guard). ---
assert.equal(
  survivesOrdinaryPullRequest("github.event_name == 'schedule'"),
  false,
  "a condition naming only a non pull-request event must not survive",
);

// --- Case 4b: a job restricted to a specific pull_request action (e.g. a
// workflow with `types: [labeled]` gating a job on `github.event.action`)
// must not survive. Found by a CodeRabbit CLI pass on this same PR: `labeled`
// excludes the ordinary opened/synchronize/reopened flow just as thoroughly
// as an explicit label-contains check does, and nothing in this repository's
// workflows exercises it today, so this is a synthetic case rather than a
// reproduced live one. ---
assert.equal(
  survivesOrdinaryPullRequest("github.event.action == 'labeled'"),
  false,
  "an action-scoped condition must not survive an ordinary pull request",
);

// --- Case 5: the label-gate case (existing behaviour, regression guard). ---
assert.equal(
  survivesOrdinaryPullRequest("contains(github.event.pull_request.labels.*.name, 'run-owui-e2e')"),
  false,
  "a label gate must not survive an ordinary pull request",
);

console.log("verify-spec-wiring.test: PASS");
