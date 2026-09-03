#!/usr/bin/env node
// Structural guard on the harness/application split in the visual-proof
// workflows (issue #1758).
//
// Both proof workflows read their YAML from one ref and check out a different
// tree to run it against: the target pull request's. The scripts the steps
// invoke by name are therefore coupled to command lines that live in the YAML,
// and taking them from the target meant a pull request branched before a
// harness change could not be proven at all. Run 33690589909 died nine seconds
// in on `unknown argument: --oauth-server`, an argument main's YAML passed to a
// script only main carried.
//
// Each workflow now re-takes a named list of scripts from its own ref. The list
// is the boundary, and the way it rots is the ordinary one: someone adds a step
// that calls a new script and does not add it to the list. That reintroduces
// the exact defect silently, on a workflow nobody runs on most pull requests.
// So this lint fails instead.
//
// Two checks per workflow:
//
//   A. Every `scripts/...` path the workflow invokes is either on that
//      workflow's harness list or on APPLICATION_SCRIPTS below, which is the
//      place to declare, with a reason, that a script must come from the target
//      tree instead.
//   B. Every path on the harness list exists, so a rename on main is a lint
//      failure here rather than a `git checkout` failure inside a proof run.
//
// Comment-only lines and the `paths:` trigger list are not invocations and are
// skipped. Follows the pattern of tools/lint-no-token-in-proof-captures.mjs: a
// small scanner with a MUST_CATCH / MUST_ALLOW self-test that runs as a
// preflight on every invocation.

import { readFileSync, existsSync } from "node:fs";
import { dirname, resolve, join } from "node:path";
import { fileURLToPath } from "node:url";
import assert from "node:assert/strict";

const HERE = dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = resolve(HERE, "..");

const WORKFLOWS = [
  ".github/workflows/chat-visual-proof.yml",
  ".github/workflows/agent-visual-proof.yml",
];

// Scripts a proof workflow may invoke from the target pull request's own tree.
// Empty today, and a new entry needs a reason: the test is whether the script
// has an interface with the workflow YAML. One called with no arguments, whose
// behaviour is pinned to data the target carries, belongs here.
// scripts/apply-migrations.sh is the live example, but it is reached through
// ci-throwaway-db.sh rather than named in the YAML, so it never appears below.
const APPLICATION_SCRIPTS = new Set();

const SCRIPT_REF = /scripts\/[A-Za-z0-9][A-Za-z0-9._-]*/g;

// The array literal the workflow's harness step declares. Returns null when the
// step is gone, which is itself a failure: a workflow with no harness list has
// silently gone back to running the target's copies.
function harnessList(text) {
  const open = text.indexOf("harness=(");
  if (open === -1) return null;
  const close = text.indexOf(")", open);
  if (close === -1) return null;
  return text
    .slice(open + "harness=(".length, close)
    .split("\n")
    .map((line) => line.trim())
    .filter((line) => line.length > 0);
}

// Everything that is not a comment, not a `paths:` trigger entry, and not the
// harness list itself. What is left is what the job actually runs.
function invocationText(text) {
  const open = text.indexOf("harness=(");
  const close = open === -1 ? -1 : text.indexOf(")", open);
  const withoutList =
    open === -1 ? text : text.slice(0, open) + text.slice(close + 1);
  return withoutList
    .split("\n")
    .filter((line) => !/^\s*#/.test(line))
    .filter((line) => !/^\s*-\s*'[^']*'\s*$/.test(line))
    .join("\n");
}

function scan(text) {
  const declared = harnessList(text);
  const problems = [];
  if (declared === null) {
    problems.push(
      "no `harness=( ... )` list found, so nothing pins the harness to this workflow's own ref (issue #1758)",
    );
    return { problems, declared: [] };
  }
  if (declared.length === 0) {
    problems.push("the harness list is empty");
  }
  const known = new Set([...declared, ...APPLICATION_SCRIPTS]);
  const invoked = new Set(invocationText(text).match(SCRIPT_REF) ?? []);
  for (const ref of [...invoked].sort()) {
    if (!known.has(ref)) {
      problems.push(
        `${ref} is invoked but is on neither the harness list nor APPLICATION_SCRIPTS. Add it to the harness list, or declare with a reason that it must come from the target tree.`,
      );
    }
  }
  return { problems, declared };
}

// --- self-test preflight -----------------------------------------------------

const MUST_CATCH = [
  [
    "a new invocation missing from the list",
    ["          harness=(", "            scripts/a.sh", "          )", "          scripts/b.sh --go"].join("\n"),
  ],
  [
    "the harness step deleted outright",
    "          scripts/a.sh --go\n",
  ],
  ["an empty harness list", "          harness=(\n          )\n"],
];

const MUST_ALLOW = [
  [
    "a comment and a paths: entry naming an unlisted script",
    [
      "      - 'scripts/b.sh'",
      "          # see scripts/c.sh for why",
      "          harness=(",
      "            scripts/a.sh",
      "          )",
      "          scripts/a.sh --go",
    ].join("\n"),
  ],
  [
    "a listed script reached through a relative path and inside a string",
    [
      "          harness=(",
      "            scripts/a.py",
      "          )",
      "          cat x | python3 ../../scripts/a.py",
      '          echo "::error::see scripts/a.py\'s main()"',
    ].join("\n"),
  ],
];

function selfTest() {
  for (const [name, fixture] of MUST_CATCH) {
    assert.ok(
      scan(fixture).problems.length > 0,
      `self-test: should have been caught: ${name}`,
    );
  }
  for (const [name, fixture] of MUST_ALLOW) {
    assert.deepEqual(
      scan(fixture).problems,
      [],
      `self-test: should have been allowed: ${name}`,
    );
  }
}

// --- main --------------------------------------------------------------------

selfTest();

let failed = false;
for (const rel of WORKFLOWS) {
  const abs = join(REPO_ROOT, rel);
  if (!existsSync(abs)) {
    console.error(`${rel}: missing. Update WORKFLOWS in ${"tools/lint-visual-proof-harness-split.mjs"} if it was renamed.`);
    failed = true;
    continue;
  }
  const { problems, declared } = scan(readFileSync(abs, "utf8"));
  for (const path of declared) {
    if (!existsSync(join(REPO_ROOT, path))) {
      problems.push(`${path} is on the harness list but does not exist`);
    }
  }
  if (problems.length > 0) {
    failed = true;
    for (const problem of problems) console.error(`${rel}: ${problem}`);
  } else {
    console.log(`${rel}: harness split OK (${declared.length} pinned scripts)`);
  }
}

if (failed) {
  console.error(
    "\nThe visual-proof workflows take these scripts from their own ref so a pull request branched before a harness change can still be proven. See issue #1758 and the comment above the harness step.",
  );
  process.exit(1);
}
